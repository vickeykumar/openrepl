package server

import (
	"strings"
	"testing"
)

func TestTheAgentModelIsCheckedAndPublished(t *testing.T) {
	if _, err := (SiteSettings{Genie: GenieSettings{AgentModel: "gpt-9"}}).normalize(); err == nil {
		t.Fatal("an unknown Agent model was accepted")
	}
	if _, err := (SiteSettings{Genie: GenieSettings{AgentModel: modelLuna, DisabledModels: []string{modelLuna}}}).normalize(); err == nil {
		t.Fatal("an Agent model that is switched off was accepted")
	}
	s, err := (SiteSettings{Genie: GenieSettings{AgentModel: " " + modelLuna + " "}}).normalize()
	if err != nil || s.Genie.AgentModel != modelLuna {
		t.Fatalf("a good Agent model: %v %q", err, s.Genie.AgentModel)
	}
	if err := func() error { _, e := (SiteSettings{}).normalize(); return e }(); err != nil {
		t.Fatalf("none is allowed: %v", err)
	}
}

func TestTheAgentPromptHasTheIDEGuide(t *testing.T) {
	p := agentSystemPrompt()
	for _, want := range []string{"HOW OPENREPL WORKS", "you CAN read both", "\"type\":\"run\"", "terminal_type", "SAVE AFTER EVERY EDIT", "files_save"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the agent prompt does not have %q", want)
		}
	}
	if strings.Index(p, "HOW OPENREPL WORKS") > strings.Index(p, "Answer every step") {
		t.Fatal("the guide comes after the format")
	}
}
