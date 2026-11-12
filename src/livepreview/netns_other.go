//go:build !linux

package livepreview

import (
	"errors"
	"net"
	"time"
)

// DialIn needs Linux.
func DialIn(nsPath string, port int, timeout time.Duration) (net.Conn, error) {
	return nil, errors.New("the preview of a program needs Linux")
}

// FindPID needs Linux.
func FindPID(home string) int { return 0 }

// SameNetwork needs Linux.
func SameNetwork(pid int) bool { return false }

// ReadProc needs Linux.
func ReadProc(name string) string { return "" }
