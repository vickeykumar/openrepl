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
	pgid      int
	identity  workspace.Identity
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
		identity:  identity,
		ptyClosed: ptyClosed,
	}

	for _, option := range options {
		option(lcmd)
	}

	pid := cmd.Process.Pid
	pgid, pgidErr := syscall.Getpgid(pid)
	if pgidErr != nil {
		log.Printf("lcmd: unable to read process group for pid=%d: %v", pid, pgidErr)
		pgid = pid // pty.Start creates a new session, so PID is the expected PGID.
	}
	if pgid != pid {
		log.Printf("lcmd: unexpected process group for terminal pid=%d pgid=%d; group signaling disabled", pid, pgid)
	}
	lcmd.pgid = pgid
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
	if lcmd.cmd == nil || lcmd.cmd.Process == nil {
		return nil
	}

	// pty.Start creates a new session for this terminal (and therefore a new
	// process group led by the command). Signal the group so children started
	// by a shell or REPL do not outlive a disconnected terminal. Use the normal
	// signal path first; only fall back to a helper under the workspace UID if
	// the service is denied permission.
	log.Printf("lcmd: closing terminal process group pid=%d pgid=%d", lcmd.cmd.Process.Pid, lcmd.processGroupID())
	if err := lcmd.signalProcessGroup(lcmd.closeSignal); err != nil {
		log.Printf("lcmd: direct signal failed pid=%d pgid=%d: %v; retrying under workspace identity", lcmd.cmd.Process.Pid, lcmd.processGroupID(), err)
		if helperErr := lcmd.signalWorkspaceProcessGroup(lcmd.closeSignal, lcmd.closeTimeout); helperErr != nil {
			log.Printf("lcmd: workspace-identity process cleanup failed pid=%d pgid=%d: %v", lcmd.cmd.Process.Pid, lcmd.processGroupID(), helperErr)
			return nil
		}
		if lcmd.ptyClosed != nil {
			<-lcmd.ptyClosed
		}
		return nil
	}
	var timer *time.Timer
	var timeout <-chan time.Time
	if lcmd.closeTimeout >= 0 {
		timer = time.NewTimer(lcmd.closeTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// cmd.Wait only tracks the session leader. It may exit on SIGINT while a
	// child in the same process group ignores the signal and keeps running.
	// Keep checking the group after the leader is reaped; only finish when the
	// whole group is gone or the grace period expires.
	leaderExited := lcmd.ptyClosed
	for {
		if !lcmd.processGroupExists() {
			if leaderExited != nil {
				<-leaderExited
			}
			log.Printf("lcmd: terminal process group exited pgid=%d", lcmd.processGroupID())
			return nil
		}
		select {
		case <-leaderExited:
			leaderExited = nil
		case <-ticker.C:
		case <-timeout:
			log.Printf("lcmd: terminal process group did not exit after %s; sending SIGKILL pgid=%d", lcmd.closeTimeout, lcmd.processGroupID())
			if err := lcmd.signalProcessGroup(syscall.SIGKILL); err != nil {
				log.Printf("lcmd: direct SIGKILL failed pid=%d pgid=%d: %v; retrying under workspace identity", lcmd.cmd.Process.Pid, lcmd.processGroupID(), err)
				if helperErr := lcmd.signalWorkspaceProcessGroup(syscall.SIGKILL, 0); helperErr != nil {
					log.Printf("lcmd: workspace-identity SIGKILL failed pid=%d pgid=%d: %v", lcmd.cmd.Process.Pid, lcmd.processGroupID(), helperErr)
					return nil
				}
			}
			if leaderExited != nil {
				<-leaderExited
			}
			log.Printf("lcmd: terminal process group kill sent pgid=%d", lcmd.processGroupID())
			return nil
		}
	}
}

// signalWorkspaceProcessGroup runs cleanup as the REPL's workspace identity.
// It is used only when the ordinary service-credential signal was denied.
func (lcmd *LocalCommand) signalWorkspaceProcessGroup(signal syscall.Signal, grace time.Duration) error {
	launcher, err := workspaceCleanupLauncher(lcmd.identity, lcmd.cmd.Process.Pid, lcmd.processGroupID(), signal, grace)
	if err != nil {
		return err
	}
	cmd := exec.Command(launcher[0], launcher[1:]...)
	// The cleanup helper needs no application or user environment.
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		log.Printf("lcmd: workspace process cleanup pid=%d pgid=%d: %s", lcmd.cmd.Process.Pid, lcmd.processGroupID(), strings.TrimSpace(string(output)))
	}
	if err != nil {
		return errors.Wrap(err, "workspace cleanup helper failed")
	}
	return nil
}

func workspaceCleanupLauncher(id workspace.Identity, pid, pgid int, signal syscall.Signal, grace time.Duration) ([]string, error) {
	setpriv, err := exec.LookPath("setpriv")
	if err != nil {
		return nil, errors.Wrap(err, "setpriv is required to clean up workspace processes")
	}
	target := strconv.Itoa(pgid)
	if pgid == pid {
		target = "-" + target
	} else {
		log.Printf("lcmd: unexpected process group for terminal pid=%d pgid=%d; cleanup targets only the terminal pid", pid, pgid)
		target = strconv.Itoa(pid)
	}
	graceTenths := "0"
	if grace < 0 {
		graceTenths = "-1"
	} else if grace > 0 {
		graceTenths = strconv.FormatInt(int64((grace+100*time.Millisecond-1)/(100*time.Millisecond)), 10)
	}
	const cleanupScript = `
target=$1
signal=$2
grace=$3
if ! kill -"$signal" -- "$target" 2>/dev/null; then
  echo "unable to send $signal to $target" >&2
  exit 1
fi
if [ "$grace" -lt 0 ]; then
  while kill -0 -- "$target" 2>/dev/null; do sleep 0.1; done
  exit 0
fi
count=0
while kill -0 -- "$target" 2>/dev/null; do
  if [ "$count" -ge "$grace" ]; then
    echo "process group did not exit after grace period; sending SIGKILL to $target" >&2
    kill -9 -- "$target" 2>/dev/null || {
      echo "unable to send SIGKILL to $target" >&2
      exit 1
    }
    exit 0
  fi
  sleep 0.1
  count=$((count + 1))
done
`
	return []string{setpriv,
		"--reuid=" + strconv.FormatUint(uint64(id.UID), 10),
		"--regid=" + strconv.FormatUint(uint64(id.GID), 10),
		"--clear-groups", "--inh-caps=-all", "--ambient-caps=-all", "--no-new-privs", "--",
		"/bin/sh", "-c", cleanupScript, "openrepl-workspace-cleanup",
		target, strconv.Itoa(int(signal)), graceTenths,
	}, nil
}

func (lcmd *LocalCommand) processGroupID() int {
	if lcmd.pgid > 0 {
		return lcmd.pgid
	}
	if lcmd.cmd != nil && lcmd.cmd.Process != nil {
		return lcmd.cmd.Process.Pid
	}
	return 0
}

func (lcmd *LocalCommand) processGroupExists() bool {
	if lcmd.cmd == nil || lcmd.cmd.Process == nil {
		return false
	}
	if lcmd.processGroupID() != lcmd.cmd.Process.Pid {
		return lcmd.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
	err := syscall.Kill(-lcmd.processGroupID(), 0)
	return err == nil || err == syscall.EPERM
}

func (lcmd *LocalCommand) signalProcessGroup(signal syscall.Signal) error {
	if lcmd.cmd == nil || lcmd.cmd.Process == nil {
		return nil
	}
	pid := lcmd.cmd.Process.Pid
	pgid := lcmd.processGroupID()
	// pty.Start should make the command the leader of a private session and
	// process group. Refuse to signal a different group, which could be shared
	// with GoTTY or another terminal if startup attributes were not applied.
	if pgid != pid {
		if err := lcmd.cmd.Process.Signal(signal); err != nil {
			log.Printf("lcmd: unable to signal terminal pid=%d with %s: %v", pid, signal, err)
			return err
		}
		return nil
	}
	if err := syscall.Kill(-pgid, signal); err != nil {
		log.Printf("lcmd: unable to signal terminal process group pgid=%d with %s: %v", pgid, signal, err)
		if processErr := lcmd.cmd.Process.Signal(signal); processErr != nil {
			log.Printf("lcmd: unable to signal terminal pid=%d with %s: %v", pid, signal, processErr)
			return processErr
		}
	}
	return nil
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
