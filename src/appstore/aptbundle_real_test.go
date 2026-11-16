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

// Every tool of the catalogue that apt makes is installed, trimmed and run by the name a user types (OPENREPL_APT_ALL=1, in the image, with the
// internet): the programs Ubuntu renames (batcat, fdfind) have to be linked by their entries.
func TestAptToolsRunByTheirNames(t *testing.T) {
	if os.Getenv("OPENREPL_APT_ALL") == "" {
		t.Skip("set OPENREPL_APT_ALL=1 to install every apt tool of the catalogue")
	}
	entries, _ := DefaultCatalogue()
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.PackageName()] = e
	}
	store := t.TempDir()
	a := AptBundle{Dir: store, Lookup: func(kind, app string) (Entry, bool) { e, ok := byName[app]; return e, ok }}
	var bad []string
	only := os.Getenv("OPENREPL_APT_ONLY")
	for _, e := range entries {
		if e.Manager != MgrApt || e.Kind != CKTool || (only != "" && !strings.Contains(","+only+",", ","+e.ID+",")) {
			continue
		}
		ctx := WithManager(context.Background(), MgrApt)
		res, err := a.Resolve(ctx, e.PackageName(), "", KindTool)
		if err != nil {
			if strings.Contains(err.Error(), "already") {
				t.Logf("%s: %v", e.ID, err)
				continue
			}
			bad = append(bad, e.ID+": resolve: "+err.Error())
			continue
		}
		dest := filepath.Join(store, "apps", BundleID(e.PackageName(), res.Version))
		var log workLog
		if err := a.Install(ctx, e.PackageName(), res.Version, KindTool, dest, &log); err != nil {
			bad = append(bad, e.ID+": install: "+err.Error())
			continue
		}
		o, _ := e.OptionOf(MgrApt)
		TrimBundle(dest, TrimOptions{Keep: o.Keep})
		if err := verifyToolAs(ctx, dest, e.CheckArg, e.PackageName(), res.Version); err != nil {
			bad = append(bad, e.ID+": "+err.Error())
			continue
		}
		t.Logf("%s: %s runs", e.ID, e.CheckArg)
		os.RemoveAll(dest)
	}
	if len(bad) > 0 {
		t.Errorf("tools that do not run by the name a user types:\n  %s", strings.Join(bad, "\n  "))
	}
}
