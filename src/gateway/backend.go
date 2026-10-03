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
	// Capacity reports the gateway's own load. Optional.
	Capacity func() (used, max int64)
}

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

func (b *LocalBackend) ID() string              { return LocalID }
func (b *LocalBackend) State() State            { return Online }
func (b *LocalBackend) Weight() int             { return b.cfg.Weight }
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
