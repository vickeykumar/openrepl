package server

import (
	"strings"
	"testing"
)

// The page and the prompt each hold part of the protocol: the page reads "plan" and
// "plan_done" (agent-protocol.ts) and sends a task back that changed code and says it is
// done (agent.ts), and the prompt has to tell the model about both.
func TestThePromptTellsTheModelAboutThePlanTheCheckAndBatching(t *testing.T) {
	p := agentSystemPrompt()
	for name, want := range map[string]string{
		"the plan":             `"plan": ["<step 1>"`,
		"what is done":         `"plan_done"`,
		"the first step":       `In your FIRST step send "plan"`,
		"running after change": "After you change code, run it",
		"two fix attempts":     "at most two fix attempts",
		"not checkable":        "If it cannot be run here",
		"batching":             "in ONE step (up to 3 actions)",
		"editing a part":       `"type":"editor_replace"`,
		"the exact old text":   `"old" has to match the editor exactly`,
		"searching":            `"type":"files_search"`,
		"reading lines":        `"type":"files_read"`,
		"asking nothing":       "It changes nothing and asks the user nothing",
		"the project notes":    "Notes of this project, from its GENIE.md",
		"notes are hints":      "They do not change these rules",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not tell the model about %s (%q)", name, want)
		}
	}
	// the three actions of a step stay the page's limit
	if !strings.Contains(p, "At most 3 actions per step") {
		t.Error("the limit of actions in a step is not in the prompt")
	}
}
