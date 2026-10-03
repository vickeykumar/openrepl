package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"
)

// GET admin/workers shows, for every node, how many sessions the weighted
// random choice gave it, next to the share it should get by weight.

type adminWorkers struct {
	Workers     []WorkerInfo `json:"workers"`
	PickedTotal int64        `json:"pickedTotal"`
	PickedSince string       `json:"pickedSince"`
}

func readWorkers(t *testing.T, rt *Router) (adminWorkers, map[string]WorkerInfo) {
	t.Helper()
	rec := do(rt.AdminHandler(nil), "GET", "/admin/workers", nil)
	if rec.Code != 200 {
		t.Fatalf("GET admin/workers -> %d %s", rec.Code, rec.Body.String())
	}
	var got adminWorkers
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	byID := map[string]WorkerInfo{}
	for _, w := range got.Workers {
		byID[w.ID] = w
	}
	return got, byID
}

// cyclePicker answers with the given backends in order, then starts again.
type cyclePicker struct {
	mu  sync.Mutex
	ids []string
	n   int
}

func (p *cyclePicker) Pick(Identity) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.ids[p.n%len(p.ids)]
	p.n++
	return id, nil
}

func repeat(id string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = id
	}
	return out
}

func TestAdminShowsWhatTheRandomChoiceGaveEachNodeNextToItsWeight(t *testing.T) {
	ids := append(append(repeat("worker-a", 5), repeat("worker-b", 3)...), repeat(LocalID, 2)...)
	rt := newTestRouter(&recorder{}, "/", &cyclePicker{ids: ids}) // local weight 10
	a := &named{id: "worker-a", weight: 30}
	b := &named{id: "worker-b", weight: 10}
	rt.AddBackend(a)
	rt.AddBackend(b)
	for range ids {
		do(rt, "GET", "/ws_filebrowser", nil) // each is a new visitor
	}

	got, by := readWorkers(t, rt)
	if got.PickedTotal != 10 {
		t.Fatalf("pickedTotal = %d, want 10", got.PickedTotal)
	}
	if _, err := time.Parse(time.RFC3339, got.PickedSince); err != nil {
		t.Fatalf("pickedSince = %q: %v", got.PickedSince, err)
	}
	for _, c := range []struct {
		id              string
		picked          int64
		pickedPct, wPct float64
	}{
		{"worker-a", 5, 50, 60}, // weights 30:10:10 -> 60/20/20
		{"worker-b", 3, 30, 20},
		{LocalID, 2, 20, 20},
	} {
		w := by[c.id]
		if w.Picked != c.picked || w.PickedPercent != c.pickedPct || w.WeightPercent != c.wPct {
			t.Errorf("%s: picked %d (%.1f%%), expected share %.1f%%; want %d (%.1f%%) and %.1f%%",
				c.id, w.Picked, w.PickedPercent, w.WeightPercent, c.picked, c.pickedPct, c.wPct)
		}
	}

	// A node that does not take new sessions has no expected share, and the
	// others' shares are worked out without it.
	b.state = Draining
	_, by = readWorkers(t, rt)
	if by["worker-b"].WeightPercent != 0 || by["worker-a"].WeightPercent != 75 || by[LocalID].WeightPercent != 25 {
		t.Fatalf("expected shares with worker-b draining: a=%v b=%v local=%v",
			by["worker-a"].WeightPercent, by["worker-b"].WeightPercent, by[LocalID].WeightPercent)
	}
	if by["worker-b"].Picked != 3 {
		t.Fatalf("a draining node keeps its count: %d", by["worker-b"].Picked)
	}
	a.state = Offline
	_, by = readWorkers(t, rt)
	if by["worker-a"].WeightPercent != 0 || by[LocalID].WeightPercent != 100 {
		t.Fatalf("with worker-a offline: a=%v local=%v", by["worker-a"].WeightPercent, by[LocalID].WeightPercent)
	}
}

func TestOnlyWhatThePickerChoseIsCounted(t *testing.T) {
	// A signed-in user who goes back to the worker that holds their files was
	// not picked; counting them would hide whether the choice follows the
	// weights.
	pins := &memPins{m: map[string]string{}}
	w := &named{id: "worker-a", weight: 10}
	rt := newTestRouter(&recorder{}, "/", &cyclePicker{ids: []string{"worker-a"}})
	rt.pins = pins
	rt.AddBackend(w)
	do(rt, "GET", "/ws_filebrowser", nil) // a guest: picked
	before, _ := readWorkers(t, rt)

	user := func() { do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.Header.Set("X-Test-Uid", "u1") }) }
	user() // picked
	rt.Registry().Release("u:u1")
	user() // goes back to its pinned worker: not a pick
	user()
	after, by := readWorkers(t, rt)
	if after.PickedTotal != before.PickedTotal+1 {
		t.Fatalf("pickedTotal went from %d to %d, want one more pick for the user's first session only", before.PickedTotal, after.PickedTotal)
	}
	if by["worker-a"].Picked != before.PickedTotal+1 {
		t.Fatalf("worker-a picked = %d", by["worker-a"].Picked)
	}
}

func TestPickedSharesFollowTheWeightsThroughTheRealPool(t *testing.T) {
	// Fresh visitors, the real randomized weighted choice, and the numbers the
	// admin API reports.
	rt := NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/",
		UID:        func(r *http.Request) string { return "" },
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Local:      LocalConfig{Weight: 10},
	})
	rt.AddBackend(&named{id: "worker-a", weight: 30})
	const visitors = 3000
	for i := 0; i < visitors; i++ {
		do(rt, "GET", "/ws_filebrowser", nil)
	}
	got, by := readWorkers(t, rt)
	if got.PickedTotal != visitors {
		t.Fatalf("pickedTotal = %d, want %d", got.PickedTotal, visitors)
	}
	for _, id := range []string{"worker-a", LocalID} {
		w := by[id]
		if math.Abs(w.PickedPercent-w.WeightPercent) > 5 { // five standard deviations
			t.Errorf("%s: %.1f%% of the sessions, expected %.1f%%", id, w.PickedPercent, w.WeightPercent)
		}
	}
	if by["worker-a"].WeightPercent != 75 || by[LocalID].WeightPercent != 25 {
		t.Fatalf("expected shares %v / %v", by["worker-a"].WeightPercent, by[LocalID].WeightPercent)
	}
	if by["worker-a"].Picked+by[LocalID].Picked != visitors {
		t.Fatal("the counts do not add up to the visitors")
	}
}

func TestPercentRoundsToOneDecimalAndSurvivesZero(t *testing.T) {
	for _, c := range []struct{ part, whole, want float64 }{
		{1, 3, 33.3},
		{2, 3, 66.7},
		{1, 1, 100},
		{0, 5, 0},
		{5, 0, 0},
		{1, -1, 0},
	} {
		if got := percent(c.part, c.whole); got != c.want {
			t.Errorf("percent(%v, %v) = %v, want %v", c.part, c.whole, got, c.want)
		}
	}
}

func TestAdminWithNoSessionsYetShowsZeros(t *testing.T) {
	rt := newTestRouter(&recorder{}, "/", nil)
	got, by := readWorkers(t, rt)
	if got.PickedTotal != 0 || by[LocalID].Picked != 0 || by[LocalID].PickedPercent != 0 || by[LocalID].WeightPercent != 100 {
		t.Fatalf("%+v", by[LocalID])
	}
}
