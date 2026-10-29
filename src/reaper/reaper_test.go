package reaper

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestParseStatReadsTheStateAndTheParentWhateverTheNameIs(t *testing.T) {
	for line, want := range map[string]struct {
		state byte
		ppid  int
	}{
		"127 (test.c.o) Z 1 127 127 0 -1 4194308 0":          {'Z', 1},
		"5 (a b) c (d)) S 77 5 5 0":                          {'S', 77},
		"1 (gotty) S 0 1 1 0 -1 4194560":                     {'S', 0},
		"940 ((sd-pam)) R 938 938 938 0 -1 4194560 21 0 0 0": {'R', 938},
	} {
		state, ppid, ok := parseStat(line)
		if !ok || state != want.state || ppid != want.ppid {
			t.Errorf("%q: %c %d %v, want %c %d", line, state, ppid, ok, want.state, want.ppid)
		}
	}
	for _, bad := range []string{"", "no brackets", "1 (x)", "1 (x) ZZ 1", "1 (x) Z notanumber"} {
		if _, _, ok := parseStat(bad); ok {
			t.Errorf("%q was read", bad)
		}
	}
}

func TestAZombieIsReapedOnlyWhenItWasThereAtTheLastSweepToo(t *testing.T) {
	s := &sweeper{seen: map[int]bool{}}
	var asked []int
	reapOne := func(pid int) bool { asked = append(asked, pid); return true }

	// first sight: only remembered, so a child whose own Wait is about to read it is left alone
	if got := s.sweep([]int{10, 11}, reapOne); len(got) != 0 || len(asked) != 0 {
		t.Fatalf("reaped at first sight: %v %v", got, asked)
	}
	// 11 is still there, 10 was reaped by its owner meanwhile, 12 is new
	got := s.sweep([]int{11, 12}, reapOne)
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("second sweep reaped %v, want [11]", got)
	}
	// 12 stays one more sweep; a pid that could not be reaped is not claimed
	asked = nil
	if got := s.sweep([]int{12}, func(int) bool { return false }); len(got) != 0 {
		t.Errorf("claimed a reap that failed: %v", got)
	}
	// a pid that went away and came back (the number was reused) starts again at first sight
	s.sweep(nil, reapOne)
	if got := s.sweep([]int{12}, reapOne); len(got) != 0 {
		t.Errorf("a reused pid was reaped at once: %v", got)
	}
}

func TestARealOrphanZombieIsFoundAndReaped(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	// a child that ends and is never waited for is a zombie of this test process
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		found := false
		for _, z := range zombiesOf(os.Getpid()) {
			found = found || z == pid
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never showed as a zombie of this process", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := &sweeper{seen: map[int]bool{}}
	s.sweep(zombiesOf(os.Getpid()), reap)
	got := s.sweep(zombiesOf(os.Getpid()), reap)
	reapedIt := false
	for _, p := range got {
		reapedIt = reapedIt || p == pid
	}
	if !reapedIt {
		t.Fatalf("the zombie %d was not reaped: %v", pid, got)
	}
	for _, z := range zombiesOf(os.Getpid()) {
		if z == pid {
			t.Errorf("%d is still a zombie", pid)
		}
	}
}
