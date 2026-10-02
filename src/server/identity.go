package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"containers"
	"cookie"
	"encoder"
	"trusted"
	"tunnel"
	"user"
	"utils"
)

// trustedConnKey marks requests that arrived over the gateway tunnel. Only
// those may carry the gateway's identity headers.
type trustedConnKey struct{}

// trustTunnel is the http.Server ConnContext of a worker: every connection
// it serves is a tunnel stream.
func trustTunnel(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, trustedConnKey{}, true)
}

func isTrusted(r *http.Request) bool {
	v, _ := r.Context().Value(trustedConnKey{}).(bool)
	return v
}

// requestIdentity returns the user id and workspace directory for a request.
// On a worker they come from the gateway's trusted headers. Everywhere else
// they come from the session cookie, as before.
func (server *Server) requestIdentity(w http.ResponseWriter, r *http.Request) (uid string, homedir string) {
	if isTrusted(r) {
		uid, homedir, _ = server.trustedIdentity(r)
		return uid, homedir
	}
	uid = cookie.Get_Uid(r)
	homedir = cookie.GetOrUpdateHomeDir(w, r, uid)
	if !homeOverridden(r) {
		server.routes.home(homedir)
	}
	return uid, homedir
}

// homeOverridden reports whether the request names its workspace itself (a
// fork link's jid or a shared session's homedir) instead of using its own.
func homeOverridden(r *http.Request) bool {
	return r.Form.Get("jid") != "" || r.Form.Has(utils.HOME_DIR_KEY)
}

// trustedIdentity resolves a forwarded request on a worker. The workspace
// follows the same order as cookie.GetOrUpdateHomeDir: a fork's jid, then an
// explicit homedir, then the requester's own directory.
func (server *Server) trustedIdentity(r *http.Request) (uid string, homedir string, privilege string) {
	id := trusted.FromHeader(r.Header)
	uid = id.UID
	privilege = utils.GUEST
	if id.Privilege == utils.ADMIN {
		privilege = utils.ADMIN
	}

	if ppid := encoder.DecodeToPID(r.Form.Get("jid")); ppid != -1 {
		return uid, containers.GetWorkingDir(ppid), privilege
	}
	if r.Form.Has(utils.HOME_DIR_KEY) {
		return uid, r.Form.Get(utils.HOME_DIR_KEY), privilege
	}

	homedir, err := trusted.HomeDir(utils.HOME_DIR, id)
	if err != nil {
		// The gateway always sends a home id or a guest id.
		log.Println("Error: forwarded request without a usable identity: ", err)
		homedir = user.GetHomeDir("")
	} else if _, serr := os.Stat(homedir); os.IsNotExist(serr) {
		os.MkdirAll(homedir, 0755)
	}
	if uid == "" {
		// A guest's directory is deleted after the same idle time as on a
		// single server.
		dir := homedir
		utils.GottyJobs.ResetJob(utils.REMOVE_JOB_KEY+dir, utils.DEADLINE_MINUTES*time.Minute, func() {
			utils.RemoveDir(dir)
		})
	}
	server.routes.home(homedir)
	return uid, homedir, privilege
}

// routeSink receives the keys this node owns. On a worker it is the tunnel
// client; on a gateway it is the router's route map for the local backend.
type routeSink interface {
	RouteOpen(kind, key string)
	RouteClose(kind, key string)
}

// homeRouteTTL is how long an unused workspace stays routable. A guest's
// directory is deleted after utils.DEADLINE_MINUTES of idleness.
const homeRouteTTL = 2 * utils.DEADLINE_MINUTES * time.Minute

// routeTracker announces the jids and workspaces this node owns, so that a
// fork link or a shared session opened from another session reaches it. A
// nil tracker (standalone mode) does nothing.
type routeTracker struct {
	sink routeSink

	mu        sync.Mutex
	homes     map[string]time.Time // workspace -> last use
	lastPrune time.Time
}

func newRouteTracker(sink routeSink) *routeTracker {
	return &routeTracker{sink: sink, homes: make(map[string]time.Time)}
}

// home records a use of a workspace, announcing it the first time.
func (t *routeTracker) home(dir string) {
	if t == nil || dir == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	_, known := t.homes[dir]
	t.homes[dir] = now
	var stale []string
	if now.Sub(t.lastPrune) > time.Minute {
		t.lastPrune = now
		for d, used := range t.homes {
			if now.Sub(used) > homeRouteTTL {
				stale = append(stale, d)
				delete(t.homes, d)
			}
		}
	}
	t.mu.Unlock()

	if !known {
		t.sink.RouteOpen(tunnel.RouteHome, dir)
	}
	for _, d := range stale {
		t.sink.RouteClose(tunnel.RouteHome, d)
	}
}

// jidOpen announces a running session's jid; the returned func withdraws it.
func (t *routeTracker) jidOpen(pid interface{}) func() {
	if t == nil {
		return func() {}
	}
	jid := encoder.EncodePID(pid)
	t.sink.RouteOpen(tunnel.RouteJID, jid)
	return func() { t.sink.RouteClose(tunnel.RouteJID, jid) }
}
