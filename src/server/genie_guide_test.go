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
	for _, want := range []string{"HOW OPENREPL WORKS", "you CAN read both", "\"type\":\"run\"", "terminal_type", "SAVE AFTER EVERY EDIT", "EDITING WITH editor_replace", "THE PREVIEW is the page", "preview_read", "WEB PAGES:", "Workspace now", "WHEN UNSURE", "files_save"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the agent prompt does not have %q", want)
		}
	}
	if strings.Index(p, "HOW OPENREPL WORKS") > strings.Index(p, "Answer every step") {
		t.Fatal("the guide comes after the format")
	}
}

func TestTheSummaryLimitIsCheckedAndPublished(t *testing.T) {
	for _, n := range []int{999, 40001, -5} {
		if _, err := (SiteSettings{Genie: GenieSettings{SummaryTokens: n}}).normalize(); err == nil {
			t.Fatalf("%d tokens were accepted", n)
		}
	}
	if (GenieSettings{}).summaryTokens() != defaultSummaryTokens {
		t.Fatal("the built-in limit")
	}
	if (GenieSettings{SummaryTokens: 9000}).summaryTokens() != 9000 {
		t.Fatal("the admin's limit")
	}
	if (GenieSettings{SummaryTokens: 9000, SummaryOff: true}).summaryTokens() != 0 {
		t.Fatal("off is 0 for the page")
	}
}

func TestASummaryLineIsKept(t *testing.T) {
	if _, ok := cleanChatLine(map[string]interface{}{"t": "summary", "text": "The user builds a shop site.", "steps": float64(4)}); !ok {
		t.Fatal("a summary line was refused")
	}
}
