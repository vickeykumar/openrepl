package appstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Verification (LLD 18). Every app has a step that proves it works, and it is run at two points:
//
//   - when a node has installed a bundle (checkBundle: the bundle's own Python imports the module, node requires the library, a tool or a
//     language prints its version), before the node says it has it;
//   - when a user switches it on (RunVerify): the same step, in the sandbox of that user, with that user's environment, on the node
//     where the user's terminals run. Only when it passes is it on; when it does not, it is off again and the user can report it.

// ErrNotHere says that the bundle is not on this node (yet): the user waits a moment, nobody is to blame.
var ErrNotHere = errors.New("this server does not have it yet")

// versionFlags are what a tool or a language is asked to print to show that it runs, the first that works.
var versionFlags = [][]string{{"--version"}, {"-version"}, {"-v"}, {"-V"}, {"version"}}

// exeOf is the program of a tool or language whose package name is not the name of its program.
var exeOf = map[string]string{"r-base": "R", "nodejs": "node", "go": "go", "openjdk": "java", "ruby": "ruby", "julia": "julia", "lua": "lua"}

// verifyExe finds the program to start for a tool or a language in a bundle: "" when it has none (a package of data or headers).
func verifyExe(dest, name string) string {
	cands := []string{name}
	if e, ok := exeOf[name]; ok {
		cands = []string{e, name}
	}
	for _, c := range cands {
		if fi, err := os.Stat(filepath.Join(dest, "bin", c)); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// VerifyArgs is the command that proves an app works, run with the environment of a user who has it on (PATH, PYTHONPATH, NODE_PATH),
// and ok=false when there is nothing to run (an editor plugin is a file; a package of data). Each form is a list of commands, the first
// that ends well passes.
func VerifyArgs(dest, kind, name string) (cmds [][]string, ok bool) {
	switch cleanKind(kind) {
	case KindNode:
		if !ValidName(name) {
			return nil, false
		}
		return [][]string{{"node", "-e", "require('" + name + "')"}}, true
	case KindTool, KindLang:
		exe := verifyExe(dest, name)
		if exe == "" {
			return nil, false
		}
		for _, f := range versionFlags {
			cmds = append(cmds, append([]string{exe}, f...))
		}
		return cmds, true
	case KindPlugin:
		return nil, false
	default: // a Python library
		if mod := moduleOf(dest, name); mod != "" {
			return [][]string{{"python3", "-c", "import " + mod}}, true
		}
		return nil, false
	}
}

// RunVerify runs the verification of one app for one user. wrap makes the command run in the user's sandbox (nil: as the server).
// dir is where it starts (the user's workspace; empty: a temporary folder). It returns what the program said when it failed.
func RunVerify(ctx context.Context, st Store, home, id, name, kind, dir string, wrap func([]string) []string) error {
	if !ValidBundle(id) || !ValidName(name) || !Kept(home) {
		return errors.New("that is not an app of a signed-in user")
	}
	if !st.HasBundle(id) {
		return ErrNotHere
	}
	cmds, ok := VerifyArgs(st.BundlePath(id), kind, name)
	if !ok {
		return nil
	}
	eff, err := st.Effective(home)
	if err != nil {
		return err
	}
	if !has(eff, id) {
		eff = append(eff, id)
	}
	env := VarsFor(st.Dir, eff).Apply(os.Environ())
	if dir == "" {
		dir = os.TempDir()
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = os.TempDir()
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var last string
	for _, argv := range cmds {
		if wrap != nil {
			argv = wrap(argv)
		}
		c := exec.CommandContext(ctx, lookIn(env, argv[0]), argv[1:]...)
		c.Env, c.Dir = env, dir
		out, err := c.CombinedOutput()
		if err == nil {
			return nil
		}
		last = fmt.Sprintf("%s: %v", strings.Join(argv[len(argv)-lastWords(argv):], " "), err)
		if t := strings.TrimSpace(string(out)); t != "" {
			last += "\n" + tail(t, 400)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.New(last)
}

// lastWords is how many trailing words of a (maybe wrapped) command to show: the whole command, not the sandbox in front of it.
func lastWords(argv []string) int {
	for i, a := range argv {
		if a == "--" {
			return len(argv) - i - 1
		}
	}
	return len(argv)
}

// verifyTool starts a tool or a language of a bundle with the first version flag that works: a bundle whose program does not run is never
// offered as installed. It runs as the server, in an empty folder, with the bundle's own bin first on the PATH.
func verifyTool(ctx context.Context, dest, name string) error {
	exe := verifyExe(dest, name)
	if exe == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var last string
	for _, f := range versionFlags {
		c := exec.CommandContext(ctx, filepath.Join(dest, "bin", exe), f...)
		c.Env = append(os.Environ(), "PATH="+filepath.Join(dest, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		c.Dir = os.TempDir()
		out, err := c.CombinedOutput()
		if err == nil {
			return nil
		}
		last = fmt.Sprintf("%s %s: %v %s", exe, strings.Join(f, " "), err, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("%s did not start: %s", name, tail(last, 400))
}

// lookIn finds a program the way the user's shell would: on the PATH of the environment it is given, not of this server.
func lookIn(env []string, name string) string {
	if strings.ContainsRune(name, filepath.Separator) {
		return name
	}
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			for _, d := range filepath.SplitList(strings.TrimPrefix(e, "PATH=")) {
				p := filepath.Join(d, name)
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
					return p
				}
			}
		}
	}
	return name
}
