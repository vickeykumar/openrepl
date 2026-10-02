package gateway

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"trusted"
)

// Identity names the session a request belongs to.
type Identity struct {
	Key     string
	UID     string
	GuestID string
	// Home is the home directory name; set only when Config.HomeOf is.
	Home string
}

// Picker chooses the backend for a session that has none yet. It is called
// once per session; the choice is then kept in the SessionRegistry.
type Picker interface {
	Pick(id Identity) (backendID string, err error)
}

// PinStore remembers which backend holds a signed-in user's workspace, so
// that the user returns to it after a gateway restart.
type PinStore interface {
	Get(uid string) (backendID string, ok bool)
	Set(uid, backendID string)
}

// ErrWorkspaceUnavailable means the backend that holds a signed-in user's
// files cannot take the session. The session is not placed anywhere else.
var ErrWorkspaceUnavailable = errors.New("workspace node unavailable")

// Config wires a Router to the existing server.
type Config struct {
	// Site serves everything that runs on the gateway: pages, account routes,
	// /admin and the local execution handlers.
	Site http.Handler
	// PathPrefix is the URL prefix the routes are mounted under, "/" or
	// "/<random>/".
	PathPrefix string
	// IsEntryPage reports whether a path (without the prefix) is a page that
	// starts a workspace; the session is assigned a backend when it is loaded.
	IsEntryPage func(rel string) bool
	// UID returns the signed-in user's id, or "" for a guest.
	UID func(r *http.Request) string
	// Secret signs the affinity cookie.
	Secret []byte
	// GuestTTL is how long an idle guest context lives.
	GuestTTL time.Duration
	// Picker chooses a backend for new sessions. The default is a Pool:
	// randomized weighted selection over the backends with free capacity.
	Picker Picker
	// Local describes the gateway's own backend: its weight and load.
	Local LocalConfig
	// Pins persists the backend of signed-in users. Optional.
	Pins PinStore
	// Terminal reports whether a path (without the prefix) starts a REPL,
	// and if so which command it runs and that command's memory weight.
	Terminal func(rel string) (command string, weight int64, ok bool)
	// HasLocalWorkspace reports whether a signed-in user already has files on
	// the gateway's disk. Such a user is always placed on the local backend,
	// so enabling workers never hides an existing workspace.
	HasLocalWorkspace func(uid string) bool
	// Identity returns what a worker should be told about the requester.
	Identity func(w http.ResponseWriter, r *http.Request, ec ExecutionContext) trusted.Identity
	// PrepareHome is called once for a session placed on a worker, before its
	// first request is forwarded: it makes the worker's copy of the home match
	// the gateway's, so a user moved to another worker finds their files.
	PrepareHome func(ctx context.Context, home, backendID string) error
	// OnMoved is called when a signed-in user is placed on another worker than
	// the one that held their files, so the old copy can be dropped.
	OnMoved func(home, from, to string)
	// RelocateAfter is how long a worker may be gone before the signed-in
	// users pinned to it are placed elsewhere. Default 2 minutes. A worker
	// that is draining gives up its users at once.
	RelocateAfter time.Duration
	// HomeOf names the home directory of a session. When set, the router
	// records it and, while a session's worker is away, serves the file
	// browser and uploads from the gateway's own copy of that home.
	HomeOf func(id Identity) string
	// TerminalNotice answers a terminal's WebSocket request by telling the
	// page that the session's worker is away and how long until the session is
	// placed again, and reports whether it handled the response. The page
	// shows a countdown. Optional: without it the answer is a plain 503.
	TerminalNotice func(w http.ResponseWriter, r *http.Request, retryIn time.Duration) bool
}

// Router sends each request either to the gateway's own handlers or, for
// execution-bound routes, to the backend that owns the session.
type Router struct {
	site     http.Handler
	prefix   string
	isEntry  func(string) bool
	uid      func(*http.Request) string
	affinity *Affinity
	registry *SessionRegistry
	routes   *RouteMap
	picker   Picker
	pins     PinStore
	terminal func(string) (string, int64, bool)
	hasLocal func(string) bool
	identity func(http.ResponseWriter, *http.Request, ExecutionContext) trusted.Identity
	homeOf   func(Identity) string
	notice   func(http.ResponseWriter, *http.Request, time.Duration) bool

	prepare       func(ctx context.Context, home, backendID string) error
	onMoved       func(home, from, to string)
	relocateAfter time.Duration
	started       time.Time
	offlineSince  map[string]time.Time // guarded by mu

	// picks counts, per backend, the sessions the picker has given it since
	// the gateway started (GET admin/workers shows it next to the weights).
	pickMu sync.Mutex
	picks  map[string]int64

	tunnelPath string
	tunnel     http.Handler

	// Per-home activity, kept when workspace sync is on so that the gateway
	// alone decides when a guest's home has been idle long enough.
	amu      sync.Mutex
	activity map[string]*homeActivity

	mu       sync.RWMutex
	backends map[string]Backend
}

// NewRouter creates a Router with the local backend registered.
func NewRouter(cfg Config) *Router {
	uid := cfg.UID
	if uid == nil {
		uid = func(*http.Request) string { return "" }
	}
	isEntry := cfg.IsEntryPage
	if isEntry == nil {
		isEntry = func(string) bool { return false }
	}
	local := NewLocalBackend(cfg.Site, cfg.Local)
	registryTTL := cfg.GuestTTL
	if cfg.HomeOf != nil {
		// With workspace sync a guest's context lives as long as its home:
		// ExpireGuests ends both together, after the guest has really been
		// idle, instead of the registry dropping the context on its own.
		registryTTL = 0
	}
	rt := &Router{
		site:     cfg.Site,
		prefix:   cfg.PathPrefix,
		isEntry:  isEntry,
		uid:      uid,
		affinity: NewAffinity(cfg.Secret, cfg.GuestTTL),
		registry: NewSessionRegistry(registryTTL),
		activity: make(map[string]*homeActivity),
		routes:   NewRouteMap(),
		picker:   cfg.Picker,
		pins:     cfg.Pins,
		terminal: cfg.Terminal,
		hasLocal: cfg.HasLocalWorkspace,
		identity: cfg.Identity,
		homeOf:   cfg.HomeOf,
		notice:   cfg.TerminalNotice,

		prepare:       cfg.PrepareHome,
		onMoved:       cfg.OnMoved,
		relocateAfter: cfg.RelocateAfter,
		started:       time.Now(),
		offlineSince:  make(map[string]time.Time),
		picks:         make(map[string]int64),
		backends:      map[string]Backend{local.ID(): local},
	}
	if rt.picker == nil {
		rt.picker = NewPool(rt.Backends)
	}
	if rt.relocateAfter <= 0 {
		rt.relocateAfter = 2 * time.Minute
	}
	return rt
}

// SetTunnel mounts the endpoint workers connect to at path (relative to the
// prefix, e.g. "api/tunnel"). Call it before the router serves requests.
func (rt *Router) SetTunnel(path string, handler http.Handler) {
	rt.tunnelPath = strings.Trim(path, "/")
	rt.tunnel = handler
}

// OwnerOf returns the backend that runs the sessions using a home.
func (rt *Router) OwnerOf(home string) (string, bool) { return rt.registry.HomeOwner(home) }

// Registry exposes the session registry (for the admin API and tests).
func (rt *Router) Registry() *SessionRegistry { return rt.registry }

// Routes exposes the cross-session route map.
func (rt *Router) Routes() *RouteMap { return rt.routes }

// AddBackend registers a backend, replacing one with the same id.
func (rt *Router) AddBackend(b Backend) {
	rt.mu.Lock()
	rt.backends[b.ID()] = b
	delete(rt.offlineSince, b.ID())
	rt.mu.Unlock()
}

// RemoveBackend unregisters b and forgets the routes it owned. A newer
// backend that has taken over the same id is left alone. Sessions assigned
// to the backend keep their context, so they fail instead of moving.
func (rt *Router) RemoveBackend(b Backend) {
	rt.mu.Lock()
	removed := rt.backends[b.ID()] == b
	if removed {
		delete(rt.backends, b.ID())
		rt.offlineSince[b.ID()] = time.Now()
	}
	rt.mu.Unlock()
	if removed {
		rt.routes.DropBackend(b.ID())
	}
}

// Backend returns the backend with the given id.
func (rt *Router) Backend(id string) (Backend, bool) {
	rt.mu.RLock()
	b, ok := rt.backends[id]
	rt.mu.RUnlock()
	return b, ok
}

// Backends returns the registered backends ordered by id.
func (rt *Router) Backends() []Backend {
	rt.mu.RLock()
	out := make([]Backend, 0, len(rt.backends))
	for _, b := range rt.backends {
		out = append(out, b)
	}
	rt.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// rel strips the mount prefix from the request path.
func (rt *Router) rel(path string) string {
	return strings.TrimPrefix(path, rt.prefix)
}

// IsAdmin reports whether the path is /admin or below it. Admin routes always
// run on the gateway.
func IsAdmin(rel string) bool {
	return rel == "admin" || strings.HasPrefix(rel, "admin/")
}

// IsFileRoute reports whether the path is the file browser or an upload,
// which the gateway can serve from its own copy of a home.
func IsFileRoute(rel string) bool { return rel == "ws_filebrowser" || rel == "upload_file" }

type workspaceKey struct{}
type ownWorkspaceKey struct{}

func withWorkspace(r *http.Request, home string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), workspaceKey{}, home))
}

// withOwnWorkspace is withWorkspace for a session the gateway runs itself.
func withOwnWorkspace(r *http.Request, home string) *http.Request {
	r = withWorkspace(r, home)
	return r.WithContext(context.WithValue(r.Context(), ownWorkspaceKey{}, true))
}

// WorkspaceOf returns the home a request must use instead of the one its
// cookie names: the gateway's copy of the session's home, which it serves
// from while the worker is away or runs the session in when it holds it.
func WorkspaceOf(r *http.Request) string {
	h, _ := r.Context().Value(workspaceKey{}).(string)
	return h
}

// OwnWorkspace reports whether the gateway itself runs the session that
// WorkspaceOf names the home of, as opposed to serving a worker's files while
// it is away.
func OwnWorkspace(r *http.Request) bool {
	own, _ := r.Context().Value(ownWorkspaceKey{}).(bool)
	return own
}

// IsExecutionBound reports whether the path runs user code or touches a
// user's workspace: the terminal WebSockets, the file browser and uploads.
func IsExecutionBound(rel string) bool {
	return rel == "ws" || strings.HasPrefix(rel, "ws_") || rel == "upload_file"
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := rt.rel(r.URL.Path)
	if rt.tunnel != nil && rel == rt.tunnelPath {
		rt.tunnel.ServeHTTP(w, r)
		return
	}
	// The trusted identity headers mean something only on a worker, and only
	// when the gateway set them. Drop whatever a client sent.
	trusted.Strip(r.Header)
	switch {
	case IsAdmin(rel):
		rt.site.ServeHTTP(w, r)
	case IsExecutionBound(rel):
		rt.execute(w, r)
	case rt.isEntry(rel):
		// Assign the session before the page loads so the parallel requests
		// the page makes afterwards all find the same backend.
		// The page is served even when no node can take the session; the
		// terminal then reports the problem.
		backendID := unassigned
		if ec, err := rt.assign(w, r); err == nil {
			backendID = ec.BackendID
			rt.touchHome(ec.Home)
		}
		ctx := context.WithValue(r.Context(), backendKey{}, backendID)
		rt.site.ServeHTTP(w, r.WithContext(ctx))
	default:
		rt.site.ServeHTTP(w, r)
	}
}

func (rt *Router) execute(w http.ResponseWriter, r *http.Request) {
	ec, err := rt.assign(w, r)
	if err == nil {
		rt.touchHome(ec.Home)
		if rt.terminal != nil {
			if _, _, isTerminal := rt.terminal(rt.rel(r.URL.Path)); isTerminal {
				// A terminal keeps its home in use for as long as it is open.
				rt.terminalOpened(ec.Home)
				defer rt.terminalClosed(ec.Home)
			}
		}
	}
	if err != nil {
		code := http.StatusServiceUnavailable
		if err == errIdentity {
			code = http.StatusInternalServerError
		}
		rt.refuseError(w, r, err, code)
		return
	}
	if b, found := rt.Backend(ec.BackendID); found && b.State() != Offline {
		if err := rt.prepareHome(r, ec); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	backendID := ec.BackendID
	owner, viaRoute := rt.routed(r)
	if viaRoute {
		backendID = owner
	}
	b, found := rt.Backend(backendID)
	if (!found || b.State() == Offline) && rt.homeOf != nil && ec.Home != "" && IsFileRoute(rt.rel(r.URL.Path)) && backendID == ec.BackendID {
		// The worker is away but the gateway has a copy of the home: serve
		// the file browser, downloads, saves and uploads from it. Terminals
		// cannot be served: the programs went with the worker.
		if local, ok := rt.Backend(LocalID); ok {
			if err := local.Serve(w, withWorkspace(r, ec.Home)); err != nil {
				log.Printf("gateway: backend %s: %v", local.ID(), err)
			}
			return
		}
	}
	if !found || b.State() == Offline {
		msg := "execution node unavailable"
		var retryIn time.Duration
		if backendID == ec.BackendID {
			if ec.UID != "" {
				msg = ErrWorkspaceUnavailable.Error()
			}
			if ec.Home != "" {
				retryIn = rt.retryIn(backendID)
			}
		}
		rt.refuse(w, r, msg, retryIn)
		return
	}
	if rt.terminal != nil {
		if command, weight, ok := rt.terminal(rt.rel(r.URL.Path)); ok {
			if !b.HasLanguage(command) {
				http.Error(w, "this language is not available on your execution node", http.StatusServiceUnavailable)
				return
			}
			r = withSessionWeight(r, weight)
		}
	}
	if b.ID() != LocalID {
		id := trusted.Identity{UID: ec.UID, Guest: ec.GuestID, Session: ec.Key}
		if rt.identity != nil {
			id = rt.identity(w, r, ec)
		}
		r = withIdentity(r, id)
		if rt.homeOf != nil && ec.Home != "" && backendID == ec.BackendID {
			// If the worker goes away under an open terminal, the browser is
			// told how long until the session is placed again.
			r = withAway(r, func() time.Duration { return rt.retryIn(backendID) })
		}
	} else if rt.homeOf != nil && ec.Home != "" && !viaRoute {
		// With workspace sync a session has one home name on every node, the
		// one a worker would use. A session the gateway runs itself therefore
		// works in the gateway's copy of that home: this is what a session
		// that was placed here after its worker was lost finds its files in.
		r = withOwnWorkspace(r, ec.Home)
	}
	if err := b.Serve(w, r); err != nil {
		log.Printf("gateway: backend %s: %v", b.ID(), err)
	}
}

type backendKey struct{}

// unassigned is what BackendOf returns for an entry page whose session could
// not be placed on any backend.
const unassigned = "-"

// BackendOf returns the backend an entry page's session was assigned to, "-"
// if it could not be placed, or "" when the request did not come through an
// entry page of a Router.
func BackendOf(r *http.Request) string {
	b, _ := r.Context().Value(backendKey{}).(string)
	return b
}

// routed returns the backend that owns the jid or homedir a request names.
// A fork link and a shared session's file requests come from another
// session, so they follow the key instead of the requester's own affinity.
func (rt *Router) routed(r *http.Request) (string, bool) {
	q := r.URL.Query()
	if jid := q.Get("jid"); jid != "" {
		if b, ok := rt.routes.Lookup(RouteJID, jid); ok {
			return b, true
		}
	}
	if home := q.Get("homedir"); home != "" {
		if b, ok := rt.routes.Lookup(RouteHome, home); ok {
			return b, true
		}
	}
	return "", false
}

// Route kinds in the RouteMap.
const (
	RouteJID  = "jid"
	RouteHome = "home"
)

// errIdentity means the guest id cookie could not be created.
var errIdentity = errors.New("internal error")

// assign returns the request's execution context, creating one (and the guest
// id cookie) if the session has none yet.
func (rt *Router) assign(w http.ResponseWriter, r *http.Request) (ExecutionContext, error) {
	id, err := rt.identify(w, r)
	if err != nil {
		return ExecutionContext{}, err
	}
	replaced := ""
	if ec, found := rt.registry.Resolve(id.Key); found {
		if !rt.goneForGood(ec) {
			rt.registry.Touch(id.Key)
			return ec, nil
		}
		// The worker that held the session has been gone for longer than the
		// grace period. Its programs are lost, and the gateway has the files,
		// so the session is placed again, on a worker that is sent them.
		replaced = ec.BackendID
		rt.registry.Release(id.Key)
	}
	backendID, from, err := rt.place(id)
	if err != nil {
		return ExecutionContext{}, err
	}
	if from == "" {
		from = replaced
	}
	if from != "" && from != backendID && rt.onMoved != nil && id.Home != "" {
		rt.onMoved(id.Home, from, backendID)
	}
	return rt.registry.Create(id, backendID), nil
}

// goneForGood reports whether the worker of a session has been away longer
// than the grace period, so that the session can be placed elsewhere. It is
// only ever true with workspace sync, which is what makes the files available
// on the gateway.
func (rt *Router) goneForGood(ec ExecutionContext) bool {
	if rt.homeOf == nil || ec.BackendID == LocalID || ec.Home == "" {
		return false
	}
	if _, found := rt.Backend(ec.BackendID); found {
		return false
	}
	return rt.goneLongEnough(ec.BackendID)
}

// goneLongEnough reports whether a backend that is not connected has been
// away for the grace period. After a gateway restart every worker gets the
// whole period to reconnect.
func (rt *Router) goneLongEnough(id string) bool {
	return time.Since(rt.awaySince(id)) >= rt.relocateAfter
}

// errPrepare means the chosen worker could not be given the session's home.
var errPrepare = errors.New("the workspace could not be prepared on the execution node")

// prepareHome makes the backend's copy of the session's home match the
// gateway's, once per session, before the first request is forwarded.
func (rt *Router) prepareHome(r *http.Request, ec ExecutionContext) error {
	if rt.prepare == nil || ec.Prepared || ec.Home == "" || ec.BackendID == LocalID {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := rt.prepare(ctx, ec.Home, ec.BackendID); err != nil {
		log.Printf("gateway: preparing home %s on %s: %v", ec.Home, ec.BackendID, err)
		return errPrepare
	}
	rt.registry.MarkPrepared(ec.Key)
	return nil
}

// place chooses the backend for a session that has none. A signed-in user
// goes back to where their files are; everyone else is placed by the picker.
// from is set when a user is placed on another backend than the one that
// held their files.
func (rt *Router) place(id Identity) (backendID, from string, err error) {
	if id.UID != "" {
		if rt.pins != nil {
			if pinned, ok := rt.pins.Get(id.UID); ok {
				b, found := rt.Backend(pinned)
				switch {
				case found && (b.State() == Online || b.State() == Syncing):
					// Syncing: the worker is catching up with the gateway and
					// serves this user's home as soon as that home is in step.
					return pinned, "", nil
				case rt.canRelocate(pinned, found, b):
					// Their files are on the gateway as well, so they can be
					// placed on another worker, which is sent a copy first.
					from = pinned
				default:
					// Starting somewhere else would show the user an empty
					// workspace, so fail instead.
					return "", "", rt.withRetry(ErrWorkspaceUnavailable, pinned)
				}
			}
		}
		// With workspace sync the files on the gateway are a copy that is sent
		// to whichever worker is chosen, so they no longer pin the user here.
		if from == "" && rt.homeOf == nil && rt.hasLocal != nil && rt.hasLocal(id.UID) {
			return LocalID, "", nil
		}
	}
	backendID, err = rt.picker.Pick(id)
	if err != nil {
		return "", "", err
	}
	rt.countPick(backendID)
	if id.UID != "" && rt.pins != nil {
		rt.pins.Set(id.UID, backendID)
	}
	if backendID == from {
		from = ""
	}
	return backendID, from, nil
}

// canRelocate reports whether the users pinned to a backend may be placed
// elsewhere: only with workspace sync (the gateway holds their files), and
// only when the backend is draining or has been gone for the grace period.
func (rt *Router) canRelocate(pinned string, found bool, b Backend) bool {
	if rt.homeOf == nil || pinned == LocalID {
		return false
	}
	if found {
		return b.State() == Draining
	}
	return rt.goneLongEnough(pinned)
}

func (rt *Router) identify(w http.ResponseWriter, r *http.Request) (Identity, error) {
	if uid := rt.uid(r); uid != "" {
		return rt.withHome(Identity{Key: "u:" + uid, UID: uid}), nil
	}
	guest := rt.affinity.GuestID(r)
	if guest == "" {
		var err error
		if guest, err = rt.newGuest(w); err != nil {
			log.Printf("gateway: issuing guest id: %v", err)
			return Identity{}, errIdentity
		}
	}
	return rt.withHome(Identity{Key: "g:" + guest, GuestID: guest}), nil
}

func (rt *Router) withHome(id Identity) Identity {
	if rt.homeOf != nil {
		id.Home = rt.homeOf(id)
	}
	return id
}

// newGuest issues a guest id that no live session is using. The id is short
// (it names the guest's folder), so a clash with another current guest is
// checked for rather than left to chance; two guests with one id would share
// a workspace.
func (rt *Router) newGuest(w http.ResponseWriter) (string, error) {
	for attempt := 0; ; attempt++ {
		id, err := NewGuestID()
		if err != nil {
			return "", err
		}
		if _, taken := rt.registry.Resolve("g:" + id); taken && attempt < 8 {
			continue
		}
		return id, rt.affinity.Set(w, id)
	}
}
