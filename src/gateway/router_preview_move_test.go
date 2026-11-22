package gateway

import (
	"testing"
	"time"

	"livepreview"
)

// A session that moved back to the gateway: the route table still names the worker that held the workspace before (no "closed" announcement
// came), but the registry says where the session runs now. The Preview follows the registry.
func TestThePreviewFollowsTheSessionWhenItMovedAndTheRouteIsStale(t *testing.T) {
	site := &recorder{}
	a, b := &named{id: "worker-a"}, &named{id: "worker-b"}
	rt := newTestRouter(site, "/", &countingPicker{id: "worker-a"})
	rt.AddBackend(a)
	rt.AddBackend(b)
	home := "/home/openrepl/donadjdc"
	tok, _ := livepreview.NewToken("secret", home, time.Now(), time.Hour)
	// the worker announced the home; then the session moved to the gateway
	rt.Routes().Set(RouteHome, home, "worker-b")
	rt.registry.Create(Identity{Key: "u:donadjdc", UID: "donadjdc", Home: home}, LocalID)
	do(rt, "GET", "/preview/"+tok+"/p/8000/", nil)
	if b.hits != 0 || len(site.paths) != 1 {
		t.Fatalf("the Preview went to the old worker: b=%d site=%v", b.hits, site.paths)
	}
	if _, stale := rt.Routes().Lookup(RouteHome, home); stale {
		t.Error("the stale route must be dropped")
	}
	// and the other way: the session runs on a worker, the table knows nothing
	rt2 := newTestRouter(&recorder{}, "/", &countingPicker{id: "worker-a"})
	w := &named{id: "worker-a"}
	rt2.AddBackend(w)
	rt2.registry.Create(Identity{Key: "u:donadjdc", UID: "donadjdc", Home: home}, "worker-a")
	do(rt2, "GET", "/preview/"+tok+"/p/8000/", nil)
	if w.hits != 1 {
		t.Errorf("the Preview did not go to the worker that runs the session: %d", w.hits)
	}
}
