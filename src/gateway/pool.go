package gateway

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// ErrNoBackend means no backend can take a new session.
var ErrNoBackend = errors.New("no execution node available")

// Pool places new sessions by randomized weighted selection, the method of
// sish-lb's ServerPool: the candidates are laid out on a number line, each
// taking a stretch as long as its weight, and a uniformly random point picks
// one. A backend with twice the weight gets twice the sessions on average.
//
// Unlike sish-lb, the candidate set is recomputed for every pick, because
// which backends are eligible changes with their state and load.
type Pool struct {
	backends func() []Backend

	mu  sync.Mutex
	rng *rand.Rand
}

// NewPool creates a pool over the backends the function returns.
func NewPool(backends func() []Backend) *Pool {
	return &Pool{
		backends: backends,
		// Its own generator: seeding the shared one, as sish-lb did, would
		// make every other user of math/rand predictable.
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Pick chooses a backend for a new session.
func (p *Pool) Pick(Identity) (string, error) {
	b, err := p.pick(p.backends())
	if err != nil {
		return "", err
	}
	return b.ID(), nil
}

func (p *Pool) pick(all []Backend) (Backend, error) {
	// Only ONLINE backends with a share take new sessions.
	var eligible, roomy []Backend
	for _, b := range all {
		if b.State() != Online || b.Weight() <= 0 {
			continue
		}
		eligible = append(eligible, b)
		if used, max := b.Capacity(); max <= 0 || used < max {
			roomy = append(roomy, b)
		}
	}
	candidates := roomy
	if len(candidates) == 0 {
		// Everything is full. Still place the session: the node itself then
		// refuses the terminal with its usual "max connections" message,
		// exactly as a full single server does.
		candidates = eligible
	}
	if len(candidates) == 0 {
		return nil, ErrNoBackend
	}

	// Cumulative weights, lightest first (a stable order for equal weights).
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Weight() != candidates[j].Weight() {
			return candidates[i].Weight() < candidates[j].Weight()
		}
		return candidates[i].ID() < candidates[j].ID()
	})
	totals := make([]int, len(candidates))
	running := 0
	for i, b := range candidates {
		running += b.Weight()
		totals[i] = running
	}

	p.mu.Lock()
	r := p.rng.Intn(running) + 1 // 1..total
	p.mu.Unlock()
	return candidates[sort.SearchInts(totals, r)], nil
}
