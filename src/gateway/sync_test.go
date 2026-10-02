package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// homeSite records the workspace each request was served with.
type homeSite struct {
	recorder
	homes []string
	own   []bool // whether the gateway itself runs the session
}

func (h *homeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.homes = append(h.homes, WorkspaceOf(r))
	h.own = append(h.own, OwnWorkspace(r))
	h.mu.Unlock()
	h.recorder.ServeHTTP(w, r)
}

func syncRouter(site http.Handler, picker Picker) *Router {
	return NewRouter(Config{
		Site:       site,
		PathPrefix: "/",
		UID:        func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Picker:     picker,
		HomeOf: func(id Identity) string {
			if id.UID != "" {
				return "home-" + id.UID
			}
			return "guest-" + id.GuestID
		},
	})
}

func TestSyncingWorkerTakesNoNewSessionsButKeepsItsOwn(t *testing.T) {
	w := &named{id: "worker-1", weight: 10}
	rt := syncRouter(&recorder{}, nil)
	rt.AddBackend(w)
	first := do(rt, "GET", "/ws_filebrowser", nil)
	c := cookieOf(t, first)
	if w.hits != 1 {
		t.Fatal("the online worker should have taken the session")
	}

	w.state = Syncing // it reconnected and is reconciling
	for i := 0; i < 20; i++ {
		do(rt, "GET", "/ws_filebrowser", nil) // new guests
	}
	if w.hits != 1 {
		t.Fatalf("a SYNCING worker received %d new session(s)", w.hits-1)
	}
	// A session it already had is still forwarded: the worker itself waits
	// for that home to be in step.
	do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
	if w.hits != 2 {
		t.Fatalf("an existing session was not forwarded to its SYNCING worker: %d", w.hits)
	}

	w.state = Online
	n := 0
	for i := 0; i < 40; i++ {
		before := w.hits
		do(rt, "GET", "/ws_filebrowser", nil)
		n += w.hits - before
	}
	if n == 0 {
		t.Fatal("the worker was never chosen again once ONLINE")
	}
	if Syncing.String() != "SYNCING" {
		t.Fatalf("state name = %q", Syncing.String())
	}
}

func TestFileRoutesAreServedFromTheGatewayWhileTheWorkerIsAway(t *testing.T) {
	site := &homeSite{}
	w := &named{id: "worker-1", weight: 10}
	rt := syncRouter(site, nil)
	rt.AddBackend(w)

	first := do(rt, "GET", "/ws_filebrowser", nil)
	c := cookieOf(t, first)
	home := "guest-" + rt.affinity.GuestID(requestWith(c))
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if w.hits != 1 {
		t.Fatal("test set-up: the worker should hold the session")
	}

	w.state = Offline
	for _, p := range []string{"/ws_filebrowser?q=load", "/ws_filebrowser", "/upload_file"} {
		before := len(site.homes)
		rec := do(rt, "POST", p, withCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s while the worker is away -> %d %q", p, rec.Code, rec.Body.String())
		}
		if len(site.homes) != before+1 || site.homes[before] != home {
			t.Fatalf("%s was not served from the gateway's copy of %s: %v", p, home, site.homes)
		}
	}
	if w.hits != 1 {
		t.Fatalf("a request went to the worker that is away: %d", w.hits)
	}

	// Terminals cannot be served: the programs went with the worker.
	for _, p := range []string{"/ws_python", "/ws", "/ws_go"} {
		rec := do(rt, "GET", p, withCookie)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "unavailable") {
			t.Fatalf("%s -> %d %q, want 503", p, rec.Code, rec.Body.String())
		}
	}
	// A route that is not a file route is not served locally either.
	if rec := do(rt, "GET", "/ws_filebrowserx", withCookie); rec.Code == http.StatusOK && len(site.homes) > 3 {
		t.Fatal("a look-alike path was treated as a file route")
	}

	// Back online: forwarded again, not served locally.
	w.state = Online
	before := len(site.homes)
	do(rt, "GET", "/ws_filebrowser", withCookie)
	if w.hits != 2 || len(site.homes) != before {
		t.Fatalf("after the worker returned: hits=%d local=%d", w.hits, len(site.homes)-before)
	}
}

func TestFileRoutesFallBackForASignedInUserToo(t *testing.T) {
	site := &homeSite{}
	w := &named{id: "worker-1", weight: 10}
	rt := syncRouter(site, nil)
	rt.AddBackend(w)
	asUser := func(r *http.Request) { r.Header.Set("X-Test-Uid", "42") }
	do(rt, "GET", "/ws_filebrowser", asUser)
	w.state = Offline
	if rec := do(rt, "GET", "/ws_filebrowser", asUser); rec.Code != http.StatusOK {
		t.Fatalf("-> %d", rec.Code)
	}
	if len(site.homes) != 1 || site.homes[0] != "home-42" {
		t.Fatalf("served with %v, want home-42", site.homes)
	}
}

func TestNoFallbackWithoutAHomeName(t *testing.T) {
	// Without workspace sync the router has no HomeOf and keeps its old
	// behaviour: a lost worker means 503, even for file requests.
	w := &named{id: "worker-1", weight: 10}
	rt := newTestRouter(&recorder{}, "/", &countingPicker{id: "worker-1"}) // the session is on the worker
	rt.AddBackend(w)
	first := do(rt, "GET", "/ws_filebrowser", nil)
	c := cookieOf(t, first)
	if w.hits != 1 {
		t.Fatal("test set-up: the session should be on the worker")
	}
	w.state = Offline
	rec := do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("-> %d", rec.Code)
	}
}

func TestRouteMapCrossSessionKeysDoNotUseTheFallback(t *testing.T) {
	// A viewer's request for another session's home that is routed to a
	// worker that is away fails: only a session's own home is served locally.
	site := &homeSite{}
	owner := &named{id: "worker-2", weight: 1}
	rt := syncRouter(site, &countingPicker{id: LocalID})
	rt.AddBackend(owner)
	rt.Routes().Set(RouteHome, "/tmp/home/shared", "worker-2")
	owner.state = Offline
	rec := do(rt, "GET", "/ws_filebrowser?homedir=/tmp/home/shared", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a shared-session request to an away worker -> %d, want 503", rec.Code)
	}
}

func TestRegistryHomeOwner(t *testing.T) {
	site := &recorder{}
	w := &named{id: "worker-1", weight: 10}
	rt := syncRouter(site, nil)
	rt.AddBackend(w)
	first := do(rt, "GET", "/ws_filebrowser", nil)
	c := cookieOf(t, first)
	home := "guest-" + rt.affinity.GuestID(requestWith(c))

	if owner, ok := rt.OwnerOf(home); !ok || owner != "worker-1" {
		t.Fatalf("OwnerOf(%s) = %q, %v", home, owner, ok)
	}
	if _, ok := rt.OwnerOf("guest-unknown"); ok {
		t.Fatal("an unknown home has an owner")
	}
	if _, ok := rt.OwnerOf(""); ok {
		t.Fatal("the empty home has an owner")
	}
	rt.Registry().Release("g:" + strings.TrimPrefix(home, "guest-"))
	if _, ok := rt.OwnerOf(home); ok {
		t.Fatal("a released session still owns its home")
	}
	_ = httptest.NewRecorder
}
