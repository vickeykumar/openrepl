package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Approval of a call to a connection, kept by the server (the review of 2026-11-20, phase 0).
//
// A write or a call that cannot be undone runs only with a ticket: the server makes it when the page proposes the exact call (connection, tool,
// arguments, task), the person's answer on the card approves it, and the call uses it up. It is bound to the person, the task, the connection, the
// tool, a hash of the arguments and the tool's own description (so a service that changes a tool is asked about again) and it ends after two minutes.
// What the page sends as "confirmed" counts for nothing.
//
// "Allow for this session" is a grant, also kept here: a connection, up to the level of a write, for a while. It never covers a call that cannot be
// undone, and it never covers a call made after text from outside came into the task (what a service returns carries no authority).

const (
	connTicketTTL = 2 * time.Minute
	connGrantTTL  = 4 * time.Hour
	maxTickets    = 200
)

type connTicket struct {
	uid, task, conn, tool, argsHash, digest, level string
	exp                                            time.Time
	approved                                       bool
}

type connGrant struct {
	exp time.Time
}

var connTickets = struct {
	sync.Mutex
	m      map[string]*connTicket
	grants map[string]connGrant // uid|conn
}{m: map[string]*connTicket{}, grants: map[string]connGrant{}}

func newTicketID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// argsHash is the hash of the arguments as JSON (Go writes the keys of a map in order, so the same arguments give the same hash).
func argsHash(args map[string]interface{}) string {
	if args == nil {
		args = map[string]interface{}{}
	}
	b, _ := json.Marshal(args)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// toolDigest is a hash of what a tool says about itself; when it changes, an approval for the old one is no longer good.
func toolDigest(t mcpTool) string {
	b, _ := json.Marshal([]interface{}{t.Name, t.Description, string(t.InputSchema), t.Annotations})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func grantKey(uid, conn string) string { return uid + "|" + conn }

// grantCovers says whether "allow for this session" holds for a connection at this level: a write, never a call that cannot be undone.
func grantCovers(uid, conn, level string, now time.Time) bool {
	if level != "write" {
		return false
	}
	connTickets.Lock()
	defer connTickets.Unlock()
	g, ok := connTickets.grants[grantKey(uid, conn)]
	return ok && g.exp.After(now)
}

func revokeGrants(uid, conn string) {
	connTickets.Lock()
	delete(connTickets.grants, grantKey(uid, conn))
	connTickets.Unlock()
}

// openTicket makes a ticket for a proposed call. approved is true when a grant covers it (and the task holds no text from outside).
func openTicket(t connTicket, tainted bool, now time.Time) (string, bool, error) {
	connTickets.Lock()
	defer connTickets.Unlock()
	for k, v := range connTickets.m {
		if !v.exp.After(now) {
			delete(connTickets.m, k)
		}
	}
	for k, g := range connTickets.grants {
		if !g.exp.After(now) {
			delete(connTickets.grants, k)
		}
	}
	if len(connTickets.m) >= maxTickets {
		return "", false, fmt.Errorf("too many calls are waiting for an answer")
	}
	t.exp = now.Add(connTicketTTL)
	g, hasGrant := connTickets.grants[grantKey(t.uid, t.conn)]
	if hasGrant && g.exp.After(now) && t.level == "write" && !tainted {
		t.approved = true
	}
	id := newTicketID()
	tc := t
	connTickets.m[id] = &tc
	return id, tc.approved, nil
}

// approveTicket records the person's answer. remember "session" also opens a grant for the connection (a write only).
func approveTicket(uid, id, remember string, now time.Time) error {
	connTickets.Lock()
	defer connTickets.Unlock()
	t, ok := connTickets.m[id]
	if !ok || t.uid != uid || !t.exp.After(now) {
		return fmt.Errorf("that call is no longer waiting for an answer: ask again")
	}
	t.approved = true
	if remember == "session" && t.level == "write" {
		connTickets.grants[grantKey(uid, t.conn)] = connGrant{exp: now.Add(connGrantTTL)}
	}
	return nil
}

// useTicket checks a ticket against the call that is about to run and uses it up: once, whatever the outcome.
func useTicket(uid, id, task, conn, tool, hash, digest string, now time.Time) error {
	connTickets.Lock()
	defer connTickets.Unlock()
	t, ok := connTickets.m[id]
	if !ok {
		return fmt.Errorf("there is no approval for this call: it has to be proposed and allowed first")
	}
	delete(connTickets.m, id) // one use, also when it does not fit
	switch {
	case t.uid != uid, t.task != task, t.conn != conn, t.tool != tool:
		return fmt.Errorf("the approval was for another call")
	case t.argsHash != hash:
		return fmt.Errorf("the arguments are not the ones that were allowed")
	case t.digest != digest:
		return fmt.Errorf("the service changed this tool since it was allowed: ask again")
	case !t.exp.After(now):
		return fmt.Errorf("the approval ran out: ask again")
	case !t.approved:
		return fmt.Errorf("the call was not allowed")
	}
	return nil
}

// ---- the arguments, checked against the tool's own schema before anything is sent ----------------------------------------------------------

// validateArgs checks the arguments against the schema the service gave: required ones present, the type of each known one, and no unknown name
// when the schema says it takes no others. It is a guard against a model's mistakes and a service's surprises, not a full JSON Schema.
func validateArgs(schema json.RawMessage, args map[string]interface{}) error {
	if len(schema) == 0 || string(schema) == "null" {
		return nil
	}
	var s struct {
		Properties map[string]struct {
			Type interface{} `json:"type"`
		} `json:"properties"`
		Required             []string    `json:"required"`
		AdditionalProperties interface{} `json:"additionalProperties"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	var missing []string
	for _, r := range s.Required {
		if _, ok := args[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing: %s", strings.Join(missing, ", "))
	}
	names := make([]string, 0, len(args))
	for n := range args {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p, known := s.Properties[n]
		if !known {
			if b, ok := s.AdditionalProperties.(bool); ok && !b {
				return fmt.Errorf("%s is not an argument of this tool", clipRunes(n, 40))
			}
			continue
		}
		if !jsonTypeOK(p.Type, args[n]) {
			return fmt.Errorf("%s has the wrong type", clipRunes(n, 40))
		}
	}
	return nil
}

func jsonTypeOK(want interface{}, v interface{}) bool {
	var types []string
	switch w := want.(type) {
	case string:
		types = []string{w}
	case []interface{}:
		for _, x := range w {
			if s, ok := x.(string); ok {
				types = append(types, s)
			}
		}
	}
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		switch t {
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "integer":
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				return true
			}
		case "number":
			if _, ok := v.(float64); ok {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "array":
			if _, ok := v.([]interface{}); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]interface{}); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		default:
			return true // a type this check does not know is not a reason to refuse
		}
	}
	return false
}
