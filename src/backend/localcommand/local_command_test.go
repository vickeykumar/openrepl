package localcommand

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"workspace"
)

func TestWorkspaceLauncherDropsIdentityAndPreservesArguments(t *testing.T) {
	bin := t.TempDir()
	setpriv := filepath.Join(bin, "setpriv")
	if err := os.WriteFile(setpriv, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	id := workspace.Identity{UID: 1234567890, GID: 1234567890}
	args := []string{"/usr/bin/bash", "-c", "cd \"$HOME/New node\"", "New node/test.go"}
	got, err := workspaceLauncher(id, args, -1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != setpriv {
		t.Fatalf("launcher executable = %q, want %q", got[0], setpriv)
	}
	joined := strings.Join(got, "\x00")
	for _, option := range []string{
		"--reuid=1234567890", "--regid=1234567890", "--clear-groups",
		"--inh-caps=-all", "--ambient-caps=-all", "--no-new-privs", "umask 077",
		"exec \"$@\"",
	} {
		if !strings.Contains(joined, option) {
			t.Errorf("launcher is missing %q: %v", option, got)
		}
	}
	if !reflect.DeepEqual(got[len(got)-len(args):], args) {
		t.Fatalf("command arguments changed: got tail %q, want %q", got[len(got)-len(args):], args)
	}
}

func TestWorkspaceLauncherJoinsNamespacesBeforeDroppingIdentity(t *testing.T) {
	bin := t.TempDir()
	setpriv := filepath.Join(bin, "setpriv")
	if err := os.WriteFile(setpriv, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	command := []string{"/usr/bin/nsenter", "-t123", "--net", "/bin/bash", "New node/test.go"}
	got, err := workspaceLauncher(workspace.Identity{UID: 10001, GID: 10001}, command, 123)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"/usr/bin/nsenter", "-t123", "--net", "--", setpriv}
	if !reflect.DeepEqual(got[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("nsenter must enter parent namespaces before setpriv: got %v", got)
	}
	if !reflect.DeepEqual(got[len(got)-2:], command[len(command)-2:]) {
		t.Fatalf("command args changed after namespace join: %v", got)
	}
	for _, arg := range got[len(wantPrefix):] {
		if arg == "/usr/bin/nsenter" || arg == "-t123" || arg == "--net" {
			t.Fatalf("namespace launcher arguments leaked into user command: %v", got)
		}
	}
}

func TestCloseKillsProcessGroupAfterLeaderExits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses Linux /proc process state")
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("/bin/sh", "-c", `trap 'exit 0' INT; (trap '' INT; exec sleep 30) & echo $! > "$1"; wait`, "test", pidFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ptyClosed := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ptyClosed)
	}()

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child process did not start")
	}

	lcmd := &LocalCommand{
		cmd:          cmd,
		pgid:         cmd.Process.Pid,
		closeSignal:  syscall.SIGINT,
		closeTimeout: 200 * time.Millisecond,
		ptyClosed:    ptyClosed,
	}
	if err := lcmd.Close(); err != nil {
		t.Fatal(err)
	}

	// A zombie has stopped executing and can only be reaped by PID 1; accept
	// that state while ensuring the child did not survive as a live process.
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(childPID), "stat"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read child process state: %v", err)
	}
	if err == nil {
		end := strings.LastIndexByte(string(stat), ')')
		if end < 0 {
			t.Fatalf("could not parse child process state: %q", stat)
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) == 0 {
			t.Fatalf("could not parse child process state: %q", stat)
		}
		state := fields[0]
		if state != "Z" {
			t.Fatalf("child process %d is still running with state %q", childPID, state)
		}
	}
}
