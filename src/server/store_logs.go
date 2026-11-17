package server

import (
	"net/http"
	"strconv"
	"strings"

	"appstore"
)

// handleAdminStoreLog is the admin page's read of an install log (LLD 18, section 13): GET /admin/store/log?app=<name>&node=<gateway|worker id>&attempt=<n, 0: newest>&from=<byte offset>.
// It answers {"node", "chunk"}: the attempts of the app, the attempt asked for, and its lines from the offset (a live tail asks again with "next"). A worker's chunk is
// asked of the worker over the tunnel and arrives with its next heartbeat, a few seconds.
func (server *Server) handleAdminStoreLog(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "error": appStoreErr})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	app := strings.ToLower(strings.TrimSpace(q.Get("app")))
	if !appstore.ValidName(app) {
		http.Error(w, "that is not an app", http.StatusBadRequest)
		return
	}
	attempt, _ := strconv.Atoi(q.Get("attempt"))
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	if from < 0 {
		from = 0
	}
	node := q.Get("node")
	if node == "" || node == "gateway" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"node": "gateway", "chunk": svc.ReadLog(app, attempt, from)})
		return
	}
	raw, err := askWorkerLog(r.Context(), node, app, attempt, from)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"node": node, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"node": node, "chunk": raw})
}
