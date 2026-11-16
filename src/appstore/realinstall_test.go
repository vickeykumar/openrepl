//go:build realinstall

package appstore

// The real thing: pixi, conda-forge, the image's Python and the filesystem sandbox. It is not part of `go test` (it needs
// the internet and takes a long time); the CI job "App Store installs" builds it with -tags realinstall and runs it inside the
// image that was just built (docker-image.yml), so what it proves is what a user gets. It installs EVERY app of the safe list.
//
//	OPENREPL_PIXI         the program (default: the PATH)
//	OPENREPL_REAL_STORE   the folder of the store (default: a temporary one)
//	OPENREPL_HELPER       landlock-helper (default: the PATH)
//	OPENREPL_REAL_ONLY    a comma list of names, for a quick run (the CI installs the whole safe list)

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var realOnce sync.Once
var realSvc *Service
var realStore Store

// npm: OPENREPL_NPM, the PATH, or the nvm of the image (where the Node REPLs get theirs)
func findRealNpm() string {
	if p := os.Getenv("OPENREPL_NPM"); p != "" {
		return p
	}
	if p, err := exec.LookPath("npm"); err == nil {
		return p
	}
	if m, _ := filepath.Glob("/root/.nvm/versions/node/*/bin/npm"); len(m) > 0 {
		return m[len(m)-1]
	}
	return ""
}

func realService(t *testing.T) (*Service, Store) {
	t.Helper()
	realOnce.Do(func() {
		dir := os.Getenv("OPENREPL_REAL_STORE")
		if dir == "" {
			dir = os.TempDir() + "/real-store"
		}
		bin := os.Getenv("OPENREPL_PIXI")
		if bin == "" {
			bin, _ = exec.LookPath("pixi")
		}
		if bin == "" {
			return
		}
		py := PythonOf("python3")
		realStore = Store{Dir: dir}
		inst := Multi{Conda: Pixi{Bin: bin, Dir: dir, Python: py}, Plugin: PluginInstaller{}}
		if npm := findRealNpm(); npm != "" {
			inst.Node = Npm{Bin: npm, Dir: dir}
		}
		realSvc = New(Config{Store: realStore, KV: newKV(), Installer: inst, Safe: func() []string { return DefaultSafeList }})
	})
	if realSvc == nil {
		t.Fatal("pixi was not found: set OPENREPL_PIXI")
	}
	return realSvc, realStore
}

// the module a package brings, where it is not the package's name
var realModules = map[string]string{"beautifulsoup4": "bs4", "pillow": "PIL", "pyyaml": "yaml", "scikit-learn": "sklearn"}

// the tools of the safe list: a program that must run
var realTools = map[string][]string{"pytest": {"pytest", "--version"}, "flask": {"flask", "--version"}}

func TestEveryAppOfTheSafeListInstallsAndWorks(t *testing.T) {
	svc, st := realService(t)
	t.Logf("python of the server: %q, installer: %s", PythonOf("python3"), svc.Installer().Name())
	entries := DefaultSafeList
	if only := os.Getenv("OPENREPL_REAL_ONLY"); only != "" { // a quick run on a laptop: a few entries; the CI installs them all
		entries = strings.Split(only, ",")
	}
	for _, entry := range entries {
		kind, name := safeEntry(entry)
		entry := entry
		t.Run(entry, func(t *testing.T) {
			if kind == KindNode {
				if findRealNpm() == "" {
					t.Skip("npm is not here")
				}
			}
			if err := svc.Install("ci", name, kind, false); err != nil {
				t.Fatal(err)
			}
			svc.Wait()
			var app App
			for _, a := range svc.Apps() {
				if a.Name == name {
					app = a
				}
			}
			if app.State != StateInstalled || !st.HasBundle(app.ID) {
				t.Fatalf("not installed: %+v", app)
			}
			t.Logf("%s %s: %.0f MB, %d packages", name, app.Version, float64(app.Size)/(1<<20), app.Packages)
			// what turning it on does for a user: the app's own check, with the user's environment
			if err := RunVerify(context.Background(), st, "ann", app.ID, name, kind, "", nil); err != nil {
				t.Fatalf("the check for a user: %v", err)
			}
			env := VarsFor(st.Dir, []string{app.ID}).Apply(os.Environ())
			switch kind {
			case KindLib:
				// the system's python3 imports it through the PYTHONPATH of the bundle: what a user's terminal does
				mod := realModules[name]
				if mod == "" {
					mod = moduleOf(st.BundlePath(app.ID), name)
				}
				if mod != "" {
					c := exec.Command("python3", "-c", "import "+mod)
					c.Env, c.Dir = env, os.TempDir()
					if out, err := c.CombinedOutput(); err != nil {
						t.Errorf("python3 cannot import %s: %v\n%s", mod, err, tailOf(out))
					}
				}
				if cmd, ok := realTools[name]; ok {
					runInBundle(t, st, app, env, cmd)
				}
			case KindTool:
				runInBundle(t, st, app, env, []string{name, "--version"})
			case KindNode:
				node, err := exec.LookPath("node")
				if err != nil {
					if m, _ := filepath.Glob("/root/.nvm/versions/node/*/bin/node"); len(m) > 0 {
						node, err = m[len(m)-1], nil
					}
				}
				if err == nil {
					c := exec.Command(node, "-e", "require('"+name+"'); console.log('loaded')")
					c.Env, c.Dir = env, os.TempDir()
					if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "loaded") {
						t.Errorf("node cannot load %s: %v\n%s", name, err, tailOf(out))
					}
				}
			}
		})
	}
}

// the program is looked up in the bundle's own folder (exec looks in the PATH of this process, not in the one given to the child)
func runInBundle(t *testing.T, st Store, app App, env []string, cmd []string) {
	t.Helper()
	bin := filepath.Join(PrefixOf(st.BundlePath(app.ID)), "bin", cmd[0])
	if _, err := os.Stat(bin); err != nil {
		bin = filepath.Join(st.BundlePath(app.ID), "node_modules", ".bin", cmd[0])
	}
	c := exec.Command(bin, cmd[1:]...)
	c.Env, c.Dir = env, os.TempDir()
	if out, err := c.CombinedOutput(); err != nil {
		t.Errorf("%s does not run: %v\n%s", strings.Join(cmd, " "), err, tailOf(out))
	}
}

// A language and a plugin, the two kinds an admin installs: a language brings a program a terminal can run, a plugin its script.
func TestALanguageAndAPluginAreInstalledByAnAdmin(t *testing.T) {
	svc, st := realService(t)
	if err := svc.Install("ci", "lua", KindLang, false); err != ErrNeedsApproval {
		t.Fatalf("a user cannot install a language: %v", err)
	}
	if err := svc.Install("root", "lua", KindLang, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Install("root", "wordcount", KindPlugin, true); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	var lua, plugin App
	for _, a := range svc.Apps() {
		switch a.Name {
		case "lua":
			lua = a
		case "wordcount":
			plugin = a
		}
	}
	if lua.State != StateInstalled || plugin.State != StateInstalled {
		t.Fatalf("lua %+v, plugin %+v", lua, plugin)
	}
	env := VarsFor(st.Dir, []string{lua.ID}).Apply(os.Environ())
	c := exec.Command(filepath.Join(PrefixOf(st.BundlePath(lua.ID)), "bin", "lua"), "-e", "print('lua works')")
	c.Env, c.Dir = env, os.TempDir()
	if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "lua works") {
		t.Errorf("lua does not run: %v %s", err, tailOf(out))
	}
	code, err := st.PluginCode(plugin.ID)
	if err != nil || !strings.Contains(code, "openreplPlugin") {
		t.Errorf("the plugin's script: %v %.80s", err, code)
	}
}

// The search of the store, for real: conda-forge and npm answer, and what is found can be sized before it is installed.
func TestSearchAndSizeAreAnsweredForReal(t *testing.T) {
	svc, _ := realService(t)
	hits, err := svc.Search(context.Background(), "flask", "")
	if err != nil || len(hits) == 0 {
		t.Fatalf("a search for flask: %v %v", err, hits)
	}
	found := false
	for _, h := range hits {
		if h.Name == "flask" && h.Kind == KindLib && h.Version != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("conda-forge did not list flask: %v", hits)
	}
	r, err := svc.Estimate(context.Background(), "requests", KindLib)
	if err != nil || r.Version == "" || r.DownloadMB <= 0 || r.Packages < 2 {
		t.Errorf("the size of requests before it is installed: %+v %v", r, err)
	}
}

func tailOf(b []byte) string {
	s := string(b)
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return s
}

// A terminal sees exactly the bundles on its user's list, under the real filesystem sandbox.
func TestTheSandboxLetsOnlyTheListedBundlesThrough(t *testing.T) {
	svc, st := realService(t)
	helper := os.Getenv("OPENREPL_HELPER")
	if helper == "" {
		helper, _ = exec.LookPath("landlock-helper")
	}
	if helper == "" {
		t.Skip("landlock-helper is not here")
	}
	if err := exec.Command(helper, "--probe").Run(); err != nil {
		t.Skipf("this host has no Landlock: %v", err)
	}
	ids := map[string]string{}
	for _, a := range svc.Apps() {
		ids[a.Name] = a.ID
	}
	if ids["flask"] == "" || ids["requests"] == "" {
		t.Skip("flask and requests are not installed (the install test did not run)")
	}
	root := t.TempDir()
	for _, home := range []string{"ann", "bob"} {
		os.MkdirAll(filepath.Join(root, home), 0755)
	}
	if err := st.WriteManifest("ann", []string{ids["flask"]}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest("bob", []string{ids["requests"]}); err != nil {
		t.Fatal(err)
	}
	run := func(home string, args ...string) (string, error) {
		a := append([]string{"--workspace", filepath.Join(root, home), "--home-root", root, "--store-dir", st.Dir, "--"}, args...)
		var out bytes.Buffer
		c := exec.CommandContext(context.Background(), helper, a...)
		c.Stdout, c.Stderr = &out, &out
		err := c.Run()
		return out.String(), err
	}
	if out, err := run("ann", "python3", "-c", "import flask; print('ann has flask')"); err != nil || !strings.Contains(out, "ann has flask") {
		t.Errorf("ann cannot use flask: %v %s", err, tailOf([]byte(out)))
	}
	if out, err := run("ann", "python3", "-c", "import requests"); err == nil {
		t.Errorf("ann can use requests, which is not on her list: %s", out)
	}
	if out, err := run("bob", "python3", "-c", "import requests; print('bob has requests')"); err != nil || !strings.Contains(out, "bob has requests") {
		t.Errorf("bob cannot use requests: %v %s", err, tailOf([]byte(out)))
	}
	if out, err := run("bob", "python3", "-c", "import flask"); err == nil {
		t.Errorf("bob can use flask, which is not on his list: %s", out)
	}
	for _, args := range [][]string{{"ls", filepath.Join(st.Dir, "apps")}, {"ls", st.BundlePath(ids["requests"])}, {"ls", filepath.Join(st.Dir, "envs", "bob")}} {
		if out, err := run("ann", args...); err == nil {
			t.Errorf("ann listed %s: %s", args[1], out)
		}
	}
	if out, err := run("ann", "sh", "-c", "echo x > "+filepath.Join(st.BundlePath(ids["flask"]), "new")); err == nil {
		t.Errorf("a terminal wrote into a bundle: %s", out)
	}
	if out, err := run("ann", "sh", "-c", "echo '[]' > "+filepath.Join(st.Dir, "envs", "ann", "manifest.json")); err == nil {
		t.Errorf("a terminal changed its own list: %s", out)
	}
}

// the packages an environment holds, from its conda-meta: name-version-build for each
func installedPackages(t *testing.T, bundle string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(PrefixOf(bundle), "conda-meta", "*.json"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	sort.Strings(out)
	return out
}

// A node that installs from the lock of the catalog gets exactly the packages the gateway got, and the lock is what a worker is given
// over the tunnel: nothing is solved again.
func TestAWorkerInstallsExactlyWhatTheLockOfTheGatewaySays(t *testing.T) {
	svc, st := realService(t)
	// flask is installed already when the whole suite runs (the safe list test); alone, this test installs it
	if err := svc.Install("ci", "flask", KindLib, false); err != nil && !errors.Is(err, ErrInstalled) {
		t.Fatal(err)
	}
	svc.Wait()
	app, ok := svc.AppByID("flask-" + versionOf(svc, "flask"))
	if !ok || !app.Locked {
		t.Skipf("flask is not installed with a lock: %+v", app)
	}
	lock, ok := svc.LockOf(app.ID)
	if !ok || !strings.Contains(lock.Lock, "linux-64") || !strings.Contains(lock.Lock, "linux-aarch64") {
		t.Fatalf("the lock must cover both CPU types: %v", ok)
	}
	// a second node: its own store, the same installer, the lock from the catalog (no solving: the lock says what to fetch)
	dir := t.TempDir()
	node := New(Config{Store: Store{Dir: dir}, KV: newKV(), Installer: Pixi{Bin: realPixiBin(), Dir: dir, Python: PythonOf("python3")}})
	node.SetLock(func(ctx context.Context, id string) (Lock, error) { return lock, nil })
	st2 := node.Apply(context.Background(), Snapshot{Full: true, Apps: []App{app}})
	if len(st2.Failed) != 0 {
		t.Fatalf("the node could not install from the lock: %+v", st2)
	}
	a, b := installedPackages(t, st.BundlePath(app.ID)), installedPackages(t, Store{Dir: dir}.BundlePath(app.ID))
	if len(a) < 20 || strings.Join(a, " ") != strings.Join(b, " ") {
		t.Errorf("not the same packages:\n%v\n%v", a, b)
	}
}

func versionOf(svc *Service, name string) string {
	for _, a := range svc.Apps() {
		if a.Name == name {
			return a.Version
		}
	}
	return ""
}

func realPixiBin() string {
	if p := os.Getenv("OPENREPL_PIXI"); p != "" {
		return p
	}
	p, _ := exec.LookPath("pixi")
	return p
}

// A package that sets variables when it is activated (proj: PROJ_DATA) has them in the environment of a user who has it on.
func TestThePackagesActivationVariablesReachTheUsersEnvironment(t *testing.T) {
	svc, st := realService(t)
	if err := svc.Install("root", "proj", KindTool, true); err != nil && !errors.Is(err, ErrInstalled) {
		t.Fatal(err)
	}
	svc.Wait()
	app, ok := svc.AppByID("proj-" + versionOf(svc, "proj"))
	if !ok {
		t.Fatalf("proj is not installed: %v", svc.Apps())
	}
	v := VarsFor(st.Dir, []string{app.ID})
	want := filepath.Join(PrefixOf(st.BundlePath(app.ID)), "share", "proj")
	if v.Extra["PROJ_DATA"] != want {
		t.Fatalf("PROJ_DATA %q want %q (all: %v)", v.Extra["PROJ_DATA"], want, v.Extra)
	}
	for k := range v.Extra {
		if strings.HasPrefix(k, "PIXI_") || strings.HasPrefix(k, "CONDA_") || k == "PATH" {
			t.Errorf("%s must not be passed on", k)
		}
	}
	// and the program uses it: projinfo finds its data through the variable
	c := exec.Command(filepath.Join(PrefixOf(st.BundlePath(app.ID)), "bin", "projinfo"), "EPSG:4326")
	c.Env, c.Dir = v.Apply(os.Environ()), os.TempDir()
	if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "WGS 84") {
		t.Errorf("projinfo: %v\n%s", err, tailOf(out))
	}
}

// The core of the catalogue (every entry of level core that anyone may install) is installed, checked with the entry's own check, and used the way a
// terminal does with its scratch folder: a Python library that is imported leaves nothing in the workspace (the bytecode goes to scratch), and a
// browser library has the files the preview serves. This is the "workspace growth" check, on both CPU types in CI.
func TestTheCoreOfTheCatalogueInstallsAndLeavesTheWorkspaceAlone(t *testing.T) {
	svc, st := realService(t)
	only := os.Getenv("OPENREPL_REAL_ONLY")
	for _, e := range svc.Catalogue() {
		k := e.InstallKind()
		if e.Level != "core" || e.Install != "anyone" || k == "" || k == KindPlugin || e.Python == "2.7" {
			continue
		}
		if only != "" && !strings.Contains(","+only+",", ","+e.PackageName()+",") {
			continue
		}
		e := e
		t.Run(e.ID, func(t *testing.T) {
			if k == KindNode && findRealNpm() == "" {
				t.Skip("npm is not here")
			}
			if err := svc.InstallEntry("ci", e); err != nil && !errors.Is(err, ErrInstalled) { // an app that an earlier test of the run installed is used as it is
				t.Fatal(err)
			}
			svc.Wait()
			var app App
			for _, a := range svc.Apps() {
				if a.Name == e.PackageName() && cleanKind(a.Kind) == k {
					app = a
				}
			}
			if app.State != StateInstalled {
				t.Fatalf("not installed: %+v", app)
			}
			if err := svc.checkApp(context.Background(), st.BundlePath(app.ID), app); err != nil {
				t.Fatalf("the entry's check: %v", err)
			}
			if e.Kind == CKWeb {
				for _, f := range e.Files {
					if _, ok := svc.LibFile(e.PackageName(), f); !ok {
						t.Errorf("the preview cannot serve %s", f)
					}
				}
				return
			}
			if e.Kind != CKLib {
				return
			}
			workspace, scratchDir := t.TempDir(), t.TempDir()
			env := append(VarsFor(st.Dir, []string{app.ID}).Apply(os.Environ()), "PYTHONPYCACHEPREFIX="+filepath.Join(scratchDir, "pycache"), "MPLCONFIGDIR="+filepath.Join(scratchDir, "mpl"), "HOME="+workspace)
			mod := e.CheckArg
			if mod == "" {
				mod = moduleOf(st.BundlePath(app.ID), e.PackageName())
			}
			if mod == "" {
				return
			}
			c := exec.Command("python3", "-c", "import "+mod)
			c.Env, c.Dir = env, workspace
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("python3 cannot import %s: %v\n%s", mod, err, tailOf(out))
			}
			var grown []string
			filepath.WalkDir(workspace, func(p string, d os.DirEntry, err error) error {
				if err == nil && p != workspace {
					grown = append(grown, strings.TrimPrefix(p, workspace))
				}
				return nil
			})
			if len(grown) > 0 {
				t.Errorf("importing %s wrote in the workspace: %v", mod, grown)
			}
		})
	}
}

// The mirrors, for real (go, java and cargo of the image, the real registries): one library of each kind is installed from the default catalogue,
// passes its own check, and is then USED THE WAY A TERMINAL DOES, with no internet: a Go program that imports the module builds with GOPROXY=file://,
// javac compiles and java runs a class that uses the jar from the CLASSPATH, and cargo builds a crate that depends on the vendored crate.
func TestTheMirrorsInstallAndAreUsedOffline(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	byID := map[string]Entry{}
	entries, _ := DefaultCatalogue()
	for _, e := range entries {
		byID[e.ID] = e
	}
	lookup := func(kind, app string) (Entry, bool) {
		for _, e := range entries {
			if e.InstallKind() == kind && e.PackageName() == app {
				return e, true
			}
		}
		return Entry{}, false
	}
	only := os.Getenv("OPENREPL_REAL_ONLY")
	want := func(id string) bool { return only == "" || strings.Contains(","+only+",", ","+id+",") }
	install := func(t *testing.T, in Installer, e Entry) (string, string) {
		t.Helper()
		k := e.InstallKind()
		res, err := in.Resolve(context.Background(), e.PackageName(), "", k)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		t.Logf("%s resolves to %s (%d packages, %.1f MB)", e.ID, res.Version, res.Packages, res.DiskMB)
		dest := st.BundlePath(BundleID(e.PackageName(), res.Version))
		var log bytes.Buffer
		if err := in.Install(context.Background(), e.PackageName(), res.Version, k, dest, &log); err != nil {
			t.Fatalf("install: %v\n%s", err, log.String())
		}
		t.Log(strings.TrimSpace(log.String()))
		return dest, res.Version
	}
	run := func(t *testing.T, dir string, env []string, prog string, args ...string) string {
		t.Helper()
		c := exec.Command(prog, args...)
		c.Dir, c.Env = dir, append(os.Environ(), env...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", prog, args, err, tailOf(out))
		}
		return string(out)
	}
	if want("go:cobra") {
		t.Run("go:cobra", func(t *testing.T) {
			e := byID["go:cobra"]
			g := GoMod{Dir: st.Dir, Lookup: lookup}
			dest, v := install(t, g, e)
			if err := g.check(context.Background(), dest, e, v); err != nil {
				t.Fatal(err)
			}
			// a user's program, with only the mirror
			ws := t.TempDir()
			os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\nimport (\"fmt\"; \"github.com/spf13/cobra\")\nfunc main(){ c := &cobra.Command{Use:\"hi\"}; fmt.Println(c.Use) }\n"), 0644)
			env := VarsFor(st.Dir, []string{BundleID(e.PackageName(), v)}).Apply([]string{"GOTOOLCHAIN=local", "GOMODCACHE=" + filepath.Join(ws, "m"), "GOCACHE=" + filepath.Join(ws, "c"), "GOPATH=" + filepath.Join(ws, "p"), "GO111MODULE=on"})
			run(t, ws, env, "go", "mod", "init", "x")
			run(t, ws, env, "go", "get", "github.com/spf13/cobra@v"+v)
			if out := run(t, ws, env, "go", "run", "."); !strings.Contains(out, "hi") {
				t.Errorf("the program printed %q", out)
			}
		})
	}
	if want("jar:gson") {
		t.Run("jar:gson", func(t *testing.T) {
			e := byID["jar:gson"]
			m := Maven{Dir: st.Dir, Lookup: lookup}
			dest, v := install(t, m, e)
			if err := m.check(context.Background(), dest, e); err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			os.WriteFile(filepath.Join(ws, "T.java"), []byte("public class T { public static void main(String[] a){ System.out.println(new com.google.gson.Gson().toJson(java.util.List.of(1,2))); } }\n"), 0644)
			env := VarsFor(st.Dir, []string{BundleID(e.PackageName(), v)}).Apply([]string{"HOME=" + ws})
			run(t, ws, env, "javac", "T.java")
			if out := run(t, ws, env, "java", "T"); !strings.Contains(out, "[1,2]") {
				t.Errorf("java printed %q", out)
			}
		})
	}
	if want("crate:serde_json") {
		t.Run("crate:serde_json", func(t *testing.T) {
			e := byID["crate:serde_json"]
			c := Cargo{Dir: st.Dir, Lookup: lookup}
			dest, v := install(t, c, e)
			if err := c.check(context.Background(), dest, e, v); err != nil {
				t.Fatal(err)
			}
		})
	}
	if want("py27:six") {
		t.Run("py27:six", func(t *testing.T) {
			bin := os.Getenv("OPENREPL_PIXI")
			if bin == "" {
				bin, _ = exec.LookPath("pixi")
			}
			if bin == "" {
				t.Skip("pixi is not here")
			}
			e := byID["py27-six"]
			svc := New(Config{Store: st, KV: newKV(), Installer: Multi{Conda: Pixi{Bin: bin, Dir: st.Dir, Python: PythonOf("python3")}}, Safe: func() []string { return nil }})
			if err := svc.InstallEntry("ci", e); err != nil {
				t.Fatal(err)
			}
			svc.Wait()
			var app App
			for _, a := range svc.Apps() {
				if a.Name == e.PackageName() {
					app = a
				}
			}
			if app.State != StateInstalled {
				t.Fatalf("not installed: %+v", app)
			}
			py := filepath.Join(PrefixOf(st.BundlePath(app.ID)), "bin", "python")
			if out := run(t, t.TempDir(), nil, py, "-c", "import six, sys; sys.stdout.write(str(sys.version_info[0]) + ' ' + six.__version__)"); !strings.HasPrefix(out, "2 ") {
				t.Errorf("python 2.7 printed %q", out)
			}
		})
	}
}
