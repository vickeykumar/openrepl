//go:build linux
// +build linux

package wsync

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// nodes is a gateway manager and a worker manager that can be connected and
// disconnected, with the machines' disks kept between connections.
type nodes struct {
	t      *testing.T
	gs, ws *site
	gm, wm *Manager
	owner  func(string) (string, bool)
	ready  int32
	drops  chan string

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	gwErr  error
	wkErr  error
}

func newNodes(t *testing.T, owner func(string) (string, bool)) *nodes {
	n := &nodes{t: t, gs: newSite(t), ws: newSite(t), owner: owner, drops: make(chan string, 8)}
	n.gm = n.newManager(n.gs, "gateway", true)
	n.wm = n.newManager(n.ws, "worker-1", false)
	return n
}

func (n *nodes) newManager(s *site, node string, isGW bool) *Manager {
	m, err := NewManager(ManagerConfig{
		Node: node, IsGateway: isGW, BaseDir: s.base, StateDir: s.state,
		OwnerOf: n.owner, OnDrop: func(home string) { n.drops <- home },
		Debounce: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond,
		Logf: n.t.Logf,
	})
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(m.Close)
	return m
}

// connect joins the two managers; readyTimeout, if set, is the worker's.
func (n *nodes) connect() {
	n.t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.cancel = cancel
	n.mu.Unlock()
	n.wg.Add(2)
	go func() { defer n.wg.Done(); n.gwErr = n.gm.Serve(ctx, "worker-1", a, nil) }()
	go func() {
		defer n.wg.Done()
		n.wkErr = n.wm.Serve(ctx, "gateway", b, func() { atomic.AddInt32(&n.ready, 1) })
	}()
	n.t.Cleanup(n.disconnect)
	waitFor(n.t, "both sides to be connected", func() bool { return n.gm.Connected("worker-1") && n.wm.Connected("gateway") })
}

func (n *nodes) disconnect() {
	n.mu.Lock()
	cancel := n.cancel
	n.cancel = nil
	n.mu.Unlock()
	if cancel != nil {
		cancel()
		n.wg.Wait()
	}
}

func (n *nodes) ensure(home string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return n.wm.EnsureHome(ctx, "gateway", home)
}

func (n *nodes) readyCount() int { return int(atomic.LoadInt32(&n.ready)) }

func ownedBy(worker string, homes ...string) func(string) (string, bool) {
	return func(home string) (string, bool) {
		for _, h := range homes {
			if h == home {
				return worker, true
			}
		}
		return "", false
	}
}

func TestManagerWorkerHomeIsSynchronizedBothWaysIncludingProgramOutput(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", hm))
	n.ws.put(hm, "main.c", "int main(){}")
	n.gs.put(hm, "upload.txt", "uploaded on the gateway")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	if n.gs.read(hm, "main.c") != "int main(){}" || n.ws.read(hm, "upload.txt") != "uploaded on the gateway" {
		t.Fatal("the first reconcile did not carry both sides' files")
	}

	// A program on the worker: gcc, redirection, rm, mv.
	root := n.ws.root(hm)
	put(t, root, "main.o", "object")
	put(t, root, "output.txt", "result of ./main")
	os.Remove(filepath.Join(root, "main.c"))
	os.MkdirAll(filepath.Join(root, "results"), 0755)
	os.Rename(filepath.Join(root, "output.txt"), filepath.Join(root, "results", "out.txt"))
	os.Rename(filepath.Join(root, "main.o"), filepath.Join(root, "results", "main.o"))
	waitFor(t, "the program's files on the gateway", func() bool {
		return n.gs.has(hm, "results/main.o") && n.gs.has(hm, "results/out.txt") && !n.gs.has(hm, "main.c") && !n.gs.has(hm, "main.o") && !n.gs.has(hm, "output.txt")
	})

	// The gateway side: a save through the file API.
	put(t, n.gs.root(hm), "upload.txt", "saved through the file browser")
	waitFor(t, "the save on the worker", func() bool { return n.ws.read(hm, "upload.txt") == "saved through the file browser" })
	if !sameHomes(n.gs, n.ws, hm) {
		time.Sleep(500 * time.Millisecond)
		if !sameHomes(n.gs, n.ws, hm) {
			t.Fatalf("not equal:\n gateway %s\n worker  %s", describe(n.gs.scan(hm)), describe(n.ws.scan(hm)))
		}
	}
}

func TestManagerGatewayRefusesAWorkerThatDoesNotOwnTheHome(t *testing.T) {
	n := newNodes(t, ownedBy("worker-2", hm)) // another worker owns it
	n.ws.put(hm, "x.txt", "x")
	n.connect()
	err := n.ensure(hm)
	if err == nil || !strings.Contains(err.Error(), "belongs to worker-2") {
		t.Fatalf("got %v, want a refusal that names the owner", err)
	}
	if exists(n.gs.base, hm) {
		t.Fatal("the gateway created a home for a worker that does not own it")
	}
}

func TestManagerUnknownOwnerIsAcceptedOnlyIfThisWorkerSyncedItBefore(t *testing.T) {
	// The gateway has just restarted and knows no owners.
	n := newNodes(t, func(string) (string, bool) { return "", false })
	n.ws.put(hm, "a.txt", "a")
	n.connect()
	if err := n.ensure(hm); err == nil {
		t.Fatal("a home with no known owner and no record was accepted from a worker")
	}

	// Once the gateway has a record of the worker having synced it, it is
	// accepted again. (Simulated by a record written by an earlier run.)
	n.disconnect()
	rec := NewRecord(hm, "worker-1")
	rec.Valid = true
	if err := (RecordStore{Dir: n.gs.state}).Save(rec); err != nil {
		t.Fatal(err)
	}
	n2 := &nodes{t: t, gs: n.gs, ws: n.ws, owner: n.owner, drops: n.drops}
	n2.gm = n2.newManager(n2.gs, "gateway", true)
	n2.wm = n2.newManager(n2.ws, "worker-1", false)
	n2.connect()
	if err := n2.ensure(hm); err != nil {
		t.Fatalf("a worker re-claiming a home it synchronized before was refused: %v", err)
	}
	if n2.gs.read(hm, "a.txt") != "a" {
		t.Fatal("the home was not synchronized")
	}
}

func TestManagerWorkerReconcilesItsHomesOnReconnectBeforeReady(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", "guest-a", "guest-b"))
	n.ws.put("guest-a", "a.txt", "a")
	n.ws.put("guest-b", "b.txt", "b")
	n.connect()
	waitFor(t, "ready with no homes yet", func() bool { return n.readyCount() == 1 })
	for _, h := range []string{"guest-a", "guest-b"} {
		if err := n.ensure(h); err != nil {
			t.Fatal(err)
		}
	}
	n.disconnect()

	// While apart: the worker's program changes files, the gateway receives uploads.
	n.ws.put("guest-a", "a.txt", "a, edited by a program")
	n.ws.put("guest-a", "out.txt", "output")
	os.Remove(filepath.Join(n.ws.root("guest-b"), "b.txt"))
	n.gs.put("guest-b", "upload.txt", "uploaded while the worker was away")
	n.gs.put("guest-a", "note.txt", "saved on the gateway")

	n.connect()
	// Ready is reported only after both homes were reconciled.
	waitFor(t, "ready after the reconnect", func() bool { return n.readyCount() == 2 })
	for _, h := range []string{"guest-a", "guest-b"} {
		if !sameHomes(n.gs, n.ws, h) {
			t.Fatalf("%s: not reconciled when ready was reported:\n gateway %s\n worker  %s", h, describe(n.gs.scan(h)), describe(n.ws.scan(h)))
		}
	}
	if n.gs.read("guest-a", "a.txt") != "a, edited by a program" || n.ws.read("guest-a", "note.txt") != "saved on the gateway" ||
		n.gs.has("guest-b", "b.txt") || n.ws.read("guest-b", "upload.txt") != "uploaded while the worker was away" {
		t.Fatalf("the offline changes were not reconciled: gateway %s / worker %s", describe(n.gs.scan("guest-a")), describe(n.ws.scan("guest-b")))
	}
}

func TestManagerGatewaySendsAHomeToAWorkerThatIsToRunItsSession(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", hm))
	n.gs.put(hm, "project/main.py", "print(1)")
	n.gs.put(hm, "notes.txt", "n")
	n.connect()
	// The gateway initiates: copy on placement.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := n.gm.EnsureHome(ctx, "worker-1", hm); err != nil {
		t.Fatal(err)
	}
	if n.ws.read(hm, "project/main.py") != "print(1)" || n.ws.read(hm, "notes.txt") != "n" {
		t.Fatal("the worker did not receive the home")
	}
	// And it keeps both copies in step from then on.
	put(t, n.ws.root(hm), "project/out.txt", "o")
	waitFor(t, "the worker's output on the gateway", func() bool { return n.gs.has(hm, "project/out.txt") })
}

func TestManagerDropReachesTheWorker(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", hm))
	n.ws.put(hm, "a", "a")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	// The worker is ready as soon as its own side is in step; the gateway
	// writes its record a moment later.
	waitFor(t, "the gateway to keep a record", func() bool { return len(n.gm.Homes("worker-1")) == 1 })
	if err := n.gm.Drop("worker-1", hm); err != nil {
		t.Fatal(err)
	}
	select {
	case home := <-n.drops:
		if home != hm {
			t.Fatalf("dropped %q", home)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker was not told to drop the home")
	}
	if len(n.gm.Homes("worker-1")) != 0 {
		t.Fatal("the gateway's record of a dropped home was not removed")
	}
	if err := n.gm.Drop("worker-1", "../x"); err == nil {
		t.Fatal("Drop accepted a home name that is a path")
	}
}

func TestManagerReconnectReplacesTheOldConversation(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", hm))
	n.ws.put(hm, "a", "a")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	first := n.gm.link("worker-1")

	// The worker reconnects before the gateway noticed the old connection
	// is dead: the old conversation is closed, the new one works.
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.gm.Serve(ctx, "worker-1", a, nil)
	go n.wm.Serve(ctx, "gateway", b, nil)
	waitFor(t, "the old conversation to end", func() bool {
		select {
		case <-first.Done():
			return true
		default:
			return false
		}
	})
	waitFor(t, "the new conversation", func() bool { l := n.gm.link("worker-1"); return l != nil && l != first })
	put(t, n.ws.root(hm), "b", "b")
	waitFor(t, "a change over the new conversation", func() bool {
		return n.gm.link("worker-1") != nil && n.gs.has(hm, "b") || sameHomes(n.gs, n.ws, hm)
	})
}

func TestManagerWorkerGivesUpWhenReconcilingTakesTooLong(t *testing.T) {
	// A gateway that connects but never answers.
	ws := newSite(t)
	rec := NewRecord(hm, "gateway")
	rec.Valid = true
	if err := (RecordStore{Dir: ws.state}).Save(rec); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(ws.root(hm), 0755)
	wm, err := NewManager(ManagerConfig{Node: "worker-1", BaseDir: ws.base, StateDir: ws.state, ReadyTimeout: 300 * time.Millisecond, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer wm.Close()
	a, b := net.Pipe()
	defer a.Close()
	go func() { // reads and discards, never replies
		buf := make([]byte, 4096)
		for {
			if _, err := a.Read(buf); err != nil {
				return
			}
		}
	}()
	readyCalled := int32(0)
	done := make(chan error, 1)
	go func() {
		done <- wm.Serve(context.Background(), "gateway", b, func() { atomic.AddInt32(&readyCalled, 1) })
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker kept waiting for a gateway that never answers")
	}
	if atomic.LoadInt32(&readyCalled) != 0 {
		t.Fatal("ready was reported although the homes were never reconciled")
	}
}

func TestManagerRejectsPeerNamesThatArePaths(t *testing.T) {
	s := newSite(t)
	m, err := NewManager(ManagerConfig{Node: "gateway", IsGateway: true, BaseDir: s.base, StateDir: s.state})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, b := net.Pipe()
	defer b.Close()
	if err := m.Serve(context.Background(), "../evil", a, nil); err == nil {
		t.Fatal("a peer name with .. was accepted")
	}
	if m.Homes("../evil") != nil {
		t.Fatal("Homes accepted a peer name that is a path")
	}
}

func TestManagerDropOrderWaitsForAWorkerThatIsAway(t *testing.T) {
	// The gateway drops a home because its session ended or moved, so from
	// then on the gateway no longer names the worker as its owner.
	var expired int32
	n := newNodes(t, func(home string) (string, bool) {
		if atomic.LoadInt32(&expired) == 1 {
			return "", false
		}
		return ownedBy("worker-1", hm)(home)
	})
	n.ws.put(hm, "a.txt", "a")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	n.disconnect()

	// The guest expires on the gateway while the worker is away.
	atomic.StoreInt32(&expired, 1)
	if err := n.gm.Drop("worker-1", hm); err != nil {
		t.Fatal(err)
	}
	if len(n.gm.Homes("worker-1")) != 0 {
		t.Fatal("the gateway still has a record of the dropped home")
	}
	select {
	case <-n.drops:
		t.Fatal("the worker was told while it was away")
	default:
	}

	// When the worker returns it is told, and it still becomes ready: the
	// gateway's refusal of the home it remembers is not a failure.
	n.connect()
	select {
	case home := <-n.drops:
		if home != hm {
			t.Fatalf("dropped %q", home)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pending drop was never delivered")
	}
	waitFor(t, "the worker to report ready", func() bool { return n.readyCount() >= 1 })
	waitFor(t, "the worker's record to be removed", func() bool { return len(n.wm.Homes("gateway")) == 0 })
	if _, err := os.Stat(filepath.Join(n.gs.state, "worker-1", hm+".drop")); err == nil {
		t.Fatal("the pending drop order was not cleared once delivered")
	}
	// A later connection does not send it again.
	n.disconnect()
	n.connect()
	select {
	case home := <-n.drops:
		t.Fatalf("the drop of %s was delivered twice", home)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestManagerADroppedHomeStopsSynchronizing(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", hm))
	n.ws.put(hm, "a.txt", "a")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	if err := n.gm.Drop("worker-1", hm); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker to be told", func() bool { return len(n.drops) == 1 })

	// The files are removed by whoever owns the node (OnDrop); here we only
	// check that nothing flows any more, in either direction.
	put(t, n.ws.root(hm), "after.txt", "x")
	put(t, n.gs.root(hm), "after-gateway.txt", "y")
	time.Sleep(700 * time.Millisecond)
	if n.gs.has(hm, "after.txt") || n.ws.has(hm, "after-gateway.txt") {
		t.Fatal("a dropped home kept synchronizing")
	}
}

func TestLinkDetachForgetsAHomeAndRefusesItsChanges(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "a", "a")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	watchFor(t, ws, c.wk, hm)

	c.gw.Detach(hm)
	if c.gw.home(hm) != nil || c.gw.Synced(hm) {
		t.Fatal("the home is still attached on the gateway")
	}
	ws.put(hm, "late.txt", "l")
	time.Sleep(600 * time.Millisecond)
	if gs.has(hm, "late.txt") {
		t.Fatal("a change was applied for a detached home")
	}
	// The worker's side learns that its changes are not wanted: the sender
	// retries later, which is harmless.
	if c.wk.Stats()["recv:ack"] == 0 {
		t.Fatal("the worker was never answered")
	}
	c.gw.Detach(hm) // twice is fine
}

func TestManagerStaleHomesAndHasRecord(t *testing.T) {
	n := newNodes(t, ownedBy("worker-1", "guest-old", "guest-new"))
	n.ws.put("guest-old", "a", "a")
	n.ws.put("guest-new", "b", "b")
	n.connect()
	for _, h := range []string{"guest-old", "guest-new"} {
		if err := n.ensure(h); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "both records on disk", func() bool { return len(n.gm.Homes("worker-1")) == 2 })
	if !n.gm.HasRecord("guest-old") || n.gm.HasRecord("guest-unknown") {
		t.Fatal("HasRecord is wrong")
	}
	// Age one record.
	old := filepath.Join(n.gs.state, "worker-1", "guest-old.json")
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	stale := n.gm.StaleHomes(time.Hour)
	if len(stale) != 1 || stale[0].Home != "guest-old" || stale[0].Peer != "worker-1" || stale[0].Age < 2*time.Hour {
		t.Fatalf("stale = %+v", stale)
	}
	if got := n.gm.StaleHomes(5 * time.Hour); len(got) != 0 {
		t.Fatalf("stale with a 5 hour limit = %+v", got)
	}
}
