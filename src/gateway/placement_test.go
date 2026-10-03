package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// memPins is a PinStore that outlives a Router, like the user DB.
type memPins struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *memPins) Get(uid string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.m[uid]
	return b, ok
}

func (p *memPins) Set(uid, backend string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[uid] = backend
}

func asUser(uid string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("X-Test-Uid", uid) }
}

// pinnedRouter is a routing-only gateway with a pin store.
func pinnedRouter(site http.Handler, pins PinStore, picker Picker) *Router {
	return NewRouter(Config{
		Site:        site,
		PathPrefix:  "/",
		IsEntryPage: func(rel string) bool { return rel == "" },
		UID:         func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:      []byte("secret"),
		GuestTTL:    time.Hour,
		Pins:        pins,
		Picker:      picker,
	})
}

func TestSignedInUserReturnsToPinnedWorkerAfterRestart(t *testing.T) {
	pins := &memPins{}
	a1, b1 := &named{id: "worker-a", weight: 10}, &named{id: "worker-b", weight: 10}
	rt := pinnedRouter(&recorder{}, pins, &countingPicker{id: "worker-b"})
	rt.AddBackend(a1)
	rt.AddBackend(b1)
	do(rt, "GET", "/ws_go", asUser("u1"))
	if b1.hits != 1 {
		t.Fatalf("first placement: a=%d b=%d", a1.hits, b1.hits)
	}
	if got, _ := pins.Get("u1"); got != "worker-b" {
		t.Fatalf("pin = %q", got)
	}

	// The gateway restarts: a new router, an empty registry, the same pins.
	// The picker now prefers worker-a, but the user's files are on worker-b.
	picker := &countingPicker{id: "worker-a"}
	a2, b2 := &named{id: "worker-a", weight: 10}, &named{id: "worker-b", weight: 10}
	rt = pinnedRouter(&recorder{}, pins, picker)
	rt.AddBackend(a2)
	rt.AddBackend(b2)
	do(rt, "GET", "/ws_go", asUser("u1"))
	do(rt, "GET", "/ws_filebrowser", asUser("u1"))
	if b2.hits != 2 || a2.hits != 0 || picker.calls != 0 {
		t.Fatalf("after restart: a=%d b=%d picker calls=%d", a2.hits, b2.hits, picker.calls)
	}

	// A guest is not pinned and follows the picker.
	do(rt, "GET", "/ws_go", nil)
	if a2.hits != 1 {
		t.Fatalf("guest: a=%d", a2.hits)
	}
}

func TestPinnedUserFailsWhileTheirWorkerIsAway(t *testing.T) {
	pins := &memPins{m: map[string]string{"u1": "worker-b"}}
	picker := &countingPicker{id: "worker-a"}
	a := &named{id: "worker-a", weight: 10}
	rt := pinnedRouter(&recorder{}, pins, picker)
	rt.AddBackend(a)

	// worker-b is not connected.
	rec := do(rt, "GET", "/ws_go", asUser("u1"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "workspace node unavailable") {
		t.Fatalf("offline pinned worker -> %d %q", rec.Code, rec.Body.String())
	}
	if a.hits != 0 || picker.calls != 0 {
		t.Fatalf("the user was placed elsewhere: a=%d picker=%d", a.hits, picker.calls)
	}
	if got, _ := pins.Get("u1"); got != "worker-b" {
		t.Fatalf("the pin was rewritten to %q", got)
	}
	// The page itself still loads.
	if rec := do(rt, "GET", "/", asUser("u1")); rec.Code != http.StatusOK {
		t.Fatalf("entry page -> %d", rec.Code)
	}

	// A draining worker takes no new session either.
	b := &named{id: "worker-b", weight: 10, state: Draining}
	rt.AddBackend(b)
	if rec := do(rt, "GET", "/ws_go", asUser("u1")); rec.Code != http.StatusServiceUnavailable || b.hits != 0 {
		t.Fatalf("draining pinned worker -> %d, hits=%d", rec.Code, b.hits)
	}

	// Once it is back online the user reaches it again.
	b.state = Online
	if rec := do(rt, "GET", "/ws_go", asUser("u1")); rec.Code != http.StatusOK || b.hits != 1 {
		t.Fatalf("pinned worker back -> %d, hits=%d", rec.Code, b.hits)
	}
	// An existing session carries on when the worker starts draining.
	b.state = Draining
	if do(rt, "GET", "/ws_filebrowser", asUser("u1")); b.hits != 2 {
		t.Fatalf("existing session on a draining worker: hits=%d", b.hits)
	}
}

func TestEntryPageLoadsWhenNoNodeCanTakeTheSession(t *testing.T) {
	var backend string
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backend = BackendOf(r) })
	rt := pinnedRouter(site, nil, nil) // routing-only gateway, no workers
	if rec := do(rt, "GET", "/", nil); rec.Code != http.StatusOK || backend != "-" {
		t.Fatalf("entry page -> %d, backend %q", rec.Code, backend)
	}
	rec := do(rt, "GET", "/ws_go", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "no execution node available") {
		t.Fatalf("terminal -> %d %q", rec.Code, rec.Body.String())
	}
}

func terminalFor(routes map[string]string) func(string) (string, int64, bool) {
	return func(rel string) (string, int64, bool) {
		cmd, ok := routes[rel]
		return cmd, 20, ok
	}
}

func TestLanguageMissingOnTheSessionsNode(t *testing.T) {
	w := &named{id: "worker-1", weight: 10, langs: []string{"python"}}
	rt := NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/",
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Terminal:   terminalFor(map[string]string{"ws_python": "python", "ws_go": "gointerpreter"}),
	})
	rt.AddBackend(w)

	first := do(rt, "GET", "/ws_python", nil)
	c := cookieOf(t, first)
	if first.Code != http.StatusOK || w.hits != 1 {
		t.Fatalf("python -> %d", first.Code)
	}
	rec := do(rt, "GET", "/ws_go", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not available") || w.hits != 1 {
		t.Fatalf("missing language -> %d %q", rec.Code, rec.Body.String())
	}
	// File requests are not terminals and are not language-checked.
	if do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) }); w.hits != 2 {
		t.Fatalf("file request was refused, hits=%d", w.hits)
	}
}

func TestOpenTerminalsCountAgainstCapacityImmediately(t *testing.T) {
	up := websocket.Upgrader{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer worker.Close()

	reported := int64(5) // what the worker's last heartbeat said
	b := remoteTo(worker, "worker-1", "/")
	b.cfg.Capacity = func() (int64, int64) { return reported, 40 }
	rt := NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/",
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Terminal:   terminalFor(map[string]string{"ws_go": "gointerpreter"}),
	})
	rt.AddBackend(b)
	gw := httptest.NewServer(rt)
	defer gw.Close()

	if used, max := b.Capacity(); used != 5 || max != 40 {
		t.Fatalf("idle capacity = %d/%d", used, max)
	}
	url := "ws" + strings.TrimPrefix(gw.URL, "http") + "/ws_go"
	c1, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two terminals of weight 20 are open; the heartbeat has not caught up.
	if used, _ := b.Capacity(); used != 40 {
		t.Fatalf("with two terminals open used = %d, want 40", used)
	}
	// The worker is full now, so the pool stops choosing it.
	if _, err := NewPool(rt.Backends).Pick(Identity{}); err != nil {
		t.Fatal(err) // still placed: every eligible backend is full
	}
	other := &named{id: "worker-2", weight: 1}
	rt.AddBackend(other)
	for i := 0; i < 50; i++ {
		if id, _ := NewPool(rt.Backends).Pick(Identity{}); id != "worker-2" {
			t.Fatalf("a full worker was picked while another had room: %s", id)
		}
	}

	c1.Close()
	c2.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if used, _ := b.Capacity(); used == 5 {
			break
		}
		if time.Now().After(deadline) {
			used, _ := b.Capacity()
			t.Fatalf("capacity was not released after the terminals closed: used = %d", used)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
