package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cookie"
	"persist"
	"utils"
)

// The tokens Genie has spent for a user: what the models report for each answer
// (the "usage" of an OpenAI or OpenRouter reply), added up per user and model.
//
// A signed-in user's totals (this month and all time) are kept in the site's
// database, so they follow the user to other devices. Visitors are counted
// together, in one record, because a visitor has no account; the panel also
// shows a visitor what their own visit has used, which the server keeps in
// memory under an anonymous id in the session cookie. Only numbers are stored:
// no question, answer, file or code.

const TOKENS_DB = utils.GOTTY_PATH + "/genie_usage.db"

const (
	tokensVisitorsKey = "visitors"
	tokensUserPrefix  = "u:"
	maxVisits         = 5000           // visits kept in memory at once
	visitTTL          = 24 * time.Hour // how long a visit is remembered
)

var (
	tokens_db_handle persist.Store
	tokensDBMu       sync.Mutex
)

func InitTokenDBHandle() {
	tokensDBMu.Lock()
	defer tokensDBMu.Unlock()
	var err error
	tokens_db_handle, err = persist.Open(TOKENS_DB)
	if err != nil {
		log.Println("ERROR: Error while creating the Genie usage DB handle : ", err.Error())
		tokens_db_handle = nil // the totals then last until the server restarts
		return
	}
	log.Println("Successfully initialized the Genie usage handle, stored in", tokens_db_handle.Backend())
}

func CloseTokenDBHandle() {
	tokensDBMu.Lock()
	defer tokensDBMu.Unlock()
	if tokens_db_handle == nil {
		return
	}
	if err := tokens_db_handle.Close(); err != nil {
		log.Println("ERROR: Error while closing the Genie usage DB handle : ", err.Error())
	}
	tokens_db_handle = nil
}

// ---- what a reply says it cost -------------------------------------------------------

// usageCount is the usage of one answer.
type usageCount struct {
	In      int64   // tokens sent
	Out     int64   // tokens written, thinking included
	Cost    float64 // dollars, when the provider says (OpenRouter)
	HasCost bool
}

type usageJSON struct {
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	Cost             *float64 `json:"cost"`
}

func (u usageJSON) count() usageCount {
	c := usageCount{In: u.PromptTokens, Out: u.CompletionTokens}
	if u.Cost != nil && *u.Cost >= 0 {
		c.Cost, c.HasCost = *u.Cost, true
	}
	return c
}

// extractUsage finds the usage in the body of a reply: the JSON of a whole answer,
// or the last "data:" line of a stream that carries one. false when there is none.
func extractUsage(body []byte) (usageCount, bool) {
	var whole struct {
		Usage *usageJSON `json:"usage"`
	}
	if json.Unmarshal(body, &whole) == nil {
		if whole.Usage == nil {
			return usageCount{}, false
		}
		return whole.Usage.count(), true
	}
	var last *usageJSON
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Usage *usageJSON `json:"usage"`
		}
		if json.Unmarshal([]byte(payload), &chunk) == nil && chunk.Usage != nil {
			last = chunk.Usage
		}
	}
	if last == nil {
		return usageCount{}, false
	}
	return last.count(), true
}

// bodyTap passes a body on and keeps a copy of it, up to a limit, to look at when it is over.
type bodyTap struct {
	r     io.Reader
	buf   bytes.Buffer
	limit int
	over  bool
}

func (t *bodyTap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && !t.over {
		if t.buf.Len()+n > t.limit {
			t.over = true
			t.buf.Reset()
		} else {
			t.buf.Write(p[:n])
		}
	}
	return n, err
}

// ---- the totals ----------------------------------------------------------------------

// modelTokens is what one user has spent on one model: m is this month, a is all time.
type modelTokens struct {
	MIn      int64   `json:"mIn,omitempty"`
	MOut     int64   `json:"mOut,omitempty"`
	MAns     int64   `json:"mAns,omitempty"`
	MCost    float64 `json:"mCost,omitempty"`    // dollars the provider reported
	MCostAns int64   `json:"mCostAns,omitempty"` // answers that reported one
	AIn      int64   `json:"aIn,omitempty"`
	AOut     int64   `json:"aOut,omitempty"`
	AAns     int64   `json:"aAns,omitempty"`
	ACost    float64 `json:"aCost,omitempty"`
	ACostAns int64   `json:"aCostAns,omitempty"`
}

type tokenRecord struct {
	Month   string                  `json:"month"` // the month the m fields count, "2026-10"
	Models  map[string]*modelTokens `json:"models"`
	Updated int64                   `json:"updated"`
}

func monthOf(t time.Time) string { return t.UTC().Format("2006-01") }

// forMonth is the record with this month's numbers cleared when the record is
// of another month. It does not change the original.
func (r tokenRecord) forMonth(month string) tokenRecord {
	out := tokenRecord{Month: month, Models: map[string]*modelTokens{}, Updated: r.Updated}
	for id, m := range r.Models {
		c := *m
		if r.Month != month {
			c.MIn, c.MOut, c.MAns, c.MCost, c.MCostAns = 0, 0, 0, 0, 0
		}
		out.Models[id] = &c
	}
	return out
}

func (r *tokenRecord) add(model string, u usageCount, now time.Time) {
	month := monthOf(now)
	if r.Models == nil {
		r.Models = map[string]*modelTokens{}
	}
	if r.Month != month {
		for _, m := range r.Models {
			m.MIn, m.MOut, m.MAns, m.MCost, m.MCostAns = 0, 0, 0, 0, 0
		}
		r.Month = month
	}
	m := r.Models[model]
	if m == nil {
		m = &modelTokens{}
		r.Models[model] = m
	}
	m.MIn, m.MOut, m.MAns = m.MIn+u.In, m.MOut+u.Out, m.MAns+1
	m.AIn, m.AOut, m.AAns = m.AIn+u.In, m.AOut+u.Out, m.AAns+1
	if u.HasCost {
		m.MCost, m.MCostAns = m.MCost+u.Cost, m.MCostAns+1
		m.ACost, m.ACostAns = m.ACost+u.Cost, m.ACostAns+1
	}
	r.Updated = now.Unix()
}

// totals are sent, written and answers, for the month or for all time.
type totals struct {
	In      int64 `json:"in"`
	Out     int64 `json:"out"`
	Answers int64 `json:"answers"`
}

func (r tokenRecord) sum(month bool) totals {
	var t totals
	for _, m := range r.Models {
		if month {
			t.In, t.Out, t.Answers = t.In+m.MIn, t.Out+m.MOut, t.Answers+m.MAns
		} else {
			t.In, t.Out, t.Answers = t.In+m.AIn, t.Out+m.AOut, t.Answers+m.AAns
		}
	}
	return t
}

// A visit: what one anonymous session has used, kept in memory.
type visitTokens struct {
	totals
	seen time.Time
}

type tokenLedger struct {
	mu      sync.Mutex
	records map[string]*tokenRecord // the database's records, as far as they were read or written
	visits  map[string]*visitTokens
	now     func() time.Time
}

func newTokenLedger() *tokenLedger {
	return &tokenLedger{records: map[string]*tokenRecord{}, visits: map[string]*visitTokens{}, now: time.Now}
}

var tokenTotals = newTokenLedger()

// record returns the stored record of a key, read from the database the first
// time. The caller holds l.mu.
func (l *tokenLedger) record(key string) *tokenRecord {
	if r, ok := l.records[key]; ok {
		return r
	}
	r := &tokenRecord{Models: map[string]*modelTokens{}}
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h != nil {
		if raw, err := h.Fetch([]byte(key)); err == nil {
			json.Unmarshal(raw, r)
			if r.Models == nil {
				r.Models = map[string]*modelTokens{}
			}
		}
	}
	l.records[key] = r
	return r
}

func (l *tokenLedger) save(key string, r *tokenRecord) {
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h == nil {
		return
	}
	raw, err := json.Marshal(r)
	if err == nil {
		err = h.Store([]byte(key), raw)
	}
	if err == nil {
		err = h.Commit()
	}
	if err != nil {
		log.Println("ERROR: saving the Genie usage of", key, ":", err)
	}
}

// add counts one answer. key is the signed-in user's record ("u:<uid>") or the
// visitors' ("visitors"); visit is the anonymous id of a visitor's session, "" for a user.
func (l *tokenLedger) add(key, visit, model string, u usageCount) {
	if u.In == 0 && u.Out == 0 && !u.HasCost {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	r := l.record(key)
	r.add(model, u, now)
	l.save(key, r)
	if visit != "" {
		v := l.visits[visit]
		if v == nil {
			l.trimVisits(now)
			v = &visitTokens{}
			l.visits[visit] = v
		}
		v.In, v.Out, v.Answers = v.In+u.In, v.Out+u.Out, v.Answers+1
		v.seen = now
	}
}

// trimVisits forgets the visits that were quiet for a day, and the oldest ones when
// there are too many. The caller holds l.mu.
func (l *tokenLedger) trimVisits(now time.Time) {
	for id, v := range l.visits {
		if now.Sub(v.seen) > visitTTL {
			delete(l.visits, id)
		}
	}
	for len(l.visits) >= maxVisits {
		oldest, at := "", now
		for id, v := range l.visits {
			if oldest == "" || v.seen.Before(at) {
				oldest, at = id, v.seen
			}
		}
		delete(l.visits, oldest)
	}
}

// user is the record of a signed-in user, with this month's numbers right for now.
func (l *tokenLedger) user(uid string) tokenRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record(tokensUserPrefix + uid).forMonth(monthOf(l.now()))
}

func (l *tokenLedger) visitors() tokenRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.record(tokensVisitorsKey).forMonth(monthOf(l.now()))
}

func (l *tokenLedger) visit(id string) totals {
	if id == "" {
		return totals{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if v := l.visits[id]; v != nil {
		return v.totals
	}
	return totals{}
}

// every is each signed-in user's record (key without the prefix), from the
// database and from what was counted since the server started.
func (l *tokenLedger) every() map[string]tokenRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	month := monthOf(l.now())
	out := map[string]tokenRecord{}
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h != nil {
		h.Each(func(k, v []byte) bool {
			key := string(k)
			if strings.HasPrefix(key, tokensUserPrefix) {
				var r tokenRecord
				if json.Unmarshal(v, &r) == nil {
					out[strings.TrimPrefix(key, tokensUserPrefix)] = r
				}
			}
			return true
		})
	}
	for key, r := range l.records {
		if strings.HasPrefix(key, tokensUserPrefix) {
			out[strings.TrimPrefix(key, tokensUserPrefix)] = *r
		}
	}
	for uid, r := range out {
		out[uid] = r.forMonth(month)
	}
	return out
}

// whoPays is the key and the visit id of the caller of the proxy: a signed-in
// user's record, or the visitors' with the anonymous id of the visit.
func whoPays(req *http.Request) (key, visit string) {
	if uid, ok := agentUser(req); ok {
		return tokensUserPrefix + uid, ""
	}
	return tokensVisitorsKey, cookie.GuestID(req)
}

// ---- dollars -------------------------------------------------------------------------

// modelPrice is what a model costs, in dollars for a million tokens.
type modelPrice struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
}

func (p modelPrice) set() bool { return p.In > 0 || p.Out > 0 }

// cost is the dollars a model's tokens cost: what the provider reported when it did,
// else the table's price. partial is true when there is no way to tell.
func (m modelTokens) cost(month bool, price modelPrice) (usd float64, partial bool) {
	in, out, ans, rep, repAns := m.AIn, m.AOut, m.AAns, m.ACost, m.ACostAns
	if month {
		in, out, ans, rep, repAns = m.MIn, m.MOut, m.MAns, m.MCost, m.MCostAns
	}
	if ans == 0 {
		return 0, false
	}
	if repAns > 0 {
		return rep, repAns < ans && !price.set()
	}
	if !price.set() {
		return 0, true
	}
	return float64(in)/1e6*price.In + float64(out)/1e6*price.Out, false
}

// cost of a whole record, adding its models.
func (r tokenRecord) cost(month bool, prices map[string]modelPrice) (usd float64, partial bool) {
	for id, m := range r.Models {
		c, p := m.cost(month, prices[id])
		usd += c
		partial = partial || p
	}
	return usd, partial
}
