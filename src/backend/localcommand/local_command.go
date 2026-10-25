package localcommand

import (
	"containers"
	"github.com/kr/pty"
	"github.com/pkg/errors"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
	"utils"
	"workspace"
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

	exitMu  sync.Mutex
	exitErr error // what cmd.Wait() returned, set before ptyClosed is closed
}

func New(command string, argv []string, ppid int, params url.Values, options ...Option) (*LocalCommand, error) {
	if ppid != -1 && !containers.IsProcess(ppid) {
		return nil, errors.Errorf("failed to start command `%s` due to invalid parent id: %d", command, ppid)
	}
	uid := utils.GetUid(params)
	homedir := utils.GetHomeDir(params)
	if ppid != -1 {
		parentHome, ok := containers.WorkspaceHome(ppid)
		if !ok {
			return nil, errors.New("parent REPL has no verified workspace")
		}
		homedir = parentHome
	}
	if homedir == "" {
		return nil, errors.New("workspace identity could not be resolved")
	}
	if err := workspace.PrepareCapability(); err != nil {
		return nil, err
	}
	identity, err := workspace.Prepare(utils.HOME_DIR, homedir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to prepare private workspace")
	}
	commandArgs := containers.GetCommandArgs(command, argv, ppid, params)
	launcher, err := workspaceLauncher(identity, commandArgs, ppid)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(launcher[0], launcher[1:]...)
	if ppid != -1 {
		cmd.Dir = homedir
	} else {
		cmd.Dir = homedir
		if uid == "" {
			// reset the job to delete the guests working dir after a certain deadline, longer now as session has just started
			jobname := utils.REMOVE_JOB_KEY + cmd.Dir
			utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
				utils.RemoveDir(cmd.Dir)
			})
		}
	}
	if command == "bash" {
		if err := ioutil.WriteFile(cmd.Dir+"/.bashrc", []byte(`PS1='${debian_chroot:+($debian_chroot)}\[\033[01;32m\]repl@$HOSTNAME\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '`), 0600); err == nil {
			_ = os.Chown(cmd.Dir+"/.bashrc", int(identity.UID), int(identity.GID))
		}
	}
	// not the server's own environment: its secrets must not reach the user's shell
	cmd.Env = utils.ChildEnviron(os.Environ())
	cmd.Env = append(cmd.Env, "TERM=xterm")
	cmd.Env = append(cmd.Env, "GOPATH=/opt/gotty/")
	cmd.Env = append(cmd.Env, "GOCACHE=/tmp/go_cache/.cache/go-build/")
	cmd.Env = append(cmd.Env, "HOME="+cmd.Dir)
	cmd.Env = append(cmd.Env, "HOSTNAME="+command)
	cmd.Env = append(cmd.Env, "GCC_EXEC_PREFIX=/usr/lib/gcc/")
	cmd.Env = append(cmd.Env, utils.IdeLangKey+"="+utils.GetCompilerLang(params))
	cmd.Env = append(cmd.Env, utils.CompilerOptionKey+"="+utils.GetCompilerOption(params))
	cmd.Env = append(cmd.Env, utils.IdeFileNameKey+"="+utils.GetIdeFileName(params))

	// Append /opt/gotty/bin to the PATH environment variable
	path := os.Getenv("PATH")
	path += string(os.PathListSeparator) + "/opt/gotty/bin"
	cmd.Env = append(cmd.Env, "PATH="+path)

	//fill other env variables recieved from client
	envargs := utils.GetEnvFlags(params)
	envvars := strings.Split(envargs, " ")

	for _, envvar := range envvars {
		// sanitize
		if strings.Contains(envvar, "$") {
			// Expand nested environment variable references, in the environment
			// the program gets and not in the server's
			envvar = utils.ExpandIn(cmd.Env, envvar)
		}
		if strings.Contains(envvar, "~/") {
			// Replace '~' with user's home directory
			envvar = strings.ReplaceAll(envvar, "~/", homedir+"/")
		}
		cmd.Env = append(cmd.Env, envvar)
	} // done filling env vars

	// Do not let user-controlled loader variables affect setpriv while it is
	// still running with the service credentials. The NSS wrapper is enabled
	// by the post-drop shell only.
	safeEnv := cmd.Env[:0]
	for _, entry := range cmd.Env {
		name := entry
		if at := strings.IndexByte(entry, '='); at >= 0 {
			name = entry[:at]
		}
		switch name {
		case "LD_PRELOAD", "LD_AUDIT", "LD_LIBRARY_PATH", "LD_ORIGIN_PATH",
			"NSS_WRAPPER_PASSWD", "NSS_WRAPPER_GROUP", "OPENREPL_NSS_WRAPPER":
			continue
		}
		safeEnv = append(safeEnv, entry)
	}
	cmd.Env = safeEnv

	// Add the private NSS mapping after client environment flags. NSS wrapper
	// is loaded by the target REPL only after setpriv has dropped credentials.
	if nssEnv, nssErr := workspace.NSSWrapperEnvironment(identity, homedir); nssErr != nil {
		log.Printf("workspace: NSS identity names unavailable for uid %d: %v", identity.UID, nssErr)
	} else {
		cmd.Env = append(cmd.Env, nssEnv...)
	}

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
	containers.TrackWorkspaceProcess(pid, homedir)
	containers.AddProcesstoNewSubCgroup(command, pid, utils.Iscompiled(params)) // creating and adding process to new subcrgroup container
	go containers.EnableNetworking(pid)                                         // enable loopback
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
				jobname := utils.REMOVE_JOB_KEY + lcmd.cmd.Dir
				utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
					utils.RemoveDir(lcmd.cmd.Dir)
				})
			}
		}()

		cmderr := lcmd.cmd.Wait()
		if cmderr != nil {
			log.Printf("lcmd.cmd.wait : %s", cmderr.Error())
		}
		lcmd.exitMu.Lock()
		lcmd.exitErr = cmderr
		lcmd.exitMu.Unlock()
	}()

	return lcmd, nil
}

// workspaceLauncher drops privileges only after a forked terminal has entered
// its owner's namespaces. The shell sets a restrictive umask and immediately
// execs the requested REPL, preserving the terminal process ID.
func workspaceLauncher(id workspace.Identity, commandArgs []string, ppid int) ([]string, error) {
	setpriv, err := exec.LookPath("setpriv")
	if err != nil {
		return nil, errors.Wrap(err, "setpriv is required for workspace isolation")
	}
	inner := []string{"--reuid=" + strconv.FormatUint(uint64(id.UID), 10), "--regid=" + strconv.FormatUint(uint64(id.GID), 10), "--clear-groups", "--inh-caps=-all", "--ambient-caps=-all", "--no-new-privs", "--", "/bin/sh", "-c", "umask 077; if [ -n \"${OPENREPL_NSS_WRAPPER:-}\" ]; then export LD_PRELOAD=\"$OPENREPL_NSS_WRAPPER\"; fi; unset OPENREPL_NSS_WRAPPER; exec \"$@\"", "openrepl-workspace"}
	userCommand := commandArgs
	if ppid != -1 && len(commandArgs) > 0 && commandArgs[0] == "/usr/bin/nsenter" {
		// Keep nsenter privileged long enough to join the parent's namespaces,
		// then drop to the exact same workspace identity before user code.
		at := 1
		for at < len(commandArgs) && strings.HasPrefix(commandArgs[at], "-") {
			at++
		}
		out := append([]string{}, commandArgs[:at]...)
		out = append(out, "--", setpriv)
		userCommand = commandArgs[at:]
		inner = append(inner, userCommand...)
		return append(out, inner...), nil
	}
	inner = append(inner, userCommand...)
	out := []string{setpriv}
	return append(out, inner...), nil
}

// ExitReason reports why the command ended: "killed" when it was stopped by
// SIGKILL (for example by the container's memory limit), "" otherwise.
// It waits briefly for the process to be reaped.
func (lcmd *LocalCommand) ExitReason() string {
	select {
	case <-lcmd.ptyClosed:
	case <-time.After(2 * time.Second):
	}
	lcmd.exitMu.Lock()
	defer lcmd.exitMu.Unlock()
	if lcmd.exitErr != nil && strings.Contains(lcmd.exitErr.Error(), "signal: killed") {
		return "killed"
	}
	return ""
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
