package server

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// isolateSettings gives a test the default settings and a settings file of its
// own: the real /opt/gotty/settings.json of a machine that runs a server (and
// has had switches changed in its dashboard) must not decide a test's outcome.
func isolateSettings(t *testing.T) {
	t.Helper()
	old := SETTINGS_FILE
	SETTINGS_FILE = filepath.Join(t.TempDir(), "settings.json")
	settingsMu.Lock()
	siteSettings, settingsLoaded, settingsBackend = SiteSettings{}, false, nil
	settingsMu.Unlock()
	t.Cleanup(func() {
		SETTINGS_FILE = old
		settingsMu.Lock()
		siteSettings, settingsLoaded = SiteSettings{}, false
		settingsMu.Unlock()
	})
}

func sanitized(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	isolateSettings(t)
	out, _, err := sanitizeChatBody([]byte(body))
	if err != nil {
		t.Fatalf("sanitizeChatBody(%s): %v", body, err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	return got
}

func TestChatBodyLunaKeepsItsOwnSettings(t *testing.T) {
	got := sanitized(t, `{"model":"gpt-6-luna","reasoning_effort":"medium","max_completion_tokens":3000,
		"temperature":0.5,"max_tokens":800,"messages":[{"role":"user","content":"hi"}]}`)
	if got["model"] != "gpt-6-luna" || got["reasoning_effort"] != "medium" || got["max_completion_tokens"] != float64(3000) {
		t.Fatalf("Luna settings changed: %v", got)
	}
	if _, has := got["temperature"]; has {
		t.Fatalf("Luna must not get temperature: %v", got)
	}
	if _, has := got["max_tokens"]; has {
		t.Fatalf("Luna must not get max_tokens: %v", got)
	}
}

func TestChatBodyMiniKeepsItsOwnSettings(t *testing.T) {
	got := sanitized(t, `{"model":"gpt-4o-mini","reasoning_effort":"high","max_completion_tokens":4000,
		"temperature":0.5,"max_tokens":800,"messages":[{"role":"user","content":"hi"}]}`)
	if got["model"] != "gpt-4o-mini" || got["temperature"] != 0.5 || got["max_tokens"] != float64(800) {
		t.Fatalf("4o mini settings changed: %v", got)
	}
	for _, field := range []string{"reasoning_effort", "max_completion_tokens"} {
		if _, has := got[field]; has {
			t.Fatalf("4o mini must not get %s: %v", field, got)
		}
	}
}

func TestChatBodyOtherModelsFallBackToMini(t *testing.T) {
	for _, model := range []string{"gpt-3.5-turbo", "gpt-6-astra", "o1-pro", "", "GPT-6-LUNA"} {
		got := sanitized(t, `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
		if got["model"] != "gpt-4o-mini" {
			t.Errorf("model %q reached OpenAI as %v", model, got["model"])
		}
	}
	got := sanitized(t, `{"messages":[{"role":"user","content":"hi"}]}`)
	if got["model"] != "gpt-4o-mini" {
		t.Errorf("a request without a model got %v", got["model"])
	}
}

func TestChatBodyEffortAndBudgets(t *testing.T) {
	// a missing or unknown effort becomes low, and the budget follows the effort
	got := sanitized(t, `{"model":"gpt-6-luna","messages":[{"role":"user","content":"hi"}]}`)
	if got["reasoning_effort"] != "low" || got["max_completion_tokens"] != float64(2000) {
		t.Errorf("defaults: %v", got)
	}
	got = sanitized(t, `{"model":"gpt-6-luna","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`)
	if got["reasoning_effort"] != "low" {
		t.Errorf("an effort the panel does not offer (max) was passed on: %v", got)
	}
	for effort, want := range map[string]float64{"none": 1000, "low": 2000, "medium": 3000, "high": 4000} {
		got = sanitized(t, `{"model":"gpt-6-luna","reasoning_effort":"`+effort+`","messages":[{"role":"user","content":"hi"}]}`)
		if got["max_completion_tokens"] != want {
			t.Errorf("effort %s: budget %v, want %v", effort, got["max_completion_tokens"], want)
		}
	}
}

func TestChatBodyClampsNumbers(t *testing.T) {
	got := sanitized(t, `{"model":"gpt-6-luna","max_completion_tokens":999999,"messages":[{"role":"user","content":"hi"}]}`)
	if got["max_completion_tokens"] != float64(7000) {
		t.Errorf("Luna answer budget not capped: %v", got["max_completion_tokens"])
	}
	got = sanitized(t, `{"model":"gpt-6-luna","max_completion_tokens":-5,"messages":[{"role":"user","content":"hi"}]}`)
	if got["max_completion_tokens"] != float64(100) {
		t.Errorf("Luna answer budget not raised to the floor: %v", got["max_completion_tokens"])
	}
	got = sanitized(t, `{"model":"gpt-4o-mini","max_tokens":100000,"temperature":9,"messages":[{"role":"user","content":"hi"}]}`)
	if got["max_tokens"] != float64(3000) || got["temperature"] != float64(2) {
		t.Errorf("4o mini numbers not capped: %v", got)
	}
	got = sanitized(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if got["max_tokens"] != float64(800) {
		t.Errorf("4o mini default budget: %v", got["max_tokens"])
	}
}

func TestChatBodyPassesOnlyJSONObjectFormat(t *testing.T) {
	got := sanitized(t, `{"model":"gpt-6-luna","response_format":{"type":"json_object"},"messages":[{"role":"user","content":"hi"}]}`)
	format, ok := got["response_format"].(map[string]interface{})
	if !ok || format["type"] != "json_object" || len(format) != 1 {
		t.Errorf("json_object was not passed on as it is: %v", got["response_format"])
	}
	for _, bad := range []string{
		`{"type":"json_schema","json_schema":{"name":"x"}}`,
		`{"type":"text"}`,
		`"json_object"`,
		`{"type":"json_object","extra":1,"json_schema":{}}`,
	} {
		got = sanitized(t, `{"model":"gpt-4o-mini","response_format":`+bad+`,"messages":[{"role":"user","content":"hi"}]}`)
		if format, ok := got["response_format"].(map[string]interface{}); ok && (len(format) != 1 || format["type"] != "json_object") {
			t.Errorf("response_format %s changed what OpenAI is asked for: %v", bad, format)
		}
	}
}

func TestChatBodyDropsFieldsTheServiceDoesNotUse(t *testing.T) {
	got := sanitized(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],
		"n":50,"tools":[{"type":"function"}],"user":"x","top_p":0.1,"stream":true}`)
	for _, field := range []string{"n", "tools", "user", "top_p"} {
		if _, has := got[field]; has {
			t.Errorf("%s reached OpenAI: %v", field, got)
		}
	}
	if got["stream"] != true {
		t.Errorf("stream was dropped: %v", got)
	}
	if got = sanitized(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],"stream":"yes"}`); got["stream"] != nil {
		t.Errorf("a stream that is not true was kept: %v", got)
	}
}

func TestChatBodyKeepsTheMessagesAsSent(t *testing.T) {
	const messages = `[{"role":"system","content":"code é ☃"},{"role":"user","content":"why?"}]`
	out, _, err := sanitizeChatBody([]byte(`{"model":"gpt-4o-mini","messages":` + messages + `}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	var want, have interface{}
	json.Unmarshal([]byte(messages), &want)
	json.Unmarshal(got.Messages, &have)
	wantJSON, _ := json.Marshal(want)
	haveJSON, _ := json.Marshal(have)
	if string(wantJSON) != string(haveJSON) {
		t.Errorf("messages changed:\n got %s\nwant %s", haveJSON, wantJSON)
	}
}

func TestChatBodyRefusesWhatIsNotAChatRequest(t *testing.T) {
	for _, body := range []string{``, `not json`, `[]`, `{"model":"gpt-4o-mini"}`, `{"messages":"hi"}`, `{"messages":{}}`, `null`} {
		if _, _, err := sanitizeChatBody([]byte(body)); err == nil {
			t.Errorf("sanitizeChatBody(%q) accepted it", body)
		}
	}
}

func TestChatBodyGemmaGoesToOpenRouterWithTheHostRule(t *testing.T) {
	const body = `{"model":"google/gemma-4-31b-it","temperature":9,"max_tokens":99999,
		"reasoning_effort":"high","max_completion_tokens":5000,
		"response_format":{"type":"json_object"},"messages":[{"role":"user","content":"hi"}]}`
	out, model, err := sanitizeChatBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != modelGemma || model.Provider != providerOpenRouter {
		t.Fatalf("wrong model: %+v", model)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["max_tokens"] != float64(4000) || got["temperature"] != float64(2) {
		t.Errorf("Gemma limits not applied: %v", got)
	}
	for _, f := range []string{"reasoning_effort", "max_completion_tokens"} {
		if _, has := got[f]; has {
			t.Errorf("Gemma must not get %s: %v", f, got)
		}
	}
	if !reflect.DeepEqual(got["response_format"], map[string]interface{}{"type": "json_object"}) {
		t.Errorf("JSON mode was not passed on: %v", got["response_format"])
	}
	want := map[string]interface{}{
		"order":           []interface{}{"modelrun/fp4", "coreweave/fp4"},
		"only":            []interface{}{"modelrun/fp4", "coreweave/fp4"},
		"allow_fallbacks": false,
	}
	if !reflect.DeepEqual(got["provider"], want) {
		t.Errorf("host rule = %v, want %v", got["provider"], want)
	}
}

func TestChatBodyHostRuleCannotBeChangedFromTheBrowser(t *testing.T) {
	got := sanitized(t, `{"model":"google/gemma-4-31b-it","provider":{"order":["deepinfra"],"allow_fallbacks":true},
		"messages":[{"role":"user","content":"hi"}]}`)
	provider, _ := got["provider"].(map[string]interface{})
	if provider["allow_fallbacks"] != false || !reflect.DeepEqual(provider["only"], []interface{}{"modelrun/fp4", "coreweave/fp4"}) {
		t.Errorf("a host rule from the browser got through: %v", got["provider"])
	}
	got = sanitized(t, `{"model":"gpt-4o-mini","provider":{"order":["x"]},"messages":[{"role":"user","content":"hi"}]}`)
	if _, has := got["provider"]; has {
		t.Errorf("OpenAI requests must not carry a provider field: %v", got)
	}
}

func TestChatBodyReturnsTheModelItIsFor(t *testing.T) {
	for asked, want := range map[string]string{
		"gpt-6-luna": modelLuna, "gpt-4o-mini": modelMini, "google/gemma-4-31b-it": modelGemma,
		"gpt-5-pro": modelMini, "google/gemma-4-26b-a4b-it": modelMini, "": modelMini,
	} {
		_, model, err := sanitizeChatBody([]byte(`{"model":"` + asked + `","messages":[{"role":"user","content":"hi"}]}`))
		if err != nil || model.ID != want {
			t.Errorf("model %q: got %q (%v), want %q", asked, model.ID, err, want)
		}
	}
}
