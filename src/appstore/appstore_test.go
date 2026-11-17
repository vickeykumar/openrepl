package appstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newKV() *memKV { return &memKV{m: map[string][]byte{}} }
func (k *memKV) Store(key, v []byte) error {
	k.m[string(key)] = append([]byte(nil), v...)
	return nil
}
func (k *memKV) Fetch(key []byte) ([]byte, error) {
	v, ok := k.m[string(key)]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}
func (k *memKV) Delete(key []byte) error { delete(k.m, string(key)); return nil }
func (k *memKV) Commit() error           { return nil }
func (k *memKV) Each(fn func(k, v []byte) bool) error {
	for key, v := range k.m {
		if !fn([]byte(key), v) {
			break
		}
	}
	return nil
}

func newService(t *testing.T, inst Installer, safe ...string) (*Service, Store) {
	t.Helper()
	st := Store{Dir: t.TempDir()}
	svc := New(Config{Store: st, KV: newKV(), Installer: inst, Safe: func() []string { return safe }})
	return svc, st
}

func TestNames(t *testing.T) {
	for _, ok := range []string{"flask", "scikit-learn", "numpy", "a.b_c-d", "python3"} {
		if !ValidName(ok) {
			t.Errorf("%q should be a name", ok)
		}
	}
	for _, bad := range []string{"", "Flask", "../x", "a/b", "-a", "a b", "a..b", strings.Repeat("a", 65)} {
		if ValidName(bad) {
			t.Errorf("%q should not be a name", bad)
		}
	}
	for _, ok := range []string{"flask-3.0.2", "scikit-learn-1.5.2", "numpy-2.1.0", "x-1"} {
		if !ValidBundle(ok) {
			t.Errorf("%q should be a bundle", ok)
		}
	}
	for _, bad := range []string{"flask", "../flask-1", "flask-1/../x", "flask-1 ", "flask-x", "-1", "flask-1..2", "a/b-1"} {
		if ValidBundle(bad) {
			t.Errorf("%q should not be a bundle", bad)
		}
	}
	if !ValidHome("vickohna") || !ValidHome("guest-0e21e4b2e2") || ValidHome("..") || ValidHome("a/b") || ValidHome("") {
		t.Error("homes")
	}
	if !ValidApt("postgresql-client") || ValidApt("a b") || ValidApt("-x") {
		t.Error("apt names")
	}
}

func TestManifestAndEnv(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if err := st.Ready(); err != nil {
		t.Fatal(err)
	}
	// a bundle that is not finished cannot be put on a list
	if err := st.WriteManifest("ann", []string{"flask-3.0.2"}); err == nil {
		t.Fatal("an unfinished bundle must be refused")
	}
	f := Fake{}
	dest := st.BundlePath("flask-3.0.2")
	if err := f.Install(context.Background(), "flask", "3.0.2", KindLib, dest, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if st.HasBundle("flask-3.0.2") {
		t.Fatal("no marker yet")
	}
	if _, err := st.Finish("flask-3.0.2", "flask", "3.0.2"); err != nil {
		t.Fatal(err)
	}
	if got := st.Bundles(); !reflect.DeepEqual(got, []string{"flask-3.0.2"}) {
		t.Fatalf("bundles %v", got)
	}
	if err := st.WriteManifest("ann", []string{"flask-3.0.2", "flask-3.0.2"}); err != nil {
		t.Fatal(err)
	}
	ids, err := st.Manifest("ann")
	if err != nil || !reflect.DeepEqual(ids, []string{"flask-3.0.2"}) {
		t.Fatalf("manifest %v %v", ids, err)
	}
	if none, err := st.Manifest("bob"); err != nil || len(none) != 0 {
		t.Fatalf("no manifest is an empty list: %v %v", none, err)
	}
	script, _ := os.ReadFile(filepath.Join(st.HomeDir("ann"), "env.sh"))
	if !strings.Contains(string(script), "PYTHONPATH=") || !strings.Contains(string(script), "flask-3.0.2/lib/python3.10/site-packages") ||
		!strings.Contains(string(script), `export PATH="$PATH":`) {
		t.Fatalf("env.sh:\n%s", script)
	}
	env := VarsFor(st.Dir, ids).Apply([]string{"PATH=/usr/bin", "HOME=/h"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin:"+st.Dir+"/apps/flask-3.0.2/bin") || !strings.Contains(joined, "PYTHONPATH="+st.Dir) || strings.Contains(joined, "LD_LIBRARY_PATH") {
		t.Fatalf("env:\n%s", joined)
	}
	// a bad entry in a manifest file is an error, never skipped
	bad := filepath.Join(st.HomeDir("eve"), "manifest.json")
	os.MkdirAll(filepath.Dir(bad), 0755)
	for _, content := range []string{`["../../etc"]`, `["flask-3.0.2","/x-1"]`, `{"a":1}`} {
		os.WriteFile(bad, []byte(content), 0644)
		if _, err := ReadManifest(bad); err == nil {
			t.Errorf("%s must be refused", content)
		}
	}
}

func TestDirSizeCountsHardLinksOnce(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a"), make([]byte, 1000), 0644)
	os.Link(filepath.Join(d, "a"), filepath.Join(d, "b"))
	if got := DirSize(d); got != 1000 {
		t.Fatalf("size %d", got)
	}
}

func TestInstallSafeAndRequest(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask")
	if err := svc.Install("ann", "polars", "lib", false); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("not on the safe list: %v", err)
	}
	if err := svc.Install("ann", "Flask", "lib", false); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	apps := svc.Apps()
	if len(apps) != 1 || apps[0].State != StateInstalled || apps[0].ID != "flask-1.0.0" || apps[0].Size <= 0 || !st.HasBundle("flask-1.0.0") {
		t.Fatalf("apps %+v", apps)
	}
	if err := svc.Install("bob", "flask", "lib", false); !errors.Is(err, ErrInstalled) {
		t.Fatalf("again: %v", err)
	}
	// a request is joined by a second asker, then approved: installed, and the request is gone
	r, err := svc.Request("ann", "polars", "lib", "arrays")
	if err != nil || r.Status != "waiting" {
		t.Fatal(r, err)
	}
	svc.Request("bob", "polars", "lib", "")
	if reqs := svc.Requests(); len(reqs) != 1 || len(reqs[0].Askers) != 2 || reqs[0].Reason != "arrays" {
		t.Fatalf("requests %+v", reqs)
	}
	if err := svc.Approve("root", "polars"); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if reqs := svc.Requests(); len(reqs) != 1 || reqs[0].Status != "approved" || len(svc.Apps()) != 2 { // kept until the askers have seen the answer
		t.Fatalf("after approval: %+v %+v", svc.Requests(), svc.Apps())
	}
	// rejected with a reason
	svc.Request("ann", "ffmpeg", "tool", "")
	if err := svc.Reject("root", "ffmpeg", "too big"); err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, v := range svc.Views("ann", "ann") {
		if v.Name == "ffmpeg" && v.State == "rejected" && v.Answer == "too big" && v.Mine {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("views %+v", svc.Views("ann", "ann"))
	}
}

func TestFailureReportAndAdminFix(t *testing.T) {
	inst := Fake{Fail: map[string]bool{"pillow": true}}
	svc, st := newService(t, inst, "pillow")
	svc.Install("ann", "pillow", "lib", false)
	svc.Wait()
	a := svc.Apps()[0]
	if a.State != StateFailed || !strings.Contains(a.Err, "Resolving the dependencies") || a.ID != "" {
		t.Fatalf("%+v", a)
	}
	if err := svc.Install("ann", "pillow", "lib", false); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("a retry within a minute: %v", err)
	}
	if err := svc.Report("ann", "pillow", "I need it"); err != nil {
		t.Fatal(err)
	}
	svc.Report("bob", "pillow", "")
	reps := svc.Reports()
	if len(reps) != 1 || len(reps[0].Reporters) != 2 || reps[0].Note != "I need it" || reps[0].Status != "open" {
		t.Fatalf("%+v", reps)
	}
	if err := svc.Report("ann", "nothing", ""); err == nil {
		t.Fatal("only a failed app can be reported")
	}
	// the admin fixes the installer, retries, and everyone sees Installed; the report is closed
	svc.cfg.Installer = Fake{}
	if err := svc.ResolveReport("root", "pillow", "retry", ""); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if a := svc.Apps()[0]; a.State != StateInstalled || !st.HasBundle(a.ID) {
		t.Fatalf("%+v", a)
	}
	if reps := svc.Reports(); reps[0].Status != "fixed" {
		t.Fatalf("%+v", reps)
	}
}

func TestSwitchAndRemove(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask", "numpy")
	svc.Install("ann", "flask", "lib", false)
	svc.Install("ann", "numpy", "lib", false)
	svc.Wait()
	if err := svc.Switch("ann", "flask-1.0.0", true); err != nil {
		t.Fatal(err)
	}
	svc.Switch("ann", "numpy-1.0.0", true)
	svc.Switch("bob", "flask-1.0.0", true)
	svc.Switch("ann", "numpy-1.0.0", false)
	if got, _ := st.Manifest("ann"); !reflect.DeepEqual(got, []string{"flask-1.0.0"}) {
		t.Fatalf("ann: %v", got)
	}
	if err := svc.Switch("ann", "nothing-9", true); !errors.Is(err, ErrUnknown) {
		t.Fatalf("an app that is not installed: %v", err)
	}
	if err := svc.Switch("../ann", "flask-1.0.0", true); err == nil {
		t.Fatal("a bad home")
	}
	views := svc.Views("ann", "ann")
	on := map[string]bool{}
	for _, v := range views {
		on[v.Name] = v.On
	}
	if !on["flask"] || on["numpy"] {
		t.Fatalf("views %+v", views)
	}
	n, err := svc.Remove("root", "flask")
	if err != nil || n != 2 {
		t.Fatalf("remove: %d %v", n, err)
	}
	if got, _ := st.Manifest("ann"); len(got) != 0 {
		t.Fatalf("ann after: %v", got)
	}
	if st.HasBundle("flask-1.0.0") || len(svc.Apps()) != 1 {
		t.Fatal("the bundle and the record must be gone")
	}
}

func TestStoreFull(t *testing.T) {
	svc, _ := newService(t, Fake{}, "flask", "numpy")
	svc.cfg.LimitMB = func() int { return 1 }
	svc.Install("ann", "flask", "lib", false)
	svc.Wait()
	os.WriteFile(filepath.Join(svc.cfg.Store.Dir, "big"), make([]byte, 2<<20), 0644)
	if err := svc.Install("ann", "numpy", "lib", false); !errors.Is(err, ErrFull) {
		t.Fatalf("%v", err)
	}
}

func TestApplyReplaysOnAnotherNode(t *testing.T) {
	a, _ := newService(t, Fake{}, "flask", "numpy")
	a.Install("ann", "flask", "lib", false)
	a.Install("ann", "numpy", "lib", false)
	a.Wait()
	a.Switch("ann", "flask-1.0.0", true)
	a.Switch("bob", "numpy-1.0.0", true)
	snap := a.Snapshot()
	if len(snap.Apps) != 2 || !snap.Full || len(snap.Users) != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
	// a second node starts empty and a worker joins: it gets the same bundles and the same lists
	b, bst := newService(t, Fake{})
	state := b.Apply(context.Background(), snap)
	if len(state.Failed) != 0 || !reflect.DeepEqual(state.Have, []string{"flask-1.0.0", "numpy-1.0.0"}) {
		t.Fatalf("state %+v", state)
	}
	if got, _ := bst.Manifest("ann"); !reflect.DeepEqual(got, []string{"flask-1.0.0"}) {
		t.Fatalf("ann on b: %v", got)
	}
	// the app is removed on the first node: the next apply removes it on the second, and empties the list
	a.Remove("root", "flask")
	b.Apply(context.Background(), a.Snapshot())
	if bst.HasBundle("flask-1.0.0") {
		t.Fatal("removed on the first node, still here")
	}
	if got, _ := bst.Manifest("ann"); len(got) != 0 {
		t.Fatalf("ann on b after: %v", got)
	}
	// a failing install on a node is reported and does not stop the others
	c, _ := newService(t, Fake{Fail: map[string]bool{"numpy": true}})
	st2 := c.Apply(context.Background(), a.Snapshot())
	if len(st2.Failed) != 1 || st2.Failed["numpy-1.0.0"] == "" {
		t.Fatalf("%+v", st2)
	}
}

func TestRecoverMarksInterruptedInstall(t *testing.T) {
	svc, _ := newService(t, Fake{}, "flask")
	svc.put("app:flask", App{Name: "flask", State: StateInstalling, At: time.Now().Unix()})
	svc.Recover()
	if a := svc.Apps()[0]; a.State != StateFailed || !strings.Contains(a.Err, "stopped") {
		t.Fatalf("%+v", a)
	}
}

func TestAptList(t *testing.T) {
	svc, _ := newService(t, Fake{})
	if err := svc.SetApt([]string{"postgresql-client", "jq", "jq"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Apt(); !reflect.DeepEqual(got, []string{"postgresql-client", "jq"}) {
		t.Fatalf("%v", got)
	}
	if err := svc.SetApt([]string{"ok", "bad name"}); err == nil {
		t.Fatal("a bad name is refused")
	}
}

func TestCleanLog(t *testing.T) {
	got := CleanLog("error in /var/store/apps/x-1/lib\n"+strings.Repeat("line\n", 600), "/var/store")
	if strings.Contains(got, "/var/store") || len(got) > 1600 {
		t.Fatalf("%d %q", len(got), got[:60])
	}
}

type fakeApt struct {
	have map[string]bool
	fail map[string]bool
	ran  []string
}

func (f *fakeApt) Installed(p string) bool { return f.have[p] }
func (f *fakeApt) Install(ctx context.Context, p string) error {
	f.ran = append(f.ran, p)
	if f.fail[p] {
		return errors.New("no such package")
	}
	f.have[p] = true
	return nil
}

func TestApplyInstallsTheApprovedAptPackagesOnce(t *testing.T) {
	svc, _ := newService(t, Fake{})
	apt := &fakeApt{have: map[string]bool{"jq": true}, fail: map[string]bool{"nonexistent-pkg": true}}
	svc.cfg.Apt = apt
	snap := Snapshot{Full: true, Apt: []string{"jq", "postgresql-client", "nonexistent-pkg", "bad name"}}
	st := svc.Apply(context.Background(), snap)
	if !reflect.DeepEqual(apt.ran, []string{"postgresql-client", "nonexistent-pkg"}) {
		t.Fatalf("ran %v: what is there is left alone, and a bad name never reaches apt", apt.ran)
	}
	if len(st.Failed) != 1 || st.Failed["apt:nonexistent-pkg"] == "" {
		t.Fatalf("%+v", st.Failed)
	}
	apt.ran = nil
	svc.Apply(context.Background(), Snapshot{Full: true, Apt: []string{"jq", "postgresql-client"}})
	if len(apt.ran) != 0 {
		t.Fatalf("a package that is installed is not installed again: %v", apt.ran)
	}
	// a server with no runner does nothing
	svc.cfg.Apt = nil
	svc.Apply(context.Background(), snap)
}

func TestStepsAndTheCheckOfABundle(t *testing.T) {
	svc, _ := newService(t, Fake{Delay: 300 * time.Millisecond}, "flask")
	if err := svc.Install("ann", "flask", "lib", false); err != nil {
		t.Fatal(err)
	}
	var seen bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !seen {
		for _, v := range svc.Views("ann", "ann") {
			if v.Name == "flask" && v.State == StateInstalling && len(v.Steps) == len(installSteps) && v.Steps[1].State == "done" && v.Steps[2].State == "current" {
				seen = true
				if v.Detail == "" || v.Pct <= 5 {
					t.Errorf("the step list says nothing of the solve: %+v", v)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !seen {
		t.Error("the job never showed the step 'Downloading and linking' as the current one")
	}
	svc.Wait()
	// a bundle that cannot start its Python is refused, one that can is accepted
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bin"), 0755)
	os.WriteFile(filepath.Join(dir, "bin", "python"), []byte("#!/bin/sh\nexit 0\n"), 0755)
	if err := checkBundle(context.Background(), dir, "x", KindLib, "1.0.0"); err != nil {
		t.Errorf("a working python: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "bin", "python"), []byte("#!/bin/sh\necho 'ImportError: no module named x' >&2\nexit 1\n"), 0755)
	if err := checkBundle(context.Background(), dir, "x", KindLib, "1.0.0"); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Errorf("a python that fails must refuse the bundle: %v", err)
	}
	if err := checkBundle(context.Background(), t.TempDir(), "x", KindLib, "1.0.0"); err != nil {
		t.Errorf("no python is nothing to start: %v", err)
	}
	mod := t.TempDir()
	os.MkdirAll(filepath.Join(mod, "lib", "python3.10", "site-packages", "scikit_learn"), 0755)
	if moduleOf(mod, "scikit-learn") != "scikit_learn" || moduleOf(mod, "numpy") != "" || moduleOf(mod, "a b") != "" {
		t.Error("moduleOf")
	}
}

func TestAnAdminCanPickAVersion(t *testing.T) {
	svc, _ := newService(t, Fake{})
	if err := svc.InstallVersion("root", "flask", "lib", "2.3.0"); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if a := svc.Apps()[0]; a.State != StateInstalled || a.Version != "2.3.0" || a.ID != "flask-2.3.0" {
		t.Fatalf("%+v", a)
	}
	if err := svc.InstallVersion("root", "x", "lib", "../1"); err == nil {
		t.Error("a bad version")
	}
}

func TestUsersAreToldTheAnswers(t *testing.T) {
	svc, _ := newService(t, Fake{Fail: map[string]bool{"pillow": true}}, "pillow")
	// a request that is approved
	svc.Request("ann", "numpy", "lib", "")
	svc.Request("bob", "numpy", "lib", "")
	svc.Approve("root", "numpy")
	svc.Wait()
	ann := svc.Notices("ann")
	if len(ann) != 1 || ann[0].Status != "approved" || ann[0].Name != "numpy" {
		t.Fatalf("%+v", ann)
	}
	svc.Ack("ann", "numpy")
	if len(svc.Notices("ann")) != 0 || len(svc.Notices("bob")) != 1 {
		t.Fatal("each user is told once")
	}
	svc.Ack("bob", "numpy")
	if len(svc.Requests()) != 0 {
		t.Fatalf("an approved request is forgotten when everyone has seen it: %+v", svc.Requests())
	}
	// a request that is rejected stays, with the reason
	svc.Request("ann", "ffmpeg", "tool", "")
	svc.Reject("root", "ffmpeg", "too big")
	n := svc.Notices("ann")
	if len(n) != 1 || n[0].Status != "rejected" || !strings.Contains(n[0].Text, "too big") {
		t.Fatalf("%+v", n)
	}
	svc.Ack("ann", "ffmpeg")
	if len(svc.Notices("ann")) != 0 || len(svc.Requests()) != 1 {
		t.Fatal("a rejected request stays so that the card can say so")
	}
	// a report that is fixed
	svc.Install("ann", "pillow", "lib", false)
	svc.Wait()
	svc.Report("ann", "pillow", "")
	svc.cfg.Installer = Fake{}
	svc.ResolveReport("root", "pillow", "retry", "")
	svc.Wait()
	n = svc.Notices("ann")
	if len(n) != 1 || n[0].Kind != "report" || n[0].Status != "fixed" {
		t.Fatalf("%+v", n)
	}
	if len(svc.Notices("bob")) != 0 {
		t.Error("only the reporters are told")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestTheKindsAndWhoMayInstallThem(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask", "tool:jq", "npm:express")
	// a Python library, a tool and a Node library on the safe list: a user installs them
	for _, c := range []struct{ name, kind string }{{"flask", KindLib}, {"jq", KindTool}, {"express", KindNode}} {
		if err := svc.Install("ann", c.name, c.kind, false); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	svc.Wait()
	// the same name asked for as another kind is not on the list for that kind
	if err := svc.Install("ann", "jq", KindNode, false); err != ErrNeedsApproval {
		t.Errorf("jq is a tool on the list, not a Node library: %v", err)
	}
	if err := svc.Install("ann", "express", KindLib, false); err != ErrNeedsApproval {
		t.Errorf("express is a Node library on the list, not a Python one: %v", err)
	}
	// a language and a plugin are always an admin's
	if err := svc.Install("ann", "lua", KindLang, false); err != ErrNeedsApproval {
		t.Errorf("a language: %v", err)
	}
	if err := svc.Install("ann", "wordcount", KindPlugin, false); err != ErrNeedsApproval {
		t.Errorf("a plugin: %v", err)
	}
	if err := svc.Install("root", "lua", KindLang, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Install("root", "wordcount", KindPlugin, true); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	by := map[string]App{}
	for _, a := range svc.Apps() {
		by[a.Name] = a
	}
	for _, n := range []string{"flask", "jq", "express", "lua", "wordcount"} {
		if by[n].State != StateInstalled {
			t.Fatalf("%s: %+v", n, by[n])
		}
	}
	// the bundles put the right things on a terminal's environment
	v := VarsFor(st.Dir, []string{by["express"].ID, by["jq"].ID, by["lua"].ID})
	if len(v.NodePath) != 1 || !strings.HasSuffix(v.NodePath[0], "express-1.0.0/node_modules") {
		t.Errorf("NODE_PATH %v", v.NodePath)
	}
	var bins []string
	for _, b := range v.PathAdd {
		bins = append(bins, strings.TrimPrefix(b, st.Dir))
	}
	if !reflect.DeepEqual(bins, []string{"/apps/express-1.0.0/node_modules/.bin", "/apps/jq-1.0.0/bin", "/apps/lua-1.0.0/bin"}) {
		t.Errorf("PATH %v", bins)
	}
	// a plugin's script can be read; a plugin that is not installed has none
	if code, err := st.PluginCode(by["wordcount"].ID); err != nil || !strings.Contains(code, "openreplPlugin") {
		t.Errorf("the script: %v", err)
	}
	if _, err := st.PluginCode("clock-1.0.0"); err == nil {
		t.Error("a plugin that is not installed has no script")
	}
	// the name of an installed app cannot be taken by another kind
	if err := svc.Install("root", "flask", KindNode, true); err == nil || !strings.Contains(err.Error(), "installed already") {
		t.Errorf("one name, two kinds: %v", err)
	}
}

func TestTheViewsListTheSamplesOfEveryKind(t *testing.T) {
	svc, _ := newService(t, Fake{}, "flask", "tool:jq", "npm:express")
	got := map[string]string{}
	for _, v := range svc.Views("ann", "ann") {
		got[v.Name] = v.Kind + "/" + v.State
	}
	for name, want := range map[string]string{"flask": "lib/available", "jq": "tool/available", "express": "node/available", "lua": "lang/available", "ruby": "lang/available", "wordcount": "plugin/available", "clock": "plugin/available"} {
		if got[name] != want {
			t.Errorf("%s: %q, want %q", name, got[name], want)
		}
	}
	for _, v := range svc.Views("ann", "ann") {
		if (v.Kind == KindLang || v.Kind == KindPlugin) && (v.Safe || v.Summary == "") {
			t.Errorf("%+v: a sample of an admin's kind is not safe and says what it is", v)
		}
	}
}

func TestSearchSaysWhatTheStoreKnowsOfEachHit(t *testing.T) {
	svc, _ := newService(t, Fake{}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	hits, err := svc.Search(context.Background(), "flask", "")
	if err != nil || len(hits) != 2 {
		t.Fatalf("%v %+v", err, hits)
	}
	if hits[0].Name != "flask" || hits[0].State != StateInstalled || !hits[0].Safe || hits[1].Name != "flask-cors" || hits[1].State != "available" || hits[1].Safe {
		t.Errorf("%+v", hits)
	}
	if h, _ := svc.Search(context.Background(), "expr", KindNode); len(h) != 1 || h[0].Kind != KindNode {
		t.Errorf("a Node search: %+v", h)
	}
	if h, _ := svc.Search(context.Background(), "word", KindPlugin); len(h) != 1 || h[0].Name != "wordcount" {
		t.Errorf("a plugin search: %+v", h)
	}
	if _, err := (Missing{Reason: "no pixi"}).Search(context.Background(), "x", ""); err == nil {
		t.Error("a server without pixi says so")
	}
}

func TestNpmViewIsRead(t *testing.T) {
	r, err := npmViewFrom([]byte(`{"version":"4.19.2","dist.unpackedSize":2097152}`))
	if err != nil || r.Version != "4.19.2" || r.DiskMB != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = npmViewFrom([]byte(`[{"version":"4.0.0","dist.unpackedSize":1048576},{"version":"4.1.0","dist.unpackedSize":3145728}]`))
	if err != nil || r.Version != "4.1.0" || r.DiskMB != 3 {
		t.Fatalf("several versions: the last is the newest: %+v %v", r, err)
	}
	if _, err := npmViewFrom([]byte(`{"version":"../x"}`)); err == nil {
		t.Error("a bad version")
	}
	if _, err := npmViewFrom([]byte("")); err == nil {
		t.Error("an empty answer")
	}
}

func TestEstimateSaysWhatAnInstallWouldCostAndKeepsTheAnswer(t *testing.T) {
	svc, _ := newService(t, Fake{Fail: map[string]bool{"broken": true}})
	r, err := svc.Estimate(context.Background(), "flask", KindLib)
	if err != nil || r.Version != "1.0.0" || r.DownloadMB != 1.5 || r.Packages != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := svc.Estimate(context.Background(), "broken", KindLib); err == nil {
		t.Error("an app that cannot be solved says so before it is installed")
	}
	if _, err := svc.Estimate(context.Background(), "../x", KindLib); err == nil {
		t.Error("a bad name")
	}
}

func TestReinstallMakesABundleAgainOnTheNode(t *testing.T) {
	a, _ := newService(t, Fake{}, "flask")
	a.Install("ann", "flask", KindLib, false)
	a.Wait()
	snap := a.Snapshot()
	b, bst := newService(t, Fake{})
	b.Apply(context.Background(), snap)
	os.WriteFile(filepath.Join(bst.BundlePath("flask-1.0.0"), "junk"), []byte("damaged"), 0644)
	snap.Reinstall = []string{"flask-1.0.0", "../x"}
	st := b.Apply(context.Background(), snap)
	if _, err := os.Stat(filepath.Join(bst.BundlePath("flask-1.0.0"), "junk")); err == nil {
		t.Error("the bundle was not made again")
	}
	if len(st.Failed) != 0 || !bst.HasBundle("flask-1.0.0") || st.Sizes["flask-1.0.0"] <= 0 {
		t.Errorf("%+v", st)
	}
}

func TestAUserMayUninstallAnAppOnlyWhenNobodyElseHasItOn(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask", "numpy")
	svc.Install("ann", "flask", KindLib, false)
	svc.Install("ann", "numpy", KindLib, false)
	svc.Wait()
	svc.Switch("ann", "flask-1.0.0", true)
	svc.Switch("bob", "flask-1.0.0", true)
	// bob has it on too: ann cannot uninstall it, and is told so without a count (how many others use it is not hers to know)
	err := svc.Uninstall("ann", "ann", "flask")
	if !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "other users have it on") || strings.ContainsAny(err.Error(), "0123456789") || !st.HasBundle("flask-1.0.0") {
		t.Fatalf("%v", err)
	}
	var v View
	for _, x := range svc.Views("ann", "ann") {
		if x.Name == "flask" {
			v = x
		}
	}
	if v.OthersOn != 1 || !v.On {
		t.Errorf("the view says how many others use it: %+v", v)
	}
	// when bob turns it off, ann may: her own switch does not count, and it goes with the app
	svc.Switch("bob", "flask-1.0.0", false)
	if err := svc.Uninstall("ann", "ann", "flask"); err != nil {
		t.Fatal(err)
	}
	if st.HasBundle("flask-1.0.0") || len(svc.MyList("ann")) != 0 {
		t.Error("the bundle and ann's switch must be gone")
	}
	// an app nobody has on can be uninstalled by anyone signed in
	if err := svc.Uninstall("bob", "bob", "numpy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Uninstall("bob", "bob", "numpy"); !errors.Is(err, ErrUnknown) {
		t.Errorf("an app that is not installed: %v", err)
	}
	// an admin removes an app that others have on
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	svc.Switch("bob", "flask-1.0.0", true)
	if n, err := svc.Remove("root", "flask"); err != nil || n != 1 {
		t.Errorf("an admin removes it anyway: %d %v", n, err)
	}
}

func TestNodeRangesAreReadForTheNodeOfTheServer(t *testing.T) {
	for _, c := range []struct {
		rng, ver string
		want     bool
	}{
		{"", "12.22.9", true}, {"*", "12.22.9", true}, {">=12", "12.22.9", true}, {">=18", "12.22.9", false}, {">= 18.0.0", "12.22.9", false},
		{">=0.10.0 <13", "12.22.9", true}, {">=14", "12.22.9", false}, {"^12.0.0", "12.22.9", true}, {"^14.17.0 || >=16", "12.22.9", false},
		{"^12.17.0 || >=14", "12.22.9", true}, {"12.x", "12.22.9", true}, {"13.x", "12.22.9", false}, {"~12.22", "12.22.9", true}, {"~12.21", "12.22.9", false},
		{"12 - 14", "12.22.9", true}, {"14 - 16", "12.22.9", false}, {">=6", "v12.22.9", true}, {"<12", "12.22.9", false}, {"nonsense?", "12.22.9", true},
		{">=18", "bad", true},
	} {
		if got := NodeSatisfies(c.rng, c.ver); got != c.want {
			t.Errorf("%q on %q: %v, want %v", c.rng, c.ver, got, c.want)
		}
	}
}

func TestNpmCandidatesAreTheNewestFirstWithoutPrereleases(t *testing.T) {
	got := npmCandidates([]byte(`["0.19.0","0.18.1","1.0.0","1.10.0","1.9.0","2.0.0-beta.1","bad version","1.2.0+build"]`))
	if !reflect.DeepEqual(got, []string{"1.10.0", "1.9.0", "1.0.0", "0.19.0", "0.18.1"}) {
		t.Errorf("%v", got)
	}
	if got := npmCandidates([]byte(`"3.0.0"`)); !reflect.DeepEqual(got, []string{"3.0.0"}) {
		t.Errorf("one version: %v", got)
	}
	if npmCandidates([]byte("nonsense")) != nil {
		t.Error("an answer that is not a list")
	}
}

func TestNpmEnginesAreRead(t *testing.T) {
	if rng, mb := npmEngines([]byte(`{"engines.node":">= 0.10.0","dist.unpackedSize":2097152}`)); rng != ">= 0.10.0" || mb != 2 {
		t.Errorf("%q %v", rng, mb)
	}
	for _, odd := range []string{`{"engines.node":[">=4","<9"]}`, `{"engines.node":{"a":1}}`, ``, `{}`, `not json`} {
		if rng, _ := npmEngines([]byte(odd)); rng != "" {
			t.Errorf("%q must be any Node: %q", odd, rng)
		}
	}
	// the Node of the image (12) with a library whose latest wants 18
	cands := npmCandidates([]byte(`["4.21.2","5.3.0","4.17.1"]`))
	var pick string
	for _, v := range cands {
		rng := map[string]string{"5.3.0": ">= 18", "4.21.2": ">= 0.10.0", "4.17.1": ">= 0.10.0"}[v]
		if NodeSatisfies(rng, "v12.22.9") {
			pick = v
			break
		}
	}
	if pick != "4.21.2" {
		t.Errorf("the newest that fits: %q", pick)
	}
}

// countingInstaller counts the installs it is asked for.
type countingInstaller struct {
	Fake
	n *int
}

func (c countingInstaller) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	*c.n++
	return c.Fake.Install(ctx, name, version, kind, dest, log)
}

// A failed install that may pass is tried again after 5, 15, 30 and 60 minutes, not at every apply, and then given up; a command from the admin and a
// catalog without the app end the waiting.
func TestAFailedInstallWaitsLongerAndLongerBeforeItIsTriedAgain(t *testing.T) {
	a, _ := newService(t, Fake{}, "flask")
	a.Install("ann", "flask", "lib", false)
	a.Wait()
	snap := a.Snapshot()
	tries := 0
	b, bst := newService(t, countingInstaller{Fake: Fake{Fail: map[string]bool{"flask": true}}, n: &tries})
	now := time.Unix(1800000000, 0)
	b.cfg.Now = func() time.Time { return now }
	ctx := context.Background()
	want := []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}
	for i, w := range want {
		st := b.Apply(ctx, snap)
		if st.Failed["flask-1.0.0"] == "" || st.Retry["flask-1.0.0"] != now.Add(w).Unix() {
			t.Fatalf("try %d: failed %q, next %v (want in %s)", i+1, st.Failed, st.Retry, w)
		}
		// asked again before its time: no new try, and the same answer
		before := tries
		now = now.Add(w - time.Second)
		if st2 := b.Apply(ctx, snap); st2.Retry["flask-1.0.0"] != st.Retry["flask-1.0.0"] || tries != before {
			t.Fatalf("try %d: it was tried again before its time", i+1)
		}
		now = now.Add(time.Second)
	}
	if next, ok := b.NextRetry(); !ok || next.Before(now) {
		t.Errorf("the next retry: %v %v", next, ok)
	}
	// the fifth failure is the last: it is given up, and asking again makes no new try
	if st := b.Apply(ctx, snap); st.Failed["flask-1.0.0"] == "" || !st.GaveUp["flask-1.0.0"] || st.Retry["flask-1.0.0"] != 0 {
		t.Fatalf("it should be given up: %+v", st)
	}
	before := tries
	now = now.Add(24 * time.Hour)
	if st := b.Apply(ctx, snap); !st.GaveUp["flask-1.0.0"] || tries != before {
		t.Fatalf("a bundle that was given up was tried again")
	}
	// the admin's command ends the waiting, and a success ends the story
	b.cfg.Installer = Fake{}
	snap.Reinstall = []string{"flask-1.0.0"}
	if st := b.Apply(ctx, snap); len(st.Failed) != 0 || !bst.HasBundle("flask-1.0.0") {
		t.Fatalf("after the command: %+v", st)
	}
	if _, ok := b.NextRetry(); ok {
		t.Error("nothing should be waiting any more")
	}
}

// An app an admin turned on for everyone is on for every signed-in user, the ones who sign in later too (not for guests), until a user
// turns it off for themselves; it cannot be uninstalled by a user; and a worker that applies the catalog gets the same.
func TestAnAppOnForEveryoneIsOnForEverySignedInUserUntilTheyTurnItOff(t *testing.T) {
	a, ast := newService(t, Fake{}, "flask", "numpy")
	a.Install("ann", "flask", "lib", false)
	a.Install("ann", "numpy", "lib", false)
	// turned on for everyone while it is being installed: it takes effect when the install ends
	if err := a.SetDefault("root", "flask", true); err != nil {
		t.Fatal(err)
	}
	a.Wait()
	const id = "flask-1.0.0"
	for _, home := range []string{"ann", "zed"} { // zed has never switched anything
		if !has(a.MyList(home), id) {
			t.Errorf("%s: flask should be on: %v", home, a.MyList(home))
		}
		if eff, err := ast.Effective(home); err != nil || !has(eff, id) {
			t.Errorf("%s: what the terminal reads: %v %v", home, eff, err)
		}
	}
	if has(a.MyList("guest-7"), id) {
		t.Error("a guest must not get it")
	}
	if eff, _ := ast.Effective("guest-7"); has(eff, id) {
		t.Error("a guest's terminal must not get it")
	}
	if a.Uninstall("ann", "ann", "flask") == nil {
		t.Error("a user must not uninstall what is on for everyone")
	}
	// a user turns it off for themselves; the others keep it; numpy (not for everyone) is the user's own business
	if err := a.Switch("ann", id, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Switch("ann", "numpy-1.0.0", true); err != nil {
		t.Fatal(err)
	}
	if has(a.MyList("ann"), id) || !has(a.MyList("ann"), "numpy-1.0.0") || !has(a.MyList("zed"), id) {
		t.Fatalf("ann %v zed %v", a.MyList("ann"), a.MyList("zed"))
	}
	if eff, _ := ast.Effective("ann"); has(eff, id) {
		t.Errorf("ann's terminal still gets it: %v", eff)
	}
	if env, _ := os.ReadFile(filepath.Join(ast.HomeDir("ann"), "env.sh")); strings.Contains(string(env), "flask-1.0.0") {
		t.Error("ann's env.sh still has flask")
	}
	// a worker gets the same from the catalog
	snap := a.Snapshot()
	if !has(snap.Defaults, id) || !has(snap.Off["ann"], id) {
		t.Fatalf("the catalog: defaults %v off %v", snap.Defaults, snap.Off)
	}
	b, bst := newService(t, Fake{})
	if st := b.Apply(context.Background(), snap); len(st.Failed) != 0 {
		t.Fatalf("%+v", st)
	}
	if eff, _ := bst.Effective("ann"); has(eff, id) || !has(eff, "numpy-1.0.0") {
		t.Errorf("the worker, ann: %v", eff)
	}
	if eff, _ := bst.Effective("zed"); !has(eff, id) {
		t.Errorf("the worker, a user who never switched anything: %v", eff)
	}
	// she turns it on again; then an admin stops it being on for everyone: it stays on for her, off for the rest
	if err := a.Switch("ann", id, true); err != nil || !has(a.MyList("ann"), id) {
		t.Fatalf("on again: %v %v", err, a.MyList("ann"))
	}
	a.SetDefault("root", "flask", false)
	if has(a.MyList("zed"), id) {
		t.Error("it should not be on for zed any more")
	}
	// removing an app that is on for everyone clears it everywhere
	a.SetDefault("root", "numpy", true)
	if _, err := a.Remove("root", "numpy"); err != nil {
		t.Fatal(err)
	}
	if len(a.Defaults()) != 0 {
		t.Errorf("defaults after the removal: %v", a.Defaults())
	}
	if d, _ := ast.Defaults(); len(d) != 0 {
		t.Errorf("defaults.json after the removal: %v", d)
	}
}

// Switching an app on is checked first: the app's own verification runs with the user's environment, and what a failure says is kept.
func TestTheVerificationOfAnAppRunsWithTheUsersEnvironmentAndSaysWhyItFailed(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask", "tool:zzcli", "npm:express")
	for _, c := range []struct{ name, kind string }{{"flask", KindLib}, {"zzcli", KindTool}, {"express", KindNode}} {
		if err := svc.Install("ann", c.name, c.kind, false); err != nil {
			t.Fatal(err)
		}
	}
	svc.Install("root", "wordcount", KindPlugin, true)
	svc.Wait()
	ctx := context.Background()
	for _, c := range []struct{ id, name, kind string }{{"flask-1.0.0", "flask", KindLib}, {"zzcli-1.0.0", "zzcli", KindTool}, {"express-1.0.0", "express", KindNode}, {"wordcount-1.0.0", "wordcount", KindPlugin}} {
		if err := RunVerify(ctx, st, "ann", c.id, c.name, c.kind, "", nil); err != nil {
			t.Errorf("%s should work: %v", c.name, err)
		}
	}
	// a tool that does not start, and a library that cannot be imported, say why
	os.WriteFile(filepath.Join(st.BundlePath("zzcli-1.0.0"), "bin", "zzcli"), []byte("#!/bin/sh\necho 'zzcli: error while loading libonig.so.5'\nexit 3\n"), 0755)
	err := RunVerify(ctx, st, "ann", "zzcli-1.0.0", "zzcli", KindTool, "", nil)
	if err == nil || !strings.Contains(err.Error(), "libonig") {
		t.Errorf("a tool that does not start: %v", err)
	}
	os.WriteFile(filepath.Join(st.BundlePath("flask-1.0.0"), "lib", "python3.10", "site-packages", "flask", "__init__.py"), []byte("raise ImportError('boom: libffi')\n"), 0644)
	err = RunVerify(ctx, st, "ann", "flask-1.0.0", "flask", KindLib, "", nil)
	if err == nil || !strings.Contains(err.Error(), "boom: libffi") {
		t.Errorf("a library that cannot be imported: %v", err)
	}
	// not here yet is its own answer, a guest has no apps, and the node installs nothing it cannot start
	if err := RunVerify(ctx, st, "ann", "nothing-1.0", "nothing", KindLib, "", nil); !errors.Is(err, ErrNotHere) {
		t.Errorf("a bundle the node does not have: %v", err)
	}
	if err := RunVerify(ctx, st, "guest-3", "flask-1.0.0", "flask", KindLib, "", nil); err == nil {
		t.Error("a guest cannot have an app on")
	}
	bad := filepath.Join(t.TempDir(), "zzcli-2.0.0")
	os.MkdirAll(filepath.Join(bad, "bin"), 0755)
	os.WriteFile(filepath.Join(bad, "bin", "zzcli"), []byte("#!/bin/sh\nexit 1\n"), 0755)
	if err := checkBundle(ctx, bad, "zzcli", KindTool, "1.0.0"); err == nil {
		t.Error("a tool that does not start must not be offered as installed")
	}
}

// A node that installed a bundle in a replay proves it works before it says it has it.
func TestANodeProvesWhatItInstalledInAReplayBeforeItSaysItHasIt(t *testing.T) {
	a, _ := newService(t, Fake{}, "tool:jq")
	a.Install("ann", "jq", KindTool, false)
	a.Wait()
	snap := a.Snapshot()
	b, bst := newService(t, brokenTool{})
	st := b.Apply(context.Background(), snap)
	if st.Failed["jq-1.0.0"] == "" || bst.HasBundle("jq-1.0.0") {
		t.Fatalf("a bundle that does not start was taken: %+v", st)
	}
	if st.Retry["jq-1.0.0"] == 0 {
		t.Error("and it is tried again later")
	}
}

// brokenTool installs a tool whose program does not start.
type brokenTool struct{ Fake }

func (brokenTool) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	if err := os.MkdirAll(filepath.Join(dest, "bin"), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "bin", name), []byte("#!/bin/sh\necho 'cannot start'\nexit 9\n"), 0755)
}

// lockingInstaller installs a Python-library-like bundle with pixi's two files, and says whether the lock was in the folder before it ran.
type lockingInstaller struct {
	Fake
	sawLock *string
}

func (l lockingInstaller) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	os.MkdirAll(dest, 0755)
	if raw, err := os.ReadFile(filepath.Join(dest, "pixi.lock")); err == nil {
		*l.sawLock = string(raw)
	} else if err := os.WriteFile(filepath.Join(dest, "pixi.toml"), []byte("[workspace]\nname=\""+name+"\"\n"), 0644); err == nil {
		os.WriteFile(filepath.Join(dest, "pixi.lock"), []byte("version: 6\nlocked-by: gateway\n"), 0644)
	}
	return l.Fake.Install(ctx, name, version, kind, dest, log)
}

// The lock of an install is saved in the catalog; a node installs exactly it (and installs nothing when it cannot have it).
func TestTheLockOfAnInstallIsKeptAndEveryNodeInstallsFromIt(t *testing.T) {
	var sawA string
	a, _ := newService(t, lockingInstaller{sawLock: &sawA}, "flask")
	a.Install("ann", "flask", KindLib, false)
	a.Wait()
	app, _ := a.AppByID("flask-1.0.0")
	if !app.Locked || sawA != "" {
		t.Fatalf("the gateway solves and keeps the lock: locked %v, saw %q", app.Locked, sawA)
	}
	l, ok := a.LockOf("flask-1.0.0")
	if !ok || !strings.Contains(l.Lock, "locked-by: gateway") {
		t.Fatalf("the lock in the catalog: %+v %v", l, ok)
	}
	snap := a.Snapshot()
	// a worker gets the lock from the gateway and installs from it
	var sawB string
	b, bst := newService(t, lockingInstaller{sawLock: &sawB})
	b.SetLock(func(ctx context.Context, id string) (Lock, error) {
		l, ok := a.LockOf(id)
		if !ok {
			return Lock{}, errors.New("no lock")
		}
		return l, nil
	})
	if st := b.Apply(context.Background(), snap); len(st.Failed) != 0 || !bst.HasBundle("flask-1.0.0") {
		t.Fatalf("%+v", st)
	}
	if !strings.Contains(sawB, "locked-by: gateway") {
		t.Errorf("the worker did not install from the lock: %q", sawB)
	}
	// a worker that cannot get the lock installs nothing: it would not be the same bundle
	var sawC string
	c, cst := newService(t, lockingInstaller{sawLock: &sawC})
	c.SetLock(func(ctx context.Context, id string) (Lock, error) {
		return Lock{}, errors.New("the gateway is not reachable")
	})
	st := c.Apply(context.Background(), snap)
	if st.Failed["flask-1.0.0"] == "" || cst.HasBundle("flask-1.0.0") {
		t.Fatalf("a worker without the lock must not install: %+v", st)
	}
	// a gateway whose disk was wiped installs from the lock in its own database
	var sawD string
	d := New(Config{Store: Store{Dir: t.TempDir()}, KV: a.cfg.KV.(*lockedKV).inner, Installer: lockingInstaller{sawLock: &sawD}})
	if st := d.Apply(context.Background(), snap); len(st.Failed) != 0 || !strings.Contains(sawD, "locked-by: gateway") {
		t.Fatalf("a gateway after a wipe: %+v saw %q", st, sawD)
	}
	// removing the app removes its lock
	a.Remove("root", "flask")
	if _, ok := a.LockOf("flask-1.0.0"); ok {
		t.Error("the lock of a removed app must go")
	}
}

// ---- pixi: what is read from its files and its output ------------------------------------------------------------------------

func TestPixiLockIsReadForTheNodesCPUType(t *testing.T) {
	raw, err := os.ReadFile("testdata/pixi-numpy.lock") // a real lock of python 3.10 + numpy 2.2.6 for linux-64 and linux-aarch64
	if err != nil {
		t.Fatal(err)
	}
	amd, err := LockPackages(raw, "linux-64")
	if err != nil {
		t.Fatal(err)
	}
	arm, err := LockPackages(raw, "linux-aarch64")
	if err != nil {
		t.Fatal(err)
	}
	find := func(pk []LockedPackage, name string) LockedPackage {
		for _, p := range pk {
			if p.Name == name {
				return p
			}
		}
		return LockedPackage{}
	}
	for _, c := range []struct {
		pk   []LockedPackage
		cpu  string
		want string
	}{{amd, "linux-64", "linux-64/numpy-2.2.6"}, {arm, "linux-aarch64", "linux-aarch64/numpy-2.2.6"}} {
		n := find(c.pk, "numpy")
		if n.Version != "2.2.6" || !strings.Contains(n.URL, c.want) || n.Size < 1e5 {
			t.Errorf("%s numpy: %+v", c.cpu, n)
		}
		if py := find(c.pk, "python"); !strings.HasPrefix(py.Version, "3.10.") {
			t.Errorf("%s python: %+v", c.cpu, py)
		}
		for _, p := range c.pk {
			if !strings.Contains(p.URL, "/"+c.cpu+"/") && !strings.Contains(p.URL, "/noarch/") {
				t.Errorf("%s has a package of another CPU: %s", c.cpu, p.URL)
			}
			if p.Size == 0 || p.Name == "" || p.Version == "" || p.Build == "" {
				t.Errorf("an incomplete package: %+v", p)
			}
		}
	}
	if len(amd) < 20 || len(amd) != len(arm) {
		t.Errorf("packages: %d and %d", len(amd), len(arm))
	}
	// the version, the count and the size for the page
	r, err := resolvedFrom(raw, "numpy", "linux-aarch64")
	if err != nil || r.Version != "2.2.6" || r.Packages != len(arm) || r.DownloadMB < 20 || r.DiskMB < r.DownloadMB {
		t.Errorf("%+v %v", r, err)
	}
	if _, err := resolvedFrom(raw, "nothing", "linux-64"); err == nil {
		t.Error("a package the lock does not have")
	}
	if _, err := LockPackages(raw, "osx-arm64"); err == nil || !strings.Contains(err.Error(), "linux-64") {
		t.Errorf("a CPU type that is not in the lock: %v", err)
	}
}

func TestPixiManifestPinsTheAppAndThePythonOfALibrary(t *testing.T) {
	p := Pixi{Python: "3.10"}
	m, err := p.Manifest("numpy", "2.2.6", KindLib)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`platforms = ["linux-64", "linux-aarch64"]`, `python = "3.10.*"`, `"numpy" = "==2.2.6"`, `channels = ["conda-forge"]`} {
		if !strings.Contains(m, want) {
			t.Errorf("the manifest lacks %s:\n%s", want, m)
		}
	}
	if m, _ := p.Manifest("jq", "1.7", KindTool); strings.Contains(m, "python") {
		t.Errorf("a tool must not drag a Python in:\n%s", m)
	}
	if m, _ := p.Manifest("lua", "", KindLang); !strings.Contains(m, `"lua" = "*"`) {
		t.Errorf("the newest:\n%s", m)
	}
	for _, bad := range [][2]string{{"Numpy", "1.0"}, {"a b", "1.0"}, {"x\"y", "1.0"}, {"numpy", "1.0\"\n[x]"}} {
		if _, err := p.Manifest(bad[0], bad[1], KindLib); err == nil {
			t.Errorf("%q %q must be refused", bad[0], bad[1])
		}
	}
}

func TestPixiSearchIsRead(t *testing.T) {
	text := `Using channels: conda-forge
num-utils (1 version)
  0.5 pl5321hd8ed1ab_0 [noarch] conda-forge

num2words (5 versions)
  0.5.14 pyhe01879c_0 [noarch] conda-forge
  0.5.12 pyhd8ed1ab_1 [noarch] conda-forge

numpy (140 versions)
  2.2.6 py310h6e5608f_0 [linux-aarch64] conda-forge
  ... and 139 more versions (use -l to show more)

numactl-conda-aarch64 (5 versions)
  2.0.12 h4616a5c_1111 [noarch] conda-forge
`
	got := foundFromPixi(text, "numpy", "")
	if len(got) != 4 || got[0].Name != "numpy" || got[0].Version != "2.2.6" || got[0].Kind != KindLib {
		t.Fatalf("%+v", got)
	}
	if got[1].Name != "num-utils" || got[2].Name != "num2words" || got[2].Version != "0.5.14" {
		t.Errorf("order and the newest version: %+v", got)
	}
}

func TestTheActivationOfAPackageIsKeptAndFiltered(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root", "LANG=C"}
	hook := []byte(`{"environment_variables":{"PATH":"/b/.pixi/envs/default/bin:/usr/bin","JAVA_HOME":"/b/.pixi/envs/default/lib/jvm","PROJ_DATA":"/b/share/proj",
	"PROJ_NETWORK":"ON","CONDA_PREFIX":"/b/.pixi/envs/default","PIXI_EXE":"/x/pixi","LD_LIBRARY_PATH":"/evil","LD_PRELOAD":"/evil.so","HOME":"/tmp/x","LANG":"C",
	"JAVA_LD_LIBRARY_PATH":"/b/lib/jvm/lib/server","BASH_ENV":"/evil","BAD NAME":"x","XDG_DATA_DIRS":"/b/share:/usr/share","MULTI":"a\nb"}}`)
	got, err := activationFromHook(hook, base)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"JAVA_HOME": "/b/.pixi/envs/default/lib/jvm", "PROJ_DATA": "/b/share/proj", "PROJ_NETWORK": "ON", "JAVA_LD_LIBRARY_PATH": "/b/lib/jvm/lib/server"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	// saved in the bundle, read back, passed to a terminal and written in env.sh; the first bundle on the list wins a name
	dir := t.TempDir()
	a, b := filepath.Join(dir, "apps", "openjdk-17.0.1"), filepath.Join(dir, "apps", "proj-9.0.0")
	os.MkdirAll(a, 0755)
	os.MkdirAll(b, 0755)
	if err := WriteActivation(a, map[string]string{"JAVA_HOME": "/a/jvm", "SHARED": "from-a"}); err != nil {
		t.Fatal(err)
	}
	WriteActivation(b, map[string]string{"PROJ_DATA": "/b/proj", "SHARED": "from-b"})
	os.WriteFile(filepath.Join(a, activateFile), []byte(`{"env":{"JAVA_HOME":"/a/jvm","SHARED":"from-a","PATH":"/evil","LD_PRELOAD":"/x"}}`), 0644) // a file with what must not pass
	v := VarsFor(dir, []string{"openjdk-17.0.1", "proj-9.0.0"})
	if v.Extra["JAVA_HOME"] != "/a/jvm" || v.Extra["PROJ_DATA"] != "/b/proj" || v.Extra["SHARED"] != "from-a" || v.Extra["PATH"] != "" || v.Extra["LD_PRELOAD"] != "" {
		t.Errorf("%v", v.Extra)
	}
	env := v.Apply([]string{"PATH=/usr/bin", "JAVA_HOME=/old"})
	if !contains(env, "JAVA_HOME=/a/jvm") || !contains(env, "PROJ_DATA=/b/proj") {
		t.Errorf("%v", env)
	}
	if sh := EnvScript(dir, []string{"openjdk-17.0.1"}); !strings.Contains(sh, "export JAVA_HOME='/a/jvm'") || strings.Contains(sh, "LD_PRELOAD") {
		t.Errorf("env.sh:\n%s", sh)
	}
	// a bundle with no file, and a broken one, give nothing and stop nothing
	os.WriteFile(filepath.Join(b, activateFile), []byte("{not json"), 0644)
	if v := VarsFor(dir, []string{"proj-9.0.0"}); len(v.Extra) != 0 {
		t.Errorf("%v", v.Extra)
	}
}

// A bundle that pixi made keeps its prefix in .pixi/envs/default; one made before keeps it at its root; both are on the PATH of the user.
func TestTheEnvironmentFollowsThePrefixOfEachBundleLayout(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "apps", "numpy-2.2.6", ".pixi", "envs", "default")
	older := filepath.Join(dir, "apps", "flask-3.0.0")
	for _, p := range []string{filepath.Join(newer, "bin"), filepath.Join(newer, "lib", "python3.10", "site-packages"), filepath.Join(older, "bin"), filepath.Join(older, "lib", "python3.10", "site-packages")} {
		os.MkdirAll(p, 0755)
	}
	v := VarsFor(dir, []string{"numpy-2.2.6", "flask-3.0.0"})
	if len(v.PathAdd) != 2 || v.PathAdd[0] != filepath.Join(newer, "bin") || v.PathAdd[1] != filepath.Join(older, "bin") || len(v.PythonPath) != 2 {
		t.Errorf("%+v", v)
	}
	if PrefixOf(filepath.Join(dir, "apps", "numpy-2.2.6")) != newer || PrefixOf(older) != older {
		t.Error("PrefixOf")
	}
}

func TestPixiIsNeverGivenABadSearchPattern(t *testing.T) {
	if got, _ := (Pixi{Bin: "/x"}).Search(context.Background(), "../x", ""); got != nil {
		t.Error("a bad pattern is never sent to pixi")
	}
}

// pixi gets its home and its cache in the store and nothing that could change the result from node to node.
func TestPixiRunsWithItsOwnHomeAndCacheAndNoColourOrProgress(t *testing.T) {
	c := (Pixi{Bin: "/usr/bin/pixi", Dir: "/store"}).cmd(context.Background(), "/tmp", "lock")
	for _, want := range []string{"PIXI_HOME=/store/pixi", "PIXI_CACHE_DIR=/store/pkgs", "PIXI_NO_PROGRESS=true", "PIXI_COLOR=never"} {
		if !contains(c.Env, want) {
			t.Errorf("the environment lacks %s", want)
		}
	}
}

// An admin can ask for a fresh lock of an installed app: the same version, solved again; the people who have it on keep their switch.
func TestAnAdminCanMakeAFreshLockOfAnInstalledApp(t *testing.T) {
	var saw string
	svc, st := newService(t, lockingInstaller{sawLock: &saw}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	if err := svc.Switch("ann", "flask-1.0.0", true); err != nil {
		t.Fatal(err)
	}
	old, _ := svc.LockOf("flask-1.0.0")
	os.WriteFile(filepath.Join(st.BundlePath("flask-1.0.0"), "marker-of-the-old-install"), nil, 0644)
	id, err := svc.Relock("root", "flask")
	if err != nil || id != "flask-1.0.0" {
		t.Fatalf("%q %v", id, err)
	}
	if _, ok := svc.LockOf(id); ok {
		t.Error("the old lock must be gone while it is made again")
	}
	svc.Wait()
	l, ok := svc.LockOf(id)
	if !ok || l.Lock == "" || l.Lock != old.Lock { // the fake solves the same lock again; what matters is that it was made again
		t.Fatalf("the new lock: %v %+v", ok, l)
	}
	if saw != "" {
		t.Errorf("a fresh lock is solved, not taken from the old one: %q", saw)
	}
	if _, err := os.Stat(filepath.Join(st.BundlePath(id), "marker-of-the-old-install")); err == nil {
		t.Error("the bundle was made again")
	}
	if !has(svc.MyList("ann"), id) {
		t.Error("the switch of the user is kept")
	}
	if eff, _ := st.Manifest("ann"); !has(eff, id) {
		t.Errorf("the files of the user have it again: %v", eff)
	}
	if _, err := svc.Relock("root", "nothing"); err == nil {
		t.Error("an app that is not installed")
	}
}

// What the image has built in is found at the start, listed with its version, and a built-in name cannot be installed or asked for.
func TestBuiltInAppsAreListedAndTheirNamesCannotBeInstalled(t *testing.T) {
	got := DetectBuiltins(context.Background())
	byName := map[string]Builtin{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if p, ok := byName["Python"]; !ok || !strings.HasPrefix(p.Version, "3.") || len(p.Used) == 0 || p.Docs == "" {
		t.Errorf("python3 is on this machine: %+v", got)
	}
	svc, _ := newService(t, Fake{}, "flask")
	svc.SetBuiltins([]Builtin{{Name: "Ruby", Version: "3.0.2", Used: []string{"irb"}, Names: []string{"ruby", "irb"}}})
	if err := svc.Install("ann", "ruby", KindLang, true); !errors.Is(err, ErrBuiltin) {
		t.Errorf("a built-in name must not be installed, not even by an admin: %v", err)
	}
	if _, err := svc.Request("ann", "ruby", KindLang, "please"); !errors.Is(err, ErrBuiltin) {
		t.Errorf("or asked for: %v", err)
	}
	if err := svc.Install("ann", "flask", KindLib, false); err != nil {
		t.Errorf("an ordinary name: %v", err)
	}
	svc.Wait()
	hits, _ := svc.Search(context.Background(), "ru", "")
	for _, h := range hits {
		if h.Name == "ruby" && h.State != "builtin" {
			t.Errorf("a search shows a built-in name as built in: %+v", h)
		}
	}
	if b, ok := svc.BuiltinNamed("IRB"); !ok || b.Name != "Ruby" {
		t.Errorf("%+v %v", b, ok)
	}
}

// A failure that cannot pass (nothing to install for this CPU type) is not tried again by itself at all; the admin's Reinstall tries once more.
func TestAFailureThatCannotPassIsNotTriedAgain(t *testing.T) {
	a, _ := newService(t, Fake{}, "flask")
	a.Install("ann", "flask", "lib", false)
	a.Wait()
	snap := a.Snapshot()
	b, bst := newService(t, permFail{})
	ctx := context.Background()
	st := b.Apply(ctx, snap)
	if st.Failed["flask-1.0.0"] == "" || !st.GaveUp["flask-1.0.0"] || st.Retry["flask-1.0.0"] != 0 {
		t.Fatalf("a failure that cannot pass was kept for a retry: %+v", st)
	}
	if !strings.Contains(st.Failed["flask-1.0.0"], "Not available on this server") {
		t.Errorf("the admin should be told why: %q", st.Failed["flask-1.0.0"])
	}
	b.cfg.Installer = Fake{}
	snap.Reinstall = []string{"flask-1.0.0"}
	if st := b.Apply(ctx, snap); len(st.Failed) != 0 || !bst.HasBundle("flask-1.0.0") {
		t.Fatalf("after Reinstall: %+v", st)
	}
}

// permFail cannot install anything for its CPU type.
type permFail struct{ Fake }

func (permFail) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	return errors.New("Cannot solve the request because of: No candidates were found for " + name)
}

// A solve that has no answer for every CPU type is made for this CPU type only, and the app is marked partial; a worker solves for its own.
type cpuPixi struct {
	Fake
	failAll bool // it cannot solve unless the install is for one CPU type
	single  *int
}

func (c cpuPixi) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	if c.failAll && !singleCPU(ctx) {
		return Resolved{}, errors.New("conda-forge could not solve " + name + ": no candidates for linux-aarch64")
	}
	if singleCPU(ctx) {
		*c.single++
	}
	return c.Fake.Resolve(ctx, name, version, kind)
}

func TestWhatCannotBeSolvedForEveryCPUIsInstalledForThisOne(t *testing.T) {
	n := 0
	svc, st := newService(t, Multi{Conda: cpuPixi{failAll: true, single: &n}, Plugin: PluginInstaller{}}, "py27lib")
	if err := svc.Install("ann", "py27lib", KindLib, true); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	var app App
	for _, a := range svc.Apps() {
		if a.Name == "py27lib" {
			app = a
		}
	}
	if app.State != StateInstalled || !app.Partial || app.Locked || n == 0 || !st.HasBundle(app.ID) {
		t.Fatalf("the best version for this CPU type was not installed: %+v (single solves %d)", app, n)
	}
	// a worker of another CPU type installs its own best version of the same bundle id
	w, wst := newService(t, Multi{Conda: cpuPixi{failAll: true, single: &n}, Plugin: PluginInstaller{}})
	got := w.Apply(context.Background(), svc.Snapshot())
	if len(got.Failed) != 0 || !wst.HasBundle(app.ID) {
		t.Fatalf("the worker did not install its own: %+v", got.Failed)
	}
}

// The gateway tries a failure that may pass again by itself, a few times, and not one that cannot pass.
func TestTheGatewayTriesAgainByItselfOnlyWhatMayPass(t *testing.T) {
	old := Backoff
	Backoff = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	defer func() { Backoff = old }()
	tries := 0
	svc, _ := newService(t, netFail{n: &tries}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	time.Sleep(300 * time.Millisecond)
	svc.Wait()
	if tries != 3 { // the first try and two retries, then it is given up
		t.Fatalf("tries: %d", tries)
	}
	for _, a := range svc.Apps() {
		if a.Name == "flask" && (a.State != StateFailed || !strings.Contains(a.Err, "gave up")) {
			t.Fatalf("it should be given up: %+v", a)
		}
	}
	tries = 0
	svc2, _ := newService(t, permFail{}, "flask")
	svc2.Install("ann", "flask", KindLib, false)
	svc2.Wait()
	time.Sleep(100 * time.Millisecond)
	for _, a := range svc2.Apps() {
		if a.Name == "flask" && !strings.Contains(a.Err, "no automatic retry") {
			t.Fatalf("a failure that cannot pass: %+v", a)
		}
	}
}

// The classification of a failure.
func TestPermanentAndTransientFailures(t *testing.T) {
	for _, m := range []string{"Cannot solve the request because of: No candidates were found for git ==2.34", "conda-forge could not solve bat: x", "no recent version of jest works with this server's Node v12", "E: Version '2.34' for 'git' was not found"} {
		if !IsPermanent(errors.New(m)) {
			t.Errorf("%q should be permanent", m)
		}
	}
	for _, m := range []string{"Get https://x: connection reset by peer", "no space left on device", "context deadline exceeded", "the solver failed", "i/o timeout", "403 Forbidden"} {
		if IsPermanent(errors.New(m)) {
			t.Errorf("%q should be transient", m)
		}
	}
	if IsPermanent(errNoProgress) || IsPermanent(nil) {
		t.Error("a stopped install and no error are not permanent")
	}
}

// resolveCounter counts the solves it is asked for.
type resolveCounter struct {
	Fake
	n *int
}

func (c resolveCounter) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	*c.n++
	return c.Fake.Resolve(ctx, name, version, kind)
}

// netFail has no network: every solve fails in a way that may pass later.
type netFail struct {
	Fake
	n *int
}

func (c netFail) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	*c.n++
	return Resolved{}, errors.New("Get https://conda.anaconda.org: connection reset by peer")
}

// slowInstaller answers a solve slowly and an install not at all (until it is stopped).
type slowInstaller struct {
	Fake
	solve time.Duration
	hang  bool
}

func (s slowInstaller) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	select {
	case <-time.After(s.solve):
		return s.Fake.Resolve(ctx, name, version, kind)
	case <-ctx.Done():
		return Resolved{}, ctx.Err()
	}
}

func (s slowInstaller) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	if s.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Fake.Install(ctx, name, version, kind, dest, log)
}

// A solve that has not answered in SolveLimit is stopped, whatever the manager.
func TestASolveIsLimitedInTime(t *testing.T) {
	old := SolveLimit
	SolveLimit = 40 * time.Millisecond
	defer func() { SolveLimit = old }()
	start := time.Now()
	_, err := Multi{Conda: slowInstaller{solve: 5 * time.Second}}.Resolve(context.Background(), "flask", "", KindLib)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("the solve was not limited: %v after %s", err, time.Since(start))
	}
	if IsPermanent(err) {
		t.Error("a timeout may pass: it is not a permanent failure")
	}
}

// An install that shows no sign of life is stopped, and the lock is free for the next.
func TestAnInstallWithNoProgressIsStopped(t *testing.T) {
	oldN, oldT := NoProgress, watchTick
	NoProgress, watchTick = 60*time.Millisecond, 10*time.Millisecond
	defer func() { NoProgress, watchTick = oldN, oldT }()
	svc, _ := newService(t, slowInstaller{hang: true}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	for _, a := range svc.Apps() {
		if a.Name == "flask" && (a.State != StateFailed || !strings.Contains(a.Err, "no progress")) {
			t.Fatalf("a hung install was not stopped: %+v", a)
		}
	}
}

// An app that only waits for its turn is shown as waiting, not as installing.
func TestAnAppThatWaitsForItsTurnIsShownAsWaiting(t *testing.T) {
	svc, _ := newService(t, Fake{Delay: 300 * time.Millisecond}, "flask", "numpy")
	svc.Install("ann", "flask", KindLib, false)
	time.Sleep(60 * time.Millisecond) // flask has the lock
	svc.Install("ann", "numpy", KindLib, false)
	waiting := map[string]bool{}
	for _, a := range svc.AppsWithWaiting() {
		waiting[a.Name] = a.Waiting
	}
	if waiting["flask"] || !waiting["numpy"] {
		t.Fatalf("flask runs and numpy waits: %v", waiting)
	}
	for _, v := range svc.Views("ann", "ann") {
		if v.Name == "numpy" && (v.State != StateInstalling || !v.Waiting) {
			t.Fatalf("the view of numpy: %+v", v)
		}
	}
	svc.Wait()
}

// ladderFake has the versions 3.0.0, 2.0.0 and 1.0.0 of every app, answers a bound ("<2.0.0": the newest below it), and cannot install the versions
// that are in bad.
type ladderFake struct {
	Fake
	bad   map[string]bool
	asked *[]string
	vers  []string // the versions it has, newest first (default 3.0.0, 2.0.0, 1.0.0)
}

func (l ladderFake) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	if kind == KindPlugin {
		return l.Fake.Resolve(ctx, name, version, kind)
	}
	all := []string{"3.0.0", "2.0.0", "1.0.0"}
	if len(l.vers) > 0 {
		all = l.vers
	}
	if b, ok := upperBound(version); ok {
		all = olderThan(all, b)
	} else if version != "" {
		all = []string{version}
	}
	if len(all) == 0 {
		return Resolved{}, errors.New("no version below that")
	}
	return Resolved{Version: all[0], Packages: 1, DownloadMB: 1, DiskMB: 2}, nil
}

func (l ladderFake) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	if l.asked != nil {
		*l.asked = append(*l.asked, version)
	}
	if l.bad[version] {
		return errors.New(name + " did not start: cannot run " + version)
	}
	return l.Fake.Install(ctx, name, version, kind, dest, log)
}

// When the newest version does not install, the newest one below it is tried, so that the user gets the best version that works.
func TestTheInstallLaddersDownToAVersionThatWorks(t *testing.T) {
	var asked []string
	svc, st := newService(t, ladderFake{bad: map[string]bool{"3.0.0": true, "2.0.0": true}, asked: &asked}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	var app App
	for _, a := range svc.Apps() {
		if a.Name == "flask" {
			app = a
		}
	}
	if app.State != StateInstalled || app.Version != "1.0.0" || !st.HasBundle("flask-1.0.0") {
		t.Fatalf("the best version that works was not installed: %+v (asked %v)", app, asked)
	}
	if strings.Join(asked, ",") != "3.0.0,2.0.0,1.0.0" {
		t.Errorf("versions tried: %v", asked)
	}
}

// The ladder has a limit, and an app with no working version fails with the error of the newest one.
func TestTheLadderHasALimitAndFailsWithTheError(t *testing.T) {
	old := MaxLadder
	MaxLadder = 1
	defer func() { MaxLadder = old }()
	var asked []string
	svc, _ := newService(t, ladderFake{bad: map[string]bool{"3.0.0": true, "2.0.0": true, "1.0.0": true}, asked: &asked}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	if len(asked) != 2 {
		t.Fatalf("one try and one step down, not %v", asked)
	}
	for _, a := range svc.Apps() {
		if a.Name == "flask" && (a.State != StateFailed || !strings.Contains(a.Err, "did not start")) {
			t.Fatalf("it should be failed: %+v", a)
		}
	}
}

// The bound is understood by the helpers every manager uses.
func TestTheOlderThanBound(t *testing.T) {
	if b, ok := upperBound("<1.2.3"); !ok || b != "1.2.3" {
		t.Fatalf("bound: %q %v", b, ok)
	}
	if _, ok := upperBound("1.2.3"); ok {
		t.Error("a version is not a bound")
	}
	got := olderThan([]string{"v1.10.0", "v1.9.2", "v1.9.0", "v0.5.0"}, "1.9.2")
	if strings.Join(got, ",") != "v1.9.0,v0.5.0" {
		t.Errorf("older than 1.9.2: %v", got)
	}
	if !validWant("<2.0") || !validWant("1.2") || !validWant("") || validWant("<; rm") {
		t.Error("validWant")
	}
}

// The manifest of pixi and the project of cargo carry the bound.
func TestTheBoundIsPassedToPixiAndCargo(t *testing.T) {
	m, err := Pixi{}.Manifest("numpy", "<2.0", KindTool)
	if err != nil || !strings.Contains(m, `"numpy" = "<2.0"`) {
		t.Fatalf("pixi: %v %q", err, m)
	}
	dir := t.TempDir()
	if err := project(dir, "serde", "<1.0.200"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if !strings.Contains(string(raw), `serde = "<1.0.200"`) {
		t.Fatalf("cargo: %s", raw)
	}
}

// A worker walks down the ladder by itself: the gateway's version does not work on this node, so it installs the newest one below it, in the bundle
// of the gateway's id, and reports the version it holds.
func TestAWorkerLaddersDownToAVersionThatWorksOnIt(t *testing.T) {
	gw, _ := newService(t, ladderFake{}, "flask")
	gw.Install("ann", "flask", KindLib, false)
	gw.Wait()
	snap := gw.Snapshot()
	var asked []string
	w, wst := newService(t, ladderFake{bad: map[string]bool{"3.0.0": true}, asked: &asked})
	st := w.Apply(context.Background(), snap)
	if len(st.Failed) != 0 || !wst.HasBundle("flask-3.0.0") {
		t.Fatalf("the worker did not get a version that works: %+v (asked %v)", st.Failed, asked)
	}
	if st.Versions["flask-3.0.0"] != "2.0.0" {
		t.Errorf("it should report the version it holds: %v", st.Versions)
	}
	if strings.Join(asked, ",") != "3.0.0,2.0.0" {
		t.Errorf("versions tried: %v", asked)
	}
	// nothing older that works: it fails with the error of the version that failed
	w2, _ := newService(t, ladderFake{bad: map[string]bool{"3.0.0": true, "2.0.0": true, "1.0.0": true}})
	if st := w2.Apply(context.Background(), snap); st.Failed["flask-3.0.0"] == "" {
		t.Fatalf("it should fail: %+v", st)
	}
}

// A node tells the gateway what it is doing when a bundle BEGINS (a long install sends nothing else for minutes): the app it installs, since when,
// and the apps that wait for their turn.
func TestANodeSaysWhatItIsInstallingAndWhatWaits(t *testing.T) {
	gw, _ := newService(t, Fake{}, "flask", "numpy")
	gw.Install("ann", "flask", KindLib, false)
	gw.Wait()
	gw.Install("ann", "numpy", KindLib, false)
	gw.Wait()
	snap := gw.Snapshot()
	w, _ := newService(t, Fake{})
	var states []NodeState
	w.SetOnStep(func(st NodeState) { states = append(states, st) })
	w.Apply(context.Background(), snap)
	if len(states) != 4 { // a start and an end for each of the two bundles
		t.Fatalf("reports: %d", len(states))
	}
	first := states[0]
	if first.Doing != "flask" || first.Since == 0 || len(first.Queued) != 1 || first.Queued[0] != "numpy" {
		t.Fatalf("at the start of the first bundle: %+v", first)
	}
	second := states[2]
	if second.Doing != "numpy" || len(second.Queued) != 0 {
		t.Fatalf("at the start of the second: %+v", second)
	}
	if last := states[3]; last.Doing != "" || len(last.Have) != 2 {
		t.Fatalf("at the end: %+v", last)
	}
	if doing, _, queued := w.Activity(); doing != "" || len(queued) != 0 {
		t.Fatalf("a node that is done is doing nothing: %q %v", doing, queued)
	}
}

// A version of an npm package that cannot be looked up does not stop the install: the next older one is tried (the newest eslint could not be found
// and the whole install failed with "npm could not find eslint@10.0.3").
func TestAnNpmVersionThatCannotBeLookedUpIsSkipped(t *testing.T) {
	dir := t.TempDir()
	npm := filepath.Join(dir, "npm")
	os.WriteFile(npm, []byte(`#!/bin/sh
# npm view <spec> [fields] --json
spec="$2"
case "$spec" in
  eslint) echo '["9.0.0","10.0.3"]' ;;
  eslint@10.0.3) exit 1 ;;
  eslint@9.0.0) echo '{"engines.node":">=18","dist.unpackedSize":3000000}' ;;
  *) exit 1 ;;
esac
`), 0755)
	node := filepath.Join(dir, "node")
	os.WriteFile(node, []byte("#!/bin/sh\necho v20.1.0\n"), 0755)
	res, err := Npm{Bin: npm, Node: node, Dir: dir}.Resolve(context.Background(), "eslint", "", KindNode)
	if err != nil || res.Version != "9.0.0" {
		t.Fatalf("the older version was not taken: %+v %v", res, err)
	}
	// every version unknown: the error says so
	os.WriteFile(npm, []byte("#!/bin/sh\ncase \"$2\" in eslint) echo '[\"10.0.3\"]' ;; *) exit 1 ;; esac\n"), 0755)
	if _, err := (Npm{Bin: npm, Node: node, Dir: dir}).Resolve(context.Background(), "eslint", "", KindNode); err == nil || !strings.Contains(err.Error(), "could be looked up") {
		t.Fatalf("all versions failed: %v", err)
	}
}

// The version of an npm package is looked for per major: a library whose last release that the Node can run is far back (more than 40 releases) is found in a few
// lookups, and inside a major the newest release the Node can run is found when only the newest ones need a newer Node.
func TestNpmChoosesPerMajorAndWalksDownAMajor(t *testing.T) {
	dir := t.TempDir()
	npm := filepath.Join(dir, "npm")
	calls := filepath.Join(dir, "calls")
	// versions: 9.0 to 9.19 and 8.0 to 8.19 (all need Node 18), 7.0 to 7.32 (7.28 and up need Node 18, the rest Node 10), 6.0 to 6.8
	script := `#!/bin/sh
echo "$2" >> ` + calls + `
case "$2" in
  eslint)
    echo -n '['
    first=1
    for v in 6.0.0 6.8.0 7.0.0 7.5.0 7.10.0 7.15.0 7.20.0 7.27.0 7.28.0 7.30.0 7.32.0 8.0.0 8.10.0 8.19.0 9.0.0 9.10.0 9.19.0; do
      [ $first = 1 ] || echo -n ','; first=0; echo -n "\"$v\""
    done
    echo ']' ;;
  eslint@9.*|eslint@8.*|eslint@7.28.*|eslint@7.3*) echo '{"engines.node":">=18","dist.unpackedSize":3000000}' ;;
  eslint@7.*|eslint@6.*) echo '{"engines.node":">=10.12","dist.unpackedSize":2000000}' ;;
  *) exit 1 ;;
esac
`
	os.WriteFile(npm, []byte(script), 0755)
	node := filepath.Join(dir, "node")
	os.WriteFile(node, []byte("#!/bin/sh\necho v12.22.9\n"), 0755)
	res, err := Npm{Bin: npm, Node: node, Dir: dir}.Resolve(context.Background(), "eslint", "", KindNode)
	if err != nil || res.Version != "7.27.0" {
		t.Fatalf("the newest release of major 7 that Node 12 can run is 7.27.0: %+v %v", res, err)
	}
	raw, _ := os.ReadFile(calls)
	if n := strings.Count(string(raw), "\n"); n > 12 {
		t.Fatalf("%d lookups: one per major, not one per release:\n%s", n, raw)
	}
	// a bound: older than 7.27.0 gives 7.20.0
	res, err = Npm{Bin: npm, Node: node, Dir: dir}.Resolve(context.Background(), "eslint", "<7.27.0", KindNode)
	if err != nil || res.Version != "7.20.0" {
		t.Fatalf("below 7.27.0: %+v %v", res, err)
	}
	if g := groupByMajor([]string{"9.1.0", "9.0.0", "8.2.0", "7.0.0", "7.0.0-beta"}); len(g) != 3 || len(g[0]) != 2 {
		t.Fatalf("groups: %v", g)
	}
}

// After a second failure the ladder jumps to the next lower major: the steps are not spent on the patch releases of a major that is broken.
func TestTheLadderJumpsToTheNextLowerMajor(t *testing.T) {
	if ladderBound("19.2.0", 0) != "<19.2.0" || ladderBound("19.1.0", 1) != "<19.0.0" || ladderBound("0.7.3", 1) != "<0.7.0" || ladderBound("1.0.0", 1) != "<1.0.0" {
		t.Fatalf("bounds: %s %s %s %s", ladderBound("19.2.0", 0), ladderBound("19.1.0", 1), ladderBound("0.7.3", 1), ladderBound("1.0.0", 1))
	}
	var asked []string
	f := ladderFake{vers: []string{"3.2.0", "3.1.0", "3.0.0", "2.9.0", "2.8.0", "1.5.0"}, bad: map[string]bool{"3.2.0": true, "3.1.0": true, "3.0.0": true, "2.9.0": true, "2.8.0": true}, asked: &asked}
	svc, st := newService(t, f, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	if strings.Join(asked, ",") != "3.2.0,3.1.0,2.9.0,1.5.0" || !st.HasBundle("flask-1.5.0") {
		t.Fatalf("versions tried: %v", asked)
	}
}

// A scoped npm package has a name of the store that is a folder name, npm gets its own name, and the files of a browser library are found under it.
func TestAScopedNpmPackageWorksInTheStore(t *testing.T) {
	for in, want := range map[string]string{"@picocss/pico": "picocss__pico", "@tailwindcss/browser": "tailwindcss__browser", "lodash": "lodash", "@x": "@x", "react-dom": "react-dom"} {
		if got := EncodeNpmName(in); got != want {
			t.Errorf("EncodeNpmName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"picocss__pico": "@picocss/pico", "lodash": "lodash", "@a/b": "@a/b", "a__": "a__"} {
		if got := NpmName(in); got != want {
			t.Errorf("NpmName(%q) = %q, want %q", in, got, want)
		}
	}
	e := Entry{ID: "web:@picocss/pico", Name: "@picocss/pico", Kind: CKWeb, Package: "@picocss/pico"}
	if e.PackageName() != "picocss__pico" || !ValidName(e.PackageName()) {
		t.Fatalf("the name of the app: %q", e.PackageName())
	}
	// the installer asks npm for the real name
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	npm := filepath.Join(dir, "npm")
	os.WriteFile(npm, []byte("#!/bin/sh\necho \"$@\" >> "+argsFile+"\ncase \"$2\" in @picocss/pico) echo '[\"2.0.6\",\"1.5.0\"]' ;; @picocss/pico@2.0.6) echo '{\"engines.node\":\">=12\",\"dist.unpackedSize\":100000}' ;; esac\n"), 0755)
	node := filepath.Join(dir, "node")
	os.WriteFile(node, []byte("#!/bin/sh\necho v20.1.0\n"), 0755)
	n := Npm{Bin: npm, Node: node, Dir: dir}
	res, err := n.Resolve(context.Background(), "picocss__pico", "", KindNode)
	if err != nil || res.Version != "2.0.6" {
		t.Fatalf("resolve: %+v %v", res, err)
	}
	n.Install(context.Background(), "picocss__pico", "2.0.6", KindNode, filepath.Join(dir, "b"), io.Discard)
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "@picocss/pico@2.0.6") || strings.Contains(string(raw), "picocss__pico") {
		t.Fatalf("npm was asked for: %s", raw)
	}
	// the default catalogue has both scoped entries, with valid names
	entries, _ := DefaultCatalogue()
	var found int
	for _, x := range entries {
		if strings.HasPrefix(x.Source(), "@") {
			found++
			if !ValidName(x.PackageName()) {
				t.Errorf("%s: %q is not a valid name", x.ID, x.PackageName())
			}
		}
	}
	if found == 0 {
		t.Error("the default catalogue has scoped entries")
	}
}

func TestACacheFolderPastItsLimitIsEmptied(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gocache")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a"), make([]byte, 3<<20), 0o644)
	capCache(dir, 100)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("a small cache must stay")
	}
	capCache(dir, 1)
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("a cache past its limit must be emptied")
	}
}

func TestRetiredEntriesAreListedAndRemovedOnlyWhenPicked(t *testing.T) {
	svc, _ := newService(t, Fake{})
	// a record of a retired entry (it was measured once) and one of a live, admin-added entry
	if err := svc.AddEntry("measure", Entry{ID: "polars", Name: "polars", Kind: CKLib, Package: "polars", Manager: MgrPixi, Repls: []string{"python"}, Level: "optional", Install: "anyone"}); err != nil {
		t.Fatal(err)
	}
	svc.AddEntry("admin", Entry{ID: "mine", Name: "mine", Kind: CKLib, Package: "mine", Manager: MgrPixi, Repls: []string{"python"}, Level: "optional", Install: "anyone"})
	r := svc.Retired()
	if len(r) != 1 || r[0].ID != "polars" {
		t.Fatalf("retired = %+v", r)
	}
	if n, _ := svc.PurgeRetired([]string{"mine", "numpy", "polars"}); n != 1 {
		t.Errorf("removed %d, want 1 (only the retired and listed one)", n)
	}
	if _, ok := svc.Entry("polars"); ok {
		t.Error("polars is still listed")
	}
	if _, ok := svc.Entry("mine"); !ok {
		t.Error("an admin's own entry was taken off")
	}
	if _, ok := svc.Entry("numpy"); !ok {
		t.Error("a live entry was taken off")
	}
}

func TestAnAdminRemovedEntryIsListedAndCanComeBack(t *testing.T) {
	svc, _ := newService(t, Fake{})
	if err := svc.RemoveEntry("admin", "numpy"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Entry("numpy"); ok {
		t.Fatal("numpy is still listed after Remove")
	}
	var found bool
	for _, r := range svc.Retired() {
		if r.ID == "numpy" && r.Removed && r.InDefault {
			found = true
		}
	}
	if !found {
		t.Fatal("a removed entry is not offered for restoring")
	}
	if n, _ := svc.PurgeRetired([]string{"numpy"}); n != 0 {
		t.Error("purge must not touch an entry the default file has")
	}
	if svc.RestoreRemoved([]string{"numpy", "pandas"}) != 1 {
		t.Error("only the removed one comes back")
	}
	if _, ok := svc.Entry("numpy"); !ok {
		t.Error("numpy did not come back")
	}
}
