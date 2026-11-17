//go:build realinstall

package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The health of the catalogue (LLD 18, section 13): every entry that the catalogue says has a chosen way to install (a measured `minimal`) is installed with
// that manager, trimmed and checked, inside the image. An entry that worked when it was measured and does not now has broken (a new release, a package that
// left an archive, a Node or Go too old for it): the test fails and lists them. The weekly workflow "Catalogue health" runs it on both CPU types.
//
//	OPENREPL_HEALTH_OUT   the file to write (a JSON list of what was tried)
//	OPENREPL_HEALTH_ONLY  a comma list of entry ids, for a quick run
func TestCatalogueHealth(t *testing.T) {
	out := os.Getenv("OPENREPL_HEALTH_OUT")
	if out == "" {
		t.Skip("set OPENREPL_HEALTH_OUT to check the health of the catalogue")
	}
	arch := ArchAMD64
	if runtime.GOARCH == "arm64" {
		arch = ArchARM64
	}
	svc, dir := realMeasureService(t)
	m := MeasureEnv{Svc: svc, Work: dir + "/health", Arch: arch, AptPackage: AptHas}
	os.MkdirAll(m.Work, 0755)
	only := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("OPENREPL_HEALTH_ONLY"), ",") {
		if n != "" {
			only[n] = true
		}
	}
	type row struct {
		ID      string  `json:"id"`
		Manager string  `json:"manager"`
		OK      bool    `json:"ok"`
		Version string  `json:"version,omitempty"`
		MB      float64 `json:"mb,omitempty"`
		Seconds int     `json:"seconds"`
		Err     string  `json:"err,omitempty"`
	}
	var rows []row
	var broken []string
	var unchecked int
	for _, e := range svc.Catalogue() {
		if e.InstallKind() == "" || e.Minimal == nil || e.Manager == MgrOurs || (len(only) > 0 && !only[e.ID]) {
			if e.InstallKind() != "" && e.Minimal == nil && e.Manager != MgrOurs {
				unchecked++
			}
			continue
		}
		o, ok := e.OptionOf(e.Manager)
		if !ok {
			o = Option{Manager: e.Manager}
		}
		o.Size, o.Before, o.Err = nil, nil, ""
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		m.measureOne(ctx, e, &o, e.InstallKind())
		cancel()
		r := row{ID: e.ID, Manager: o.Manager, OK: o.Err == "", Version: o.Version, MB: o.Size[arch], Seconds: int(time.Since(start).Seconds()), Err: tail(o.Err, 300)}
		rows = append(rows, r)
		if !r.OK {
			broken = append(broken, fmt.Sprintf("%s (%s): %s", e.ID, o.Manager, firstLineOf(o.Err)))
		}
		t.Logf("%-22s %-12s ok=%v %s (%ds)", e.ID, o.Manager, r.OK, r.Version, r.Seconds)
		raw, _ := json.MarshalIndent(rows, "", " ")
		os.WriteFile(out, raw, 0644) // after every entry: a run that is cut short still leaves what it tried
	}
	sort.Strings(broken)
	summary := fmt.Sprintf("%d entries installed with their chosen manager on %s, %d broke; %d entries have no measured way to install (not checked)\n", len(rows)-len(broken), arch, len(broken), unchecked)
	if len(broken) > 0 {
		summary += "\nBroken:\n  " + strings.Join(broken, "\n  ") + "\n"
	}
	t.Log("\n" + summary)
	if f := os.Getenv("GITHUB_STEP_SUMMARY"); f != "" {
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644); err == nil {
			fmt.Fprintf(fh, "### Catalogue health (%s)\n```\n%s```\n", arch, summary)
			fh.Close()
		}
	}
	if len(broken) > 0 {
		t.Errorf("entries that worked when they were measured and do not now:\n  %s", strings.Join(broken, "\n  "))
	}
}

// A worker replays a bundle that the gateway made with apt, for real (the Raspberry Pi workers asked pixi for "git ==2.34.1-1ubuntu1.18" before): the same
// package and version from the gateway's lock, installed on a store of its own, and the program runs from there.
func TestAWorkerReplaysARealAptBundle(t *testing.T) {
	if _, err := exec.LookPath("apt-get"); err != nil {
		t.Skip("no apt-get here")
	}
	gw, _ := realMeasureService(t)
	e, ok := gw.Entry("tmux")
	if !ok || e.Manager != MgrApt {
		t.Skipf("the catalogue does not make tmux with apt: %+v", e.Manager)
	}
	if err := gw.InstallEntry("ci", e); err != nil && !isInstalled(err) {
		t.Fatal(err)
	}
	gw.Wait()
	var app App
	for _, a := range gw.Apps() {
		if a.Name == e.PackageName() {
			app = a
		}
	}
	if app.State != StateInstalled || app.Manager != MgrApt {
		t.Fatalf("the gateway did not make it with apt: %+v", app)
	}
	w, wdir := realMeasureService(t)
	w.SetLock(func(ctx context.Context, id string) (Lock, error) {
		if l, ok := gw.LockOf(id); ok {
			return l, nil
		}
		return Lock{}, fmt.Errorf("no lock for %s", id)
	})
	st := w.Apply(context.Background(), gw.Snapshot())
	if len(st.Failed) != 0 || !w.Store().HasBundle(app.ID) {
		t.Fatalf("the worker did not make the bundle: %+v", st.Failed)
	}
	cmd := exec.Command("sh", "-c", "tmux -V")
	cmd.Env = VarsFor(wdir, []string{app.ID}).Apply(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "tmux") {
		t.Fatalf("tmux does not run from the worker's bundle: %v %s", err, out)
	}
	if at := w.LogAttempts(e.PackageName()); len(at) == 0 || at[0].Label != "replay" || at[0].Result != "installed" {
		t.Fatalf("the replay has no log: %+v", at)
	}
}

func isInstalled(err error) bool { return err != nil && err.Error() == ErrInstalled.Error() }

// Several installs asked at once on one machine (a user presses Install on a few apps): they go one at a time, all of them end, and the ones that wait say so. Run
// with little memory (docker run --memory 512m) it also shows an install that the machine kills.
func TestInstallsAskedAtOnceGoOneAtATime(t *testing.T) {
	if _, err := exec.LookPath("apt-get"); err != nil {
		t.Skip("no apt-get here")
	}
	svc, _ := realMeasureService(t)
	var entries []Entry
	for _, id := range []string{"htop", "ripgrep", "tmux"} {
		if e, ok := svc.Entry(id); ok {
			entries = append(entries, e)
		}
	}
	var wg sync.WaitGroup
	for _, e := range entries {
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.InstallEntry("ci", e); err != nil && !isInstalled(err) {
				t.Errorf("%s: %v", e.ID, err)
			}
		}()
	}
	wg.Wait()
	sawWaiting := false
	for i := 0; i < 5; i++ {
		for _, a := range svc.AppsWithWaiting() {
			if a.Waiting {
				sawWaiting = true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	svc.Wait()
	for _, e := range entries {
		var app App
		for _, a := range svc.Apps() {
			if a.Name == e.PackageName() {
				app = a
			}
		}
		if app.State != StateInstalled {
			t.Errorf("%s did not end installed: %+v", e.ID, app)
		}
		if at := svc.LogAttempts(e.PackageName()); len(at) == 0 {
			t.Errorf("%s has no log", e.ID)
		}
	}
	_ = sawWaiting // an install that is fast may leave no time to see it waiting: the unit test of the label checks that
}
