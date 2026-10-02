package gateway

import (
	"math"
	"math/rand"
	"testing"
)

func seededPool(bs ...Backend) *Pool {
	p := NewPool(func() []Backend { return bs })
	p.rng = rand.New(rand.NewSource(1))
	return p
}

func picks(t *testing.T, p *Pool, n int) map[string]int {
	t.Helper()
	got := map[string]int{}
	for i := 0; i < n; i++ {
		id, err := p.Pick(Identity{})
		if err != nil {
			t.Fatal(err)
		}
		got[id]++
	}
	return got
}

func TestPoolIsRandomizedAndWeighted(t *testing.T) {
	p := seededPool(
		&named{id: "a", weight: 1},
		&named{id: "b", weight: 2},
		&named{id: "c", weight: 7},
	)
	const n = 20000
	got := picks(t, p, n)
	for id, weight := range map[string]float64{"a": 1, "b": 2, "c": 7} {
		want := n * weight / 10
		if math.Abs(float64(got[id])-want) > want*0.10 {
			t.Errorf("%s: %d picks, want about %.0f (share %v/10)", id, got[id], want, weight)
		}
	}

	// It is a random draw, not a rotation: the picks are not in a fixed cycle.
	first := make([]string, 30)
	for i := range first {
		first[i], _ = p.Pick(Identity{})
	}
	cyclic := true
	for i := 10; i < len(first); i++ {
		if first[i] != first[i-10] {
			cyclic = false
		}
	}
	if cyclic {
		t.Fatalf("picks repeat with period 10: %v", first)
	}
}

func TestPoolEqualWeightsShareEvenly(t *testing.T) {
	p := seededPool(&named{id: "a", weight: 10}, &named{id: "b", weight: 10})
	got := picks(t, p, 10000)
	if math.Abs(float64(got["a"]-got["b"])) > 500 {
		t.Fatalf("uneven split: %v", got)
	}
}

func TestPoolSkipsBackendsThatTakeNoNewSessions(t *testing.T) {
	p := seededPool(
		&named{id: "ok", weight: 1},
		&named{id: "draining", weight: 100, state: Draining},
		&named{id: "offline", weight: 100, state: Offline},
		&named{id: "routing-only", weight: 0},
	)
	got := picks(t, p, 500)
	if len(got) != 1 || got["ok"] != 500 {
		t.Fatalf("picks = %v, want only ok", got)
	}
}

func TestPoolRespectsCapacity(t *testing.T) {
	full := &named{id: "full", weight: 100, used: 512, max: 512}
	room := &named{id: "room", weight: 1, used: 100, max: 512}
	unlimited := &named{id: "unlimited", weight: 1} // max 0 = no limit
	p := seededPool(full, room, unlimited)
	got := picks(t, p, 1000)
	if got["full"] != 0 || got["room"] == 0 || got["unlimited"] == 0 {
		t.Fatalf("picks = %v", got)
	}

	// Space frees up on the full backend and it is chosen again.
	full.used = 10
	if got := picks(t, p, 1000); got["full"] == 0 {
		t.Fatalf("picks = %v", got)
	}
}

func TestPoolStillPlacesWhenEverythingIsFull(t *testing.T) {
	p := seededPool(
		&named{id: "a", weight: 10, used: 9, max: 9},
		&named{id: "b", weight: 10, used: 9, max: 9},
		&named{id: "down", weight: 10, state: Offline},
	)
	got := picks(t, p, 400)
	if got["a"] == 0 || got["b"] == 0 || got["down"] != 0 {
		t.Fatalf("picks = %v", got)
	}
}

func TestPoolWithNoEligibleBackend(t *testing.T) {
	for _, p := range []*Pool{
		seededPool(),
		seededPool(&named{id: "a", weight: 0}, &named{id: "b", weight: 5, state: Draining}),
	} {
		if _, err := p.Pick(Identity{}); err != ErrNoBackend {
			t.Fatalf("err = %v, want ErrNoBackend", err)
		}
	}
}
