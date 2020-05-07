package localcommand

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"
	"io/ioutil"
	"containers"
	"github.com/kr/pty"
	"github.com/pkg/errors"
	"log"
	"utils"
)

const (
	DefaultCloseSignal  = syscall.SIGINT
	DefaultCloseTimeout = 10 * time.Second
)

type LocalCommand struct {
	command string
	argv    []string

	closeSignal  syscall.Signal
	closeTimeout time.Duration

	cmd       *exec.Cmd
	pty       *os.File
	ptyClosed chan struct{}
}

func New(command string, argv []string, ppid int, params map[string][]string, options ...Option) (*LocalCommand, error) {
	if ppid != -1 && !containers.IsProcess(ppid) {
		return nil, errors.Errorf("failed to start command `%s` due to invalid parent id: %d", command, ppid)
	}
	commandArgs := containers.GetCommandArgs(command, argv, ppid, params)
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.Dir = containers.HOME_DIR + command + "/" + strconv.Itoa(int(time.Now().Unix()))
	os.MkdirAll(cmd.Dir, 0755)
	if command == "bash" {
		ioutil.WriteFile(cmd.Dir+"/.bashrc", []byte(`PS1='${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@$HOSTNAME\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '`), 0644)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "TERM=xterm")
	cmd.Env = append(cmd.Env, "GOPATH=/opt/gotty/")
	cmd.Env = append(cmd.Env, "HOME="+cmd.Dir)
	cmd.Env = append(cmd.Env, "HOSTNAME="+command)
	cmd.Env = append(cmd.Env, "GCC_EXEC_PREFIX=/usr/lib/gcc/")
	cmd.Env = append(cmd.Env, utils.IdeLangKey+"="+utils.GetCompilerLang(params))
	pty, err := pty.Start(command, cmd, ppid)
	if err != nil {
		// todo close cmd?
		return nil, errors.Wrapf(err, "failed to start command `%s`", command)
	}
	ptyClosed := make(chan struct{})

	lcmd := &LocalCommand{
		command: command,
		argv:    argv,

		closeSignal:  DefaultCloseSignal,
		closeTimeout: DefaultCloseTimeout,

		cmd:       cmd,
		pty:       pty,
		ptyClosed: ptyClosed,
	}

	for _, option := range options {
		option(lcmd)
	}

	pid := cmd.Process.Pid
	containers.AddProcesstoNewSubCgroup(command, pid) // creating and adding process to new subcrgroup container
	containers.EnableNetworking(pid)	// enable loopback
	// When the process is closed by the user,
	// close pty so that Read() on the pty breaks with an EOF.
	go func() {
		defer func() {
			lcmd.pty.Close()
			close(lcmd.ptyClosed)
			containers.DeleteProcessFromSubCgroup(command, pid) // deleting the subcgroup container of that process
			os.RemoveAll(lcmd.cmd.Dir)
		}()

		cmderr := lcmd.cmd.Wait()
		if cmderr != nil {
	        log.Printf("lcmd.cmd.wait : %s", cmderr.Error())
		}
	}()

	return lcmd, nil
}

func (lcmd *LocalCommand) Read(p []byte) (n int, err error) {
	return lcmd.pty.Read(p)
}

func (lcmd *LocalCommand) Write(p []byte) (n int, err error) {
	return lcmd.pty.Write(p)
}

func (lcmd *LocalCommand) Close() error {
	if lcmd.cmd != nil && lcmd.cmd.Process != nil {
		lcmd.cmd.Process.Signal(lcmd.closeSignal)
	}
	for {
		select {
		case <-lcmd.ptyClosed:
			return nil
		case <-lcmd.closeTimeoutC():
			lcmd.cmd.Process.Signal(syscall.SIGKILL)
		}
	}
}

func (lcmd *LocalCommand) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{
		"command": lcmd.command,
		"argv":    lcmd.argv,
		"pid":     lcmd.cmd.Process.Pid,
	}
}

func (lcmd *LocalCommand) ResizeTerminal(width int, height int) error {
	window := struct {
		row uint16
		col uint16
		x   uint16
		y   uint16
	}{
		uint16(height),
		uint16(width),
		0,
		0,
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		lcmd.pty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&window)),
	)
	if errno != 0 {
		return errno
	} else {
		return nil
	}
}

func (lcmd *LocalCommand) closeTimeoutC() <-chan time.Time {
	if lcmd.closeTimeout >= 0 {
		return time.After(lcmd.closeTimeout)
	}

	return make(chan time.Time)
}
