package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Installer makes bundles. The real one runs micromamba against conda-forge; a test, or a dev server without micromamba, uses
// the fake. The server runs it outside any terminal's sandbox, so a terminal needs no network for an install.
type Installer interface {
	// Resolve says which version of a package an install would get now (the newest that works with the server's Python).
	Resolve(ctx context.Context, name string) (string, error)
	// Install makes the finished contents of a bundle in dest (which does not exist yet); what it says goes to log. It does not
	// write the marker: the caller does, after its own checks.
	Install(ctx context.Context, name, version, dest string, log io.Writer) error
	// Name says which installer this is, for the admin page.
	Name() string
}

// ---- micromamba ----------------------------------------------------------------------------------------------------------

// Mamba installs from conda-forge with micromamba. Every bundle is a whole conda prefix made of hard links to the shared cache
// in PkgsDir, so a package that two bundles need is on the disk once.
type Mamba struct {
	Bin     string // the micromamba program
	Dir     string // the store (its pkgs folder is the cache)
	Python  string // the Python of the server, as "3.10": every bundle is solved with it
	Timeout time.Duration
}

func (m Mamba) Name() string { return "micromamba (conda-forge)" }

func (m Mamba) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, m.Bin, args...)
	c.Env = append(os.Environ(),
		"MAMBA_ROOT_PREFIX="+filepath.Join(m.Dir, "mamba"),
		"CONDA_PKGS_DIRS="+filepath.Join(m.Dir, "pkgs"),
		"MAMBA_NO_BANNER=1", "MAMBA_NO_RC=1", "CONDARC=/dev/null")
	return c
}

func (m Mamba) py() string {
	if m.Python == "" {
		return "3.10"
	}
	return m.Python
}

func (m Mamba) common() []string {
	return []string{"--override-channels", "-c", "conda-forge", "--no-rc", "-y", "python=" + m.py()}
}

func (m Mamba) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	t := m.Timeout
	if t == 0 {
		t = 15 * time.Minute
	}
	return context.WithTimeout(ctx, t)
}

func (m Mamba) Resolve(ctx context.Context, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q is not a package name", name)
	}
	ctx, cancel := m.withTimeout(ctx)
	defer cancel()
	args := append([]string{"create", "--dry-run", "--json", "-n", "openrepl-probe"}, m.common()...)
	args = append(args, name)
	var out, errb bytes.Buffer
	c := m.cmd(ctx, args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("conda-forge could not solve %s: %s", name, tail(errb.String()+out.String(), 600))
	}
	return versionFromSolve(out.Bytes(), name)
}

// versionFromSolve reads the version of a package from the JSON of a dry run.
func versionFromSolve(raw []byte, name string) (string, error) {
	var res struct {
		Actions struct {
			Link  []solvedPkg `json:"LINK"`
			Fetch []solvedPkg `json:"FETCH"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", errors.New("the answer of micromamba could not be read")
	}
	for _, list := range [][]solvedPkg{res.Actions.Link, res.Actions.Fetch} {
		for _, p := range list {
			if p.Name == name && ValidVersion(p.Version) {
				return p.Version, nil
			}
		}
	}
	return "", fmt.Errorf("conda-forge has no %s for this server's Python", name)
}

type solvedPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (m Mamba) Install(ctx context.Context, name, version, dest string, log io.Writer) error {
	if !ValidName(name) || !ValidVersion(version) {
		return fmt.Errorf("%q %q is not a package", name, version)
	}
	ctx, cancel := m.withTimeout(ctx)
	defer cancel()
	args := append([]string{"create", "-p", dest}, m.common()...)
	args = append(args, name+"="+version)
	c := m.cmd(ctx, args...)
	c.Stdout, c.Stderr = log, log
	return c.Run()
}

// ---- the fake ------------------------------------------------------------------------------------------------------------

// Fake makes small bundles with a Python module of the package's name, for tests and for a dev server that has no micromamba.
// Failing names fail, as a conda install that cannot be solved does.
type Fake struct {
	Version string          // what every package resolves to, "1.0.0" when empty
	Fail    map[string]bool // names that cannot be installed
	Delay   time.Duration
}

func (f Fake) Name() string { return "fake (tests and development)" }

func (f Fake) ver() string {
	if f.Version == "" {
		return "1.0.0"
	}
	return f.Version
}

func (f Fake) Resolve(ctx context.Context, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q is not a package name", name)
	}
	if f.Fail[name] {
		return "", fmt.Errorf("conda-forge could not solve %s: python 3.10.* is not compatible with %s (fake)", name, name)
	}
	return f.ver(), nil
}

func (f Fake) Install(ctx context.Context, name, version, dest string, log io.Writer) error {
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.Fail[name] {
		fmt.Fprintf(log, "Solving environment: failed\nFound conflicts! %s is not compatible with python 3.10.*\n", name)
		return errors.New("the solver failed")
	}
	mod := strings.NewReplacer("-", "_", ".", "_").Replace(name)
	site := filepath.Join(dest, "lib", "python3.10", "site-packages", mod)
	if err := os.MkdirAll(site, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(site, "__init__.py"), []byte(fmt.Sprintf("__version__ = %q\nNAME = %q\n", version, name)), 0644); err != nil {
		return err
	}
	bin := filepath.Join(dest, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	script := "#!/bin/sh\necho \"" + name + " " + version + "\"\n"
	return os.WriteFile(filepath.Join(bin, name+"-hello"), []byte(script), 0755)
}

// tail is the last n bytes of s, from a line start.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return s
}

// CleanLog makes a log fit to show and to send to an admin: the last lines, with the store's folders shortened and no secrets.
func CleanLog(s, dir string) string {
	s = strings.ReplaceAll(s, dir, "/store")
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		s = strings.ReplaceAll(s, h, "~")
	}
	return tail(s, 1500)
}

// ---- no installer --------------------------------------------------------------------------------------------------------

// Missing is the installer of a server that has no micromamba: every install says why it cannot be done.
type Missing struct{ Reason string }

func (m Missing) Name() string { return "none: " + m.Reason }
func (m Missing) Resolve(ctx context.Context, name string) (string, error) {
	return "", errors.New(m.Reason)
}
func (m Missing) Install(ctx context.Context, name, version, dest string, log io.Writer) error {
	return errors.New(m.Reason)
}

// PythonOf asks a python3 program for its "major.minor" (so that every bundle is solved with the Python of the server).
func PythonOf(bin string) string {
	out, err := exec.Command(bin, "-c", "import sys;print('%d.%d' % sys.version_info[:2])").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || !regexpMinor.MatchString(v) {
		return ""
	}
	return v
}

var regexpMinor = regexp.MustCompile(`^3\.[0-9]{1,2}$`)
