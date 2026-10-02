package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
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
)

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

// GetSiteSettings returns the current settings, reading SETTINGS_FILE once.
// A missing or invalid file means the defaults.
func GetSiteSettings() SiteSettings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if !settingsLoaded {
		data, err := ioutil.ReadFile(SETTINGS_FILE)
		if err == nil {
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
	}
	return siteSettings
}

// SaveSiteSettings writes the settings to SETTINGS_FILE and makes them current.
func SaveSiteSettings(s SiteSettings) error {
	s, err := s.normalize()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(utils.GOTTY_PATH, 0755); err != nil {
		return err
	}
	tmp := SETTINGS_FILE + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, SETTINGS_FILE); err != nil {
		return err
	}
	settingsMu.Lock()
	siteSettings = s
	settingsLoaded = true
	settingsMu.Unlock()
	utils.SetGenieRates(s.Genie.GuestPerMinute, s.Genie.UserPerMinute)
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
}

func (s SiteSettings) public() publicSettings {
	langs := s.DisabledLanguages
	if langs == nil {
		langs = []string{}
	}
	return publicSettings{
		ColorOfTheDay:     s.ColorOfTheDay,
		Announcement:      s.Announcement,
		Maintenance:       s.Maintenance,
		DisabledLanguages: langs,
		GenieDisabled:     s.Genie.Disabled,
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
}

func reply(s SiteSettings) settingsReply {
	if s.DisabledLanguages == nil {
		s.DisabledLanguages = []string{}
	}
	return settingsReply{SiteSettings: s, Languages: languageChoices()}
}

// handleAdminSettings reads and saves the settings. It runs behind adminAPI,
// so a change always carries the dashboard's header.
func (server *Server) handleAdminSettings(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		adminJSON(rw, http.StatusOK, reply(GetSiteSettings()))
	case http.MethodPost, http.MethodPut:
		req.Body = http.MaxBytesReader(rw, req.Body, 64*1024)
		var s SiteSettings
		if err := json.NewDecoder(req.Body).Decode(&s); err != nil {
			adminError(rw, http.StatusBadRequest, "The settings were not valid JSON.")
			return
		}
		before := GetSiteSettings()
		if err := SaveSiteSettings(s); err != nil {
			if _, bad := s.normalize(); bad != nil {
				adminError(rw, http.StatusBadRequest, bad.Error())
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
