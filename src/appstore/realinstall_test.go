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
