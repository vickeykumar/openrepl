package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"cookie"
	"gateway"
	"tunnel"
	"user"
	"utils"
	"wsync"
)

// The admin dashboard (src/resources/admin.html) and the JSON API behind it.
// Everything under /admin goes through adminAPI: only an admin gets in, a
// change must carry the header the dashboard sends (a form on another site
// cannot add it), and nothing is cached.

const (
	adminHeader      = "X-Requested-With"
	adminHeaderValue = "openrepl-admin"
)

// adminState is what the dashboard needs to know about the running server.
type adminState struct {
	// check decides who is an admin. nil means IsUserAdmin; tests set it.
	check   func(http.ResponseWriter, *http.Request) bool
	router  *gateway.Router // gateway mode
	tunnel  *tunnel.Server  // gateway mode with workers enabled
	sync    *wsync.Manager  // gateway mode with workspace sync
	hostKey string          // the gateway's tunnel host key fingerprint
	started time.Time
	prefix  string // the site's path prefix, "/" unless a random URL is on
	counter *counter
	audit   auditLog
	stats   statsStore
}

// isAdmin reports whether the request comes from an admin.
func (server *Server) isAdmin(w http.ResponseWriter, r *http.Request) bool {
	if server.admin.check != nil {
		return server.admin.check(w, r)
	}
	return IsUserAdmin(w, r)
}

// adminRequestOK reports whether a request that changes something came from
// the dashboard: it carries the header, and its Origin, if any, is this site.
func adminRequestOK(r *http.Request) bool {
	if r.Header.Get(adminHeader) != adminHeaderValue {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// adminAPI wraps a handler of the admin API.
func (server *Server) adminAPI(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !server.isAdmin(w, r) {
			adminError(w, http.StatusUnauthorized, "Sign in as an admin.")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !adminRequestOK(r) {
				adminError(w, http.StatusForbidden, "This change did not come from the admin dashboard.")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func adminJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func adminError(w http.ResponseWriter, status int, msg string) {
	adminJSON(w, status, map[string]string{"error": msg})
}

// adminParts returns the parts of the path below admin/ for a request.
func (server *Server) adminParts(r *http.Request) []string {
	return adminPath(r, server.admin.prefix)
}

// adminPath returns the parts of the path below admin/, without the prefix.
func adminPath(r *http.Request, prefix string) []string {
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	rel = strings.Trim(strings.TrimPrefix(strings.Trim(rel, "/"), "admin"), "/")
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// ---- audit log ---------------------------------------------------------------

// auditFile is where the audit log is kept, one JSON object per line.
var auditFile = utils.GOTTY_PATH + "/admin-audit.jsonl"

const auditKeep = 500

// auditEntry is one change an admin made. The detail never holds a secret.
type auditEntry struct {
	Time   string `json:"time"`
	Admin  string `json:"admin"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	From   string `json:"from,omitempty"`
}

type auditLog struct {
	mu      sync.Mutex
	loaded  bool
	entries []auditEntry
}

func (a *auditLog) load() {
	if a.loaded {
		return
	}
	a.loaded = true
	f, err := os.Open(auditFile)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e auditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			a.entries = append(a.entries, e)
			if len(a.entries) > auditKeep {
				a.entries = a.entries[1:]
			}
		}
	}
}

func (a *auditLog) add(e auditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	a.entries = append(a.entries, e)
	if len(a.entries) > auditKeep {
		a.entries = a.entries[len(a.entries)-auditKeep:]
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(utils.GOTTY_PATH, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(auditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// recent returns the newest entries first.
func (a *auditLog) recent(limit int) []auditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	if limit <= 0 || limit > len(a.entries) {
		limit = len(a.entries)
	}
	out := make([]auditEntry, 0, limit)
	for i := len(a.entries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, a.entries[i])
	}
	return out
}

// adminActor names who made a request: the account's email address.
func adminActor(r *http.Request) string {
	session := cookie.Get_SessionCookie(r)
	if session.Uid == "" {
		return "unknown"
	}
	if up, err := user.FetchUserProfileData(session.Uid); err == nil && up.Email != "" {
		return up.Email
	}
	return session.Uid
}

// audit records a change an admin made.
func (server *Server) audit(r *http.Request, action, detail string) {
	server.admin.audit.add(auditEntry{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Admin:  adminActor(r),
		Action: action,
		Detail: detail,
		From:   clientIP(r),
	})
}

func (server *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	adminJSON(w, http.StatusOK, map[string]interface{}{"entries": server.admin.audit.recent(200)})
}
