//go:build linux
// +build linux

package wsync

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type events struct {
	mu  sync.Mutex
	got map[string]int
}

func newWatcherWithEvents(t *testing.T, base string) (*Watcher, *events) {
	ev := &events{got: map[string]int{}}
	w, err := NewWatcher(base, func(home, rel string) {
		ev.mu.Lock()
		ev.got[home+"|"+rel]++
		ev.mu.Unlock()
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, ev
}

func (e *events) has(home, rel string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.got[home+"|"+rel] > 0
}

func (e *events) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for k := range e.got {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func waitEvent(t *testing.T, e *events, home, rel string) {
	t.Helper()
	waitFor(t, "an event for "+rel, func() bool { return e.has(home, rel) })
}

func TestWatcherReportsCreateModifyDeleteAndRename(t *testing.T) {
	s := newSite(t)
	s.put(hm, "keep.txt", "k")
	w, ev := newWatcherWithEvents(t, s.base)
	if err := w.AddHome(hm); err != nil {
		t.Fatal(err)
	}
	root := s.root(hm)

	put(t, root, "new.txt", "n")
	waitEvent(t, ev, hm, "new.txt")

	put(t, root, "keep.txt", "modified")
	waitEvent(t, ev, hm, "keep.txt")

	os.Remove(filepath.Join(root, "new.txt"))
	waitFor(t, "a second event for new.txt", func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return ev.got[hm+"|new.txt"] >= 2 })

	os.Rename(filepath.Join(root, "keep.txt"), filepath.Join(root, "renamed.txt"))
	waitEvent(t, ev, hm, "renamed.txt") // the new name
	os.Chmod(filepath.Join(root, "renamed.txt"), 0755)
	waitFor(t, "a chmod event", func() bool { ev.mu.Lock(); defer ev.mu.Unlock(); return ev.got[hm+"|renamed.txt"] >= 2 })
}

func TestWatcherPicksUpFilesCreatedInANewDirectoryRightAway(t *testing.T) {
	s := newSite(t)
	s.put(hm, "x", "x")
	w, ev := newWatcherWithEvents(t, s.base)
	if err := w.AddHome(hm); err != nil {
		t.Fatal(err)
	}
	root := s.root(hm)

	// A whole tree appears at once, as when an archive is unpacked: files
	// land in directories before a watch on them can exist.
	for i := 0; i < 20; i++ {
		put(t, root, fmt.Sprintf("tree/d%d/sub/f.txt", i), "x")
	}
	for i := 0; i < 20; i++ {
		waitEvent(t, ev, hm, fmt.Sprintf("tree/d%d/sub/f.txt", i))
	}
	// And the directories are watched: later changes inside are seen.
	put(t, root, "tree/d7/sub/later.txt", "l")
	waitEvent(t, ev, hm, "tree/d7/sub/later.txt")
}

func TestWatcherIgnoresTemporaryFilesAndOtherHomes(t *testing.T) {
	s := newSite(t)
	s.put(hm, "a", "a")
	s.put("guest-other", "b", "b")
	w, ev := newWatcherWithEvents(t, s.base)
	if err := w.AddHome(hm); err != nil {
		t.Fatal(err)
	}
	put(t, s.root(hm), TempPrefix+"partial", "x")
	put(t, s.root(hm), "d/"+TempPrefix+"nested", "x")
	put(t, s.root("guest-other"), "c", "c")
	put(t, s.root(hm), "marker", "m")
	waitEvent(t, ev, hm, "marker")
	for _, e := range ev.list() {
		if strings.Contains(e, TempPrefix) || strings.HasPrefix(e, "guest-other") {
			t.Fatalf("unexpected event %s (all: %v)", e, ev.list())
		}
	}
}

func TestWatcherDoesNotWatchThroughALink(t *testing.T) {
	s := newSite(t)
	outside := t.TempDir()
	s.put(hm, "a", "a")
	os.Symlink(outside, filepath.Join(s.root(hm), "out"))
	w, ev := newWatcherWithEvents(t, s.base)
	if err := w.AddHome(hm); err != nil {
		t.Fatal(err)
	}
	put(t, outside, "x.txt", "x")
	put(t, s.root(hm), "marker", "m")
	waitEvent(t, ev, hm, "marker")
	time.Sleep(100 * time.Millisecond)
	for _, e := range ev.list() {
		if strings.Contains(e, "x.txt") {
			t.Fatalf("an event from outside the home: %v", ev.list())
		}
	}
}

func TestWatcherRemoveHomeStopsEvents(t *testing.T) {
	s := newSite(t)
	s.put(hm, "a", "a")
	w, ev := newWatcherWithEvents(t, s.base)
	w.AddHome(hm)
	put(t, s.root(hm), "one", "1")
	waitEvent(t, ev, hm, "one")
	w.RemoveHome(hm)
	put(t, s.root(hm), "two", "2")
	time.Sleep(200 * time.Millisecond)
	if ev.has(hm, "two") {
		t.Fatal("events continued after RemoveHome")
	}
	if err := w.AddHome(hm); err != nil { // can be added again
		t.Fatal(err)
	}
	put(t, s.root(hm), "three", "3")
	waitEvent(t, ev, hm, "three")
}

func TestWatcherAddHomeRejectsBadNames(t *testing.T) {
	s := newSite(t)
	w, _ := newWatcherWithEvents(t, s.base)
	for _, n := range []string{"../x", "a/b", "", ".."} {
		if err := w.AddHome(n); err == nil {
			t.Errorf("AddHome(%q) succeeded", n)
		}
	}
	if err := w.AddHome("no-such-home"); err == nil {
		t.Error("AddHome of a home that does not exist succeeded")
	}
}

func TestLinkBurstOfWritesIsSentOnce(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "log.txt", "start")
	c := connect(t, gs, ws, tweaks{gw: func(cfg *Config) { cfg.Debounce = 150 * time.Millisecond; cfg.MaxDelay = 2 * time.Second }})
	c.syncHome(hm)
	watchFor(t, gs, c.gw, hm)

	before := c.gw.Stats()["sent:put"]
	for i := 0; i < 100; i++ {
		put(t, gs.root(hm), "log.txt", strings.Repeat("x", i+1))
		setMtime(t, gs.root(hm), "log.txt", tick())
		time.Sleep(2 * time.Millisecond)
	}
	c.waitEqual(gs, ws, hm)
	sent := c.gw.Stats()["sent:put"] - before
	if sent < 1 || sent > 5 {
		t.Fatalf("100 rapid writes were sent as %d transfers; they must be coalesced", sent)
	}
	if ws.read(hm, "log.txt") != strings.Repeat("x", 100) {
		t.Fatal("the final content did not arrive")
	}
}

func TestLinkMissedEventsAreRecoveredByAFullReconcile(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "a.txt", "a")
	gs.put(hm, "b.txt", "b")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	// No watcher: these changes are never reported, as if events were lost.
	gs.put(hm, "a.txt", "edited")
	os.Remove(filepath.Join(gs.root(hm), "b.txt"))
	gs.put(hm, "c.txt", "new")
	ws.put(hm, "w.txt", "from worker")
	time.Sleep(150 * time.Millisecond)
	if sameHomes(gs, ws, hm) {
		t.Fatal("test set-up: the homes should differ")
	}
	// The watcher reports an overflow: every home is reconciled.
	c.gw.NotifyAll()
	c.waitEqual(gs, ws, hm)
	if ws.read(hm, "a.txt") != "edited" || ws.has(hm, "b.txt") || ws.read(hm, "c.txt") != "new" || gs.read(hm, "w.txt") != "from worker" {
		t.Fatalf("the full reconcile missed something: %s", describe(ws.scan(hm)))
	}
}

func TestLinkRandomEditsOnBothSidesConverge(t *testing.T) {
	seeds := 12
	if testing.Short() {
		seeds = 3
	}
	for seed := 1; seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		gs, ws := newSite(t), newSite(t)
		gn := &node{name: "gateway", root: gs.root(hm), loose: true}
		wn := &node{name: "worker", root: ws.root(hm), loose: true}
		os.MkdirAll(gn.root, 0755)
		os.MkdirAll(wn.root, 0755)
		randomOps(t, rng, gn, 8, map[string]bool{})
		c := connect(t, gs, ws, tweaks{})
		c.syncHome(hm)
		watchFor(t, gs, c.gw, hm)
		watchFor(t, ws, c.wk, hm)

		// Edits on both sides, at the same time, in small bursts.
		var wg sync.WaitGroup
		for _, n := range []*node{gn, wn} {
			wg.Add(1)
			go func(n *node, seed int64) {
				defer wg.Done()
				r := rand.New(rand.NewSource(seed))
				for i := 0; i < 8; i++ {
					randomOps(t, r, n, 1+r.Intn(3), map[string]bool{})
					time.Sleep(time.Duration(r.Intn(40)) * time.Millisecond)
				}
			}(n, int64(seed)*100+int64(len(n.name)))
		}
		wg.Wait()
		// Let it settle, then ask for one more full reconcile, as the system
		// does after a reconnect, so a change that raced is picked up.
		c.waitEqualOrReconcile(gs, ws, hm)
		noTemps(t, gs.root(hm))
		noTemps(t, ws.root(hm))
		c.close()
	}
}

// waitEqualOrReconcile waits for the homes to match, asking for a full
// reconcile if they have not after a while.
func (c *conn) waitEqualOrReconcile(gs, ws *site, home string) {
	c.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	asked := 0
	for time.Now().Before(deadline) {
		if sameHomes(gs, ws, home) {
			time.Sleep(300 * time.Millisecond) // and still equal once everything is quiet
			if sameHomes(gs, ws, home) {
				return
			}
		}
		if asked < 3 && time.Until(deadline) < 15*time.Second-time.Duration(asked)*3*time.Second {
			c.t.Logf("HELP: the homes had not converged on their own; asking for a full reconcile (%d)", asked+1)
			c.gw.NotifyAll()
			asked++
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("the homes never converged:\n gateway %s\n worker  %s", describe(gs.scan(home)), describe(ws.scan(home)))
}

func TestLinkReconcilesPeriodicallyWithoutAnyEvent(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "a.txt", "a")
	every := func(cfg *Config) { cfg.ReconcileEvery = 150 * time.Millisecond }
	c := connect(t, gs, ws, tweaks{gw: every, wk: every})
	c.syncHome(hm)
	// No watcher and no notification at all.
	gs.put(hm, "a.txt", "edited")
	ws.put(hm, "w.txt", "new")
	c.waitEqual(gs, ws, hm)
	if ws.read(hm, "a.txt") != "edited" || gs.read(hm, "w.txt") != "new" {
		t.Fatal("the periodic reconcile did not carry the changes")
	}
}

func TestLinkReconcileOfAHomeInStepTransfersNothing(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	for i := 0; i < 20; i++ {
		gs.put(hm, fmt.Sprintf("f%d.txt", i), strings.Repeat("x", 100+i))
	}
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	before := c.gw.Stats()["sent:put"] + c.wk.Stats()["sent:put"]
	for i := 0; i < 3; i++ {
		c.gw.NotifyAll()
		c.wk.NotifyAll()
		time.Sleep(200 * time.Millisecond)
	}
	after := c.gw.Stats()["sent:put"] + c.wk.Stats()["sent:put"]
	if after != before {
		t.Fatalf("a reconcile of homes that are in step transferred %d file(s)", after-before)
	}
	if st := c.gw.Stats(); st["sent:attach"] < 2 {
		t.Fatalf("the reconcile did not run: %v", st)
	}
}
