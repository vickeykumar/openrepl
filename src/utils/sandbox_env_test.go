package utils

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHomeDirFromSetting(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/tmp/home/",
		"   ":                    "/tmp/home/",
		"/home/openrepl":         "/home/openrepl/",
		"/home/openrepl/":        "/home/openrepl/",
		"/srv/data/../openrepl/": "/srv/openrepl/",
	} {
		got, err := HomeDirFromSetting(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"relative/dir", "home", "/", "/tmp", "/tmp/", "/home", "/root", "/etc", "/usr", "/opt", "/proc", "/dev", "/home/.."} {
		if got, err := HomeDirFromSetting(bad); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}

func TestInitHomeDirUsesTheSettingAndMakesTheFolder(t *testing.T) {
	saved := HOME_DIR
	defer func() { HOME_DIR = saved }()
	dir := filepath.Join(t.TempDir(), "workspaces")
	os.Setenv(EnvHomeDir, dir)
	defer os.Unsetenv(EnvHomeDir)
	InitHomeDir()
	if HOME_DIR != dir+"/" {
		t.Errorf("HOME_DIR is %q", HOME_DIR)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("the folder was not made: %v", err)
	}
	os.Setenv(EnvHomeDir, "relative")
	InitHomeDir()
	if HOME_DIR != "/tmp/home/" {
		t.Errorf("a bad setting left HOME_DIR as %q, want the default", HOME_DIR)
	}
}

func TestLandlockMode(t *testing.T) {
	defer os.Unsetenv(EnvLandlock)
	for in, want := range map[string]string{
		"": LandlockAuto, "auto": LandlockAuto, "AUTO": LandlockAuto, " require ": LandlockRequire,
		"off": LandlockOff, "false": LandlockOff, "nonsense": LandlockAuto,
	} {
		os.Setenv(EnvLandlock, in)
		if got := LandlockMode(); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestTmpMaxAge(t *testing.T) {
	defer os.Unsetenv(EnvTmpMaxAge)
	for in, want := range map[string]time.Duration{"": 0, "1h": time.Hour, "90m": 90 * time.Minute, "0": 0, "nonsense": 0, "-1h": 0} {
		os.Setenv(EnvTmpMaxAge, in)
		if got := TmpMaxAge(); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

func TestCleanDirDeletesOnlyWhatIsOldAndKeepsWhatIsInUse(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old, fresh := now.Add(-3*time.Hour), now.Add(-5*time.Minute)
	mk := func(rel string, isDir bool, mtime time.Time) {
		p := filepath.Join(dir, rel)
		if isDir {
			os.MkdirAll(p, 0755)
		} else {
			os.MkdirAll(filepath.Dir(p), 0755)
			ioutil.WriteFile(p, []byte("x"), 0644)
		}
		os.Chtimes(p, mtime, mtime)
	}
	mk("old.txt", false, old)
	mk("fresh.txt", false, fresh)
	mk("oldfolder/a.txt", false, old)
	os.Chtimes(filepath.Join(dir, "oldfolder"), old, old)
	// a folder made long ago that something is still working in
	mk("busy/new.txt", false, fresh)
	os.Chtimes(filepath.Join(dir, "busy"), old, old)
	mk("go_cache/x", false, old)
	os.Chtimes(filepath.Join(dir, "go_cache"), old, old)
	mk("home/guest-1/f", false, old)
	os.Chtimes(filepath.Join(dir, "home"), old, old)

	keep := func(p string) bool { return filepath.Base(p) == "go_cache" || filepath.Base(p) == "home" }
	if n := CleanDir(dir, time.Hour, now, keep); n != 2 {
		t.Errorf("removed %d, want 2 (old.txt and oldfolder)", n)
	}
	for name, exists := range map[string]bool{"old.txt": false, "oldfolder": false, "fresh.txt": true, "busy": true, "go_cache": true, "home": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != exists {
			t.Errorf("%s: exists=%v, want %v", name, err == nil, exists)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("the folder itself was removed")
	}
}

func TestTmpKeepKeepsTheWorkspacesFolder(t *testing.T) {
	saved := HOME_DIR
	defer func() { HOME_DIR = saved }()
	HOME_DIR = "/tmp/home/"
	keep := tmpKeep()
	for path, want := range map[string]bool{"/tmp/home": true, "/tmp/go_cache": true, "/tmp/other": false, "/tmp/homework": false} {
		if got := keep(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}
