package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// the cargo home is the scratch folder's whether or not the user has vendored crates, so the registry cache is not in the workspace
func TestTheCargoHomeIsTheScratchFoldersEvenWithNoVendoredCrates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENREPL_CRATE_VENDORS", "")
	t.Setenv("CARGO_HOME", "")
	t.Setenv("RUSTUP_HOME", "/somewhere")
	finishEnv(dir, "/home/x", []string{"/usr/bin/evcxr"})
	if got := os.Getenv("CARGO_HOME"); got != filepath.Join(dir, "cargo-home") {
		t.Errorf("CARGO_HOME is %q", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, "cargo-home")); err != nil || !fi.IsDir() {
		t.Error("the cargo home was not made")
	}
	// no scratch folder on this server: nothing is set
	t.Setenv("CARGO_HOME", "")
	finishEnv("", "/home/x", []string{"/usr/bin/evcxr"})
	if os.Getenv("CARGO_HOME") != "" {
		t.Error("without a scratch folder the cargo home is left alone")
	}
}

// Maven's repository goes after the proxy options the server put in MAVEN_OPTS, once
func TestMavenKeepsItsRepositoryInScratchAfterTheProxyOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAVEN_OPTS", "-Dhttps.proxyHost=127.0.0.1")
	dir := makeScratch(root, "/home/openrepl/ann")
	if dir == "" {
		t.Skip("no scratch folder here")
	}
	m := os.Getenv("MAVEN_OPTS")
	if !strings.HasPrefix(m, "-Dhttps.proxyHost=127.0.0.1 ") || !strings.HasSuffix(m, "-Dmaven.repo.local="+filepath.Join(dir, "cache", "m2")) {
		t.Errorf("MAVEN_OPTS is %q", m)
	}
	dir = makeScratch(root, "/home/openrepl/ann") // another terminal of the same person: still one repository option
	if strings.Count(os.Getenv("MAVEN_OPTS"), "maven.repo.local") != 1 {
		t.Error("the repository must be named once")
	}
	if os.Getenv("GRADLE_USER_HOME") != filepath.Join(dir, "cache", "gradle") {
		t.Errorf("GRADLE_USER_HOME is %q", os.Getenv("GRADLE_USER_HOME"))
	}
}
