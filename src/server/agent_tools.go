package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Native tool calling for the agent, behind an admin switch (GenieSettings.AgentNativeTools). The agent normally
// answers with one JSON object in plain text ({"say", "plan", "actions", "done"}) that the page reads
// (resources/chat-widget/src/agent-protocol.ts). With the switch on, a model whose host takes forced tool calls
// is given ONE function, "step", whose parameters are that same object, and is told to call it: the host then
// checks that the arguments are JSON of that shape, which takes away the slips of a model writing JSON as
// text. The page reads the arguments of the call exactly as it reads the text, so nothing else changes.
// Only OpenAI models get it: the other hosts do not all take a forced tool call.

// nativeToolModels are the models that are given the tool: the ones an admin ticked, or, until one was saved, the OpenAI models.
func (g GenieSettings) nativeToolModels() []string {
	if g.NativeToolModels != nil {
		return g.NativeToolModels
	}
	var out []string
	for _, id := range g.allModelIDs() {
		if m, ok := modelByID(id); ok && m.Provider == providerOpenAI {
			out = append(out, id)
		}
	}
	return out
}

// normalizeNativeTools checks the list of models: each is a model of the site, none twice. nil stays nil ("never chosen").
func (g *GenieSettings) normalizeNativeTools() error {
	if g.NativeToolModels == nil {
		return nil
	}
	known := map[string]bool{}
	for _, id := range g.allModelIDs() {
		known[id] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, id := range g.NativeToolModels {
		if !known[id] {
			return fmt.Errorf("%q is not a model", id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	g.NativeToolModels = out
	return nil
}

// nativeToolFor says whether a model is given the tool: the admin's switch is on, the model is one of those that take it,
// and its host has not refused it lately (a host that refused a call with the tool is not asked again for an hour).
func nativeToolFor(g GenieSettings, m chatModel) bool {
	if !g.AgentNativeTools || toolMemory.refused(m.ID, time.Now()) {
		return false
	}
	for _, id := range g.nativeToolModels() {
		if id == m.ID {
			return true
		}
	}
	return false
}

// ---- when a host does not take the tool ---------------------------------------------------------

// toolRefusedFor is how long a model that refused a call with the tool is left without it.
const toolRefusedFor = time.Hour

// toolMemory is the models whose hosts refused the tool, with the time until which they are not given it. In memory only:
// a restart asks again, which costs one more refused call at the most.
var toolMemory = &refusedTools{until: map[string]time.Time{}}

type refusedTools struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (r *refusedTools) refused(model string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.until[model]
	if ok && now.After(t) {
		delete(r.until, model)
		return false
	}
	return ok
}

func (r *refusedTools) remember(model string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.until[model] = now.Add(toolRefusedFor)
}

func (r *refusedTools) forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.until = map[string]time.Time{}
}

// toolWasRefused says whether an answer of a host to a call with the tool is a refusal of the tool: a 400 or 422, or an
// error whose text is about tools. The body is what was read of it.
func toolWasRefused(status int, body []byte) bool {
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
		return true
	}
	if status < 400 {
		return false
	}
	text := strings.ToLower(string(body))
	for _, w := range []string{"tool_choice", "tools", "tool use", "tool_use", "function calling", "tool_calls", "function_call"} {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// stepTool is the function the model calls for a step.
func stepTool() map[string]interface{} {
	str := map[string]string{"type": "string"}
	action := map[string]interface{}{
		"type":        "object",
		"description": "One action. Which other fields it has depends on its type, as the instructions say.",
		"properties": map[string]interface{}{
			"type":            map[string]interface{}{"type": "string", "enum": agentActions},
			"text":            str,
			"command":         str,
			"path":            str,
			"to":              str,
			"name":            str,
			"language":        str,
			"old":             str,
			"new":             str,
			"query":           str,
			"kind":            map[string]interface{}{"type": "string", "enum": []string{"file", "folder"}},
			"tab":             map[string]string{"type": "integer"},
			"port":            map[string]string{"type": "integer"},
			"start_line":      map[string]string{"type": "integer"},
			"end_line":        map[string]string{"type": "integer"},
			"timeout_seconds": map[string]string{"type": "integer"},
			"wait_seconds":    map[string]string{"type": "integer"},
			"ref":             str,
			"key":             str,
			"value":           str,
			"area":            str,
			"op":              str,
			"size":            str,
			"checked":         map[string]string{"type": "boolean"},
			"more":            map[string]string{"type": "boolean"},
			"viewport":        map[string]string{"type": "boolean"},
			"enter":           map[string]string{"type": "boolean"},
			"append":          map[string]string{"type": "boolean"},
			"gone":            map[string]string{"type": "boolean"},
		},
		"required": []string{"type"},
	}
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "step",
			"description": "Your answer for this step of the task: what to tell the user, the plan, the actions to carry out and whether the task is done.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"say":       str,
					"plan":      map[string]interface{}{"type": "array", "items": str},
					"plan_done": map[string]interface{}{"type": "array", "items": map[string]string{"type": "integer"}},
					"actions":   map[string]interface{}{"type": "array", "items": action},
					"done":      map[string]string{"type": "boolean"},
				},
				"required": []string{"say", "actions", "done"},
			},
		},
	}
}

// withStepTool is the body of a request for a step, with the tool in it and the call forced. A body it cannot read is not changed.
func withStepTool(body []byte) []byte {
	var in map[string]json.RawMessage
	if json.Unmarshal(body, &in) != nil {
		return body
	}
	tools, _ := json.Marshal([]interface{}{stepTool()})
	choice, _ := json.Marshal(map[string]interface{}{"type": "function", "function": map[string]string{"name": "step"}})
	in["tools"], in["tool_choice"] = tools, choice
	delete(in, "response_format") // the arguments of the call are the JSON
	out, err := json.Marshal(in)
	if err != nil {
		return body
	}
	return out
}
