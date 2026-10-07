//go:build linux
// +build linux

package wsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A gateway on a host that keeps no files between deploys starts on an empty
// disk every time: no copies of the homes, no records, and no sessions until
// the first request. Its workers still have everything. These tests are about
// that meeting: the worker's files must reach the gateway again, and nothing
// that decides over a worker's copy may take the empty gateway for a copy.

// workerWithAHome returns a worker that has synchronized a home with a
// gateway before: it has the files and a record of them.
func workerWithAHome(t *testing.T) *nodes {
	t.Helper()
	n := newNodes(t, ownedBy("worker-1", hm))
	n.ws.put(hm, "main.c", "int main(){}")
	n.ws.put(hm, "notes/todo.txt", "write the tests")
	n.connect()
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
	n.disconnect()
	if len(n.wm.Homes("gateway")) != 1 {
		t.Fatal("the worker has no record of the home it synchronized")
	}
	return n
}

// emptyGateway gives the worker a gateway that starts on an empty disk. The
// worker's disk stays as it is.
func (n *nodes) emptyGateway(owner func(string) (string, bool)) *nodes {
	n.disconnect()
	n2 := &nodes{t: n.t, gs: newSite(n.t), ws: n.ws, owner: owner, drops: make(chan string, 8)}
	n2.gm = n2.newManager(n2.gs, "gateway", true)
	n2.wm = n2.newManager(n2.ws, "worker-1", false)
	return n2
}

// refusedAtReconnect connects the worker to an empty gateway that has no
// session for the home yet, and returns once the worker's offer of the home
// has been refused. placed makes the gateway name the worker as the owner.
func refusedAtReconnect(t *testing.T) (n *nodes, placed func()) {
	t.Helper()
	var has int32
	n = workerWithAHome(t).emptyGateway(func(home string) (string, bool) {
		if atomic.LoadInt32(&has) == 1 {
			return ownedBy("worker-1", hm)(home)
		}
		return "", false
	})
	n.connect()
	waitFor(t, "the worker to report ready", func() bool { return n.readyCount() == 1 })
	if exists(n.gs.base, hm) || n.wm.Synced("gateway", hm) {
		t.Fatal("a home with no session and no record was taken from the worker")
	}
	return n, func() { atomic.StoreInt32(&has, 1) }
}

func within(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func TestManagerAHomeRefusedAtReconnectIsTakenOnceItsSessionExists(t *testing.T) {
	n, placed := refusedAtReconnect(t)

	// The user's first request: the gateway places the session on the worker
	// that has the files, and the worker asks for the home again.
	placed()
	if err := n.ensure(hm); err != nil {
		t.Fatalf("the refusal outlasted its reason: %v", err)
	}
	if n.gs.read(hm, "main.c") != "int main(){}" || n.gs.read(hm, "notes/todo.txt") != "write the tests" {
		t.Fatal("the worker's files did not reach the gateway")
	}
	if !sameHomes(n.gs, n.ws, hm) {
		t.Fatalf("the homes differ:\n gateway %s\n worker  %s", describe(n.gs.scan(hm)), describe(n.ws.scan(hm)))
	}
	// From here on the home is kept in step like any other.
	n.ws.put(hm, "out.txt", "output of a program")
	waitFor(t, "a later change to reach the gateway", func() bool { return n.gs.has(hm, "out.txt") })
}

func TestManagerARefusedHomeIsRefusedAgainWhileTheReasonStands(t *testing.T) {
	n, _ := refusedAtReconnect(t)
	for i := 0; i < 2; i++ {
		var refused *RefusedError
		if err := n.ensure(hm); !errors.As(err, &refused) {
			t.Fatalf("attempt %d: %v, want a refusal", i+1, err)
		}
	}
	if exists(n.gs.base, hm) {
		t.Fatal("the gateway created the home it refused")
	}
	if !n.ws.has(hm, "main.c") || len(n.wm.Homes("gateway")) != 1 {
		t.Fatal("a refusal touched the worker's files or its record")
	}
}

// The session ran on the gateway for a while, in a home that was empty there,
// and is then given to the worker that has the user's older files: the
// gateway sends its copy. The worker must answer although it was refused this
// very home when it connected. Nothing is deleted, and where both have a
// file the later one is kept.
func TestManagerAGatewayCanSendAHomeItRefusedBefore(t *testing.T) {
	n, _ := refusedAtReconnect(t)
	n.gs.put(hm, "new.txt", "written on the gateway")
	n.gs.put(hm, "main.c", "int main(){ return 1; }") // later than the worker's

	ctx, cancel := within(20 * time.Second)
	defer cancel()
	if err := n.gm.EnsureHome(ctx, "worker-1", hm); err != nil {
		t.Fatalf("the gateway could not send the home: %v", err)
	}
	if n.ws.read(hm, "new.txt") != "written on the gateway" || n.gs.read(hm, "notes/todo.txt") != "write the tests" {
		t.Fatal("the reconcile was not the union of both sides")
	}
	if got := n.ws.read(hm, "main.c"); got != "int main(){ return 1; }" {
		t.Fatalf("main.c on the worker = %q, want the later version", got)
	}
	if !sameHomes(n.gs, n.ws, hm) {
		t.Fatalf("the homes differ:\n gateway %s\n worker  %s", describe(n.gs.scan(hm)), describe(n.ws.scan(hm)))
	}
	// The worker's own wait for the home is over too.
	if err := n.ensure(hm); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSecureCopyBringsAConnectedPeerInStepFirst(t *testing.T) {
	n, _ := refusedAtReconnect(t)
	ctx, cancel := within(20 * time.Second)
	defer cancel()
	if err := n.gm.SecureCopy(ctx, "worker-1", hm); err != nil {
		t.Fatal(err)
	}
	if !sameHomes(n.gs, n.ws, hm) || !n.gs.has(hm, "main.c") {
		t.Fatal("the gateway's copy is not in step with the worker's")
	}
}

func TestManagerSecureCopyForAPeerThatIsAway(t *testing.T) {
	n := workerWithAHome(t) // in step with this gateway, then disconnected
	ctx, cancel := within(5 * time.Second)
	defer cancel()
	check := func(what string, home string, want error) {
		t.Helper()
		if err := n.gm.SecureCopy(ctx, "worker-1", home); !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", what, err, want)
		}
	}
	check("a complete reconcile is on record", hm, nil)
	check("a home that was never synchronized", "guest-other", ErrNoCopy)

	// The first reconcile was cut short: the record lists what had arrived.
	store := RecordStore{Dir: n.gs.state}
	rec, err := store.Load("worker-1", hm)
	if err != nil || !rec.Valid || rec.Partial {
		t.Fatalf("record after a complete reconcile: %+v %v", rec, err)
	}
	rec.Partial = true
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	check("a partial record", hm, ErrNoCopy)
	rec.Partial = false
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	check("the record is complete again", hm, nil)

	// The record outlived the files.
	if err := os.RemoveAll(n.gs.root(hm)); err != nil {
		t.Fatal(err)
	}
	check("the files are gone", hm, ErrNoCopy)

	// And a gateway that started on an empty disk has neither.
	n2 := n.emptyGateway(func(string) (string, bool) { return "", false })
	if err := n2.gm.SecureCopy(ctx, "worker-1", hm); !errors.Is(err, ErrNoCopy) {
		t.Fatalf("an empty gateway: %v", err)
	}
}

func TestManagerDropMovedNeverGivesUpTheOnlyCopy(t *testing.T) {
	n := workerWithAHome(t).emptyGateway(func(string) (string, bool) { return "", false })
	ctx, cancel := within(20 * time.Second)
	defer cancel()

	// The worker is away and the gateway has nothing of the home.
	if err := n.gm.DropMoved(ctx, "worker-1", hm); !errors.Is(err, ErrNoCopy) {
		t.Fatalf("DropMoved with no copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(n.gs.state, "worker-1", hm+".drop")); err == nil {
		t.Fatal("a drop order was left for the worker")
	}
	n.connect()
	waitFor(t, "the worker to report ready", func() bool { return n.readyCount() == 1 })
	select {
	case home := <-n.drops:
		t.Fatalf("the worker was told to delete %s, the only copy", home)
	case <-time.After(300 * time.Millisecond):
	}
	if !n.ws.has(hm, "main.c") || len(n.wm.Homes("gateway")) != 1 {
		t.Fatal("the worker's files or record are gone")
	}

	// Connected: the gateway takes its copy first, and only then is the
	// worker told.
	if err := n.gm.DropMoved(ctx, "worker-1", hm); err != nil {
		t.Fatal(err)
	}
	select {
	case home := <-n.drops:
		if home != hm {
			t.Fatalf("dropped %q", home)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker was never told to drop the home")
	}
	if n.gs.read(hm, "main.c") != "int main(){}" || n.gs.read(hm, "notes/todo.txt") != "write the tests" {
		t.Fatal("the worker was told to drop the home before the gateway had its files")
	}
}

// A record is written a moment after each change, so one exists before the
// first reconcile is complete. It must say so: until then the copy on this
// side is only part of the peer's.
func TestLinkARecordIsPartialUntilTheFirstReconcileIsComplete(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	ws.put(hm, "a.txt", "a")
	ws.put(hm, "b.txt", "b")
	release := make(chan struct{})
	c := connect(t, gs, ws, tweaks{})
	c.wk.hookBeforeOpen = func(_, rel string) {
		if rel == "b.txt" {
			<-release // the worker stops halfway through sending the home
		}
	}
	if err := c.wk.Attach(hm); err != nil {
		t.Fatal(err)
	}
	store := RecordStore{Dir: gs.state}
	waitFor(t, "the gateway to record the first file", func() bool {
		rec, err := store.Load("worker-1", hm)
		return err == nil && rec.Valid && len(rec.Entries) == 1
	})
	rec, _ := store.Load("worker-1", hm)
	if !rec.Partial {
		t.Fatal("a record written halfway through the first reconcile is not marked partial")
	}

	close(release)
	ctx, cancel := within(20 * time.Second)
	defer cancel()
	if err := c.wk.WaitSynced(ctx, hm); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the gateway's record to be complete", func() bool {
		rec, err := store.Load("worker-1", hm)
		return err == nil && !rec.Partial && len(rec.Entries) == 2
	})
	w, err := (RecordStore{Dir: ws.state}).Load("gateway", hm)
	waitFor(t, "the worker's record to be complete", func() bool {
		w, err = (RecordStore{Dir: ws.state}).Load("gateway", hm)
		return err == nil && w.Valid && !w.Partial
	})
}
