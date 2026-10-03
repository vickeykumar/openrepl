package gateway

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// With workspace sync a session has one home name on every node. A session
// the gateway runs itself must work in the gateway's copy of that home, or a
// session placed on the gateway after its worker was lost would not find its
// files.

func TestASessionTheGatewayRunsItselfUsesTheSyncedHome(t *testing.T) {
	site := &homeSite{}
	rt := syncRouter(site, &countingPicker{id: LocalID})

	first := do(rt, "GET", "/ws_python", nil)
	c := cookieOf(t, first)
	home := "guest-" + rt.affinity.GuestID(requestWith(c))
	do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })

	if len(site.homes) != 2 {
		t.Fatalf("the local backend served %d requests, want 2", len(site.homes))
	}
	for i := range site.homes {
		if site.homes[i] != home || !site.own[i] {
			t.Fatalf("request %d: home %q own=%v, want %q own=true", i, site.homes[i], site.own[i], home)
		}
	}

	// A signed-in user's home is named the same way.
	site2 := &homeSite{}
	rt2 := syncRouter(site2, &countingPicker{id: LocalID})
	do(rt2, "GET", "/ws_python", func(r *http.Request) { r.Header.Set("X-Test-Uid", "42") })
	if len(site2.homes) != 1 || site2.homes[0] != "home-42" || !site2.own[0] {
		t.Fatalf("signed-in user: %v %v", site2.homes, site2.own)
	}
}

func TestACrossSessionRequestToTheGatewayKeepsTheNameItAsks(t *testing.T) {
	// A shared session's viewer and a fork link name the owner's home or
	// process themselves; the gateway must not replace that with the viewer's
	// own home.
	site := &homeSite{}
	rt := syncRouter(site, &countingPicker{id: LocalID})
	c := cookieOf(t, do(rt, "GET", "/ws_python", nil))
	rt.Routes().Set(RouteHome, "/tmp/home/shared", LocalID)
	rt.Routes().Set(RouteJID, "abcd", LocalID)

	for _, p := range []string{"/ws_filebrowser?homedir=/tmp/home/shared", "/ws_python?jid=abcd"} {
		before := len(site.homes)
		do(rt, "GET", p, func(r *http.Request) { r.AddCookie(c) })
		if len(site.homes) != before+1 || site.homes[before] != "" || site.own[before] {
			t.Fatalf("%s was served with home %q own=%v, want neither", p, site.homes[before], site.own[before])
		}
	}
}

func TestWithoutWorkspaceSyncASessionTheGatewayRunsKeepsItsCookieHome(t *testing.T) {
	site := &homeSite{}
	rt := newTestRouter(site, "/", &countingPicker{id: LocalID})
	do(rt, "GET", "/ws_python", nil)
	if len(site.homes) != 1 || site.homes[0] != "" || site.own[0] {
		t.Fatalf("home %v own=%v, want the router to leave the home alone", site.homes, site.own)
	}
}

func TestAGuestPlacedOnTheGatewayAfterItsWorkerWasLostFindsItsHome(t *testing.T) {
	site := &homeSite{}
	c := &calls{}
	worker := &named{id: "worker-a", weight: 10}
	picker := &countingPicker{id: "worker-a"}
	rt := NewRouter(Config{
		Site:          site,
		PathPrefix:    "/",
		UID:           func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:        []byte("secret"),
		GuestTTL:      time.Hour,
		Picker:        picker,
		HomeOf:        func(id Identity) string { return "guest-" + id.GuestID },
		PrepareHome:   c.prepare,
		OnMoved:       c.onMoved,
		RelocateAfter: 40 * time.Millisecond,
	})
	rt.AddBackend(worker)

	first := do(rt, "GET", "/ws_filebrowser", nil)
	cookie := cookieOf(t, first)
	home := "guest-" + rt.affinity.GuestID(requestWith(cookie))
	if worker.hits != 1 {
		t.Fatal("test set-up: the worker should hold the session")
	}

	rt.RemoveBackend(worker) // the worker is lost for good
	picker.id = LocalID
	time.Sleep(80 * time.Millisecond)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(cookie) })
	if rec.Code != http.StatusOK || len(site.homes) != 1 {
		t.Fatalf("-> %d, local requests %d", rec.Code, len(site.homes))
	}
	if site.homes[0] != home || !site.own[0] {
		t.Fatalf("the session was placed on the gateway with home %q own=%v, want its old home %q", site.homes[0], site.own[0], home)
	}
	if _, moved := c.snapshot(); len(moved) != 1 || !strings.HasSuffix(moved[0], ":worker-a->"+LocalID) {
		t.Fatalf("the old worker was not told to drop its copy: %v", moved)
	}
}
