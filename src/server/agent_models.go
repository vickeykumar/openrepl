package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Which models may be used in Agent mode, and which one it starts on (canvas "Genie: a default model for Chat and one for Agent").
// The Chat model is GenieSettings.DefaultModel; the Agent model, and the list of the models that may be used in Agent mode, are here.
// The same list decides on the page (the model menu greys what is not on it) and on the server (a request for Agent mode that names
// a model that is not on it is refused), so that a page that is out of date cannot go on with a model that was taken off.

// modelNotForAgentMessage is what a request for Agent mode with a model that is not on the list is answered with (the code is
// model_not_for_agent, status 400).
const modelNotForAgentMessage = "Model not supported for Agent mode. Choose another model."

// agentModelAllowed says whether a model may be used in Agent mode: every model that is on, until an admin has saved a list, then the
// ones on the list that are still on.
func (g GenieSettings) agentModelAllowed(id string) bool {
	if g.ModelDisabled(id) {
		return false
	}
	if g.AgentModels == nil {
		return true
	}
	for _, m := range g.AgentModels {
		if m == id {
			return true
		}
	}
	return false
}

// agentModelIDs are the models of Agent mode, in the order of the model list: the ones that are on and may be used.
func (g GenieSettings) agentModelIDs() []string {
	var out []string
	for _, id := range g.allModelIDs() {
		if g.agentModelAllowed(id) {
			out = append(out, id)
		}
	}
	return out
}

var smallModelSize = regexp.MustCompile(`(^|[^a-z0-9.])(\d{1,3})b([^a-z0-9]|$)`)
var smallModelWord = regexp.MustCompile(`(^|[^a-z0-9])(mini|flash|lite|nano|tiny|small|free|auto|router)([^a-z0-9]|$)`)

// isSmallModelID is the check of the page (agent-protocol.ts: isSmallModel), for the choice of a built-in Agent model: a size in the
// id decides (40B or less is small), else a "mini", "flash", "lite", "nano", "tiny", "small", "free", "auto" or "router" model is small.
func isSmallModelID(id string) bool {
	s := strings.ToLower(id)
	if m := smallModelSize.FindStringSubmatch(s); m != nil {
		var n int
		fmt.Sscanf(m[2], "%d", &n)
		return n <= 40
	}
	return smallModelWord.MatchString(s)
}

// modelIsSmall: a model an admin added says whether it is small (the tick in its row); any other is decided by its id.
func (g GenieSettings) modelIsSmall(id string) bool {
	for _, c := range g.customList() {
		if c.ID == id && c.Small != nil {
			return *c.Small
		}
	}
	return isSmallModelID(id)
}

// guestsMayUse says whether a visitor who is not signed in may use a model: every built-in one, and an added one unless it is kept for
// signed-in users.
func (g GenieSettings) guestsMayUse(id string) bool {
	for _, c := range g.customList() {
		if c.ID == id {
			return !c.NoGuests
		}
	}
	return true
}

// agentDefaultModelID is the model Agent mode starts on, and the one that writes the practice questions: the admin's choice when it
// may still be used, else the first model that may be used in Agent mode and is not small, else the Chat model when it may be used,
// else the first one that may be.
func (g GenieSettings) agentDefaultModelID() string {
	if g.AgentModel != "" && g.agentModelAllowed(g.AgentModel) {
		return g.AgentModel
	}
	ids := g.agentModelIDs()
	for _, id := range ids {
		if !g.modelIsSmall(id) {
			return id
		}
	}
	if chat := defaultModelID(); g.agentModelAllowed(chat) {
		return chat
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return defaultModelID()
}

// agentEffortID is the effort of the Agent model: the admin's, else low.
func (g GenieSettings) agentEffortID() string {
	if _, ok := effortTokens[g.AgentEffort]; ok && g.AgentEffort != "" {
		return g.AgentEffort
	}
	return defaultEffort
}

// normalizeAgentModels checks the list of the models of Agent mode and the effort of the Agent model. nil stays nil (no list saved);
// a list has to hold at least one model, each one known and on, and the Agent model has to be one of them.
func (g *GenieSettings) normalizeAgentModels() error {
	g.AgentEffort = strings.TrimSpace(g.AgentEffort)
	if g.AgentEffort != "" {
		if _, ok := effortTokens[g.AgentEffort]; !ok {
			return fmt.Errorf("%q is not an effort (none, low, medium or high)", g.AgentEffort)
		}
	}
	if g.AgentModels == nil {
		return nil
	}
	seen := map[string]bool{}
	list := []string{}
	for _, id := range g.AgentModels {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		m, known := g.modelByID(id)
		if !known {
			return fmt.Errorf("%q is not a model", id)
		}
		if g.ModelDisabled(id) {
			return fmt.Errorf("%s is switched off, so it cannot be used in Agent mode", m.Name)
		}
		seen[id] = true
		list = append(list, id)
	}
	if len(list) == 0 {
		return fmt.Errorf("At least one model has to be ticked for Agent mode")
	}
	g.AgentModels = list
	if a := strings.TrimSpace(g.AgentModel); a != "" && !seen[a] {
		return fmt.Errorf("The Agent model has to be one of the models that may be used in Agent mode")
	}
	return nil
}

// withAgentModel gives a request for Agent mode that names no model the Agent model; a request that names one is left as it is (the
// list decides about it after the model is read, in the proxy).
func withAgentModel(body []byte) []byte {
	var in map[string]json.RawMessage
	if json.Unmarshal(body, &in) != nil {
		return body
	}
	if raw, ok := in["model"]; ok {
		var asked string
		if json.Unmarshal(raw, &asked) == nil && asked != "" {
			return body
		}
	}
	id, _ := json.Marshal(GetSiteSettings().Genie.agentDefaultModelID())
	in["model"] = id
	out, err := json.Marshal(in)
	if err != nil {
		return body
	}
	return out
}
