package localcommand

import (
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
	"io/ioutil"
	"containers"
	"github.com/kr/pty"
	"github.com/pkg/errors"
	"log"
	"utils"
	"user"
	"net/url"
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

func New(command string, argv []string, ppid int, params url.Values, options ...Option) (*LocalCommand, error) {
	if ppid != -1 && !containers.IsProcess(ppid) {
		return nil, errors.Errorf("failed to start command `%s` due to invalid parent id: %d", command, ppid)
	}
	uid := utils.GetUid(params)
	homedir := utils.GetHomeDir(params)
	commandArgs := containers.GetCommandArgs(command, argv, ppid, params)
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	if ppid != -1 {
		// using working directory of parent process only 
		cmd.Dir = containers.GetWorkingDir(ppid)
	} else {
		// get working for a user uid, same as homedir generated
		if homedir == "" {
			cmd.Dir = user.GetHomeDir(uid)
		} else {
			cmd.Dir = homedir
		}
		os.MkdirAll(cmd.Dir, 0755)
		if uid == "" {
				// reset the job to delete the guests working dir after a certain deadline, longer now as session has just started
				jobname := utils.REMOVE_JOB_KEY+cmd.Dir
				utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
					utils.RemoveDir(cmd.Dir)
				})
		}
	}
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
	cmd.Env = append(cmd.Env, utils.CompilerOptionKey+"="+utils.GetCompilerOption(params))
	cmd.Env = append(cmd.Env, utils.IdeFileNameKey+"="+utils.GetIdeFileName(params))
	pty, err := pty.Start(command, cmd, ppid, params)
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
	containers.AddProcesstoNewSubCgroup(command, pid, utils.Iscompiled(params)) // creating and adding process to new subcrgroup container
	go containers.EnableNetworking(pid)	// enable loopback
	// When the process is closed by the user,
	// close pty so that Read() on the pty breaks with an EOF.
	go func() {
		defer func() {
			lcmd.pty.Close()
			close(lcmd.ptyClosed)
			containers.DeleteProcessFromSubCgroup(command, pid) // deleting the subcgroup container of that process

			// don't delete working directory if user is logged in
			// only Guest users homedir to be deleted
			if ppid == -1 && uid == "" {
				// reset the job to delete the working dir after a certain deadline 
				// if guest is not conecting again
				jobname := utils.REMOVE_JOB_KEY+lcmd.cmd.Dir
				utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
					utils.RemoveDir(lcmd.cmd.Dir)
				})
			}
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
