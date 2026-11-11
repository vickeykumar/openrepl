//go:build !linux

package netemu

import (
	"errors"
	"os"
)

// Install is not available here: the emulation needs Linux.
func Install(sock int) error { return errors.New("the network emulation needs Linux") }

// Attach does nothing here.
func Attach(sock *os.File, workspace string, pid int) { sock.Close() }

// WorkspaceID is the identifier of a workspace folder.
func WorkspaceID(workspace string) string { return workspace }

// Probe says the emulation is not available here.
func Probe(helper string) error { return errors.New("the network emulation needs Linux") }

// SelfTest is not available here.
func SelfTest() error { return errors.New("the network emulation needs Linux") }

// NewSocketPair is not available here.
func NewSocketPair() (parent, child *os.File, err error) {
	return nil, nil, errors.New("the network emulation needs Linux")
}
