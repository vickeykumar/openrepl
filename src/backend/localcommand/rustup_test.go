package localcommand

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheRustToolchainsAreFoundFromTheServersHomeNotTheWorkspace(t *testing.T) {
	home := t.TempDir()
	os.Setenv("HOME", home)
	os.Unsetenv("RUSTUP_HOME")
	if got := rustupHome(); got != "" {
		t.Errorf("no toolchains below the home, but got %q", got)
	}
	os.Mkdir(filepath.Join(home, ".rustup"), 0755)
	if got := rustupHome(); got != filepath.Join(home, ".rustup") {
		t.Errorf("the toolchains below the home were not found: %q", got)
	}
	os.Setenv("RUSTUP_HOME", "/somewhere/else")
	defer os.Unsetenv("RUSTUP_HOME")
	if got := rustupHome(); got != "/somewhere/else" {
		t.Errorf("the setting of the environment was not kept: %q", got)
	}
}
