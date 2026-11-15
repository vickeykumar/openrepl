package appstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

func TestSolveFrom(t *testing.T) {
	raw := []byte(`{"actions":{"FETCH":[{"name":"werkzeug","version":"3.0.4","size":1048576},{"name":"flask","version":"3.0.2","size":2097152}],"LINK":[{"name":"werkzeug","version":"3.0.4","size":1048576},{"name":"flask","version":"3.0.2","size":2097152},{"name":"click","version":"8.1","size":1048576}]}}`)
	r, err := solveFrom(raw, "flask")
	if err != nil || r.Version != "3.0.2" || r.Packages != 3 || r.DownloadMB != 4 || r.DiskMB != 10 {
		t.Errorf("got %+v %v", r, err)
	}
	// every package is in the cache already: nothing is fetched, the size is still known
	cached := []byte(`{"actions":{"FETCH":[],"LINK":[{"name":"flask","version":"3.0.2","size":3145728}]}}`)
	if r, err := solveFrom(cached, "flask"); err != nil || r.DownloadMB != 3 {
		t.Errorf("a warm cache: %+v %v", r, err)
	}
	if _, err := solveFrom(raw, "numpy"); err == nil {
		t.Error("a package that is not in the answer is an error")
	}
	if _, err := solveFrom([]byte("not json"), "flask"); err == nil {
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
	if err := checkBundle(context.Background(), dir, "x", KindLib); err != nil {
		t.Errorf("a working python: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "bin", "python"), []byte("#!/bin/sh\necho 'ImportError: no module named x' >&2\nexit 1\n"), 0755)
	if err := checkBundle(context.Background(), dir, "x", KindLib); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Errorf("a python that fails must refuse the bundle: %v", err)
	}
	if err := checkBundle(context.Background(), t.TempDir(), "x", KindLib); err != nil {
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

func TestABundleTravelsAsATarball(t *testing.T) {
	a := Store{Dir: t.TempDir()}
	f := Fake{}
	id := "flask-1.0.0"
	if err := f.Install(context.Background(), "flask", "1.0.0", KindLib, a.BundlePath(id), os.Stderr); err != nil {
		t.Fatal(err)
	}
	os.Symlink("../lib/python3.10", filepath.Join(a.BundlePath(id), "bin", "pylib"))
	a.Finish(id, "flask", "1.0.0")
	var buf bytes.Buffer
	if err := a.WriteBundle(id, &buf); err != nil {
		t.Fatal(err)
	}
	b := Store{Dir: t.TempDir()}
	if err := ReadBundle(bytes.NewReader(buf.Bytes()), b.BundlePath(id)); err != nil {
		t.Fatal(err)
	}
	if !b.HasBundle(id) {
		t.Fatal("the marker comes with it, last")
	}
	got, err := os.ReadFile(filepath.Join(b.BundlePath(id), "lib", "python3.10", "site-packages", "flask", "__init__.py"))
	if err != nil || !strings.Contains(string(got), "1.0.0") {
		t.Fatalf("%s %v", got, err)
	}
	if l, err := os.Readlink(filepath.Join(b.BundlePath(id), "bin", "pylib")); err != nil || l != "../lib/python3.10" {
		t.Fatalf("the link: %q %v", l, err)
	}
	if fi, _ := os.Stat(filepath.Join(b.BundlePath(id), "bin", "flask-hello")); fi == nil || fi.Mode()&0100 == 0 {
		t.Error("the executable bit")
	}
	if err := a.WriteBundle("nothing-1", &buf); err == nil {
		t.Error("an unfinished bundle is not sent")
	}
	// a bad archive cannot write outside the bundle
	for name, mk := range map[string]func(tw *tar.Writer){
		"climbing name": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0644, Size: 1, Typeflag: tar.TypeReg})
			tw.Write([]byte("x"))
		},
		"absolute name": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "/etc/evil", Mode: 0644, Size: 1, Typeflag: tar.TypeReg})
			tw.Write([]byte("x"))
		},
		"outside link": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "l", Linkname: "../../etc", Typeflag: tar.TypeSymlink})
		},
		"absolute link": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "l", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
		},
		"a device": func(tw *tar.Writer) { tw.WriteHeader(&tar.Header{Name: "d", Typeflag: tar.TypeChar}) },
	} {
		var bad bytes.Buffer
		gz := gzip.NewWriter(&bad)
		tw := tar.NewWriter(gz)
		mk(tw)
		tw.Close()
		gz.Close()
		dest := filepath.Join(t.TempDir(), "x-1")
		if err := ReadBundle(&bad, dest); err == nil {
			t.Errorf("%s must be refused", name)
		}
		if _, err := os.Stat(dest); err == nil {
			t.Errorf("%s: what was written must be removed", name)
		}
	}
}

func TestAWorkerTakesABundleFromTheGatewayFirst(t *testing.T) {
	a, _ := newService(t, Fake{}, "flask")
	a.Install("ann", "flask", "lib", false)
	a.Wait()
	snap := a.Snapshot()
	b, bst := newService(t, Fake{Fail: map[string]bool{"flask": true}}) // it could not install it itself
	fetched := 0
	b.cfg.Fetch = func(ctx context.Context, id, dest string, progress func(int)) error {
		fetched++
		var buf bytes.Buffer
		if err := a.Store().WriteBundle(id, &buf); err != nil {
			return err
		}
		return ReadBundle(&buf, dest)
	}
	st := b.Apply(context.Background(), snap)
	if fetched != 1 || len(st.Failed) != 0 || !bst.HasBundle("flask-1.0.0") {
		t.Fatalf("fetched %d, state %+v", fetched, st)
	}
	// and when the gateway cannot give it, the node installs it itself
	c, cst := newService(t, Fake{})
	c.cfg.Fetch = func(ctx context.Context, id, dest string, progress func(int)) error {
		return errors.New("the gateway is not reachable")
	}
	st = c.Apply(context.Background(), snap)
	if len(st.Failed) != 0 || !cst.HasBundle("flask-1.0.0") {
		t.Fatalf("%+v", st)
	}
}

// micromamba reads its settings from MAMBA_* variables too, and a switch with the value 1 stops it ("Bad conversion of configurable
// 'no_rc'"): the folders are the only thing the program may be given that way, the switches are flags.
func TestMambaIsGivenNoSwitchesInTheEnvironment(t *testing.T) {
	m := Mamba{Bin: "/usr/bin/micromamba", Dir: "/store", Python: "3.10"}
	c := m.cmd(context.Background(), "create")
	for _, e := range c.Env {
		if strings.HasPrefix(e, "MAMBA_") || strings.HasPrefix(e, "CONDA") {
			k := e[:strings.Index(e, "=")]
			if k != "MAMBA_ROOT_PREFIX" && k != "CONDA_PKGS_DIRS" && !strings.HasPrefix(e, k+"=/") && !strings.HasPrefix(os.Getenv("NOPE_"+k), "x") {
				t.Errorf("%s in the environment of micromamba: a switch must be a flag", e)
			}
		}
	}
	if !contains(m.common(KindLib), "--no-rc") {
		t.Error("the rc files must be switched off by a flag")
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
	if _, err := (Missing{Reason: "no micromamba"}).Search(context.Background(), "x", ""); err == nil {
		t.Error("a server without micromamba says so")
	}
}

func TestFoundFromReadsTheSearchOfMicromamba(t *testing.T) {
	raw := []byte(`{"result":{"pkgs":[{"name":"flask","version":"2.0.0"},{"name":"flask","version":"3.1.3"},{"name":"flask-cors","version":"4.0.0"},{"name":"flaskcache","version":"0.1"},{"name":"BAD NAME","version":"1"}]}}`)
	got, err := foundFrom(raw, "flask", KindLib)
	if err != nil || len(got) != 3 || got[0].Name != "flask" || got[0].Version != "3.1.3" || got[1].Name != "flask-cors" || got[2].Name != "flaskcache" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := foundFrom([]byte("nope"), "x", ""); err == nil {
		t.Error("bad json")
	}
	if got, _ := (Mamba{Bin: "/x"}).Search(context.Background(), "../x", ""); got != nil {
		t.Error("a bad pattern is never sent to micromamba")
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

func TestAToolIsSolvedWithoutAPythonAndALibraryWithIt(t *testing.T) {
	m := Mamba{Bin: "/x", Python: "3.10"}
	if !contains(m.common(KindLib), "python=3.10") || contains(m.common(KindTool), "python=3.10") || contains(m.common(KindLang), "python=3.10") {
		t.Errorf("lib %v tool %v lang %v", m.common(KindLib), m.common(KindTool), m.common(KindLang))
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

func TestAFetchSaysHowFarItIs(t *testing.T) {
	a := Store{Dir: t.TempDir()}
	id := "flask-1.0.0"
	Fake{}.Install(context.Background(), "flask", "1.0.0", KindLib, a.BundlePath(id), os.Stderr)
	a.Finish(id, "flask", "1.0.0")
	var buf bytes.Buffer
	if err := a.WriteBundle(id, &buf); err != nil {
		t.Fatal(err)
	}
	b := Store{Dir: t.TempDir()}
	var seen []int
	if err := ReadBundleProgress(&buf, b.BundlePath(id), a.BundleSize(id), func(p int) { seen = append(seen, p) }); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || seen[len(seen)-1] > 100 {
		t.Errorf("progress %v", seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Errorf("the percent only goes up: %v", seen)
		}
	}
}

func TestAUserMayUninstallAnAppOnlyWhenNobodyElseHasItOn(t *testing.T) {
	svc, st := newService(t, Fake{}, "flask", "numpy")
	svc.Install("ann", "flask", KindLib, false)
	svc.Install("ann", "numpy", KindLib, false)
	svc.Wait()
	svc.Switch("ann", "flask-1.0.0", true)
	svc.Switch("bob", "flask-1.0.0", true)
	// bob has it on too: ann cannot uninstall it, and is told how many others use it
	err := svc.Uninstall("ann", "ann", "flask")
	if !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "1 other users") || !st.HasBundle("flask-1.0.0") {
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
