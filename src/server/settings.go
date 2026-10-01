package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"

	"utils"
)

// SETTINGS_FILE holds site-wide switches that an admin changes at /admin.
const SETTINGS_FILE = utils.GOTTY_PATH + "/settings.json"

// SiteSettings are site-wide switches. Every field defaults to off.
type SiteSettings struct {
	// ColorOfTheDay picks a different accent colour each day
	// (js/src/preprocessing.js). Off keeps the brand coral.
	ColorOfTheDay bool `json:"colorOfTheDay"`
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
			} else {
				siteSettings = s
			}
		}
		settingsLoaded = true
	}
	return siteSettings
}

// SaveSiteSettings writes the settings to SETTINGS_FILE and makes them current.
func SaveSiteSettings(s SiteSettings) error {
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
	return nil
}

// handleSettingsJS serves the public settings as a script that defines
// window.site_settings. Pages load it before js/preprocessing.js.
func handleSettingsJS(rw http.ResponseWriter, req *http.Request) {
	data, err := json.Marshal(GetSiteSettings())
	if err != nil {
		data = []byte("{}")
	}
	rw.Header().Set("Content-Type", "application/javascript")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Write([]byte("var site_settings = " + string(data) + ";"))
}

var AdminSettings_Template = `<article class="doc__content">
  <h2>Site settings</h2>
  {{if .Saved}}<p><strong>Settings saved.</strong> Visitors see the change on their next page load.</p>{{end}}
  <form method="post" action="admin/settings">
    <p><label><input type="checkbox" name="colorOfTheDay" value="on"{{if .Settings.ColorOfTheDay}} checked{{end}}> Colour of the day</label></p>
    <p>Uses a different accent colour each day. When off, the site always uses the brand coral.</p>
    <p><button type="submit" class="button--primary">Save settings</button></p>
  </form>
</article>`

var adminSettingsTemplate = template.Must(template.New("admin").Parse(AdminSettings_Template))

// handleAdminPage renders the settings form. It is registered behind wrapAdmin.
func handleAdminPage(rw http.ResponseWriter, req *http.Request) {
	vars := map[string]interface{}{
		"Settings": GetSiteSettings(),
		"Saved":    req.URL.Query().Get("saved") == "1",
	}
	buf := new(bytes.Buffer)
	if err := adminSettingsTemplate.Execute(buf, vars); err != nil {
		log.Println("admin page template failed: ", err)
		errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	commonHandler(rw, req, "Admin settings", buf.String(), http.StatusOK)
}

// handleAdminSettings saves the settings form. Admin only.
func handleAdminSettings(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !IsUserAdmin(rw, req) {
		errorHandler(rw, req, "Unauthorized Access!! Please Sign in again as Admin.", http.StatusUnauthorized)
		return
	}
	req.ParseForm()
	s := GetSiteSettings()
	s.ColorOfTheDay = req.FormValue("colorOfTheDay") == "on"
	if err := SaveSiteSettings(s); err != nil {
		log.Println("saving site settings failed: ", err)
		errorHandler(rw, req, "Could not save settings. Please try again.", http.StatusInternalServerError)
		return
	}
	log.Println("site settings updated: ", s)
	http.Redirect(rw, req, "../admin?saved=1", http.StatusSeeOther)
}
