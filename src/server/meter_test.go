package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"persist"
)

func TestCreditsComeFromTheWeightThenThePriceThenTheReportedCost(t *testing.T) {
	u := usageCount{In: 2000, Out: 1000}
	g := GenieSettings{}
	if c := g.creditsFor("m", u); c != 0 {
		t.Errorf("no weight, no price, no reported cost: %v", c)
	}
	// a dollar for a million tokens is a credit for a thousand
	g.ModelPrices = map[string]modelPrice{"m": {In: 0.5, Out: 2}}
	if c := g.creditsFor("m", u); c != 3 { // 2*0.5 + 1*2
		t.Errorf("by the price: %v", c)
	}
	g.CreditWeights = map[string]modelPrice{"m": {In: 1, Out: 4}}
	if c := g.creditsFor("m", u); c != 6 { // 2*1 + 1*4
		t.Errorf("by the weight: %v", c)
	}
	// the cost the provider reported, when nothing else says
	rep := usageCount{In: 10, Out: 10, Cost: 0.0042, HasCost: true}
	if c := (GenieSettings{}).creditsFor("x", rep); c != 4.2 {
		t.Errorf("by the reported cost: %v", c)
	}
	// a free model costs nothing
	if c := (GenieSettings{ModelPrices: map[string]modelPrice{"f": {}}}).creditsFor("f", u); c != 0 {
		t.Errorf("free: %v", c)
	}
}

func TestACountedStepIsNotCountedAgain(t *testing.T) {
	l := newMeterLedger()
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	ev := meterEvent{Key: "u:a", Kind: kindAgent, Task: "t1", Step: 1, First: true, Credits: 2.5}
	if !l.add(ev) {
		t.Fatal("the first count was refused")
	}
	if l.add(ev) {
		t.Error("the same step was counted twice")
	}
	ev.Step, ev.First = 2, false
	l.add(ev)
	l.add(meterEvent{Key: "u:a", Kind: kindChat, Credits: 1})
	r := l.month("u:a")
	if r.Credits != 6 || r.Steps != 2 || r.Tasks != 1 || r.Chats != 1 || r.ACredits != 6 {
		t.Fatalf("the record: %+v", r)
	}
	// a new month starts the month's numbers again, not the all-time one
	now = now.AddDate(0, 1, 0)
	if r := l.month("u:a"); r.Credits != 0 || r.Steps != 0 || r.ACredits != 6 {
		t.Errorf("next month: %+v", r)
	}
	l.add(meterEvent{Key: "u:a", Kind: kindChat, Credits: 1})
	if r := l.month("u:a"); r.Credits != 1 || r.ACredits != 7 {
		t.Errorf("next month, one answer: %+v", r)
	}
}

func TestThePlanIsTodaysLimitsUntilCreditsAreOn(t *testing.T) {
	g := GenieSettings{PlanCredits: 500, PlanBudget: 40}
	e := g.entitlementsFor("u1", false)
	if e.Enforce || e.CreditsPerMonth != 0 || e.BudgetPerTask != 0 || e.MaxSteps != defaultAgentMaxSteps || e.TasksPerHour != defaultAgentTasksPerHour || e.Plan != "Free" {
		t.Errorf("credits off: %+v", e)
	}
	g.CreditsOn, g.PlanName = true, "Team"
	e = g.entitlementsFor("u1", false)
	if !e.Enforce || e.CreditsPerMonth != 500 || e.BudgetPerTask != 40 || e.Plan != "Team" {
		t.Errorf("credits on: %+v", e)
	}
	// an admin is not held to credits or to the hourly count; a visitor has no plan
	if a := g.entitlementsFor("u1", true); a.Enforce || a.TasksPerHour != 0 {
		t.Errorf("admin: %+v", a)
	}
	if v := g.entitlementsFor("", false); v.Enforce {
		t.Errorf("a visitor: %+v", v)
	}
}

func TestTheCreditNumbersOfTheSettingsAreChecked(t *testing.T) {
	ok := GenieSettings{PlanCredits: 500, PlanBudget: 40, PlanName: " Team ", CreditWeights: map[string]modelPrice{"gpt-4o-mini": {In: 0.2, Out: 0.8}, "google/gemma-4-31b-it": {}}}
	if err := ok.normalizeCredits(); err != nil || ok.PlanName != "Team" || len(ok.CreditWeights) != 1 {
		t.Fatalf("a good setting: %v %+v", err, ok)
	}
	for name, g := range map[string]GenieSettings{
		"negative credits": {PlanCredits: -1},
		"too many credits": {PlanBudget: maxCredits + 1},
		"unknown model":    {CreditWeights: map[string]modelPrice{"nope": {In: 1}}},
		"negative weight":  {CreditWeights: map[string]modelPrice{"gpt-4o-mini": {In: -1}}},
		"a very long name": {PlanName: strings.Repeat("x", 41)},
	} {
		if err := g.normalizeCredits(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

const answerWithUsage = `{"choices":[{"message":{"content":"{\"say\":\"ok\",\"actions\":[],\"done\":true}"}}],"usage":{"prompt_tokens":2000,"completion_tokens":1000}}`

func meterSetup(t *testing.T, g GenieSettings) (c *http.Cookie) {
	t.Helper()
	c, _ = agentSetup(t, g)
	meter.forget()
	t.Cleanup(meter.forget)
	url, _, _ := fakeUpstream(t, 200, answerWithUsage)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	return c
}

func TestEveryAgentStepAndChatAnswerIsRecordedWithItsCredits(t *testing.T) {
	g := GenieSettings{ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 0.5, Out: 2}}}
	c := meterSetup(t, g)
	w := agentCall(t, agentStep1, c, "")
	if w.Code != 200 {
		t.Fatalf("step 1: %d %s", w.Code, w.Body.String())
	}
	token := w.Header().Get(agentTaskHeader)
	if w := agentCall(t, agentStepN(token), c, ""); w.Code != 200 {
		t.Fatalf("step 2: %d", w.Code)
	}
	chat := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	if w := agentCall(t, chat, c, ""); w.Code != 200 {
		t.Fatalf("chat: %d", w.Code)
	}
	r := meter.month("u:uid-ann")
	if r.Steps != 2 || r.Tasks != 1 || r.Chats != 1 || r.Credits != 9 { // 3 credits each
		t.Fatalf("the record: %+v", r)
	}
	// the task has its own credits, for the page to tell
	u := usageCallTask(t, c, token)
	if u.Task == nil || u.Task.Credits != 6 || u.Task.Steps != 2 {
		t.Fatalf("the task: %+v", u.Task)
	}
	// credits are not on: nobody is told, nobody is limited
	if u.Credits != nil {
		t.Errorf("credits shown while off: %+v", u.Credits)
	}
	// someone else cannot read the task
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if o := usageCallTask(t, other, token); o.Task != nil {
		t.Errorf("another user read the task: %+v", o.Task)
	}
}

func TestTheRecordCanBeSwitchedOff(t *testing.T) {
	g := GenieSettings{MeterOff: true, ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 1, Out: 1}}}
	c := meterSetup(t, g)
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if r := meter.month("u:uid-ann"); r.Credits != 0 || r.Steps != 0 {
		t.Errorf("recorded while off: %+v", r)
	}
}

func TestCreditsOnShowTheLineAndStopAMonthThatIsUsedUp(t *testing.T) {
	g := GenieSettings{CreditsOn: true, PlanCredits: 5, PlanBudget: 40, ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 0.5, Out: 2}}}
	c := meterSetup(t, g)
	chat := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	if w := agentCall(t, chat, c, ""); w.Code != 200 { // 3 of 5
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	u := usageCall(t, c)
	if u.Credits == nil || u.Credits.Of != 5 || u.Credits.Used != 3 || u.Credits.Left != 2 || u.Credits.TaskBudget != 40 || u.Credits.Plan != "Free" || len(u.Credits.Resets) != 10 {
		t.Fatalf("the credits line: %+v", u.Credits)
	}
	if w := agentCall(t, chat, c, ""); w.Code != 200 { // 6 of 5: the last one that is allowed
		t.Fatalf("second: %d %s", w.Code, w.Body.String())
	}
	w := agentCall(t, chat, c, "")
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusTooManyRequests || e.Error.Code != "credits_exhausted" {
		t.Fatalf("a used-up month: %d %s", w.Code, w.Body.String())
	}
	// and agent mode is refused the same way, before it takes a task
	if w := agentCall(t, agentStep1, c, ""); w.Code != http.StatusTooManyRequests {
		t.Errorf("agent mode with no credits: %d", w.Code)
	}
	if used, _ := agents.started("uid-ann"); used != 0 {
		t.Errorf("a refused task was counted: %d", used)
	}
	// the admin switching credits off gives the user the service back
	g.CreditsOn = false
	SaveSiteSettings(SiteSettings{Genie: g})
	if w := agentCall(t, chat, c, ""); w.Code != 200 {
		t.Errorf("credits off again: %d", w.Code)
	}
}

func tellOutcome(t *testing.T, c *http.Cookie, task, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/chat/agent-outcome", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleAgentOutcome(w, req)
	return w.Code
}

func TestTheAgentCountsAreKeptAndEachTaskEndsOnce(t *testing.T) {
	g := GenieSettings{AgentMaxSteps: 2}
	c := meterSetup(t, g)
	statsForget()
	t.Cleanup(statsForget)
	w := agentCall(t, agentStep1, c, "")
	token := w.Header().Get(agentTaskHeader)
	agentCall(t, agentStepN(token), c, "")
	if w := agentCall(t, agentStepN(token), c, ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a third step: %d", w.Code)
	}
	s := statsSnapshot().Now
	if s.Tasks != 1 || s.Steps != 2 || s.StepLimit != 1 || s.ReqBytes <= 0 {
		t.Fatalf("the counts: %+v", s)
	}
	// the page says how it ended: once, for its own user only
	done := `{"task":"` + token + `","outcome":"done","checked":true,"changed":true}`
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if code := tellOutcome(t, other, token, done); code != http.StatusNoContent {
		t.Errorf("another user: %d", code)
	}
	if s := statsSnapshot().Now; s.Done != 0 {
		t.Errorf("another user's word was counted: %+v", s)
	}
	tellOutcome(t, c, token, done)
	tellOutcome(t, c, token, done) // again: not counted again
	if s := statsSnapshot().Now; s.Done != 1 || s.Checked != 1 || s.NotChecked != 0 {
		t.Errorf("one outcome: %+v", s)
	}
	// a visitor, a bad body and an unknown outcome count nothing
	if code := tellOutcome(t, nil, token, done); code != http.StatusBadRequest {
		t.Errorf("a visitor: %d", code)
	}
	if code := tellOutcome(t, c, token, `{"task":`); code != http.StatusBadRequest {
		t.Errorf("bad json: %d", code)
	}
	if code := tellOutcome(t, c, token, `{"task":"`+token+`","outcome":"exploded"}`); code != http.StatusNoContent {
		t.Errorf("unknown outcome: %d", code)
	}
	// the baseline keeps today's values, and later counts come on top of it
	statsBaseline(time.Now())
	r := statsSnapshot()
	if r.Base == nil || r.Base.Steps != 2 || r.BaseAt == 0 {
		t.Fatalf("the baseline: %+v", r)
	}
	w = agentCall(t, agentStep1, c, "")
	if r := statsSnapshot(); r.Now.Tasks != 2 || r.Base.Tasks != 1 {
		t.Errorf("after the baseline: %+v", r)
	}
	agentCall(t, agentStepN(w.Header().Get(agentTaskHeader)), c, "")
}

func TestTheDashboardSavesAndReturnsTheCreditSettings(t *testing.T) {
	_, h := adminTestServer(t)
	body := `{"colorOfTheDay":false,"announcement":{"text":"","level":"info"},"maintenance":{"enabled":false,"message":""},"disabledLanguages":[],` +
		`"genie":{"disabled":false,"creditsOn":true,"planName":"Team","planCredits":500,"planBudget":40,"creditWeights":{"gpt-4o-mini":{"in":0.2,"out":0.8}}}}`
	w := post(h, "/admin/settings", body)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	got := GetSiteSettings().Genie
	if !got.CreditsOn || got.PlanName != "Team" || got.PlanCredits != 500 || got.PlanBudget != 40 || got.CreditWeights["gpt-4o-mini"].Out != 0.8 || got.MeterOff {
		t.Fatalf("in use: %+v", got)
	}
	for _, bad := range []string{`"planCredits":-5`, `"creditWeights":{"nope":{"in":1,"out":1}}`} {
		if w := post(h, "/admin/settings", strings.Replace(body, `"planCredits":500`, bad, 1)); bad == `"planCredits":-5` && w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	// the measures: only an admin, GET and the baseline
	if w := get(h, "/admin/genie-measures"); w.Code != 200 || !strings.Contains(w.Body.String(), `"now"`) {
		t.Errorf("measures: %d %s", w.Code, w.Body.String())
	}
	if w := post(h, "/admin/genie-measures", `{"action":"baseline"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"base"`) {
		t.Errorf("baseline: %d %s", w.Code, w.Body.String())
	}
	if w := post(h, "/admin/genie-measures", `{"action":"drop"}`); w.Code != http.StatusBadRequest {
		t.Errorf("another action: %d", w.Code)
	}
	// the audit log has the change
	var log struct{ Entries []struct{ Detail string } }
	decode(t, get(h, "/admin/audit"), &log)
	found := false
	for _, e := range log.Entries {
		found = found || strings.Contains(e.Detail, "credits")
	}
	if !found {
		t.Errorf("audit: %s", get(h, "/admin/audit").Body.String())
	}
}

func TestTheCreditsAndTheCountsSurviveARestart(t *testing.T) {
	db, err := persist.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := tokens_db_handle
	tokens_db_handle = db
	t.Cleanup(func() { tokens_db_handle = old; db.Close(); statsForget() })
	l := newMeterLedger()
	l.add(meterEvent{Key: "u:a", Kind: kindAgent, Task: "t", Step: 1, First: true, Credits: 4})
	statsForget()
	statsAdd(func(s *agentStats) { s.Tasks = 7 })
	// a new server: nothing in memory
	again := newMeterLedger()
	if r := again.month("u:a"); r.Credits != 4 || r.Tasks != 1 {
		t.Errorf("the record after a restart: %+v", r)
	}
	statsForget()
	if s := statsSnapshot().Now; s.Tasks != 7 {
		t.Errorf("the counts after a restart: %+v", s)
	}
	// the record sits under its own prefix, so the token totals do not read it as a user's
	if _, ok := tokenTotals.every()["m:u:a"]; ok {
		t.Error("the credit record was read as a token record")
	}
}

func TestAPersonCanHaveAPlanOfTheirOwn(t *testing.T) {
	g := GenieSettings{CreditsOn: true, PlanCredits: 500, PlanBudget: 40, PlanName: "Free"}
	c := meterSetup(t, g)
	_ = c
	if e := entitlementsOf(g, "uid-ann", false); e.CreditsPerMonth != 500 || e.Plan != "Free" {
		t.Fatalf("the default plan: %+v", e)
	}
	meter.setPlan("u:uid-ann", &planRecord{Name: "Team", Credits: 2000, Budget: 100})
	if e := entitlementsOf(g, "uid-ann", false); e.CreditsPerMonth != 2000 || e.BudgetPerTask != 100 || e.Plan != "Team" || !e.Enforce {
		t.Errorf("their own plan: %+v", e)
	}
	if e := entitlementsOf(g, "uid-bob", false); e.CreditsPerMonth != 500 {
		t.Errorf("another person got it: %+v", e)
	}
	// with credits off the plan counts for nothing
	g.CreditsOn = false
	if e := entitlementsOf(g, "uid-ann", false); e.Enforce || e.CreditsPerMonth != 0 {
		t.Errorf("credits off: %+v", e)
	}
	meter.setPlan("u:uid-ann", nil)
	g.CreditsOn = true
	if e := entitlementsOf(g, "uid-ann", false); e.CreditsPerMonth != 500 {
		t.Errorf("default again: %+v", e)
	}
	// the admin sets it through the API
	_, h := adminTestServer(t)
	if w := post(h, "/admin/genie-plan", `{"uid":"uid-ann","name":"Team","credits":2000,"budget":100}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"Team"`) {
		t.Errorf("set: %d %s", w.Code, w.Body.String())
	}
	if w := post(h, "/admin/genie-plan", `{"uid":"uid-ann","credits":-1}`); w.Code != http.StatusBadRequest {
		t.Errorf("a bad plan: %d", w.Code)
	}
	post(h, "/admin/genie-plan", `{"uid":"uid-ann"}`)
	if meter.month("u:uid-ann").Plan != nil {
		t.Error("the plan was not cleared")
	}
}

// ---- carrying on ------------------------------------------------------------------------

func TestATaskCarriesOnPastItsStepsOnlyWhenAsked(t *testing.T) {
	l, now := testLedger()
	run, _ := l.begin("u1", 20, 3)
	for want := 2; want <= 3; want++ {
		if r, err := l.nextWith(run.token, "u1", 3, 0, false); err != nil || r.step != want || r.total != want {
			t.Fatalf("step %d: %+v %v", want, r, err)
		}
	}
	if _, err := l.nextWith(run.token, "u1", 3, 0, false); err != errAgentSteps {
		t.Fatalf("without asking: %v", err)
	}
	// carrying on starts a new stretch: the steps count from 1 again, the task's own count goes on
	*now = now.Add(10 * time.Minute)
	r, err := l.nextWith(run.token, "u1", 3, 0, true)
	if err != nil || r.step != 1 || r.total != 4 || r.max != 3 {
		t.Fatalf("carry on: %+v %v", r, err)
	}
	// the stretch has its own steps and its own time
	*now = now.Add(10 * time.Minute)
	l.nextWith(run.token, "u1", 3, 0, false)
	if r, err := l.nextWith(run.token, "u1", 3, 0, false); err != nil || r.step != 3 {
		t.Fatalf("the third step of the stretch: %+v %v", r, err)
	}
	if _, err := l.nextWith(run.token, "u1", 3, 0, false); err != errAgentSteps {
		t.Fatalf("the stretch is over: %v", err)
	}
	// three carries and no more
	for i := 2; i <= maxAgentCarries; i++ {
		if _, err := l.nextWith(run.token, "u1", 3, 0, true); err != nil {
			t.Fatalf("carry %d: %v", i, err)
		}
		l.nextWith(run.token, "u1", 3, 0, false)
		l.nextWith(run.token, "u1", 3, 0, false)
	}
	if _, err := l.nextWith(run.token, "u1", 3, 0, true); err != errAgentCarries {
		t.Errorf("a fourth carry: %v", err)
	}
	// someone else cannot carry it on
	if _, err := l.nextWith(run.token, "u2", 3, 0, true); err != errAgentInvalid {
		t.Errorf("another user: %v", err)
	}
	// a carry asked for when nothing is over is not used up
	fresh, _ := l.begin("u3", 20, 5)
	l.nextWith(fresh.token, "u3", 5, 0, true)
	if l.tasks[fresh.id].carries != 0 {
		t.Error("a carry was used when the task had room")
	}
}

func TestATaskThatPassesItsBudgetAsksFirstAndCarriesOnForAnotherBudget(t *testing.T) {
	l, _ := testLedger()
	run, _ := l.begin("u1", 20, 8)
	l.addCredits(run.id, 38)
	if r, err := l.nextWith(run.token, "u1", 8, 40, false); err != nil || r.step != 2 {
		t.Fatalf("under the budget: %+v %v", r, err)
	}
	l.addCredits(run.id, 4) // 42 of 40
	_, err := l.nextWith(run.token, "u1", 8, 40, false)
	if err == nil || err.Code != "agent_budget" || err.Status != http.StatusTooManyRequests || !strings.Contains(err.Message, "42 of the 40 credits") {
		t.Fatalf("over the budget: %+v", err)
	}
	if r, err := l.nextWith(run.token, "u1", 8, 40, true); err != nil || r.step != 3 {
		t.Fatalf("carry on: %+v %v", r, err)
	}
	if l.tasks[run.id].extra != 40 {
		t.Errorf("the budget was not raised: %v", l.tasks[run.id].extra)
	}
	// 42 of 80 now: no more asking until it passes that
	if _, err := l.nextWith(run.token, "u1", 8, 40, false); err != nil {
		t.Errorf("within the raised budget: %v", err)
	}
	l.addCredits(run.id, 40) // 82 of 80
	if _, err := l.nextWith(run.token, "u1", 8, 40, false); err == nil || err.Code != "agent_budget" {
		t.Errorf("over the raised budget: %v", err)
	}
	// no budget, no asking
	free, _ := l.begin("u2", 20, 8)
	l.addCredits(free.id, 1e6)
	if _, err := l.nextWith(free.token, "u2", 8, 0, false); err != nil {
		t.Errorf("no budget: %v", err)
	}
}

func TestACarriedOnStepIsGivenBackWhenTheModelCouldNotAnswer(t *testing.T) {
	l, _ := testLedger()
	run, _ := l.begin("u1", 20, 2)
	l.nextWith(run.token, "u1", 2, 0, false)
	r, _ := l.nextWith(run.token, "u1", 2, 0, true)
	l.undo(r)
	if again, err := l.nextWith(run.token, "u1", 2, 0, false); err != nil || again.step != 1 {
		t.Errorf("after the carried-on step was given back: %+v %v", again, err)
	}
}

func TestCarryingOnThroughTheProxyKeepsTheStepsOfEachStretch(t *testing.T) {
	g := GenieSettings{AgentMaxSteps: 2, CreditsOn: true, PlanBudget: 5, PlanCredits: 1000, ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 0.5, Out: 2}}}
	c := meterSetup(t, g)
	w := agentCall(t, agentStep1, c, "")
	token := w.Header().Get(agentTaskHeader)
	// a step is 3 credits: two steps are 6 of the 5, so the third asks
	agentCall(t, agentStepN(token), c, "")
	w = agentCall(t, agentStepN(token), c, "")
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusTooManyRequests || e.Error.Code != "agent_step_limit" && e.Error.Code != "agent_budget" {
		t.Fatalf("the third step: %d %s", w.Code, w.Body.String())
	}
	carry := `{"model":"gpt-4o-mini","context":"agent","agent_task":"` + token + `","agent_carry":true,"messages":[{"role":"user","content":"[user-ab] write hello"}]}`
	w = agentCall(t, carry, c, "")
	if w.Code != 200 || w.Header().Get(agentStepHeader) != "1/2" {
		t.Fatalf("carried on: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if strings.Contains(w.Body.String(), "agent_carry") {
		t.Error("the carry flag went to the model")
	}
	// the record counts the steps of the whole task, none twice
	if r := meter.month("u:uid-ann"); r.Steps != 3 || r.Tasks != 1 {
		t.Errorf("the record: %+v", r)
	}
}
