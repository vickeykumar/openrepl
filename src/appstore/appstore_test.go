package appstore

import (
	"context"
	"errors"
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

func TestVersionFromSolve(t *testing.T) {
	raw := []byte(`{"actions":{"LINK":[{"name":"werkzeug","version":"3.0.4"},{"name":"flask","version":"3.0.2"}]}}`)
	if v, err := versionFromSolve(raw, "flask"); err != nil || v != "3.0.2" {
		t.Errorf("got %q %v", v, err)
	}
	if _, err := versionFromSolve(raw, "numpy"); err == nil {
		t.Error("a package that is not in the answer is an error")
	}
	if _, err := versionFromSolve([]byte("not json"), "flask"); err == nil {
		t.Error("bad json")
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
	if err := f.Install(context.Background(), "flask", "3.0.2", dest, os.Stderr); err != nil {
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
	if err := svc.Install("ann", "numpy", "lib", false); !errors.Is(err, ErrNeedsApproval) {
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
	r, err := svc.Request("ann", "numpy", "lib", "arrays")
	if err != nil || r.Status != "waiting" {
		t.Fatal(r, err)
	}
	svc.Request("bob", "numpy", "lib", "")
	if reqs := svc.Requests(); len(reqs) != 1 || len(reqs[0].Askers) != 2 || reqs[0].Reason != "arrays" {
		t.Fatalf("requests %+v", reqs)
	}
	if err := svc.Approve("root", "numpy"); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if len(svc.Requests()) != 0 || len(svc.Apps()) != 2 {
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
