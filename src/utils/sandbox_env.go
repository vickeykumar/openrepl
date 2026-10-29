package utils

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Settings of where the users' files live and of the filesystem sandbox
// (LLD 14). Like every OPENREPL_* setting they may come from an env file, so
// they are read after it is loaded (InitHomeDir is called by main), not at
// start-up of the program.
const (
	// EnvHomeDir is the folder that holds the workspaces (one folder per user or
	// guest). Default /tmp/home/. It must be the same on a gateway and its workers:
	// the gateway sends a worker the path of the workspace.
	EnvHomeDir = "OPENREPL_HOME_DIR"
	// EnvLandlock is the mode of the filesystem sandbox: auto (the default: on
	// where the kernel has Landlock, else a visible status and no sandbox), off,
	// or require (a REPL does not start without it).
	EnvLandlock = "OPENREPL_LANDLOCK"
	// EnvTmpMaxAge, a duration such as 1h, is how long a file may stay in /tmp
	// before the server deletes it. Unset or 0 means never. The Docker image sets it.
	EnvTmpMaxAge = "OPENREPL_TMP_MAX_AGE"
)

const defaultHomeDir = "/tmp/home/"

// HOME_DIR holds the workspaces. It ends with a slash. It is the default until
// InitHomeDir reads OPENREPL_HOME_DIR.
var HOME_DIR = defaultHomeDir

// folders that can never be the folder of the workspaces: the server deletes
// below HOME_DIR, and a REPL may read and write below it
var forbiddenHomeDirs = map[string]bool{
	"/": true, "/tmp": true, "/var": true, "/var/tmp": true, "/home": true, "/root": true,
	"/etc": true, "/usr": true, "/opt": true, "/bin": true, "/sbin": true, "/lib": true,
	"/lib64": true, "/proc": true, "/dev": true, "/sys": true, "/run": true, "/mnt": true,
	"/boot": true, "/srv": true,
}

// HomeDirFromSetting turns the value of OPENREPL_HOME_DIR into HOME_DIR: an
// absolute folder, cleaned, with a trailing slash. Empty means the default.
func HomeDirFromSetting(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultHomeDir, nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path, not %q", EnvHomeDir, value)
	}
	clean := filepath.Clean(value)
	if forbiddenHomeDirs[clean] {
		return "", fmt.Errorf("%s cannot be %s: it holds more than the workspaces", EnvHomeDir, clean)
	}
	return clean + "/", nil
}

// InitHomeDir sets HOME_DIR from OPENREPL_HOME_DIR and makes the folder. A bad
// value is logged and the default is used.
func InitHomeDir() {
	dir, err := HomeDirFromSetting(os.Getenv(EnvHomeDir))
	if err != nil {
		log.Printf("ERROR: %v; using %s", err, defaultHomeDir)
		dir = defaultHomeDir
	}
	HOME_DIR = dir
	if err := os.MkdirAll(HOME_DIR, 0777); err != nil {
		log.Printf("ERROR: cannot make the workspace folder %s: %v", HOME_DIR, err)
		return
	}
	os.Chmod(HOME_DIR, 0777)
}

// Modes of the filesystem sandbox.
const (
	LandlockAuto    = "auto"
	LandlockOff     = "off"
	LandlockRequire = "require"
)

// LandlockMode is OPENREPL_LANDLOCK: auto, off or require. Anything else (and
// nothing) is auto.
func LandlockMode() string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvLandlock))); v {
	case LandlockOff, "0", "false", "no":
		return LandlockOff
	case LandlockRequire, "required", "on-strict":
		return LandlockRequire
	case "", LandlockAuto, "on", "1", "true", "yes":
		return LandlockAuto
	default:
		log.Printf("ERROR: %s=%q is not auto, off or require; using auto", EnvLandlock, v)
		return LandlockAuto
	}
}

// TmpMaxAge is OPENREPL_TMP_MAX_AGE, or 0 for "never delete".
func TmpMaxAge() time.Duration {
	v := strings.TrimSpace(os.Getenv(EnvTmpMaxAge))
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		log.Printf("ERROR: %s=%q is not a duration such as 1h; /tmp is not cleaned", EnvTmpMaxAge, v)
		return 0
	}
	return d
}
