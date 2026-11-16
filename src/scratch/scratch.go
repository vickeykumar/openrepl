// Package scratch is the scratch folder of a terminal (LLD 18, "Scratch"): a folder outside the workspace, outside /opt, made for one
// terminal when it starts, that holds what a program writes only to run faster or to build: bytecode, caches, build folders, temporary
// files. It is never synced, never snapshotted and is removed when the terminal ends. The sandbox lets the terminal write in its own folder
// only. The size has a soft cap: the server measures the folder and clears the caches first, then stops a terminal that still fills it.
// There is no mount and no hard quota, so no privilege is needed.
package scratch

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultRoot is where the scratch folders are, unless OPENREPL_SCRATCH_DIR says another place.
const DefaultRoot = "/scratch"

var (
	userPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	termPattern = regexp.MustCompile(`^t-[0-9a-f]{8}$`)
)

// pidFile says which process the folder belongs to: "<pid> <start time>" (the start time tells a process from a later one that got the
// same number).
const pidFile = ".pid"

// Make creates the folder of a new terminal of a user: <root>/<user>/t-<random>, with the folders that the variables point at. It writes
// the pid of the calling process (the terminal's shell takes its place when the helper runs it, so the pid stays the same).
func Make(root, user string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || !userPattern.MatchString(user) || strings.Contains(user, "..") {
		return "", fmt.Errorf("scratch: not usable: %q %q", root, user)
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(root, user, "t-"+hex.EncodeToString(b[:]))
	if err := os.MkdirAll(filepath.Dir(dir), 0711); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	for _, sub := range []string{"tmp", "gotmp", "state", "build", "cache"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			return "", err
		}
	}
	pid := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, pidFile), []byte(fmt.Sprintf("%d %s\n", pid, startTime(pid))), 0600); err != nil {
		return "", err
	}
	return dir, nil
}

// Vars are the variables a terminal gets (NAME=value) so that its caches and builds go to its scratch folder. The include paths of the
// apps (PYTHONPATH, NODE_PATH ...) are not here: they point at the read-only store.
func Vars(dir string) []string {
	c := filepath.Join(dir, "cache")
	return []string{
		"OPENREPL_SCRATCH=" + dir,
		"PYTHONPYCACHEPREFIX=" + filepath.Join(c, "pycache"),
		"MPLCONFIGDIR=" + filepath.Join(c, "mpl"),
		"NUMBA_CACHE_DIR=" + filepath.Join(c, "numba"),
		"MYPY_CACHE_DIR=" + filepath.Join(c, "mypy"),
		"RUFF_CACHE_DIR=" + filepath.Join(c, "ruff"),
		"GOCACHE=" + filepath.Join(c, "go-build"),
		"GOTMPDIR=" + filepath.Join(dir, "gotmp"),
		"CARGO_TARGET_DIR=" + filepath.Join(dir, "build", "cargo-target"),
		"EVCXR_TMPDIR=" + filepath.Join(dir, "build", "evcxr"),
		"EVCXR_TARGET_DIR=" + filepath.Join(dir, "build", "evcxr"),
		"npm_config_cache=" + filepath.Join(c, "npm"),
		"CCACHE_DIR=" + filepath.Join(c, "ccache"),
		"TMPDIR=" + filepath.Join(dir, "tmp"),
		"XDG_CACHE_HOME=" + filepath.Join(c, "xdg"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"),
		"TMUX_TMPDIR=" + filepath.Join(dir, "tmp"),
	}
}

// JavaOption is added to JAVA_TOOL_OPTIONS (java has no variable for its temporary folder).
func JavaOption(dir string) string { return "-Djava.io.tmpdir=" + filepath.Join(dir, "tmp") }

// Usage is the physical size of a folder in bytes (a file with several hard links counts once).
func Usage(dir string) int64 {
	var n int64
	seen := map[[2]uint64]bool{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			k := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			n += st.Blocks * 512
		} else {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// ClearCaches empties the folders that are only caches (what the first thing to go is when the soft cap is reached).
func ClearCaches(dir string) {
	for _, sub := range []string{"cache", "gotmp"} {
		p := filepath.Join(dir, sub)
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			os.RemoveAll(filepath.Join(p, e.Name()))
		}
	}
}

// Terminal is one terminal's folder.
type Terminal struct {
	User string
	Dir  string
	PID  int
	Live bool
}

// startTime is the start time of a process (field 22 of /proc/<pid>/stat), "" when the process is not there.
func startTime(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return ""
	}
	return f[19]
}

func owner(dir string) (pid int, start string) {
	raw, err := os.ReadFile(filepath.Join(dir, pidFile))
	if err != nil {
		return 0, ""
	}
	f := strings.Fields(string(raw))
	if len(f) < 1 {
		return 0, ""
	}
	pid, _ = strconv.Atoi(f[0])
	if len(f) > 1 {
		start = f[1]
	}
	return pid, start
}

// alive says whether the terminal of a folder is still running. On a system without /proc a folder is never taken for dead (the sweep
// then leaves it).
func alive(pid int, start string) bool {
	if pid <= 0 {
		return false
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		return true
	}
	now := startTime(pid)
	return now != "" && (start == "" || now == start)
}

// List lists the folders under root.
func List(root string) []Terminal {
	var out []Terminal
	users, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, u := range users {
		if !u.IsDir() || !userPattern.MatchString(u.Name()) {
			continue
		}
		ts, _ := os.ReadDir(filepath.Join(root, u.Name()))
		for _, t := range ts {
			if !t.IsDir() || !termPattern.MatchString(t.Name()) {
				continue
			}
			dir := filepath.Join(root, u.Name(), t.Name())
			pid, start := owner(dir)
			out = append(out, Terminal{User: u.Name(), Dir: dir, PID: pid, Live: alive(pid, start)})
		}
	}
	return out
}

// Sweep removes the folders of terminals that have ended, and the empty folders of users. It returns how many terminals it removed.
func Sweep(root string) int {
	n := 0
	for _, t := range List(root) {
		if !t.Live {
			// a folder that has no pid yet is one that is being made: leave it for a minute
			if fi, err := os.Stat(t.Dir); t.PID == 0 && err == nil && time.Since(fi.ModTime()) < time.Minute {
				continue
			}
			if os.RemoveAll(t.Dir) == nil {
				n++
			}
		}
	}
	users, _ := os.ReadDir(root)
	for _, u := range users {
		if u.IsDir() {
			os.Remove(filepath.Join(root, u.Name())) // only when it is empty
		}
	}
	return n
}

// Alive says whether a process is still there (by its number: for the moment right after it was signalled).
func Alive(pid int) bool { return pid > 0 && startTime(pid) != "" }
