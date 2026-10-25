package localcommand

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
