package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// registerAdmin mounts the dashboard and its API on the site's mux. Like
// every /admin route they run on the gateway or standalone server itself and
// are never forwarded to a worker.
func (server *Server) registerAdmin(mux *http.ServeMux, prefix string) {
	server.admin.prefix = prefix
	if server.admin.started.IsZero() {
		server.admin.started = time.Now()
	}
	api := func(path string, h http.HandlerFunc) {
		mux.Handle(prefix+path, server.adminAPI(h))
	}

	mux.Handle(prefix+"admin", server.wrapAdmin(http.HandlerFunc(server.handleAdminPage)))
	api("admin/settings", server.handleAdminSettings)
	api("admin/keys", server.handleAdminKeys)
	api("admin/admins", server.handleAdminAdmins)
	api("admin/gateway", server.handleAdminGateway)
	api("admin/health", server.handleAdminHealth)
	api("admin/audit", server.handleAdminAudit)
	api("admin/stats", server.handleAdminStats)
	api("admin/logs", server.handleAdminLogs)
	api("admin/feedback", server.handleAdminFeedback)
	api("admin/feedback/", server.handleAdminFeedback)
	api("admin/snippets", server.handleAdminSnippets)
	api("admin/snippets/", server.handleAdminSnippets)
	api("admin/users", server.handleAdminUsers)
	api("admin/users/", server.handleAdminUsers)
	if server.options.Mode == ModeGateway {
		for _, p := range []string{"admin/workers", "admin/workers/", "admin/sessions", "admin/sessions/"} {
			api(p, server.handleGatewayAdmin)
		}
	}
}

// handleAdminPage serves the dashboard. The page itself holds no data; it
// loads everything from the API, which checks who is asking again.
func (server *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	data, err := Asset("static/admin.html")
	if err != nil {
		errorHandler(w, r, "The admin page is not built into this server.", http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", adminCSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	w.Write(data)
}

// adminCSP lets the dashboard run only its own script and load the site's
// fonts. Everything it shows from visitors is put on the page as text, and
// this policy would stop an inline script even if one slipped through.
const adminCSP = "default-src 'none'; script-src 'self'; style-src 'self' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// ---- site controls -------------------------------------------------------------

// terminalLanguage returns the language a terminal route starts, as the
// language picker names it ("python", "cpp"), and whether rel is a terminal
// route at all. The plain shell route has no language, "".
func (server *Server) terminalLanguage(rel string) (string, bool) {
	if _, ok := server.terminals[rel]; !ok {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(rel, "ws"), "_"), true
}

// wrapControls applies the switches an admin sets to new terminals and counts
// them for the dashboard's charts: maintenance mode and languages that are
// switched off refuse everybody but admins. It sits in front of the gateway,
// so it covers the terminals of workers as well. A worker checks only the people
// who open its own port (wrapWorkerControls, worker_config.go).
func (server *Server) wrapControls(next http.Handler, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang, ok := server.terminalLanguage(strings.TrimPrefix(r.URL.Path, prefix))
		if !ok || !isWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		reason := siteBlockReason(GetSiteSettings(), lang)
		if reason != "" && !server.isAdmin(w, r) {
			server.closeWithNotice(w, r, reason)
			return
		}
		server.admin.stats.record(visitorHash(r, time.Now().UTC().Format("2006-01-02")), lang)
		next.ServeHTTP(w, r)
	})
}

// isWebSocketUpgrade reports whether r asks to open a WebSocket.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// noticePrefix starts the close reason of a terminal that was refused on
// purpose; the page shows what follows (src/js/src/webtty.ts).
const noticePrefix = "site notice: "

// closeWithNotice completes a terminal's WebSocket handshake and closes it at
// once with a reason the page shows.
func (server *Server) closeWithNotice(w http.ResponseWriter, r *http.Request, reason string) {
	conn, err := server.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // the upgrader has answered
	}
	defer conn.Close()
	// A close reason is at most 123 bytes, and must stay valid UTF-8.
	msg := noticePrefix + reason
	for len(msg) > 120 {
		runes := []rune(msg)
		msg = string(runes[:len(runes)-1])
	}
	conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
		time.Now().Add(time.Second))
	// Let the page read the close and answer it; it sends its init message first.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
