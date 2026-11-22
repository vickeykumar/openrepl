package utils

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGoEnvIsInTheWorkspaceAndNeverTheServersFolder(t *testing.T) {
	env := strings.Join(GoEnv("/home/openrepl/ann"), " ")
	for _, want := range []string{"GOPATH=/home/openrepl/ann/go", "GOBIN=/home/openrepl/ann/go/bin", "GOTOOLCHAIN=local"} {
		if !strings.Contains(env, want) {
			t.Errorf("%q is not in %s", want, env)
		}
	}
	if strings.Contains(env, "/opt/gotty") || GoBinDir("/h") != "/h/go/bin" {
		t.Error("the server's folder is not the user's")
	}
}

func TestEveryPackageManagerInstallsIntoTheWorkspace(t *testing.T) {
	env := strings.Join(UserToolEnv("/h"), " ")
	for _, want := range []string{"GOPATH=/h/go", "GEM_HOME=/h/.gem", "NPM_CONFIG_PREFIX=/h/.npm-global", "PERL_MM_OPT=INSTALL_BASE=/h/perl5", "PERL_MB_OPT=--install_base /h/perl5", "PERL5LIB=/h/perl5/lib/perl5", "CARGO_INSTALL_ROOT=/h/.cargo", "PIP_USER=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("%q is not in %s", want, env)
		}
	}
	if strings.Contains(env, "GEM_PATH") {
		t.Error("GEM_PATH must stay unset: the system gems are found through it")
	}
	bins := strings.Join(UserBinDirs("/h"), ":")
	if bins != "/h/go/bin:/h/.gem/bin:/h/.npm-global/bin:/h/.local/bin:/h/perl5/bin:/h/.cargo/bin" {
		t.Errorf("bin folders: %s", bins)
	}
}

// the bash terminal's PIP_USER follows the virtual environment: set outside one, gone inside one
func TestPipUserIsDroppedWhileAVirtualEnvironmentIsActive(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	run := func(venv string) string {
		script := PipBashrc + "\nunset PIP_USER; _openrepl_pip; echo \"[${PIP_USER}]\""
		cmd := exec.Command(bash, "--norc", "-c", script)
		cmd.Env = append(os.Environ(), "VIRTUAL_ENV="+venv)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(""); got != "[1]" {
		t.Errorf("outside a virtual environment: %s", got)
	}
	if got := run("/h/venv"); got != "[]" {
		t.Errorf("inside one: %s", got)
	}
}

// the one-liner `activate && pip install x`: no prompt comes between the two, so pip itself must not get PIP_USER inside a virtual environment
func TestPipInOneLineInAVirtualEnvironmentGetsNoPipUser(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	bin := t.TempDir()
	// a stand-in pip that says what it was given
	if err := os.WriteFile(bin+"/pip", []byte("#!/bin/sh\necho \"[${PIP_USER}] $*\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	run := func(venv string) string {
		cmd := exec.Command(bash, "--norc", "-c", PipBashrc+"\nexport PIP_USER=1; pip install x")
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "VIRTUAL_ENV="+venv)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(""); got != "[1] install x" {
		t.Errorf("outside a virtual environment: %s", got)
	}
	if got := run("/h/venv"); got != "[] install x" {
		t.Errorf("inside one: %s", got)
	}
}
