package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTheToolGoesToTheModelsThatTakeItAndOpenAIOnesAreTheDefault(t *testing.T) {
	toolMemory.forget()
	openai, _ := modelByID("gpt-4o-mini")
	gemma, _ := modelByID("google/gemma-4-31b-it")
	g := GenieSettings{AgentNativeTools: true}
	if !nativeToolFor(g, openai) || nativeToolFor(g, gemma) {
		t.Errorf("the default: openai %v, gemma %v", nativeToolFor(g, openai), nativeToolFor(g, gemma))
	}
	// the admin's choice replaces the default
	g.NativeToolModels = []string{"google/gemma-4-31b-it"}
	if nativeToolFor(g, openai) || !nativeToolFor(g, gemma) {
		t.Errorf("a chosen list: openai %v, gemma %v", nativeToolFor(g, openai), nativeToolFor(g, gemma))
	}
	// an empty list is a choice too: no model
	g.NativeToolModels = []string{}
	if nativeToolFor(g, openai) || nativeToolFor(g, gemma) {
		t.Error("an empty list gave the tool to a model")
	}
	// the master switch comes first
	g.NativeToolModels = []string{"gpt-4o-mini"}
	g.AgentNativeTools = false
	if nativeToolFor(g, openai) {
		t.Error("the tool was given with the switch off")
	}
}

func TestAHostThatRefusedTheToolIsLeftWithoutItForAnHour(t *testing.T) {
	toolMemory.forget()
	t.Cleanup(toolMemory.forget)
	m, _ := modelByID("gpt-4o-mini")
	g := GenieSettings{AgentNativeTools: true}
	now := time.Now()
	toolMemory.remember(m.ID, now)
	if nativeToolFor(g, m) {
		t.Error("a model that refused was given the tool again")
	}
	if !toolMemory.refused(m.ID, now.Add(59*time.Minute)) || toolMemory.refused(m.ID, now.Add(61*time.Minute)) {
		t.Error("the memory is an hour")
	}
	other, _ := modelByID("google/gemma-4-31b-it")
	if toolMemory.refused(other.ID, now) {
		t.Error("another model was remembered")
	}
}

func TestWhatCountsAsARefusalOfTheTool(t *testing.T) {
	for status, body := range map[int]string{400: "anything", 422: "{}"} {
		if !toolWasRefused(status, []byte(body)) {
			t.Errorf("%d was not taken as a refusal", status)
		}
	}
	if !toolWasRefused(404, []byte(`{"error":{"message":"No endpoints found that support tool use"}}`)) || !toolWasRefused(500, []byte("tool_choice is not supported")) {
		t.Error("an error about tools")
	}
	for _, c := range []struct {
		status int
		body   string
	}{{200, "tools"}, {429, "slow down"}, {500, "boom"}, {401, "bad key"}} {
		if toolWasRefused(c.status, []byte(c.body)) {
			t.Errorf("%d %q was taken as a refusal", c.status, c.body)
		}
	}
}

func TestTheModelListOfTheToolIsCheckedSavedAndShown(t *testing.T) {
	_, h := adminTestServer(t)
	body := `{"colorOfTheDay":false,"announcement":{"text":"","level":"info"},"maintenance":{"enabled":false,"message":""},"disabledLanguages":[],"genie":{"disabled":false,"agentNativeTools":true,"nativeToolModels":["gpt-4o-mini","google/gemma-4-31b-it","gpt-4o-mini"]}}`
	if w := post(h, "/admin/settings", body); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	g := GetSiteSettings().Genie
	if strings.Join(g.NativeToolModels, ",") != "gpt-4o-mini,google/gemma-4-31b-it" {
		t.Errorf("saved: %v", g.NativeToolModels)
	}
	var got settingsReply
	decode(t, get(h, "/admin/settings"), &got)
	if len(got.Genie.NativeToolModels) != 2 {
		t.Errorf("shown: %v", got.Genie.NativeToolModels)
	}
	if w := post(h, "/admin/settings", strings.Replace(body, `"gpt-4o-mini","google`, `"not-a-model","google`, 1)); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown model: %d", w.Code)
	}
	// an empty list is kept as a choice, and no list stays the default
	if w := post(h, "/admin/settings", strings.Replace(body, `["gpt-4o-mini","google/gemma-4-31b-it","gpt-4o-mini"]`, `[]`, 1)); w.Code != 200 || GetSiteSettings().Genie.NativeToolModels == nil {
		t.Errorf("an empty list: %d %v", w.Code, GetSiteSettings().Genie.NativeToolModels)
	}
	if w := post(h, "/admin/settings", strings.Replace(body, `,"nativeToolModels":["gpt-4o-mini","google/gemma-4-31b-it","gpt-4o-mini"]`, ``, 1)); w.Code != 200 || GetSiteSettings().Genie.NativeToolModels != nil {
		t.Errorf("no list: %d %v", w.Code, GetSiteSettings().Genie.NativeToolModels)
	}
}

// an upstream that refuses a call with tools, and answers one without
type toolUpstream struct {
	mu     sync.Mutex
	bodies []string
}

func (u *toolUpstream) serve(t *testing.T, refuseStatus int, refuseBody string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.bodies = append(u.bodies, string(b))
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(b), "tool_choice") {
			w.WriteHeader(refuseStatus)
			io.WriteString(w, refuseBody)
			return
		}
		io.WriteString(w, answerWithUsage)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestARefusedToolIsSentAgainAsTextWithoutAnErrorAStepAChargeOrAnEvent(t *testing.T) {
	toolMemory.forget()
	t.Cleanup(toolMemory.forget)
	c := meterSetup(t, GenieSettings{AgentNativeTools: true, ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 0.5, Out: 2}}})
	statsForget()
	t.Cleanup(statsForget)
	up := &toolUpstream{}
	old := openaiEndpoint
	openaiEndpoint = up.serve(t, 400, `{"error":{"message":"tools are not supported by this model"}}`)
	t.Cleanup(func() { openaiEndpoint = old })

	w := agentCall(t, agentStep1, c, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"choices"`) {
		t.Fatalf("the user saw: %d %s", w.Code, w.Body.String())
	}
	token := w.Header().Get(agentTaskHeader)
	if len(up.bodies) != 2 || !strings.Contains(up.bodies[0], "tool_choice") || strings.Contains(up.bodies[1], "tool_choice") || strings.Contains(up.bodies[1], `"tools"`) {
		t.Fatalf("the two calls: %d %v", len(up.bodies), up.bodies)
	}
	if !strings.Contains(up.bodies[1], `"response_format"`) {
		t.Error("the text request has no JSON mode")
	}
	// one step, one event, charged once: 3 credits for the answer that came
	if r := meter.month("u:uid-ann"); r.Steps != 1 || r.Credits != 3 || r.Tasks != 1 {
		t.Errorf("the record: %+v", r)
	}
	if used, _ := agents.started("uid-ann"); used != 1 {
		t.Errorf("the tasks started: %d", used)
	}
	if st := statsSnapshot().Now; st.ToolTries != 1 || st.ToolFallbacks != 1 || st.ToolRefused["gpt-4o-mini"] != 1 || st.Steps != 1 || st.Tasks != 1 {
		t.Errorf("the counts: %+v", st)
	}
	// the next step does not try the tool again, and the model is remembered for an hour
	if w := agentCall(t, agentStepN(token), c, ""); w.Code != 200 {
		t.Fatalf("the second step: %d", w.Code)
	}
	if len(up.bodies) != 3 || strings.Contains(up.bodies[2], "tool_choice") {
		t.Errorf("the second step asked with the tool again: %d", len(up.bodies))
	}
	if st := statsSnapshot().Now; st.ToolTries != 1 {
		t.Errorf("a try that was skipped was counted: %+v", st)
	}
	if r := meter.month("u:uid-ann"); r.Steps != 2 || r.Credits != 6 {
		t.Errorf("after two steps: %+v", r)
	}
}

func TestARefusalThatIsOnlyAnErrorTextIsTakenToo(t *testing.T) {
	toolMemory.forget()
	t.Cleanup(toolMemory.forget)
	c := meterSetup(t, GenieSettings{AgentNativeTools: true})
	up := &toolUpstream{}
	old := openaiEndpoint
	openaiEndpoint = up.serve(t, 404, `{"error":{"message":"No endpoints found that support tool use"}}`)
	t.Cleanup(func() { openaiEndpoint = old })
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 || len(up.bodies) != 2 {
		t.Errorf("a 404 about tools: %d, %d calls", w.Code, len(up.bodies))
	}
}

func TestAnErrorThatIsNotAboutToolsIsShownAndNotRetried(t *testing.T) {
	toolMemory.forget()
	t.Cleanup(toolMemory.forget)
	c := meterSetup(t, GenieSettings{AgentNativeTools: true})
	up := &toolUpstream{}
	old := openaiEndpoint
	openaiEndpoint = up.serve(t, 429, `{"error":{"message":"rate limited"}}`)
	t.Cleanup(func() { openaiEndpoint = old })
	w := agentCall(t, agentStep1, c, "")
	if w.Code != 429 || len(up.bodies) != 1 {
		t.Errorf("a rate limit: %d, %d calls", w.Code, len(up.bodies))
	}
	if toolMemory.refused("gpt-4o-mini", time.Now()) {
		t.Error("a rate limit was remembered as a refusal of the tool")
	}
	// the task was given back, as for any step the model could not answer
	if used, _ := agents.started("uid-ann"); used != 0 {
		t.Errorf("the task was kept: %d", used)
	}
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
}

func TestAModelOfAnotherHostGetsTheToolWhenTheAdminTickedIt(t *testing.T) {
	toolMemory.forget()
	t.Cleanup(toolMemory.forget)
	g := GenieSettings{AgentNativeTools: true, NativeToolModels: []string{"gpt-4o-mini"}}
	c := meterSetup(t, g)
	_, lastBody := fakeStep(t)
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 || !strings.Contains(*lastBody, "tool_choice") {
		t.Errorf("a ticked model: %d %.100s", w.Code, *lastBody)
	}
	g.NativeToolModels = []string{}
	SaveSiteSettings(SiteSettings{Genie: g})
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 || strings.Contains(*lastBody, "tool_choice") {
		t.Errorf("a model that is not ticked: %d %.100s", w.Code, *lastBody)
	}
}

// an upstream that remembers what it was last sent
func fakeStep(t *testing.T) (string, *string) {
	t.Helper()
	url, _, body := fakeUpstream(t, 200, answerWithUsage)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	return url, body
}
