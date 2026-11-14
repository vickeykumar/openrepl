//go:build realinstall

package appstore

// The real thing: micromamba, conda-forge, the image's Python and the filesystem sandbox. It is not part of `go test` (it needs
// the internet and takes a long time); the CI job "App Store installs" builds it with -tags realinstall and runs it inside the
// image that was just built (docker-image.yml), so what it proves is what a user gets. It installs EVERY app of the safe list.
//
//	OPENREPL_MICROMAMBA   the program (default: the PATH)
//	OPENREPL_REAL_STORE   the folder of the store (default: a temporary one)
//	OPENREPL_HELPER       landlock-helper (default: the PATH)
//	OPENREPL_REAL_ONLY    a comma list of names, for a quick run (the CI installs the whole safe list)

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var realOnce sync.Once
var realSvc *Service
var realStore Store

func realService(t *testing.T) (*Service, Store) {
	t.Helper()
	realOnce.Do(func() {
		dir := os.Getenv("OPENREPL_REAL_STORE")
		if dir == "" {
			dir = os.TempDir() + "/real-store"
		}
		bin := os.Getenv("OPENREPL_MICROMAMBA")
		if bin == "" {
			bin, _ = exec.LookPath("micromamba")
		}
		if bin == "" {
			return
		}
		py := PythonOf("python3")
		realStore = Store{Dir: dir}
		realSvc = New(Config{Store: realStore, KV: newKV(), Installer: Mamba{Bin: bin, Dir: dir, Python: py}, Safe: func() []string { return DefaultSafeList }})
	})
	if realSvc == nil {
		t.Fatal("micromamba was not found: set OPENREPL_MICROMAMBA")
	}
	return realSvc, realStore
}

// the module a package brings, where it is not the package's name
var realModules = map[string]string{"beautifulsoup4": "bs4", "pillow": "PIL", "pyyaml": "yaml", "scikit-learn": "sklearn"}

// the tools of the safe list: a program that must run
var realTools = map[string][]string{"jq": {"jq", "--version"}, "pytest": {"pytest", "--version"}, "flask": {"flask", "--version"}}

func TestEveryAppOfTheSafeListInstallsAndWorks(t *testing.T) {
	svc, st := realService(t)
	t.Logf("python of the server: %q, installer: %s", PythonOf("python3"), svc.Installer().Name())
	names := DefaultSafeList
	if only := os.Getenv("OPENREPL_REAL_ONLY"); only != "" { // a quick run on a laptop: a few names; the CI installs them all
		names = strings.Split(only, ",")
	}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			if err := svc.Install("ci", name, KindLib, false); err != nil {
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
			env := VarsFor(st.Dir, []string{app.ID}).Apply(os.Environ())
			// the system's python3 imports it through the PYTHONPATH of the bundle: what a user's terminal does
			if mod := realModules[name]; mod != "" || moduleOf(st.BundlePath(app.ID), name) != "" {
				if mod == "" {
					mod = moduleOf(st.BundlePath(app.ID), name)
				}
				c := exec.Command("python3", "-c", "import "+mod)
				c.Env, c.Dir = env, os.TempDir()
				if out, err := c.CombinedOutput(); err != nil {
					t.Errorf("python3 cannot import %s: %v\n%s", mod, err, tailOf(out))
				}
			}
			if cmd, ok := realTools[name]; ok {
				// the program is looked up in the bundle's own bin (exec looks in the PATH of this process, not in the one given to the child)
				c := exec.Command(filepath.Join(st.BundlePath(app.ID), "bin", cmd[0]), cmd[1:]...)
				c.Env, c.Dir = env, os.TempDir()
				if out, err := c.CombinedOutput(); err != nil {
					t.Errorf("%s does not run: %v\n%s", strings.Join(cmd, " "), err, tailOf(out))
				}
			}
		})
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

// What a worker does: take the catalog from the gateway, the bundles as tarballs, and have the same bundles working.
func TestAWorkerTakesTheBundlesFromTheGateway(t *testing.T) {
	svc, st := realService(t)
	var flask App
	for _, a := range svc.Apps() {
		if a.Name == "flask" {
			flask = a
		}
	}
	if flask.ID == "" {
		t.Skip("flask is not installed (the install test did not run)")
	}
	// the same folder is needed on the worker (the bundle holds its path), so the worker's store is this folder, emptied of the bundle
	snap := Snapshot{Full: true, Apps: []App{flask}, Users: map[string][]string{"ann": {flask.ID}}}
	var tarball bytes.Buffer
	if err := st.WriteBundle(flask.ID, &tarball); err != nil {
		t.Fatal(err)
	}
	t.Logf("flask travels as %.0f MB", float64(tarball.Len())/(1<<20))
	if err := st.RemoveBundle(flask.ID); err != nil {
		t.Fatal(err)
	}
	worker := New(Config{Store: st, KV: newKV(), Installer: Missing{Reason: "a worker must not need to install"},
		Fetch: func(ctx context.Context, id, dest string) error {
			return ReadBundle(bytes.NewReader(tarball.Bytes()), dest)
		}})
	state := worker.Apply(context.Background(), snap)
	if len(state.Failed) != 0 || !st.HasBundle(flask.ID) {
		t.Fatalf("the worker could not apply the catalog: %+v", state)
	}
	c := exec.Command("python3", "-c", "import flask; print(flask.__file__)")
	c.Env, c.Dir = VarsFor(st.Dir, []string{flask.ID}).Apply(os.Environ()), os.TempDir()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("flask from the tarball does not import: %v\n%s", err, tailOf(out))
	}
	if err := checkBundle(context.Background(), st.BundlePath(flask.ID), "flask"); err != nil {
		t.Fatalf("the bundle from the tarball fails its check: %v", err)
	}
}
