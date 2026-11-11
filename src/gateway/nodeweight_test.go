package gateway

import (
	"net/http"
	"sync"
	"testing"
)

// An admin's weight wins over the node's own, it can be taken away again, and the pool follows it at once.
func TestAnAdminWeightWinsOverTheNodesOwnAndTheNodesOwnComesBack(t *testing.T) {
	var mu sync.Mutex
	overrides := map[string]int{}
	lookup := func(id string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		w, ok := overrides[id]
		return w, ok
	}
	set := func(id string, w int) { mu.Lock(); overrides[id] = w; mu.Unlock() }
	unset := func(id string) { mu.Lock(); delete(overrides, id); mu.Unlock() }

	rt := NewRouter(Config{Site: http.NotFoundHandler(), PathPrefix: "/", Secret: []byte("s"), Local: LocalConfig{Weight: 10}, NodeWeight: lookup})
	remote := NewRemoteBackend(RemoteConfig{ID: "pi-1", Weight: 4, WeightOverride: lookup})
	rt.AddBackend(remote)
	local, _ := rt.Backend(LocalID)

	if local.Weight() != 10 || remote.Weight() != 4 {
		t.Fatalf("own weights: %d %d", local.Weight(), remote.Weight())
	}
	set(LocalID, 30)
	set("pi-1", 0)
	if local.Weight() != 30 || remote.Weight() != 0 {
		t.Fatalf("with overrides: %d %d", local.Weight(), remote.Weight())
	}
	// what the dashboard shows
	rows := map[string]WorkerInfo{}
	for _, row := range rt.workerInfo(nil) {
		rows[row.ID] = row
	}
	if r := rows[LocalID]; r.Weight != 30 || r.ConfiguredWeight != 10 || !r.WeightSet {
		t.Errorf("the gateway's row: %+v", r)
	}
	if r := rows["pi-1"]; r.Weight != 0 || r.ConfiguredWeight != 4 || !r.WeightSet || r.WeightPercent != 0 {
		t.Errorf("the worker's row: %+v", r)
	}

	// the pool follows: a node with weight 0 is never picked
	pool := NewPool(rt.Backends)
	for i := 0; i < 200; i++ {
		if id, err := pool.Pick(Identity{}); err != nil || id == "pi-1" {
			t.Fatalf("pick %d: %q %v", i, id, err)
		}
	}
	// and the share follows the weights
	set("pi-1", 30)
	picks := map[string]int{}
	for i := 0; i < 3000; i++ {
		id, _ := pool.Pick(Identity{})
		picks[id]++
	}
	if picks["pi-1"] < 1200 || picks["pi-1"] > 1800 {
		t.Errorf("equal weights should split about evenly: %v", picks)
	}

	// back to their own
	unset(LocalID)
	unset("pi-1")
	if local.Weight() != 10 || remote.Weight() != 4 {
		t.Fatalf("after removing the overrides: %d %d", local.Weight(), remote.Weight())
	}
	for _, row := range rt.workerInfo(nil) {
		if row.WeightSet || row.Weight != row.ConfiguredWeight {
			t.Errorf("%s still looks overridden: %+v", row.ID, row)
		}
	}
}

func TestNodesWithoutAnOverrideFunctionKeepTheirOwnWeight(t *testing.T) {
	rt := NewRouter(Config{Site: http.NotFoundHandler(), PathPrefix: "/", Secret: []byte("s"), Local: LocalConfig{Weight: 10}})
	rt.AddBackend(NewRemoteBackend(RemoteConfig{ID: "pi-1", Weight: 4}))
	for _, b := range rt.Backends() {
		if _, set := b.(interface{ WeightOverride() (int, bool) }).WeightOverride(); set {
			t.Errorf("%s has an override although there is no way to set one", b.ID())
		}
	}
}

// The Terminals column counts the terminals open on each node, the gateway's own included.
func TestTheWorkersPageCountsTheTerminalsOfEveryNode(t *testing.T) {
	rt := NewRouter(Config{Site: http.NotFoundHandler(), PathPrefix: "/", Secret: []byte("s"),
		Local: LocalConfig{Weight: 10, Active: func() int64 { return 1 }}})
	rt.AddBackend(NewRemoteBackend(RemoteConfig{ID: "pi-1", Weight: 10}))

	onLocal := rt.registry.Create(Identity{Key: "ka", UID: "a"}, LocalID)
	onPi := rt.registry.Create(Identity{Key: "kb", UID: "b"}, "pi-1")
	rt.registry.Create(Identity{Key: "kc", UID: "c"}, "pi-1") // a session with no terminal open
	cancel := func() {}
	forget := []func(){
		rt.trackTerminal(onLocal.Key, cancel),
		rt.trackTerminal(onLocal.Key, cancel),
		rt.trackTerminal(onLocal.Key, cancel),
		rt.trackTerminal(onPi.Key, cancel),
		rt.trackTerminal(onPi.Key, cancel),
	}
	rows := map[string]WorkerInfo{}
	for _, row := range rt.workerInfo(nil) {
		rows[row.ID] = row
	}
	if rows[LocalID].Terminals != 3 || rows["pi-1"].Terminals != 2 {
		t.Fatalf("terminals: gateway %d (want 3), pi-1 %d (want 2)", rows[LocalID].Terminals, rows["pi-1"].Terminals)
	}
	if rows[LocalID].Sessions != 1 || rows["pi-1"].Sessions != 2 {
		t.Fatalf("sessions: %+v %+v", rows[LocalID], rows["pi-1"])
	}
	for _, f := range forget {
		f()
	}
	for _, row := range rt.workerInfo(nil) {
		// the gateway's own server may know of one more (its own port); nobody else has any
		if row.ID == "pi-1" && row.Terminals != 0 {
			t.Errorf("pi-1 still shows %d terminals after they closed", row.Terminals)
		}
		if row.ID == LocalID && row.Terminals != 1 {
			t.Errorf("the gateway shows %d, want the 1 its own server reports", row.Terminals)
		}
	}
}
