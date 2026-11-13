//go:build linux

package server

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"syscall"
	"time"

	"containers"
	"netemu"
	"utils"
)

// runShell runs `bash -c command` in the workspace home, under the same rules as the terminal's bash: the filesystem sandbox (landlock), the
// private network, the namespaces and the memory limit of the "bash" REPL. No input (/dev/null) and no terminal (its own session), so a program
// that asks a question ends. After the time limit, or when ctx ends (the user pressed Stop, or closed the page), the command and everything it
// started are killed.
func runShell(ctx context.Context, home, command string, timeout time.Duration) (shellResult, error) {
	os.MkdirAll(home, 0o755)
	params := url.Values{} // not an admin's, not a compilation: the sandbox applies
	if err := containers.LandlockCheck(); err != nil {
		return shellResult{}, err
	}
	if err := containers.NetEmuCheck(); err != nil {
		return shellResult{}, err
	}
	args := containers.GetCommandArgsIn("bash", []string{"-c", command}, -1, params, home)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = home
	keep := &outputKeeper{}
	cmd.Stdout, cmd.Stderr = keep, keep
	cmd.Stdin = nil
	// not the server's own environment: its secrets must not reach the command
	env := utils.ChildEnviron(os.Environ())
	env = append(env, "TERM=dumb", "HOME="+home, "HOSTNAME=bash", "GOPATH=/opt/gotty/", "GOCACHE=/tmp/go_cache/.cache/go-build/", "GCC_EXEC_PREFIX=/usr/lib/gcc/")
	env = append(env, "PATH="+os.Getenv("PATH")+string(os.PathListSeparator)+"/opt/gotty/bin")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	containers.AddContainerAttributes("bash", cmd.SysProcAttr, params)
	// the network emulation: the helper tells this process, over descriptor 3, where to send the notifications of the command's network calls
	var netParent, netChild *os.File
	if containers.WantsNetSocket(args) {
		var err error
		if netParent, netChild, err = netemu.NewSocketPair(); err != nil {
			return shellResult{}, err
		}
		cmd.ExtraFiles = []*os.File{netChild}
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		if netParent != nil {
			netParent.Close()
			netChild.Close()
		}
		return shellResult{}, err
	}
	pid := cmd.Process.Pid
	if netParent != nil {
		netChild.Close()
		netemu.Attach(netParent, home, pid)
	}
	if c, ok := containers.Containers["bash"]; ok {
		c.AddProcesstoNewSubCgroup(pid, false) // the memory limit of the bash REPL
		go containers.EnableNetworking(pid)
		defer c.DeleteProcessFromSubCgroup(pid)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
	var werr error
	select {
	case werr = <-done:
	case <-timer.C:
		timedOut = true
		syscall.Kill(-pid, syscall.SIGKILL)
		cmd.Process.Kill()
		werr = <-done
	case <-ctx.Done():
		syscall.Kill(-pid, syscall.SIGKILL)
		cmd.Process.Kill()
		werr = <-done
	}
	syscall.Kill(-pid, syscall.SIGKILL) // what the command left behind
	code := 0
	if werr != nil {
		code = -1
		if ee, ok := werr.(*exec.ExitError); ok {
			code = ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				code = 128 + int(ws.Signal())
			}
		}
	}
	out, truncated := keep.text()
	return shellResult{ExitCode: code, Output: out, Truncated: truncated, TimedOut: timedOut, DurationMS: time.Since(start).Milliseconds()}, nil
}
