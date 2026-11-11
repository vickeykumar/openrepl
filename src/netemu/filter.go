//go:build linux

package netemu

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// the seccomp user notification interface of the kernel (Linux 5.0; ADDFD 5.9)
const (
	seccompSetModeFilter         = 1
	seccompFilterFlagNewListener = 1 << 3
	retAllow                     = 0x7fff0000
	retKillProcess               = 0x80000000
	retUserNotif                 = 0x7fc00000
	retErrnoBase                 = 0x00050000
	ioctlNotifRecv               = 0xC0502100
	ioctlNotifSend               = 0xC0182101
	ioctlNotifIDValid            = 0x40082102
	ioctlNotifAddFD              = 0x40182103
	notifFlagContinue            = 1
	addfdSend                    = 1 << 1
	sysPidfdOpen                 = 434
	sysPidfdGetfd                = 438
	solSocket                    = 1
)

type sockFilter struct {
	code   uint16
	jt, jf uint8
	k      uint32
}

type sockFprog struct {
	n   uint16
	_   [6]byte
	ptr *sockFilter
}

type notif struct {
	id    uint64
	pid   uint32
	flags uint32
	nr    int32
	arch  uint32
	ip    uint64
	args  [6]uint64
}

type notifResp struct {
	id    uint64
	val   int64
	errno int32
	flags uint32
}

type addFD struct {
	id       uint64
	flags    uint32
	srcfd    uint32
	newfd    uint32
	newfdFlg uint32
}

func ioctl(fd int, req uintptr, p unsafe.Pointer) syscall.Errno {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(p))
	return e
}

func auditArch() uint32 {
	if runtime.GOARCH == "arm64" {
		return 0xC00000B7
	}
	return 0xC000003E
}

func sysSeccomp() uintptr {
	if runtime.GOARCH == "arm64" {
		return 277
	}
	return 317
}

// A rule says what the filter does for a system call: notify always, notify when an argument is not zero (a
// call without an address is not worth a round trip), notify when the low 32 bits of an argument differ from a
// value (an option level other than SOL_SOCKET), or fail it with an errno.
type rule struct {
	nr   int
	kind int // 0 always, 1 when args[idx] != 0, 2 when low 32 bits of args[idx] != val, 3 errno
	idx  int
	val  uint32
}

const (
	ruleAlways = iota
	ruleNonZero
	ruleNotEqual
	ruleErrno
)

// The io_uring calls can make a socket call without passing through the filter: they are refused.
const (
	sysIoUringSetup    = 425
	sysIoUringEnter    = 426
	sysIoUringRegister = 427
)

// sendmmsg and recvmmsg are not in the syscall package of every architecture
var sysSendmmsg, sysRecvmmsg = func() (int, int) {
	if runtime.GOARCH == "arm64" {
		return 269, 243
	}
	return 307, 299 // amd64
}()

func rules() []rule {
	return []rule{
		{syscall.SYS_SOCKET, ruleAlways, 0, 0},
		{syscall.SYS_BIND, ruleAlways, 0, 0},
		{syscall.SYS_CONNECT, ruleAlways, 0, 0},
		{syscall.SYS_GETSOCKNAME, ruleAlways, 0, 0},
		{syscall.SYS_GETPEERNAME, ruleAlways, 0, 0},
		{syscall.SYS_SENDMSG, ruleAlways, 0, 0},
		{syscall.SYS_RECVMSG, ruleAlways, 0, 0},
		{sysSendmmsg, ruleAlways, 0, 0},
		{sysRecvmmsg, ruleAlways, 0, 0},
		{syscall.SYS_ACCEPT, ruleNonZero, 1, 0},
		{syscall.SYS_ACCEPT4, ruleNonZero, 1, 0},
		{syscall.SYS_SENDTO, ruleNonZero, 4, 0},
		{syscall.SYS_RECVFROM, ruleNonZero, 4, 0},
		{syscall.SYS_SETSOCKOPT, ruleNotEqual, 1, solSocket},
		{syscall.SYS_GETSOCKOPT, ruleNotEqual, 1, solSocket},
		{sysIoUringSetup, ruleErrno, 0, uint32(syscall.ENOSYS)},
		{sysIoUringEnter, ruleErrno, 0, uint32(syscall.ENOSYS)},
		{sysIoUringRegister, ruleErrno, 0, uint32(syscall.ENOSYS)},
	}
}

// buildFilter is the classic BPF program of rules(): architecture check, then one block per rule.
func buildFilter(rs []rule) []sockFilter {
	p := []sockFilter{
		{0x20, 0, 0, 4},              // ld arch
		{0x15, 1, 0, auditArch()},    // jeq arch
		{0x06, 0, 0, retKillProcess}, // another architecture (32-bit calls): kill
		{0x20, 0, 0, 0},              // ld nr
	}
	for _, r := range rs {
		lo, hi := uint32(16+8*r.idx), uint32(20+8*r.idx)
		var blk []sockFilter
		switch r.kind {
		case ruleAlways:
			blk = []sockFilter{{0x06, 0, 0, retUserNotif}}
		case ruleNonZero:
			blk = []sockFilter{
				{0x20, 0, 0, lo}, {0x15, 0, 3, 0},
				{0x20, 0, 0, hi}, {0x15, 0, 1, 0},
				{0x06, 0, 0, retAllow}, {0x06, 0, 0, retUserNotif},
			}
		case ruleNotEqual:
			blk = []sockFilter{
				{0x20, 0, 0, lo}, {0x15, 1, 0, r.val},
				{0x06, 0, 0, retUserNotif}, {0x06, 0, 0, retAllow},
			}
		case ruleErrno:
			blk = []sockFilter{{0x06, 0, 0, retErrnoBase | r.val}}
		}
		p = append(p, sockFilter{0x15, 0, uint8(len(blk)), uint32(r.nr)})
		p = append(p, blk...)
	}
	return append(p, sockFilter{0x06, 0, 0, retAllow})
}

// Install is called by the helper, in the process that is about to become the REPL, with the descriptor of a
// socket the server gave it (it must be the process that execs the REPL: the filter stays on it and on
// everything it starts). It sets no_new_privs, loads the filter, and tells the server the number of the
// descriptor that receives the notifications; the server takes a copy of it (pidfd_getfd: the filter is on
// already, so the helper cannot pass a descriptor with sendmsg, which the filter sends to the server) and
// answers with one byte, and only then does the helper close its own.
func Install(sock int) error {
	runtime.LockOSThread()
	if _, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 38, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("setting no_new_privs: %v", e)
	}
	prog := buildFilter(rules())
	fp := sockFprog{n: uint16(len(prog)), ptr: &prog[0]}
	fd, _, e := syscall.Syscall(sysSeccomp(), seccompSetModeFilter, seccompFilterFlagNewListener, uintptr(unsafe.Pointer(&fp)))
	if e != 0 {
		return fmt.Errorf("loading the seccomp filter: %v", e)
	}
	defer syscall.Close(int(fd))
	syscall.SetsockoptTimeval(sock, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Sec: 10})
	var num [4]byte
	binary.LittleEndian.PutUint32(num[:], uint32(fd))
	if _, err := syscall.Write(sock, num[:]); err != nil {
		return fmt.Errorf("telling the server the notification descriptor: %v", err)
	}
	var ack [1]byte
	if n, err := syscall.Read(sock, ack[:]); err != nil || n != 1 {
		return fmt.Errorf("the server did not take the notification descriptor: %v", err)
	}
	syscall.CloseOnExec(sock)
	return nil
}

// NewSocketPair makes the socket the server shares with a REPL's helper: the server keeps parent, the child
// process gets child (ExtraFiles, so it is descriptor 3 there).
func NewSocketPair() (parent, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "netemu-parent"), os.NewFile(uintptr(fds[1]), "netemu-child"), nil
}

// fetchFD is the server's side of Install: it reads the number of the helper's descriptor, copies it from the
// helper (pid), and tells the helper it may go on.
func fetchFD(sock *os.File, pid int) (int, error) {
	var num [4]byte
	if _, err := io.ReadFull(sock, num[:]); err != nil {
		return -1, fmt.Errorf("the helper ended before it installed the filter: %v", err)
	}
	pfd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if e != 0 {
		return -1, fmt.Errorf("pidfd_open: %v", e)
	}
	defer syscall.Close(int(pfd))
	fd, _, e := syscall.Syscall(sysPidfdGetfd, pfd, uintptr(binary.LittleEndian.Uint32(num[:])), 0)
	if e != 0 {
		return -1, fmt.Errorf("pidfd_getfd: %v", e)
	}
	if _, err := sock.Write([]byte{1}); err != nil {
		syscall.Close(int(fd))
		return -1, err
	}
	return int(fd), nil
}
