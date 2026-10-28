//go:build linux

// Package childreaper makes the server a Linux child subreaper and collects
// orphaned descendants without stealing processes owned by exec.Cmd.
package childreaper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const prSetChildSubreaper = 36

var children = struct {
	sync.Mutex
	pids map[int]struct{}
}{pids: make(map[int]struct{})}

// Init enables Linux orphan adoption and starts a reaper for children not
// currently owned by an exec.Cmd. Call it before starting any subprocesses.
func Init() error {
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("enable child subreaper: %w", errno)
	}
	go reapLoop()
	return nil
}

// Start starts cmd and records it so the orphan reaper cannot consume its
// exit status before cmd.Wait does.
func Start(cmd *exec.Cmd) error {
	children.Lock()
	defer children.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	children.pids[cmd.Process.Pid] = struct{}{}
	return nil
}

// Wait waits for a command started with Start and releases its PID from the
// protected set only after exec.Cmd has reaped it.
func Wait(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if cmd.Process != nil {
		children.Lock()
		delete(children.pids, cmd.Process.Pid)
		children.Unlock()
	}
	return err
}

// Run is the tracked equivalent of exec.Cmd.Run.
func Run(cmd *exec.Cmd) error {
	if err := Start(cmd); err != nil {
		return err
	}
	return Wait(cmd)
}

// Output is the tracked equivalent of exec.Cmd.Output.
func Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("childreaper: stdout already set")
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	captureStderr := cmd.Stderr == nil
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	if err := Start(cmd); err != nil {
		return nil, err
	}
	err := Wait(cmd)
	if exitErr, ok := err.(*exec.ExitError); ok && captureStderr {
		exitErr.Stderr = []byte(stderr.String())
	}
	return []byte(stdout.String()), err
}

func reapLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		reapOrphans()
	}
}

func reapOrphans() {
	children.Lock()
	defer children.Unlock()

	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return
	}
	for _, task := range tasks {
		pids, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if err != nil {
			continue
		}
		for _, item := range strings.Fields(string(pids)) {
			pid, err := strconv.Atoi(item)
			if err != nil {
				continue
			}
			if _, owned := children.pids[pid]; owned {
				continue
			}
			var status syscall.WaitStatus
			if waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err == nil && waited > 0 {
				// The child has been reaped. It was not registered to an exec.Cmd.
			}
		}
	}
}
