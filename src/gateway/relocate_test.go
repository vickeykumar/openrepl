package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// calls records what the router asked of the sync layer.
type calls struct {
	mu       sync.Mutex
	prepared []string // "home@backend"
	moved    []string // "home:from->to"
	failNext int
}

func (c *calls) prepare(ctx context.Context, home, backend string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failNext > 0 {
		c.failNext--
		return errors.New("worker could not take the home")
	}
	c.prepared = append(c.prepared, home+"@"+backend)
	return nil
}

func (c *calls) onMoved(home, from, to string) {
	c.mu.Lock()
	c.moved = append(c.moved, home+":"+from+"->"+to)
	c.mu.Unlock()
}

func (c *calls) snapshot() (prepared, moved []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.prepared...), append([]string(nil), c.moved...)
}

func relocRouter(pins PinStore, c *calls, after time.Duration, picker Picker) *Router {
	return NewRouter(Config{
		Site:          &recorder{},
		PathPrefix:    "/",
		UID:           func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:        []byte("secret"),
		GuestTTL:      time.Hour,
		Pins:          pins,
		Picker:        picker,
		HomeOf:        func(id Identity) string { return "home-" + id.UID + id.GuestID },
		PrepareHome:   c.prepare,
		OnMoved:       c.onMoved,
		RelocateAfter: after,
	})
}

// reset forgets the live context, as if it had expired or the gateway restarted.
func resetContext(rt *Router) { rt.Registry().Release("u:u1") }

func TestPinnedUserWaitsOutABriefWorkerAbsence(t *testing.T) {
	pins := &memPins{}
	c := &calls{}
	a := &named{id: "worker-a", weight: 10}
	b := &named{id: "worker-b", weight: 10}
	rt := relocRouter(pins, c, time.Hour, &countingPicker{id: "worker-a"})
	rt.AddBackend(a)
	rt.AddBackend(b)
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if got, _ := pins.Get("u1"); got != "worker-a" {
		t.Fatalf("pin = %q", got)
	}

	rt.RemoveBackend(a) // worker-a restarts
	resetContext(rt)
	rec := do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "workspace node unavailable") {
		t.Fatalf("within the grace period -> %d %q", rec.Code, rec.Body.String())
	}
	if got, _ := pins.Get("u1"); got != "worker-a" {
		t.Fatalf("the pin moved to %q during a brief absence", got)
	}
	_, moved := c.snapshot()
	if len(moved) != 0 {
		t.Fatalf("moved = %v", moved)
	}

	// It comes back: the user returns to it.
	a2 := &named{id: "worker-a", weight: 10}
	rt.AddBackend(a2)
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK || a2.hits != 1 {
		t.Fatalf("after the worker returned -> %d, hits=%d", code, a2.hits)
	}
}

func TestPinnedUserIsPlacedElsewhereWhenTheirWorkerStaysAway(t *testing.T) {
	pins := &memPins{}
	c := &calls{}
	a := &named{id: "worker-a", weight: 10}
	b := &named{id: "worker-b", weight: 10}
	picker := &countingPicker{id: "worker-a"}
	rt := relocRouter(pins, c, 30*time.Millisecond, picker)
	rt.AddBackend(a)
	rt.AddBackend(b)
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	prepared, _ := c.snapshot()
	if len(prepared) != 1 || prepared[0] != "home-u1@worker-a" {
		t.Fatalf("prepared = %v", prepared)
	}

	rt.RemoveBackend(a)
	resetContext(rt)
	picker.id = "worker-b" // the pool would now choose worker-b
	time.Sleep(60 * time.Millisecond)
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK {
		t.Fatalf("after the grace period -> %d", code)
	}
	if b.hits != 1 {
		t.Fatalf("the user was not placed on worker-b: a=%d b=%d", a.hits, b.hits)
	}
	if got, _ := pins.Get("u1"); got != "worker-b" {
		t.Fatalf("pin = %q, want worker-b", got)
	}
	prepared, moved := c.snapshot()
	if len(moved) != 1 || moved[0] != "home-u1:worker-a->worker-b" {
		t.Fatalf("moved = %v", moved)
	}
	if len(prepared) != 2 || prepared[1] != "home-u1@worker-b" {
		t.Fatalf("the new worker was not sent the home first: %v", prepared)
	}

	// Later requests do not prepare again.
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	do(rt, "GET", "/upload_file", asUser("u1"))
	if prepared, _ := c.snapshot(); len(prepared) != 2 {
		t.Fatalf("the home was prepared again: %v", prepared)
	}
}

func TestDrainingWorkerGivesUpItsPinnedUsersAtOnce(t *testing.T) {
	pins := &memPins{m: map[string]string{"u1": "worker-a"}}
	c := &calls{}
	a := &named{id: "worker-a", weight: 10, state: Draining}
	b := &named{id: "worker-b", weight: 10}
	rt := relocRouter(pins, c, time.Hour, &countingPicker{id: "worker-b"})
	rt.AddBackend(a)
	rt.AddBackend(b)
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK || b.hits != 1 || a.hits != 0 {
		t.Fatalf("-> %d, a=%d b=%d", code, a.hits, b.hits)
	}
	if got, _ := pins.Get("u1"); got != "worker-b" {
		t.Fatalf("pin = %q", got)
	}
	if _, moved := c.snapshot(); len(moved) != 1 {
		t.Fatalf("moved = %v", moved)
	}
}

func TestPinnedUserKeepsWaitingForASyncingWorker(t *testing.T) {
	pins := &memPins{m: map[string]string{"u1": "worker-a"}}
	c := &calls{}
	a := &named{id: "worker-a", weight: 10, state: Syncing}
	b := &named{id: "worker-b", weight: 10}
	rt := relocRouter(pins, c, 0, &countingPicker{id: "worker-b"})
	rt.AddBackend(a)
	rt.AddBackend(b)
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if a.hits != 1 || b.hits != 0 {
		t.Fatalf("a SYNCING worker's user was moved: a=%d b=%d", a.hits, b.hits)
	}
}

func TestAfterAGatewayRestartWorkersGetTheFullGracePeriod(t *testing.T) {
	pins := &memPins{m: map[string]string{"u1": "worker-a"}}
	c := &calls{}
	b := &named{id: "worker-b", weight: 10}
	rt := relocRouter(pins, c, 80*time.Millisecond, &countingPicker{id: "worker-b"})
	rt.AddBackend(b)
	// worker-a has not reconnected yet: the router has never seen it.
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("right after the restart -> %d, want 503", code)
	}
	time.Sleep(120 * time.Millisecond)
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK || b.hits != 1 {
		t.Fatalf("after the grace period -> %d, b=%d", code, b.hits)
	}
}

func TestExistingFilesOnTheGatewayNoLongerPinAUserToIt(t *testing.T) {
	c := &calls{}
	w := &named{id: "worker-1", weight: 10}
	rt := NewRouter(Config{
		Site:              &recorder{},
		PathPrefix:        "/",
		UID:               func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:            []byte("secret"),
		GuestTTL:          time.Hour,
		HasLocalWorkspace: func(uid string) bool { return true },
		HomeOf:            func(id Identity) string { return "home-" + id.UID },
		PrepareHome:       c.prepare,
	})
	rt.AddBackend(w)
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if w.hits != 1 {
		t.Fatal("with workspace sync a user with files on the gateway must still be placed on a worker")
	}
	if prepared, _ := c.snapshot(); len(prepared) != 1 {
		t.Fatalf("the gateway's copy was not sent to the worker: %v", prepared)
	}
}

func TestFailedPreparationIsRetriedAndNothingIsForwarded(t *testing.T) {
	c := &calls{failNext: 2}
	w := &named{id: "worker-1", weight: 10}
	rt := relocRouter(&memPins{}, c, 0, &countingPicker{id: "worker-1"})
	rt.AddBackend(w)
	for i := 0; i < 2; i++ {
		rec := do(rt, "GET", "/ws_filebrowser", asUser("u1"))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "could not be prepared") {
			t.Fatalf("attempt %d -> %d %q", i, rec.Code, rec.Body.String())
		}
	}
	if w.hits != 0 {
		t.Fatalf("a request reached a worker whose home was not ready: %d", w.hits)
	}
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK || w.hits != 1 {
		t.Fatalf("after the worker recovered -> %d, hits=%d", code, w.hits)
	}
}

func TestLocalSessionsNeedNoPreparation(t *testing.T) {
	c := &calls{}
	rt := relocRouter(&memPins{}, c, 0, &countingPicker{id: LocalID})
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusOK {
		t.Fatalf("-> %d", code)
	}
	if prepared, _ := c.snapshot(); len(prepared) != 0 {
		t.Fatalf("a gateway-run session was prepared: %v", prepared)
	}
}

func TestWithoutWorkspaceSyncAPinnedUserIsNeverMoved(t *testing.T) {
	// No HomeOf: the router behaves as before, even with a long-gone worker.
	pins := &memPins{m: map[string]string{"u1": "worker-a"}}
	b := &named{id: "worker-b", weight: 10}
	rt := NewRouter(Config{
		Site: &recorder{}, PathPrefix: "/", Secret: []byte("secret"), GuestTTL: time.Hour,
		UID:  func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Pins: pins, RelocateAfter: time.Millisecond,
	})
	rt.AddBackend(b)
	time.Sleep(20 * time.Millisecond)
	if code := do(rt, "GET", "/ws_filebrowser", asUser("u1")).Code; code != http.StatusServiceUnavailable || b.hits != 0 {
		t.Fatalf("-> %d, b=%d: without workspace sync the user must wait for their worker", code, b.hits)
	}
}

func TestASessionOnAWorkerThatNeverReturnsIsPlacedAgainWithoutAnyManualStep(t *testing.T) {
	pins := &memPins{}
	c := &calls{}
	a := &named{id: "worker-a", weight: 10}
	b := &named{id: "worker-b", weight: 10}
	picker := &countingPicker{id: "worker-a"}
	rt := relocRouter(pins, c, 40*time.Millisecond, picker)
	rt.AddBackend(a)
	rt.AddBackend(b)
	do(rt, "GET", "/ws_filebrowser", asUser("u1")) // the session now belongs to worker-a

	rt.RemoveBackend(a)
	picker.id = "worker-b"
	// The context still names worker-a; within the grace period nothing moves.
	if code := do(rt, "GET", "/ws_python", asUser("u1")).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("within the grace period -> %d", code)
	}
	if b.hits != 0 {
		t.Fatal("the session moved during the grace period")
	}
	time.Sleep(80 * time.Millisecond)
	if code := do(rt, "GET", "/ws_python", asUser("u1")).Code; code != http.StatusOK || b.hits != 1 {
		t.Fatalf("after the grace period -> %d, b=%d", code, b.hits)
	}
	if _, moved := c.snapshot(); len(moved) != 1 || moved[0] != "home-u1:worker-a->worker-b" {
		t.Fatalf("moved = %v", moved)
	}
}

func TestAGuestOnAWorkerThatNeverReturnsIsPlacedAgainToo(t *testing.T) {
	c := &calls{}
	a := &named{id: "worker-a", weight: 10}
	b := &named{id: "worker-b", weight: 10}
	picker := &countingPicker{id: "worker-a"}
	rt := relocRouter(&memPins{}, c, 40*time.Millisecond, picker)
	rt.AddBackend(a)
	rt.AddBackend(b)
	first := do(rt, "GET", "/ws_filebrowser", nil)
	cookie := cookieOf(t, first)
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }

	rt.RemoveBackend(a)
	picker.id = "worker-b"
	time.Sleep(80 * time.Millisecond)
	if code := do(rt, "GET", "/ws_python", withCookie).Code; code != http.StatusOK || b.hits != 1 {
		t.Fatalf("-> %d, b=%d", code, b.hits)
	}
	prepared, moved := c.snapshot()
	if len(moved) != 1 || !strings.HasSuffix(moved[0], ":worker-a->worker-b") || len(prepared) != 2 {
		t.Fatalf("moved=%v prepared=%v", moved, prepared)
	}
}

func TestASessionOnALocalBackendIsNeverReplaced(t *testing.T) {
	c := &calls{}
	rt := relocRouter(&memPins{}, c, time.Millisecond, &countingPicker{id: LocalID})
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	time.Sleep(10 * time.Millisecond)
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if _, moved := c.snapshot(); len(moved) != 0 {
		t.Fatalf("a local session was moved: %v", moved)
	}
}
