package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"utils"
)

// SETTINGS_FILE holds site-wide switches that an admin changes at /admin.
var SETTINGS_FILE = utils.GOTTY_PATH + "/settings.json"

const (
	noticeMaxChars   = 280 // the announcement and the maintenance message
	maxDisabledLangs = 40
	maxGenieRate     = 60.0 // requests per minute
	maxExtraAdmins   = 50
)

var emailPattern = regexp.MustCompile(`^[^@\s,;<>"']+@[^@\s,;<>"']+\.[^@\s,;<>"']+$`)

// validEmail is a plain check that s looks like an address: it is what an admin
// types, and the sign-in provider decides what an account's address is.
func validEmail(s string) bool {
	return len(s) <= 254 && emailPattern.MatchString(s)
}

// SiteSettings are site-wide switches. Every field defaults to off.
type SiteSettings struct {
	// ColorOfTheDay picks a different accent colour each day
	// (js/src/preprocessing.js). Off keeps the brand coral.
	ColorOfTheDay bool `json:"colorOfTheDay"`
	// Announcement is a banner every page shows while its text is not empty.
	Announcement Announcement `json:"announcement"`
	// Maintenance stops new terminals for everyone but admins and tells
	// visitors why.
	Maintenance Maintenance `json:"maintenance"`
	// DisabledLanguages are the values of the language picker (python, cpp, ...)
	// that are switched off: the page hides them and their terminals refuse.
	DisabledLanguages []string `json:"disabledLanguages"`
	// Genie holds the AI assistant's switch and its rate limits.
	Genie GenieSettings `json:"genie"`
	// Admins are the accounts added in the dashboard, lower case, besides the
	// owners the environment names (OPENREPL_ADMIN_EMAILS). Only owners change
	// the list, through /admin/admins, and a form cannot.
	Admins []string `json:"admins"`
	// Secrets are the API keys an admin saved in the dashboard, encrypted
	// (settings_keys.go). No API reply carries them and a form cannot set them.
	Secrets *StoredKeys `json:"secrets,omitempty"`
}

// Announcement is the banner at the top of every page.
type Announcement struct {
	Text  string `json:"text"`
	Level string `json:"level"` // info or warning
}

// Maintenance is the maintenance mode switch.
type Maintenance struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message"`
}

// GenieSettings control the AI assistant.
type GenieSettings struct {
	Disabled bool `json:"disabled"`
	// GuestPerMinute and UserPerMinute are the requests per minute a guest and
	// a signed-in user get. 0 means the built-in rate.
	GuestPerMinute float64 `json:"guestPerMinute"`
	UserPerMinute  float64 `json:"userPerMinute"`
	// DisabledModels are the model ids (chatModels) that are switched off: the
	// picker hides them and the proxy answers 503 model_unavailable. At least
	// one model stays on.
	DisabledModels []string `json:"disabledModels"`
	// What Genie is told about the page, and how much of the conversation it
	// keeps: the editor's code (characters), the terminal's recent output
	// (characters and lines) and the number of messages. 0 is the built-in value.
	// The page reads them from settings.js (the effective values).
	ContextEditorChars   int `json:"contextEditorChars"`
	ContextTerminalChars int `json:"contextTerminalChars"`
	ContextTerminalLines int `json:"contextTerminalLines"`
	HistoryMessages      int `json:"historyMessages"`
	// AnswerCaps is the most tokens an answer of a model may have, by model id;
	// a model that is not in it has the built-in cap.
	AnswerCaps map[string]int `json:"answerCaps"`
	// OpenRouterTimeoutSec is how long OpenRouter may take (0: built in, 60).
	OpenRouterTimeoutSec int `json:"openRouterTimeoutSec"`
	// OpenRouterHosts are the hosts OpenRouter may use for Gemma, in order, by
	// slug (openRouterHostNames); empty is the built-in two.
	OpenRouterHosts []string `json:"openRouterHosts"`
	// DefaultModel is the model that answers a request naming none, and the one
	// a visitor starts with. Empty means the built-in choice. It must be on.
	DefaultModel string `json:"defaultModel"`

	// Agent mode (agent.go): Genie works on a task in steps, in the editor, once
	// the user allowed each kind of action. Signed-in users have it, unless an
	// admin sets AgentDisabled; it is off on the practice page unless
	// AgentOnPractice is set. A user may start AgentTasksPerHour tasks an hour
	// (0: the built-in 20), and a task has AgentMaxSteps steps (0: the built-in
	// 8, at most 8).
	AgentDisabled     bool `json:"agentDisabled"`
	AgentOnPractice   bool `json:"agentOnPractice"`
	AgentTasksPerHour int  `json:"agentTasksPerHour"`
	AgentMaxSteps     int  `json:"agentMaxSteps"`
}

// ModelDisabled reports whether an admin switched the model off.
func (g GenieSettings) ModelDisabled(id string) bool {
	for _, d := range g.DisabledModels {
		if d == id {
			return true
		}
	}
	return false
}

var languagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,19}$`)

// normalize cleans up settings from an untrusted source and says what is
// wrong with them, if anything.
func (s SiteSettings) normalize() (SiteSettings, error) {
	s.Announcement.Text = strings.TrimSpace(s.Announcement.Text)
	s.Maintenance.Message = strings.TrimSpace(s.Maintenance.Message)
	if utf8.RuneCountInString(s.Announcement.Text) > noticeMaxChars {
		return s, fmt.Errorf("the announcement is longer than %d characters", noticeMaxChars)
	}
	if utf8.RuneCountInString(s.Maintenance.Message) > noticeMaxChars {
		return s, fmt.Errorf("the maintenance message is longer than %d characters", noticeMaxChars)
	}
	switch s.Announcement.Level {
	case "info", "warning":
	case "":
		s.Announcement.Level = "info"
	default:
		return s, fmt.Errorf("the announcement level must be info or warning")
	}

	seen := map[string]bool{}
	langs := []string{}
	for _, l := range s.DisabledLanguages {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || seen[l] {
			continue
		}
		if !languagePattern.MatchString(l) {
			return s, fmt.Errorf("%q is not a language name", l)
		}
		seen[l] = true
		langs = append(langs, l)
	}
	if len(langs) > maxDisabledLangs {
		return s, fmt.Errorf("at most %d languages can be switched off", maxDisabledLangs)
	}
	sort.Strings(langs)
	s.DisabledLanguages = langs

	for name, v := range map[string]float64{"guest": s.Genie.GuestPerMinute, "signed-in": s.Genie.UserPerMinute} {
		if v < 0 || v > maxGenieRate {
			return s, fmt.Errorf("the %s Genie rate must be between 0 and %d requests per minute", name, int(maxGenieRate))
		}
	}

	admins := []string{}
	seenAdmin := map[string]bool{}
	for _, a := range s.Admins {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || seenAdmin[a] {
			continue
		}
		if !validEmail(a) {
			return s, fmt.Errorf("%q is not an email address", a)
		}
		seenAdmin[a] = true
		admins = append(admins, a)
	}
	if len(admins) > maxExtraAdmins {
		return s, fmt.Errorf("at most %d admins can be added", maxExtraAdmins)
	}
	sort.Strings(admins)
	s.Admins = admins

	if err := s.Genie.normalizeNumbers(); err != nil {
		return s, err
	}

	off := []string{}
	seenModel := map[string]bool{}
	for _, id := range s.Genie.DisabledModels {
		id = strings.TrimSpace(id)
		if id == "" || seenModel[id] {
			continue
		}
		if _, known := chatModels[id]; !known {
			return s, fmt.Errorf("%q is not a model", id)
		}
		seenModel[id] = true
		off = append(off, id)
	}
	sort.Strings(off)
	if len(off) >= len(chatModels) {
		return s, fmt.Errorf("at least one model has to stay switched on")
	}
	s.Genie.DisabledModels = off
	s.Genie.DefaultModel = strings.TrimSpace(s.Genie.DefaultModel)
	if d := s.Genie.DefaultModel; d != "" {
		if _, known := chatModels[d]; !known {
			return s, fmt.Errorf("%q is not a model", d)
		}
		if seenModel[d] {
			return s, fmt.Errorf("the default model %s is switched off", chatModels[d].Name)
		}
	}
	return s, nil
}

// LanguageDisabled reports whether an admin switched a language off.
func (s SiteSettings) LanguageDisabled(lang string) bool {
	for _, l := range s.DisabledLanguages {
		if l == lang {
			return true
		}
	}
	return false
}

var (
	settingsMu     sync.Mutex
	siteSettings   SiteSettings
	settingsLoaded bool
)

// GetSiteSettings returns the current settings. With the file store it reads
// SETTINGS_FILE once (a missing or invalid file means the defaults). With
// MongoDB (settings_sync.go) it returns the copy the sync loop keeps, the
// defaults until the first read of the database has worked.
func GetSiteSettings() SiteSettings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsBackend != nil {
		return siteSettings
	}
	if !settingsLoaded {
		data, _, found, err := fileSettingsStore{}.Load()
		if err == nil && found {
			var s SiteSettings
			if err := json.Unmarshal(data, &s); err != nil {
				log.Println("settings: ignoring invalid ", SETTINGS_FILE, ": ", err)
			} else if s, err = s.normalize(); err != nil {
				log.Println("settings: ignoring invalid ", SETTINGS_FILE, ": ", err)
			} else {
				siteSettings = s
			}
		}
		settingsLoaded = true
		utils.SetGenieRates(siteSettings.Genie.GuestPerMinute, siteSettings.Genie.UserPerMinute)
		keys, admins := siteSettings.Secrets, siteSettings.Admins
		settingsMu.Unlock()
		applyKeyOverrides(keys)
		utils.SetExtraAdmins(admins)
		settingsMu.Lock()
	}
	return siteSettings
}

// SaveSiteSettings stores the settings (in MongoDB when that is the store, else
// in SETTINGS_FILE) and makes them current. With MongoDB, version is the
// version the admin's form was loaded from: a store that moved on since then
// answers errSettingsConflict. Pass -1 to skip that check.
func SaveSiteSettings(s SiteSettings, version ...int64) error {
	s, err := s.normalize()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	settingsMu.Lock()
	backend, base, synced := settingsBackend, settingsVersion, settingsSynced
	settingsMu.Unlock()
	if backend == nil {
		if _, err := (fileSettingsStore{}).Save(data, 0); err != nil {
			return err
		}
		applySettings(s, 0, false)
		return nil
	}
	if !synced {
		return errSettingsUnavailable
	}
	if len(version) > 0 && version[0] >= 0 && version[0] != base {
		go syncSettingsOnce() // catch up, so that the next try sees the new version
		return errSettingsConflict
	}
	newVersion, err := backend.Save(data, base)
	if err == errSettingsConflict {
		go syncSettingsOnce()
		return err
	}
	if err != nil {
		log.Println("settings: saving to ", backend.Name(), " failed: ", err)
		return errSettingsUnavailable
	}
	applySettings(s, newVersion, true)
	return nil
}

// publicSettings is what every visitor may read: what the page needs to
// draw itself, nothing about limits.
type publicSettings struct {
	ColorOfTheDay     bool         `json:"colorOfTheDay"`
	Announcement      Announcement `json:"announcement"`
	Maintenance       Maintenance  `json:"maintenance"`
	DisabledLanguages []string     `json:"disabledLanguages"`
	GenieDisabled     bool         `json:"genieDisabled"`
	// the models that are switched off, and the one visitors start with (empty:
	// the built-in choice); model-choice.js reads them
	DisabledModels []string `json:"disabledModels"`
	DefaultModel   string   `json:"defaultModel"`
	// what Genie reads from the page and keeps (the effective numbers)
	GenieContext genieContext `json:"genieContext"`
	// agent mode: whether the panel offers it (to signed-in users; the server
	// decides), whether on the practice page, and the steps of a task
	AgentEnabled    bool `json:"agentEnabled"`
	AgentOnPractice bool `json:"agentOnPractice"`
	AgentMaxSteps   int  `json:"agentMaxSteps"`
}

type genieContext struct {
	EditorChars   int `json:"editorChars"`
	TerminalChars int `json:"terminalChars"`
	TerminalLines int `json:"terminalLines"`
	History       int `json:"history"`
}

func orDefault(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}

func (g GenieSettings) context() genieContext {
	return genieContext{
		EditorChars:   orDefault(g.ContextEditorChars, defaultContextEditorChars),
		TerminalChars: orDefault(g.ContextTerminalChars, defaultContextTerminalChars),
		TerminalLines: orDefault(g.ContextTerminalLines, defaultContextTerminalLines),
		History:       orDefault(g.HistoryMessages, defaultHistoryMessages),
	}
}

// the limits of the numbers an admin may set
const (
	minEditorChars, maxEditorChars     = 1000, 50000
	minTerminalChars, maxTerminalChars = 200, 20000
	minTerminalLines, maxTerminalLines = 1, 200
	minHistory, maxHistory             = 6, 50
	minAnswerCap, maxAnswerCap         = 500, 16000
	minORTimeout, maxORTimeout         = 10, 90
	minAgentTasks, maxAgentTasks       = 1, maxAgentTasksPerHour
	minAgentSteps, maxAgentStepsSet    = 1, maxAgentSteps
)

// normalizeNumbers checks the numbers and the hosts of the Genie settings.
func (g *GenieSettings) normalizeNumbers() error {
	for _, n := range []struct {
		name     string
		v        int
		min, max int
	}{
		{"The editor context", g.ContextEditorChars, minEditorChars, maxEditorChars},
		{"The terminal context (characters)", g.ContextTerminalChars, minTerminalChars, maxTerminalChars},
		{"The terminal context (lines)", g.ContextTerminalLines, minTerminalLines, maxTerminalLines},
		{"The conversation length", g.HistoryMessages, minHistory, maxHistory},
		{"The OpenRouter time limit", g.OpenRouterTimeoutSec, minORTimeout, maxORTimeout},
		{"The agent tasks per hour", g.AgentTasksPerHour, minAgentTasks, maxAgentTasks},
		{"The agent steps per task", g.AgentMaxSteps, minAgentSteps, maxAgentStepsSet},
	} {
		if n.v != 0 && (n.v < n.min || n.v > n.max) {
			return fmt.Errorf("%s must be between %d and %d (or empty for the built-in value)", n.name, n.min, n.max)
		}
	}
	caps := map[string]int{}
	for id, n := range g.AnswerCaps {
		if _, known := chatModels[id]; !known {
			return fmt.Errorf("%q is not a model", id)
		}
		if n == 0 {
			continue // the built-in cap
		}
		if n < minAnswerCap || n > maxAnswerCap {
			return fmt.Errorf("The answer size of %s must be between %d and %d tokens (or empty)", chatModels[id].Name, minAnswerCap, maxAnswerCap)
		}
		caps[id] = n
	}
	if len(caps) == 0 {
		caps = nil
	}
	g.AnswerCaps = caps
	hosts := []string{}
	seen := map[string]bool{}
	for _, h := range g.OpenRouterHosts {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		if _, ok := openRouterHostNames[h]; !ok {
			return fmt.Errorf("%q is not a host OpenRouter may use", h)
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		hosts = nil
	}
	g.OpenRouterHosts = hosts
	return nil
}

func (s SiteSettings) public() publicSettings {
	langs := s.DisabledLanguages
	if langs == nil {
		langs = []string{}
	}
	models := s.Genie.DisabledModels
	if models == nil {
		models = []string{}
	}
	return publicSettings{
		ColorOfTheDay:     s.ColorOfTheDay,
		Announcement:      s.Announcement,
		Maintenance:       s.Maintenance,
		DisabledLanguages: langs,
		GenieDisabled:     s.Genie.Disabled,
		DisabledModels:    models,
		DefaultModel:      s.Genie.DefaultModel,
		GenieContext:      s.Genie.context(),
		AgentEnabled:      !s.Genie.AgentDisabled && !s.Genie.Disabled,
		AgentOnPractice:   s.Genie.AgentOnPractice,
		AgentMaxSteps:     s.Genie.agentMaxSteps(),
	}
}

// handleSettingsJS serves the public settings as a script that defines
// window.site_settings. Pages load it before js/preprocessing.js.
func handleSettingsJS(rw http.ResponseWriter, req *http.Request) {
	data, err := json.Marshal(GetSiteSettings().public())
	if err != nil {
		data = []byte("{}")
	}
	rw.Header().Set("Content-Type", "application/javascript")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Write([]byte("var site_settings = " + string(data) + ";"))
}

// languageChoice is a language of the picker, for the list of languages an
// admin can switch off.
type languageChoice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// extraLanguages are picker entries that have no page of their own.
var extraLanguages = []languageChoice{
	{"yaegi", "Go (yaegi)"},
	{"python2.7", "Python 2.7"},
	{"ipython3", "IPython 3"},
}

// languageChoices lists the picker's languages by name.
func languageChoices() []languageChoice {
	seen := map[string]bool{}
	out := []languageChoice{}
	for _, p := range langPages {
		if !seen[p.Repl] {
			seen[p.Repl] = true
			out = append(out, languageChoice{p.Repl, p.Name})
		}
	}
	for _, l := range extraLanguages {
		if !seen[l.Value] {
			seen[l.Value] = true
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// settingsReply is the settings with the choices the form needs.
type settingsReply struct {
	SiteSettings
	Languages []languageChoice `json:"languages"`
	// Models are the models the form offers, with whether the server can call
	// each (its key is set).
	Models []modelInfo `json:"models"`
	// Defaults are the built-in values of the numbers, for the form to show
	// where a field is empty.
	Defaults genieDefaults `json:"defaults"`
	// Store is "file" or "mongodb"; Version is the stored version the form was
	// filled from (MongoDB only). The dashboard sends it back with a save, so
	// that a form that is out of date is refused instead of overwriting.
	Store   string `json:"store"`
	Version int64  `json:"version"`
}

func reply(s SiteSettings) settingsReply {
	if s.DisabledLanguages == nil {
		s.DisabledLanguages = []string{}
	}
	settingsMu.Lock()
	store, version := "file", int64(0)
	if settingsBackend != nil {
		store, version = settingsBackend.Name(), settingsVersion
	}
	settingsMu.Unlock()
	if s.Genie.DisabledModels == nil {
		s.Genie.DisabledModels = []string{}
	}
	s.Secrets = nil
	return settingsReply{SiteSettings: s, Languages: languageChoices(), Models: modelInfos(), Defaults: currentGenieDefaults(), Store: store, Version: version}
}

// handleAdminSettings reads and saves the settings. It runs behind adminAPI,
// so a change always carries the dashboard's header.
func (server *Server) handleAdminSettings(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		adminJSON(rw, http.StatusOK, reply(GetSiteSettings()))
	case http.MethodPost, http.MethodPut:
		req.Body = http.MaxBytesReader(rw, req.Body, 64*1024)
		var posted struct {
			SiteSettings
			Version *int64 `json:"version"`
		}
		if err := json.NewDecoder(req.Body).Decode(&posted); err != nil {
			adminError(rw, http.StatusBadRequest, "The settings were not valid JSON.")
			return
		}
		s := posted.SiteSettings
		base := int64(-1) // a form that does not say where it came from skips the check
		if posted.Version != nil {
			base = *posted.Version
		}
		before := GetSiteSettings()
		s.Secrets = before.Secrets // the form cannot set keys; /admin/keys does
		s.Admins = before.Admins   // nor admins; /admin/admins does, for owners
		if err := SaveSiteSettings(s, base); err != nil {
			if _, bad := s.normalize(); bad != nil {
				adminError(rw, http.StatusBadRequest, bad.Error())
				return
			}
			if err == errSettingsConflict {
				adminError(rw, http.StatusConflict, "Someone else changed the settings since you opened this page. Reload to see their changes, then make yours again.")
				return
			}
			if err == errSettingsUnavailable {
				adminError(rw, http.StatusServiceUnavailable, "The settings store is not reachable right now, so nothing was saved. Try again in a moment.")
				return
			}
			log.Println("saving site settings failed: ", err)
			adminError(rw, http.StatusInternalServerError, "Could not save the settings. Please try again.")
			return
		}
		after := GetSiteSettings()
		for _, change := range settingsChanges(before, after) {
			server.audit(req, "settings", change)
		}
		adminJSON(rw, http.StatusOK, reply(after))
	default:
		rw.Header().Set("Allow", "GET, POST")
		adminError(rw, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}

// settingsChanges describes what differs, one line each, for the audit log.
// The announcement text is not copied: the log says that it changed.
func settingsChanges(a, b SiteSettings) []string {
	var out []string
	onoff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	if a.ColorOfTheDay != b.ColorOfTheDay {
		out = append(out, "colour of the day "+onoff(b.ColorOfTheDay))
	}
	if a.Announcement != b.Announcement {
		if b.Announcement.Text == "" {
			out = append(out, "announcement removed")
		} else {
			out = append(out, "announcement changed ("+b.Announcement.Level+")")
		}
	}
	if a.Maintenance.Enabled != b.Maintenance.Enabled {
		out = append(out, "maintenance mode "+onoff(b.Maintenance.Enabled))
	} else if a.Maintenance.Message != b.Maintenance.Message {
		out = append(out, "maintenance message changed")
	}
	if strings.Join(a.DisabledLanguages, ",") != strings.Join(b.DisabledLanguages, ",") {
		out = append(out, "languages switched off: "+strings.Join(b.DisabledLanguages, ", "))
	}
	if a.Genie.Disabled != b.Genie.Disabled {
		out = append(out, "Genie "+map[bool]string{true: "switched off", false: "switched on"}[b.Genie.Disabled])
	}
	for _, id := range chatModelOrder {
		if a.Genie.ModelDisabled(id) != b.Genie.ModelDisabled(id) {
			out = append(out, "model "+chatModels[id].Name+" "+map[bool]string{true: "switched off", false: "switched on"}[b.Genie.ModelDisabled(id)])
		}
	}
	if a.Genie.DefaultModel != b.Genie.DefaultModel {
		name := "built-in"
		if m, ok := chatModels[b.Genie.DefaultModel]; ok {
			name = m.Name
		}
		out = append(out, "default model: "+name)
	}
	if ga, gb := a.Genie, b.Genie; ga.context() != gb.context() || ga.OpenRouterTimeoutSec != gb.OpenRouterTimeoutSec ||
		fmt.Sprint(ga.AnswerCaps) != fmt.Sprint(gb.AnswerCaps) || strings.Join(ga.OpenRouterHosts, ",") != strings.Join(gb.OpenRouterHosts, ",") {
		out = append(out, "Genie limits changed (context, history, answer sizes, OpenRouter time limit or hosts)")
	}
	if a.Genie.AgentDisabled != b.Genie.AgentDisabled {
		out = append(out, "agent mode "+onoff(!b.Genie.AgentDisabled))
	}
	if a.Genie.AgentOnPractice != b.Genie.AgentOnPractice {
		out = append(out, "agent mode on the practice page "+onoff(b.Genie.AgentOnPractice))
	}
	if a.Genie.AgentTasksPerHour != b.Genie.AgentTasksPerHour || a.Genie.AgentMaxSteps != b.Genie.AgentMaxSteps {
		out = append(out, fmt.Sprintf("agent limits: %d tasks an hour, %d steps a task", b.Genie.agentTasksPerHour(), b.Genie.agentMaxSteps()))
	}
	if a.Genie.GuestPerMinute != b.Genie.GuestPerMinute || a.Genie.UserPerMinute != b.Genie.UserPerMinute {
		rate := func(v float64) string {
			if v == 0 {
				return "built-in rate"
			}
			return fmt.Sprintf("%g/min", v)
		}
		out = append(out, "Genie rates: guests "+rate(b.Genie.GuestPerMinute)+", signed-in "+rate(b.Genie.UserPerMinute))
	}
	return out
}

// modelInfo is a model as the dashboard lists it.
type modelInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// KeySet is false when the server has no key for the model's provider, so
	// switching it on has no effect until one is set.
	KeySet bool `json:"keySet"`
}

func modelInfos() []modelInfo {
	out := make([]modelInfo, 0, len(chatModelOrder))
	for _, id := range chatModelOrder {
		m := chatModels[id]
		key := utils.OpenAIKey()
		if m.Provider == providerOpenRouter {
			key = utils.OpenRouterKey()
		}
		out = append(out, modelInfo{ID: m.ID, Name: m.Name, Provider: m.Provider, KeySet: key != ""})
	}
	return out
}

// genieDefaults are what an empty number means.
type genieDefaults struct {
	ContextEditorChars   int               `json:"contextEditorChars"`
	ContextTerminalChars int               `json:"contextTerminalChars"`
	ContextTerminalLines int               `json:"contextTerminalLines"`
	HistoryMessages      int               `json:"historyMessages"`
	AnswerCaps           map[string]int    `json:"answerCaps"`
	OpenRouterTimeoutSec int               `json:"openRouterTimeoutSec"`
	AgentTasksPerHour    int               `json:"agentTasksPerHour"`
	AgentMaxSteps        int               `json:"agentMaxSteps"`
	OpenRouterHosts      []hostChoice      `json:"openRouterHosts"` // every host that may be chosen, in the built-in order
	Limits               map[string][2]int `json:"limits"`          // the allowed range of each number
}

type hostChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func currentGenieDefaults() genieDefaults {
	caps := map[string]int{}
	for _, id := range chatModelOrder {
		m := chatModels[id]
		if m.Reasoning {
			caps[id] = maxCompletionTokensCap
		} else {
			caps[id] = m.MaxTokensCap
		}
	}
	hosts := []hostChoice{}
	for _, h := range openRouterHosts {
		hosts = append(hosts, hostChoice{ID: h, Name: openRouterHostNames[h]})
	}
	return genieDefaults{
		ContextEditorChars: defaultContextEditorChars, ContextTerminalChars: defaultContextTerminalChars,
		ContextTerminalLines: defaultContextTerminalLines, HistoryMessages: defaultHistoryMessages,
		AnswerCaps: caps, OpenRouterTimeoutSec: defaultOpenRouterTimeoutSec, OpenRouterHosts: hosts,
		AgentTasksPerHour: defaultAgentTasksPerHour, AgentMaxSteps: defaultAgentMaxSteps,
		Limits: map[string][2]int{
			"contextEditorChars": {minEditorChars, maxEditorChars}, "contextTerminalChars": {minTerminalChars, maxTerminalChars},
			"contextTerminalLines": {minTerminalLines, maxTerminalLines}, "historyMessages": {minHistory, maxHistory},
			"answerCap": {minAnswerCap, maxAnswerCap}, "openRouterTimeoutSec": {minORTimeout, maxORTimeout},
			"agentTasksPerHour": {minAgentTasks, maxAgentTasks}, "agentMaxSteps": {minAgentSteps, maxAgentStepsSet},
		},
	}
}
