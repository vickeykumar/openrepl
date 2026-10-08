package containers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"utils"
)

func TestNamespacesWeAreInAlreadyAreNotEntered(t *testing.T) {
	if _, err := os.Stat("/proc/self/ns/user"); err != nil {
		t.Skip("no /proc/self/ns here")
	}
	// a process in the same namespaces as this one (this process itself): nothing to enter
	if flags := differingNamespaceFlags(os.Getpid()); len(flags) != 0 {
		t.Fatalf("flags for our own namespaces: %v", flags)
	}
	// a process that cannot be looked at: all of them, as before
	if flags := differingNamespaceFlags(0x7ffffff0); len(flags) != len(ns_flags) {
		t.Fatalf("flags for a process that is not there: %v", flags)
	}
}

// The REPL of "java" is jshell, with small memory limits; Run (a compile
// request) and every other language are not affected.
func TestJavaTerminalRunsATunedJshell(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"jshell", "java", "cling"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	got := GetCommandArgs("java", []string{"extra"}, -1, map[string][]string{})
	if len(got) < 4 || got[0] != filepath.Join(dir, "jshell") || got[1] != "--execution" || got[2] != "local" || got[len(got)-1] != "extra" {
		t.Fatalf("java terminal: %v", got)
	}
	has := func(flag string) bool {
		for _, a := range got {
			if a == flag {
				return true
			}
		}
		return false
	}
	for _, flag := range []string{"-J-Xmx48m", "-J-XX:+UseSerialGC", "-J-XX:TieredStopAtLevel=1"} {
		if !has(flag) {
			t.Errorf("%s missing from %v", flag, got)
		}
	}

	// another language: its own program, nothing added
	if got := GetCommandArgs("cling", []string{"-xc"}, -1, map[string][]string{}); len(got) != 2 || got[0] != filepath.Join(dir, "cling") || got[1] != "-xc" {
		t.Errorf("cling terminal: %v", got)
	}

	// Run: the compile script through bash, not jshell
	run := map[string][]string{utils.IdeLangKey: {"java"}, utils.IdeContentKey: {"x"}}
	for _, a := range GetCommandArgs("java", nil, -1, run) {
		if strings.Contains(a, "jshell") {
			t.Errorf("a Run request started jshell: %v", a)
		}
	}

	// jshell missing: the command itself, as before
	if err := os.Remove(filepath.Join(dir, "jshell")); err != nil {
		t.Fatal(err)
	}
	if got := GetCommandArgs("java", nil, -1, map[string][]string{}); len(got) != 1 || got[0] != filepath.Join(dir, "java") {
		t.Errorf("without jshell: %v", got)
	}
}
