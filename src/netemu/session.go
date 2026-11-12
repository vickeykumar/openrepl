//go:build linux

package netemu

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// WorkspaceID is the short, name-safe identifier of a workspace folder: every REPL of the workspace (its
// terminals and forks) gets the same one, so that they share one network.
func WorkspaceID(workspace string) string {
	h := sha1.Sum([]byte(workspace))
	return hex.EncodeToString(h[:6])
}

var debug = os.Getenv("OPENREPL_NETEMU_DEBUG") != ""

// maxSockets bounds what a session remembers about its sockets; a session that has more forgets them and
// falls back to the defaults (an IPv4 socket bound to the loopback).
const maxSockets = 1 << 16

type sockInfo struct {
	family uint16 // the family the REPL asked for
	any    bool   // bound to the wildcard
}

// a connection that was accepted for a REPL whose call was cancelled before it got the descriptor: the next
// accept on that listener returns it
type stashed struct {
	fd    int
	peer  string
	named bool
}

// Session answers the notifications of one REPL and of everything it starts.
type Session struct {
	id    string
	lfd   int
	mu    sync.Mutex
	socks map[uint64]*sockInfo
	stash map[uint64][]stashed
	tgids map[uint32]uint32
	trace []string // the latest calls, for the log when something unexpected happens
}

// Attach serves the REPL (process pid) that holds the other end of sock, in the background: it waits for the
// helper to announce the notification descriptor, takes a copy, and serves until the REPL and everything it
// started are gone. It takes ownership of sock.
func Attach(sock *os.File, workspace string, pid int) {
	go func() {
		if err := attach(sock, workspace, pid, nil); err != nil {
			log.Printf("netemu: a REPL's network could not be set up: %v", err)
		}
	}()
}

// attach is Attach in the foreground. ready, if not nil, is told (once) whether the session started, and why not.
func attach(sock *os.File, workspace string, pid int, ready chan<- error) error {
	lfd, err := fetchFD(sock, pid)
	sock.Close()
	if ready != nil {
		ready <- err
	}
	if err != nil {
		return err
	}
	newSession(WorkspaceID(workspace), lfd).serve()
	return nil
}

func newSession(id string, lfd int) *Session {
	return &Session{id: id, lfd: lfd, socks: map[uint64]*sockInfo{}, stash: map[uint64][]stashed{}, tgids: map[uint32]uint32{}}
}

func (s *Session) serve() {
	// Polling, because ENOENT from a receive does not mean the end: it is what a call that was cancelled by a
	// signal while it was being received leaves behind. The end is POLLHUP: no task uses the filter any more.
	syscall.SetNonblock(s.lfd, true)
	type pollfd struct {
		fd      int32
		events  int16
		revents int16
	}
	for {
		p := pollfd{fd: int32(s.lfd), events: 1} // POLLIN
		ts := syscall.Timespec{Sec: 1}
		syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&p)), 1, uintptr(unsafe.Pointer(&ts)), 0, 0, 0)
		if p.revents&(0x10|0x08|0x20) != 0 && p.revents&1 == 0 { // POLLHUP, POLLERR, POLLNVAL
			break
		}
		if p.revents&1 == 0 {
			continue
		}
		var n notif
		switch e := ioctl(s.lfd, ioctlNotifRecv, unsafe.Pointer(&n)); e {
		case 0:
			go s.answer(&n)
		case syscall.ENOENT, syscall.EINTR, syscall.EAGAIN:
		default:
			if debug {
				log.Printf("netemu[%s]: the notification descriptor failed: %v", s.id, e)
			}
			s.finish()
			return
		}
	}
	s.finish()
}

func (s *Session) finish() {
	syscall.Close(s.lfd)
	s.mu.Lock()
	for _, l := range s.stash {
		for _, st := range l {
			syscall.Close(st.fd)
		}
	}
	s.stash = nil
	s.mu.Unlock()
}

func (s *Session) note(line string) {
	s.mu.Lock()
	if len(s.trace) >= 40 {
		s.trace = s.trace[1:]
	}
	s.trace = append(s.trace, line)
	s.mu.Unlock()
}

func (s *Session) valid(id uint64) bool {
	return ioctl(s.lfd, ioctlNotifIDValid, unsafe.Pointer(&id)) == 0
}

type result struct {
	val     int64
	errno   syscall.Errno
	cont    bool // let the kernel carry the call out as the REPL made it
	replied bool // nothing more is to be sent: the answer went with the descriptor (ADDFD), or the call is not ours to answer any more
	gone    bool // the REPL's call was cancelled or answered meanwhile (a signal interrupted it): what was made for it is to be undone
}

func fail(e syscall.Errno) result { return result{errno: e} }
func ok(v int64) result           { return result{val: v} }
func cont() result                { return result{cont: true} }

func (s *Session) answer(n *notif) {
	r := s.handle(n)
	s.note(fmt.Sprintf("tid %d call %d args %x -> %+v", n.pid, n.nr, n.args[:4], r))
	if debug {
		log.Printf("netemu[%s]: call %d args %x -> %+v", s.id, n.nr, n.args[:4], r)
	}
	if r.replied {
		return
	}
	resp := notifResp{id: n.id}
	switch {
	case r.cont:
		resp.flags = notifFlagContinue
	case r.errno != 0:
		resp.errno = -int32(r.errno)
	default:
		resp.val = r.val
	}
	if debug && r.errno != 0 {
		log.Printf("netemu[%s]: call %d %x -> %v", s.id, n.nr, n.args[:4], r.errno)
	}
	ioctl(s.lfd, ioctlNotifSend, unsafe.Pointer(&resp))
}

// ---- the REPL's memory and descriptors -----------------------------------------------------------

// tgid: the notification names a THREAD; pidfd_open wants the process.
func (s *Session) tgid(tid uint32) uint32 {
	s.mu.Lock()
	if v, ok := s.tgids[tid]; ok {
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	v := tid
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(int(tid)) + "/status"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "Tgid:") {
				if x, err := strconv.Atoi(strings.TrimSpace(line[5:])); err == nil {
					v = uint32(x)
				}
				break
			}
		}
	}
	s.mu.Lock()
	if len(s.tgids) > 4096 {
		s.tgids = map[uint32]uint32{}
	}
	s.tgids[tid] = v
	s.mu.Unlock()
	return v
}

func readMem(pid uint32, addr uint64, n int) ([]byte, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(int(pid)) + "/mem")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	_, err = f.ReadAt(b, int64(addr))
	return b, err
}

// putMem writes to the REPL's memory for the call n, and only while that call is still the one waiting. A signal can
// cancel the call (a Go program is sent many, to preempt its goroutines); the thread then goes on, and its stack and
// heap hold something else at those addresses. An answer written there afterwards corrupts them, and the REPL
// crashes much later, somewhere else (the stress test of 1500 short connections did, on a return address). The check
// comes right before the write, as the kernel's documentation asks of anything that touches the target's memory on
// behalf of a notification.
func (s *Session) putMem(n *notif, addr uint64, b []byte) error {
	if !s.valid(n.id) {
		return syscall.ENOENT
	}
	return writeMem(n.pid, addr, b)
}

func writeMem(pid uint32, addr uint64, b []byte) error {
	f, err := os.OpenFile("/proc/"+strconv.Itoa(int(pid))+"/mem", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteAt(b, int64(addr))
	return err
}

// theirFd gives this process a duplicate of the REPL's descriptor (the same open file).
func (s *Session) theirFd(pid uint32, fd uint64) (int, syscall.Errno) {
	pfd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(s.tgid(pid)), 0, 0)
	if e != 0 {
		return -1, e
	}
	defer syscall.Close(int(pfd))
	nfd, _, e := syscall.Syscall(sysPidfdGetfd, pfd, uintptr(fd), 0)
	if e != 0 {
		return -1, e
	}
	return int(nfd), 0
}

func inodeOf(fd int) uint64 {
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil {
		return 0
	}
	return st.Ino
}

func (s *Session) owned(fd int) (sockInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.socks[inodeOf(fd)]; ok {
		return *i, true
	}
	return sockInfo{family: afInet}, false
}

func (s *Session) setInfo(fd int, i sockInfo) {
	s.mu.Lock()
	if len(s.socks) >= maxSockets {
		s.socks = map[uint64]*sockInfo{}
	}
	s.socks[inodeOf(fd)] = &i
	s.mu.Unlock()
}

func isNonblock(fd int) bool {
	fl, _, _ := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	return fl&syscall.O_NONBLOCK != 0
}

func sockType(fd int) int {
	t, _ := syscall.GetsockoptInt(fd, solSocket, syscall.SO_TYPE)
	return t
}

func protoOf(fd int) string {
	if sockType(fd) == syscall.SOCK_DGRAM {
		return "u"
	}
	return "t"
}

func errnoOf(err error) syscall.Errno {
	if e, ok := err.(syscall.Errno); ok {
		return e
	}
	return syscall.EIO
}

// ---- names the kernel wrote, as raw bytes (Go's wrappers cannot read an abstract name back) ----------

func rawName(buf []byte, n int) (string, bool) {
	if n <= 2 || n > len(buf) {
		return "", false
	}
	path := buf[2:n]
	if path[0] == 0 {
		return "@" + string(path[1:]), true
	}
	for i, c := range path {
		if c == 0 {
			return string(path[:i]), true
		}
	}
	return string(path), true
}

func rawGetname(fd int, peer bool) (string, bool, error) {
	var buf [128]byte
	l := uint32(len(buf))
	nr := uintptr(syscall.SYS_GETSOCKNAME)
	if peer {
		nr = uintptr(syscall.SYS_GETPEERNAME)
	}
	if _, _, e := syscall.Syscall(nr, uintptr(fd), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&l))); e != 0 {
		return "", false, e
	}
	name, named := rawName(buf[:], int(l))
	return name, named, nil
}

func rawAccept4(fd, flags int) (int, string, bool, error) {
	var buf [128]byte
	l := uint32(len(buf))
	r, _, e := syscall.Syscall6(syscall.SYS_ACCEPT4, uintptr(fd), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&l)), uintptr(flags), 0, 0)
	if e != 0 {
		return -1, "", false, e
	}
	name, named := rawName(buf[:], int(l))
	return int(r), name, named, nil
}

func rawRecvfrom(fd int, p []byte, flags int) (int, string, bool, error) {
	var buf [128]byte
	l := uint32(len(buf))
	var z byte
	base := unsafe.Pointer(&z)
	if len(p) > 0 {
		base = unsafe.Pointer(&p[0])
	}
	r, _, e := syscall.Syscall6(syscall.SYS_RECVFROM, uintptr(fd), uintptr(base), uintptr(len(p)), uintptr(flags), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&l)))
	if e != 0 {
		return 0, "", false, e
	}
	name, named := rawName(buf[:], int(l))
	return int(r), name, named, nil
}

func (s *Session) name(proto string, port uint16) string { return Name(s.id, proto, port) }

// ensureBound gives an unbound socket an ephemeral port, as the kernel does at connect or sendto.
func (s *Session) ensureBound(fd int) syscall.Errno {
	if _, named, err := rawGetname(fd, false); err == nil && named {
		return 0
	}
	return s.bindEphemeral(fd)
}

func (s *Session) bindEphemeral(fd int) syscall.Errno {
	proto := protoOf(fd)
	for i := 0; i < 200; i++ {
		p := uint16(ephemeralFirst + rand.Intn(ephemeralLast-ephemeralFirst))
		err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: s.name(proto, p)})
		if err == nil {
			return 0
		}
		if err != syscall.EADDRINUSE {
			return errnoOf(err)
		}
	}
	return syscall.EADDRINUSE
}

// wait polls fd in short turns while it blocks in the REPL, and gives up when the REPL's call was cancelled.
func (s *Session) wait(fd int, events int16, id uint64) bool {
	type pollfd struct {
		fd      int32
		events  int16
		revents int16
	}
	for {
		p := pollfd{fd: int32(fd), events: events}
		ts := syscall.Timespec{Nsec: int64(100 * time.Millisecond)}
		r, _, _ := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&p)), 1, uintptr(unsafe.Pointer(&ts)), 0, 0, 0)
		if r > 0 {
			return true
		}
		if !s.valid(id) {
			return false
		}
	}
}

// putAddr writes sa to the REPL's addr / addrlen pair as the kernel does: truncated, with the full size reported.
func (s *Session) putAddr(n *notif, addrPtr, lenPtr uint64, sa []byte) syscall.Errno {
	lb, err := readMem(n.pid, lenPtr, 4)
	if err != nil {
		return syscall.EFAULT
	}
	cut := len(sa)
	if have := int(int32(binary.LittleEndian.Uint32(lb))); have < cut {
		cut = have
	}
	if cut < 0 {
		cut = 0
	}
	if cut > 0 && s.putMem(n, addrPtr, sa[:cut]) != nil {
		return syscall.EFAULT
	}
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(len(sa)))
	if s.putMem(n, lenPtr, out) != nil {
		return syscall.EFAULT
	}
	return 0
}

// addrOf is the sockaddr a REPL sees for a socket whose kernel-side name is name.
func (s *Session) addrOf(i sockInfo, name string, remote bool) ([]byte, bool) {
	port, ok := PortOf(s.id, name)
	if !ok {
		return nil, false
	}
	return Build(i.family, port, i.any && !remote, remote), true
}

// inject puts fd into the REPL as a new descriptor and answers its call with the number.
func (s *Session) inject(n *notif, fd int, cloexec bool) result {
	a := addFD{id: n.id, flags: addfdSend, srcfd: uint32(fd)}
	if cloexec {
		a.newfdFlg = syscall.O_CLOEXEC
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s.lfd), ioctlNotifAddFD, uintptr(unsafe.Pointer(&a))); e != 0 {
		if e == syscall.ENOENT || e == syscall.EINPROGRESS {
			// the notification is not waiting for an answer from us any more: a signal interrupted the REPL's
			// call (it is made again, and then notified again). Answering would be wrong.
			return result{replied: true, gone: true}
		}
		return fail(e)
	}
	return result{replied: true}
}

// ---- the calls ----------------------------------------------------------------------------------------

func (s *Session) handle(n *notif) result {
	switch int(n.nr) {
	case syscall.SYS_SOCKET:
		return s.hSocket(n)
	case syscall.SYS_BIND, syscall.SYS_CONNECT:
		return s.hBindConnect(n)
	case syscall.SYS_GETSOCKNAME, syscall.SYS_GETPEERNAME:
		return s.hGetName(n)
	case syscall.SYS_ACCEPT, syscall.SYS_ACCEPT4:
		return s.hAccept(n)
	case syscall.SYS_SENDTO:
		return s.hSendto(n)
	case syscall.SYS_RECVFROM:
		return s.hRecvfrom(n)
	case syscall.SYS_SENDMSG:
		return s.hSendmsg(n)
	case syscall.SYS_RECVMSG:
		return s.hRecvmsg(n)
	case sysSendmmsg, sysRecvmmsg:
		return s.hMmsg(n)
	case syscall.SYS_SETSOCKOPT, syscall.SYS_GETSOCKOPT:
		return s.hSockopt(n)
	}
	return fail(syscall.ENOSYS)
}

// own returns the REPL's descriptor as ours, and whether it is one of the emulated sockets.
func (s *Session) own(n *notif, fdArg uint64) (int, sockInfo, bool, syscall.Errno) {
	fd, e := s.theirFd(n.pid, fdArg)
	if e != 0 {
		return -1, sockInfo{}, false, e
	}
	i, mine := s.owned(fd)
	return fd, i, mine, 0
}

func (s *Session) hSocket(n *notif) result {
	dom, typ, proto := int(n.args[0]), int(n.args[1]), int(n.args[2])
	base := typ &^ (syscall.SOCK_NONBLOCK | syscall.SOCK_CLOEXEC)
	switch dom {
	case afUnix:
		// A unix socket cannot reach another workspace unless it is given a name, and no call that names an
		// AF_UNIX address is carried out (hBindConnect, hSendto), with one exception: a datagram socket could
		// be sent to a name by a call this filter does not see an address in. So there is none.
		if base == syscall.SOCK_DGRAM {
			return fail(syscall.EACCES)
		}
		return cont()
	case afInet, afInet6:
	default:
		return fail(syscall.EAFNOSUPPORT)
	}
	switch {
	case base == syscall.SOCK_STREAM && (proto == 0 || proto == syscall.IPPROTO_TCP):
	case base == syscall.SOCK_DGRAM && (proto == 0 || proto == syscall.IPPROTO_UDP):
	case base == syscall.SOCK_STREAM || base == syscall.SOCK_DGRAM:
		return fail(syscall.EPROTONOSUPPORT)
	default:
		return fail(syscall.ESOCKTNOSUPPORT) // raw, packet, seqpacket
	}
	fd, err := syscall.Socket(afUnix, base|(typ&syscall.SOCK_NONBLOCK)|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return fail(errnoOf(err))
	}
	defer syscall.Close(fd)
	s.setInfo(fd, sockInfo{family: uint16(dom)})
	return s.inject(n, fd, typ&syscall.SOCK_CLOEXEC != 0)
}

func (s *Session) hBindConnect(n *notif) result {
	fd, i, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	ln := int(n.args[2])
	if ln < 2 || ln > 128 {
		return fail(syscall.EINVAL)
	}
	sa, err := readMem(n.pid, n.args[1], ln)
	if err != nil || !s.valid(n.id) {
		return fail(syscall.ESRCH)
	}
	if binary.LittleEndian.Uint16(sa[:2]) == afUnix {
		return fail(syscall.EACCES) // named unix sockets are not available (see hSocket)
	}
	a, e := Parse(sa)
	if e != 0 {
		return fail(e)
	}
	if !mine {
		if true {
			s.mu.Lock()
			t := strings.Join(s.trace, "\n  ")
			s.mu.Unlock()
			log.Printf("netemu[%s]: UNEXPECTED: latest calls:\n  %s", s.id, t)
			var st syscall.Stat_t
			syscall.Fstat(fd, &st)
			log.Printf("netemu[%s]: connect/bind on a socket that is not ours: tid %d fd %d inode %d mode %o type %d", s.id, n.pid, n.args[0], st.Ino, st.Mode, sockType(fd))
		}
		return fail(syscall.EAFNOSUPPORT)
	}
	proto := protoOf(fd)
	if int(n.nr) == syscall.SYS_BIND {
		if a.Port == 0 {
			if e := s.bindEphemeral(fd); e != 0 {
				return fail(e)
			}
		} else if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: s.name(proto, a.Port)}); err != nil {
			return fail(errnoOf(err))
		}
		i.any = a.Any
		s.setInfo(fd, i)
		return ok(0)
	}
	if e := s.ensureBound(fd); e != 0 {
		return fail(e)
	}
	target := &syscall.SockaddrUnix{Name: s.name(proto, a.Port)}
	// a full backlog makes the kernel answer EAGAIN: a TCP connect would wait in the queue for the server
	for tries := 0; ; tries++ {
		err := syscall.Connect(fd, target)
		if err == nil {
			return ok(0)
		}
		if err != syscall.EAGAIN || tries >= 40 || !s.valid(n.id) {
			return fail(errnoOf(err))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Session) hGetName(n *notif) result {
	fd, i, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine {
		return cont()
	}
	remote := int(n.nr) == syscall.SYS_GETPEERNAME
	name, _, err := rawGetname(fd, remote)
	if err != nil {
		return fail(errnoOf(err))
	}
	out, found := s.addrOf(i, name, remote)
	if !found {
		// bound to nothing yet: the kernel's answer is an empty address of the family
		out = Build(i.family, 0, true, false)
		if remote {
			return fail(syscall.ENOTCONN)
		}
	}
	if e := s.putAddr(n, n.args[1], n.args[2], out); e != 0 {
		return fail(e)
	}
	return ok(0)
}

func (s *Session) hAccept(n *notif) result {
	fd, i, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine {
		return cont()
	}
	flags := 0
	if int(n.nr) == syscall.SYS_ACCEPT4 {
		flags = int(n.args[3])
	}
	ino := inodeOf(fd)
	var nfd int
	var peer string
	var named bool
	s.mu.Lock()
	if q := s.stash[ino]; len(q) > 0 {
		st := q[0]
		s.stash[ino] = q[1:]
		nfd, peer, named = st.fd, st.peer, st.named
		s.mu.Unlock()
	} else {
		s.mu.Unlock()
		if !isNonblock(fd) && !s.wait(fd, 1, n.id) {
			return fail(syscall.EINTR)
		}
		var err error
		nfd, peer, named, err = rawAccept4(fd, syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK)
		if err != nil {
			return fail(errnoOf(err))
		}
	}
	// the accepted socket is non-blocking in the kernel only until the REPL has it: it asked for what it asked for
	if flags&syscall.SOCK_NONBLOCK == 0 {
		syscall.SetNonblock(nfd, false)
	}
	s.setInfo(nfd, sockInfo{family: i.family})
	if n.args[1] != 0 {
		if out, found := s.addrOf(i, peer, true); found && named {
			if e := s.putAddr(n, n.args[1], n.args[2], out); e != 0 {
				s.keep(ino, nfd, peer, named)
				return fail(e)
			}
		}
	}
	r := s.inject(n, nfd, flags&syscall.SOCK_CLOEXEC != 0)
	if r.replied && !r.gone {
		syscall.Close(nfd)
		return r
	}
	// the REPL's call was cancelled (a signal): the connection is not lost, the retried accept gets it
	s.keep(ino, nfd, peer, named)
	return r
}

func (s *Session) keep(listener uint64, fd int, peer string, named bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stash == nil {
		syscall.Close(fd)
		return
	}
	s.stash[listener] = append(s.stash[listener], stashed{fd: fd, peer: peer, named: named})
}

// sendTo sends payload from the emulated datagram socket fd to the address sa (a sockaddr_in/in6 of the REPL).
func (s *Session) sendTo(fd int, payload []byte, flags int, sa []byte) result {
	var to syscall.Sockaddr
	if sa != nil {
		a, e := Parse(sa)
		if e != 0 {
			return fail(e)
		}
		to = &syscall.SockaddrUnix{Name: s.name(protoOf(fd), a.Port)}
		if e := s.ensureBound(fd); e != 0 {
			return fail(e)
		}
	}
	if err := syscall.Sendto(fd, payload, flags, to); err != nil {
		return fail(errnoOf(err))
	}
	return ok(int64(len(payload)))
}

func (s *Session) hSendto(n *notif) result {
	fd, _, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	ln := int(n.args[5])
	if ln < 2 || ln > 128 {
		return fail(syscall.EINVAL)
	}
	sa, err := readMem(n.pid, n.args[4], ln)
	if err != nil {
		return fail(syscall.EFAULT)
	}
	if !mine || binary.LittleEndian.Uint16(sa[:2]) == afUnix {
		return fail(syscall.EACCES)
	}
	l := int(n.args[2])
	if l > 65535 {
		l = 65535
	}
	payload := []byte{}
	if l > 0 {
		if payload, err = readMem(n.pid, n.args[1], l); err != nil || !s.valid(n.id) {
			return fail(syscall.EFAULT)
		}
	}
	if sockType(fd) == syscall.SOCK_STREAM {
		sa = nil // a connected stream socket ignores the address
	}
	return s.sendTo(fd, payload, int(n.args[3]), sa)
}

func (s *Session) hRecvfrom(n *notif) result {
	fd, i, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine {
		return cont()
	}
	l := int(n.args[2])
	if l > 65535 {
		l = 65535
	}
	flags := int(n.args[3])
	if !isNonblock(fd) && flags&syscall.MSG_DONTWAIT == 0 && !s.wait(fd, 1, n.id) {
		return fail(syscall.EINTR)
	}
	buf := make([]byte, l+1)
	got, from, named, err := rawRecvfrom(fd, buf[:l], flags|syscall.MSG_DONTWAIT)
	if err != nil {
		return fail(errnoOf(err))
	}
	if got > 0 && s.putMem(n, n.args[1], buf[:got]) != nil {
		return fail(syscall.EFAULT)
	}
	if named {
		if out, found := s.addrOf(i, from, true); found {
			s.putAddr(n, n.args[4], n.args[5], out)
		}
	} else {
		s.putMem(n, n.args[5], []byte{0, 0, 0, 0})
	}
	return ok(int64(got))
}

// msghdr and iovec of 64-bit Linux
const (
	msgName, msgNameLen, msgIov, msgIovLen, msgControl, msgControlLen, msgFlags = 0, 8, 16, 24, 32, 40, 48
	msghdrSize                                                                  = 56
)

func readMsghdr(pid uint32, addr uint64) ([]byte, error) { return readMem(pid, addr, msghdrSize) }

func u64(b []byte, off int) uint64 { return binary.LittleEndian.Uint64(b[off : off+8]) }
func u32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off : off+4]) }

// iovecs reads the scatter list of a message: (base, length) pairs, at most 64, 64 KB in all.
func iovecs(pid uint32, hdr []byte) ([][2]uint64, int, syscall.Errno) {
	cnt := int(u64(hdr, msgIovLen))
	if cnt < 0 || cnt > 64 {
		return nil, 0, syscall.EMSGSIZE
	}
	if cnt == 0 {
		return nil, 0, 0
	}
	raw, err := readMem(pid, u64(hdr, msgIov), cnt*16)
	if err != nil {
		return nil, 0, syscall.EFAULT
	}
	var out [][2]uint64
	total := 0
	for k := 0; k < cnt; k++ {
		base, ln := u64(raw, k*16), u64(raw, k*16+8)
		if total+int(ln) > 65535 {
			ln = uint64(65535 - total)
		}
		out = append(out, [2]uint64{base, ln})
		total += int(ln)
	}
	return out, total, 0
}

func (s *Session) hSendmsg(n *notif) result {
	fd, _, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine || sockType(fd) == syscall.SOCK_STREAM {
		return cont() // a stream carries no address; a socket that is not ours cannot name anything
	}
	hdr, err := readMsghdr(n.pid, n.args[1])
	if err != nil {
		return fail(syscall.EFAULT)
	}
	if u64(hdr, msgControlLen) != 0 {
		return fail(syscall.EOPNOTSUPP)
	}
	var sa []byte
	if nameLen := int(u32(hdr, msgNameLen)); u64(hdr, msgName) != 0 && nameLen > 0 {
		if nameLen > 128 {
			return fail(syscall.EINVAL)
		}
		if sa, err = readMem(n.pid, u64(hdr, msgName), nameLen); err != nil {
			return fail(syscall.EFAULT)
		}
		if binary.LittleEndian.Uint16(sa[:2]) == afUnix {
			return fail(syscall.EACCES)
		}
	}
	iov, total, e := iovecs(n.pid, hdr)
	if e != 0 {
		return fail(e)
	}
	payload := make([]byte, 0, total)
	for _, v := range iov {
		if v[1] == 0 {
			continue
		}
		part, err := readMem(n.pid, v[0], int(v[1]))
		if err != nil {
			return fail(syscall.EFAULT)
		}
		payload = append(payload, part...)
	}
	if !s.valid(n.id) {
		return fail(syscall.EINTR)
	}
	return s.sendTo(fd, payload, int(n.args[2]), sa)
}

func (s *Session) hRecvmsg(n *notif) result {
	fd, i, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine || sockType(fd) == syscall.SOCK_STREAM {
		return cont()
	}
	hdr, err := readMsghdr(n.pid, n.args[1])
	if err != nil {
		return fail(syscall.EFAULT)
	}
	iov, total, e := iovecs(n.pid, hdr)
	if e != 0 {
		return fail(e)
	}
	flags := int(n.args[2])
	if !isNonblock(fd) && flags&syscall.MSG_DONTWAIT == 0 && !s.wait(fd, 1, n.id) {
		return fail(syscall.EINTR)
	}
	buf := make([]byte, total+1)
	got, from, named, err := rawRecvfrom(fd, buf[:total], flags|syscall.MSG_DONTWAIT)
	if err != nil {
		return fail(errnoOf(err))
	}
	rest := buf[:got]
	for _, v := range iov {
		if len(rest) == 0 {
			break
		}
		k := int(v[1])
		if k > len(rest) {
			k = len(rest)
		}
		if k > 0 && s.putMem(n, v[0], rest[:k]) != nil {
			return fail(syscall.EFAULT)
		}
		rest = rest[k:]
	}
	if namePtr := u64(hdr, msgName); namePtr != 0 {
		var out []byte
		if named {
			out, _ = s.addrOf(i, from, true)
		}
		have := int(u32(hdr, msgNameLen))
		if len(out) > have {
			s.putMem(n, namePtr, out[:have])
		} else if len(out) > 0 {
			s.putMem(n, namePtr, out)
		}
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(len(out)))
		s.putMem(n, n.args[1]+msgNameLen, b)
	}
	s.putMem(n, n.args[1]+msgControlLen, make([]byte, 8))
	s.putMem(n, n.args[1]+msgFlags, make([]byte, 4))
	return ok(int64(got))
}

// sendmmsg and recvmmsg: a datagram socket is told they do not exist (callers such as libuv fall back to
// sendmsg and recvmsg); a stream, or a socket that is not ours, carries on.
func (s *Session) hMmsg(n *notif) result {
	fd, _, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine || sockType(fd) == syscall.SOCK_STREAM {
		return cont()
	}
	return fail(syscall.ENOSYS)
}

func (s *Session) hSockopt(n *notif) result {
	fd, _, mine, e := s.own(n, n.args[0])
	if e != 0 {
		return fail(e)
	}
	defer syscall.Close(fd)
	if !mine {
		return cont()
	}
	if int(n.nr) == syscall.SYS_SETSOCKOPT {
		return ok(0) // IP, IPv6, TCP and UDP options mean nothing here
	}
	if n.args[3] != 0 && n.args[4] != 0 {
		s.putMem(n, n.args[3], []byte{0, 0, 0, 0})
		s.putMem(n, n.args[4], []byte{4, 0, 0, 0})
	}
	return ok(0)
}

var _ = fmt.Sprintf
