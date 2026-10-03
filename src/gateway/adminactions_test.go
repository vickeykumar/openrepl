package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// What an admin can do to a running session: end it, move it, and look at it.

// terminalBackend serves a terminal the way a worker does: the request stays
// open until its context is cancelled.
type terminalBackend struct {
	named
	mu      sync.Mutex
	open    int
	started chan struct{}
	ended   chan struct{}
}

func newTerminalBackend(id string) *terminalBackend {
	return &terminalBackend{
		named:   named{id: id, weight: 10},
		started: make(chan struct{}, 8),
		ended:   make(chan struct{}, 8),
	}
}

func (b *terminalBackend) Serve(w http.ResponseWriter, r *http.Request) error {
	b.mu.Lock()
	b.open++
	b.mu.Unlock()
	b.started <- struct{}{}
	<-r.Context().Done()
	b.mu.Lock()
	b.open--
	b.mu.Unlock()
	b.ended <- struct{}{}
	return nil
}

func (b *terminalBackend) openTerminals() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

func actionRouter(c *calls, pins PinStore, picker Picker) *Router {
	return NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/",
		UID:        func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Pins:       pins,
		Picker:     picker,
		HomeOf:     func(id Identity) string { return "home-" + id.UID + id.GuestID },
		Terminal: func(rel string) (string, int64, bool) {
			return "python", 10, strings.HasPrefix(rel, "ws_") && rel != "ws_filebrowser"
		},
		PrepareHome: c.prepare,
		OnMoved:     c.onMoved,
		UserLabel:   func(uid string) string { return uid + "@example.com" },
	})
}

// openTerminal starts a terminal for a user and returns when it is being served.
func openTerminal(rt *Router, b *terminalBackend, uid string) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest("GET", "/ws_python", nil)
		req.Header.Set("X-Test-Uid", uid)
		rt.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-b.started
	return done
}

func waitClosed(t *testing.T, done chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

func adminPost(rt *Router, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	rt.AdminHandler(nil).ServeHTTP(rec, req)
	return rec
}

func TestEndingASessionClosesItsTerminalsAndForgetsIt(t *testing.T) {
	c := &calls{}
	rt := actionRouter(c, &memPins{}, &countingPicker{id: "worker-a"})
	a := newTerminalBackend("worker-a")
	rt.AddBackend(a)

	t1 := openTerminal(rt, a, "u1")
	t2 := openTerminal(rt, a, "u1")
	other := openTerminal(rt, a, "u2")
	if a.openTerminals() != 3 {
		t.Fatalf("open = %d", a.openTerminals())
	}

	// the sessions table shows the open terminals and the user's label
	var list struct{ Sessions []SessionInfo }
	rec := httptest.NewRecorder()
	rt.AdminHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/admin/sessions", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]SessionInfo{}
	for _, s := range list.Sessions {
		byKey[s.Key] = s
	}
	if s := byKey["u:u1"]; s.Terminals != 2 || s.User != "u1@example.com" || s.Home != "home-u1" || s.LastActive == "" || s.Backend != "worker-a" {
		t.Errorf("session u1 = %+v", s)
	}
	if s := byKey["u:u2"]; s.Terminals != 1 {
		t.Errorf("session u2 = %+v", s)
	}

	rec = adminPost(rt, "/admin/sessions/u:u1/end", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"terminalsClosed":2`) {
		t.Fatalf("end -> %d %s", rec.Code, rec.Body.String())
	}
	waitClosed(t, t1, "the first terminal")
	waitClosed(t, t2, "the second terminal")
	if a.openTerminals() != 1 {
		t.Errorf("open = %d, want only the other user's terminal", a.openTerminals())
	}
	if _, ok := rt.Registry().Resolve("u:u1"); ok {
		t.Error("the session is still registered")
	}
	if _, ok := rt.Registry().Resolve("u:u2"); !ok {
		t.Error("another user's session was ended")
	}
	select {
	case <-other:
		t.Error("another user's terminal was closed")
	default:
	}

	if rec := adminPost(rt, "/admin/sessions/u:u1/end", ""); rec.Code != http.StatusNotFound {
		t.Errorf("ending it again -> %d, want 404", rec.Code)
	}
	if rec := adminPost(rt, "/admin/sessions/g:nobody/end", ""); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown session -> %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	rt.AdminHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/admin/sessions/u:u1/end", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET to an action -> %d, want 405", rec.Code)
	}

	// the user comes back and is placed again
	t3 := openTerminal(rt, a, "u1")
	rt.EndSession("u:u1")
	rt.EndSession("u:u2")
	waitClosed(t, t3, "the new terminal")
	waitClosed(t, other, "the other user's terminal")
}

func TestMovingASessionNeedsSyncAndAReadyNode(t *testing.T) {
	c := &calls{}
	pins := &memPins{}
	rt := actionRouter(c, pins, &countingPicker{id: "worker-a"})
	a, b := newTerminalBackend("worker-a"), newTerminalBackend("worker-b")
	rt.AddBackend(a)
	rt.AddBackend(b)
	draining := &named{id: "worker-d", weight: 10, state: Draining}
	rt.AddBackend(draining)

	term := openTerminal(rt, a, "u1")
	if got, _ := pins.Get("u1"); got != "worker-a" {
		t.Fatalf("pin = %q", got)
	}

	for _, tc := range []struct {
		to   string
		want int
		msg  string
	}{
		{"worker-a", http.StatusConflict, "already on that node"},
		{"worker-zzz", http.StatusConflict, "no node"},
		{"worker-d", http.StatusConflict, "draining"},
	} {
		rec := adminPost(rt, "/admin/sessions/u:u1/move", `{"to":"`+tc.to+`"}`)
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.msg) {
			t.Errorf("move to %s -> %d %q, want %d containing %q", tc.to, rec.Code, rec.Body.String(), tc.want, tc.msg)
		}
	}
	if rec := adminPost(rt, "/admin/sessions/u:u1/move", `{`); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad body -> %d", rec.Code)
	}
	if rec := adminPost(rt, "/admin/sessions/u:u1/move", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no target -> %d", rec.Code)
	}
	if rec := adminPost(rt, "/admin/sessions/g:nobody/move", `{"to":"worker-b"}`); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown session -> %d", rec.Code)
	}
	if a.openTerminals() != 1 {
		t.Fatal("a refused move closed the terminal")
	}

	rec := adminPost(rt, "/admin/sessions/u:u1/move", `{"to":"worker-b"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"terminalsClosed":1`) {
		t.Fatalf("move -> %d %s", rec.Code, rec.Body.String())
	}
	waitClosed(t, term, "the terminal on the old node")
	ec, ok := rt.Registry().Resolve("u:u1")
	if !ok || ec.BackendID != "worker-b" || ec.Prepared {
		t.Errorf("context after the move = %+v %v", ec, ok)
	}
	if got, _ := pins.Get("u1"); got != "worker-b" {
		t.Errorf("pin = %q, want worker-b", got)
	}
	if _, moved := c.snapshot(); len(moved) != 1 || moved[0] != "home-u1:worker-a->worker-b" {
		t.Errorf("the old node was not told to drop the home: %v", moved)
	}

	// the next terminal runs on the new node, after the home was sent to it
	term2 := openTerminal(rt, b, "u1")
	if prepared, _ := c.snapshot(); len(prepared) == 0 || prepared[len(prepared)-1] != "home-u1@worker-b" {
		t.Errorf("home not sent to the new node: %v", prepared)
	}
	rt.EndSession("u:u1")
	waitClosed(t, term2, "the terminal on the new node")

	// without workspace sync nothing can be moved: the files would stay behind
	plain := NewRouter(Config{Site: &recorder{}, PathPrefix: "/", UID: func(r *http.Request) string { return "u1" }, Picker: &countingPicker{id: "worker-a"}})
	plain.AddBackend(&named{id: "worker-a", weight: 10})
	plain.AddBackend(&named{id: "worker-b", weight: 10})
	do(plain, "GET", "/ws_filebrowser", nil)
	if _, err := plain.MoveSession("u:u1", "worker-b"); err == nil || !strings.Contains(err.Error(), "workspace sync") {
		t.Errorf("a move without sync: %v", err)
	}
}

func TestAnAdminCanDropAWorkersConnection(t *testing.T) {
	rt := actionRouter(&calls{}, &memPins{}, nil)
	h := rt.AdminHandler(nil) // workers are disabled
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/admin/workers/local/reconnect", http.StatusBadRequest},
		{"/admin/workers/worker-a/reconnect", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("POST %s -> %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workers/worker-a/reconnect", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET -> %d, want 405", rec.Code)
	}
}

func TestSyncDetailsAppearInTheWorkerList(t *testing.T) {
	rt := NewRouter(Config{
		Site: &recorder{}, PathPrefix: "/",
		SyncInfo: func(id string) (SyncInfo, bool) {
			if id == "worker-a" {
				return SyncInfo{Homes: 4, Connected: true, ClockOffset: 250 * time.Millisecond}, true
			}
			return SyncInfo{}, false
		},
	})
	rt.AddBackend(&named{id: "worker-a", weight: 10})
	rt.AddBackend(&named{id: "worker-b", weight: 10})

	rec := httptest.NewRecorder()
	rt.AdminHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workers", nil))
	var list struct{ Workers []WorkerInfo }
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]WorkerInfo{}
	for _, w := range list.Workers {
		got[w.ID] = w
	}
	if s := got["worker-a"].Sync; s == nil || s.Homes != 4 || s.ClockOffsetMs != 250 {
		t.Errorf("worker-a sync = %+v", s)
	}
	if got["worker-b"].Sync != nil || got["local"].Sync != nil {
		t.Errorf("a node without a sync conversation must not show one: %+v", got)
	}
}

// A healthy worker whose terminal was ended by an admin is not "away": the
// page must not be told to count down.
func TestAwayOnlyWhileTheWorkerIsGone(t *testing.T) {
	rt := actionRouter(&calls{}, &memPins{}, nil)
	rt.relocateAfter = time.Hour
	rt.AddBackend(&named{id: "worker-a", weight: 10})
	if d := rt.awayFor("worker-a"); d != 0 {
		t.Errorf("a connected worker is away for %v", d)
	}
	b, _ := rt.Backend("worker-a")
	rt.RemoveBackend(b)
	if d := rt.awayFor("worker-a"); d <= 0 {
		t.Errorf("a lost worker is away for %v, want a countdown", d)
	}
	if d := rt.awayFor("never-seen"); d < 0 {
		t.Errorf("unknown worker: %v", d)
	}
}
