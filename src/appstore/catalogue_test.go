package appstore

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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
	for name, want := range map[string]string{"benchmark": `"libstdcxx-ng" = "<13"`, "perl-try-tiny": `"perl" = "5.34.*"`, "tcllib": `"tcl" = "8.6.*"`} {
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
