//go:build linux

package localcommand

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

// drainLimit is the longest the server waits, once a program has ended, for the terminal's reader to take what
// the program wrote last.
const drainLimit = 2 * time.Second

// drainPty waits until what is still unread in the master side of a pseudo-terminal has been read, or limit has
// passed. Closing the master throws away what is in it: a program that prints and exits at once, on a slow
// machine (a Raspberry Pi worker), would show nothing in the page because the reader had not yet taken its
// output when the server closed the terminal.
func drainPty(master *os.File, limit time.Duration) {
	rc, err := master.SyscallConn()
	if err != nil {
		return
	}
	deadline := time.Now().Add(limit)
	// What a program wrote last may still be on its way from the terminal's input buffer to the reader's
	// (the kernel moves it in the background): an empty reading counts only when it is seen twice.
	time.Sleep(10 * time.Millisecond)
	empty := 0
	for {
		var pending int32
		var errno syscall.Errno
		if err := rc.Control(func(fd uintptr) {
			_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCINQ, uintptr(unsafe.Pointer(&pending)))
		}); err != nil || errno != 0 {
			return
		}
		if pending == 0 {
			if empty++; empty >= 2 {
				return
			}
		} else {
			empty = 0
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
