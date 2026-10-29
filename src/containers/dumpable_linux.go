//go:build linux
// +build linux

package containers

import "syscall"

const prSetDumpable = 4

// makeNonDumpable marks this process as not dumpable. The files of /proc/<pid> of such a process
// (its environment and memory among them) can then be read only by a process that has the
// capability to trace it, so a REPL of the same user cannot read the secrets the server was
// started with. Processes the server starts are dumpable again after their exec.
func makeNonDumpable() error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetDumpable, 0, 0, 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
