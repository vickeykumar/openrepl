package appstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// moduleOf guesses the module a package of this name brings, from what the bundle holds: a folder or a .py file of the package's
// name (dashes and dots become underscores) in its site-packages. "" when there is none (a tool, or a package named otherwise).
func moduleOf(dest, name string) string {
	mod := strings.NewReplacer("-", "_", ".", "_").Replace(name)
	if !identPattern.MatchString(mod) {
		return ""
	}
	dirs, _ := filepath.Glob(filepath.Join(dest, "lib", "python3*", "site-packages"))
	for _, d := range dirs {
		if fi, err := os.Stat(filepath.Join(d, mod)); err == nil && fi.IsDir() {
			return mod
		}
		if _, err := os.Stat(filepath.Join(d, mod+".py")); err == nil {
			return mod
		}
	}
	return ""
}

// checkBundle starts the bundle's own Python and imports the package's module (or just starts Python, when the module is not
// known): a bundle that cannot start is never offered as installed. A bundle with no Python has nothing to start. It runs as the
// server, not in a terminal's sandbox, in an empty folder, with a time limit.
func checkBundle(ctx context.Context, dest, name, kind string) error {
	if kind == KindNode {
		return checkNode(ctx, dest, name)
	}
	py := filepath.Join(dest, "bin", "python")
	if _, err := os.Stat(py); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	code := "import sys"
	if mod := moduleOf(dest, name); mod != "" {
		code = "import " + mod
	}
	c := exec.CommandContext(ctx, py, "-I", "-c", code)
	c.Dir = os.TempDir()
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s did not start: %s", name, tail(strings.TrimSpace(string(out)), 400))
	}
	return nil
}

// checkNode requires the module with the node program of the server (when it has one): a Node library that cannot be loaded is
// never offered as installed.
func checkNode(ctx context.Context, dest, name string) error {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, node, "-e", "require('"+name+"')")
	c.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(dest, "node_modules"))
	c.Dir = os.TempDir()
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%s could not be loaded: %s", name, tail(strings.TrimSpace(string(out)), 400))
	}
	return nil
}
