package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"

	"gateway"
)

// Weights per node.
//
// A node's weight (its share of new sessions, gateway/pool.go) is fixed when it
// starts: --local-weight for the gateway, --worker-weight announced by a worker.
// An admin can set another from the dashboard (POST /admin/workers/<id>/weight).
// It is saved with the site settings (SiteSettings.NodeWeights), wins over the
// node's own, survives a restart of the gateway and a worker connecting again,
// and takes effect for the next new session; running sessions do not move.
// Removing it gives back the node's own weight.

// maxNodeWeight is the largest weight an admin can set: a node can be given a
// thousandth of the shares up to a thousand times the default (10), and a typo
// such as 50000 is refused.
const maxNodeWeight = 1000

// normalizeNodeWeights cleans the weights of an untrusted source: valid node
// ids, weights from 0 to maxNodeWeight.
func normalizeNodeWeights(in map[string]int) (map[string]int, error) {
	if len(in) > maxNodeLanguageNodes {
		return nil, fmt.Errorf("weights can be saved for at most %d nodes", maxNodeLanguageNodes)
	}
	out := map[string]int{}
	for id, w := range in {
		if id != "local" && !nodeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%q is not a node id", id)
		}
		if w < 0 || w > maxNodeWeight {
			return nil, fmt.Errorf("the weight of %s must be between 0 and %d", id, maxNodeWeight)
		}
		out[id] = w
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// NodeWeight is the weight an admin set for the node, if one is set.
func (s SiteSettings) NodeWeight(node string) (int, bool) {
	w, ok := s.NodeWeights[node]
	return w, ok
}

// withNodeWeight returns the settings with the weight set (set false: removed); s is not changed.
func (s SiteSettings) withNodeWeight(node string, weight int, set bool) SiteSettings {
	weights := map[string]int{}
	for id, w := range s.NodeWeights {
		weights[id] = w
	}
	if set {
		weights[node] = weight
	} else {
		delete(weights, node)
	}
	if len(weights) == 0 {
		weights = nil
	}
	s.NodeWeights = weights
	return s
}

// nodeWeightChanges describes the differences in the weights for the audit log.
func nodeWeightChanges(a, b SiteSettings) []string {
	ids := map[string]bool{}
	for id := range a.NodeWeights {
		ids[id] = true
	}
	for id := range b.NodeWeights {
		ids[id] = true
	}
	var sorted []string
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	var out []string
	for _, id := range sorted {
		old, hadOld := a.NodeWeights[id]
		now, hasNow := b.NodeWeights[id]
		switch {
		case hadOld && hasNow && old != now:
			out = append(out, fmt.Sprintf("changed the weight of %s from %d to %d", nodeLabel(id), old, now))
		case !hadOld && hasNow:
			out = append(out, fmt.Sprintf("set the weight of %s to %d (it had its own)", nodeLabel(id), now))
		case hadOld && !hasNow:
			out = append(out, fmt.Sprintf("gave %s its own weight back (was %d)", nodeLabel(id), old))
		}
	}
	return out
}

// takesNewSessions says whether the node would take new sessions with the weights the settings hold: it is online
// and its weight is above 0.
func takesNewSessions(b gateway.Backend, s SiteSettings) bool {
	if b.State() != gateway.Online {
		return false
	}
	if w, ok := s.NodeWeight(b.ID()); ok {
		return w > 0
	}
	if cw, ok := b.(interface{ ConfiguredWeight() int }); ok {
		return cw.ConfiguredWeight() > 0
	}
	return b.Weight() > 0
}

// someNodeTakesSessions says whether, with these settings, at least one node takes new sessions.
func (server *Server) someNodeTakesSessions(s SiteSettings) bool {
	if server.admin.router == nil {
		return true
	}
	for _, b := range server.admin.router.Backends() {
		if takesNewSessions(b, s) {
			return true
		}
	}
	return false
}

// nodeWeightReply is what the weight routes answer.
type nodeWeightReply struct {
	ID         string `json:"id"`
	Weight     int    `json:"weight"`
	Configured int    `json:"configuredWeight"`
	Set        bool   `json:"weightSet"`
	Max        int    `json:"maxWeight"`
}

func (server *Server) nodeWeightReply(node string) nodeWeightReply {
	reply := nodeWeightReply{ID: node, Max: maxNodeWeight}
	if server.admin.router != nil {
		if b, found := server.admin.router.Backend(node); found {
			reply.Weight = b.Weight()
			reply.Configured = b.Weight()
			if cw, ok := b.(interface{ ConfiguredWeight() int }); ok {
				reply.Configured = cw.ConfiguredWeight()
			}
		}
	}
	if w, ok := GetSiteSettings().NodeWeight(node); ok {
		reply.Weight, reply.Set = w, true
	}
	return reply
}

// handleNodeWeight serves /admin/workers/<id>/weight.
//
//	GET   the node's weight, the one it was started with, and whether an admin set it
//	POST  {"weight": 20} sets it; {"weight": null} (or {"reset": true}) gives the node's own back
func (server *Server) handleNodeWeight(w http.ResponseWriter, r *http.Request, node string) {
	if node != "local" && !nodeIDPattern.MatchString(node) {
		adminError(w, http.StatusBadRequest, "That is not a node id.")
		return
	}
	if !server.nodeKnown(node) {
		adminError(w, http.StatusNotFound, "No such node.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		adminJSON(w, http.StatusOK, server.nodeWeightReply(node))
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		var in struct {
			Weight *int `json:"weight"`
			Reset  bool `json:"reset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			adminError(w, http.StatusBadRequest, "The request was not valid JSON.")
			return
		}
		set := in.Weight != nil && !in.Reset
		weight := 0
		if set {
			weight = *in.Weight
			if weight < 0 || weight > maxNodeWeight {
				adminError(w, http.StatusBadRequest, fmt.Sprintf("The weight must be a whole number from 0 to %d.", maxNodeWeight))
				return
			}
		}
		if err := server.setNodeWeight(r, node, weight, set); err != nil {
			switch err {
			case errNoNodeTakesSessions:
				adminError(w, http.StatusConflict, "No node would take new sessions after this change. Keep at least one online node with a weight above 0.")
			case errSettingsConflict:
				adminError(w, http.StatusConflict, "Someone else changed the settings at the same moment. Try again.")
			case errSettingsUnavailable:
				adminError(w, http.StatusServiceUnavailable, "The settings store is not reachable right now, so nothing was saved. Try again in a moment.")
			default:
				log.Println("saving a node's weight failed: ", err)
				adminError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		adminJSON(w, http.StatusOK, server.nodeWeightReply(node))
	default:
		w.Header().Set("Allow", "GET, POST")
		adminError(w, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}

var errNoNodeTakesSessions = fmt.Errorf("no node would take new sessions")

// setNodeWeight saves one weight. It reads, changes and writes the whole settings, so a store that moved on in
// between (MongoDB, another admin) is retried. A change that leaves no node taking new sessions, when one took
// them before, is refused.
func (server *Server) setNodeWeight(r *http.Request, node string, weight int, set bool) error {
	var err error
	for try := 0; try < 3; try++ {
		before := GetSiteSettings()
		after := before.withNodeWeight(node, weight, set)
		if server.someNodeTakesSessions(before) && !server.someNodeTakesSessions(after) {
			return errNoNodeTakesSessions
		}
		if err = SaveSiteSettings(after, currentSettingsVersion()); err == errSettingsConflict {
			continue
		}
		if err != nil {
			return err
		}
		for _, change := range nodeWeightChanges(before, GetSiteSettings()) {
			server.audit(r, "settings", change)
		}
		return nil
	}
	return err
}
