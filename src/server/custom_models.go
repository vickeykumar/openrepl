package server

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Models an admin adds in the dashboard (Settings, Genie, "OpenRouter models").
//
// The three built-in models (GPT-6 Luna, GPT-4o mini, Gemma 4 31B) are fixed:
// an admin can switch them off, not remove them. Any other model of OpenRouter
// is added to this list by its OpenRouter id, with a switch, and removed again,
// without a change of code. Visitors can only pick what is in the list and on:
// the server sends on nothing else, whatever the browser asks for.

// the two models of defaultCustomModels
const (
	modelNemotronFree = "nvidia/nemotron-3-super-120b-a12b:free"
	modelNorthFree    = "cohere/north-mini-code:free"
)

const (
	maxCustomModels        = 20
	maxCustomNameChars     = 40
	maxCustomHosts         = 4
	defaultCustomAnswer    = 4000  // the answer cap of a model that has none of its own
	maxCustomThinkingRoom  = 16000 // extra tokens for a model that thinks first
	customModelIDMaxLength = 160
)

// the id OpenRouter gives a model: author/model, optionally with a variant
// (":free"). Lower case, so that two spellings are not two models.
var customModelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}/[a-z0-9][a-z0-9._:+-]{0,95}$`)

// an OpenRouter provider slug, as in "nvidia" or "modelrun/fp4"
var hostSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,39}$`)

// CustomModel is an OpenRouter model an admin added.
type CustomModel struct {
	// ID is OpenRouter's id of the model, for example "nvidia/nemotron-3-super-120b-a12b:free".
	ID string `json:"id"`
	// Name is what visitors see.
	Name string `json:"name"`
	// Enabled is the switch: a model that is off is not offered and the proxy
	// refuses it.
	Enabled bool `json:"enabled"`
	// Free marks a model on a provider's free tier. It costs nothing, but its
	// provider may keep and use what is sent, and its limits are shared; visitors
	// are told so. An id that ends in ":free" is always marked.
	Free bool `json:"free"`
	// AnswerTokens is the most tokens of an answer (0: 4000).
	AnswerTokens int `json:"answerTokens"`
	// ThinkingRoom is added to that for a model that thinks before it answers: the
	// thinking counts against the limit, and the answer would be cut off without
	// room for it (0: none).
	ThinkingRoom int `json:"thinkingRoom"`
	// JSONMode says the model's host takes response_format json_object, which
	// Agent mode asks for. Without it the agent asks for JSON in words, and asks
	// again when it cannot read the answer.
	JSONMode bool `json:"jsonMode"`
	// Hosts are the OpenRouter providers (slugs) that may answer, and no others.
	// Empty: OpenRouter chooses, which the privacy page cannot name.
	Hosts []string `json:"hosts"`
}

// defaultCustomModels are in the list until an admin saves one of their own: the
// two free models that were tried and answered, off.
func defaultCustomModels() []CustomModel {
	return []CustomModel{
		{ID: modelNemotronFree, Name: "Nemotron 3 Super (free)", Free: true,
			AnswerTokens: 4000, ThinkingRoom: 3000, JSONMode: true, Hosts: []string{"nvidia"}},
		{ID: modelNorthFree, Name: "North mini code (free)", Free: true,
			AnswerTokens: 4000, ThinkingRoom: 3000, JSONMode: false, Hosts: []string{"cohere"}},
	}
}

// customList is the list in effect: the admin's, or the defaults while nothing
// was ever saved (nil; an admin who removed them all has an empty list).
func (g GenieSettings) customList() []CustomModel {
	if g.CustomModels != nil {
		return g.CustomModels
	}
	return defaultCustomModels()
}

// model is the entry of the proxy's table for it.
func (c CustomModel) model() chatModel {
	answer := c.AnswerTokens
	if answer <= 0 {
		answer = defaultCustomAnswer
	}
	return chatModel{
		ID: c.ID, Name: c.Name, Provider: providerOpenRouter, MaxTokensCap: answer,
		Free: c.Free || strings.HasSuffix(c.ID, ":free"), Hosts: append([]string(nil), c.Hosts...),
		ThinkingRoom: c.ThinkingRoom, NoJSONMode: !c.JSONMode, Custom: true,
	}
}

// modelByID finds a built-in model or one an admin added (on or off).
func (g GenieSettings) modelByID(id string) (chatModel, bool) {
	if m, ok := chatModels[id]; ok {
		return m, true
	}
	for _, c := range g.customList() {
		if c.ID == id {
			return c.model(), true
		}
	}
	return chatModel{}, false
}

func modelByID(id string) (chatModel, bool) { return GetSiteSettings().Genie.modelByID(id) }

// allModelIDs are the built-in models, then the ones an admin added.
func (g GenieSettings) allModelIDs() []string {
	ids := append([]string(nil), chatModelOrder...)
	for _, c := range g.customList() {
		ids = append(ids, c.ID)
	}
	return ids
}

// normalizeCustomModels cleans the list of an untrusted source and says what is
// wrong with it. nil stays nil: "never configured".
func normalizeCustomModels(in []CustomModel) ([]CustomModel, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) > maxCustomModels {
		return nil, fmt.Errorf("at most %d OpenRouter models can be added", maxCustomModels)
	}
	out := []CustomModel{}
	seen := map[string]bool{}
	for _, c := range in {
		c.ID = strings.ToLower(strings.TrimSpace(c.ID))
		if len(c.ID) > customModelIDMaxLength || !customModelIDPattern.MatchString(c.ID) {
			return nil, fmt.Errorf("%q is not an OpenRouter model id (it looks like author/model-name)", c.ID)
		}
		if _, builtin := chatModels[c.ID]; builtin {
			return nil, fmt.Errorf("%s is one of the built-in models; they cannot be added or removed", c.ID)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s was added twice", c.ID)
		}
		seen[c.ID] = true

		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = c.ID
		}
		if utf8.RuneCountInString(c.Name) > maxCustomNameChars {
			return nil, fmt.Errorf("the name of %s is longer than %d characters", c.ID, maxCustomNameChars)
		}
		for _, r := range c.Name {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("the name of %s has a control character", c.ID)
			}
		}
		if c.AnswerTokens != 0 && (c.AnswerTokens < minAnswerCap || c.AnswerTokens > maxAnswerCap) {
			return nil, fmt.Errorf("the answer size of %s must be between %d and %d tokens (or empty)", c.ID, minAnswerCap, maxAnswerCap)
		}
		if c.ThinkingRoom < 0 || c.ThinkingRoom > maxCustomThinkingRoom {
			return nil, fmt.Errorf("the room for thinking of %s must be between 0 and %d tokens", c.ID, maxCustomThinkingRoom)
		}
		if strings.HasSuffix(c.ID, ":free") {
			c.Free = true
		}
		hosts := []string{}
		seenHost := map[string]bool{}
		for _, h := range c.Hosts {
			h = strings.ToLower(strings.TrimSpace(h))
			if h == "" || seenHost[h] {
				continue
			}
			if !hostSlugPattern.MatchString(h) {
				return nil, fmt.Errorf("%q is not a provider name of %s", h, c.ID)
			}
			seenHost[h] = true
			hosts = append(hosts, h)
		}
		if len(hosts) > maxCustomHosts {
			return nil, fmt.Errorf("at most %d providers can be named for %s", maxCustomHosts, c.ID)
		}
		c.Hosts = hosts
		out = append(out, c)
	}
	return out, nil
}

// publicCustomModel is what a page needs of a model that is on.
type publicCustomModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Free bool   `json:"free"`
}

// publicCustomModels lists the models that are on, for the picker.
func (g GenieSettings) publicCustomModels() []publicCustomModel {
	out := []publicCustomModel{}
	for _, c := range g.customList() {
		if c.Enabled {
			out = append(out, publicCustomModel{ID: c.ID, Name: c.Name, Free: c.model().Free})
		}
	}
	return out
}

// customModelChanges describes what an admin did to the list, for the audit log.
func customModelChanges(a, b GenieSettings) []string {
	before := map[string]CustomModel{}
	for _, c := range a.customList() {
		before[c.ID] = c
	}
	var out []string
	after := map[string]bool{}
	for _, c := range b.customList() {
		after[c.ID] = true
		old, existed := before[c.ID]
		switch {
		case !existed:
			state := "off"
			if c.Enabled {
				state = "on"
			}
			out = append(out, "OpenRouter model "+c.ID+" added ("+state+")")
		case old.Enabled != c.Enabled:
			out = append(out, "OpenRouter model "+c.ID+" "+map[bool]string{true: "switched on", false: "switched off"}[c.Enabled])
		case fmt.Sprint(old) != fmt.Sprint(c):
			out = append(out, "OpenRouter model "+c.ID+" changed (name, limits or providers)")
		}
	}
	for _, c := range a.customList() {
		if !after[c.ID] {
			out = append(out, "OpenRouter model "+c.ID+" removed")
		}
	}
	return out
}
