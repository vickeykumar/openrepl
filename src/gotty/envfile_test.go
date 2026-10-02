package main

import (
	"os"
	"testing"

	"utils"
)

func TestEnvFileFromArgs(t *testing.T) {
	old, had := os.LookupEnv(utils.EnvEnvFile)
	os.Unsetenv(utils.EnvEnvFile)
	defer func() {
		if had {
			os.Setenv(utils.EnvEnvFile, old)
		}
	}()

	for _, c := range []struct {
		name     string
		args     []string
		path     string
		explicit bool
	}{
		{"nothing given", nil, "~/.env", false},
		{"only other flags", []string{"-w", "-p", "8080", "--mode=worker"}, "~/.env", false},
		{"two arguments", []string{"--env-file", "/etc/o.env"}, "/etc/o.env", true},
		{"one with =", []string{"--env-file=/etc/o.env"}, "/etc/o.env", true},
		{"single dash", []string{"-env-file", "/etc/o.env"}, "/etc/o.env", true},
		{"after flags that take a value", []string{"-p", "8080", "--worker-token", "t", "--env-file", "x.env"}, "x.env", true},
		{"after a boolean flag", []string{"-w", "--env-file", "x.env"}, "x.env", true},
		{"a boolean flag does not swallow it", []string{"--tls", "--env-file=x.env", "-w"}, "x.env", true},
		{"the last one wins", []string{"--env-file", "a.env", "--env-file", "b.env"}, "b.env", true},
		{"with the config flag", []string{"--config", "c.hcl", "--env-file", "x.env"}, "x.env", true},
		{"belongs to the command being run", []string{"-w", "docker", "run", "--env-file", "x.env"}, "~/.env", false},
		{"after --", []string{"--", "--env-file", "x.env"}, "~/.env", false},
		{"without a value", []string{"--env-file"}, "~/.env", false},
	} {
		path, explicit := envFileFromArgs(c.args)
		if path != c.path || explicit != c.explicit {
			t.Errorf("%s: %v -> %q, explicit=%v; want %q, %v", c.name, c.args, path, explicit, c.path, c.explicit)
		}
	}

	os.Setenv(utils.EnvEnvFile, "/from/env.env")
	defer os.Unsetenv(utils.EnvEnvFile)
	if path, explicit := envFileFromArgs(nil); path != "/from/env.env" || !explicit {
		t.Errorf("GOTTY_ENV_FILE: %q %v", path, explicit)
	}
	if path, _ := envFileFromArgs([]string{"--env-file", "cli.env"}); path != "cli.env" {
		t.Errorf("the command line must win over GOTTY_ENV_FILE: %q", path)
	}
}
