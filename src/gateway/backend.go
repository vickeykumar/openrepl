// Package gateway routes execution-bound requests to the backend that owns
// the user's session. See docs/lld/11-distributed-execution.md.
package gateway

import "net/http"

// LocalID is the id of the backend that runs sessions on the gateway itself.
const LocalID = "local"

// State is the availability of a backend for new and existing sessions.
type State int

const (
	// Online backends accept new sessions.
	Online State = iota
	// Draining backends keep existing sessions but get no new ones.
	Draining
	// Offline backends are unreachable; their sessions fail.
	Offline
	// Syncing backends have just connected and are still reconciling their
	// homes with the gateway. They take no new sessions; the ones they
	// already have are served once their own home is in step.
	Syncing
)

func (s State) String() string {
	switch s {
	case Online:
		return "ONLINE"
	case Draining:
		return "DRAINING"
	case Syncing:
		return "SYNCING"
	default:
		return "OFFLINE"
	}
}

// Backend serves execution-bound requests for the sessions it owns.
type Backend interface {
	ID() string
	State() State
	// Weight is the backend's relative share of new sessions; 0 means it
	// never receives one.
	Weight() int
	// Capacity is the memory budget for sessions, in the MB units of the
	// per-command memory weights. max 0 means no limit.
	Capacity() (used, max int64)
	// HasLanguage reports whether the backend can run the REPL command.
	HasLanguage(command string) bool
	Serve(w http.ResponseWriter, r *http.Request) error
}

// LocalConfig describes the gateway's own backend.
type LocalConfig struct {
	// Weight is the gateway's share of new sessions; 0 makes it routing-only.
	Weight int
	// WeightOverride, when it answers true, is the share an admin set from the
	// dashboard: it is used instead of Weight. Optional.
	WeightOverride func(id string) (int, bool)
	// Capacity reports the gateway's own load. Optional.
	Capacity func() (used, max int64)
	// Active reports how many terminals are open on the gateway itself, for
	// the admin API. Optional.
	Active func() int64
	// Ptrace is what the gateway's host answers about tracing programs (see
	// tunnel.RegisterRequest.Ptrace), for the admin API. Optional.
	Ptrace string
}

// LanguageRule is what an admin decided for one language on one node.
type LanguageRule int

const (
	// LanguageDefault follows what the node says it can run (--worker-languages).
	LanguageDefault LanguageRule = iota
	// LanguageOff refuses new terminals of the language on the node, even if it
	// can run them.
	LanguageOff
	// LanguageOn lets the node take the language although it did not declare it:
	// an admin installed it since the worker started.
	LanguageOn
)

// LocalBackend runs the request with the existing OpenREPL handlers, with
// no proxy hop in between.
type LocalBackend struct {
	handler http.Handler
	cfg     LocalConfig
}

// NewLocalBackend wraps the handler that serves the request on this machine.
func NewLocalBackend(handler http.Handler, cfg LocalConfig) *LocalBackend {
	return &LocalBackend{handler: handler, cfg: cfg}
}

func (b *LocalBackend) ID() string   { return LocalID }
func (b *LocalBackend) State() State { return Online }
func (b *LocalBackend) Weight() int {
	if w, ok := b.WeightOverride(); ok {
		return w
	}
	return b.cfg.Weight
}

// Active is how many terminals are open on the gateway itself.
func (b *LocalBackend) Active() int64 {
	if b.cfg.Active == nil {
		return 0
	}
	return b.cfg.Active()
}

// ConfiguredWeight is the weight the gateway was started with (--local-weight).
func (b *LocalBackend) ConfiguredWeight() int { return b.cfg.Weight }

// WeightOverride is the weight an admin set from the dashboard, if one is set.
func (b *LocalBackend) WeightOverride() (int, bool) {
	if b.cfg.WeightOverride == nil {
		return 0, false
	}
	return b.cfg.WeightOverride(LocalID)
}

func (b *LocalBackend) HasLanguage(string) bool { return true }

func (b *LocalBackend) Capacity() (used, max int64) {
	if b.cfg.Capacity == nil {
		return 0, 0
	}
	return b.cfg.Capacity()
}

func (b *LocalBackend) Serve(w http.ResponseWriter, r *http.Request) error {
	b.handler.ServeHTTP(w, r)
	return nil
}
