package appstore

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real install of a few programs from the Ubuntu archive (needs apt and the internet): OPENREPL_APT_REAL=1, in the image.
// Each program must run from its bundle, after the trim, with the environment the terminal gets.
// the variables an entry sets (what the catalogue has for these entries)
var aptEnv = map[string]map[string]string{
	"vim": {"VIMRUNTIME": "{bundle}/usr/share/vim/vim82"},
	"git": {"GIT_EXEC_PATH": "{bundle}/usr/lib/git-core", "GIT_TEMPLATE_DIR": "{bundle}/usr/share/git-core/templates"},
}

func TestAptBundleReal(t *testing.T) {
	if os.Getenv("OPENREPL_APT_REAL") == "" {
		t.Skip("set OPENREPL_APT_REAL=1 to install from the archive")
	}
	store := t.TempDir()
	entries := map[string]Entry{}
	look := func(kind, app string) (Entry, bool) { e, ok := entries[app]; return e, ok }
	for _, n := range strings.Fields(os.Getenv("OPENREPL_APT_APPS")) {
		entries[n] = Entry{Name: n, Kind: KindTool, Package: n, Options: []Option{{Manager: MgrApt, Package: n, Env: aptEnv[n]}}}
	}
	a := AptBundle{Dir: store, Lookup: look}
	cmds := map[string][]string{"vim": {"vim", "-es", "-u", "NONE", "-c", "\"if empty(globpath(&rtp,'syntax/c.vim'))|cq|endif|q\""}, "tmux": {"tmux", "-V"}, "git": {"git", "--version"}, "ripgrep": {"rg", "--version"}, "nano": {"nano", "--version"}, "htop": {"htop", "--version"}, "jq": {"jq", "--version"}}
	for n := range entries {
		ctx := WithManager(context.Background(), MgrApt)
		res, err := a.Resolve(ctx, n, "", KindTool)
		if err != nil && strings.Contains(err.Error(), "already") {
			t.Logf("%s: %v", n, err)
			continue
		}
		if err != nil {
			t.Fatalf("%s resolve: %v", n, err)
		}
		dest := filepath.Join(store, "apps", BundleID(n, res.Version))
		var log bytes.Buffer
		if err := a.Install(ctx, n, res.Version, KindTool, dest, &log); err != nil {
			t.Fatalf("%s install: %v\n%s", n, err, log.String())
		}
		before := dirMB(dest)
		rep := TrimBundle(dest, TrimOptions{})
		c := cmds[n]
		env := VarsFor(store, []string{BundleID(n, res.Version)}).Apply(os.Environ())
		cmd := exec.Command("sh", "-c", "command -v "+c[0]+" >&2; "+strings.Join(c, " "))
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		t.Logf("%s %s: %d packages, %.1f MB -> %.1f MB after trim (%d files), run: %v %q", n, res.Version, res.Packages, before, rep.AfterMB, rep.Files, err, firstLine(string(out)))
		if err != nil {
			t.Errorf("%s does not run: %v\n%s", n, err, out)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
