package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTheListOfAgentModelsIsChecked(t *testing.T) {
	adminTestServer(t)
	ok := func(g GenieSettings) (SiteSettings, error) { return (SiteSettings{Genie: g}).normalize() }
	// no list: nothing to check, and it stays "no list"
	s, err := ok(GenieSettings{})
	if err != nil || s.Genie.AgentModels != nil {
		t.Fatalf("no list: %v %v", err, s.Genie.AgentModels)
	}
	// a list: known models, at least one, no repeats, the Agent model among them
	s, err = ok(GenieSettings{AgentModels: []string{modelLuna, " " + modelLuna, modelMini}, AgentModel: modelLuna, AgentEffort: "medium"})
	if err != nil || strings.Join(s.Genie.AgentModels, ",") != modelLuna+","+modelMini || s.Genie.AgentEffort != "medium" {
		t.Fatalf("a list: %v %v", err, s.Genie)
	}
	for name, g := range map[string]GenieSettings{
		"an empty list":                    {AgentModels: []string{}},
		"an unknown model":                 {AgentModels: []string{"gpt-9"}},
		"a model that is switched off":     {AgentModels: []string{modelLuna}, DisabledModels: []string{modelLuna}},
		"an Agent model that is not on it": {AgentModels: []string{modelMini}, AgentModel: modelLuna},
		"an effort that is not one":        {AgentEffort: "extreme"},
	} {
		if _, err := ok(g); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestWhichModelsMayBeUsedInAgentMode(t *testing.T) {
	g := GenieSettings{}
	if !g.agentModelAllowed(modelLuna) || !g.agentModelAllowed(modelMini) {
		t.Fatal("with no list every model that is on may be used")
	}
	g = GenieSettings{AgentModels: []string{modelLuna}}
	if !g.agentModelAllowed(modelLuna) || g.agentModelAllowed(modelMini) {
		t.Fatal("with a list only the ticked ones")
	}
	g.DisabledModels = []string{modelLuna}
	if g.agentModelAllowed(modelLuna) {
		t.Fatal("a ticked model that is switched off may not be used")
	}
}

func TestTheModelAgentModeStartsOn(t *testing.T) {
	adminTestServer(t)
	// the admin's choice, while it may be used
	g := GenieSettings{AgentModel: modelMini}
	if got := g.agentDefaultModelID(); got != modelMini {
		t.Fatalf("the admin's: %s", got)
	}
	// not chosen: the first that may be used and is not small (mini is small, Luna is not)
	g = GenieSettings{}
	if got := g.agentDefaultModelID(); got != modelLuna {
		t.Fatalf("built in: %s", got)
	}
	// the chosen one was taken off the list: the built-in choice
	g = GenieSettings{AgentModel: modelMini, AgentModels: []string{modelLuna}}
	if got := g.agentDefaultModelID(); got != modelLuna {
		t.Fatalf("taken off the list: %s", got)
	}
	// only small models may be used: the Chat model if it may, else the first
	g = GenieSettings{AgentModels: []string{modelMini}}
	if got := g.agentDefaultModelID(); got != modelMini {
		t.Fatalf("only a small one: %s", got)
	}
	if (GenieSettings{}).agentEffortID() != "low" || (GenieSettings{AgentEffort: "high"}).agentEffortID() != "high" {
		t.Fatal("the effort")
	}
}

// the same table as agent-protocol.ts (isSmallModel): the page and the server agree
func TestTheSmallModelCheckAgreesWithThePage(t *testing.T) {
	for _, id := range []string{"@cf/qwen/qwen3-30b-a3b-fp8", "@cf/google/gemma-4-26b-a4b-it", "gpt-4o-mini", "@cf/zai-org/glm-4.7-flash", "google/gemma-4-31b-it",
		"openrouter/free", "openrouter/auto", "some/free-router", "cohere/north-mini-code:free"} {
		if !isSmallModelID(id) {
			t.Errorf("%s is small", id)
		}
	}
	for _, id := range []string{"gpt-6-luna", "anthropic/claude-sonnet", "meta/llama-3.3-70b-instruct", "", "nvidia/nemotron-3-super-120b-a12b:free", "meta/llama-3.3-70b-instruct:free"} {
		if isSmallModelID(id) {
			t.Errorf("%s is not small", id)
		}
	}
}

func TestThePageIsToldTheAgentModelsAndTheList(t *testing.T) {
	adminTestServer(t)
	p := (SiteSettings{Genie: GenieSettings{AgentModels: []string{modelLuna}, AgentEffort: "high"}}).public()
	if p.AgentModel != modelLuna || len(p.AgentModels) != 1 || p.AgentEffort != "high" {
		t.Fatalf("public: %+v %+v %q", p.AgentModel, p.AgentModels, p.AgentEffort)
	}
	b, _ := json.Marshal((SiteSettings{}).public())
	if !strings.Contains(string(b), `"genieAgentModels":null`) || !strings.Contains(string(b), `"genieAgentEffort":"low"`) {
		t.Fatalf("the settings the page reads: %s", b)
	}
	joined := strings.Join(settingsChanges(SiteSettings{}, SiteSettings{Genie: GenieSettings{AgentModels: []string{modelLuna}, AgentModel: modelLuna, AgentEffort: "low"}}), "|")
	if !strings.Contains(joined, "models for Agent mode: "+modelLuna) || !strings.Contains(joined, "Agent model: "+modelLuna) {
		t.Fatalf("the audit log: %s", joined)
	}
}

func TestAnAgentStepWithAModelThatIsNotAllowedIsRefusedAndNotCounted(t *testing.T) {
	c, upstream := agentSetup(t, GenieSettings{AgentModels: []string{modelLuna}, AgentModel: modelLuna})
	const step = `{"model":"gpt-4o-mini","context":"agent","agent_task":"","messages":[{"role":"user","content":"[user-ab] write hello"}]}`
	w := agentCall(t, step, c, "")
	var e ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusBadRequest || e.Error.Code != "model_not_for_agent" || !strings.Contains(e.Error.Message, "Model not supported for Agent mode") {
		t.Fatalf("a model that is not ticked: %d %s", w.Code, w.Body.String())
	}
	if *upstream != "" {
		t.Fatal("the request went on to the model")
	}
	// the step was given back: the same task can begin again, with a model that is allowed
	ok := strings.Replace(step, "gpt-4o-mini", "gpt-6-luna", 1)
	if w := agentCall(t, ok, c, ""); w.Code != 200 {
		t.Fatalf("a model that is ticked: %d %s", w.Code, w.Body.String())
	}
	// Chat is not affected by the list
	if code, body := chatCall(t, modelMini); code != 200 {
		t.Fatalf("chat with a model that is not on the list: %d %s", code, body)
	}
}

func TestAnAgentStepThatNamesNoModelUsesTheAgentModel(t *testing.T) {
	c, upstream := agentSetup(t, GenieSettings{AgentModel: modelMini, DefaultModel: modelLuna})
	const step = `{"context":"agent","agent_task":"","messages":[{"role":"user","content":"[user-ab] write hello"}]}`
	if w := agentCall(t, step, c, ""); w.Code != 200 {
		t.Fatalf("no model: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(*upstream, `"model":"`+modelMini+`"`) {
		t.Fatalf("the Agent model was not used: %s", *upstream)
	}
}

func TestAnAddedModelSaysIfItIsSmallAndIfGuestsMayUseIt(t *testing.T) {
	yes, no := true, false
	g := GenieSettings{CustomModels: []CustomModel{
		{ID: "acme/big-one", Name: "Big", Enabled: true, Small: &yes, NoGuests: true},
		{ID: "acme/tiny-70b", Name: "Tiny", Enabled: true, Small: &no},
		{ID: "acme/plain-7b", Name: "Plain", Enabled: true},
	}}
	if !g.modelIsSmall("acme/big-one") || g.modelIsSmall("acme/tiny-70b") || !g.modelIsSmall("acme/plain-7b") {
		t.Fatal("the tick decides; with no tick the id does")
	}
	if g.guestsMayUse("acme/big-one") || !g.guestsMayUse("acme/plain-7b") || !g.guestsMayUse(modelLuna) {
		t.Fatal("only a model kept for signed-in users is closed to a guest")
	}
	var pub []publicCustomModel = g.publicCustomModels()
	if len(pub) != 3 || !pub[0].Small || pub[0].Guests || pub[1].Small || !pub[1].Guests {
		t.Fatalf("what the page is told: %+v", pub)
	}
	h := g
	h.CustomModels = []CustomModel{yesCopy(g.CustomModels[0], false), g.CustomModels[1], g.CustomModels[2]}
	if len(customModelChanges(g, h)) != 1 {
		t.Fatalf("a change of the guest tick is logged once: %v", customModelChanges(g, h))
	}
	if len(customModelChanges(g, g)) != 0 {
		t.Fatal("nothing changed")
	}
}

func yesCopy(c CustomModel, noGuests bool) CustomModel {
	v := *c.Small
	c.Small = &v
	c.NoGuests = noGuests
	return c
}

func TestABuiltInModelCanBeKeptForSignedInUsers(t *testing.T) {
	adminTestServer(t)
	g := GenieSettings{GuestBlockedModels: []string{modelLuna}}
	if g.guestsMayUse(modelLuna) || !g.guestsMayUse("gpt-4o-mini") {
		t.Fatal("only the ticked-off built-in model is closed to a guest")
	}
	if p := (SiteSettings{Genie: g}).public(); len(p.GuestBlocked) != 1 || p.GuestBlocked[0] != modelLuna {
		t.Fatalf("the page is told: %v", p.GuestBlocked)
	}
	s, err := (SiteSettings{Genie: GenieSettings{GuestBlockedModels: []string{"nope"}}}).normalize()
	_ = s
	if err == nil {
		t.Fatal("an unknown model is refused")
	}
}
