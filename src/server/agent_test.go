package server

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"cookie"
	"encoder"
)

// ---- the ledger -----------------------------------------------------------------------

func testLedger() (*agentLedger, *time.Time) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	l := newAgentLedger()
	l.now = func() time.Time { return now }
	l.secret = func() []byte { return []byte("test-secret") }
	return l, &now
}

func TestATaskHasAtMostItsSteps(t *testing.T) {
	l, _ := testLedger()
	run, err := l.begin("u1", 20, 3)
	if err != nil || run.step != 1 || run.max != 3 || !run.first {
		t.Fatalf("begin: %+v %v", run, err)
	}
	for want := 2; want <= 3; want++ {
		r, err := l.next(run.token, "u1", 3)
		if err != nil || r.step != want || r.first {
			t.Fatalf("step %d: %+v %v", want, r, err)
		}
	}
	if _, err := l.next(run.token, "u1", 3); err != errAgentSteps {
		t.Fatalf("a fourth step: %v", err)
	}
	// an admin lowering the steps ends a task that is further on
	run2, _ := l.begin("u2", 20, 8)
	l.next(run2.token, "u2", 8)
	l.next(run2.token, "u2", 8)
	if _, err := l.next(run2.token, "u2", 2); err != errAgentSteps {
		t.Fatalf("after lowering the steps: %v", err)
	}
}

func TestATokenIsOnlyGoodForItsUserAndItsServer(t *testing.T) {
	l, _ := testLedger()
	run, _ := l.begin("u1", 20, 8)
	if _, err := l.next(run.token, "u2", 8); err != errAgentInvalid {
		t.Errorf("another user: %v", err)
	}
	id, sig, _ := strings.Cut(run.token, ".")
	for _, forged := range []string{id + "." + strings.Repeat("0", 32), id + ".", id, "." + sig, "x." + sig, "", id + "." + sig + "00", strings.Repeat("a", 60) + "." + sig} {
		if _, err := l.next(forged, "u1", 8); err != errAgentInvalid {
			t.Errorf("token %q accepted: %v", forged, err)
		}
	}
	// another server (another secret) does not accept it
	other, _ := testLedger()
	other.secret = func() []byte { return []byte("another") }
	if _, err := other.next(run.token, "u1", 8); err != errAgentInvalid {
		t.Errorf("another secret: %v", err)
	}
	// a token that is signed but was never issued, or was given back
	made := l.token("u1", "feedfeedfeed")
	if _, err := l.next(made, "u1", 8); err != errAgentInvalid {
		t.Errorf("a task nobody started: %v", err)
	}
}

func TestATaskEndsAfterItsTimeAndTheHourlyCountRollsOver(t *testing.T) {
	l, now := testLedger()
	run, _ := l.begin("u1", 2, 8)
	l.begin("u1", 2, 8)
	if _, err := l.begin("u1", 2, 8); err == nil || err.Code != "agent_task_limit" || err.Status != http.StatusTooManyRequests {
		t.Fatalf("a third task in the hour: %v", err)
	}
	if _, err := l.begin("u2", 2, 8); err != nil {
		t.Errorf("another user was limited: %v", err)
	}
	*now = now.Add(agentTaskTTL + time.Second)
	if _, err := l.next(run.token, "u1", 8); err != errAgentInvalid {
		t.Errorf("a task after its time: %v", err)
	}
	if _, err := l.begin("u1", 2, 8); err == nil {
		t.Error("the hour is not over, the count was forgotten")
	}
	*now = now.Add(agentWindow)
	if _, err := l.begin("u1", 2, 8); err != nil {
		t.Errorf("after the hour: %v", err)
	}
	// 0 is no limit
	for i := 0; i < 50; i++ {
		if _, err := l.begin("admin", 0, 8); err != nil {
			t.Fatalf("a task without a limit: %v", err)
		}
	}
}

func TestAStepIsGivenBackWhenTheModelCouldNotAnswer(t *testing.T) {
	l, _ := testLedger()
	run, _ := l.begin("u1", 1, 3)
	l.undo(run) // the first step failed: the task and its place in the hour go back
	if _, err := l.next(run.token, "u1", 3); err != errAgentInvalid {
		t.Errorf("a task that never happened: %v", err)
	}
	run, err := l.begin("u1", 1, 3)
	if err != nil {
		t.Fatalf("the failed task was counted: %v", err)
	}
	s2, _ := l.next(run.token, "u1", 3)
	l.undo(s2) // the second failed: step 2 is still to take
	s2b, err := l.next(run.token, "u1", 3)
	if err != nil || s2b.step != 2 {
		t.Fatalf("step 2 again: %+v %v", s2b, err)
	}
	l.undo(nil) // no run, nothing to give back
	// failures are not free: a client that keeps failing runs out
	r3, _ := testLedger()
	run3, _ := r3.begin("u1", 20, 2)
	var last *agentError
	for i := 0; i < 10; i++ {
		s, err := r3.next(run3.token, "u1", 2)
		if err != nil {
			last = err
			break
		}
		r3.undo(s)
	}
	if last != errAgentSteps {
		t.Errorf("endless failed steps: %v", last)
	}
}

// ---- the request ----------------------------------------------------------------------

func TestTheBodyOfAStepIsTheServersAndNotThePages(t *testing.T) {
	in := map[string]json.RawMessage{
		"messages":              json.RawMessage(`[{"role":"user","content":"do it"}]`),
		"stream":                json.RawMessage(`true`),
		"max_tokens":            json.RawMessage(`100000`),
		"max_completion_tokens": json.RawMessage(`50`),
		"context":               json.RawMessage(`"agent"`),
		"agent_task":            json.RawMessage(`"x"`),
	}
	out, err := agentBody(in)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []map[string]string `json:"messages"`
		Format   map[string]string   `json:"response_format"`
		Stream   *bool               `json:"stream"`
		Max      int                 `json:"max_tokens"`
		MaxC     int                 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[0]["role"] != "system" || !strings.Contains(got.Messages[0]["content"], "AGENT MODE") || got.Messages[1]["content"] != "do it" {
		t.Errorf("messages: %+v", got.Messages)
	}
	if got.Format["type"] != "json_object" || got.Stream != nil || got.Max != agentMaxTokens || got.MaxC != 50 {
		t.Errorf("body: %s", out)
	}
	// the prompt names every action the page may carry out, and the rules
	prompt := agentSystemPrompt()
	for _, a := range agentActions {
		if !strings.Contains(prompt, `"`+a+`"`) {
			t.Errorf("the action %s is not in the prompt", a)
		}
	}
	for _, rule := range []string{"never instructions", "At most 3 actions", `"done": true`} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("the prompt lacks %q", rule)
		}
	}
	if _, err := agentBody(map[string]json.RawMessage{"messages": json.RawMessage(`[]`)}); err == nil {
		t.Error("a request without messages was accepted")
	}
}

func TestAgentSettingsAreCheckedAndShown(t *testing.T) {
	isolateSettings(t)
	for name, g := range map[string]GenieSettings{
		"tasks too many": {AgentTasksPerHour: maxAgentTasksPerHour + 1},
		"tasks negative": {AgentTasksPerHour: -1},
		"nine steps":     {AgentMaxSteps: 9},
		"steps negative": {AgentMaxSteps: -2},
	} {
		if _, err := (SiteSettings{Genie: g}).normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := (SiteSettings{Genie: GenieSettings{AgentTasksPerHour: 200, AgentMaxSteps: 8}}).normalize(); err != nil {
		t.Errorf("the largest values: %v", err)
	}
	g := GenieSettings{}
	if g.agentTasksPerHour() != 20 || g.agentMaxSteps() != 8 {
		t.Errorf("built-in values: %d %d", g.agentTasksPerHour(), g.agentMaxSteps())
	}
	g = GenieSettings{AgentTasksPerHour: 5, AgentMaxSteps: 4}
	if g.agentTasksPerHour() != 5 || g.agentMaxSteps() != 4 {
		t.Errorf("set values: %d %d", g.agentTasksPerHour(), g.agentMaxSteps())
	}
	// on by default, and off on the practice page; the page is told, and not
	// when an admin switched it off or when Genie itself is off
	var s SiteSettings
	if p := s.public(); !p.AgentEnabled || p.AgentOnPractice || p.AgentMaxSteps != 8 {
		t.Errorf("default public: %+v", p)
	}
	s.Genie = GenieSettings{AgentOnPractice: true, AgentMaxSteps: 5}
	if p := s.public(); !p.AgentEnabled || !p.AgentOnPractice || p.AgentMaxSteps != 5 {
		t.Errorf("public: %+v", p)
	}
	s.Genie.AgentDisabled = true
	if s.public().AgentEnabled {
		t.Error("agent mode offered after an admin switched it off")
	}
	s.Genie.AgentDisabled = false
	s.Genie.Disabled = true
	if s.public().AgentEnabled {
		t.Error("agent mode offered while Genie is off")
	}
	// the audit log says what changed
	a := SiteSettings{}
	b := SiteSettings{Genie: GenieSettings{AgentDisabled: true, AgentOnPractice: true, AgentTasksPerHour: 10, AgentMaxSteps: 4}}
	joined := strings.Join(settingsChanges(a, b), "|")
	for _, want := range []string{"agent mode off", "agent mode on the practice page on", "agent limits: 10 tasks an hour, 4 steps a task"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit lacks %q in %q", want, joined)
		}
	}
	d := currentGenieDefaults()
	if d.AgentTasksPerHour != 20 || d.AgentMaxSteps != 8 || d.Limits["agentMaxSteps"] != [2]int{1, 8} || d.Limits["agentTasksPerHour"] != [2]int{1, 200} {
		t.Errorf("defaults: %+v", d)
	}
}

// ---- through the proxy ----------------------------------------------------------------

func agentCall(t *testing.T, body string, c *http.Cookie, referer string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := encoder.Encrypt([]byte(openAIToken()), cookie.SECRET_KEY)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost")
	if referer == "" {
		referer = "http://localhost/"
	}
	req.Header.Set("Referer", referer)
	req.Header.Set("Authorization", "Bearer "+string(token))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleChatProxy(w, req)
	return w
}

// agentSetup gives a server with agent mode on, a model that answers, and a
// signed-in user.
func agentSetup(t *testing.T, g GenieSettings) (c *http.Cookie, lastBody *string) {
	t.Helper()
	adminTestServer(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	g.AgentDisabled = false
	if err := SaveSiteSettings(SiteSettings{Genie: g}); err != nil {
		t.Fatal(err)
	}
	agents = newAgentLedger()
	t.Cleanup(func() { agents = newAgentLedger() })
	url, _, body := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"{\"say\":\"ok\",\"actions\":[],\"done\":true}"}}]}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	return sessionCookie(t, "uid-ann", "ann@example.com"), body
}

const agentStep1 = `{"model":"gpt-4o-mini","context":"agent","stream":true,"messages":[{"role":"user","content":"[user-ab] write hello"}]}`

func agentStepN(token string) string {
	return `{"model":"gpt-4o-mini","context":"agent","agent_task":"` + token + `","messages":[{"role":"user","content":"[user-ab] write hello"}]}`
}

func TestAgentModeIsRefusedUnlessItIsOnAndTheUserIsSignedIn(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{})
	code := func(w *httptest.ResponseRecorder) string {
		var e ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &e)
		return e.Error.Code
	}
	// a guest
	if w := agentCall(t, agentStep1, nil, ""); w.Code != http.StatusForbidden || code(w) != "agent_login_required" {
		t.Errorf("a guest: %d %s", w.Code, w.Body.String())
	}
	// switched off by an admin
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{AgentDisabled: true}}); err != nil {
		t.Fatal(err)
	}
	if w := agentCall(t, agentStep1, c, ""); w.Code != http.StatusServiceUnavailable || code(w) != "agent_disabled" {
		t.Errorf("switched off: %d %s", w.Code, w.Body.String())
	}
	// the practice page
	SaveSiteSettings(SiteSettings{})
	for _, ref := range []string{"http://localhost/practice", "http://localhost/practice?name=two-sum", "http://localhost/practice/"} {
		if w := agentCall(t, agentStep1, c, ref); w.Code != http.StatusForbidden || code(w) != "agent_not_on_practice" {
			t.Errorf("practice %s: %d %s", ref, w.Code, w.Body.String())
		}
	}
	// the admin option lifts it
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{AgentOnPractice: true}})
	if w := agentCall(t, agentStep1, c, "http://localhost/practice?name=two-sum"); w.Code != 200 {
		t.Errorf("practice allowed: %d %s", w.Code, w.Body.String())
	}
	// a page that only looks like it
	if onPracticePage(httptest.NewRequest("POST", "/", nil)) {
		t.Error("a request without a Referer is on the practice page")
	}
	for _, ref := range []string{"http://localhost/practices", "http://localhost/x/practice", "http://localhost/?practice", "%%%"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Referer", ref)
		if onPracticePage(r) {
			t.Errorf("%s counted as the practice page", ref)
		}
	}
}

func TestAnAgentTaskGoesThroughTheProxyStepByStep(t *testing.T) {
	c, lastBody := agentSetup(t, GenieSettings{AgentMaxSteps: 3})
	w := agentCall(t, agentStep1, c, "")
	if w.Code != 200 {
		t.Fatalf("step 1: %d %s", w.Code, w.Body.String())
	}
	token := w.Header().Get(agentTaskHeader)
	if token == "" || w.Header().Get(agentStepHeader) != "1/3" || !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), agentTaskHeader) {
		t.Fatalf("headers: %v", w.Header())
	}
	// what the model was sent
	for _, want := range []string{"AGENT MODE", `"response_format":{"type":"json_object"}`} {
		if !strings.Contains(*lastBody, want) {
			t.Errorf("the model's request lacks %q: %s", want, *lastBody)
		}
	}
	for _, not := range []string{`"stream"`, "agent_task", `"context"`} {
		if strings.Contains(*lastBody, not) {
			t.Errorf("the model's request has %q", not)
		}
	}
	for i, want := range []string{"2/3", "3/3"} {
		w = agentCall(t, agentStepN(token), c, "")
		if w.Code != 200 || w.Header().Get(agentStepHeader) != want || w.Header().Get(agentTaskHeader) != token {
			t.Fatalf("step %d: %d %v", i+2, w.Code, w.Header())
		}
	}
	w = agentCall(t, agentStepN(token), c, "")
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusTooManyRequests || e.Error.Code != "agent_step_limit" {
		t.Errorf("a fourth step: %d %s", w.Code, w.Body.String())
	}
	// someone else's token, and no token at a later step
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if w := agentCall(t, agentStepN(token), other, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("another user's token: %d", w.Code)
	}
	if w := agentCall(t, agentStepN("garbage"), c, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("a garbage token: %d", w.Code)
	}
}

func TestOnlyTheFirstStepOfATaskCostsAUnit(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{})
	// a response may set the session cookie twice (the refill, then the charge):
	// the browser keeps the last
	lastCookie := func(w *httptest.ResponseRecorder) *http.Cookie {
		list := w.Result().Cookies()
		return list[len(list)-1]
	}
	balance := func(w *httptest.ResponseRecorder) float64 {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(lastCookie(w))
		return cookie.GetOpenApiRequestCount(r)
	}
	w1 := agentCall(t, agentStep1, c, "")
	b1 := balance(w1)
	token := w1.Header().Get(agentTaskHeader)
	next := lastCookie(w1)
	w2 := agentCall(t, agentStepN(token), next, "")
	b2 := balance(w2)
	w3 := agentCall(t, agentStepN(token), lastCookie(w2), "")
	b3 := balance(w3)
	if w2.Code != 200 || w3.Code != 200 {
		t.Fatalf("steps: %d %d", w2.Code, w3.Code)
	}
	if b1 > 59.5 {
		t.Errorf("the first step was not charged: %.2f", b1)
	}
	if b2 < b1-0.5 || b3 < b2-0.5 {
		t.Errorf("later steps were charged: %.2f -> %.2f -> %.2f", b1, b2, b3)
	}
	// a plain chat message still costs one
	chat := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	w4 := agentCall(t, chat, lastCookie(w3), "")
	if b4 := balance(w4); b4 > b3-0.5 {
		t.Errorf("a chat message was not charged: %.2f -> %.2f", b3, b4)
	}
}

func TestTheHourlyCountAndAFailedModel(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{AgentTasksPerHour: 2})
	for i := 0; i < 2; i++ {
		if w := agentCall(t, agentStep1, c, ""); w.Code != 200 {
			t.Fatalf("task %d: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := agentCall(t, agentStep1, c, "")
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusTooManyRequests || e.Error.Code != "agent_task_limit" {
		t.Errorf("a third task: %d %s", w.Code, w.Body.String())
	}

	// a model that is down does not use up a task
	agents = newAgentLedger()
	url, _, _ := fakeUpstream(t, 500, `{"error":{"message":"boom"}}`)
	old := openaiEndpoint
	openaiEndpoint = url
	defer func() { openaiEndpoint = old }()
	for i := 0; i < 5; i++ {
		w := agentCall(t, agentStep1, c, "")
		if w.Code != 500 {
			t.Fatalf("a model that fails: %d", w.Code)
		}
		// the task was given back, so its unit is not taken either
		if list := w.Result().Cookies(); len(list) > 0 {
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(list[len(list)-1])
			if left := cookie.GetOpenApiRequestCount(r); left < 59.5 {
				t.Errorf("a task the model could not answer was charged: %.2f left", left)
			}
		}
	}
	ok, _, _ := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"{}"}}]}`)
	openaiEndpoint = ok
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 {
		t.Errorf("failed tasks were counted: %d %s", w.Code, w.Body.String())
	}
}

func TestAnAdminHasNoHourlyCountButTheSameSteps(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{AgentTasksPerHour: 1, AgentMaxSteps: 2})
	boss := sessionCookie(t, "uid-boss", "boss@example.com") // OPENREPL_ADMIN_EMAILS names boss
	var token string
	for i := 0; i < 4; i++ {
		w := agentCall(t, agentStep1, boss, "")
		if w.Code != 200 {
			t.Fatalf("admin task %d: %d %s", i+1, w.Code, w.Body.String())
		}
		token = w.Header().Get(agentTaskHeader)
	}
	agentCall(t, agentStepN(token), boss, "")
	if w := agentCall(t, agentStepN(token), boss, ""); w.Code != http.StatusTooManyRequests {
		t.Errorf("an admin's third step: %d", w.Code)
	}
	// and an ordinary user is held to the count
	agentCall(t, agentStep1, c, "")
	if w := agentCall(t, agentStep1, c, ""); w.Code != http.StatusTooManyRequests {
		t.Errorf("a user's second task: %d", w.Code)
	}
}

func TestOtherRequestsAreNotTouchedByAgentMode(t *testing.T) {
	c, lastBody := agentSetup(t, GenieSettings{})
	w := agentCall(t, `{"model":"gpt-4o-mini","stream":false,"messages":[{"role":"user","content":"hi"}]}`, c, "")
	if w.Code != 200 || w.Header().Get(agentTaskHeader) != "" || strings.Contains(*lastBody, "AGENT MODE") || strings.Contains(*lastBody, "json_object") {
		t.Errorf("a chat request: %d %v %s", w.Code, w.Header(), *lastBody)
	}
	// a body that is not JSON is left to the existing checks
	if run, _, err := agentStart(httptest.NewRequest("POST", "/", nil), []byte("nope"), false); run != nil || err != nil {
		t.Errorf("not JSON: %v %v", run, err)
	}
}

func TestTheDashboardSavesAndReturnsTheAgentSettings(t *testing.T) {
	_, h := adminTestServer(t)
	var before settingsReply
	decode(t, get(h, "/admin/settings"), &before)
	if before.Genie.AgentDisabled || before.Genie.AgentOnPractice || before.Defaults.AgentTasksPerHour != 20 || before.Defaults.AgentMaxSteps != 8 {
		t.Fatalf("a new server: %+v %+v", before.Genie, before.Defaults)
	}
	body := `{"colorOfTheDay":false,"announcement":{"text":"","level":"info"},"maintenance":{"enabled":false,"message":""},"disabledLanguages":[],` +
		`"genie":{"disabled":false,"agentDisabled":true,"agentOnPractice":true,"agentTasksPerHour":7,"agentMaxSteps":5}}`
	w := post(h, "/admin/settings", body)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var after settingsReply
	decode(t, w, &after)
	if !after.Genie.AgentDisabled || !after.Genie.AgentOnPractice || after.Genie.AgentTasksPerHour != 7 || after.Genie.AgentMaxSteps != 5 {
		t.Errorf("saved: %+v", after.Genie)
	}
	if got := GetSiteSettings().Genie; got.agentTasksPerHour() != 7 || got.agentMaxSteps() != 5 {
		t.Errorf("in use: %+v", got)
	}
	// too many steps or tasks are refused with the reason
	for _, bad := range []string{`"agentMaxSteps":9`, `"agentTasksPerHour":500`, `"agentMaxSteps":-1`} {
		w := post(h, "/admin/settings", strings.Replace(body, `"agentMaxSteps":5`, bad, 1))
		if bad == `"agentTasksPerHour":500` {
			w = post(h, "/admin/settings", strings.Replace(body, `"agentTasksPerHour":7`, bad, 1))
		}
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "agent") {
			t.Errorf("%s: %d %s", bad, w.Code, w.Body.String())
		}
	}
	// the page is told only what it needs
	pub := httptest.NewRecorder()
	handleSettingsJS(pub, httptest.NewRequest("GET", "/settings.js", nil))
	if !strings.Contains(pub.Body.String(), `"agentEnabled":false`) || !strings.Contains(pub.Body.String(), `"agentMaxSteps":5`) || strings.Contains(pub.Body.String(), "agentTasksPerHour") {
		t.Errorf("settings.js: %s", pub.Body.String())
	}
	// the audit log has the change
	var log struct{ Entries []struct{ Detail string } }
	decode(t, get(h, "/admin/audit"), &log)
	found := false
	for _, e := range log.Entries {
		found = found || strings.Contains(e.Detail, "agent mode off")
	}
	if !found {
		t.Errorf("audit: %s", get(h, "/admin/audit").Body.String())
	}
}

// The page and the server each hold the list of actions (agent-protocol.ts and
// agentActions); the model is told one list and the page carries out the
// other, so they must be the same, in the same order.
func TestThePagesActionsAreTheServers(t *testing.T) {
	src, err := ioutil.ReadFile(filepath.Join("..", "resources", "chat-widget", "src", "agent-protocol.ts"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`ACTIONS = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no ACTIONS list in agent-protocol.ts")
	}
	var page []string
	for _, q := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(m[1], -1) {
		page = append(page, string(q[1]))
	}
	if strings.Join(page, ",") != strings.Join(agentActions, ",") {
		t.Errorf("the page has %v, the server %v", page, agentActions)
	}
	// and the header names the page reads are the ones the server sets
	agent, err := ioutil.ReadFile(filepath.Join("..", "resources", "chat-widget", "src", "agent.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{agentTaskHeader, agentStepHeader} {
		if !strings.Contains(string(agent), `"`+h+`"`) {
			t.Errorf("agent.ts does not read %s", h)
		}
	}
	for _, f := range []string{`context: "` + contextAgent + `"`, "agent_task:"} {
		if !strings.Contains(string(agent), f) {
			t.Errorf("agent.ts does not send %q", f)
		}
	}
}
