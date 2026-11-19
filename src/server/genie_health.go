package server

// The health of the model hosts as Genie sees it (admin: System > Genie health): for each model the calls of the last hours, how many
// failed and why, and how long a host took to answer. Kept in memory by the hour for healthHours, so a restart starts it again; it holds
// no text and no user, only the model and counts. The time is the one to the first byte of the host's answer (the headers), which for a
// streamed answer is when the first words can be shown.

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	healthHours          = 48
	defaultHealthErrPct  = 20   // a model whose calls fail this often (percent) is flagged
	defaultHealthSlowMs  = 8000 // a model whose slowest twentieth takes this long (ms) to start answering is flagged
	healthMinCalls       = 5    // fewer calls than this say nothing
	healthMaxWindowHours = healthHours
)

// upper bounds (ms) of the time buckets; one more bucket holds what is slower than the last
var healthBounds = []int64{250, 500, 1000, 2000, 4000, 8000, 16000, 32000}

type healthBucket struct {
	calls    int
	outcomes map[string]int
	hist     [9]int // the time to the first byte of the calls that worked
	totalMs  int64  // the whole time of every call
	retries  int    // new tries made because the host failed (chat_retry.go)
}

type genieHealthStore struct {
	mu     sync.Mutex
	models map[string]*modelHealth
}

type modelHealth struct {
	name    string
	hours   map[int64]*healthBucket
	lastErr int64 // unix time of the last failure
}

var genieHealth = &genieHealthStore{models: map[string]*modelHealth{}}

// why a call to a host failed, in words an admin can act on
func healthOutcomeOfStatus(code int) string {
	switch {
	case code < 400:
		return "ok"
	case code == http.StatusPaymentRequired:
		return "no_credit"
	case code == http.StatusTooManyRequests:
		return "limit"
	case code == http.StatusNotFound:
		return "no_host"
	case code >= 500:
		return "host_error"
	}
	return "refused"
}

func healthOutcomeOfError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "network"
}

func (g *genieHealthStore) note(model chatModel, outcome string, first, total time.Duration, now time.Time) {
	if model.ID == "" {
		return
	}
	hour := now.Unix() / 3600
	g.mu.Lock()
	defer g.mu.Unlock()
	mh := g.models[model.ID]
	if mh == nil {
		mh = &modelHealth{hours: map[int64]*healthBucket{}}
		g.models[model.ID] = mh
	}
	if model.Name != "" {
		mh.name = model.Name
	}
	b := mh.hours[hour]
	if b == nil {
		b = &healthBucket{outcomes: map[string]int{}}
		mh.hours[hour] = b
		for h := range mh.hours { // old hours go
			if h <= hour-healthHours {
				delete(mh.hours, h)
			}
		}
	}
	b.calls++
	b.outcomes[outcome]++
	b.totalMs += total.Milliseconds()
	if outcome == "ok" {
		ms, i := first.Milliseconds(), 0
		for i < len(healthBounds) && ms > healthBounds[i] {
			i++
		}
		b.hist[i]++
	} else {
		mh.lastErr = now.Unix()
	}
}

// healthRetried adds the new tries that the call needed (called right after healthNote, so the hour's bucket is there)
func healthRetried(model chatModel, n int) {
	if n <= 0 || model.ID == "" {
		return
	}
	g := genieHealth
	hour := time.Now().Unix() / 3600
	g.mu.Lock()
	defer g.mu.Unlock()
	if mh := g.models[model.ID]; mh != nil {
		if b := mh.hours[hour]; b != nil {
			b.retries += n
		}
	}
}

func healthNote(model chatModel, outcome string, first, total time.Duration) {
	genieHealth.note(model, outcome, first, total, time.Now())
}

type healthRow struct {
	Model   string         `json:"model"`
	Name    string         `json:"name"`
	Calls   int            `json:"calls"`
	Errors  int            `json:"errors"`
	Retries int            `json:"retries"` // new tries made because the host failed, whether or not the call then worked
	ErrPct  float64        `json:"errPct"`
	Reasons map[string]int `json:"reasons"`
	P50Ms   int64          `json:"p50Ms"`
	P95Ms   int64          `json:"p95Ms"` // upper bound of the time bucket; -1 when slower than the last bound
	AvgMs   int64          `json:"avgMs"`
	LastErr int64          `json:"lastError"`
	Alerts  []string       `json:"alerts"`
}

type healthReply struct {
	Hours    int         `json:"hours"`
	ErrPct   int         `json:"errPctLimit"`
	SlowMs   int         `json:"slowMsLimit"`
	Rows     []healthRow `json:"rows"`
	Alerting int         `json:"alerting"`
	Since    int64       `json:"since"` // when the server started counting
	Auto     *autoHealth `json:"auto"`  // how Auto model choice did, when it is on
}

// autoHealth is the card of Auto model choice: the calls and failures of the two models, and how often people asked for the strong
// one under an answer of the small one (a high share says the rules send too much to the small model).
type autoHealth struct {
	Small    autoSide `json:"small"`
	Strong   autoSide `json:"strong"`
	Stronger int      `json:"stronger"`
	AskedPct float64  `json:"askedPct"`
}

type autoSide struct {
	Model  string  `json:"model"`
	Name   string  `json:"name"`
	Calls  int     `json:"calls"`
	ErrPct float64 `json:"errPct"`
}

func autoHealthOf(p *autoPublic, rows []healthRow, stronger int) *autoHealth {
	if p == nil {
		return nil
	}
	side := func(id string) autoSide {
		a := autoSide{Model: id, Name: id}
		for _, r := range rows {
			if r.Model == id {
				a.Name, a.Calls, a.ErrPct = r.Name, r.Calls, r.ErrPct
				if a.Name == "" {
					a.Name = id
				}
			}
		}
		return a
	}
	h := &autoHealth{Small: side(p.Small), Strong: side(p.Strong), Stronger: stronger}
	if h.Small.Calls > 0 {
		h.AskedPct = float64(stronger) * 100 / float64(h.Small.Calls)
	}
	return h
}

// the "Use a stronger one" clicks of the last hours, from the Genie log
func countStronger(hours int, now time.Time) int {
	n := 0
	days := hours/24 + 1
	from := now.Add(-time.Duration(hours) * time.Hour)
	readEvents(genieLog.files(days, now), func(e genieEvent) {
		if e.Event != "auto_stronger" {
			return
		}
		if t, err := time.Parse(time.RFC3339, e.T); err == nil && t.Before(from) {
			return
		}
		n++
	})
	return n
}

var healthStarted = time.Now().Unix()

// the time at the given share of the calls that worked, as the upper bound of its bucket
func healthPercentile(hist [9]int, share float64) int64 {
	total := 0
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	need := int(float64(total)*share + 0.9999)
	if need < 1 {
		need = 1
	}
	seen := 0
	for i, n := range hist {
		seen += n
		if seen >= need {
			if i < len(healthBounds) {
				return healthBounds[i]
			}
			return -1
		}
	}
	return -1
}

func healthLimits(s SiteSettings) (errPct, slowMs int) {
	errPct, slowMs = s.GenieHealthErrPct, s.GenieHealthSlowMs
	if errPct <= 0 {
		errPct = defaultHealthErrPct
	}
	if slowMs <= 0 {
		slowMs = defaultHealthSlowMs
	}
	return
}

func (g *genieHealthStore) report(hours int, errLimit, slowLimit int, now time.Time) healthReply {
	if hours < 1 || hours > healthMaxWindowHours {
		hours = 24
	}
	from := now.Unix()/3600 - int64(hours) + 1
	rep := healthReply{Hours: hours, ErrPct: errLimit, SlowMs: slowLimit, Rows: []healthRow{}, Since: healthStarted}
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, mh := range g.models {
		row := healthRow{Model: id, Name: mh.name, Reasons: map[string]int{}, Alerts: []string{}, LastErr: mh.lastErr}
		var hist [9]int
		var totalMs int64
		for h, b := range mh.hours {
			if h < from {
				continue
			}
			row.Calls += b.calls
			row.Retries += b.retries
			totalMs += b.totalMs
			for k, n := range b.outcomes {
				if k != "ok" {
					row.Errors += n
					row.Reasons[k] += n
				}
			}
			for i, n := range b.hist {
				hist[i] += n
			}
		}
		if row.Calls == 0 {
			continue
		}
		row.ErrPct = float64(row.Errors) * 100 / float64(row.Calls)
		row.AvgMs = totalMs / int64(row.Calls)
		row.P50Ms = healthPercentile(hist, 0.5)
		row.P95Ms = healthPercentile(hist, 0.95)
		if row.Calls >= healthMinCalls && row.ErrPct >= float64(errLimit) {
			row.Alerts = append(row.Alerts, "many calls fail")
		}
		ok := row.Calls - row.Errors
		if ok >= healthMinCalls && (row.P95Ms == -1 || row.P95Ms >= int64(slowLimit)) {
			row.Alerts = append(row.Alerts, "slow to start answering")
		}
		if len(row.Alerts) > 0 {
			rep.Alerting++
		}
		rep.Rows = append(rep.Rows, row)
	}
	// the ones that need a look first, then the busiest
	sort.Slice(rep.Rows, func(i, j int) bool {
		a, b := rep.Rows[i], rep.Rows[j]
		if (len(a.Alerts) > 0) != (len(b.Alerts) > 0) {
			return len(a.Alerts) > 0
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Model < b.Model
	})
	return rep
}

func (server *Server) handleAdminGenieHealth(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' || n > 1000 {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= healthMaxWindowHours {
			hours = n
		}
	}
	e, s := healthLimits(GetSiteSettings())
	now := time.Now()
	rep := genieHealth.report(hours, e, s, now)
	if p := GetSiteSettings().Genie.autoPublic(); p != nil {
		rep.Auto = autoHealthOf(p, rep.Rows, countStronger(rep.Hours, now))
	}
	adminJSON(w, http.StatusOK, rep)
}
