//go:build linux

package egress

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"netemu"
)

var (
	wsMu     sync.Mutex
	wsProxy  = map[string]*Proxy{}
	newProxy = func() *Proxy { return &Proxy{MaxConns: 64, MaxBytes: 2 << 30} }
)

// ServeWorkspace makes the proxy reachable for the REPLs of a workspace that run in the private network of the emulation (LLD 16): it listens on
// the name the emulation gives to port 127.0.0.1:Port of that workspace, so the REPL's connect lands here. One listener for a workspace, kept for
// the life of the server.
func ServeWorkspace(workspace string) error {
	id := netemu.WorkspaceID(workspace)
	wsMu.Lock()
	defer wsMu.Unlock()
	if _, ok := wsProxy[id]; ok {
		return nil
	}
	l, err := net.Listen("unix", fmt.Sprintf("@openrepl-ne/%s/t/%d", id, Port))
	if err != nil {
		return err
	}
	p := newProxy()
	wsProxy[id] = p
	go p.Serve(l)
	return nil
}

// ServeInNamespace opens the listener inside the network namespace of process pid (a REPL that has a kernel namespace of its own: the Pi): a
// thread joins the namespace, makes the socket and is never given back to the runtime (it ends with its goroutine), so nothing else runs in it.
// The socket stays in that namespace and is accepted on from any thread. It is closed when the process is gone.
func ServeInNamespace(pid int) error {
	type res struct {
		fd  int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		runtime.LockOSThread() // not unlocked: see above
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
		if err != nil {
			ch <- res{-1, err}
			return
		}
		defer f.Close()
		if err := unix.Setns(int(f.Fd()), unix.CLONE_NEWNET); err != nil {
			ch <- res{-1, fmt.Errorf("join the REPL's network: %w", err)}
			return
		}
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			ch <- res{-1, err}
			return
		}
		if err := unix.Bind(fd, &unix.SockaddrInet4{Port: Port, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
			unix.Close(fd)
			ch <- res{-1, err}
			return
		}
		if err := unix.Listen(fd, 64); err != nil {
			unix.Close(fd)
			ch <- res{-1, err}
			return
		}
		ch <- res{fd, nil}
	}()
	r := <-ch
	if r.err != nil {
		return r.err
	}
	file := os.NewFile(uintptr(r.fd), "egress")
	l, err := net.FileListener(file) // a copy of the descriptor
	file.Close()
	if err != nil {
		return err
	}
	p := newProxy()
	go p.Serve(l)
	go func() { // the namespace goes with the process: close the listener then
		for {
			time.Sleep(5 * time.Second)
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
				l.Close()
				return
			}
		}
	}()
	return nil
}
