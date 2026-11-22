package utils

import (
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
	for _, want := range []string{"GOPATH=/h/go", "GEM_HOME=/h/.gem", "NPM_CONFIG_PREFIX=/h/.npm-global"} {
		if !strings.Contains(env, want) {
			t.Errorf("%q is not in %s", want, env)
		}
	}
	if strings.Contains(env, "GEM_PATH") {
		t.Error("GEM_PATH must stay unset: the system gems are found through it")
	}
	bins := strings.Join(UserBinDirs("/h"), ":")
	if bins != "/h/go/bin:/h/.gem/bin:/h/.npm-global/bin:/h/.local/bin" {
		t.Errorf("bin folders: %s", bins)
	}
}
