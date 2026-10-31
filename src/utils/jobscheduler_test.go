package utils

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The scheduler that removes a guest's folder an hour after the last visit (DEADLINE_MINUTES): a job
// fires, a reset moves it later, and what is still waiting when the server stops is saved and
// carried on by the next one, also when its time came while the server was down.

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func TestAJobFiresAndAResetMovesItLater(t *testing.T) {
	js := NewJobScheduler()
	var mu sync.Mutex
	fired := map[string]int{}
	note := func(name string) func() {
		return func() { mu.Lock(); fired[name]++; mu.Unlock() }
	}
	count := func(name string) int { mu.Lock(); defer mu.Unlock(); return fired[name] }

	js.AddJob("REMOVE-a", 30*time.Millisecond, note("a"))
	js.AddJob("REMOVE-b", 60*time.Millisecond, note("b"))
	// a visit before the deadline resets the job: it must not fire at the old time
	time.Sleep(40 * time.Millisecond)
	js.ResetJob("REMOVE-b", 150*time.Millisecond, note("b"))
	waitFor(t, "a fired", func() bool { return count("a") == 1 })
	time.Sleep(60 * time.Millisecond)
	if count("b") != 0 {
		t.Fatal("a reset job fired at its old time")
	}
	waitFor(t, "b fired after the reset", func() bool { return count("b") == 1 })
	waitFor(t, "fired jobs are forgotten", func() bool {
		js.mutex.Lock()
		defer js.mutex.Unlock()
		return len(js.Jobs) == 0
	})
}

func TestWhatWaitsWhenTheServerStopsIsCarriedOnByTheNextOne(t *testing.T) {
	file := filepath.Join(t.TempDir(), "jobfile")
	first := NewJobScheduler()
	first.AddJob("REMOVE-later", 80*time.Millisecond, func() { t.Error("the first server ran a job it had saved") })
	first.AddJob("REMOVE-past", time.Hour, func() {})
	first.Jobs["REMOVE-past"].ExpirationTime = GetUnixMilli() - 1000 // its time came while the server was down
	if err := first.SaveJobsToFile(file); err != nil {
		t.Fatal(err)
	}
	first.RemoveJob("REMOVE-later")
	first.RemoveJob("REMOVE-past")

	next := NewJobScheduler()
	var mu sync.Mutex
	var ran []string
	if err := next.LoadJobsFromFile(file, func(name string) { mu.Lock(); ran = append(ran, name); mu.Unlock() }); err != nil {
		t.Fatal(err)
	}
	has := func(name string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range ran {
			if r == name {
				return true
			}
		}
		return false
	}
	waitFor(t, "the job that was past due runs at once", func() bool { return has("REMOVE-past") })
	waitFor(t, "the job that was waiting runs at its time", func() bool { return has("REMOVE-later") })
}

func TestAJobThatRunsOutDoesNotTakeItsNewerNamesakeWithIt(t *testing.T) {
	js := NewJobScheduler()
	started := make(chan struct{})
	release := make(chan struct{})
	js.AddJob("REMOVE-x", 10*time.Millisecond, func() { close(started); <-release })
	<-started
	// the guest comes back while the old job is still running: a new job takes the name
	js.AddJob("REMOVE-x", time.Hour, func() {})
	close(release)
	time.Sleep(50 * time.Millisecond)
	if !js.Pending("REMOVE-x") {
		t.Fatal("the new job was forgotten when the old one ended")
	}
	js.RemoveJob("REMOVE-x")
}

func TestLoadingDoesNotReplaceAJobAlreadyScheduled(t *testing.T) {
	file := filepath.Join(t.TempDir(), "jobfile")
	saved := NewJobScheduler()
	saved.AddJob("REMOVE-a", time.Hour, func() {})
	saved.AddJob("REMOVE-b", time.Hour, func() {})
	if err := saved.SaveJobsToFile(file); err != nil {
		t.Fatal(err)
	}
	js := NewJobScheduler()
	js.AddJob("REMOVE-a", 2*time.Hour, func() {})
	mine := js.Jobs["REMOVE-a"]
	if err := js.LoadJobsFromFile(file, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if js.Jobs["REMOVE-a"] != mine || !js.Pending("REMOVE-b") || len(js.Jobs) != 2 {
		t.Fatalf("after loading: %+v", js.Jobs)
	}
}

func TestTheJobFileIsWrittenWholeAndKeptCurrent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "jobfile")
	js := NewJobScheduler()
	js.AddJob("REMOVE-a", time.Hour, func() {})
	if err := js.SaveJobsToFile(file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file + ".tmp"); err == nil {
		t.Error("the file it was written beside was left")
	}

	// saved by itself, so that a hard stop loses only the last interval
	saved := JobSaveInterval
	JobSaveInterval = 20 * time.Millisecond
	defer func() { JobSaveInterval = saved }()
	os.Remove(file)
	stop := js.StartJobSaver(file)
	defer stop()
	waitFor(t, "the waiting jobs were saved without a stop", func() bool {
		next := NewJobScheduler()
		return next.LoadJobsFromFile(file, func(string) {}) == nil && next.Pending("REMOVE-a")
	})
	js.AddJob("REMOVE-b", time.Hour, func() {})
	waitFor(t, "a job added later was saved too", func() bool {
		next := NewJobScheduler()
		return next.LoadJobsFromFile(file, func(string) {}) == nil && next.Pending("REMOVE-b")
	})
}

func TestForgottenGuestFoldersAreSweptAndTheOthersAreNot(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	make := func(name string, age time.Duration) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0777); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "src", "main.c")
		os.WriteFile(file, []byte("x"), 0666)
		past := now.Add(-age)
		for _, p := range []string{file, filepath.Join(dir, "src"), dir} {
			os.Chtimes(p, past, past)
		}
		return dir
	}
	forgotten := make("guest-111", 3*time.Hour)
	waiting := make("guest-222", 3*time.Hour)  // a job is waiting for it
	fresh := make("guest-333", 10*time.Minute) // in use a moment ago
	signedIn := make("anna0123", 5*time.Hour)  // not a guest's: never touched here
	busy := make("guest-444", 3*time.Hour)     // the folder is old, a file in it is not
	os.Chtimes(filepath.Join(busy, "src", "main.c"), now, now)
	os.WriteFile(filepath.Join(root, "guest-file"), nil, 0666) // not a folder

	var removed []string
	n := SweepGuestHomes(root, func(job string) bool { return job == REMOVE_JOB_KEY+waiting }, GuestSweepAge, now, func(dir string) {
		removed = append(removed, dir)
		os.RemoveAll(dir)
	})
	if n != 1 || len(removed) != 1 || removed[0] != forgotten {
		t.Fatalf("removed %d: %v", n, removed)
	}
	for _, kept := range []string{waiting, fresh, signedIn, busy} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}
}
