package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"utils"
)

// fakeStore is MongoDB's stand-in: one document with a version, which can be
// made unreachable.
type fakeStore struct {
	mu    sync.Mutex
	data  []byte
	ver   int64
	found bool
	down  bool
}

func (f *fakeStore) Name() string { return "mongodb" }

func (f *fakeStore) Load() ([]byte, int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, 0, false, errors.New("server selection timeout")
	}
	return f.data, f.ver, f.found, nil
}

func (f *fakeStore) Save(data []byte, base int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return 0, errors.New("server selection timeout")
	}
	if base != f.ver {
		return 0, errSettingsConflict
	}
	f.data, f.ver, f.found = data, f.ver+1, true
	return f.ver, nil
}

// put stores settings as another instance would.
func (f *fakeStore) put(t *testing.T, s SiteSettings) {
	t.Helper()
	data, _ := json.Marshal(s)
	f.mu.Lock()
	f.data, f.ver, f.found = data, f.ver+1, true
	f.mu.Unlock()
}

// withStore points the settings at f (with settings.json in a temp directory)
// and puts everything back afterwards.
func withStore(t *testing.T, f *fakeStore) {
	t.Helper()
	oldFile := SETTINGS_FILE
	SETTINGS_FILE = filepath.Join(t.TempDir(), "settings.json")
	settingsMu.Lock()
	siteSettings, settingsLoaded = SiteSettings{}, false
	settingsBackend, settingsVersion, settingsSynced, settingsLastErr, settingsMigrated = f, 0, false, "", ""
	settingsMu.Unlock()
	t.Cleanup(func() {
		SETTINGS_FILE = oldFile
		settingsMu.Lock()
		siteSettings, settingsLoaded = SiteSettings{}, false
		settingsBackend, settingsVersion, settingsSynced, settingsLastErr, settingsMigrated = nil, 0, false, "", ""
		keysProblem = ""
		settingsMu.Unlock()
		utils.SetKeyOverrides("", "")
		utils.SetExtraAdmins(nil)
	})
}

func writeSettingsFile(t *testing.T, s SiteSettings) {
	t.Helper()
	data, _ := json.Marshal(s)
	if err := ioutil.WriteFile(SETTINGS_FILE, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFirstStartUploadsTheLocalFileOnce(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	writeSettingsFile(t, SiteSettings{Announcement: Announcement{Text: "from the file", Level: "info"}})

	syncSettingsOnce()
	if got := GetSiteSettings().Announcement.Text; got != "from the file" {
		t.Fatalf("the uploaded settings are not in use: %q", got)
	}
	if !f.found || f.ver != 1 || !strings.Contains(string(f.data), "from the file") {
		t.Fatalf("the store holds %q at version %d", f.data, f.ver)
	}
	if !strings.Contains(currentSettingsStoreStatus().Detail, "uploaded") {
		t.Errorf("the health page does not say what the first start did: %+v", currentSettingsStoreStatus())
	}

	// the file is not read again: the store wins
	writeSettingsFile(t, SiteSettings{Announcement: Announcement{Text: "changed in the file", Level: "info"}})
	syncSettingsOnce()
	if got := GetSiteSettings().Announcement.Text; got != "from the file" {
		t.Fatalf("a later change to the file was taken: %q", got)
	}
	if f.ver != 1 {
		t.Fatalf("the store was written again: version %d", f.ver)
	}
}

func TestAStoreThatHoldsSettingsWinsOverTheFile(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{Maintenance: Maintenance{Enabled: true, Message: "back soon"}})
	writeSettingsFile(t, SiteSettings{Announcement: Announcement{Text: "from the file", Level: "info"}})

	syncSettingsOnce()
	got := GetSiteSettings()
	if !got.Maintenance.Enabled || got.Announcement.Text != "" {
		t.Fatalf("settings: %+v", got)
	}
	if f.ver != 1 {
		t.Fatalf("the file was uploaded over the stored settings: version %d", f.ver)
	}
}

func TestNothingToUploadMeansTheDefaults(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	syncSettingsOnce()
	if f.found {
		t.Fatalf("a document was created out of nothing: %s", f.data)
	}
	if st := currentSettingsStoreStatus(); !st.Healthy {
		t.Errorf("an empty store is healthy: %+v", st)
	}
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != nil {
		t.Fatal(err)
	}
	if !f.found || f.ver != 1 || !GetSiteSettings().ColorOfTheDay {
		t.Fatalf("first save: found=%v version=%d", f.found, f.ver)
	}
}

func TestSavesGoToTheStoreAndNotTheFile(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	syncSettingsOnce()
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SETTINGS_FILE); !os.IsNotExist(err) {
		t.Fatalf("settings.json was written although MongoDB is the store: %v", err)
	}
}

func TestAnUnreachableStoreMeansDefaultsAndNoSaves(t *testing.T) {
	f := &fakeStore{down: true}
	withStore(t, f)
	writeSettingsFile(t, SiteSettings{Announcement: Announcement{Text: "from the file", Level: "info"}})

	syncSettingsOnce()
	if got := GetSiteSettings(); got.Announcement.Text != "" || got.Genie.Disabled {
		t.Fatalf("not the defaults: %+v", got)
	}
	st := currentSettingsStoreStatus()
	if st.Healthy || st.Degraded || st.Name != "mongodb" || !strings.Contains(st.Detail, "defaults") {
		t.Fatalf("status: %+v", st)
	}
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != errSettingsUnavailable {
		t.Fatalf("a save with the store down: %v", err)
	}

	// it comes back: the stored settings (here: the upload of the file) are taken
	f.mu.Lock()
	f.down = false
	f.mu.Unlock()
	syncSettingsOnce()
	if st := currentSettingsStoreStatus(); !st.Healthy {
		t.Fatalf("status after it came back: %+v", st)
	}
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAStoreThatGoesDownAfterwardsKeepsTheLastSettings(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{Genie: GenieSettings{Disabled: true}})
	syncSettingsOnce()
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	syncSettingsOnce()
	if !GetSiteSettings().Genie.Disabled {
		t.Fatal("the last known settings were dropped when the store went down")
	}
	if st := currentSettingsStoreStatus(); !st.Degraded {
		t.Fatalf("status: %+v", st)
	}
}

func TestAnotherInstancesChangeIsPickedUp(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	syncSettingsOnce()
	f.put(t, SiteSettings{Maintenance: Maintenance{Enabled: true}})
	syncSettingsOnce()
	if !GetSiteSettings().Maintenance.Enabled {
		t.Fatal("the change made elsewhere was not taken")
	}
}

func TestAStaleFormIsRefused(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{})
	syncSettingsOnce()
	opened := settingsVersion // the admin's form was filled from this version

	f.put(t, SiteSettings{ColorOfTheDay: true}) // somebody else saves
	syncSettingsOnce()
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{Disabled: true}}, opened); err != errSettingsConflict {
		t.Fatalf("a save from an out-of-date form: %v", err)
	}
	if !GetSiteSettings().ColorOfTheDay || GetSiteSettings().Genie.Disabled {
		t.Fatalf("the refused save changed something: %+v", GetSiteSettings())
	}
	// the same save from a current form goes through
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{Disabled: true}}, settingsVersion); err != nil {
		t.Fatal(err)
	}
}

func TestTwoInstancesCannotOverwriteEachOther(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{})
	syncSettingsOnce()
	// the store moves on between this server's last read and its save
	f.put(t, SiteSettings{ColorOfTheDay: true})
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{Disabled: true}}); err != errSettingsConflict {
		t.Fatalf("the compare-and-set did not refuse: %v", err)
	}
}

func TestInvalidStoredSettingsAreNotApplied(t *testing.T) {
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{Announcement: Announcement{Text: "ok", Level: "info"}})
	syncSettingsOnce()
	f.mu.Lock()
	f.data, f.ver = []byte(`{"announcement":{"text":"x","level":"loud"}}`), f.ver+1
	f.mu.Unlock()
	syncSettingsOnce()
	if GetSiteSettings().Announcement.Text != "ok" {
		t.Fatalf("an invalid document was applied: %+v", GetSiteSettings())
	}
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != nil {
		t.Fatalf("an admin cannot save over an invalid document: %v", err)
	}
}

func TestAWorkerNeverUsesTheStore(t *testing.T) {
	t.Setenv("OPENREPL_MONGODB_URI", "mongodb://127.0.0.1:1/")
	withStore(t, &fakeStore{})
	settingsMu.Lock()
	settingsBackend = nil
	settingsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartSettingsSync(ctx, ModeWorker)
	time.Sleep(50 * time.Millisecond)
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsBackend != nil {
		t.Fatal("a worker started the settings sync")
	}
}

func TestTheFileStoreStaysTheDefault(t *testing.T) {
	t.Setenv("OPENREPL_MONGODB_URI", "")
	withStore(t, &fakeStore{})
	settingsMu.Lock()
	settingsBackend = nil
	settingsMu.Unlock()
	if err := SaveSiteSettings(SiteSettings{ColorOfTheDay: true}); err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadFile(SETTINGS_FILE)
	if err != nil || !strings.Contains(string(data), `"colorOfTheDay": true`) {
		t.Fatalf("settings.json: %v %s", err, data)
	}
	if st := currentSettingsStoreStatus(); st.Name != "file" || !st.Healthy {
		t.Fatalf("status: %+v", st)
	}
}

func TestTheStoreErrorNeverShowsThePassword(t *testing.T) {
	uri := "mongodb+srv://boss:hunter2@cluster0.example.mongodb.net/?retryWrites=true"
	err := redactURI(errors.New("error parsing uri ("+uri+"): bad"), uri)
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatal(err)
	}
}

// ---- through the dashboard's API ---------------------------------------------

func TestTheDashboardGetsTheStoreAndVersionAndSendsItBack(t *testing.T) {
	_, h := adminTestServer(t)
	f := &fakeStore{}
	withStore(t, f)
	f.put(t, SiteSettings{})
	syncSettingsOnce()

	var r settingsReply
	decode(t, get(h, "/admin/settings"), &r)
	if r.Store != "mongodb" || r.Version != 1 {
		t.Fatalf("store %q version %d", r.Store, r.Version)
	}
	// somebody else saves; this admin's form is now out of date
	f.put(t, SiteSettings{ColorOfTheDay: true})
	syncSettingsOnce()
	w := post(h, "/admin/settings", `{"genie":{"disabled":true},"version":1}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Reload") {
		t.Fatalf("a stale form: %d %s", w.Code, w.Body.String())
	}
	decode(t, get(h, "/admin/settings"), &r)
	w = post(h, "/admin/settings", fmt.Sprintf(`{"genie":{"disabled":true},"version":%d}`, r.Version))
	if w.Code != 200 {
		t.Fatalf("a current form: %d %s", w.Code, w.Body.String())
	}
	var after settingsReply
	decode(t, w, &after)
	if after.Version != r.Version+1 || !after.Genie.Disabled {
		t.Fatalf("reply after the save: %+v", after)
	}
}

func TestTheDashboardSaysWhenTheStoreIsNotReachable(t *testing.T) {
	_, h := adminTestServer(t)
	f := &fakeStore{down: true}
	withStore(t, f)
	syncSettingsOnce()
	w := post(h, "/admin/settings", `{"genie":{"disabled":true}}`)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not reachable") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// ---- models ----------------------------------------------------------------------

func TestModelSwitchesAreChecked(t *testing.T) {
	for name, g := range map[string]GenieSettings{
		"unknown model":   {DisabledModels: []string{"gpt-9"}},
		"all off":         {DisabledModels: []string{modelLuna, modelMini, modelGemma}},
		"unknown default": {DefaultModel: "gpt-9"},
		"default is off":  {DisabledModels: []string{modelMini}, DefaultModel: modelMini},
	} {
		if _, err := (SiteSettings{Genie: g}).normalize(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	s, err := SiteSettings{Genie: GenieSettings{DisabledModels: []string{modelMini, " ", modelMini, modelGemma}, DefaultModel: " " + modelLuna}}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Genie.DisabledModels, ","); got != modelMini+","+modelGemma && got != modelGemma+","+modelMini {
		t.Errorf("switched off: %s", got)
	}
	if s.Genie.DefaultModel != modelLuna {
		t.Errorf("default %q", s.Genie.DefaultModel)
	}
}

func chatCall(t *testing.T, model string) (int, string) {
	t.Helper()
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	w := proxyCall(t, `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
	return w.Code, w.Body.String()
}

func TestASwitchedOffModelIsRefusedByTheProxy(t *testing.T) {
	adminTestServer(t)
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{DisabledModels: []string{modelLuna}}}); err != nil {
		t.Fatal(err)
	}
	url, _, _ := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })

	code, body := chatCall(t, modelLuna)
	var e ErrorResponse
	json.Unmarshal([]byte(body), &e)
	if code != 503 || e.Error.Type != "model_unavailable" || e.Error.Code != "model_disabled" || !strings.Contains(e.Error.Message, "GPT-6 Luna") {
		t.Fatalf("a switched-off model: %d %s", code, body)
	}
	if code, body = chatCall(t, modelMini); code != 200 {
		t.Fatalf("a model that is on: %d %s", code, body)
	}
}

func TestTheDefaultModelFollowsTheSwitches(t *testing.T) {
	adminTestServer(t)
	byName := func(body string) string {
		_, m, err := sanitizeChatBody([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	const none = `{"messages":[{"role":"user","content":"hi"}]}`
	if got := byName(none); got != modelMini {
		t.Fatalf("built-in default %s", got)
	}
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{DefaultModel: modelLuna}})
	if got := byName(none); got != modelLuna {
		t.Fatalf("the admin's default: %s", got)
	}
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{DisabledModels: []string{modelMini}}})
	if got := byName(none); got != modelLuna {
		t.Fatalf("with mini off, a request naming none: %s", got)
	}
	// an unknown name gets the same default; a name that is off is not rerouted
	if got := byName(`{"model":"gpt-9","messages":[{"role":"user","content":"hi"}]}`); got != modelLuna {
		t.Fatalf("unknown: %s", got)
	}
	if got := byName(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`); got != modelMini {
		t.Fatalf("a model that is off keeps its own name so that the proxy refuses it: %s", got)
	}
}

func TestTheDashboardListsTheModelsAndTheirKeys(t *testing.T) {
	_, h := adminTestServer(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-x")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "")
	var r settingsReply
	decode(t, get(h, "/admin/settings"), &r)
	if len(r.Models) != 3 || r.Models[0].ID != modelLuna || !r.Models[0].KeySet || r.Models[2].ID != modelGemma || r.Models[2].KeySet {
		t.Fatalf("models: %+v", r.Models)
	}
	w := post(h, "/admin/settings", `{"genie":{"disabledModels":["gpt-4o-mini"],"defaultModel":"gpt-6-luna"}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w := post(h, "/admin/settings", `{"genie":{"disabledModels":["gpt-6-luna","gpt-4o-mini","google/gemma-4-31b-it"]}}`); w.Code != 400 {
		t.Fatalf("all models off: %d", w.Code)
	}
	pub := GetSiteSettings().public()
	if len(pub.DisabledModels) != 1 || pub.DefaultModel != modelLuna {
		t.Fatalf("public: %+v", pub)
	}
}

// ---- the Genie numbers ------------------------------------------------------------

func TestGenieNumbersAreChecked(t *testing.T) {
	for name, g := range map[string]GenieSettings{
		"editor too small":  {ContextEditorChars: 10},
		"editor too big":    {ContextEditorChars: 1 << 20},
		"terminal chars":    {ContextTerminalChars: 5},
		"terminal lines":    {ContextTerminalLines: 1000},
		"history too short": {HistoryMessages: 2},
		"history too long":  {HistoryMessages: 500},
		"timeout too short": {OpenRouterTimeoutSec: 1},
		"timeout too long":  {OpenRouterTimeoutSec: 200},
		"cap too small":     {AnswerCaps: map[string]int{modelLuna: 10}},
		"cap too big":       {AnswerCaps: map[string]int{modelLuna: 1 << 20}},
		"cap of no model":   {AnswerCaps: map[string]int{"gpt-9": 1000}},
		"host of nobody":    {OpenRouterHosts: []string{"evil.example/fp4"}},
		"negative":          {HistoryMessages: -5},
	} {
		if _, err := (SiteSettings{Genie: g}).normalize(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	s, err := SiteSettings{Genie: GenieSettings{
		ContextEditorChars: 20000, ContextTerminalLines: 50, HistoryMessages: 30, OpenRouterTimeoutSec: 30,
		AnswerCaps: map[string]int{modelLuna: 4000, modelMini: 0}, OpenRouterHosts: []string{"coreweave/fp4", " ", "coreweave/fp4", "modelrun/fp4"},
	}}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Genie.AnswerCaps) != 1 || s.Genie.AnswerCaps[modelLuna] != 4000 {
		t.Errorf("caps: %v", s.Genie.AnswerCaps)
	}
	if strings.Join(s.Genie.OpenRouterHosts, ",") != "coreweave/fp4,modelrun/fp4" {
		t.Errorf("hosts: %v", s.Genie.OpenRouterHosts)
	}
}

func TestThePageGetsTheEffectiveContextNumbers(t *testing.T) {
	adminTestServer(t)
	if got := GetSiteSettings().public().GenieContext; got != (genieContext{12000, 4000, 20, 20}) {
		t.Fatalf("built-in: %+v", got)
	}
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{ContextEditorChars: 20000, HistoryMessages: 8}}); err != nil {
		t.Fatal(err)
	}
	if got := GetSiteSettings().public().GenieContext; got != (genieContext{20000, 4000, 20, 8}) {
		t.Fatalf("set: %+v", got)
	}
	if strings.Contains(get2(t, "/settings.js"), "answerCaps") {
		t.Error("the answer sizes are in the public settings")
	}
}

// get2 reads the public settings script.
func get2(t *testing.T, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	handleSettingsJS(w, httptest.NewRequest("GET", path, nil))
	return w.Body.String()
}

func TestAnswerSizesFollowTheSettings(t *testing.T) {
	adminTestServer(t)
	sized := func(model, field string, asked int) float64 {
		body := fmt.Sprintf(`{"model":%q,"%s":%d,"messages":[{"role":"user","content":"hi"}]}`, model, field, asked)
		out, _, err := sanitizeChatBody([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		json.Unmarshal(out, &got)
		return got[field].(float64)
	}
	if sized(modelMini, "max_tokens", 9000) != 3000 || sized(modelLuna, "max_completion_tokens", 9000) != 7000 {
		t.Fatal("the built-in caps changed")
	}
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{AnswerCaps: map[string]int{modelMini: 6000, modelLuna: 3000}}}); err != nil {
		t.Fatal(err)
	}
	if got := sized(modelMini, "max_tokens", 9000); got != 6000 {
		t.Errorf("mini cap: %v", got)
	}
	if got := sized(modelLuna, "max_completion_tokens", 9000); got != 3000 {
		t.Errorf("luna cap: %v", got)
	}
	if got := sized(modelGemma, "max_tokens", 9000); got != 4000 {
		t.Errorf("a model without a number keeps its cap: %v", got)
	}
}

func TestOpenRouterHostsAndTimeoutFollowTheSettings(t *testing.T) {
	adminTestServer(t)
	routing := func() map[string]interface{} {
		out, _, err := sanitizeChatBody([]byte(gemmaRequest))
		if err != nil {
			t.Fatal(err)
		}
		var got struct{ Provider map[string]interface{} }
		json.Unmarshal([]byte(strings.Replace(string(out), `"provider"`, `"Provider"`, 1)), &got)
		return got.Provider
	}
	if fmt.Sprint(routing()["only"]) != "[modelrun/fp4 coreweave/fp4]" || openRouterTimeoutSetting() != 60*time.Second {
		t.Fatalf("built in: %v %v", routing(), openRouterTimeoutSetting())
	}
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{OpenRouterHosts: []string{"coreweave/fp4"}, OpenRouterTimeoutSec: 25}}); err != nil {
		t.Fatal(err)
	}
	r := routing()
	if fmt.Sprint(r["only"]) != "[coreweave/fp4]" || fmt.Sprint(r["order"]) != "[coreweave/fp4]" || r["allow_fallbacks"] != false {
		t.Fatalf("chosen: %v", r)
	}
	if openRouterTimeoutSetting() != 25*time.Second {
		t.Fatalf("timeout %v", openRouterTimeoutSetting())
	}
}

func TestTheDashboardShowsTheBuiltInNumbers(t *testing.T) {
	_, h := adminTestServer(t)
	var r settingsReply
	decode(t, get(h, "/admin/settings"), &r)
	d := r.Defaults
	if d.ContextEditorChars != 12000 || d.HistoryMessages != 20 || d.OpenRouterTimeoutSec != 60 || d.AnswerCaps[modelLuna] != 7000 ||
		len(d.OpenRouterHosts) != 2 || d.OpenRouterHosts[0].Name != "ModelRun" || d.Limits["historyMessages"] != [2]int{6, 50} {
		t.Fatalf("defaults: %+v", d)
	}
	w := post(h, "/admin/settings", `{"genie":{"historyMessages":3}}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "conversation length") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
