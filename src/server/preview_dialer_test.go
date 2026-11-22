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
