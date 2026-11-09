package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// sanitizedAsIs is sanitized without resetting the settings the test saved.
func sanitizedAsIs(t *testing.T, body string) map[string]interface{} {
	t.Helper()
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

func customOn(t *testing.T, id string) GenieSettings {
	t.Helper()
	g := GetSiteSettings().Genie
	g.CustomModels = append([]CustomModel{}, g.customList()...)
	for i := range g.CustomModels {
		if g.CustomModels[i].ID == id {
			g.CustomModels[i].Enabled = true
		}
	}
	return g
}

func TestModelsAnAdminAddsAreOffUntilSwitchedOn(t *testing.T) {
	isolateSettings(t)
	g := GetSiteSettings().Genie
	// nothing was ever saved: the two free models are listed, off, and hidden from the page
	if len(g.customList()) != 2 || !g.ModelDisabled(modelNemotronFree) || !g.ModelDisabled(modelNorthFree) {
		t.Fatalf("defaults: %+v", g.customList())
	}
	if pub := GetSiteSettings().public(); len(pub.CustomModels) != 0 {
		t.Fatalf("the page is offered models that are off: %+v", pub.CustomModels)
	}
	// the built-in three are untouched
	for _, id := range chatModelOrder {
		if g.ModelDisabled(id) {
			t.Errorf("%s is off", id)
		}
	}

	s := GetSiteSettings()
	s.Genie = customOn(t, modelNemotronFree)
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	g = GetSiteSettings().Genie
	pub := GetSiteSettings().public().CustomModels
	if g.ModelDisabled(modelNemotronFree) || !g.ModelDisabled(modelNorthFree) || len(pub) != 1 || pub[0].ID != modelNemotronFree || !pub[0].Free {
		t.Fatalf("after switching one on: %+v / %+v", g.customList(), pub)
	}
	// a request for it is now for it, and for the one that is still off the proxy refuses (chatproxy.go)
	if m, ok := modelByID(modelNemotronFree); !ok || GetSiteSettings().Genie.ModelDisabled(m.ID) {
		t.Fatalf("the model that was switched on is off: %+v %v", m, ok)
	}
	got2 := sanitizedAsIs(t, `{"model":"`+modelNemotronFree+`","messages":[{"role":"user","content":"hi"}]}`)
	if got2["model"] != modelNemotronFree {
		t.Fatalf("the request went to %v", got2["model"])
	}
}

func TestModelsCanBeAddedAndRemovedAndTheBuiltInOnesCannot(t *testing.T) {
	isolateSettings(t)
	s := GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{{ID: " Qwen/Qwen3-Coder:free ", Name: "  Qwen coder  ", Enabled: true, ThinkingRoom: 2000, JSONMode: true, Hosts: []string{" Together ", "together"}}}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	c := GetSiteSettings().Genie.CustomModels
	if len(c) != 1 || c[0].ID != "qwen/qwen3-coder:free" || c[0].Name != "Qwen coder" || !c[0].Free || !reflect.DeepEqual(c[0].Hosts, []string{"together"}) {
		t.Fatalf("cleaned: %+v", c)
	}
	// the added model is a model of the proxy, the default ones are gone with it
	if m, ok := modelByID("qwen/qwen3-coder:free"); !ok || !m.Custom || m.Provider != providerOpenRouter {
		t.Fatalf("lookup: %+v %v", m, ok)
	}
	if _, ok := modelByID(modelNemotronFree); ok {
		t.Fatal("a removed model is still known")
	}
	// removing all of them is a choice too: it does not bring the defaults back
	s = GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := GetSiteSettings().Genie.customList(); len(got) != 0 {
		t.Fatalf("after removing all: %+v", got)
	}

	for name, list := range map[string][]CustomModel{
		"a built-in model":      {{ID: modelGemma}},
		"a bad id":              {{ID: "not an id"}},
		"no author":             {{ID: "justaname"}},
		"the same id twice":     {{ID: "a/b"}, {ID: "A/B"}},
		"a bad provider":        {{ID: "a/b", Hosts: []string{"x y"}}},
		"too long a name":       {{ID: "a/b", Name: strings.Repeat("n", 41)}},
		"a control character":   {{ID: "a/b", Name: "bad\x07name"}},
		"a silly answer size":   {{ID: "a/b", AnswerTokens: 5}},
		"a silly thinking room": {{ID: "a/b", ThinkingRoom: 999999}},
		"too many providers":    {{ID: "a/b", Hosts: []string{"a", "b", "c", "d", "e"}}},
	} {
		s := GetSiteSettings()
		s.Genie.CustomModels = list
		if err := SaveSiteSettings(s); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	many := []CustomModel{}
	for i := 0; i <= maxCustomModels; i++ {
		many = append(many, CustomModel{ID: "a/m" + strings.Repeat("x", i)})
	}
	s = GetSiteSettings()
	s.Genie.CustomModels = many
	if err := SaveSiteSettings(s); err == nil {
		t.Error("more models than the limit were accepted")
	}
}

func TestAnAddedModelGoesWhereItsSettingsSay(t *testing.T) {
	isolateSettings(t)
	s := GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{
		{ID: "vendor/coder:free", Name: "Coder", Enabled: true, AnswerTokens: 2000, ThinkingRoom: 1500, JSONMode: false, Hosts: []string{"vendor"}},
		{ID: "other/open-model", Name: "Open", Enabled: true, JSONMode: true},
	}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	got := sanitizedAsIs(t, `{"model":"vendor/coder:free","max_tokens":99999,"temperature":0.5,"response_format":{"type":"json_object"},
		"reasoning_effort":"high","provider":{"only":["evil"]},"messages":[{"role":"user","content":"hi"}]}`)
	if got["model"] != "vendor/coder:free" || got["max_tokens"] != float64(3500) || got["temperature"] != 0.5 {
		t.Errorf("body: %v", got)
	}
	if _, has := got["response_format"]; has {
		t.Errorf("JSON mode went to a host that does not take it: %v", got)
	}
	if _, has := got["reasoning_effort"]; has {
		t.Errorf("reasoning_effort reached a custom model: %v", got)
	}
	want := map[string]interface{}{"order": []interface{}{"vendor"}, "only": []interface{}{"vendor"}, "allow_fallbacks": false}
	if !reflect.DeepEqual(got["provider"], want) {
		t.Errorf("host rule from the browser got through, or is wrong: %v", got["provider"])
	}

	// no providers named: OpenRouter chooses, and the browser cannot say otherwise
	got = sanitizedAsIs(t, `{"model":"other/open-model","provider":{"only":["evil"]},"response_format":{"type":"json_object"},"messages":[{"role":"user","content":"hi"}]}`)
	if _, has := got["provider"]; has {
		t.Errorf("a provider rule without hosts: %v", got["provider"])
	}
	if got["max_tokens"] != float64(800) || !reflect.DeepEqual(got["response_format"], map[string]interface{}{"type": "json_object"}) {
		t.Errorf("open model: %v", got)
	}
	// the host rule of Gemma is its own and an admin's choice of hosts stays Gemma's
	got = sanitizedAsIs(t, `{"model":"google/gemma-4-31b-it","messages":[{"role":"user","content":"hi"}]}`)
	if !reflect.DeepEqual(got["provider"].(map[string]interface{})["only"], []interface{}{"modelrun/fp4", "coreweave/fp4"}) {
		t.Errorf("Gemma hosts: %v", got["provider"])
	}
}

func TestTheSettingsPageKeepsTheModelListItDoesNotSend(t *testing.T) {
	s, mux := adminTestServer(t)
	_ = s
	st := GetSiteSettings()
	st.Genie.CustomModels = []CustomModel{{ID: "a/kept", Name: "Kept", Enabled: true}}
	if err := SaveSiteSettings(st); err != nil {
		t.Fatal(err)
	}
	// a dashboard page from before the list existed sends none
	if w := post(mux, "/admin/settings", `{"colorOfTheDay":true,"genie":{"guestPerMinute":1}}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got := GetSiteSettings().Genie.CustomModels; len(got) != 1 || got[0].ID != "a/kept" {
		t.Fatalf("the list was lost: %+v", got)
	}
	// the dashboard sends the list it shows, and what it sends is what is kept
	var r settingsReply
	decode(t, get(mux, "/admin/settings"), &r)
	if len(r.Genie.CustomModels) != 1 || r.Genie.CustomModels[0].ID != "a/kept" {
		t.Fatalf("the dashboard is shown %+v", r.Genie.CustomModels)
	}
	w := post(mux, "/admin/settings", `{"genie":{"customModels":[{"id":"b/new","name":"New","enabled":true,"jsonMode":true}]}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got := GetSiteSettings().Genie.CustomModels; len(got) != 1 || got[0].ID != "b/new" || !got[0].Enabled {
		t.Fatalf("saved: %+v", got)
	}
	// the audit log says what was added and removed
	audit := get(mux, "/admin/audit").Body.String()
	for _, want := range []string{"OpenRouter model b/new added (on)", "OpenRouter model a/kept removed"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit log lacks %q: %s", want, audit)
		}
	}
	// a mistake is refused with a message, and changes nothing
	w = post(mux, "/admin/settings", `{"genie":{"customModels":[{"id":"google/gemma-4-31b-it"}]}}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "built-in") {
		t.Fatalf("a built-in id: %d %s", w.Code, w.Body.String())
	}
	if got := GetSiteSettings().Genie.CustomModels; len(got) != 1 || got[0].ID != "b/new" {
		t.Fatalf("a refused save changed the list: %+v", got)
	}
}

// ---- persistence --------------------------------------------------------------------

func TestTheModelListSurvivesARestartOfTheFileStore(t *testing.T) {
	isolateSettings(t)
	s := GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{{ID: "a/b", Name: "AB", Enabled: true, Free: true, AnswerTokens: 3000, ThinkingRoom: 500, JSONMode: true, Hosts: []string{"h"}}}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	want := GetSiteSettings().Genie.CustomModels
	// a restart: nothing in memory, the file is read again
	settingsMu.Lock()
	siteSettings, settingsLoaded = SiteSettings{}, false
	settingsMu.Unlock()
	if got := GetSiteSettings().Genie.CustomModels; !reflect.DeepEqual(got, want) {
		t.Fatalf("after a restart: %+v, want %+v", got, want)
	}
}

func TestTheModelListIsKeptInTheDatabaseAndAnotherServerSeesIt(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	syncSettingsOnce()
	s := GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{{ID: "a/b", Name: "AB", Enabled: true, JSONMode: true}, {ID: "c/d:free", Name: "CD"}}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Genie struct {
			CustomModels []CustomModel `json:"customModels"`
		} `json:"genie"`
	}
	if err := json.Unmarshal(f.data, &stored); err != nil || len(stored.Genie.CustomModels) != 2 || stored.Genie.CustomModels[0].ID != "a/b" || !stored.Genie.CustomModels[1].Free {
		t.Fatalf("the database holds %s (%v)", f.data, err)
	}

	// another server (or a restart) starts with nothing and reads the database
	settingsMu.Lock()
	siteSettings, settingsLoaded, settingsSynced = SiteSettings{}, false, false
	settingsMu.Unlock()
	syncSettingsOnce()
	got := GetSiteSettings().Genie
	if len(got.CustomModels) != 2 || got.ModelDisabled("a/b") || !got.ModelDisabled("c/d:free") {
		t.Fatalf("the other server: %+v", got.CustomModels)
	}
	if pub := GetSiteSettings().public().CustomModels; len(pub) != 1 || pub[0].ID != "a/b" {
		t.Fatalf("what its page is told: %+v", pub)
	}
}
