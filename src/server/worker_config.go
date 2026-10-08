package server

import (
	"log"
	"net/http"
	"strings"

	"tunnel"
	"utils"
)

// The settings a worker follows. The gateway applies the site rules (maintenance
// mode, languages that are switched off) to everybody it forwards. A worker
// that people can also reach on its own port must apply them to those people
// too, and show them the same announcement. So the gateway hands its rules to
// every worker when it registers, and again in a heartbeat reply whenever they
// change (tunnel.WorkerConfig). A worker keeps the last it received in memory
// and does not read settings.json or a database for them.

// gatewayWorkerConfig is what the gateway hands to workers: the site rules of
// its settings, with a revision made from their content, so that the same
// rules have the same revision on every gateway and after a restart. A worker
// gets its own languages on top of that (gatewayWorkerConfigFor).
func (server *Server) gatewayWorkerConfig() *tunnel.WorkerConfig {
	return server.workerConfigWith(GetSiteSettings().DisabledLanguages)
}

// applyWorkerConfig makes the gateway's site rules the worker's settings. The
// worker's other settings (the Genie ones, the keys, the admins) are not
// replaced by anything: a worker has none of them.
func applyWorkerConfig(c *tunnel.WorkerConfig) {
	s := SiteSettings{
		ColorOfTheDay:     c.ColorOfTheDay,
		Announcement:      Announcement{Text: c.AnnouncementText, Level: c.AnnouncementLevel},
		Maintenance:       Maintenance{Enabled: c.Maintenance, Message: c.MaintenanceMessage},
		DisabledLanguages: c.DisabledLanguages,
	}
	if s.Announcement.Level == "" {
		s.Announcement.Level = "info"
	}
	settingsMu.Lock()
	siteSettings, settingsLoaded = s, true
	settingsMu.Unlock()
	utils.SetGenieRates(0, 0)
	log.Printf("worker: site rules from the gateway applied (revision %d: maintenance %v, %d languages off)",
		c.Revision, c.Maintenance, len(c.DisabledLanguages))
}

// siteBlockReason says why a new terminal of the language is refused for
// everybody but admins: maintenance mode, or the language is switched off.
// "" means it is not refused.
func siteBlockReason(settings SiteSettings, lang string) string {
	switch {
	case settings.Maintenance.Enabled:
		if settings.Maintenance.Message != "" {
			return settings.Maintenance.Message
		}
		return "The site is down for maintenance. Please try again soon."
	case settings.LanguageDisabled(lang):
		return "This language is switched off for now. Please pick another one."
	}
	return ""
}

// wrapWorkerControls applies the site rules to the people who open a worker's
// own port. What the gateway forwards (a trusted request) was checked by the
// gateway, which knows who is an admin; a worker does not, so it never lets an
// admin through: an admin who needs a terminal during maintenance uses the
// gateway.
func (server *Server) wrapWorkerControls(next http.Handler, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTrusted(r) || !isWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		lang, ok := server.terminalLanguage(strings.TrimPrefix(r.URL.Path, prefix))
		if ok {
			if reason := siteBlockReason(GetSiteSettings(), lang); reason != "" {
				server.closeWithNotice(w, r, reason)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
