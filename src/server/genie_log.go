package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"utils"
)

// The Genie log: technical events of the agent, kept so that an admin can see which steps fail and which models do badly,
// and fix them. On the free plan every signed-in user's agent reports (docs/genie-improvement-ideas.md, canvas "Genie: top five
// improvements", boards 2 and 2b); the privacy notes say so.
//
// What an event holds, and what it never holds, is fixed here and nowhere else:
//   held:   the time, a random id of the page session, the event, the model, the mode, the step number, the action, the file
//           extension, a short reason with paths, quoted text and anything like a key taken out, counts, the outcome, the plan.
//   never:  the user's name, email or account id, the task or any message, file contents or names, terminal output, page text.
// One JSON object per line, in a file for each day, so that keeping the log N days is deleting files.

var genieLogDir = utils.LOG_PATH + "/genie"

const (
	genieLogPlan       = "free" // every user is on the free plan until a paid one exists; the field stays so that the log format need not change
	genieEventKeepDays = 30
	genieDayFileMax    = 20 << 20 // a day's file stops growing here; what is refused is counted
	genieReasonMax     = 200
	genieEventBodyMax  = 2048
	genieUserEventsMax = 600 // events a user may report in an hour
	genieReadLineMax   = 16 * 1024
)

// the events a page may report
var genieEventKinds = map[string]bool{"step_failed": true, "stuck": true, "stuck_answer": true, "retry": true, "need_tools": true, "task_end": true}

// the codes a page may give for why a step failed (agent side: genie-events.ts reasonCode), the stuck rules and what the user chose after a stuck task
var (
	genieCodes   = map[string]bool{"stale_ref": true, "no_file_open": true, "unreadable": true, "too_long": true, "covered": true, "disabled": true, "limit": true, "edit_mismatch": true, "exists": true, "no_such_path": true, "refused": true, "not_available": true, "timeout": true, "other": true, "no-progress": true, "budget": true}
	genieRules   = map[string]bool{"same-step": true, "same-error": true, "no-progress": true, "budget": true}
	genieChoices = map[string]bool{"go": true, "hint": true, "stronger": true, "stop": true}
	langOK       = regexp.MustCompile(`^[A-Za-z0-9+#.\- ]{1,24}$`)
)

type genieEvent struct {
	T       string `json:"t"`
	SID     string `json:"sid"`
	Event   string `json:"event"`
	Model   string `json:"model"`
	Mode    string `json:"mode"`
	Step    int    `json:"step,omitempty"`
	Action  string `json:"action,omitempty"`
	Ext     string `json:"ext,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Failed  int    `json:"failed,omitempty"` // failed steps in the task so far
	Outcome string `json:"outcome,omitempty"`
	Steps   int    `json:"steps,omitempty"`
	Secs    int    `json:"secs,omitempty"`
	Plan    string `json:"plan"`
	Groups  string `json:"groups,omitempty"` // the groups of actions the task had been sent (agent.go, agentGroups)
	TID     string `json:"tid,omitempty"`    // one task of the page session
	Code    string `json:"code,omitempty"`   // why a step failed, as a code
	Rule    string `json:"rule,omitempty"`   // which stuck rule fired
	Choice  string `json:"choice,omitempty"` // what the user chose after a stuck task
	Trail   string `json:"trail,omitempty"`  // the last actions of the task, names only
	Tokens  int    `json:"tokens,omitempty"` // tokens the task used
	Lang    string `json:"lang,omitempty"`   // the language in the picker
	Prompt  string `json:"prompt,omitempty"`
}

var (
	pathLike   = regexp.MustCompile(`[A-Za-z0-9_.~-]*/[A-Za-z0-9_./~@+-]*|\b[A-Za-z0-9_-]+\.[A-Za-z]{1,6}\b`)
	quoted     = regexp.MustCompile("\"[^\"\\n]*\"|'[^'\\n]*'|`[^`\\n]*`")
	longToken  = regexp.MustCompile(`[A-Za-z0-9+_=-]{24,}`)
	emailLike  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+`)
	oneSpace   = regexp.MustCompile(`\s+`)
	extOK      = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
	sidOK      = regexp.MustCompile(`^[A-Za-z0-9_-]{6,40}$`)
	promptOK   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,24}$`)
	outcomesOK = map[string]bool{"done": true, "stopped": true, "failed": true, "toolong": true, "unreadable": true}
)

// scrubReason makes the text of a failure fit to keep: credentials masked as in the log viewer, then every address, path,
// file name, quoted piece of text and long token replaced, whitespace folded, and the rest cut short. A page's own error
// messages ("the editor has only 35 lines") are what is left; a stray file name or a pasted line of code is not.
func scrubReason(s string) string {
	s = quoted.ReplaceAllString(s, `"…"`) // first: a quote may hold a credential, and cutting it would break the pairs
	s = emailLike.ReplaceAllString(s, "<email>")
	s = redactLine(s)
	s = longToken.ReplaceAllString(s, "<secret>")
	s = pathLike.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "<") {
			return m
		}
		return "<path>"
	})
	s = oneSpace.ReplaceAllString(strings.TrimSpace(s), " ")
	if r := []rune(s); len(r) > genieReasonMax {
		s = string(r[:genieReasonMax]) + "…"
	}
	return s
}

// ---- the store -------------------------------------------------------------------------------

type genieStore struct {
	mu      sync.Mutex
	dir     string
	dropped int64
	pruned  time.Time
	users   map[string][]time.Time // a user's recent events, for the limit
}

var genieLog = &genieStore{dir: "", users: map[string][]time.Time{}}

func (g *genieStore) directory() string {
	if g.dir != "" {
		return g.dir
	}
	return genieLogDir
}

func genieKeepDays() int {
	d := GetSiteSettings().GenieEventDays
	if d < 1 || d > 365 {
		return genieEventKeepDays
	}
	return d
}

func genieDayFile(dir string, day time.Time) string {
	return filepath.Join(dir, "events-"+day.UTC().Format("2006-01-02")+".jsonl")
}

// allow says whether a user may report one more event now.
func (g *genieStore) allow(uid string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cut := now.Add(-time.Hour)
	list := g.users[uid]
	n := 0
	for _, t := range list {
		if t.After(cut) {
			list[n] = t
			n++
		}
	}
	list = list[:n]
	if len(list) >= genieUserEventsMax {
		g.users[uid] = list
		return false
	}
	g.users[uid] = append(list, now)
	if len(g.users) > 5000 { // forget the users that have been quiet
		for k, v := range g.users {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(g.users, k)
			}
		}
	}
	return true
}

// add writes an event. A failure to write is logged and never reaches the user.
func (g *genieStore) add(e genieEvent, now time.Time) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	dir := g.directory()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Println("ERROR: the Genie log folder:", err)
		return
	}
	if now.Sub(g.pruned) > time.Hour {
		g.pruned = now
		g.prune(dir, now)
	}
	path := genieDayFile(dir, now)
	if fi, err := os.Stat(path); err == nil && fi.Size() >= genieDayFileMax {
		g.dropped++
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		log.Println("ERROR: writing the Genie log:", err)
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// prune deletes the days that are past the time the log is kept. The caller holds the lock.
func (g *genieStore) prune(dir string, now time.Time) {
	cut := now.UTC().AddDate(0, 0, -genieKeepDays()).Format("2006-01-02")
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	for _, n := range names {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "events-"), ".jsonl")
		if day < cut {
			os.Remove(n)
		}
	}
}

// files returns the day files of the last days, oldest first.
func (g *genieStore) files(days int, now time.Time) []string {
	dir := g.directory()
	cut := now.UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	out := names[:0]
	for _, n := range names {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "events-"), ".jsonl")
		if day >= cut {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ---- what a page reports -------------------------------------------------------------------------

func knownAction(a string) bool {
	for _, k := range agentActions {
		if k == a {
			return true
		}
	}
	return false
}

// eventFrom builds the event to keep from what a page sent: every field is checked, cut or replaced, so that the page cannot
// put anything else into the log.
func eventFrom(in map[string]interface{}, now time.Time) (genieEvent, bool) {
	str := func(k string) string { s, _ := in[k].(string); return s }
	num := func(k string, max int) int {
		f, _ := in[k].(float64)
		if f < 0 || f > float64(max) {
			return 0
		}
		return int(f)
	}
	kind := str("event")
	if !genieEventKinds[kind] || !sidOK.MatchString(str("sid")) {
		return genieEvent{}, false
	}
	e := genieEvent{T: now.UTC().Format(time.RFC3339), SID: str("sid"), Event: kind, Mode: "agent", Plan: genieLogPlan}
	e.Model = modelNameForLog(str("model"))
	e.Step = num("step", 10000)
	e.Failed = num("failed", 10000)
	e.Steps = num("steps", 10000)
	e.Secs = num("secs", 86400)
	if a := str("action"); knownAction(a) {
		e.Action = a
	} else if a != "" {
		e.Action = "other"
	}
	if x := strings.ToLower(str("ext")); extOK.MatchString(x) {
		e.Ext = x
	}
	if o := str("outcome"); outcomesOK[o] {
		e.Outcome = o
	}
	if p := str("prompt"); promptOK.MatchString(p) {
		e.Prompt = p
	}
	e.Groups = groupsForLog(str("groups"))
	if t := str("tid"); sidOK.MatchString(t) {
		e.TID = t
	}
	if c := str("code"); genieCodes[c] {
		e.Code = c
	} else if c != "" {
		e.Code = "other"
	}
	if r := str("rule"); genieRules[r] {
		e.Rule = r
	}
	if c := str("choice"); genieChoices[c] {
		e.Choice = c
	}
	e.Trail = trailForLog(str("trail"))
	e.Tokens = num("tokens", 50000000)
	if l := str("lang"); langOK.MatchString(l) {
		e.Lang = l
	}
	e.Reason = scrubReason(str("reason"))
	return e, true
}

// groupsForLog keeps the names of the groups of actions in a list the page sent, in the order of agentGroups.
func groupsForLog(list string) string {
	have := map[string]bool{}
	for _, g := range strings.Split(list, ",") {
		have[strings.TrimSpace(g)] = true
	}
	var out []string
	for _, g := range agentGroups {
		if have[g.name] {
			out = append(out, g.name)
		}
	}
	return strings.Join(out, ",")
}

// trailForLog keeps the names of actions in a list the page sent (at most 10, nothing else).
func trailForLog(list string) string {
	var out []string
	for _, a := range strings.Split(list, ",") {
		a = strings.TrimSpace(a)
		if knownAction(a) && len(out) < 10 {
			out = append(out, a)
		}
	}
	return strings.Join(out, ",")
}

// modelNameForLog is the id of a model of this site, or "other": a page cannot put its own text in the field.
func modelNameForLog(id string) string {
	if m, ok := modelByID(id); ok {
		return m.ID
	}
	return "other"
}

// POST chat/genie-event: the page of a signed-in user reports a failed step, a stuck task or a retry. Nothing is answered but
// 204, so the page has nothing to wait for or to show.
func handleGenieEvent(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		rw.Header().Set("Allow", "POST")
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	uid, ok := agentUser(req)
	if !ok {
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	if GetSiteSettings().GenieEventsOff {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	var in map[string]interface{}
	if json.NewDecoder(http.MaxBytesReader(rw, req.Body, genieEventBodyMax)).Decode(&in) != nil {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now()
	e, ok := eventFrom(in, now)
	if !ok {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	if e.Event == "task_end" { // a task's end comes with its outcome (handleAgentOutcome), once
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if genieLog.allow(uid, now) {
		genieLog.add(e, now)
	}
	rw.WriteHeader(http.StatusNoContent)
}

// genieTaskEnd keeps the end of a task: called by handleAgentOutcome, which counts a task once.
func genieTaskEnd(uid string, in map[string]interface{}) {
	if GetSiteSettings().GenieEventsOff {
		return
	}
	in["event"] = "task_end"
	now := time.Now()
	e, ok := eventFrom(in, now)
	if !ok || !genieLog.allow(uid, now) {
		return
	}
	genieLog.add(e, now)
}

// ---- the admin's view --------------------------------------------------------------------------

type genieModelRow struct {
	Model     string  `json:"model"`
	Tasks     int     `json:"tasks"`
	Done      int     `json:"done"`
	DonePct   float64 `json:"donePct"`
	Failures  int     `json:"failures"`
	Stuck     int     `json:"stuck"`
	TopAction string  `json:"topAction"`
	TopReason string  `json:"topReason"`
}

type genieCodeRow struct {
	Code      string `json:"code"`
	N         int    `json:"n"`
	TopAction string `json:"topAction"`
}

type genieLogReply struct {
	Days        int             `json:"days"`
	Tasks       int             `json:"tasks"`
	Done        int             `json:"done"`
	DonePct     float64         `json:"donePct"`
	Failures    int             `json:"failures"`
	Stuck       int             `json:"stuck"`
	WorstAction string          `json:"worstAction"`
	Models      []genieModelRow `json:"models"`
	Codes       []genieCodeRow  `json:"codes"`
	Events      []genieEvent    `json:"events"`
	Matching    int             `json:"matching"`
	Off         bool            `json:"off"`
	KeepDays    int             `json:"keepDays"`
	Dropped     int64           `json:"dropped"`
}

type tally map[string]int

func (c tally) top() string {
	best, n := "", 0
	for k, v := range c {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

// readEvents reads the day files and calls f for each event, oldest first. A line that is not an event is skipped.
func readEvents(files []string, f func(genieEvent)) {
	for _, name := range files {
		fh, err := os.Open(name)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), genieReadLineMax)
		for sc.Scan() {
			var e genieEvent
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event != "" {
				f(e)
			}
		}
		fh.Close()
	}
}

func buildGenieLog(days int, model, event, action, q string, limit int, now time.Time) genieLogReply {
	rep := genieLogReply{Days: days, Models: []genieModelRow{}, Events: []genieEvent{}, KeepDays: genieKeepDays()}
	type acc struct {
		row     genieModelRow
		actions tally
		reasons tally
	}
	by := map[string]*acc{}
	worst := tally{}
	codes := tally{}
	codeActions := map[string]tally{}
	q = strings.ToLower(q)
	var matching []genieEvent
	readEvents(genieLog.files(days, now), func(e genieEvent) {
		a := by[e.Model]
		if a == nil {
			a = &acc{row: genieModelRow{Model: e.Model}, actions: tally{}, reasons: tally{}}
			by[e.Model] = a
		}
		switch e.Event {
		case "task_end":
			rep.Tasks++
			a.row.Tasks++
			if e.Outcome == "done" {
				rep.Done++
				a.row.Done++
			}
		case "step_failed":
			rep.Failures++
			a.row.Failures++
			if e.Action != "" {
				worst[e.Action]++
				a.actions[e.Action]++
			}
			if e.Reason != "" {
				a.reasons[e.Reason]++
			}
			if e.Code != "" {
				codes[e.Code]++
				if codeActions[e.Code] == nil {
					codeActions[e.Code] = tally{}
				}
				codeActions[e.Code][e.Action]++
			}
		case "stuck":
			rep.Stuck++
			a.row.Stuck++
		}
		if model != "" && e.Model != model || event != "" && e.Event != event || action != "" && e.Action != action {
			return
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Reason+" "+e.Action+" "+e.Model+" "+e.Outcome+" "+e.Code+" "+e.Rule+" "+e.Choice), q) {
			return
		}
		matching = append(matching, e)
	})
	if rep.Tasks > 0 {
		rep.DonePct = float64(int(float64(rep.Done)/float64(rep.Tasks)*1000+0.5)) / 10
	}
	rep.WorstAction = worst.top()
	for c, n := range codes {
		rep.Codes = append(rep.Codes, genieCodeRow{Code: c, N: n, TopAction: codeActions[c].top()})
	}
	sort.Slice(rep.Codes, func(i, j int) bool {
		if rep.Codes[i].N != rep.Codes[j].N {
			return rep.Codes[i].N > rep.Codes[j].N
		}
		return rep.Codes[i].Code < rep.Codes[j].Code
	})
	if rep.Codes == nil {
		rep.Codes = []genieCodeRow{}
	}
	for _, a := range by {
		if a.row.Tasks > 0 {
			a.row.DonePct = float64(int(float64(a.row.Done)/float64(a.row.Tasks)*1000+0.5)) / 10
		}
		a.row.TopAction, a.row.TopReason = a.actions.top(), a.reasons.top()
		rep.Models = append(rep.Models, a.row)
	}
	sort.Slice(rep.Models, func(i, j int) bool {
		if rep.Models[i].Tasks != rep.Models[j].Tasks {
			return rep.Models[i].Tasks > rep.Models[j].Tasks
		}
		return rep.Models[i].Model < rep.Models[j].Model
	})
	rep.Matching = len(matching)
	for i := len(matching) - 1; i >= 0 && len(rep.Events) < limit; i-- { // newest first
		rep.Events = append(rep.Events, matching[i])
	}
	return rep
}

// GET admin/genie-log?days=7&model=&event=&action=&q=&limit=200: the summary and the newest events.
// GET admin/genie-log?download=1&days=30: every event of those days, as the file of one JSON object per line.
func (server *Server) handleAdminGenieLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := genieKeepDays()
	if v, err := strconv.Atoi(q.Get("days")); err == nil && v >= 1 && v <= days {
		days = v
	}
	now := time.Now()
	if q.Get("download") == "1" {
		h := w.Header()
		h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
		h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="openrepl-genie-events-%s.jsonl"`, now.UTC().Format("2006-01-02")))
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		server.audit(r, "genie log", fmt.Sprintf("downloaded the Genie log (%d days)", days))
		out := bufio.NewWriterSize(w, 64*1024)
		readEvents(genieLog.files(days, now), func(e genieEvent) {
			if b, err := json.Marshal(e); err == nil {
				out.Write(b)
				out.WriteByte('\n')
			}
		})
		out.Flush()
		return
	}
	limit := 200
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	rep := buildGenieLog(days, q.Get("model"), q.Get("event"), q.Get("action"), strings.TrimSpace(q.Get("q")), limit, now)
	rep.Off = GetSiteSettings().GenieEventsOff
	genieLog.mu.Lock()
	rep.Dropped = genieLog.dropped
	genieLog.mu.Unlock()
	adminJSON(w, http.StatusOK, rep)
}
