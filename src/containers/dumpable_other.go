//go:build !linux
// +build !linux

package containers

// makeNonDumpable does nothing here: there is no /proc to protect.
func makeNonDumpable() error { return nil }
