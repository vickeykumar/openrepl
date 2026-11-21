package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cookie"
)

// A person's request for a server (docs/lld/19 section 25, canvas board 9). It is only text: a name, an address, what they want Genie to do with it and
// what they think the sign-in is. Nothing contacts the address and nothing is connected until an admin answers: Approve for this person, Approve for
// everyone, Customize (a draft only) or Decline. An approval makes the integration (or finds the one with that address), tests it, switches on
// the tools the server calls read-only, and publishes it when that is enough; otherwise it stays a draft for the admin to finish.

func sameAddress(a, b string) bool {
	norm := func(s string) string {
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil {
			return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/"))
		}
		return strings.ToLower(u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"))
	}
	return norm(a) == norm(b)
}

// requestRow is a request as its person sees it.
type requestRow struct {
	connRequest
	Available   bool   `json:"available,omitempty"`   // approved, and the person can use it now
	Integration string `json:"integration,omitempty"` // its id
}

func requestsForPerson(uid string) []requestRow {
	email, admin := personOf(uid)
	out := []requestRow{}
	for _, r := range userConnRequests(uid) {
		row := requestRow{connRequest: r}
		if r.Kind == "server" && r.Status == "approved" && r.DraftID != "" {
			if i, ok := findIntegration(r.DraftID); ok {
				row.Integration = i.ID
				row.Available = i.Status == integPublished && i.visibleTo(uid, email, admin)
			}
		}
		out = append(out, row)
	}
	return out
}

// POST genie_conn {"op":"ask","title","address","why","signin","docs"}
func (server *Server) connectionAsk(rw http.ResponseWriter, uid string, in connRequestBody, reply func(interface{})) {
	title := clean1Line(strings.TrimSpace(in.Title), 40)
	if title == "" {
		jsonError(rw, http.StatusBadRequest, "bad_request", "Give the server a name.")
		return
	}
	addr := strings.TrimSpace(in.Address)
	if _, err := integrationURLOK(addr); err != nil {
		jsonError(rw, http.StatusBadRequest, "bad_address", err.Error()+".")
		return
	}
	why := clipRunes(strings.TrimSpace(in.Why), 300)
	signin := in.Signin
	if signin != "oauth" && signin != "token" && signin != "unknown" {
		signin = ""
	}
	docs := strings.TrimSpace(in.Docs)
	if u, err := url.Parse(docs); docs != "" && (err != nil || u.Scheme != "https" || u.Host == "" || len(docs) > 200) {
		docs = "" // kept only as text, and only when it is an address
	}
	email, admin := personOf(uid)
	for _, i := range registry() {
		if sameAddress(i.URL, addr) && i.Status == integPublished && i.visibleTo(uid, email, admin) {
			jsonError(rw, http.StatusConflict, "already_available", i.Title+" is already available to you: enable it in the Connections tab.")
			return
		}
	}
	connReqMu.Lock()
	defer connReqMu.Unlock()
	all := readConnRequests()
	waiting := 0
	for _, r := range all {
		if r.User == uid && r.Status == "waiting" {
			if r.Kind == "server" && sameAddress(r.URL, addr) {
				reply(map[string]interface{}{"request": requestRow{connRequest: r}})
				return
			}
			waiting++
		}
	}
	if waiting >= maxWaitingPerUser {
		jsonError(rw, http.StatusTooManyRequests, "too_many", "You have requests waiting: wait for an answer.")
		return
	}
	idb := make([]byte, 6)
	rand.Read(idb)
	r := connRequest{ID: hex.EncodeToString(idb), User: uid, Kind: "server", Title: title, URL: addr, Note: why, Signin: signin, Docs: docs, Status: "waiting", At: time.Now().UTC().Format(time.RFC3339)}
	all = append(all, r)
	if len(all) > maxConnRequests {
		all = all[len(all)-maxConnRequests:]
	}
	if err := writeConnRequests(all); err != nil {
		jsonError(rw, http.StatusInternalServerError, "save", "The request could not be kept.")
		return
	}
	logConnCall(connCallEntry{User: uid, Conn: "(request)", Op: "ask", OK: true})
	reply(map[string]interface{}{"request": requestRow{connRequest: r}})
}

// ---- the admin's answer --------------------------------------------------------------------------------------------------------------------------

// decideServerRequest answers a request for a server: approve_user, approve, customize or decline.
func (server *Server) decideServerRequest(w http.ResponseWriter, r *http.Request, id, action, reason string) {
	connReqMu.Lock()
	all := readConnRequests()
	k := -1
	for n := range all {
		if all[n].ID == id {
			k = n
		}
	}
	if k < 0 {
		connReqMu.Unlock()
		adminError(w, http.StatusNotFound, "There is no such request.")
		return
	}
	rq := all[k]
	connReqMu.Unlock()
	if rq.Kind != "server" {
		adminError(w, http.StatusBadRequest, "That request is not for a server.")
		return
	}
	if rq.Status != "waiting" {
		adminError(w, http.StatusConflict, "That request was answered already.")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result := map[string]interface{}{}
	switch action {
	case "decline":
		rq.Status, rq.Scope, rq.Reason = "declined", "", clipRunes(strings.TrimSpace(reason), 200)
	case "approve", "approve_user", "customize":
		integ, published, why, err := server.integrationFromRequest(r, rq, action)
		if err != nil {
			adminError(w, http.StatusBadRequest, err.Error()+".")
			return
		}
		rq.Status, rq.DraftID = "approved", integ.ID
		rq.Scope = map[string]string{"approve": "site", "approve_user": "user", "customize": "draft"}[action]
		result["integration"] = viewOfIntegration(integ, usageOfIntegrations(), GetSiteSettings().IntegrationSecrets)
		result["published"] = published
		result["why"] = why
	default:
		adminError(w, http.StatusBadRequest, "The action is approve_user, approve, customize or decline.")
		return
	}
	rq.Decided = now
	connReqMu.Lock()
	all = readConnRequests()
	for n := range all {
		if all[n].ID == id {
			all[n] = rq
		}
	}
	err := writeConnRequests(all)
	connReqMu.Unlock()
	if err != nil {
		adminError(w, http.StatusInternalServerError, "The answer could not be kept.")
		return
	}
	server.audit(r, "connection request", action+": server "+rq.Title+" ("+rq.ID+")")
	result["request"] = rq
	adminJSON(w, http.StatusOK, result)
}

// integrationFromRequest finds or makes the integration of a request, sets who sees it, and, for an approval, tests it and publishes it when it is ready.
func (server *Server) integrationFromRequest(r *http.Request, rq connRequest, action string) (Integration, bool, string, error) {
	adminUID := cookie.Get_Uid(r)
	adminEmail, _ := personOf(adminUID)
	reqEmail, _ := personOf(rq.User)
	person := reqEmail
	if person == "" {
		person = rq.User
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := ""
	err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
		k := -1
		for n := range *list {
			if sameAddress((*list)[n].URL, rq.URL) {
				k = n
			}
		}
		if k < 0 {
			if len(*list) >= maxIntegrations {
				return errTooManyIntegrations
			}
			n, err := normalizeIntegration(Integration{ID: slugID(rq.Title, func(s string) bool { return integIndex(*list, s) >= 0 }), Title: rq.Title, URL: rq.URL,
				Description: rq.Note, Keywords: strings.Fields(rq.Title), Auth: IntegrationAuth{Mode: "token"}})
			if err != nil {
				return err
			}
			n.Status, n.CreatedBy, n.CreatedAt, n.UpdatedAt, n.FromRequest = integDraft, adminEmail, now, now, rq.ID
			*list = append(*list, n)
			k = len(*list) - 1
		}
		cur := &(*list)[k]
		switch action {
		case "approve":
			cur.Audience.Everyone = true
		case "approve_user":
			if !cur.Audience.Everyone && !containsString(cur.Audience.People, person) {
				cur.Audience.People = append(cur.Audience.People, person)
			}
		}
		cur.UpdatedAt = now
		id = cur.ID
		return nil
	})
	if err != nil {
		return Integration{}, false, "", err
	}
	if action == "customize" {
		i, _ := findIntegration(id)
		return i, false, "A draft: finish it and publish it.", nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	i, _ := testIntegration(ctx, id, adminUID, true)
	why := i.publishable()
	if why == "" {
		if err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
			if k := integIndex(*list, id); k >= 0 {
				(*list)[k].Status = integPublished
			}
			return nil
		}); err == nil {
			i, _ = findIntegration(id)
			dropCachedToolsFor(id)
			return i, true, "", nil
		}
	}
	i, _ = findIntegration(id)
	return i, false, why, nil
}

var errTooManyIntegrations = integErr("there are too many integrations")

type integErr string

func (e integErr) Error() string { return string(e) }

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
