package appstore

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultCatalogueIsSound(t *testing.T) {
	entries, packs := DefaultCatalogue()
	if len(entries) < 100 {
		t.Fatalf("only %d entries", len(entries))
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if ids[e.ID] {
			t.Errorf("%s twice", e.ID)
		}
		ids[e.ID] = true
		if err := ValidateEntry(e); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if e.Docs == "" {
			t.Errorf("%s has no documentation link", e.ID) // the owner: a link on every app and plugin page
		}
		if e.Licence == "" || e.Summary == "" {
			t.Errorf("%s needs a licence and a summary", e.ID)
		}
		if e.Kind != CKPlugin && e.Kind != CKWeb && e.Check == "" {
			t.Errorf("%s has no check", e.ID)
		}
	}
	for _, p := range packs {
		for _, id := range p.Entries {
			if !ids[id] {
				t.Errorf("pack %s names %s, which is not there", p.ID, id)
			}
		}
	}
	// the catalogue is only for the REPLs of the owner's list
	seen := map[string]bool{}
	for _, e := range entries {
		for _, r := range e.Repls {
			seen[r] = true
		}
	}
	for _, r := range []string{"python", "ipython3", "python2.7", "c", "cpp", "bash", "go", "node", "html"} {
		if !seen[r] {
			t.Errorf("no entry for the REPL %s", r)
		}
	}
}

func TestValidateEntryRefusesWhatIsNotAllowed(t *testing.T) {
	good := Entry{ID: "x", Name: "x", Summary: "s", Kind: CKLib, Repls: []string{"python"}, Level: "core", Manager: MgrPixi, Install: "anyone", Check: "python-import", CheckArg: "x"}
	if err := ValidateEntry(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Entry){
		"a shell command as the check": func(e *Entry) { e.Check = "sh -c 'rm -rf /'" },
		"a command in the argument":    func(e *Entry) { e.CheckArg = "x; rm -rf /" },
		"a path in the id":             func(e *Entry) { e.ID = "../x" },
		"an unknown REPL":              func(e *Entry) { e.Repls = []string{"cobol"} },
		"an http docs link":            func(e *Entry) { e.Docs = "http://x.example/doc" },
		"a javascript docs link":       func(e *Entry) { e.Docs = "javascript:alert(1)" },
		"an unknown manager":           func(e *Entry) { e.Manager = "curl | sh" },
		"no REPL":                      func(e *Entry) { e.Repls = nil },
		"a plugin without a place":     func(e *Entry) { e.Kind = CKPlugin; e.Repls = nil },
		"a file that climbs out":       func(e *Entry) { e.Files = []string{"../../etc/passwd"} },
	}
	for what, mut := range bad {
		e := good
		mut(&e)
		if ValidateEntry(e) == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	e := good
	e.Docs = "/docs/plugins/outline"
	if err := ValidateEntry(e); err != nil {
		t.Errorf("an internal docs page: %v", err)
	}
}

func TestCatalogueAddRemoveExportImportAndFilter(t *testing.T) {
	svc, _ := newService(t, Fake{})
	n := len(svc.Catalogue())
	e := Entry{ID: "zzlib", Name: "zzlib", Summary: "x", Kind: CKLib, Repls: []string{"python"}, Level: "optional", Manager: MgrPixi, Install: "approval", Docs: "https://example.org/d", Check: "python-import", CheckArg: "zzlib"}
	if err := svc.AddEntry("root", e); err != nil {
		t.Fatal(err)
	}
	if len(svc.Catalogue()) != n+1 {
		t.Fatal("not added")
	}
	if err := svc.Install("ann", "zzlib", KindLib, false); err != ErrNeedsApproval {
		t.Fatalf("an entry that needs approval: %v", err)
	}
	e.Install = "anyone"
	svc.AddEntry("root", e)
	if err := svc.Install("ann", "zzlib", KindLib, false); err != nil {
		t.Fatalf("an entry anyone may install: %v", err)
	}
	svc.Wait()
	raw, err := svc.Export()
	if err != nil || !strings.Contains(string(raw), "zzlib") {
		t.Fatal(err)
	}
	svc2, _ := newService(t, Fake{})
	rep, err := svc2.Import("root", raw, true) // a dry run saves nothing
	if err != nil || rep.Added < 100 || len(rep.Rejected) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, ok := svc2.Entry("zzlib"); ok {
		t.Fatal("a dry run added an entry")
	}
	if _, err := svc2.Import("root", raw, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc2.Entry("zzlib"); !ok {
		t.Fatal("import did not add")
	}
	if _, err := svc2.Import("root", []byte(`{"version":1,"entries":[{"id":"a b","name":"a"}]}`), false); err != nil {
		t.Fatal(err)
	}
	rep, _ = svc2.Import("root", []byte(`{"version":1,"entries":[{"id":"evil","name":"evil","summary":"s","kind":"lib","repls":["python"],"level":"core","manager":"pixi","install":"anyone","check":"sh -c id"}]}`), false)
	if rep.Added != 0 || len(rep.Rejected) != 1 {
		t.Fatalf("an import with a shell check: %+v", rep)
	}
	if err := svc.RemoveEntry("root", "zzlib"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Entry("zzlib"); ok {
		t.Fatal("not removed")
	}
	// filters
	all := svc.Catalogue()
	py := Filter{Repl: "python"}.Apply(all)
	if len(py) == 0 || len(py) >= len(all) {
		t.Fatalf("python filter: %d of %d", len(py), len(all))
	}
	for _, x := range py {
		if !inList(x.Repls, "python") {
			t.Errorf("%s is not for python", x.ID)
		}
	}
	br := Filter{Browser: true}.Apply(all)
	for _, x := range br {
		if x.Kind != CKWeb && x.Kind != CKPlugin {
			t.Errorf("%s is not for the browser", x.ID)
		}
	}
	if len(br) < 10 || len(Filter{Q: "eruda"}.Apply(all)) != 1 || len(Filter{Level: "core", Kind: CKLib}.Apply(all)) == 0 {
		t.Error("filters")
	}
}

func TestPackAndRequestsPerDay(t *testing.T) {
	svc, _ := newService(t, Fake{})
	started, skipped, err := svc.PackInstall("root", "cpp-developer")
	if err != nil || len(started)+len(skipped) == 0 {
		t.Fatalf("%v %v %v", started, skipped, err)
	}
	svc.Wait()
	if err := svc.SetPack("root", Pack{ID: "mine", Name: "Mine", Entries: []string{"nope"}}); err == nil {
		t.Error("a pack with an app that is not in the catalogue")
	}
	// a user may ask a few times a day
	st := Store{Dir: t.TempDir()}
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	s2 := New(Config{Store: st, KV: newKV(), Installer: Fake{}, PerDay: func() int { return 2 }, Now: func() time.Time { return now }})
	if _, err := s2.RequestFor("ann", "aaa", "lib", "python", ""); err != nil {
		t.Fatal(err)
	}
	s2.RequestFor("ann", "bbb", "lib", "python", "")
	if _, err := s2.RequestFor("ann", "ccc", "lib", "python", ""); err != ErrDaily {
		t.Fatalf("third ask: %v", err)
	}
	if _, err := s2.RequestFor("ann", "aaa", "lib", "python", ""); err != nil {
		t.Errorf("an ask the user made already does not count again: %v", err)
	}
	if _, err := s2.RequestFor("bob", "ccc", "lib", "cobol", ""); err == nil {
		t.Error("a REPL that is not of this site")
	}
	now = now.Add(24 * time.Hour)
	if _, err := s2.RequestFor("ann", "ccc", "lib", "python", ""); err != nil {
		t.Errorf("next day: %v", err)
	}
}

func TestDryRunAndBulkAdd(t *testing.T) {
	svc, _ := newService(t, Fake{Fail: map[string]bool{"nothere": true}})
	rows := svc.DryRun(context.Background(), KindLib, []string{"zzone", "nothere", "Bad Name", "zzone"})
	if len(rows) != 3 || !rows[0].Solves || rows[1].Solves || rows[2].Result != "not a package name" {
		t.Fatalf("%+v", rows)
	}
	n, err := svc.BulkAdd("root", []string{"python"}, KindLib, "optional", MgrPixi, rows)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	e, ok := svc.Entry("zzone")
	if !ok || e.Install != "approval" || e.Check != "python-import" {
		t.Fatalf("%+v", e)
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), ";") {
		t.Error("check")
	}
}

func TestWiringOfLibrariesIntoTheEnvironment(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	root := filepath.Join(st.AppsDir(), "eigen-3.4", ".pixi", "envs", "default")
	for _, d := range []string{"include/eigen3", "lib/pkgconfig", "bin"} {
		os.MkdirAll(filepath.Join(root, d), 0755)
	}
	pl := filepath.Join(st.AppsDir(), "perl-try-tiny-0.31", ".pixi", "envs", "default", "lib", "perl5", "site_perl", "5.32.1")
	os.MkdirAll(pl, 0755)
	tcl := filepath.Join(st.AppsDir(), "tcllib-1.21", ".pixi", "envs", "default", "lib", "tcllib1.21")
	os.MkdirAll(tcl, 0755)
	py := filepath.Join(st.AppsDir(), "numpy-2.0", ".pixi", "envs", "default", "lib", "python3.12", "site-packages")
	os.MkdirAll(py, 0755)
	os.MkdirAll(filepath.Join(st.AppsDir(), "numpy-2.0", ".pixi", "envs", "default", "include", "python3.12"), 0755)
	v := VarsFor(st.Dir, []string{"eigen-3.4", "perl-try-tiny-0.31", "tcllib-1.21", "numpy-2.0"})
	env := v.Apply([]string{"PATH=/usr/bin"})
	got := map[string]string{}
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i > 0 {
			got[e[:i]] = e[i+1:]
		}
	}
	if !strings.Contains(got["CPLUS_INCLUDE_PATH"], "eigen-3.4") || !strings.Contains(got["LIBRARY_PATH"], "eigen-3.4") || !strings.Contains(got["LD_RUN_PATH"], "eigen-3.4") {
		t.Errorf("include and library paths: %v", got)
	}
	if strings.Contains(got["CPLUS_INCLUDE_PATH"], "numpy") {
		t.Error("a bundle that brings Python must not put its headers before the system's")
	}
	if !strings.Contains(got["PERL5LIB"], "5.32.1") || !strings.Contains(got["TCLLIBPATH"], "tcllib-1.21") || !strings.Contains(got["PKG_CONFIG_PATH"], "eigen-3.4") {
		t.Errorf("perl, tcl, pkg-config: %v", got)
	}
	if !strings.Contains(EnvScript(st.Dir, []string{"eigen-3.4"}), "CPLUS_INCLUDE_PATH") {
		t.Error("the env script lacks the include path")
	}
}

func fakeNpmBundle(t *testing.T, st Store, id, pkg string, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(st.BundlePath(id), "node_modules", filepath.FromSlash(pkg), filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(body), 0644)
	}
	if _, err := st.Finish(id, strings.TrimSuffix(id[:strings.LastIndex(id, "-")], ""), id[strings.LastIndex(id, "-")+1:]); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewPluginsSwitchHeadAndLibFiles(t *testing.T) {
	svc, st := newService(t, Fake{})
	list := svc.PreviewPlugins("ann")
	on := map[string]bool{}
	for _, p := range list {
		on[p.ID] = p.On
		if p.Ours && !p.Ready {
			t.Errorf("%s is ours: always there", p.ID)
		}
	}
	if !on["plugin:viewport"] || !on["plugin:outline"] || !on["plugin:console"] || on["plugin:eruda"] || on["plugin:vconsole"] {
		t.Fatalf("defaults: %v", on)
	}
	if err := svc.SwitchPlugin("ann", "plugin:eruda", true); err != ErrUnknown {
		t.Fatalf("a third party plugin that is not installed cannot be switched on: %v", err)
	}
	if err := svc.SwitchPlugin("ann", "plugin:outline", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range svc.PreviewPlugins("ann") {
		if p.ID == "plugin:outline" && p.On {
			t.Error("outline should be off for ann")
		}
		if p.ID == "plugin:outline" == false && p.ID == "plugin:viewport" && !p.On {
			t.Error("viewport stays on")
		}
	}
	for _, p := range svc.PreviewPlugins("bob") {
		if p.ID == "plugin:outline" && !p.On {
			t.Error("another user's switch changed bob's")
		}
	}
	fakeNpmBundle(t, st, "eruda-3.4.3", "eruda", map[string]string{"eruda.js": "/* eruda */", "package.json": "{}"})
	if err := svc.SwitchPlugin("ann", "plugin:eruda", true); err != nil {
		t.Fatal(err)
	}
	head := svc.PreviewHead("ann", "")
	if !strings.Contains(head, `<script src="/preview/lib/eruda/eruda.js"></script>`) || !strings.Contains(head, "eruda.init()") || !strings.Contains(head, "DOMContentLoaded") {
		t.Errorf("head: %s", head)
	}
	if h := svc.PreviewHead("ann", "/site"); !strings.Contains(h, `src="/site/preview/lib/eruda/eruda.js"`) {
		t.Errorf("a site under a prefix: %s", h)
	}
	if svc.PreviewHead("bob", "") != "" {
		t.Error("bob did not turn it on")
	}
	// on for everyone, and each user may still turn it off
	if err := svc.SetPluginDefault("root", "plugin:eruda", true); err != nil {
		t.Fatal(err)
	}
	if svc.PreviewHead("bob", "") == "" {
		t.Error("on for everyone")
	}
	svc.SwitchPlugin("bob", "plugin:eruda", false)
	if svc.PreviewHead("bob", "") != "" {
		t.Error("bob turned it off for himself")
	}
	// the files a preview may be given
	if p, ok := svc.LibFile("eruda", "eruda.js"); !ok || !strings.HasSuffix(p, "eruda.js") {
		t.Errorf("the entry file: %v %v", p, ok)
	}
	for _, bad := range []string{"package.json", "../eruda-3.4.3/node_modules/eruda/eruda.js", "/etc/passwd", "a/../../x", ""} {
		if _, ok := svc.LibFile("eruda", bad); ok {
			t.Errorf("served %q", bad)
		}
	}
	if _, ok := svc.LibFile("not-installed", "x.js"); ok {
		t.Error("an app that is not installed")
	}
	// a browser library serves any file inside its package, but a link that leaves the package is refused
	fakeNpmBundle(t, st, "bootstrap-5.3.0", "bootstrap", map[string]string{"dist/css/bootstrap.min.css": "a{}", "dist/fonts/x.woff2": "f"})
	if _, ok := svc.LibFile("bootstrap", "dist/fonts/x.woff2"); !ok {
		t.Error("a font of a browser library")
	}
	os.Symlink("/etc/passwd", filepath.Join(st.BundlePath("bootstrap-5.3.0"), "node_modules", "bootstrap", "leak"))
	if _, ok := svc.LibFile("bootstrap", "leak"); ok {
		t.Error("a symbolic link out of the package was served")
	}
	// a browser library is not a terminal app: no user sees it as a Node library to switch on
	for _, v := range svc.Views("ann", "ann") {
		if v.Name == "bootstrap" && v.State == StateInstalled {
			t.Error("a browser library appears among the apps a terminal can switch on")
		}
	}
}

func TestAnAppIsSolvedAgainstTheRuntimesOfTheReplsItWorksWith(t *testing.T) {
	svc, _ := newService(t, Fake{})
	p := Pixi{Dir: t.TempDir(), Python: "3.10", Constraints: func(n string) [][2]string {
		if e, ok := svc.EntryFor(n, KindTool); ok {
			return ConstraintsFor(e)
		}
		return nil
	}}
	for name, want := range map[string]string{"benchmark": `"libstdcxx-ng" = "<13"`, "perl-json-maybexs": `"perl" = "5.34.*"`} {
		m, err := p.Manifest(name, "", KindTool)
		if err != nil || !strings.Contains(m, want) {
			t.Errorf("%s: %v\n%s", name, err, m)
		}
	}
	if m, _ := p.Manifest("cmake", "", KindTool); strings.Contains(m, "libstdcxx") {
		t.Errorf("a program is not constrained:\n%s", m)
	}
}

func TestAWorkerGetsTheAdminsEntriesWithTheSnapshot(t *testing.T) {
	gw, _ := newService(t, Fake{})
	gw.AddEntry("root", Entry{ID: "web:zzlib", Name: "zzlib", Summary: "s", Kind: CKWeb, Repls: []string{"html"}, Level: "optional", Manager: MgrNPM, Install: "approval", Files: []string{"dist/z.js"}})
	snap := gw.Snapshot()
	if len(snap.Catalogue) != 1 || snap.Catalogue[0].ID != "web:zzlib" {
		t.Fatalf("%+v", snap.Catalogue)
	}
	w, _ := newService(t, Fake{})
	if _, ok := w.EntryFor("zzlib", KindNode); ok {
		t.Fatal("the worker cannot know it yet")
	}
	w.Apply(context.Background(), snap)
	if e, ok := w.EntryFor("zzlib", KindNode); !ok || !e.asset() {
		t.Errorf("after the snapshot the worker picks the file check of a browser library: %+v %v", e, ok)
	}
}

func TestMirrorsNamesVersionsAndHelpers(t *testing.T) {
	e := Entry{ID: "go:cobra", Name: "cobra", Kind: CKGoMod, Package: "github.com/spf13/cobra"}
	if e.PackageName() != "gomod-cobra" || e.Source() != "github.com/spf13/cobra" || e.InstallKind() != KindGoMod || !ValidName(e.PackageName()) {
		t.Errorf("go module: %s %s %s", e.PackageName(), e.Source(), e.InstallKind())
	}
	if p := (Entry{Name: "gson", Kind: CKJar, Package: "com.google.code.gson:gson"}).PackageName(); p != "jar-gson" {
		t.Error(p)
	}
	if p := (Entry{Name: "serde_json", Kind: CKCrate}).PackageName(); p != "crate-serde_json" || !ValidName(p) {
		t.Error(p)
	}
	if p := (Entry{Name: "six (Python 2.7)", Kind: CKLib27, Package: "six"}).PackageName(); p != "py27-six" {
		t.Error(p)
	}
	// the same name in two ecosystems is two apps
	if (Entry{Name: "regex", Kind: CKCrate}).PackageName() == (Entry{Name: "regex", Kind: CKLib}).PackageName() {
		t.Error("a crate and a Python library of one name must not collide")
	}
	if got := stable([]string{"v1.2.0", "v1.3.0-rc.1", "v2.0.0+incompatible", "1.4", "2.0.0-SNAPSHOT", "v1.10.2"}); strings.Join(got, ",") != "v1.2.0,1.4,v1.10.2" {
		t.Errorf("stable: %v", got)
	}
	if goVer("1.2.3") != "v1.2.3" || goVer("v1.2.3") != "v1.2.3" {
		t.Error("goVer")
	}
	if lockedVersion("[[package]]\nname = \"itoa\"\nversion = \"1.0.1\"\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.200\"\n", "serde") != "1.0.200" {
		t.Error("lockedVersion")
	}
	cfg := CargoConfig("/s/v")
	if !strings.Contains(cfg, `replace-with = "vendored-sources"`) || !strings.Contains(cfg, `directory = "/s/v"`) || !strings.Contains(cfg, "offline = true") {
		t.Errorf("cargo config:\n%s", cfg)
	}
	if _, _, err := splitGA("nogroup"); err == nil {
		t.Error("group:artifact")
	}
	// a jar's class file version: 61 is Java 17
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("a/B.class")
	w.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 61})
	zw.Close()
	if m, err := classMajor(buf.Bytes()); err != nil || m != 61 {
		t.Errorf("class major %d %v", m, err)
	}
}

func TestMirrorBundlesReachTheTerminalsEnvironment(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	mk := func(id string, dirs ...string) {
		for _, d := range dirs {
			os.MkdirAll(filepath.Join(st.AppsDir(), id, filepath.FromSlash(d)), 0755)
		}
	}
	mk("gomod-cobra-1.10.2", "gomod/cache/download")
	mk("jar-gson-2.11.0", "jars")
	mk("crate-serde-1.0.200", "vendor")
	mk("py27-six-1.16.0", ".pixi/envs/default/lib/python2.7/site-packages")
	mk("numpy-2.0", ".pixi/envs/default/lib/python3.10/site-packages")
	ids := []string{"gomod-cobra-1.10.2", "jar-gson-2.11.0", "crate-serde-1.0.200", "py27-six-1.16.0", "numpy-2.0"}
	v := VarsFor(st.Dir, ids)
	env := map[string]string{}
	for _, e := range v.Apply([]string{"PATH=/usr/bin"}) {
		if i := strings.IndexByte(e, '='); i > 0 {
			env[e[:i]] = e[i+1:]
		}
	}
	if !strings.HasPrefix(env["GOPROXY"], "file://") || !strings.HasSuffix(env["GOPROXY"], ",off") || env["GOFLAGS"] != "-mod=mod" || env["GOSUMDB"] != "off" {
		t.Errorf("go: %v", env)
	}
	if !strings.HasSuffix(env["CLASSPATH"], "/jars/*:.") {
		t.Errorf("the class path keeps the current folder: %q", env["CLASSPATH"])
	}
	if !strings.Contains(env["OPENREPL_CRATE_VENDORS"], "crate-serde-1.0.200/vendor") || !strings.Contains(env["OPENREPL_PY27PATH"], "python2.7/site-packages") {
		t.Errorf("crates and python 2.7: %v", env)
	}
	if strings.Contains(env["PYTHONPATH"], "python2.7") || !strings.Contains(env["PYTHONPATH"], "python3.10") {
		t.Errorf("Python 2.7 libraries must not be on the path of Python 3: %q", env["PYTHONPATH"])
	}
	if sc := EnvScript(st.Dir, ids); !strings.Contains(sc, "GOPROXY") || !strings.Contains(sc, "CLASSPATH") || !strings.Contains(sc, "OPENREPL_PY27PATH") {
		t.Errorf("env.sh:\n%s", sc)
	}
}

func TestUseLinesAndModulesOfTheCatalogue(t *testing.T) {
	entries, _ := DefaultCatalogue()
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	for id, want := range map[string][2]string{
		"scikit-learn":     {"import sklearn", "sklearn"},
		"pandas":           {"import pandas", "pandas"},
		"ripgrep":          {"rg --version", ""},
		"eigen":            {"#include <eigen3/...>", ""},
		"npm:lodash":       {"const x = require('lodash')", "lodash"},
		"go:cobra":         {"go get github.com/spf13/cobra", "github.com/spf13/cobra"},
		"jar:gson":         {"import com.google.gson.Gson;", "com.google.gson"},
		"crate:serde_json": {":dep serde_json = \"*\"", "serde_json"},
		"py27-six":         {"python2.7 -c \"import six\"", "six"},
	} {
		e, ok := byID[id]
		if !ok {
			t.Errorf("%s is not in the catalogue", id)
			continue
		}
		if e.UseLine() != want[0] || e.Module() != want[1] {
			t.Errorf("%s: use %q module %q, want %q %q", id, e.UseLine(), e.Module(), want[0], want[1])
		}
	}
	svc, _ := newService(t, Fake{})
	found := false
	for _, v := range svc.Views("ann", "ann") {
		if v.Name == "pandas" && v.Use == "import pandas" && v.Mod == "pandas" {
			found = true
		}
	}
	if !found {
		t.Error("the list carries the use line and the module of an app")
	}
}

func TestTheManagerIsTheSmallestUsableOptionAndAptGetsAGrace(t *testing.T) {
	sz := func(a, b float64) map[string]float64 { return map[string]float64{ArchAMD64: a, ArchARM64: b} }
	vim := Entry{Options: []Option{{Manager: MgrPixi, Size: sz(330, 356), GUI: "tk"}, {Manager: MgrApt, Size: sz(38, 40)}}}
	if m, ok := Choose(vim, ArchARM64); !ok || m != MgrApt {
		t.Errorf("the only usable option is apt: %s %v", m, ok)
	}
	// an option with a GUI library, an error, or no size for the CPU type is never chosen
	if m, _ := Choose(Entry{Options: []Option{{Manager: MgrPixi, Size: sz(1, 1), GUI: "xorg-libx11"}, {Manager: MgrApt, Size: sz(500, 500)}}}, ArchAMD64); m != MgrApt {
		t.Errorf("a smaller option with a GUI library is refused: %s", m)
	}
	if _, ok := Choose(Entry{Options: []Option{{Manager: MgrPixi, Err: "does not solve", Size: sz(1, 1)}, {Manager: MgrApt, Size: map[string]float64{ArchAMD64: 5}}}}, ArchARM64); ok {
		t.Error("nothing is usable on arm64")
	}
	// apt wins inside its grace (20 percent and 5 MB), pixi outside it
	near := Entry{Options: []Option{{Manager: MgrPixi, Size: sz(10, 10)}, {Manager: MgrApt, Size: sz(16, 16)}}} // 10*1.2+5 = 17
	if m, _ := Choose(near, ArchAMD64); m != MgrApt {
		t.Errorf("apt within the grace: %s", m)
	}
	far := Entry{Options: []Option{{Manager: MgrPixi, Size: sz(10, 10)}, {Manager: MgrApt, Size: sz(18, 18)}}}
	if m, _ := Choose(far, ArchAMD64); m != MgrPixi {
		t.Errorf("apt outside the grace: %s", m)
	}
	// npm and pixi: the smaller; a tie goes to the name that sorts first, so the choice is the same on every run
	if m, _ := Choose(Entry{Options: []Option{{Manager: MgrNPM, Size: sz(4, 4)}, {Manager: MgrPixi, Size: sz(9, 9)}}}, ArchAMD64); m != MgrNPM {
		t.Errorf("the smaller of npm and pixi: %s", m)
	}
	// one manager for both CPU types: an option usable on one only is left out
	one := Entry{Options: []Option{{Manager: MgrApt, Size: map[string]float64{ArchARM64: 3}}, {Manager: MgrPixi, Size: sz(30, 31)}}}
	if m, ok := ChooseAll(one); !ok || m != MgrPixi {
		t.Errorf("both CPU types: %s %v", m, ok)
	}
	// before the amd64 run has been made, what was measured decides
	if m, ok := ChooseAll(Entry{Options: []Option{{Manager: MgrApt, Size: map[string]float64{ArchARM64: 3}}}}); !ok || m != MgrApt {
		t.Errorf("one CPU type measured: %s %v", m, ok)
	}
}

func TestGUIPackagesAreRecognisedForAptAndPixi(t *testing.T) {
	vimPixi := []string{"vim", "libxcb", "tk", "xorg-libice", "python"}
	if g := GUIOfPixi(vimPixi); g != "libxcb" {
		t.Errorf("pixi vim: %q", g)
	}
	if g := GUIOfPixi([]string{"tmux", "libevent", "ncurses", "libgcc-ng", "qt-main"}); g != "qt-main" {
		t.Errorf("a Qt package: %q", g)
	}
	for _, ok := range []string{"ripgrep", "libgcc-ng", "libexpat", "libxml2", "libxslt", "xz", "libsodium", "git", "openssl", "libgit2", "tk-demo-not"} {
		if g := GUIOfPixi([]string{ok}); g != "" && ok != "tk-demo-not" {
			t.Errorf("%s is not a GUI package: %q", ok, g)
		}
	}
	if g := GUIOfApt([]AptPkg{{Name: "vim"}, {Name: "libgpm2"}, {Name: "libsodium23"}, {Name: "vim-runtime", Section: "editors"}}); g != "" {
		t.Errorf("apt vim has no GUI package: %q", g)
	}
	for _, bad := range []AptPkg{{Name: "libx11-6"}, {Name: "libgtk-3-0"}, {Name: "libqt5core5a"}, {Name: "libgl1"}, {Name: "mesa-vulkan-drivers"}, {Name: "libwayland-client0"}, {Name: "x11-common"}, {Name: "gimp", Section: "graphics"}, {Name: "fonty", Section: "universe/x11"}, {Name: "gedit", Section: "gnome"}, {Name: "kate", Section: "kde"}} {
		if g := GUIOfApt([]AptPkg{{Name: "tool"}, bad}); g != bad.Name {
			t.Errorf("%+v is a GUI package, got %q", bad, g)
		}
	}
	lock := "packages:\n- conda: https://conda.anaconda.org/conda-forge/linux-aarch64/vim-9.1.0-py312_h1.conda\n- conda: https://conda.anaconda.org/conda-forge/linux-aarch64/xorg-libice-1.1.2-h80f16a2_0.conda\n- conda: https://conda.anaconda.org/conda-forge/noarch/python_abi-3.12-1_cp312.conda\n- conda: https://conda.anaconda.org/conda-forge/linux-aarch64/libgcc-ng-14.2.0-h1.tar.bz2\n"
	names := PixiLockNames(lock)
	if strings.Join(names, ",") != "libgcc-ng,python_abi,vim,xorg-libice" {
		t.Errorf("names of a lock: %v", names)
	}
	if GUIOfPixi(names) != "xorg-libice" {
		t.Error("the lock of vim has a GUI library")
	}
}

func TestTheTrimDeletesWhatAnAppDoesNotNeedAndNeverALicence(t *testing.T) {
	b := t.TempDir()
	put := func(rel string, size int) {
		p := filepath.Join(b, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, make([]byte, size), 0644)
	}
	for rel, size := range map[string]int{
		"usr/bin/vim.basic": 5000, "usr/share/vim/vim82/syntax/c.vim": 3000, // what the program needs
		"usr/share/doc/vim/copyright": 100, "usr/share/doc/vim/README.gz": 4000, "usr/share/man/man1/vim.1.gz": 4000, "usr/share/locale/de/LC_MESSAGES/vim.mo": 4000,
		"usr/lib/libfoo.a": 4000, ".pixi/envs/default/lib/python3.10/site-packages/pkg/__pycache__/x.pyc": 4000,
		".pixi/envs/default/lib/python3.10/site-packages/pkg/tests/test_x.py": 4000, ".pixi/envs/default/lib/python3.10/site-packages/pkg/core.py": 100,
		".pixi/envs/default/share/doc/pkg/LICENSE.txt": 50, ".pixi/envs/default/man/man1/x.1": 4000,
		"node_modules/lib/index.js": 100, "node_modules/lib/index.js.map": 4000, "node_modules/lib/README.md": 4000, "node_modules/lib/LICENSE.md": 50, "node_modules/lib/package.json": 50,
		"usr/include/eigen3/Eigen/Core": 100, "usr/lib/libfmt.a": 4000,
	} {
		put(rel, size)
	}
	rep := TrimBundle(b, TrimOptions{Keep: []string{"usr/lib/libfmt.a"}})
	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(b, filepath.FromSlash(rel))); return err == nil }
	for _, gone := range []string{"usr/share/doc/vim/README.gz", "usr/share/man", "usr/share/locale", "usr/lib/libfoo.a", ".pixi/envs/default/lib/python3.10/site-packages/pkg/__pycache__",
		".pixi/envs/default/lib/python3.10/site-packages/pkg/tests", ".pixi/envs/default/man", "node_modules/lib/index.js.map", "node_modules/lib/README.md"} {
		if exists(gone) {
			t.Errorf("%s should have been deleted", gone)
		}
	}
	for _, kept := range []string{"usr/bin/vim.basic", "usr/share/vim/vim82/syntax/c.vim", "usr/share/doc/vim/copyright", ".pixi/envs/default/share/doc/pkg/LICENSE.txt",
		".pixi/envs/default/lib/python3.10/site-packages/pkg/core.py", "node_modules/lib/index.js", "node_modules/lib/LICENSE.md", "node_modules/lib/package.json",
		"usr/include/eigen3/Eigen/Core", "usr/lib/libfmt.a"} {
		if !exists(kept) {
			t.Errorf("%s must stay (a licence, a file the program needs, or on the keep list)", kept)
		}
	}
	if rep.AfterMB > rep.BeforeMB || rep.Files < 8 {
		t.Errorf("report %+v", rep)
	}
	// a library of C or C++ keeps its static libraries
	b2 := t.TempDir()
	os.MkdirAll(filepath.Join(b2, "usr/lib"), 0755)
	os.WriteFile(filepath.Join(b2, "usr/lib/libx.a"), []byte("x"), 0644)
	TrimBundle(b2, TrimOptions{KeepStatic: true})
	if _, err := os.Stat(filepath.Join(b2, "usr/lib/libx.a")); err != nil {
		t.Error("a static library of a C library is kept")
	}
}

func TestAptAlternativesFromPostinst(t *testing.T) {
	post := "#!/bin/sh\npkg=vim\nvariant=basic\nadd() {\n  update-alternatives --install /usr/bin/vim vim /usr/bin/vim.$variant $1\n  for i in vi view ; do\n    update-alternatives --install /usr/bin/$i $i /usr/bin/vim.$variant $1\n  done\n  update-alternatives --install /usr/bin/x x /usr/bin/$other 1\n}\n"
	got := alternatives(post)
	want := map[string]string{"/usr/bin/vim": "/usr/bin/vim.basic", "/usr/bin/vi": "/usr/bin/vim.basic", "/usr/bin/view": "/usr/bin/vim.basic"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, l := range got {
		if want[l[0]] != l[1] {
			t.Errorf("link %v", l)
		}
	}
}

// brokenPixi is a pixi that cannot solve any tool.
type brokenPixi struct{ Fake }

func (g brokenPixi) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	return Resolved{}, errors.New("conda-forge could not solve " + name + " (fake)")
}

// A tool whose first manager fails is made by the next usable option, and the admin is told.
func TestRefusedFirstManagerFallsBackToApt(t *testing.T) {
	svc, st := newService(t, Multi{Conda: brokenPixi{}, Apt: Fake{}, Plugin: PluginInstaller{}})
	e := Entry{ID: "zzvim", Name: "zzvim", Summary: "x", Kind: CKTool, Level: "optional", Manager: MgrPixi, Install: "anyone", Repls: []string{"bash"},
		Options: []Option{{Manager: MgrPixi}, {Manager: MgrApt, Package: "zzvim", Size: map[string]float64{ArchARM64: 20, ArchAMD64: 20}}}}
	if err := svc.AddEntry("admin", e); err != nil {
		t.Fatal(err)
	}
	if err := svc.InstallEntry("ann", e); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	var app App
	for _, a := range svc.Apps() {
		if a.Name == "zzvim" {
			app = a
		}
	}
	if app.State != StateInstalled || app.Manager != MgrApt {
		t.Fatalf("not made by apt: %+v", app)
	}
	if _, err := os.Stat(filepath.Join(st.BundlePath(app.ID), "bin", "zzvim")); err != nil {
		t.Fatal(err)
	}
}

// sizedFake is an installer whose dry run says how big an app would be (for the choice by dry runs).
type sizedFake struct {
	Fake
	mb   float64
	fail bool
}

func (f sizedFake) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	if f.fail {
		return Resolved{}, errors.New("not possible here (fake)")
	}
	r, err := f.Fake.Resolve(ctx, name, version, kind)
	r.DiskMB = f.mb
	return r, err
}

func compareService(t *testing.T, pixi, apt Installer) *Service {
	t.Helper()
	aptHasFn = func(string) bool { return true }
	t.Cleanup(func() { aptHasFn = AptHas })
	svc, _ := newService(t, Multi{Conda: pixi, Apt: apt, Plugin: PluginInstaller{}})
	return svc
}

func newTool(id string) Entry {
	return Entry{ID: id, Name: id, Summary: "x", Kind: CKTool, Level: "optional", Manager: MgrPixi, Install: "anyone", Repls: []string{"bash"}}
}

// The dry runs choose the smaller manager, apt winning inside its allowance; the sizes are marked as estimates.
func TestCompareChoosesBySizeAndMarksEstimates(t *testing.T) {
	cases := []struct {
		name      string
		pixi, apt float64
		aptFails  bool
		want      string
	}{
		{"apt much smaller", 200, 10, false, MgrApt},
		{"pixi much smaller", 5, 80, false, MgrPixi},
		{"apt within 20 percent and 5 MB", 10, 16, false, MgrApt},
		{"apt just outside", 10, 18, false, MgrPixi},
		{"apt not possible", 50, 1, true, MgrPixi},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := compareService(t, sizedFake{mb: c.pixi}, sizedFake{mb: c.apt, fail: c.aptFails})
			e, rows := svc.Compare(context.Background(), newTool("zzdry"))
			if e.Manager != c.want || e.Minimal == nil || !e.Minimal.Estimate {
				t.Fatalf("chose %q, minimal %+v, rows %+v", e.Manager, e.Minimal, rows)
			}
			if len(rows) != 2 || SummaryOf(rows) == "" {
				t.Fatalf("rows %+v", rows)
			}
		})
	}
}

// A measured size is never replaced by an estimate; an entry with no apt candidate in the archive is compared on pixi alone.
func TestCompareKeepsMeasuredSizesAndNeedsTheArchive(t *testing.T) {
	svc := compareService(t, sizedFake{mb: 500}, sizedFake{mb: 500})
	e := newTool("zzkeep")
	e.Options = []Option{{Manager: MgrPixi, Size: map[string]float64{thisArch(): 7}, Version: "1.0.0"}}
	got, _ := svc.Compare(context.Background(), e)
	if o, _ := got.OptionOf(MgrPixi); o.Size[thisArch()] != 7 || o.Estimate {
		t.Fatalf("the measured size was replaced: %+v", o)
	}
	aptHasFn = func(string) bool { return false }
	only, rows := svc.Compare(context.Background(), newTool("zznoapt"))
	if _, has := only.OptionOf(MgrApt); has || len(rows) != 1 || only.Manager != MgrPixi {
		t.Fatalf("no apt candidate expected: %+v %+v", only.Options, rows)
	}
}

// An entry that was never measured gets its manager from the dry runs when it is installed, and the choice is kept.
func TestInstallOfAnUnmeasuredToolChoosesByDryRun(t *testing.T) {
	svc := compareService(t, sizedFake{mb: 300}, sizedFake{mb: 3})
	e := newTool("zzauto")
	if err := svc.AddEntry("admin", e); err != nil {
		t.Fatal(err)
	}
	if err := svc.InstallEntry("ann", e); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	var app App
	for _, a := range svc.Apps() {
		if a.Name == "zzauto" {
			app = a
		}
	}
	if app.State != StateInstalled || app.Manager != MgrApt {
		t.Fatalf("not made by apt: %+v", app)
	}
	if kept, _ := svc.Entry("zzauto"); kept.Minimal == nil || kept.Manager != MgrApt {
		t.Fatalf("the choice was not kept: %+v", kept)
	}
}

// A worker makes a bundle with the manager the gateway used: pixi cannot solve an apt version (the Raspberry Pi workers failed with "No candidates were
// found for git ==2.34.1-1ubuntu1.18" when they asked pixi for it).
func TestAWorkerInstallsAnAptBundleWithApt(t *testing.T) {
	svc, st := newService(t, Multi{Conda: brokenPixi{}, Apt: Fake{Version: "2.34.1-1ubuntu1.18"}, Plugin: PluginInstaller{}})
	app := App{ID: "zzgit-2.34.1-1ubuntu1.18", Name: "zzgit", Version: "2.34.1-1ubuntu1.18", Kind: KindTool, Manager: MgrApt, State: StateInstalled}
	got := svc.Apply(context.Background(), Snapshot{Apps: []App{app}})
	if len(got.Failed) != 0 || !st.HasBundle(app.ID) {
		t.Fatalf("the worker did not make the apt bundle: %+v", got.Failed)
	}
	// and an app with no manager (made before the managers) keeps the way it always had
	old := App{ID: "zzold-1.0.0", Name: "zzold", Version: "1.0.0", Kind: KindTool, State: StateInstalled}
	svc2, st2 := newService(t, Multi{Conda: Fake{}, Apt: brokenPixi{}, Plugin: PluginInstaller{}})
	if got := svc2.Apply(context.Background(), Snapshot{Apps: []App{old}}); len(got.Failed) != 0 || !st2.HasBundle(old.ID) {
		t.Fatalf("an app with no manager: %+v", got.Failed)
	}
}

// The lock of an apt bundle travels like the lock of a pixi bundle: the packages and versions, with the checksum kept only for the CPU type it is of.
func TestTheLockOfAnAptBundleTravels(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "b")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, aptLockFile), []byte(`[{"name":"git","version":"1:2.34","sha256":"abc","arch":"amd64"}]`), 0644)
	os.WriteFile(filepath.Join(dir, aptManifestFile), []byte(`{"package":"git"}`), 0644)
	l, ok := ReadLock(dir)
	if !ok || l.Manager != MgrApt || !strings.Contains(l.Lock, "git") {
		t.Fatalf("the lock was not read: %+v %v", l, ok)
	}
	other := filepath.Join(t.TempDir(), "c")
	if err := WriteLock(other, l); err != nil {
		t.Fatal(err)
	}
	if !IsAptBundle(other) {
		t.Fatal("the lock files of an apt bundle were not written")
	}
	if _, err := os.Stat(filepath.Join(other, lockFile)); err == nil {
		t.Fatal("an apt lock must not be written as a pixi lock")
	}
}

// An apt bundle that does not have the program the entry names fails its check (the package installs it under another name); the entry's link
// makes it right, and the program is found in usr/bin, where apt puts it.
func TestAnAptToolMustHaveTheProgramTheEntryNames(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "bat-0.19")
	os.MkdirAll(filepath.Join(dest, "usr", "bin"), 0755)
	os.WriteFile(filepath.Join(dest, aptLockFile), []byte(`[{"name":"bat","version":"0.19"}]`), 0644)
	os.WriteFile(filepath.Join(dest, "usr", "bin", "batcat"), []byte("#!/bin/sh\necho bat 0.19\n"), 0755)
	err := verifyToolAs(context.Background(), dest, "bat", "bat", "0.19")
	if err == nil || !strings.Contains(err.Error(), "is not in the bundle") {
		t.Fatalf("a missing program passed: %v", err)
	}
	makeLink(dest, "/usr/bin/bat", "/usr/bin/batcat")
	if err := verifyToolAs(context.Background(), dest, "bat", "bat", "0.19"); err != nil {
		t.Fatalf("with the link: %v", err)
	}
	// a pixi bundle with no such program is left alone (as before)
	pix := t.TempDir()
	if err := verifyToolAs(context.Background(), pix, "nothing", "nothing", "1"); err != nil {
		t.Fatalf("a pixi bundle: %v", err)
	}
	// the links of an option are checked: apt only, paths inside the bundle
	e := Entry{ID: "zz", Name: "zz", Summary: "x", Kind: CKTool, Level: "optional", Manager: MgrApt, Install: "anyone", Repls: []string{"bash"}}
	e.Options = []Option{{Manager: MgrApt, Links: map[string]string{"usr/bin/bat": "usr/bin/batcat"}}}
	if err := ValidateOptions(e); err != nil {
		t.Fatalf("a good link: %v", err)
	}
	for _, bad := range []map[string]string{{"../x": "usr/bin/y"}, {"usr/bin/x": "/etc/passwd"}, {"": "usr/bin/y"}} {
		e.Options = []Option{{Manager: MgrApt, Links: bad}}
		if ValidateOptions(e) == nil {
			t.Errorf("link %v accepted", bad)
		}
	}
}

// versionSeen records the version each install is asked for.
type versionSeen struct {
	Fake
	got *[]string
}

func (v versionSeen) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	*v.got = append(*v.got, version)
	return v.Fake.Install(ctx, name, version, kind, dest, log)
}

// A worker that installs a partial bundle does not ask for the gateway's version (it may not exist for its CPU type): it asks for the best one.
func TestAWorkerInstallsAPartialBundleUnpinned(t *testing.T) {
	var got []string
	svc, st := newService(t, Multi{Conda: versionSeen{Fake: Fake{Version: "1.14.5"}, got: &got}, Plugin: PluginInstaller{}})
	app := App{ID: "zzpart-1.16.5", Name: "zzpart", Version: "1.16.5", Kind: KindLib, Partial: true, State: StateInstalled}
	if res := svc.Apply(context.Background(), Snapshot{Apps: []App{app}}); len(res.Failed) != 0 || !st.HasBundle(app.ID) {
		t.Fatalf("the worker did not install it: %+v", res.Failed)
	}
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("the worker asked for %q, not for the best version", got)
	}
}
