//go:build !linux

package egress

import "errors"

// ServeWorkspace is for Linux only.
func ServeWorkspace(workspace string) error { return errors.New("egress: Linux only") }

// ServeInNamespace is for Linux only.
func ServeInNamespace(pid int) error { return errors.New("egress: Linux only") }
