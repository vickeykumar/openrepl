package server

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

// What the chat proxy lets through to a model. The Genie panel and the New
// question dialog offer three models and four effort levels; the server holds
// the same lists so that a request edited in the browser cannot ask for an
// expensive model, a long answer, a host of its own choosing or a field the
// service does not use.

const (
	providerOpenAI     = "openai"
	providerOpenRouter = "openrouter"

	modelLuna  = "gpt-6-luna"
	modelMini  = "gpt-4o-mini"
	modelGemma = "google/gemma-4-31b-it"

	// a request that names any other model (or none) is answered by this one,
	// unless an admin chose another default (defaultModelID)
	defaultChatModel = modelMini

	defaultEffort = "low"

	// Genie asks for up to 4,000; a generated question (its description and a
	// template for each language) needs more room, so the caps leave it.
	maxCompletionTokensCap = 7000 // Luna: the thinking counts against this
	minCompletionTokens    = 100
	defaultMaxTokens       = 800
	maxChatBodyBytes       = 1 << 20 // the history and the editor's code
)

// chatModel is one model the proxy will call. Reasoning models (Luna) take a
// reasoning_effort and max_completion_tokens; the others take a temperature and
// max_tokens, which is capped at MaxTokensCap.
type chatModel struct {
	ID           string
	Name         string // for messages to the visitor
	Provider     string
	Reasoning    bool
	MaxTokensCap int
}

// the order the models are listed in, in the dashboard and as a last resort
var chatModelOrder = []string{modelLuna, modelMini, modelGemma}

var chatModels = map[string]chatModel{
	modelLuna:  {ID: modelLuna, Name: "GPT-6 Luna", Provider: providerOpenAI, Reasoning: true},
	modelMini:  {ID: modelMini, Name: "GPT-4o mini", Provider: providerOpenAI, MaxTokensCap: 3000},
	modelGemma: {ID: modelGemma, Name: "Gemma 4 31B", Provider: providerOpenRouter, MaxTokensCap: 4000},
}

// defaultModelID is the model that answers a request naming none (or one that
// is not on the list): the admin's choice, else GPT-4o mini, else the first
// model that is switched on. Older callers such as the blog editor name no
// model, so they keep working as long as any model is on.
func defaultModelID() string {
	g := GetSiteSettings().Genie
	if g.DefaultModel != "" && !g.ModelDisabled(g.DefaultModel) {
		return g.DefaultModel
	}
	for _, id := range append([]string{defaultChatModel}, chatModelOrder...) {
		if !g.ModelDisabled(id) {
			return id
		}
	}
	return defaultChatModel
}

// The only hosts OpenRouter may use for a Gemma request, in this order, and no
// others: when neither can answer, the request fails and the visitor is told.
// The browser cannot change this; sanitizeChatBody sets it. The names are
// OpenRouter's provider slugs (model-choice.js does not know them).
var openRouterHosts = []string{"modelrun/fp4", "coreweave/fp4"}

// openRouterHostNames are the hosts an admin may choose from (and in which
// order), by the name the dashboard shows; the slugs are OpenRouter's.
var openRouterHostNames = map[string]string{"modelrun/fp4": "ModelRun", "coreweave/fp4": "CoreWeave"}

// activeOpenRouterHosts is the admin's choice and order of hosts, else the
// built-in two.
func activeOpenRouterHosts() []string {
	if h := GetSiteSettings().Genie.OpenRouterHosts; len(h) > 0 {
		return h
	}
	return openRouterHosts
}

func openRouterRouting() map[string]interface{} {
	hosts := activeOpenRouterHosts()
	return map[string]interface{}{
		"order":           hosts,
		"only":            hosts,
		"allow_fallbacks": false,
	}
}

// The numbers an admin can change (settings.go, genie): the answer size of each
// model, how long OpenRouter may take, and what Genie is told about the page.
// 0 in the settings means the built-in value below.
const (
	defaultOpenRouterTimeoutSec = 60
	defaultContextEditorChars   = 12000
	defaultContextTerminalChars = 4000
	defaultContextTerminalLines = 20
	defaultHistoryMessages      = 20
)

// answerCap is the most tokens an answer of the model may have: the admin's
// number for it, else the built-in cap.
func answerCap(model chatModel) int {
	if n := GetSiteSettings().Genie.AnswerCaps[model.ID]; n > 0 {
		return n
	}
	if model.Reasoning {
		return maxCompletionTokensCap
	}
	return model.MaxTokensCap
}

func openRouterTimeoutSetting() time.Duration {
	if n := GetSiteSettings().Genie.OpenRouterTimeoutSec; n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultOpenRouterTimeoutSec * time.Second
}

// effort level -> the answer budget used when the request names none
var effortTokens = map[string]int{
	"none":   1000,
	"low":    2000,
	"medium": 3000,
	"high":   4000,
}

// the only fields that reach a model ("provider" is added by the server)
var chatFields = map[string]bool{
	"model":                 true,
	"messages":              true,
	"stream":                true,
	"temperature":           true,
	"max_tokens":            true,
	"max_completion_tokens": true,
	"reasoning_effort":      true,
	"response_format":       true, // only {"type":"json_object"}, see below
}

var errBadChatBody = errors.New("The request is not a chat request.")

// sanitizeChatBody returns the request body to send on, and the model it is
// for: only the fields above, a model from the short list, and for each model
// only the settings it accepts. Numbers are clamped to the caps. For an
// OpenRouter model the body also carries the host rule above.
func sanitizeChatBody(body []byte) ([]byte, chatModel, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, chatModel{}, errBadChatBody
	}
	messages, ok := in["messages"]
	if !ok || len(messages) == 0 || messages[0] != '[' {
		return nil, chatModel{}, errBadChatBody
	}

	out := map[string]interface{}{"messages": json.RawMessage(messages)}
	if raw, ok := in["stream"]; ok {
		var stream bool
		if json.Unmarshal(raw, &stream) == nil && stream {
			out["stream"] = true
		}
	}

	// The question generator asks for JSON so that a quote or a newline inside
	// the text cannot make the reply unparseable. No other format is passed on.
	if raw, ok := in["response_format"]; ok {
		var format struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &format) == nil && format.Type == "json_object" {
			out["response_format"] = map[string]string{"type": "json_object"}
		}
	}

	model := chatModels[defaultModelID()]
	if raw, ok := in["model"]; ok {
		var asked string
		if json.Unmarshal(raw, &asked) == nil {
			if known, ok := chatModels[asked]; ok {
				model = known
			}
		}
	}
	out["model"] = model.ID

	if model.Reasoning {
		effort := defaultEffort
		if raw, ok := in["reasoning_effort"]; ok {
			var asked string
			if json.Unmarshal(raw, &asked) == nil {
				if _, known := effortTokens[asked]; known {
					effort = asked
				}
			}
		}
		out["reasoning_effort"] = effort
		out["max_completion_tokens"] = clampTokens(in["max_completion_tokens"], effortTokens[effort], minCompletionTokens, answerCap(model))
	} else {
		out["max_tokens"] = clampTokens(in["max_tokens"], defaultMaxTokens, minCompletionTokens, answerCap(model))
		if raw, ok := in["temperature"]; ok {
			var t float64
			if json.Unmarshal(raw, &t) == nil && !math.IsNaN(t) {
				out["temperature"] = math.Min(2, math.Max(0, t))
			}
		}
	}
	if model.Provider == providerOpenRouter {
		out["provider"] = openRouterRouting()
	}
	sanitized, err := json.Marshal(out)
	return sanitized, model, err
}

// clampTokens reads a token count from raw, falling back to def, and keeps it
// between lo and hi.
func clampTokens(raw json.RawMessage, def, lo, hi int) int {
	n := def
	var asked float64
	if len(raw) > 0 && json.Unmarshal(raw, &asked) == nil && !math.IsNaN(asked) && !math.IsInf(asked, 0) {
		n = int(asked)
	}
	if n < lo {
		n = lo
	}
	if n > hi {
		n = hi
	}
	return n
}
