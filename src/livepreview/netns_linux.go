//go:build linux

package livepreview

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DialIn connects to 127.0.0.1:port inside the network namespace nsPath (a file such as /proc/<pid>/ns/net): the
// namespace of a workspace that has one of its own. The socket is made by a thread that has joined the namespace;
// that thread is never given back to the runtime (it ends with its goroutine), so nothing else ever runs in it.
func DialIn(nsPath string, port int, timeout time.Duration) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // not unlocked: see above
		f, err := os.Open(nsPath)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		defer f.Close()
		if err := unix.Setns(int(f.Fd()), unix.CLONE_NEWNET); err != nil {
			ch <- result{nil, fmt.Errorf("join the workspace's network: %w", err)}
			return
		}
		fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		tv := syscall.NsecToTimeval(int64(timeout))
		syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_SNDTIMEO, &tv)
		if err := syscall.Connect(fd, &syscall.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
			syscall.Close(fd)
			ch <- result{nil, err}
			return
		}
		syscall.SetNonblock(fd, true)
		file := os.NewFile(uintptr(fd), "preview")
		c, err := net.FileConn(file) // a copy of the descriptor
		file.Close()
		ch <- result{c, err}
	}()
	r := <-ch
	return r.c, r.err
}

// FindPID is the id of a process that runs in the workspace home, or 0 when none does. A REPL's processes are told
// where their workspace is (HOME=<home>, set by the server), and the helper that starts a REPL carries
// "--workspace <home>" until it becomes the REPL; the lowest id of those is the REPL itself. The answer is kept for
// a few seconds (a page asks for many files), and a process that has gone is not given again.
func FindPID(home string) int {
	pidCache.mu.Lock()
	defer pidCache.mu.Unlock()
	if c, ok := pidCache.m[home]; ok && time.Since(c.at) < 5*time.Second {
		if _, err := os.Stat("/proc/" + strconv.Itoa(c.pid)); err == nil {
			return c.pid
		}
	}
	pids := scanPIDs(home)
	pid := 0
	if len(pids) > 0 {
		pid = pids[0]
	}
	if len(pidCache.m) > 512 {
		pidCache.m = map[string]cachedPID{}
	}
	pidCache.m[home] = cachedPID{pid, pids, time.Now()}
	return pid
}

type cachedPID struct {
	pid  int
	pids []int
	at   time.Time
}

// FindPIDs is one process of each network the workspace's processes run in (the lowest id of each). A workspace with two
// terminals, a Python REPL and a Bash one, has two networks when each REPL has a namespace of its own, and the program the
// user wants to see may be in either: the lowest id overall (FindPID) is just the oldest of them.
func FindPIDs(home string) []int {
	FindPID(home)
	pidCache.mu.Lock()
	defer pidCache.mu.Unlock()
	return append([]int{}, pidCache.m[home].pids...)
}

var pidCache = struct {
	mu sync.Mutex
	m  map[string]cachedPID
}{m: map[string]cachedPID{}}

// scanPIDs lists the lowest id of each network among the processes of the workspace, lowest first.
func scanPIDs(home string) []int {
	cmdWant := "--workspace\x00" + home + "\x00"
	envWant := "\x00HOME=" + home + "\x00"
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	lowest := map[string]int{} // network -> lowest process id
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		ns, err := os.Readlink("/proc/" + d.Name() + "/ns/net")
		if err != nil {
			continue
		}
		if have, ok := lowest[ns]; ok && pid > have {
			continue
		}
		cmd, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err == nil && strings.Contains(string(cmd), cmdWant) {
			lowest[ns] = pid
			continue
		}
		env, err := os.ReadFile("/proc/" + d.Name() + "/environ")
		if err == nil && strings.Contains("\x00"+string(env), envWant) {
			lowest[ns] = pid
		}
	}
	out := make([]int, 0, len(lowest))
	for _, pid := range lowest {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

// Unsandboxed says whether process pid runs with no seccomp filter at all. Under the network emulation every REPL of a
// workspace carries the filter of the emulation (the helper installs it before the REPL starts), so a REPL that has none,
// in the network of the server, is one that was started without any sandbox: an admin's. Its programs listen on the
// machine's real network.
func Unsandboxed(pid int) bool {
	// the helper itself, before it has installed the filter, is not a REPL without a sandbox
	if cmd, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err != nil || strings.Contains(string(cmd), "--net-emulation") || strings.Contains(string(cmd), "--workspace") {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Seccomp:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Seccomp:")) == "0"
		}
	}
	return false
}

// SameNetwork says whether process pid is in the network namespace of this process (the server).
func SameNetwork(pid int) bool {
	a, err1 := os.Readlink("/proc/self/ns/net")
	b, err2 := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/net")
	return err1 == nil && err2 == nil && a == b
}

// ReadProc reads a file of /proc (the sockets a process's namespace has: /proc/<pid>/net/tcp, /proc/net/unix).
func ReadProc(name string) string {
	b, _ := os.ReadFile(name)
	return string(b)
}
