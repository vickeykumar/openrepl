package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"meters"
	"user"
	"utils"
)

// The dashboard's live map: every part of the system with a status and a live
// number, and how much traffic runs along each line between them.
//
//	GET /admin/architecture
//
// The numbers come from what the server already knows (terminals, workers, the
// health checks, the usage totals) and from the counters of package meters
// (requests, chat answers, database operations, file events, terminals per
// program). Nothing is stored: a restart starts the counters again.

const (
	archOK   = "ok"
	archWarn = "warn"
	archFail = "fail"
	archOff  = "off"

	// a line's level of traffic, from the requests a minute over the last minute
	archNone = "none"
	archLow  = "low"
	archMid  = "mid"
	archHigh = "high"
	// a line that shows state and not traffic (the browser talks to Firebase directly)
	archState = "state"

	archWindow = 300 // seconds the rules look back
)

type archRow struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Status string `json:"status,omitempty"`
}

type archNode struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Text   string    `json:"text"`
	Rows   []archRow `json:"rows,omitempty"`
	Series []int64   `json:"series,omitempty"` // a count for each of the last 30 minutes
	View   string    `json:"view,omitempty"`   // the dashboard view with more
}

type archLink struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Status string `json:"status,omitempty"`
}

type archProblem struct {
	At   int64  `json:"at"` // seconds
	Text string `json:"text"`
}

type archReply struct {
	At       int64         `json:"at"`
	Nodes    []archNode    `json:"nodes"`
	Links    []archLink    `json:"links"`
	Problems []archProblem `json:"problems"`
}

// ---- what the chat proxy reports -----------------------------------------------------

var (
	archMu       sync.Mutex
	archProblems []archProblem
)

// noteChat counts one request to a model host: whether it worked and how long it took. A
// failure is also kept, with the time, for the list of recent problems.
func noteChat(provider string, ok bool, since time.Time) {
	meters.Add(meters.ChatReq+provider, 1)
	meters.Add(meters.ChatMs+provider, time.Since(since).Milliseconds())
	if ok {
		return
	}
	meters.Add(meters.ChatErr+provider, 1)
	archMu.Lock()
	archProblems = append(archProblems, archProblem{At: time.Now().Unix(), Text: providerName(provider) + " failed or refused a request"})
	if len(archProblems) > 10 {
		archProblems = archProblems[len(archProblems)-10:]
	}
	archMu.Unlock()
}

func recentProblems() []archProblem {
	archMu.Lock()
	defer archMu.Unlock()
	out := make([]archProblem, 0, len(archProblems))
	for i := len(archProblems) - 1; i >= 0; i-- { // newest first
		out = append(out, archProblems[i])
	}
	return out
}

func providerName(p string) string {
	if p == providerOpenRouter {
		return "OpenRouter"
	}
	return "OpenAI"
}

// replLabel is how a terminal's program is named on the map.
func replLabel(command string) string {
	switch command {
	case "":
		return "bash"
	case "gointerpreter":
		return "gomacro"
	}
	return command
}

// ---- the rules -------------------------------------------------------------------------

// level tells how busy a line is from the requests a minute.
func level(perMinute float64) string {
	switch {
	case perMinute <= 0:
		return archNone
	case perMinute < 6:
		return archLow
	case perMinute < 30:
		return archMid
	}
	return archHigh
}

// worse is the worse of two statuses (off counts as the best: it is not a problem).
func worse(a, b string) string {
	rank := map[string]int{archOff: 0, archOK: 1, archWarn: 2, archFail: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// hostHealth is the status of a model host from the last minutes: amber above 5% of the
// requests failed or the average answer took over 4 s, red when every request of the last
// minute failed.
func hostHealth(provider string) (status string, perMinute, errPct, avgMs float64) {
	req, errs, ms := meters.Get(meters.ChatReq+provider), meters.Get(meters.ChatErr+provider), meters.Get(meters.ChatMs+provider)
	n, bad, total := req.Sum(archWindow), errs.Sum(archWindow), ms.Sum(archWindow)
	perMinute = req.PerMinute(60)
	if n > 0 {
		errPct = float64(bad) * 100 / float64(n)
		avgMs = float64(total) / float64(n)
	}
	status = archOK
	if n >= 4 && (errPct > 5 || avgMs > 4000) {
		status = archWarn
	}
	if last := req.Sum(60); last >= 2 && errs.Sum(60) == last {
		status = archFail
	}
	return status, perMinute, errPct, avgMs
}

// minutes groups the last 30 minutes of a series into 30 counts, oldest first.
func minutes(s *meters.Series) []int64 {
	per := 60 / meters.BucketSeconds
	b := s.Last(meters.Buckets)
	out := make([]int64, 0, len(b)/per)
	for i := 0; i+per <= len(b); i += per {
		var sum int64
		for _, v := range b[i : i+per] {
			sum += v
		}
		out = append(out, sum)
	}
	return out
}

func pct(n float64) string { return fmt.Sprintf("%.0f%%", n) }

func ms(n float64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1f s", n/1000)
	}
	return fmt.Sprintf("%.0f ms", n)
}

func rate(n float64) string {
	if n < 10 && n != float64(int(n)) {
		return fmt.Sprintf("%.1f", n)
	}
	return fmt.Sprintf("%.0f", n)
}

// ---- cached slow reads -----------------------------------------------------------------

type archCache struct {
	mu      sync.Mutex
	at      time.Time
	health  map[string]string // the health checks, by name
	signIns int               // accounts that signed in today
}

var archSlow archCache

// slow returns what is costly to read, not more than every 10 seconds: the health
// checks and the sign-ins of today.
func (server *Server) archSlowRead() (health map[string]string, signIns int) {
	archSlow.mu.Lock()
	defer archSlow.mu.Unlock()
	if archSlow.health != nil && time.Since(archSlow.at) < 10*time.Second {
		return archSlow.health, archSlow.signIns
	}
	_, checks := server.health()
	h := map[string]string{}
	for _, c := range checks {
		h[c.Name] = c.Status
	}
	archSlow.health = h
	archSlow.at = time.Now()
	archSlow.signIns = 0
	if accounts, err := user.ListAccounts(); err == nil {
		day := time.Now().Truncate(24*time.Hour).UnixNano() / int64(time.Millisecond)
		for _, a := range accounts {
			if a.LastSignIn >= day {
				archSlow.signIns++
			}
		}
	}
	return h, archSlow.signIns
}

// ---- the map ---------------------------------------------------------------------------

func (server *Server) architecture() archReply {
	health, signIns := server.archSlowRead()
	p := server.params()
	now := time.Now().Unix()
	r := archReply{At: now, Problems: recentProblems()}
	add := func(n archNode) { r.Nodes = append(r.Nodes, n) }
	link := func(id, lvl string) { r.Links = append(r.Links, archLink{ID: id, Level: lvl}) }

	httpReq, httpErr := meters.Get(meters.HTTPReq), meters.Get(meters.HTTPErr)
	httpRate := httpReq.PerMinute(60)
	httpStatus := archOK
	if n := httpReq.Sum(archWindow); n >= 20 && float64(httpErr.Sum(archWindow))*100/float64(n) > 5 {
		httpStatus = archWarn
	}

	// the model hosts and the chat proxy
	chatStatus, chatRate := archOK, 0.0
	var hostNodes []archNode
	for _, h := range []struct{ provider, id string }{{providerOpenAI, "openai"}, {providerOpenRouter, "openrouter"}} {
		st, perMin, errPct, avg := hostHealth(h.provider)
		keySet := utils.OpenAIKey() != ""
		if h.provider == providerOpenRouter {
			keySet = utils.OpenRouterKey() != ""
		}
		node := archNode{ID: h.id, Status: st, Text: "no requests lately", View: "health"}
		switch {
		case !keySet:
			node.Status, node.Text = archOff, "no key set"
		case meters.Get(meters.ChatReq+h.provider).Sum(archWindow) > 0:
			node.Text = ms(avg) + " · " + pct(errPct) + " errors"
		}
		node.Rows = []archRow{
			{"Requests a minute", rate(perMin), ""},
			{"Errors, last 5 minutes", pct(errPct), ""},
			{"Average answer time", ms(avg), ""},
		}
		hostNodes = append(hostNodes, node)
		if keySet {
			chatStatus = worse(chatStatus, st)
		}
		chatRate += perMin
	}
	chatTokens := meters.Get(meters.ChatTokens)
	chatNode := archNode{ID: "chat", Status: chatStatus, View: "usage", Series: minutes(meters.Get(meters.ChatReq + providerOpenAI))}
	// the requests of both hosts, added up minute by minute
	other := minutes(meters.Get(meters.ChatReq + providerOpenRouter))
	for i := range chatNode.Series {
		if i < len(other) {
			chatNode.Series[i] += other[i]
		}
	}
	chatNode.Text = rate(chatRate) + " req/min"
	if st := chatNode.Status; st == archWarn || st == archFail {
		chatNode.Text += " · errors"
	}
	chatNode.Rows = []archRow{
		{"Requests a minute", rate(chatRate), ""},
		{"Tokens a minute", rate(chatTokens.PerMinute(60)), ""},
	}
	for _, n := range hostNodes {
		chatNode.Rows = append(chatNode.Rows, archRow{Label: providerName(map[string]string{"openai": providerOpenAI, "openrouter": providerOpenRouter}[n.ID]), Value: n.Text, Status: n.Status})
	}

	// terminals
	terminals := p.Counts.Terminals
	maxTerm := server.options.MaxConnection
	sandboxStatus := archOK
	sandboxText := fmt.Sprintf("%d terminals · %d MB", terminals, p.Counts.TerminalWeight)
	if maxTerm > 0 {
		sandboxText = fmt.Sprintf("%d of %d terminals · %d MB", terminals, maxTerm, p.Counts.TerminalWeight)
		switch {
		case terminals >= maxTerm:
			sandboxStatus = archFail
		case terminals*5 >= maxTerm*4:
			sandboxStatus = archWarn
		}
	}
	if s := health["Sandbox"]; s == "warn" {
		sandboxStatus = worse(sandboxStatus, archWarn)
	}

	// the programs running now
	repls := meters.Gauges(meters.Repl)
	replText := "nothing running"
	var replRows []archRow
	if len(repls) > 0 {
		sort.Slice(repls, func(i, j int) bool { return repls[i].Value > repls[j].Value })
		parts := []string{}
		for i, g := range repls {
			if i < 4 {
				parts = append(parts, fmt.Sprintf("%s %d", g.Name, g.Value))
			}
			replRows = append(replRows, archRow{Label: g.Name, Value: fmt.Sprintf("%d", g.Value)})
		}
		replText = strings.Join(parts, " · ")
	}

	// the files
	fileEvents := meters.Get(meters.FileEvents)
	watching := meters.GaugeValue(meters.FileWatches)

	// the databases
	dbOps, dbErr := meters.Get(meters.DBOps), meters.Get(meters.DBErr)
	dbStatus := archOK
	if dbErr.Sum(archWindow) > 0 {
		dbStatus = archWarn
	}
	if health["Data directory"] == "fail" || health["Databases"] == "fail" {
		dbStatus = archFail
	}
	backend := ""
	if blog_db_handle != nil {
		backend = blog_db_handle.Backend()
	}
	external := backend != "" && backend != "unqlite"
	storeStatus, storeText := archOK, "UnQLite files"
	if free, total, err := diskFree(utils.GOTTY_PATH); err == nil && total > 0 {
		freePct := float64(free) * 100 / float64(total)
		storeText = fmt.Sprintf("%.0f%% disk free", freePct)
		if freePct < 15 {
			storeStatus = archWarn
		}
		if freePct < 5 {
			storeStatus = archFail
		}
	}
	if external {
		storeStatus, storeText = archOff, "not used"
	}
	extNode := archNode{ID: "external", Status: archOff, Text: "not set up"}
	if external {
		extNode.Status, extNode.Text = dbStatus, backend
	}

	fire := archOK
	switch health["Firebase"] {
	case "warn":
		fire = archWarn
	case "fail":
		fire = archFail
	}

	fmtTerm := fmt.Sprintf("%d connected", terminals)
	add(archNode{ID: "genie", Status: chatStatus, Text: rate(chatRate) + " answers a minute", View: "usage"})
	add(archNode{ID: "terminals", Status: archOK, Text: fmtTerm, View: "sessions"})
	add(archNode{ID: "page", Status: httpStatus, Text: rate(httpRate) + " requests a minute"})
	add(archNode{ID: "http", Status: httpStatus, Text: rate(httpRate) + " req/min · " + pct(errShare(httpReq, httpErr)) + " errors", Series: minutes(httpReq), Rows: []archRow{
		{"Requests a minute", rate(httpRate), ""}, {"Server errors, last 5 minutes", pct(errShare(httpReq, httpErr)), httpStatus},
	}})
	add(archNode{ID: "ws", Status: archOK, Text: fmt.Sprintf("%d sockets", terminals), View: "sessions"})
	add(chatNode)
	add(archNode{ID: "procs", Status: archOK, Text: fmt.Sprintf("%d terminals running", terminals), View: "sessions"})
	add(archNode{ID: "files", Status: archOK, Text: fmt.Sprintf("%d watching · %s events/min", watching, rate(fileEvents.PerMinute(60))), Series: minutes(fileEvents)})
	add(archNode{ID: "sandbox", Status: sandboxStatus, Text: sandboxText, View: "health"})
	add(archNode{ID: "repls", Status: archOK, Text: replText, Rows: replRows})
	if p.Counts.WorkersTotal > 0 || server.options.Mode == ModeGateway {
		ws := archOK
		if p.Counts.WorkersTotal > 0 && p.Counts.WorkersOnline == 0 {
			ws = archFail
		} else if p.Counts.WorkersOnline < p.Counts.WorkersTotal {
			ws = archWarn
		}
		add(archNode{ID: "workers", Status: ws, Text: fmt.Sprintf("%d of %d online", p.Counts.WorkersOnline, p.Counts.WorkersTotal), View: "workers"})
	}
	add(archNode{ID: "data", Status: dbStatus, Text: rate(dbOps.PerMinute(60)) + " ops/min · " + fmt.Sprintf("%d", dbErr.Sum(archWindow)) + " errors", Series: minutes(dbOps), View: "health"})
	add(archNode{ID: "store", Status: storeStatus, Text: storeText, View: "health"})
	add(extNode)
	add(archNode{ID: "fb-db", Status: fire, Text: "state only", View: "health"})
	add(archNode{ID: "fb-auth", Status: fire, Text: fmt.Sprintf("%d signed in today", signIns), View: "users"})
	r.Nodes = append(r.Nodes, hostNodes...)

	termLevel := level(float64(terminals))
	link("page-http", level(httpRate))
	link("term-ws", termLevel)
	link("genie-chat", level(chatRate))
	link("ws-exec", termLevel)
	link("data-persist", level(dbOps.PerMinute(60)))
	link("persist-store", level(dbOps.PerMinute(60)))
	link("chat-llm", level(chatRate))
	r.Links = append(r.Links, archLink{ID: "browser-firebase", Level: archState, Status: fire})
	if p.Counts.WorkersTotal > 0 || server.options.Mode == ModeGateway {
		link("exec-workers", termLevel)
	}
	for i := range r.Links {
		if r.Links[i].Status == "" {
			r.Links[i].Status = archOK
		}
	}
	// a line into a part that has a problem shows it
	status := map[string]string{}
	for _, n := range r.Nodes {
		status[n.ID] = n.Status
	}
	for i, l := range r.Links {
		switch l.ID {
		case "chat-llm":
			r.Links[i].Status = status["chat"]
		case "data-persist":
			r.Links[i].Status = status["data"]
		}
	}
	return r
}

func errShare(req, errs *meters.Series) float64 {
	n := req.Sum(archWindow)
	if n == 0 {
		return 0
	}
	return float64(errs.Sum(archWindow)) * 100 / float64(n)
}

func (server *Server) handleAdminArchitecture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		adminError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	adminJSON(w, http.StatusOK, server.architecture())
}
