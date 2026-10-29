//go:build !linux
// +build !linux

package landlock

import "errors"

var errNotLinux = errors.New("Landlock exists on Linux only")

// ABI says there is no Landlock here.
func ABI() (int, error) { return 0, errNotLinux }

// Self is empty here.
func Self() string { return "" }

// Restrict cannot restrict anything here.
func Restrict(p *Policy) error { return errNotLinux }
