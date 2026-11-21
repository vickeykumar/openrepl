package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The request for a connection a plugin asks for (canvas board 9). A plugin from a link that names a connection is not installed until an admin
// approves it (or the admin switched "Allow connections without approval" on). The request holds the plugin's own text as the server fetched it,
// never what the page says, and is tied to its digest: a plugin that changes goes back to review. An approval is for the site or for one person.

var connReqMu sync.Mutex

const (
	maxWaitingPerUser = 10
	maxConnRequests   = 500
)

type connRequest struct {
	ID      string          `json:"id"`
	User    string          `json:"user"`
	Plugin  string          `json:"plugin"`
	Version string          `json:"version"`
	Source  string          `json:"source"`
	Digest  string          `json:"digest"`
	Conns   []connectionDef `json:"connections"`
	Skills  []string        `json:"skills"`
	Note    string          `json:"note,omitempty"`
	Status  string          `json:"status"` // waiting, approved, declined
	Scope   string          `json:"scope,omitempty"`
	// a request for a server (Kind "server"; a plugin's request has none): what the person typed, as text, and what became of it
	Kind    string `json:"kind,omitempty"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Signin  string `json:"signin,omitempty"` // what the person thinks: oauth, token or unknown
	Docs    string `json:"docs,omitempty"`
	DraftID string `json:"draftId,omitempty"` // the integration an approval made
	Reason  string `json:"reason,omitempty"`
	At      string `json:"at"`
	Decided string `json:"decided,omitempty"`
}

// the connections a plugin names, read from its plugin.json: {"id":"github"} (the site's catalogue) or {"id":"acme","title":"Acme","url":"https://…"}
func parsePluginConnections(raws []json.RawMessage, fromLink bool) ([]connectionDef, error) {
	if len(raws) > 5 {
		return nil, fmt.Errorf("a plugin may name at most 5 connections")
	}
	var out []connectionDef
	seen := map[string]bool{}
	for _, raw := range raws {
		var c struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal(raw, &c); err != nil || !connIDPattern.MatchString(c.ID) {
			return nil, fmt.Errorf("a connection in plugin.json needs an id of lower case letters, digits and dashes")
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("the connection %q is named twice", c.ID)
		}
		seen[c.ID] = true
		if def, ok := catalogueConnection(c.ID); ok {
			if c.URL != "" && c.URL != def.URL {
				return nil, fmt.Errorf("the connection %q is the site's own: its address cannot be changed", c.ID)
			}
			out = append(out, def)
			continue
		}
		if !fromLink {
			return nil, fmt.Errorf("the connection %q is not one the site offers", c.ID)
		}
		if c.URL == "" || strings.TrimSpace(c.Title) == "" || len([]rune(c.Title)) > 40 {
			return nil, fmt.Errorf("the connection %q needs a title (at most 40 characters) and the https address of its MCP server", c.ID)
		}
		if _, err := connectionURLOK(c.URL); err != nil {
			return nil, fmt.Errorf("the connection %q: %v", c.ID, err)
		}
		out = append(out, connectionDef{ID: c.ID, Title: clean1Line(c.Title, 40), URL: strings.TrimSpace(c.URL), Custom: true})
	}
	return out, nil
}

func connectionHosts(defs []connectionDef) []string {
	var hs []string
	for _, d := range defs {
		if h, err := connectionURLOK(d.URL); err == nil {
			hs = append(hs, h)
		}
	}
	return hs
}

// connAccess says where a plugin's connections stand for a person: none, official (from the catalogue), open (the admin lets them without approval),
// approved, waiting, declined, needed (a request can be sent), host_denied, off.
func connAccess(g GenieSettings, uid string, pkg *pluginPackage, fromLink bool) string {
	if len(pkg.Connections) == 0 {
		return "none"
	}
	if g.ConnectionsOff {
		return "off"
	}
	if !fromLink {
		return "official"
	}
	if g.ConnectionsNoApproval {
		for _, h := range connectionHosts(pkg.Connections) {
			if !hostAllowed(g, h) {
				return "host_denied"
			}
		}
		return "open"
	}
	connReqMu.Lock()
	defer connReqMu.Unlock()
	state := "needed"
	for _, r := range readConnRequests() {
		if r.Digest != pkg.Digest {
			continue
		}
		if r.Status == "approved" && (r.Scope == "site" || r.User == uid) {
			return "approved"
		}
		if r.User == uid && r.Status == "waiting" {
			state = "waiting"
		} else if r.User == uid && r.Status == "declined" && state == "needed" {
			state = "declined"
		}
	}
	return state
}

// createConnRequest records a person's request. The same plugin, as the same digest, waiting already, is not asked for twice.
func createConnRequest(uid string, pkg *pluginPackage, note string) (connRequest, error) {
	connReqMu.Lock()
	defer connReqMu.Unlock()
	all := readConnRequests()
	waiting := 0
	for _, r := range all {
		if r.User == uid && r.Status == "waiting" {
			if r.Digest == pkg.Digest {
				return r, nil
			}
			waiting++
		}
	}
	if waiting >= maxWaitingPerUser {
		return connRequest{}, fmt.Errorf("you have %d requests waiting: wait for an answer", waiting)
	}
	idb := make([]byte, 6)
	rand.Read(idb)
	var skills []string
	for _, s := range pkg.Skills {
		skills = append(skills, s.Name)
	}
	r := connRequest{ID: hex.EncodeToString(idb), User: uid, Plugin: pkg.Manifest.Name, Version: pkg.Manifest.Version, Source: pkg.Source, Digest: pkg.Digest,
		Conns: pkg.Connections, Skills: skills, Note: clipRunes(strings.TrimSpace(note), 300), Status: "waiting", At: time.Now().UTC().Format(time.RFC3339)}
	all = append(all, r)
	if len(all) > maxConnRequests {
		all = all[len(all)-maxConnRequests:]
	}
	if err := writeConnRequests(all); err != nil {
		return connRequest{}, fmt.Errorf("the request could not be kept")
	}
	return r, nil
}

func userConnRequests(uid string) []connRequest {
	connReqMu.Lock()
	defer connReqMu.Unlock()
	out := []connRequest{}
	all := readConnRequests()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].User == uid {
			out = append(out, all[i])
		}
	}
	return out
}

// approvedCustomConnections are the connections of other addresses that an admin approved for this person (or for the whole site).
func approvedCustomConnections(uid string) []connectionDef {
	connReqMu.Lock()
	defer connReqMu.Unlock()
	var out []connectionDef
	seen := map[string]bool{}
	for _, r := range readConnRequests() {
		if r.Status != "approved" || (r.Scope != "site" && r.User != uid) {
			continue
		}
		for _, d := range r.Conns {
			if d.Custom && !seen[d.ID] {
				seen[d.ID] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// ---- the admin's side -------------------------------------------------------------------------------------------------------------

// GET admin/connection-requests: every request, the ones waiting first. POST {id, action: approve | approve_user | decline, reason}.
func (server *Server) handleAdminConnectionRequests(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		connReqMu.Lock()
		all := readConnRequests()
		connReqMu.Unlock()
		sort.SliceStable(all, func(i, j int) bool {
			if (all[i].Status == "waiting") != (all[j].Status == "waiting") {
				return all[i].Status == "waiting"
			}
			return all[i].At > all[j].At
		})
		if len(all) > 100 {
			all = all[:100]
		}
		if all == nil {
			all = []connRequest{}
		}
		adminJSON(w, http.StatusOK, map[string]interface{}{"requests": all, "waiting": countWaiting(all)})
	case http.MethodPost:
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			adminError(w, http.StatusBadRequest, "Send {id, action}.")
			return
		}
		if in.Action != "approve" && in.Action != "approve_user" && in.Action != "decline" && in.Action != "customize" {
			adminError(w, http.StatusBadRequest, "The action is approve, approve_user, customize or decline.")
			return
		}
		// a request for a server (genie_integrations_requests.go)
		connReqMu.Lock()
		isServer := false
		for _, rq := range readConnRequests() {
			if rq.ID == in.ID && rq.Kind == "server" {
				isServer = true
			}
		}
		connReqMu.Unlock()
		if isServer {
			server.decideServerRequest(w, r, in.ID, in.Action, in.Reason)
			return
		}
		if in.Action == "customize" {
			adminError(w, http.StatusBadRequest, "Only a request for a server can be customized.")
			return
		}
		connReqMu.Lock()
		all := readConnRequests()
		found := -1
		for i := range all {
			if all[i].ID == in.ID {
				found = i
			}
		}
		if found < 0 {
			connReqMu.Unlock()
			adminError(w, http.StatusNotFound, "There is no such request.")
			return
		}
		rq := &all[found]
		rq.Decided = time.Now().UTC().Format(time.RFC3339)
		switch in.Action {
		case "approve":
			rq.Status, rq.Scope = "approved", "site"
		case "approve_user":
			rq.Status, rq.Scope = "approved", "user"
		default:
			rq.Status, rq.Scope, rq.Reason = "declined", "", clipRunes(strings.TrimSpace(in.Reason), 200)
		}
		err := writeConnRequests(all)
		done := *rq
		connReqMu.Unlock()
		if err != nil {
			adminError(w, http.StatusInternalServerError, "The answer could not be kept.")
			return
		}
		server.audit(r, "connection request", fmt.Sprintf("%s: %s %s from %s (%s)", in.Action, done.Plugin, done.Version, done.Source, done.ID))
		adminJSON(w, http.StatusOK, map[string]interface{}{"request": done})
	default:
		w.Header().Set("Allow", "GET, POST")
		adminError(w, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}

func countWaiting(all []connRequest) int {
	n := 0
	for _, r := range all {
		if r.Status == "waiting" {
			n++
		}
	}
	return n
}

// GET admin/connections: who is connected to what (never the tokens), and the newest calls (the connection, the tool and the outcome, never the arguments or the result).
func (server *Server) handleAdminConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		adminError(w, http.StatusMethodNotAllowed, "Use GET.")
		return
	}
	people := connectedPeople()
	adminJSON(w, http.StatusOK, map[string]interface{}{"connected": people, "calls": recentConnCalls(100)})
}

func recentConnCalls(n int) []connCallEntry {
	connLogMu.Lock()
	defer connLogMu.Unlock()
	entries, err := os.ReadDir(connDir)
	out := []connCallEntry{}
	if err != nil {
		return out
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "calls-") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(connDir, name))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			var e connCallEntry
			if json.Unmarshal([]byte(lines[i]), &e) == nil && e.Conn != "" {
				out = append(out, e)
			}
		}
		if len(out) >= n {
			break
		}
	}
	return out
}
