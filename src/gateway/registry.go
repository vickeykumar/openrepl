package gateway

import (
	"sort"
	"sync"
	"time"
)

// ExecutionContext records which backend owns a session. It is created once
// and reused for every later request of the session; the backend is never
// re-chosen for an existing context.
type ExecutionContext struct {
	Key       string // "u:<uid>" for signed-in users, "g:<guestID>" for guests
	UID       string // empty for guests
	GuestID   string // empty for signed-in users
	BackendID string
	CreatedAt time.Time
	ExpiresAt time.Time // zero means it does not expire
}

// SessionRegistry is an in-process map of execution contexts.
type SessionRegistry struct {
	mu        sync.Mutex
	contexts  map[string]*ExecutionContext
	guestTTL  time.Duration
	now       func() time.Time
	lastSweep time.Time
}

// NewSessionRegistry creates a registry. Guest contexts expire guestTTL after
// their last use; signed-in users' contexts do not expire.
func NewSessionRegistry(guestTTL time.Duration) *SessionRegistry {
	return &SessionRegistry{
		contexts: make(map[string]*ExecutionContext),
		guestTTL: guestTTL,
		now:      time.Now,
	}
}

// Resolve returns a copy of the live context for key.
func (r *SessionRegistry) Resolve(key string) (ExecutionContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ec, ok := r.contexts[key]
	if !ok {
		return ExecutionContext{}, false
	}
	if r.expired(ec) {
		delete(r.contexts, key)
		return ExecutionContext{}, false
	}
	return *ec, true
}

// Create stores a new context for the identity on the given backend. An
// existing live context wins and is returned unchanged, so two racing first
// requests agree on one backend.
func (r *SessionRegistry) Create(id Identity, backendID string) ExecutionContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweep(now)
	if ec, ok := r.contexts[id.Key]; ok && !r.expired(ec) {
		return *ec
	}
	ec := &ExecutionContext{
		Key:       id.Key,
		UID:       id.UID,
		GuestID:   id.GuestID,
		BackendID: backendID,
		CreatedAt: now,
	}
	if id.UID == "" && r.guestTTL > 0 {
		ec.ExpiresAt = now.Add(r.guestTTL)
	}
	r.contexts[id.Key] = ec
	return *ec
}

// Touch slides a guest context's expiry. It does nothing for contexts that
// do not expire.
func (r *SessionRegistry) Touch(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ec, ok := r.contexts[key]
	if !ok || ec.ExpiresAt.IsZero() || r.expired(ec) {
		return
	}
	ec.ExpiresAt = r.now().Add(r.guestTTL)
}

// Release removes the context for key.
func (r *SessionRegistry) Release(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.contexts, key)
}

// Len returns the number of stored contexts, expired ones included until the
// next sweep.
func (r *SessionRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.contexts)
}

// Snapshot returns the live contexts ordered by key.
func (r *SessionRegistry) Snapshot() []ExecutionContext {
	r.mu.Lock()
	out := make([]ExecutionContext, 0, len(r.contexts))
	for _, ec := range r.contexts {
		if !r.expired(ec) {
			out = append(out, *ec)
		}
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (r *SessionRegistry) expired(ec *ExecutionContext) bool {
	return !ec.ExpiresAt.IsZero() && !r.now().Before(ec.ExpiresAt)
}

// sweep drops expired contexts, at most once a minute. Caller holds r.mu.
func (r *SessionRegistry) sweep(now time.Time) {
	if now.Sub(r.lastSweep) < time.Minute {
		return
	}
	r.lastSweep = now
	for k, ec := range r.contexts {
		if r.expired(ec) {
			delete(r.contexts, k)
		}
	}
}
