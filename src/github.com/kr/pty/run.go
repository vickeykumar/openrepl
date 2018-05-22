package pty

import (
	"containers"
	"os"
	"os/exec"
	"syscall"
)

// Start assigns a pseudo-terminal tty os.File to c.Stdin, c.Stdout,
// and c.Stderr, calls c.Start, and returns the File of the tty's
// corresponding pty.
func Start(command string, c *exec.Cmd) (pty *os.File, err error) {
	pty, tty, err := Open()
	if err != nil {
		return nil, err
	}
	defer tty.Close()
	c.Stdout = tty
	c.Stdin = tty
	c.Stderr = tty
	c.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Setsid: true}
	containers.AddContainerAttributes(command, c.SysProcAttr) // containers attrib
	c.Env = os.Environ()
	c.Env = append(c.Env, "TERM=xterm")
	c.Env = append(c.Env, "GOPATH=/opt/gotty/")
	err = c.Start()
	if err != nil {
		pty.Close()
		return nil, err
	}
	containers.AddProcess(command, c)
	return pty, err
}
