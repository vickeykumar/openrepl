package appstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, l *InstallLogs, app string, n int) ([]LogLine, AttemptInfo) {
	t.Helper()
	data, _, info, ok := l.Read(app, n, 0, 0)
	if !ok {
		t.Fatalf("no log for %s #%d", app, n)
	}
	var out []LogLine
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ll LogLine
		if json.Unmarshal([]byte(line), &ll) != nil {
			t.Fatalf("a line that is not JSON: %q", line)
		}
		out = append(out, ll)
	}
	return out, info
}

// The log of an attempt: lines with a time, a step and a level; output split into lines; the result in the index; secrets and paths cleaned.
func TestTheLogOfAnAttemptIsWrittenAndCleaned(t *testing.T) {
	dir := t.TempDir()
	l := NewInstallLogs(dir, nil)
	lg := l.Begin("eslint", "install", "gateway")
	lg.Line("start", "info", "install eslint on %s", "gateway")
	lg.Output([]byte("npm http fetch GET 200 https://user:pa55@registry.npmjs.org/x 412ms\nAuthorization: Bearer abcdef123456\nin " + dir + "/apps/x\npart"))
	lg.Output([]byte("ial line\n"))
	lg.Line("command", "info", "token=ghp_abcdefghijklmnopqrstuv and cookie: user-session=abc")
	lg.End("installed", "9.2.0", "npm", "")
	lines, info := readAll(t, l, "eslint", 0)
	if info.Result != "installed" || info.Version != "9.2.0" || info.Manager != "npm" || info.N != 1 || info.End == 0 {
		t.Fatalf("the attempt: %+v", info)
	}
	joined := ""
	for _, ln := range lines {
		joined += ln.Step + "|" + ln.Level + "|" + ln.Text + "\n"
	}
	for _, secret := range []string{"pa55", "abcdef123456", "ghp_abcdefghijklmnopqrstuv", "user-session=abc", dir} {
		if strings.Contains(joined, secret) {
			t.Errorf("%q is in the log:\n%s", secret, joined)
		}
	}
	for _, want := range []string{"<hidden>@registry.npmjs.org", "/store/apps/x", "partial line", "result|info|result: installed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is not in the log:\n%s", want, joined)
		}
	}
}

// The log has a size limit, keeps the last attempts, and reads by offset in whole lines.
func TestTheLogsAreLimitedAndReadByOffset(t *testing.T) {
	oldMax, oldKeep := LogMaxBytes, LogKeep
	LogMaxBytes, LogKeep = 2000, 3
	defer func() { LogMaxBytes, LogKeep = oldMax, oldKeep }()
	l := NewInstallLogs(t.TempDir(), nil)
	for i := 0; i < 5; i++ {
		lg := l.Begin("numpy", "install", "gateway")
		for j := 0; j < 100; j++ {
			lg.Line("output", "output", "line %d of attempt %d with some text to fill the file up", j, i)
		}
		lg.End("failed", "", "pixi", "x")
	}
	at := l.Attempts("numpy")
	if len(at) != 3 || at[0].N != 5 || at[2].N != 3 {
		t.Fatalf("the last 3 attempts, newest first: %+v", at)
	}
	if _, err := os.Stat(attemptFile(l.appDir("numpy"), 1)); err == nil {
		t.Error("the file of an attempt that was dropped is still there")
	}
	lines, _ := readAll(t, l, "numpy", 5)
	last := lines[len(lines)-1]
	if !strings.Contains(last.Text, "result: failed") || !strings.Contains(strings.Join(func() []string {
		var s []string
		for _, x := range lines {
			s = append(s, x.Text)
		}
		return s
	}(), "\n"), "the log stops here") {
		t.Errorf("a log that reached its limit says so: %+v", last)
	}
	// by offset: two reads that go on from each other give the whole, in whole lines
	d1, n1, _, _ := l.Read("numpy", 5, 0, 300)
	d2, n2, _, _ := l.Read("numpy", 5, n1, 300)
	if !strings.HasSuffix(string(d1), "\n") || n2 <= n1 || len(d2) == 0 {
		t.Errorf("reading by offset: %d bytes then %d", len(d1), len(d2))
	}
	if _, _, _, ok := l.Read("numpy", 99, 0, 0); ok {
		t.Error("an attempt that does not exist")
	}
	// the age: an attempt older than LogMaxAge goes, the newest of the app stays
	now := time.Now()
	old := NewInstallLogs(t.TempDir(), func() time.Time { return now })
	lg := old.Begin("zz", "install", "g")
	lg.End("installed", "1", "pixi", "")
	now = now.Add(LogMaxAge + 24*time.Hour)
	if at := old.Attempts("zz"); len(at) != 1 {
		t.Fatalf("the newest stays whatever its age: %+v", at)
	}
	lg2 := old.Begin("zz", "install", "g")
	lg2.End("installed", "2", "pixi", "")
	if at := old.Attempts("zz"); len(at) != 1 || at[0].N != 2 {
		t.Fatalf("the one that is too old went: %+v", at)
	}
}

// An install writes its log: the environment, the managers, the look up, the install, the trim, the check, the result; a failure says why.
func TestAnInstallWritesItsLog(t *testing.T) {
	svc, _ := newService(t, Fake{}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	at := svc.LogAttempts("flask")
	if len(at) != 1 || at[0].Result != "installed" || at[0].Label != "install" || at[0].Version == "" {
		t.Fatalf("the attempt: %+v", at)
	}
	ch := svc.ReadLog("flask", 0, 0)
	steps := map[string]bool{}
	for _, l := range ch.Lines {
		steps[l.Step] = true
	}
	for _, want := range []string{"start", "env", "order", "look up", "install", "trim", "check", "result"} {
		if !steps[want] {
			t.Errorf("no %q line in the log: %+v", want, ch.Lines)
		}
	}
	if !ch.Found || ch.Running || len(ch.Attempts) != 1 {
		t.Errorf("the chunk: %+v", ch)
	}
	// a failure: the error is in the log and the attempt is failed
	bad, _ := newService(t, netFail{n: new(int)}, "flask")
	bad.Install("ann", "flask", KindLib, false)
	bad.Wait()
	at = bad.LogAttempts("flask")
	if len(at) == 0 || at[0].Result != "failed" || !strings.Contains(at[0].Err, "connection reset") {
		t.Fatalf("the failed attempt: %+v", at)
	}
	fl := bad.ReadLog("flask", 0, 0)
	var sawErr bool
	for _, l := range fl.Lines {
		if l.Level == "error" && strings.Contains(l.Text, "connection reset") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Errorf("the error is not in the log: %+v", fl.Lines)
	}
}

// The version ladder is in the log: each version tried and why it was left.
func TestTheLadderIsInTheLog(t *testing.T) {
	svc, _ := newService(t, ladderFake{bad: map[string]bool{"3.0.0": true, "2.0.0": true}}, "flask")
	svc.Install("ann", "flask", KindLib, false)
	svc.Wait()
	ch := svc.ReadLog("flask", 0, 0)
	var ladders int
	for _, l := range ch.Lines {
		if l.Step == "ladder" {
			ladders++
		}
	}
	if ladders != 2 || ch.Info.Result != "installed" || ch.Info.Version != "1.0.0" {
		t.Fatalf("ladder lines %d, attempt %+v", ladders, ch.Info)
	}
}

// A worker's replay writes a log of its own, labelled replay; and the catalog keeps a summary after the files are gone (a wiped disk).
func TestAReplayWritesALogAndTheSummarySurvivesAWipe(t *testing.T) {
	gw, _ := newService(t, Fake{}, "flask")
	gw.Install("ann", "flask", KindLib, false)
	gw.Wait()
	w, wst := newService(t, Fake{})
	w.Apply(context.Background(), gw.Snapshot())
	wa := w.LogAttempts("flask")
	if len(wa) != 1 || wa[0].Label != "replay" || wa[0].Result != "installed" {
		t.Fatalf("the replay: %+v", wa)
	}
	// wipe the files of the gateway: the summary in the catalog is still listed
	os.RemoveAll(filepath.Join(gw.Store().Dir, "logs"))
	_ = wst
	at := gw.LogAttempts("flask")
	if len(at) != 1 || !at[0].LogGone || at[0].Result != "installed" {
		t.Fatalf("after the wipe: %+v", at)
	}
	if ch := gw.ReadLog("flask", 0, 0); ch.Found || len(ch.Attempts) != 1 {
		t.Fatalf("the chunk after the wipe: %+v", ch)
	}
}

// The environment line says where the install runs.
func TestTheEnvironmentLine(t *testing.T) {
	l := EnvLine("pi-1", t.TempDir())
	if !strings.HasPrefix(l, "pi-1 | linux-") || !strings.Contains(l, "disk free") {
		t.Fatalf("env line: %q", l)
	}
}

// A resolver says what it skipped, into the log of the attempt in the context.
func TestAResolverWritesToTheLogInTheContext(t *testing.T) {
	l := NewInstallLogs(t.TempDir(), nil)
	var wl workLog
	wl.lg = l.Begin("eslint", "install", "g")
	ctx := WithAttemptLog(context.Background(), &wl)
	logf(ctx, "look up", "warn", "npm: %s could not be looked up", "eslint@10.0.3")
	logf(context.Background(), "look up", "warn", "nobody listens") // no log in the context: nothing happens
	wl.lg.End("installed", "9.2.0", "npm", "")
	lines, _ := readAll(t, l, "eslint", 0)
	if !strings.Contains(lines[0].Text, "eslint@10.0.3") {
		t.Fatalf("%+v", lines)
	}
}
