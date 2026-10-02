package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"tunnel"
)

// WorkerInfo is one row of GET /admin/workers. The local backend is listed
// too, with the worker-only fields left empty.
type WorkerInfo struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Weight   int    `json:"weight"`
	UsedMB   int64  `json:"usedMB"`
	MaxMB    int64  `json:"maxMB"` // 0 means no limit
	Sessions int    `json:"sessions"`

	// Picked is how many sessions the weighted random choice has given this
	// node since the gateway started, PickedPercent its share of all such
	// sessions, and WeightPercent the share it should get by weight among the
	// nodes that take new sessions now (0 for one that does not). Over many
	// sessions the first two approach the third. A session that was not
	// picked, such as a signed-in user going back to their worker, is not
	// counted.
	Picked        int64   `json:"picked"`
	PickedPercent float64 `json:"pickedPercent"`
	WeightPercent float64 `json:"weightPercent"`

	Terminals    int64    `json:"terminals,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	RemoteAddr   string   `json:"remoteAddr,omitempty"`
	LastSeen     string   `json:"lastSeen,omitempty"`
	ConnectionID string   `json:"connectionId,omitempty"`
	OS           string   `json:"os,omitempty"`
	Arch         string   `json:"arch,omitempty"`
}

// SessionInfo is one row of GET /admin/sessions.
type SessionInfo struct {
	Key     string `json:"key"`
	UID     string `json:"uid,omitempty"`
	Backend string `json:"backend"`
	Created string `json:"created"`
	Expires string `json:"expires,omitempty"`
}

// AdminHandler serves the gateway's own admin API. The caller mounts it under
// <prefix>admin/ behind its admin check; it is never forwarded to a worker.
//
//	GET  admin/workers               every backend with its state and load
//	POST admin/workers/<id>/drain    stop placing new sessions on a worker
//	POST admin/workers/<id>/undrain  resume
//	GET  admin/sessions              the execution contexts
//
// ts is nil when workers are disabled.
func (rt *Router) AdminHandler(ts *tunnel.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.Trim(strings.TrimPrefix(rt.rel(r.URL.Path), "admin/"), "/")
		parts := strings.Split(rel, "/")
		switch {
		case rel == "workers":
			if !allow(w, r, http.MethodGet) {
				return
			}
			total := rt.pickTotal()
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"workers":     rt.workerInfo(ts),
				"pickedTotal": total,
				"pickedSince": rt.started.UTC().Format(time.RFC3339),
			})
		case rel == "sessions":
			if !allow(w, r, http.MethodGet) {
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": rt.sessionInfo()})
		case len(parts) == 3 && parts[0] == "workers" && (parts[2] == "drain" || parts[2] == "undrain"):
			if !allow(w, r, http.MethodPost) {
				return
			}
			rt.drain(w, ts, parts[1], parts[2] == "drain")
		default:
			http.NotFound(w, r)
		}
	})
}

func allow(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (rt *Router) drain(w http.ResponseWriter, ts *tunnel.Server, id string, on bool) {
	if id == LocalID {
		http.Error(w, "the local backend cannot be drained; start the gateway with --local-weight 0 instead", http.StatusBadRequest)
		return
	}
	if ts == nil || !ts.SetDraining(id, on) {
		http.Error(w, "no such worker", http.StatusNotFound)
		return
	}
	state := Online
	if b, ok := rt.Backend(id); ok {
		state = b.State()
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "state": state.String()})
}

func (rt *Router) workerInfo(ts *tunnel.Server) []WorkerInfo {
	sessions := map[string]int{}
	for _, ec := range rt.registry.Snapshot() {
		sessions[ec.BackendID]++
	}
	backends := rt.Backends()
	// The shares nodes should get by weight: among those that take new
	// sessions now, as the pool decides.
	weightSum := 0
	for _, b := range backends {
		if b.State() == Online && b.Weight() > 0 {
			weightSum += b.Weight()
		}
	}
	picks, total := rt.pickCounts()
	out := make([]WorkerInfo, 0, len(backends))
	for _, b := range backends {
		used, max := b.Capacity()
		info := WorkerInfo{
			ID:       b.ID(),
			State:    b.State().String(),
			Weight:   b.Weight(),
			UsedMB:   used,
			MaxMB:    max,
			Sessions: sessions[b.ID()],
			Picked:   picks[b.ID()],
		}
		if total > 0 {
			info.PickedPercent = percent(float64(info.Picked), float64(total))
		}
		if b.State() == Online && b.Weight() > 0 {
			info.WeightPercent = percent(float64(b.Weight()), float64(weightSum))
		}
		if ts != nil {
			if tw := ts.Worker(b.ID()); tw != nil {
				reg := tw.Info()
				info.Terminals = tw.Active()
				info.Languages = reg.Languages
				info.RemoteAddr = tw.RemoteAddr()
				info.LastSeen = tw.LastSeen().UTC().Format(time.RFC3339)
				info.ConnectionID = tw.ConnectionID()
				info.OS, info.Arch = reg.OS, reg.Arch
			}
		}
		out = append(out, info)
	}
	return out
}

// percent is part as a percentage of whole, to one decimal.
func percent(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(part/whole*1000) / 10
}

func (rt *Router) sessionInfo() []SessionInfo {
	snap := rt.registry.Snapshot()
	out := make([]SessionInfo, 0, len(snap))
	for _, ec := range snap {
		s := SessionInfo{
			Key:     ec.Key,
			UID:     ec.UID,
			Backend: ec.BackendID,
			Created: ec.CreatedAt.UTC().Format(time.RFC3339),
		}
		if !ec.ExpiresAt.IsZero() {
			s.Expires = ec.ExpiresAt.UTC().Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out
}
