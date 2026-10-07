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
	Version      string   `json:"version,omitempty"`
	Connected    string   `json:"connected,omitempty"` // since when this connection has been up
	// ConfigRev is the revision of the gateway's site rules the worker follows
	// (tunnel.WorkerConfig); ConfigCurrent says it is the gateway's own.
	ConfigRev     int64 `json:"configRev,omitempty"`
	ConfigCurrent bool  `json:"configCurrent,omitempty"`

	// Sync is set when workspace sync is on and a conversation with the worker is running.
	Sync *WorkerSync `json:"sync,omitempty"`
}

// WorkerSync is what the gateway knows about the sync of one worker.
type WorkerSync struct {
	Homes         int   `json:"homes"`
	ClockOffsetMs int64 `json:"clockOffsetMs"`
}

// SessionInfo is one row of GET /admin/sessions.
type SessionInfo struct {
	Key     string `json:"key"`
	UID     string `json:"uid,omitempty"`
	User    string `json:"user,omitempty"` // Config.UserLabel of the user
	Backend string `json:"backend"`
	Created string `json:"created"`
	Expires string `json:"expires,omitempty"`
	// Home is the session's home directory name, with workspace sync.
	Home string `json:"home,omitempty"`
	// Terminals is how many terminals the session has open now.
	Terminals int `json:"terminals"`
	// LastActive is when the session last made a request, with workspace sync.
	LastActive string `json:"lastActive,omitempty"`
}

// AdminHandler serves the gateway's own admin API. The caller mounts it under
// <prefix>admin/ behind its admin check; it is never forwarded to a worker.
//
//	GET  admin/workers               every backend with its state and load
//	POST admin/workers/<id>/drain    stop placing new sessions on a worker
//	POST admin/workers/<id>/undrain  resume
//	GET  admin/sessions              the execution contexts
//	POST admin/workers/<id>/reconnect  drop a worker's connection; it reconnects by itself
//	POST admin/sessions/<key>/end    close a session's terminals and forget its placement
//	POST admin/sessions/<key>/move   place a session on another node (body {"to": "<id>"})
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
		case len(parts) == 3 && parts[0] == "workers" && parts[2] == "reconnect":
			if !allow(w, r, http.MethodPost) {
				return
			}
			rt.reconnect(w, ts, parts[1])
		case len(parts) == 3 && parts[0] == "sessions" && (parts[2] == "end" || parts[2] == "move"):
			if !allow(w, r, http.MethodPost) {
				return
			}
			rt.sessionAction(w, r, parts[1], parts[2])
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

func (rt *Router) reconnect(w http.ResponseWriter, ts *tunnel.Server, id string) {
	if id == LocalID {
		http.Error(w, "the local backend has no connection to drop", http.StatusBadRequest)
		return
	}
	var tw *tunnel.Worker
	if ts != nil {
		tw = ts.Worker(id)
	}
	if tw == nil {
		http.Error(w, "no such worker", http.StatusNotFound)
		return
	}
	// The worker reconnects by itself, which starts the sync conversation
	// afresh; until then its sessions see it as away.
	tw.Disconnect()
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// sessionAction ends or moves a session. The body of a move is {"to": "<node>"}.
func (rt *Router) sessionAction(w http.ResponseWriter, r *http.Request, key, action string) {
	var (
		closed int
		err    error
	)
	switch action {
	case "end":
		closed, err = rt.EndSession(key)
	default:
		var body struct {
			To string `json:"to"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.To == "" {
			http.Error(w, `the body must be {"to": "<node id>"}`, http.StatusBadRequest)
			return
		}
		closed, err = rt.MoveSession(key, body.To)
	}
	switch {
	case err == ErrNoSession:
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		writeJSON(w, http.StatusOK, map[string]interface{}{"key": key, "terminalsClosed": closed})
	}
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
				info.Version = reg.Version
				info.Connected = tw.Connected().UTC().Format(time.RFC3339)
				info.ConfigRev = tw.ConfigRev()
				info.ConfigCurrent = info.ConfigRev != 0 && info.ConfigRev == ts.ConfigRevision()
			}
		}
		if rt.syncInfo != nil {
			if si, ok := rt.syncInfo(b.ID()); ok && si.Connected {
				info.Sync = &WorkerSync{Homes: si.Homes, ClockOffsetMs: si.ClockOffset.Milliseconds()}
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
		s.Home = ec.Home
		s.Terminals = rt.terminalsOf(ec.Key)
		if ec.UID != "" && rt.userLabel != nil {
			s.User = rt.userLabel(ec.UID)
		}
		if ec.Home != "" {
			rt.amu.Lock()
			a, ok := rt.activity[ec.Home]
			var last time.Time
			if ok {
				last = a.last
			}
			rt.amu.Unlock()
			if !last.IsZero() {
				s.LastActive = last.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, s)
	}
	return out
}
