package server

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The usage record, the credits and the plan (docs/lld/19-genie-agent.md).
//
// Every answer of the models to a signed-in user, and to a visitor, is one usage
// event: counts only (tokens, credits, model, the task and its step), never a
// question, an answer, a file or code. The event of a step of a task is keyed by
// the task and its number, so that a step counted once is not counted again.
//
// A credit is a tenth of a cent. A model's credits for a thousand tokens come
// from the admin's CreditWeights, else from its price (a dollar for a million
// tokens is a credit for a thousand), else from the cost the provider reported.
//
// Nothing is charged or limited until an admin turns CreditsOn: until then the
// record is only for the admin to read. Then the plan of the user counts: credits
// for a month, and credits for one task (entitlementsFor is the one place that
// says what a person may do; one record per person can replace the default plan
// there without touching anything else).
//
// The totals are kept in the same database as the tokens (tokens.go), under "m:".

const (
	meterPrefix      = "m:"
	creditsPerDollar = 1000.0
	meterSeenTTL     = 30 * time.Minute // how long a counted step is remembered
	maxCredits       = 100000000        // the most an admin may set for a month, a task or a weight
	defaultPlanName  = "Free"
	kindAgent        = "agent"
	kindChat         = "chat"
)

func roundCredits(x float64) float64 { return math.Round(x*1000) / 1000 }

// creditsFor is the credits one answer used.
func (g GenieSettings) creditsFor(model string, u usageCount) float64 {
	if w, ok := g.CreditWeights[model]; ok && w.set() {
		return roundCredits(float64(u.In)/1000*w.In + float64(u.Out)/1000*w.Out)
	}
	if p, ok := g.ModelPrices[model]; ok && p.set() {
		return roundCredits(float64(u.In)/1000*p.In + float64(u.Out)/1000*p.Out)
	}
	if u.HasCost {
		return roundCredits(u.Cost * creditsPerDollar)
	}
	return 0
}

// normalizeCredits checks the credit numbers of the settings.
func (g *GenieSettings) normalizeCredits() error {
	g.PlanName = strings.TrimSpace(g.PlanName)
	if len(g.PlanName) > 40 {
		return fmt.Errorf("The plan name may have 40 characters at most")
	}
	for _, n := range []struct {
		name string
		v    float64
	}{{"The credits of a month", g.PlanCredits}, {"The credits of a task", g.PlanBudget}} {
		if n.v < 0 || n.v > maxCredits || math.IsNaN(n.v) || math.IsInf(n.v, 0) {
			return fmt.Errorf("%s must be between 0 and %d (0 for none)", n.name, maxCredits)
		}
	}
	known := map[string]bool{}
	for _, id := range g.allModelIDs() {
		known[id] = true
	}
	weights := map[string]modelPrice{}
	for id, w := range g.CreditWeights {
		if !known[id] {
			return fmt.Errorf("%q is not a model", id)
		}
		if w.In < 0 || w.Out < 0 || w.In > maxCredits || w.Out > maxCredits || math.IsNaN(w.In) || math.IsNaN(w.Out) {
			return fmt.Errorf("The credits of %s must be between 0 and %d for a thousand tokens (or empty)", id, maxCredits)
		}
		if w.set() {
			weights[id] = w
		}
	}
	if len(weights) == 0 {
		weights = nil
	}
	g.CreditWeights = weights
	return nil
}

// ---- the plan ---------------------------------------------------------------------

// entitlements is what one person may do. Today's limits are the default plan.
type entitlements struct {
	Plan            string  `json:"plan"`
	CreditsPerMonth float64 `json:"creditsPerMonth"` // 0: no monthly cap
	BudgetPerTask   float64 `json:"budgetPerTask"`   // 0: no budget
	MaxSteps        int     `json:"maxSteps"`        // the steps of one stretch of a task
	TasksPerHour    int     `json:"tasksPerHour"`
	Enforce         bool    `json:"enforce"` // the credit numbers count (credits are on, and not an admin)
}

func (g GenieSettings) entitlementsFor(uid string, isAdmin bool) entitlements {
	e := entitlements{Plan: defaultPlanName, MaxSteps: g.agentMaxSteps(), TasksPerHour: g.agentTasksPerHour()}
	if g.PlanName != "" {
		e.Plan = g.PlanName
	}
	if isAdmin {
		e.TasksPerHour = 0
	}
	if g.CreditsOn && !isAdmin && uid != "" {
		e.Enforce = true
		e.CreditsPerMonth = g.PlanCredits
		e.BudgetPerTask = g.PlanBudget
	}
	return e
}

// ---- the record -------------------------------------------------------------------

// meterRecord is what one person used: m fields count this month.
type meterRecord struct {
	Month    string  `json:"month"`
	Credits  float64 `json:"credits"`
	Steps    int64   `json:"steps"` // agent steps
	Tasks    int64   `json:"tasks"`
	Chats    int64   `json:"chats"` // chat answers
	ACredits float64 `json:"aCredits"`
	Updated  int64   `json:"updated"`
	// Plan, when set, is this person's own plan instead of the default one
	Plan *planRecord `json:"plan,omitempty"`
}

// planRecord is the plan of one person: the numbers that differ from the default plan.
type planRecord struct {
	Name    string  `json:"name"`
	Credits float64 `json:"credits"` // a month; 0: no monthly cap
	Budget  float64 `json:"budget"`  // one task; 0: none
}

func (r *meterRecord) roll(month string) {
	if r.Month != month {
		r.Month, r.Credits, r.Steps, r.Tasks, r.Chats = month, 0, 0, 0, 0
	}
}

// meterEvent is one answer of a model, as the record counts it.
type meterEvent struct {
	Key     string // the record: "u:<uid>" or "visitors"
	Kind    string // kindAgent or kindChat
	Task    string // the task id of an agent step
	Step    int
	First   bool // the step that started the task
	Credits float64
}

type meterLedger struct {
	mu      sync.Mutex
	records map[string]*meterRecord
	seen    map[string]time.Time // "task:step" of the steps counted
	now     func() time.Time
}

func newMeterLedger() *meterLedger {
	return &meterLedger{records: map[string]*meterRecord{}, seen: map[string]time.Time{}, now: time.Now}
}

var meter = newMeterLedger()

// record is the stored record of a key. The caller holds l.mu.
func (l *meterLedger) record(key string) *meterRecord {
	if r, ok := l.records[key]; ok {
		return r
	}
	r := &meterRecord{}
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h != nil {
		if raw, err := h.Fetch([]byte(meterPrefix + key)); err == nil {
			json.Unmarshal(raw, r)
		}
	}
	l.records[key] = r
	return r
}

func (l *meterLedger) save(key string, r *meterRecord) {
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h == nil {
		return
	}
	raw, err := json.Marshal(r)
	if err == nil {
		err = h.Store([]byte(meterPrefix+key), raw)
	}
	if err == nil {
		err = h.Commit()
	}
	if err != nil {
		log.Println("ERROR: saving the Genie credits of", key, ":", err)
	}
}

// add counts an event. It says false when the step was counted before.
func (l *meterLedger) add(ev meterEvent) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, at := range l.seen {
		if now.Sub(at) > meterSeenTTL {
			delete(l.seen, k)
		}
	}
	if ev.Kind == kindAgent {
		id := fmt.Sprintf("%s:%d", ev.Task, ev.Step)
		if _, dup := l.seen[id]; dup {
			return false
		}
		l.seen[id] = now
	}
	r := l.record(ev.Key)
	r.roll(monthOf(now))
	r.Credits = roundCredits(r.Credits + ev.Credits)
	r.ACredits = roundCredits(r.ACredits + ev.Credits)
	if ev.Kind == kindAgent {
		r.Steps++
		if ev.First {
			r.Tasks++
		}
	} else {
		r.Chats++
	}
	r.Updated = now.Unix()
	l.save(ev.Key, r)
	return true
}

// month is the record of a key with this month's numbers right for now.
func (l *meterLedger) month(key string) meterRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := *l.record(key)
	r.roll(monthOf(l.now()))
	return r
}

// forget empties the memory of this server (tests).
func (l *meterLedger) forget() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records, l.seen = map[string]*meterRecord{}, map[string]time.Time{}
}

// note is what the proxy calls after an answer was sent: it works out the
// credits, counts the event and adds the credits to the task. It returns the
// credits of the answer.
func noteMeter(g GenieSettings, key string, run *agentRun, model string, used usageCount) float64 {
	if g.MeterOff {
		return 0
	}
	c := g.creditsFor(model, used)
	ev := meterEvent{Key: key, Kind: kindChat, Credits: c}
	if run != nil {
		ev.Kind, ev.Task, ev.Step, ev.First = kindAgent, run.id, run.total, run.first
	}
	if !meter.add(ev) {
		return 0
	}
	if run != nil {
		agents.addCredits(run.id, c)
	}
	return c
}

// entitlementsOf is what a person may do: the default plan, or the person's own
// plan when they have one. It is the one place that says so.
func entitlementsOf(g GenieSettings, uid string, isAdmin bool) entitlements {
	e := g.entitlementsFor(uid, isAdmin)
	if !e.Enforce {
		return e
	}
	meter.mu.Lock()
	p := meter.record(tokensUserPrefix + uid).Plan
	meter.mu.Unlock()
	if p != nil {
		if p.Name != "" {
			e.Plan = p.Name
		}
		e.CreditsPerMonth, e.BudgetPerTask = p.Credits, p.Budget
	}
	return e
}

// setPlan gives a person their own plan; nil gives them the default plan again.
func (l *meterLedger) setPlan(key string, p *planRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.record(key)
	r.Plan = p
	l.save(key, r)
}

// creditUsage is the credits part of the usage line, there only when credits are on.
type creditUsage struct {
	Plan       string  `json:"plan"`
	Of         float64 `json:"of"`   // the credits of a month; 0: no cap
	Used       float64 `json:"used"` // this month
	Left       float64 `json:"left"`
	Resets     string  `json:"resets"` // the day the month's credits start again
	TaskBudget float64 `json:"taskBudget,omitempty"`
}

func creditsOf(g GenieSettings, uid string, isAdmin bool) *creditUsage {
	e := entitlementsOf(g, uid, isAdmin)
	if !e.Enforce {
		return nil
	}
	rec := meter.month(tokensUserPrefix + uid)
	now := meter.now()
	next := time.Date(now.UTC().Year(), now.UTC().Month()+1, 1, 0, 0, 0, 0, time.UTC)
	left := 0.0
	if e.CreditsPerMonth > 0 {
		left = math.Max(0, roundCredits(e.CreditsPerMonth-rec.Credits))
	}
	return &creditUsage{Plan: e.Plan, Of: e.CreditsPerMonth, Used: rec.Credits, Left: left, Resets: next.Format("2006-01-02"), TaskBudget: e.BudgetPerTask}
}

// creditsLeft says whether a person may still use Genie this month. A person
// with no monthly cap always may.
func creditsLeft(g GenieSettings, uid string, isAdmin bool) bool {
	e := entitlementsOf(g, uid, isAdmin)
	if !e.Enforce || e.CreditsPerMonth <= 0 {
		return true
	}
	return meter.month(tokensUserPrefix+uid).Credits < e.CreditsPerMonth
}

// ---- how the agent is doing ----------------------------------------------------------

// agentStats are counts, all time, of what happened to the agent's tasks: no
// content, no user. The admin records today's values as the baseline before a
// change, so that the change is a comparison and not a feeling.
type agentStats struct {
	Tasks      int64 `json:"tasks"`
	Steps      int64 `json:"steps"`
	ReqBytes   int64 `json:"reqBytes"` // bytes sent to the model, summed over the steps
	StepLimit  int64 `json:"stepLimit"`
	BudgetStop int64 `json:"budgetStop"`
	Unreadable int64 `json:"unreadable"` // tasks stopped for an answer that could not be read
	TooLong    int64 `json:"tooLong"`
	Stopped    int64 `json:"stopped"` // ended with Stop
	Done       int64 `json:"done"`
	Failed     int64 `json:"failed"`
	Checked    int64 `json:"checked"` // tasks that changed code and ended with a check
	NotChecked int64 `json:"notChecked"`
	Carried    int64 `json:"carried"` // Carry on pressed
	// native tool calling (agent_tools.go): steps asked as a call of the function, those the host refused and that went again as
	// text, and how many times each model's host refused
	ToolTries     int64            `json:"toolTries"`
	ToolFallbacks int64            `json:"toolFallbacks"`
	ToolRefused   map[string]int64 `json:"toolRefused,omitempty"`
}

type statsRecord struct {
	Now    agentStats  `json:"now"`
	Base   *agentStats `json:"base,omitempty"`
	BaseAt int64       `json:"baseAt,omitempty"`
}

const statsKey = meterPrefix + "stats"

var (
	statsMu  sync.Mutex
	statsRec *statsRecord
)

// statsLoad returns the record, read from the database the first time. The caller holds statsMu.
func statsLoad() *statsRecord {
	if statsRec != nil {
		return statsRec
	}
	statsRec = &statsRecord{}
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h != nil {
		if raw, err := h.Fetch([]byte(statsKey)); err == nil {
			json.Unmarshal(raw, statsRec)
		}
	}
	return statsRec
}

func statsSave(r *statsRecord) {
	tokensDBMu.Lock()
	h := tokens_db_handle
	tokensDBMu.Unlock()
	if h == nil {
		return
	}
	raw, err := json.Marshal(r)
	if err == nil {
		err = h.Store([]byte(statsKey), raw)
	}
	if err == nil {
		err = h.Commit()
	}
	if err != nil {
		log.Println("ERROR: saving the Genie agent counts:", err)
	}
}

// statsAdd changes the counts with f and stores them.
func statsAdd(f func(s *agentStats)) {
	statsMu.Lock()
	defer statsMu.Unlock()
	r := statsLoad()
	f(&r.Now)
	statsSave(r)
}

// statsToolRefused counts a refusal of the tool by a model's host.
func statsToolRefused(model string) {
	statsAdd(func(s *agentStats) {
		s.ToolFallbacks++
		if s.ToolRefused == nil {
			s.ToolRefused = map[string]int64{}
		}
		s.ToolRefused[model]++
	})
}

// statsSnapshot returns a copy of the record.
func statsSnapshot() statsRecord {
	statsMu.Lock()
	defer statsMu.Unlock()
	r := *statsLoad()
	r.Now = r.Now.copied()
	if r.Base != nil {
		b := r.Base.copied()
		r.Base = &b
	}
	return r
}

// copied is the counts with a map of their own, so that what is handed out is not changed by the next count.
func (s agentStats) copied() agentStats {
	if s.ToolRefused != nil {
		m := make(map[string]int64, len(s.ToolRefused))
		for k, v := range s.ToolRefused {
			m[k] = v
		}
		s.ToolRefused = m
	}
	return s
}

// statsBaseline keeps today's values as the baseline.
func statsBaseline(now time.Time) {
	statsMu.Lock()
	defer statsMu.Unlock()
	r := statsLoad()
	b := r.Now.copied()
	r.Base, r.BaseAt = &b, now.Unix()
	statsSave(r)
}

// statsForget empties the memory of this server (tests).
func statsForget() {
	statsMu.Lock()
	defer statsMu.Unlock()
	statsRec = nil
}

// the outcomes the page may report for a task
var agentOutcomes = map[string]func(s *agentStats){
	"done":       func(s *agentStats) { s.Done++ },
	"stopped":    func(s *agentStats) { s.Stopped++ },
	"failed":     func(s *agentStats) { s.Failed++ },
	"unreadable": func(s *agentStats) { s.Unreadable++; s.Failed++ },
	"toolong":    func(s *agentStats) { s.TooLong++; s.Failed++ },
}

// handleAgentOutcome is POST chat/agent-outcome: the page tells how its task
// ended: {"task":"<token>","outcome":"done","checked":true}. A task is
// counted once, and only for the user it belongs to.
func handleAgentOutcome(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		rw.Header().Set("Allow", "POST")
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	uid, ok := agentUser(req)
	var in struct {
		Task    string `json:"task"`
		Outcome string `json:"outcome"`
		Checked *bool  `json:"checked"`
		Changed bool   `json:"changed"`
		// what the Genie log keeps of the end of a task (genie_log.go); nothing of the task's words
		SID    string  `json:"sid"`
		Model  string  `json:"model"`
		Steps  float64 `json:"steps"`
		Secs   float64 `json:"secs"`
		Failed float64 `json:"failed"`
		Prompt string  `json:"prompt"`
		Groups string  `json:"groups"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(rw, req.Body, 4096)).Decode(&in) != nil {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	count, known := agentOutcomes[in.Outcome]
	if !known || !agents.outcome(in.Task, uid) {
		rw.WriteHeader(http.StatusNoContent) // not a task of this user, or already told: nothing to count
		return
	}
	genieTaskEnd(uid, map[string]interface{}{"sid": in.SID, "model": in.Model, "outcome": in.Outcome, "steps": in.Steps, "secs": in.Secs, "failed": in.Failed, "prompt": in.Prompt, "groups": in.Groups})
	statsAdd(func(s *agentStats) {
		count(s)
		if in.Changed && in.Checked != nil {
			if *in.Checked {
				s.Checked++
			} else {
				s.NotChecked++
			}
		}
	})
	rw.WriteHeader(http.StatusNoContent)
}
