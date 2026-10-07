package server

import (
	"encoding/json"
	"errors"
	"math"
)

// What the chat proxy lets through to OpenAI. The Genie panel offers two
// models and four effort levels; the server holds the same lists so that a
// request edited in the browser cannot ask for an expensive model, a long
// answer or a field the service does not use.

const (
	modelLuna = "gpt-6-luna"
	modelMini = "gpt-4o-mini"

	// a request that names any other model (or none) is answered by this one
	defaultChatModel = modelMini

	defaultEffort = "low"

	// Genie asks for up to 4,000; a generated question (its description and a
	// template for each language) needs more room, so the caps leave it.
	maxCompletionTokensCap = 7000 // Luna: the thinking counts against this
	minCompletionTokens    = 100
	maxTokensCap           = 3000 // 4o mini
	defaultMaxTokens       = 800
	maxChatBodyBytes       = 1 << 20 // the history and the editor's code
)

// effort level -> the answer budget used when the request names none
var effortTokens = map[string]int{
	"none":   1000,
	"low":    2000,
	"medium": 3000,
	"high":   4000,
}

// the only fields that reach OpenAI
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

// sanitizeChatBody returns the request body to send to OpenAI: only the fields
// above, a model from the short list, and for each model only the settings it
// accepts (Luna takes reasoning_effort and max_completion_tokens; 4o mini takes
// temperature and max_tokens). Numbers are clamped to the caps.
func sanitizeChatBody(body []byte) ([]byte, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, errBadChatBody
	}
	messages, ok := in["messages"]
	if !ok || len(messages) == 0 || messages[0] != '[' {
		return nil, errBadChatBody
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

	model := defaultChatModel
	if raw, ok := in["model"]; ok {
		var asked string
		if json.Unmarshal(raw, &asked) == nil && (asked == modelLuna || asked == modelMini) {
			model = asked
		}
	}
	out["model"] = model

	if model == modelLuna {
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
		out["max_completion_tokens"] = clampTokens(in["max_completion_tokens"], effortTokens[effort], minCompletionTokens, maxCompletionTokensCap)
	} else {
		out["max_tokens"] = clampTokens(in["max_tokens"], defaultMaxTokens, minCompletionTokens, maxTokensCap)
		if raw, ok := in["temperature"]; ok {
			var t float64
			if json.Unmarshal(raw, &t) == nil && !math.IsNaN(t) {
				out["temperature"] = math.Min(2, math.Max(0, t))
			}
		}
	}
	return json.Marshal(out)
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
