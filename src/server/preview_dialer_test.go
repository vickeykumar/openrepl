//go:build linux

package server

import (
	"context"
	"fmt"
	"net"
	"testing"

	"livepreview"
	"netemu"
)

// a program that listens in the emulation is reached even when no process of the workspace was found by its HOME or its helper
func TestThePreviewReachesAnEmulatedProgramWhenNoProcessWasFound(t *testing.T) {
	home := t.TempDir() // no process has this HOME
	old := netEmuActive
	netEmuActive = func() bool { return true }
	defer func() { netEmuActive = old }()
	l, err := net.Listen("unix", "@"+netemu.Name(netemu.WorkspaceID(home), "t", 8000)[1:])
	if err != nil {
		t.Skip("no abstract sockets here:", err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
	}()
	c, err := previewDialer(home)(context.Background(), 8000)
	if err != nil {
		t.Fatalf("the program listens and was not reached: %v", err)
	}
	c.Close()
	// nothing listening and nothing running: still "nothing is running yet"
	if _, err := previewDialer(home)(context.Background(), 8001); err != livepreview.ErrNoProgram {
		t.Errorf("nothing there: %v", err)
	}
	// the emulation is off: no guessing
	netEmuActive = func() bool { return false }
	if _, err := previewDialer(home)(context.Background(), 8000); err != livepreview.ErrNoProgram {
		t.Errorf("no emulation: %v", fmt.Sprint(err))
	}
}

// A workspace with an unsandboxed process in the server's network (an admin's terminal opened in it) and a REPL under the emulation: the program of
// the emulated REPL must be reached; before, the unsandboxed process sent every port to the real network.
func TestAnUnsandboxedProcessDoesNotHideAnEmulatedProgram(t *testing.T) {
	home := t.TempDir()
	oldProc, oldEmu := previewProc, netEmuActive
	defer func() { previewProc, netEmuActive = oldProc, oldEmu }()
	netEmuActive = func() bool { return true }
	previewProc.find = func(string) []int { return []int{100, 200} }
	previewProc.same = func(int) bool { return true }
	previewProc.unsandboxed = func(pid int) bool { return pid == 100 } // 100 is an admin's, 200 is under the emulation
	l, err := net.Listen("unix", "@"+netemu.Name(netemu.WorkspaceID(home), "t", 8000)[1:])
	if err != nil {
		t.Skip("no abstract sockets here:", err)
	}
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := previewDialer(home)(context.Background(), 8000)
	if err != nil {
		t.Fatalf("the emulated program was not reached: %v", err)
	}
	c.Close()
	// the site's own port is never shown for the admin's real network
	noteOwnPort("8080")
	defer func() { ownPortsMu.Lock(); delete(ownPorts, 8080); ownPortsMu.Unlock() }()
	if _, err := previewDialer(home)(context.Background(), 8080); err != errOwnPort {
		t.Errorf("the site's own port: %v", err)
	}
}

// A REPL in a network namespace of its own (a worker with cgroups) and an admin's process in the server's: each is tried
func TestAProgramInAnotherNamespaceIsReachedBeforeTheRealNetwork(t *testing.T) {
	home := t.TempDir()
	oldProc, oldEmu := previewProc, netEmuActive
	defer func() { previewProc, netEmuActive = oldProc, oldEmu }()
	netEmuActive = func() bool { return true }
	previewProc.find = func(string) []int { return []int{100, 300} }
	previewProc.same = func(pid int) bool { return pid == 100 }
	previewProc.unsandboxed = func(int) bool { return true }
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	previewProc.dialIn = func(pid, port int) (net.Conn, error) {
		if pid == 300 && port == 8000 {
			return client, nil
		}
		return nil, fmt.Errorf("nothing in the namespace of %d on %d", pid, port)
	}
	c, err := previewDialer(home)(context.Background(), 8000)
	if err != nil || c != client {
		t.Fatalf("the program of the other namespace: %v", err)
	}
}
