//go:build linux

package netemu

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ProbeFlag is the helper's flag that makes it run the self test instead of a program.
const ProbeFlag = "--net-probe"

// Probe finds out, at start, whether the emulation works on this host: it runs the helper's self test under a
// real supervisor, the way a REPL runs. It is not enough that the kernel has the features: a host can refuse
// the permission checks (a container profile that blocks pidfd_getfd, a ptrace policy).
func Probe(helper string) error {
	// the same socket pair a REPL gets: close-on-exec, so that only descriptor 3 reaches the helper (a helper that
	// held the server's end too would never see it close)
	parent, child, err := NewSocketPair()
	if err != nil {
		return err
	}
	cmd := exec.Command(helper, ProbeFlag)
	cmd.ExtraFiles = []*os.File{child}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		parent.Close()
		child.Close()
		return err
	}
	child.Close()
	// what the server side says about taking over the helper's notification descriptor
	ready := make(chan error, 1)
	go attach(parent, "probe", cmd.Process.Pid, ready)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("the self test failed: %v %s%s", err, strings.TrimSpace(out.String()), attachNote(ready))
		}
		return nil
	case <-time.After(10 * time.Second):
		// a Go program that is sent SIGQUIT prints where each of its goroutines is: that says where it is stuck
		cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		text := strings.TrimSpace(out.String())
		if len(text) > 2500 {
			text = text[:2500] + " ..."
		}
		return fmt.Errorf("the self test did not finish (helper %s): %s%s", helper, text, attachNote(ready))
	}
}

// attachNote says how the server's side of the exchange with the helper went, if it has finished.
func attachNote(ready <-chan error) string {
	select {
	case err := <-ready:
		if err != nil {
			return " [server: " + err.Error() + "]"
		}
		return " [server: took the notification descriptor]"
	default:
		return " [server: still waiting for the helper to announce its notification descriptor]"
	}
}

// SelfTest runs inside the helper, after Install: a TCP exchange and a UDP exchange on the loopback, and a
// connect to an address outside that must be refused.
func SelfTest() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %v", err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4)
		if _, err := c.Read(buf); err == nil {
			c.Write(buf)
		}
		c.Close()
	}()
	c, err := net.DialTimeout("tcp", l.Addr().String(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %v", err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return fmt.Errorf("write: %v", err)
	}
	reply := make([]byte, 4)
	if _, err := c.Read(reply); err != nil || string(reply) != "ping" {
		return fmt.Errorf("read: %v %q", err, reply)
	}
	c.Close()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("udp listen: %v", err)
	}
	defer pc.Close()
	go func() {
		b := make([]byte, 16)
		n, from, err := pc.ReadFrom(b)
		if err == nil {
			pc.WriteTo(b[:n], from)
		}
	}()
	uc, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		return fmt.Errorf("udp dial: %v", err)
	}
	defer uc.Close()
	uc.SetDeadline(time.Now().Add(3 * time.Second))
	uc.Write([]byte("hi"))
	ub := make([]byte, 16)
	if n, err := uc.Read(ub); err != nil || string(ub[:n]) != "hi" {
		return fmt.Errorf("udp exchange: %v %q", err, ub)
	}

	if c, err := net.DialTimeout("tcp", "192.0.2.1:80", 2*time.Second); err == nil {
		c.Close()
		return fmt.Errorf("a connection outside the workspace was allowed")
	}
	return nil
}
