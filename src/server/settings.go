package server

import (
	"appstore"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
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
	// NodeLanguages are an admin's choices for single nodes (the gateway itself,
	// "local", and each worker by id): languages switched off on one node only,
	// and languages switched on that the node did not declare. See
	// node_languages.go. The Settings page does not send them; /admin/workers
	// changes them.
	NodeLanguages map[string]NodeLanguages `json:"nodeLanguages,omitempty"`
	// NodeWeights are the shares of new sessions an admin set for single nodes
	// from the dashboard, instead of the ones the nodes started with. See
	// node_weights.go. The Settings page does not send them; /admin/workers
	// changes them.
	NodeWeights map[string]int `json:"nodeWeights,omitempty"`
	// DBCapacityMB is how much room the remote database has, in megabytes (an
	// Atlas free cluster has 512), for the live map's "used of total". 0: not
	// set, and the map uses the disk size MongoDB reports, if it does.
	DBCapacityMB int `json:"dbCapacityMB"`
	// MaxEditorFileMB is the largest file the editor opens, in megabytes. The editor is one document shared through
	// Firebase, which refuses a write over 16 MB, so 16 is also the most; 0 means that built-in 16. A bigger text file
	// opens read-only in the viewer (page/24-large-file-viewer.js) instead.
	MaxEditorFileMB int `json:"maxEditorFileMB"`
	// SnapshotIntervalMinutes is how often the workspaces of signed-in users that changed are snapshotted to the object
	// store (package homesnap), from 1 to 60; 0 means the built-in 5. Only the newest 3 snapshots of a home are kept.
	SnapshotIntervalMinutes int `json:"snapshotIntervalMinutes"`
	// StoreSafeList are the apps (conda-forge package names) a signed-in user may install in the App Store without asking (LLD 18);
	// empty means the built-in list. StoreLimitMB is the most the store may use on a server, 0 for the built-in 2048. StoreOff
	// switches the App Store off for everyone.
	StoreSafeList []string `json:"storeSafeList,omitempty"`
	StoreLimitMB  int      `json:"storeLimitMB"`
	StoreOff      bool     `json:"storeOff"`
	// EgressOn lets the REPLs of signed-in users (not guests, not admins) reach public internet addresses through a proxy of the server
	// (LLD 16 "Egress"): pip, npm, curl and git over https. It is handed to the workers, so it holds on every node. The environment variable
	// OPENREPL_EGRESS=public does the same and stays.
	EgressOn bool `json:"egressOn"`
	// StoreReserveMB is kept free on each server for installs in progress (0 for the built-in 3072); StoreRequestsPerDay is how many apps
	// one user may ask for in a day (0 for the built-in 5). ScratchSoftMB is the size at which a terminal's scratch folder is
	// at which the caches are cleared first and then a terminal that still fills it is stopped (a soft cap: no quota, no mount; 0 for the built-in 256);
	// ScratchServerMB is the same for all the scratch folders of a server (0 for the built-in 2048).
	StoreReserveMB      int `json:"storeReserveMB,omitempty"`
	StoreRequestsPerDay int `json:"storeRequestsPerDay,omitempty"`
	ScratchSoftMB       int `json:"scratchSoftMB,omitempty"`
	ScratchServerMB     int `json:"scratchServerMB,omitempty"`
	// PreviewLinkGuestMinutes and PreviewLinkUserMinutes are how long the address of a preview (its token) works for a guest and for
	// a signed-in user, in minutes, from 10 to 1440; 0 means the built-in 60 (guest) and 180 (signed in: 3 hours). The pane asks
	// for a new one before it ends; a link opened in a tab of its own ends with it.
	PreviewLinkGuestMinutes int `json:"previewLinkGuestMinutes"`
	PreviewLinkUserMinutes  int `json:"previewLinkUserMinutes"`
	// PreviewBaseURL is an optional separate origin for preview frames, for example
	// https://preview.openrepl.com. It has no path: frames use /<token>/... there.
	// Empty keeps previews on this site's /preview/<token>/... route.
	PreviewBaseURL string `json:"previewBaseURL"`
	// ShareLinkGuestMinutes and ShareLinkUserMinutes are the longest a share link of a live session may work (the time chosen
	// in the Share popover, with the time added), in minutes, from 10 to 10080 (7 days); 0 means the built-in 60 (guest: 1
	// hour) and 10080 (signed in: 7 days, as long as a sign-in lasts).
	ShareLinkGuestMinutes int `json:"shareLinkGuestMinutes"`
	ShareLinkUserMinutes  int `json:"shareLinkUserMinutes"`
	// SessionCleanupHours is how often the server removes the shared sessions that ended, ran out or were left in the Firebase
	// Realtime Database (session_cleanup.go), from 1 to 720 hours; 0 means the built-in 24 (daily).
	SessionCleanupHours int `json:"sessionCleanupHours"`
	// SnippetMaxDays is the longest a code link (/s/<id>) may work from the day it is made, in days, from 1 to 365; 0 means the
	// built-in 30 (a month). It is the latest day the calendar offers, and the day it starts on (snippets.go).
	SnippetMaxDays int `json:"snippetMaxDays"`
	// PreviewOff switches the Preview pane off for everyone, and PreviewGuestsOff for visitors who are not signed in:
	// the pane shows pages written by visitors on this site's domain, so abuse can be stopped here without a deploy.
	PreviewOff       bool `json:"previewOff"`
	// PracticeWorkspaceOff keeps a signed-in user's practice list and solutions in the server's practice database (the way it was) and not in
	// the .practice folder of their workspace (practice_ws.go).
	PracticeWorkspaceOff bool `json:"practiceWorkspaceOff"`
	PreviewGuestsOff bool `json:"previewGuestsOff"`
	// PreviewAgentOff stops Genie's agent from acting in the Preview page (click, type, press, scroll, select, hover, go, resize, run
	// an expression, change its storage). It can still look at the page (read, console, calls). The Preview itself stays as it is.
	PreviewAgentOff bool `json:"previewAgentOff"`
	// GenieEventsOff stops the Genie log (genie_log.go): the agent's failed steps and the end of each task, kept without any text of the
	// user's. GenieEventDays is how many days it is kept, 1 to 365; 0 means 30.
	GenieEventsOff bool `json:"genieEventsOff"`
	GenieEventDays int  `json:"genieEventDays"`
	// The Genie health card (genie_health.go) flags a model whose calls fail at least GenieHealthErrPct percent (1 to 100; 0 means 20) or whose
	// slowest twentieth takes GenieHealthSlowMs milliseconds or more to start answering (500 to 120000; 0 means 8000).
	GenieHealthErrPct int `json:"genieHealthErrPct"`
	GenieHealthSlowMs int `json:"genieHealthSlowMs"`
	// Genie holds the AI assistant's switch and its rate limits.
	Genie GenieSettings `json:"genie"`
	// Admins are the accounts added in the dashboard, lower case, besides the
	// owners the environment names (OPENREPL_ADMIN_EMAILS). Only owners change
	// the list, through /admin/admins, and a form cannot.
	Admins []string `json:"admins"`
	// Secrets are the API keys an admin saved in the dashboard, encrypted
	// (settings_keys.go). No API reply carries them and a form cannot set them.
	Secrets *StoredKeys `json:"secrets,omitempty"`
	// Integrations are the servers Genie can be connected to, as an admin set them up (genie_integrations.go); IntegrationSecrets are their sealed
	// secrets, which no reply carries; IntegrationsSeeded says that the list in Go made the first record. The Settings page does not send them:
	// /admin/integrations changes them.
	Integrations       []Integration                `json:"integrations,omitempty"`
	IntegrationSecrets map[string]IntegrationSecret `json:"integrationSecrets,omitempty"`
	IntegrationsSeeded bool                         `json:"integrationsSeeded,omitempty"`
	IntegrationsFixed  int                          `json:"integrationsFixed,omitempty"` // the one-time repairs of the records that were made (genie_integrations.go: seedIntegrations)
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
	// CustomModels are the OpenRouter models an admin added, each with its own
	// switch (custom_models.go). nil means none was ever saved: the defaults are
	// listed, off. The three built-in models are not in it and cannot be removed.
	CustomModels []CustomModel `json:"customModels"`
	// What Genie is told about the page, and how much of the conversation it
	// keeps: the editor's code (characters), the terminal's recent output
	// (characters and lines) and the number of messages. 0 is the built-in value.
	// The page reads them from settings.js (the effective values).
	ContextEditorChars   int `json:"contextEditorChars"`
	ContextTerminalChars int `json:"contextTerminalChars"`
	ContextTerminalLines int `json:"contextTerminalLines"`
	HistoryMessages      int `json:"historyMessages"`
	// What the user may add to a message with Add to Genie chat: how many chips,
	// the characters of one of them and of all of them. 0 is the built-in value.
	ContextAttachItems int `json:"contextAttachItems"`
	ContextAttachChars int `json:"contextAttachChars"`
	ContextAttachTotal int `json:"contextAttachTotal"`
	// ModelPrices is what each model costs, in dollars for a million tokens sent and
	// written, by model id. A model that is not in it has no price (the cost of
	// a model whose provider reports it, OpenRouter, is the reported one). It is
	// used for the cost shown to the admin, and to users when ShowCostToUsers is on.
	ModelPrices     map[string]modelPrice `json:"modelPrices"`
	ShowCostToUsers bool                  `json:"showCostToUsers"`
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
	// AgentModels are the models that may be used in Agent mode (nil: never chosen, every model that is on may be). A model that
	// is not in the list is refused in Agent mode (agent_models.go). AgentEffort is the effort of the Agent model ("": low).
	AgentModels []string `json:"agentModels"`
	// GuestBlockedModels are the built-in models kept for signed-in users (an added model has its own tick, NoGuests).
	GuestBlockedModels []string `json:"guestBlockedModels"`
	AgentEffort string   `json:"agentEffort"`
	// AgentModel is the model Agent mode starts on, and the model that writes the practice questions ("" is the built-in choice, see
	// agentDefaultModelID); when the model in use is small the panel offers it too: the panel offers
	// to switch to it. It changes nothing by itself.
	AgentModel string `json:"agentModel"`
	// FeaturedModels are the models the picker lists first ("Your models") for a visitor who has
	// not used or pinned any yet; the rest are under "Other models". Empty: the default model and
	// the first others. Built-in or added models, at most maxFeaturedModels.
	FeaturedModels []string `json:"featuredModels"`

	// Agent mode (agent.go): Genie works on a task in steps, in the editor, once
	// the user allowed each kind of action. Signed-in users have it, unless an
	// admin sets AgentDisabled; it is off on the practice page unless
	// AgentOnPractice is set. A user may start AgentTasksPerHour tasks an hour
	// (0: the built-in 20), and a task has AgentMaxSteps steps (0: the built-in
	// 8, at most 8).
	AgentDisabled     bool `json:"agentDisabled"`
	AgentOnPractice   bool `json:"agentOnPractice"`
	AgentTasksPerHour int  `json:"agentTasksPerHour"`
	// AgentAutoOff keeps the card for every kind of action: no "Allow everything for this session", and the editor's changes are
	// always reviewed. AgentRiskyOff takes away only the level that lets the user allow riskier things (risky terminal lines,
	// deleting, renaming and moving, closing tabs) for a while. Allowing one kind for the session stays.
	AgentAutoOff  bool `json:"agentAutoOff"`
	AgentRiskyOff bool `json:"agentRiskyOff"`
	// EscalationModels are the models, in order, that a task that is stuck may be tried again with (the user chooses; the page offers
	// the first the user may use, and credits count as for any answer). Empty: the stuck card offers only a hint and Stop.
	EscalationModels []string `json:"escalationModels"`
	// Auto model choice (the Auto entry of the model picker, chat-widget/src/model-router.ts): AutoOn offers it; AutoSmall answers plain
	// questions in Chat, AutoStrong answers what asks for a change, a message with files attached and every Agent task. AutoDefault says who
	// starts on Auto: "new" (people who have not picked a model; the default), "all" (everybody who has not chosen otherwise) or "none".
	AutoOn      bool   `json:"autoOn"`
	AutoSmall   string `json:"autoSmall"`
	AutoStrong  string `json:"autoStrong"`
	AutoDefault string `json:"autoDefault"`
	// AgentShellOff takes away the agent's "bash" action: the server refuses a command (agent_shell.go).
	AgentShellOff bool `json:"agentShellOff"`
	AgentMaxSteps int  `json:"agentMaxSteps"`
	// AgentNativeTools gives an OpenAI model the agent's answer as one forced function call instead of JSON in text (agent_tools.go).
	AgentNativeTools bool `json:"agentNativeTools"`
	// NativeToolModels are the models that take it: nil is "never chosen" (the OpenAI models), a list is the admin's choice.
	NativeToolModels []string `json:"nativeToolModels"`
	// StreamAnswers sends an answer to the page as it is written (chat answers, and the words of an agent step): off until an admin turns it on.
	StreamAnswers bool `json:"streamAnswers"`

	// Chat history (genie_chats.go): each chat of the Genie panel is saved in its owner's workspace, in .genie/chats. ChatHistoryOff
	// stops it on the site; ChatHistoryNoGuests keeps it to signed-in users. The most chats a workspace keeps (0: 50), the most open
	// tabs (0: 8), the most size of one chat in KB (0: 200) and the days a chat that is not open is kept (0: for ever).
	ChatHistoryOff      bool `json:"chatHistoryOff"`
	ChatHistoryNoGuests bool `json:"chatHistoryNoGuests"`
	ChatHistoryChats    int  `json:"chatHistoryChats"`
	ChatHistoryTabs     int  `json:"chatHistoryTabs"`
	ChatHistoryKB       int  `json:"chatHistoryKB"`
	ChatHistoryDays     int  `json:"chatHistoryDays"`

	// Skills and plugins (genie_ext.go): ExtensionsOff switches both off; PluginsFromLinks lets a signed-in user install a plugin from a GitHub or
	// .zip link (off: the catalogue only); SitePlugins are catalogue plugins that are on for everyone.
	// Connections (MCP, genie_conn.go): ConnectionsOff switches them off; ConnectionsNoApproval lets a user connect what a plugin from a link asks for
	// without an admin approving it (off: a request, genie_conn_requests.go); ConnectionHosts, when not empty, are the only hosts a connection from a
	// link may talk to.
	ConnectionsOff        bool     `json:"connectionsOff"`
	ConnectionsNoApproval bool     `json:"connectionsNoApproval"`
	ConnectionHosts       []string `json:"connectionHosts"`
	ExtensionsOff    bool     `json:"extensionsOff"`
	PluginsFromLinks bool     `json:"pluginsFromLinks"`
	SitePlugins      []string `json:"sitePlugins"`

	// A long chat is summarized by the model in use (one more request, counted like any other) once the older turns pass
	// SummaryTokens tokens (0: 6000); SummaryOff stops it. The editor and terminal text is not counted: it is sent as it is.
	// The summary is kept in the chat file and sent with the last turns.
	SummaryOff    bool `json:"summaryOff"`
	SummaryTokens int  `json:"summaryTokens"`

	// Usage record and credits (meter.go). Every step of a task and every chat
	// answer of a signed-in user is recorded with its credits (counts only, no
	// content) unless MeterOff. Nothing is charged or limited until CreditsOn:
	// then the default plan counts: PlanCredits a month (0: no monthly cap) and
	// PlanBudget for one task (0: no budget; the steps of a task stay at
	// AgentMaxSteps either way). CreditWeights is the credits a model's
	// thousand tokens cost, by model id; a model without one costs what its
	// ModelPrices say (a dollar for a million tokens is a credit for a
	// thousand: one credit is a tenth of a cent), else what the provider reported.
	MeterOff      bool                  `json:"meterOff"`
	CreditsOn     bool                  `json:"creditsOn"`
	PlanName      string                `json:"planName"`
	PlanCredits   float64               `json:"planCredits"`
	PlanBudget    float64               `json:"planBudget"`
	CreditWeights map[string]modelPrice `json:"creditWeights"`
}

// ModelDisabled reports whether an admin switched the model off (a model an admin
// added is off until its switch is on).
func (g GenieSettings) ModelDisabled(id string) bool {
	if _, builtin := chatModels[id]; !builtin {
		for _, c := range g.customList() {
			if c.ID == id {
				return !c.Enabled
			}
		}
		return false
	}
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
	if s.MaxEditorFileMB != 0 && (s.MaxEditorFileMB < minEditorFileMB || s.MaxEditorFileMB > maxEditorFileMB) {
		return s, fmt.Errorf("the largest file the editor opens must be between %d and %d MB (or empty)", minEditorFileMB, maxEditorFileMB)
	}
	if s.SnapshotIntervalMinutes != 0 && (s.SnapshotIntervalMinutes < minSnapshotMinutes || s.SnapshotIntervalMinutes > maxSnapshotMinutes) {
		return s, fmt.Errorf("the time between workspace snapshots must be between %d and %d minutes (or empty)", minSnapshotMinutes, maxSnapshotMinutes)
	}
	for name, m := range map[string]int{"guests": s.PreviewLinkGuestMinutes, "signed-in users": s.PreviewLinkUserMinutes} {
		if m != 0 && (m < minPreviewLinkMinutes || m > maxPreviewLinkMinutes) {
			return s, fmt.Errorf("the time a preview link works for %s must be between %d and %d minutes (or empty)", name, minPreviewLinkMinutes, maxPreviewLinkMinutes)
		}
	}
	base, err := cleanPreviewBaseURL(s.PreviewBaseURL)
	if err != nil {
		return s, err
	}
	s.PreviewBaseURL = base
	if base != "" {
		pubRaw := strings.TrimSpace(os.Getenv("OPENREPL_PUBLIC_URL"))
		if pubRaw != "" && !strings.Contains(pubRaw, "://") {
			pubRaw = defaultScheme(pubRaw) + pubRaw // a bare host name, as with the preview base URL
		}
		if pub, perr := url.Parse(pubRaw); perr == nil && pub.Hostname() != "" {
			if bu, _ := parsePreviewBase(base); bu != nil && strings.EqualFold(bu.Hostname(), pub.Hostname()) {
				return s, fmt.Errorf("the preview base URL must not have the same host name as this site (OPENREPL_PUBLIC_URL); use a separate host name")
			}
		}
	}
	if s.StoreLimitMB != 0 && (s.StoreLimitMB < 100 || s.StoreLimitMB > 1048576) {
		return s, fmt.Errorf("the size of the App Store must be between 100 and 1048576 MB (or empty)")
	}
	if s.StoreReserveMB < 0 || s.StoreReserveMB > 102400 || s.StoreRequestsPerDay < 0 || s.StoreRequestsPerDay > 1000 ||
		s.ScratchSoftMB < 0 || s.ScratchSoftMB > 102400 || s.ScratchServerMB < 0 || s.ScratchServerMB > 1048576 {
		return s, fmt.Errorf("the reserve, the requests a day, the scratch size or the scratch time is out of range")
	}
	if len(s.StoreSafeList) > 300 {
		return s, fmt.Errorf("the safe list of the App Store holds at most 300 names")
	}
	var safe []string
	for _, n := range s.StoreSafeList {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		bare := strings.TrimPrefix(strings.TrimPrefix(n, "npm:"), "tool:")
		if !appstore.ValidName(bare) {
			return s, fmt.Errorf("%q is not a package name (a library: flask, a tool: tool:jq, a Node library: npm:express)", n)
		}
		safe = append(safe, n)
	}
	s.StoreSafeList = safe
	if s.GenieEventDays != 0 && (s.GenieEventDays < 1 || s.GenieEventDays > 365) {
		return s, fmt.Errorf("the days the Genie log is kept must be between 1 and 365 (or empty for 30)")
	}
	if s.GenieHealthErrPct != 0 && (s.GenieHealthErrPct < 1 || s.GenieHealthErrPct > 100) {
		return s, fmt.Errorf("the share of failed calls that raises an alert must be between 1 and 100 percent (or empty for 20)")
	}
	if s.GenieHealthSlowMs != 0 && (s.GenieHealthSlowMs < 500 || s.GenieHealthSlowMs > 120000) {
		return s, fmt.Errorf("the time to start answering that raises an alert must be between 500 and 120000 milliseconds (or empty for 8000)")
	}
	if s.SnippetMaxDays != 0 && (s.SnippetMaxDays < minSnippetMaxDays || s.SnippetMaxDays > maxSnippetMaxDays) {
		return s, fmt.Errorf("the longest a code link works must be between %d and %d days (or empty)", minSnippetMaxDays, maxSnippetMaxDays)
	}
	if s.SessionCleanupHours != 0 && (s.SessionCleanupHours < minSessionCleanupHours || s.SessionCleanupHours > maxSessionCleanupHours) {
		return s, fmt.Errorf("the time between two cleanups of shared sessions must be between %d and %d hours (or empty)", minSessionCleanupHours, maxSessionCleanupHours)
	}
	for name, m := range map[string]int{"guests": s.ShareLinkGuestMinutes, "signed-in users": s.ShareLinkUserMinutes} {
		if m != 0 && (m < minShareLinkMinutes || m > maxShareLinkMinutes) {
			return s, fmt.Errorf("the longest a share link works for %s must be between %d and %d minutes (or empty)", name, minShareLinkMinutes, maxShareLinkMinutes)
		}
	}
	if s.DBCapacityMB != 0 && (s.DBCapacityMB < minDBCapacityMB || s.DBCapacityMB > maxDBCapacityMB) {
		return s, fmt.Errorf("the database capacity must be between %d and %d MB (or empty)", minDBCapacityMB, maxDBCapacityMB)
	}
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

	nodes, err := normalizeNodeLanguages(s.NodeLanguages)
	if err != nil {
		return s, err
	}
	s.NodeLanguages = nodes

	weights, err := normalizeNodeWeights(s.NodeWeights)
	if err != nil {
		return s, err
	}
	s.NodeWeights = weights

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
	s.Genie.DisabledModels = off
	blocked := []string{}
	seenBlocked := map[string]bool{}
	for _, id := range s.Genie.GuestBlockedModels {
		id = strings.TrimSpace(id)
		if id == "" || seenBlocked[id] {
			continue
		}
		if _, known := chatModels[id]; !known {
			return s, fmt.Errorf("%q is not a model", id)
		}
		seenBlocked[id] = true
		blocked = append(blocked, id)
	}
	sort.Strings(blocked)
	s.Genie.GuestBlockedModels = blocked
	custom, err := normalizeCustomModels(s.Genie.CustomModels)
	if err != nil {
		return s, err
	}
	s.Genie.CustomModels = custom
	anyOn := false
	for _, id := range s.Genie.allModelIDs() {
		if !s.Genie.ModelDisabled(id) {
			anyOn = true
		}
	}
	if !anyOn {
		return s, fmt.Errorf("at least one model has to stay switched on")
	}
	featured := []string{}
	seenFeatured := map[string]bool{}
	for _, id := range s.Genie.FeaturedModels {
		id = strings.TrimSpace(id)
		if id == "" || seenFeatured[id] {
			continue
		}
		if _, known := s.Genie.modelByID(id); !known {
			return s, fmt.Errorf("%q is not a model", id)
		}
		seenFeatured[id] = true
		featured = append(featured, id)
	}
	if len(featured) > maxFeaturedModels {
		return s, fmt.Errorf("at most %d models can start in the short list", maxFeaturedModels)
	}
	s.Genie.FeaturedModels = featured
	s.Genie.DefaultModel = strings.TrimSpace(s.Genie.DefaultModel)
	if d := s.Genie.DefaultModel; d != "" {
		m, known := s.Genie.modelByID(d)
		if !known {
			return s, fmt.Errorf("%q is not a model", d)
		}
		if s.Genie.ModelDisabled(d) {
			return s, fmt.Errorf("the default model %s is switched off", m.Name)
		}
	}
	if err := s.Genie.normalizeAgentModels(); err != nil {
		return s, err
	}
	hosts, hostsErr := normalizeConnectionHosts(s.Genie.ConnectionHosts)
	if hostsErr != nil {
		return s, hostsErr
	}
	s.Genie.ConnectionHosts = hosts
	sp, spOK := validSitePlugins(s.Genie.SitePlugins)
	if !spOK {
		return s, fmt.Errorf("a plugin that is on for everyone is not in the catalogue")
	}
	s.Genie.SitePlugins = sp
	s.Genie.AgentModel = strings.TrimSpace(s.Genie.AgentModel)
	if d := s.Genie.AgentModel; d != "" {
		m, known := s.Genie.modelByID(d)
		if !known {
			return s, fmt.Errorf("%q is not a model", d)
		}
		if s.Genie.ModelDisabled(d) {
			return s, fmt.Errorf("the Agent model %s is switched off", m.Name)
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
	// the Preview pane: off for all, or for guests (the page hides its button); Genie acting in it off
	PreviewOff       bool `json:"previewOff"`
	PreviewGuestsOff bool `json:"previewGuestsOff"`
	PreviewAgentOff  bool `json:"previewAgentOff"`
	// the separate origin of the preview frames (empty: this site's /preview/<token>/... addresses), see previewhost.go
	PreviewBaseURL string `json:"previewBaseURL"`
	// the page reports the agent's failed steps (genie_log.go) unless an admin switched that off
	GenieEventsOff bool `json:"genieEventsOff"`
	// the models a stuck task may be tried again with, in order (GenieSettings.EscalationModels)
	EscalationModels []string `json:"escalationModels"`
	// Auto model choice, when an admin switched it on and named both models (nil otherwise)
	GenieAuto *autoPublic `json:"genieAuto"`
	// the largest file the editor opens, in MB (the effective number: 16 when none is set)
	MaxEditorFileMB int `json:"maxEditorFileMB"`
	// ShareMaxGuestMinutes and ShareMaxUserMinutes are the longest a share link works (SiteSettings.ShareLinkGuestMinutes)
	ShareMaxGuestMinutes int `json:"shareMaxGuestMinutes"`
	ShareMaxUserMinutes  int `json:"shareMaxUserMinutes"`
	// SnippetMaxDays is the most days a code link may work (SiteSettings.SnippetMaxDays)
	SnippetMaxDays int `json:"snippetMaxDays"`
	// the models that are switched off, and the one visitors start with (empty:
	// the built-in choice); model-choice.js reads them
	DisabledModels []string `json:"disabledModels"`
	// the models an admin added that are on, for the picker
	CustomModels []publicCustomModel `json:"customModels"`
	DefaultModel string              `json:"defaultModel"`
	AgentModel   string              `json:"genieAgentModel"` // the model Agent mode starts on (agentDefaultModelID)
	AgentModels  []string            `json:"genieAgentModels"` // the models that may be used in Agent mode; null: every model that is on
	AgentEffort  string              `json:"genieAgentEffort"` // the effort of the Agent model
	GuestBlocked []string            `json:"genieGuestBlocked"` // built-in models kept for signed-in users
	// the models the picker starts a new visitor with (empty: its own choice)
	FeaturedModels []string `json:"featuredModels"`
	// what Genie reads from the page and keeps (the effective numbers)
	GenieContext genieContext `json:"genieContext"`
	// agent mode: whether the panel offers it (to signed-in users; the server
	// decides), whether on the practice page, and the steps of a task
	AgentEnabled    bool `json:"agentEnabled"`
	AgentAutoOff    bool `json:"agentAutoOff"`
	AgentRiskyOff   bool `json:"agentRiskyOff"`
	AgentOnPractice bool `json:"agentOnPractice"`
	AgentMaxSteps   int  `json:"agentMaxSteps"`
	// answers are sent to the page as they are written (settings.go: StreamAnswers)
	GenieStream bool `json:"genieStream"`
	// the tokens of older turns after which the page summarizes a chat (0: it does not)
	GenieSummaryTokens int `json:"genieSummaryTokens"`
	// chats are saved in the workspace, for everyone or for signed-in users only (genie_chats.go)
	ChatHistory       bool `json:"chatHistory"`
	ChatHistoryGuests bool `json:"chatHistoryGuests"`
	ChatHistoryTabs   int  `json:"chatHistoryTabs"`
}

type genieContext struct {
	EditorChars   int `json:"editorChars"`
	TerminalChars int `json:"terminalChars"`
	TerminalLines int `json:"terminalLines"`
	History       int `json:"history"`
	AttachItems   int `json:"attachItems"`
	AttachChars   int `json:"attachChars"`
	AttachTotal   int `json:"attachTotal"`
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
		AttachItems:   orDefault(g.ContextAttachItems, defaultAttachItems),
		AttachChars:   orDefault(g.ContextAttachChars, defaultAttachChars),
		AttachTotal:   orDefault(g.ContextAttachTotal, defaultAttachTotal),
	}
}

// the limits of the numbers an admin may set
const (
	minEditorChars, maxEditorChars     = 1000, 50000
	minTerminalChars, maxTerminalChars = 200, 20000
	minTerminalLines, maxTerminalLines = 1, 200
	minHistory, maxHistory             = 6, 50
	maxPricePerMillion                 = 10000
	minDBCapacityMB, maxDBCapacityMB   = 10, 100000000
	minEditorFileMB, maxEditorFileMB   = 1, 16 // 16: the most Firebase takes in one write
	minAttachItems, maxAttachItems     = 1, 20
	minAttachChars, maxAttachChars     = 500, 50000
	minAttachTotal, maxAttachTotal     = 2000, 100000
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
		{"The items added to a message", g.ContextAttachItems, minAttachItems, maxAttachItems},
		{"The characters of one added item", g.ContextAttachChars, minAttachChars, maxAttachChars},
		{"The characters of all the items added to a message", g.ContextAttachTotal, minAttachTotal, maxAttachTotal},
		{"The OpenRouter time limit", g.OpenRouterTimeoutSec, minORTimeout, maxORTimeout},
		{"The agent tasks per hour", g.AgentTasksPerHour, minAgentTasks, maxAgentTasks},
		{"The agent steps per task", g.AgentMaxSteps, minAgentSteps, maxAgentStepsSet},
		{"The tokens before a chat is summarized", g.SummaryTokens, minSummaryTokens, maxSummaryTokens},
	} {
		if n.v != 0 && (n.v < n.min || n.v > n.max) {
			return fmt.Errorf("%s must be between %d and %d (or empty for the built-in value)", n.name, n.min, n.max)
		}
	}
	if err := g.normalizeCredits(); err != nil {
		return err
	}
	if err := g.normalizeChatHistory(); err != nil {
		return err
	}
	if err := g.normalizeNativeTools(); err != nil {
		return err
	}
	if err := g.normalizeEscalation(); err != nil {
		return err
	}
	if err := g.normalizeAuto(); err != nil {
		return err
	}
	prices := map[string]modelPrice{}
	known := map[string]bool{}
	for _, id := range g.allModelIDs() {
		known[id] = true
	}
	for id, p := range g.ModelPrices {
		if !known[id] {
			return fmt.Errorf("%q is not a model", id)
		}
		if p.In < 0 || p.Out < 0 || p.In > maxPricePerMillion || p.Out > maxPricePerMillion || math.IsNaN(p.In) || math.IsNaN(p.Out) {
			return fmt.Errorf("The price of %s must be between 0 and %d dollars for a million tokens (or empty)", id, maxPricePerMillion)
		}
		if p.set() {
			prices[id] = p
		}
	}
	if len(prices) == 0 {
		prices = nil
	}
	g.ModelPrices = prices
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

// the time between two rounds of workspace snapshots, in minutes
const minSnapshotMinutes, maxSnapshotMinutes = 1, 60

// snapshotInterval is the time between two rounds of workspace snapshots in force: the one set, or 0 (the built-in 5 minutes).
func (s SiteSettings) snapshotInterval() time.Duration {
	if s.SnapshotIntervalMinutes < minSnapshotMinutes || s.SnapshotIntervalMinutes > maxSnapshotMinutes {
		return 0
	}
	return time.Duration(s.SnapshotIntervalMinutes) * time.Minute
}

// the time a preview link works, in minutes: the least and the most the admin may set, and what it is when nothing is set
const (
	minPreviewLinkMinutes      = 10
	maxPreviewLinkMinutes      = 1440
	defaultPreviewLinkGuest    = 60  // a guest: 1 hour
	defaultPreviewLinkSignedIn = 180 // a signed-in user: 3 hours
)

// the longest a share link works, in minutes: the least and the most the admin may set, and what it is when nothing is set
const (
	minShareLinkMinutes      = 10
	maxShareLinkMinutes      = 10080
	defaultShareLinkGuest    = 60    // a guest: 1 hour
	defaultShareLinkSignedIn = 10080 // a signed-in user: 7 days, as long as a sign-in lasts
)

// the longest a code link works, in days
const (
	minSnippetMaxDays     = 1
	maxSnippetMaxDays     = 365
	defaultSnippetMaxDays = 30
)

// snippetMaxDays is the longest a code link works in force: the one set, or a month.
func (s SiteSettings) snippetMaxDays() int {
	d := s.SnippetMaxDays
	if d < minSnippetMaxDays || d > maxSnippetMaxDays {
		d = defaultSnippetMaxDays
	}
	return d
}

// how often the server cleans up the shared sessions, in hours
const (
	minSessionCleanupHours     = 1
	maxSessionCleanupHours     = 720
	defaultSessionCleanupHours = 24
)

// sessionCleanupInterval is the time between two cleanups in force: the one set, or a day.
func (s SiteSettings) sessionCleanupInterval() time.Duration {
	h := s.SessionCleanupHours
	if h < minSessionCleanupHours || h > maxSessionCleanupHours {
		h = defaultSessionCleanupHours
	}
	return time.Duration(h) * time.Hour
}

// shareLinkMax is the longest a share link works in force for a guest or a signed-in user: the one set, or the built-in time.
func (s SiteSettings) shareLinkMax(guest bool) int {
	m, def := s.ShareLinkUserMinutes, defaultShareLinkSignedIn
	if guest {
		m, def = s.ShareLinkGuestMinutes, defaultShareLinkGuest
	}
	if m < minShareLinkMinutes || m > maxShareLinkMinutes {
		m = def
	}
	return m
}

// previewLinkLife is how long a preview link works in force for a guest or a signed-in user: the one set, or the built-in time.
func (s SiteSettings) previewLinkLife(guest bool) time.Duration {
	m, def := s.PreviewLinkUserMinutes, defaultPreviewLinkSignedIn
	if guest {
		m, def = s.PreviewLinkGuestMinutes, defaultPreviewLinkGuest
	}
	if m < minPreviewLinkMinutes || m > maxPreviewLinkMinutes {
		m = def
	}
	return time.Duration(m) * time.Minute
}

// previewBaseURL is the origin of the preview host (scheme and host, as the pages and the workers use it), or "" when previews
// stay below this site's /preview route. The setting is kept as the admin typed it (preview.openrepl.com); the scheme is derived
// here (defaultScheme) unless one was typed. Settings are normalized before use, but this defensive parse also keeps a bad old
// record on the compatible path.
func (s SiteSettings) previewBaseURL() string {
	u, err := parsePreviewBase(s.PreviewBaseURL)
	if err != nil || u == nil {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// defaultScheme is the scheme for a host typed without one: http for a local development host
// (localhost, 127.0.0.1, [::1], *.localhost, *.test), https for everything else.
func defaultScheme(host string) string {
	h := strings.ToLower(host)
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if strings.HasPrefix(h, "[") {
		if j := strings.Index(h, "]"); j >= 0 {
			h = h[:j+1]
		}
	} else if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	if h == "localhost" || h == "127.0.0.1" || h == "[::1]" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test") {
		return "http://"
	}
	return "https://"
}

// cleanPreviewBaseURL checks the setting and returns it as typed (trimmed, lower case, no trailing slash): no scheme is added to it.
func cleanPreviewBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if _, err := parsePreviewBase(raw); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimRight(raw, "/")), nil
}

// parsePreviewBase reads the setting as a URL: a host typed without a scheme gets one (http for a local host, else https). nil for "".
func parsePreviewBase(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "//") {
		raw = defaultScheme(raw) + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("the preview base URL must be a host name such as preview.openrepl.com (a scheme is optional; http for a local host, https otherwise), without a path, query or fragment")
	}
	return u, nil
}

// maxEditorFileMB is the limit in force: the one set, or the built-in 16.
func (s SiteSettings) maxEditorFileMB() int {
	if s.MaxEditorFileMB < minEditorFileMB || s.MaxEditorFileMB > maxEditorFileMB {
		return maxEditorFileMB
	}
	return s.MaxEditorFileMB
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
		ColorOfTheDay:        s.ColorOfTheDay,
		Announcement:         s.Announcement,
		Maintenance:          s.Maintenance,
		DisabledLanguages:    langs,
		GenieDisabled:        s.Genie.Disabled,
		PreviewOff:           s.PreviewOff,
		PreviewGuestsOff:     s.PreviewGuestsOff,
		PreviewAgentOff:      s.PreviewAgentOff,
		PreviewBaseURL:       s.PreviewBaseURL, // as typed: the page adds the scheme and port of the site it was opened from
		GenieEventsOff:       s.GenieEventsOff,
		EscalationModels:     s.Genie.EscalationModels,
		GenieAuto:            s.Genie.autoPublic(),
		MaxEditorFileMB:      s.maxEditorFileMB(),
		ShareMaxGuestMinutes: s.shareLinkMax(true),
		ShareMaxUserMinutes:  s.shareLinkMax(false),
		SnippetMaxDays:       s.snippetMaxDays(),
		DisabledModels:       models,
		CustomModels:         s.Genie.publicCustomModels(),
		DefaultModel:         s.Genie.DefaultModel,
		AgentModel:           s.Genie.agentDefaultModelID(),
		AgentModels:          s.Genie.AgentModels,
		AgentEffort:          s.Genie.agentEffortID(),
		GuestBlocked:         append([]string{}, s.Genie.GuestBlockedModels...),
		FeaturedModels:       append([]string{}, s.Genie.FeaturedModels...),
		GenieContext:         s.Genie.context(),
		AgentEnabled:         !s.Genie.AgentDisabled && !s.Genie.Disabled,
		AgentAutoOff:         s.Genie.AgentAutoOff,
		AgentRiskyOff:        s.Genie.AgentRiskyOff,
		AgentOnPractice:      s.Genie.AgentOnPractice,
		AgentMaxSteps:        s.Genie.agentMaxSteps(),
		GenieStream:          s.Genie.StreamAnswers,
		GenieSummaryTokens:   s.Genie.summaryTokens(),
		ChatHistory:          !s.Genie.ChatHistoryOff && !s.Genie.Disabled,
		ChatHistoryGuests:    !s.Genie.ChatHistoryNoGuests,
		ChatHistoryTabs:      s.Genie.chatsTabs(),
	}
}

// handleSettingsJS serves the public settings as a script that defines
// window.site_settings. Pages load it before js/preprocessing.js.
func handleSettingsJS(rw http.ResponseWriter, req *http.Request) {
	pub := GetSiteSettings().public()
	if f := nodeDisabledForRequest; f != nil {
		// On a gateway the languages that are off depend on the node that
		// serves the visitor's session.
		if langs := f(req); langs != nil {
			pub.DisabledLanguages = langs
		}
	}
	data, err := json.Marshal(pub)
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
	// PluginCatalogue is the plugins the site ships, for the list of those that are on for everyone.
	PluginCatalogue []pluginChoice `json:"pluginCatalogue"`
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
	if s.Genie.FeaturedModels == nil {
		s.Genie.FeaturedModels = []string{}
	}
	s.Genie.CustomModels = append([]CustomModel{}, s.Genie.customList()...) // the list in effect, never null
	s.Secrets = nil
	s.IntegrationSecrets = nil // sealed, and not for a browser
	s.Integrations = nil       // the Integrations page asks /admin/integrations
	return settingsReply{SiteSettings: s, Languages: languageChoices(), Models: modelInfos(), PluginCatalogue: pluginChoices(), Defaults: currentGenieDefaults(), Store: store, Version: version}
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
		if s.Genie.CustomModels == nil {
			s.Genie.CustomModels = before.Genie.CustomModels // a page from before the list existed does not send it
		}
		s.NodeLanguages = before.NodeLanguages // nor the languages of single nodes; /admin/workers does
		s.NodeWeights = before.NodeWeights     // nor their weights
		s.Integrations, s.IntegrationSecrets, s.IntegrationsSeeded, s.IntegrationsFixed = before.Integrations, before.IntegrationSecrets, before.IntegrationsSeeded, before.IntegrationsFixed // nor the integrations
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
	if strings.Join(a.StoreSafeList, ",") != strings.Join(b.StoreSafeList, ",") {
		out = append(out, "App Store safe list changed")
	}
	if a.StoreLimitMB != b.StoreLimitMB {
		out = append(out, fmt.Sprintf("App Store size limit %d MB", b.StoreLimitMB))
	}
	if a.StoreOff != b.StoreOff {
		out = append(out, "App Store "+onoff(!b.StoreOff))
	}
	if a.EgressOn != b.EgressOn {
		out = append(out, "Internet for REPLs "+onoff(b.EgressOn))
	}
	out = append(out, nodeLanguageChanges(a, b)...)
	if a.Genie.Disabled != b.Genie.Disabled {
		out = append(out, "Genie "+map[bool]string{true: "switched off", false: "switched on"}[b.Genie.Disabled])
	}
	for _, id := range chatModelOrder {
		if a.Genie.ModelDisabled(id) != b.Genie.ModelDisabled(id) {
			out = append(out, "model "+chatModels[id].Name+" "+map[bool]string{true: "switched off", false: "switched on"}[b.Genie.ModelDisabled(id)])
		}
	}
	out = append(out, customModelChanges(a.Genie, b.Genie)...)
	if strings.Join(a.Genie.FeaturedModels, ",") != strings.Join(b.Genie.FeaturedModels, ",") {
		out = append(out, "models that start in the short list changed")
	}
	if a.Genie.DefaultModel != b.Genie.DefaultModel {
		name := "built-in"
		if m, ok := chatModels[b.Genie.DefaultModel]; ok {
			name = m.Name
		}
		out = append(out, "default model: "+name)
	}
	if a.Genie.AgentModel != b.Genie.AgentModel || a.Genie.AgentEffort != b.Genie.AgentEffort {
		out = append(out, "Agent model: "+orStr(b.Genie.AgentModel, "built-in")+", effort "+orStr(b.Genie.AgentEffort, "low"))
	}
	if fmt.Sprint(a.Genie.GuestBlockedModels) != fmt.Sprint(b.Genie.GuestBlockedModels) {
		out = append(out, "models kept for signed-in users: "+strings.Join(b.Genie.GuestBlockedModels, ", "))
	}
	if fmt.Sprint(a.Genie.AgentModels) != fmt.Sprint(b.Genie.AgentModels) {
		out = append(out, "models for Agent mode: "+map[bool]string{true: "every model that is on", false: strings.Join(b.Genie.AgentModels, ", ")}[b.Genie.AgentModels == nil])
	}
	if a.Genie.SummaryOff != b.Genie.SummaryOff || a.Genie.SummaryTokens != b.Genie.SummaryTokens {
		out = append(out, "Genie: chat summary "+onoff(!b.Genie.SummaryOff))
	}
	if a.DBCapacityMB != b.DBCapacityMB {
		out = append(out, "database capacity changed")
	}
	if a.PracticeWorkspaceOff != b.PracticeWorkspaceOff {
		out = append(out, "practice saved in the workspace "+onoff(!b.PracticeWorkspaceOff))
	}
	if a.PreviewOff != b.PreviewOff {
		out = append(out, "preview "+onoff(!b.PreviewOff))
	}
	if a.PreviewAgentOff != b.PreviewAgentOff {
		out = append(out, "Genie acting in the preview "+onoff(!b.PreviewAgentOff))
	}
	if a.GenieEventsOff != b.GenieEventsOff {
		out = append(out, "the Genie log "+onoff(!b.GenieEventsOff))
	}
	if a.GenieEventDays != b.GenieEventDays {
		out = append(out, fmt.Sprintf("the Genie log is kept %d days", b.GenieEventDays))
	}
	if a.GenieHealthErrPct != b.GenieHealthErrPct || a.GenieHealthSlowMs != b.GenieHealthSlowMs {
		out = append(out, fmt.Sprintf("the Genie health alerts are at %d%% failed calls and %d ms", b.GenieHealthErrPct, b.GenieHealthSlowMs))
	}
	if a.PreviewGuestsOff != b.PreviewGuestsOff {
		out = append(out, "preview for guests "+onoff(!b.PreviewGuestsOff))
	}
	if a.previewBaseURL() != b.previewBaseURL() {
		out = append(out, "preview base URL "+orStr(b.previewBaseURL(), "this site"))
	}
	if a.previewLinkLife(true) != b.previewLinkLife(true) {
		out = append(out, fmt.Sprintf("preview link works %d minutes for guests", int(b.previewLinkLife(true).Minutes())))
	}
	if a.previewLinkLife(false) != b.previewLinkLife(false) {
		out = append(out, fmt.Sprintf("preview link works %d minutes for signed-in users", int(b.previewLinkLife(false).Minutes())))
	}
	if a.snippetMaxDays() != b.snippetMaxDays() {
		out = append(out, fmt.Sprintf("code links work at most %d days", b.snippetMaxDays()))
	}
	if a.sessionCleanupInterval() != b.sessionCleanupInterval() {
		out = append(out, fmt.Sprintf("shared session cleanup every %d hours", int(b.sessionCleanupInterval().Hours())))
	}
	if a.shareLinkMax(true) != b.shareLinkMax(true) {
		out = append(out, fmt.Sprintf("share link works at most %d minutes for guests", b.shareLinkMax(true)))
	}
	if a.shareLinkMax(false) != b.shareLinkMax(false) {
		out = append(out, fmt.Sprintf("share link works at most %d minutes for signed-in users", b.shareLinkMax(false)))
	}
	if a.maxEditorFileMB() != b.maxEditorFileMB() {
		out = append(out, fmt.Sprintf("largest editor file %d MB", b.maxEditorFileMB()))
	}
	if ga, gb := a.Genie, b.Genie; ga.context() != gb.context() || ga.OpenRouterTimeoutSec != gb.OpenRouterTimeoutSec ||
		fmt.Sprint(ga.AnswerCaps) != fmt.Sprint(gb.AnswerCaps) || fmt.Sprint(ga.ModelPrices) != fmt.Sprint(gb.ModelPrices) || ga.ShowCostToUsers != gb.ShowCostToUsers || strings.Join(ga.OpenRouterHosts, ",") != strings.Join(gb.OpenRouterHosts, ",") {
		out = append(out, "Genie limits changed (context, history, answer sizes, OpenRouter time limit or hosts)")
	}
	if ga, gb := a.Genie, b.Genie; ga.ConnectionsOff != gb.ConnectionsOff || ga.ConnectionsNoApproval != gb.ConnectionsNoApproval || strings.Join(ga.ConnectionHosts, ",") != strings.Join(gb.ConnectionHosts, ",") {
		out = append(out, "Genie connections "+onoff(!gb.ConnectionsOff)+" (without approval "+onoff(gb.ConnectionsNoApproval)+", only these servers: "+orStr(strings.Join(gb.ConnectionHosts, ", "), "any")+")")
	}
	if ga, gb := a.Genie, b.Genie; ga.ExtensionsOff != gb.ExtensionsOff || ga.PluginsFromLinks != gb.PluginsFromLinks || strings.Join(ga.SitePlugins, ",") != strings.Join(gb.SitePlugins, ",") {
		out = append(out, "Genie skills and plugins "+onoff(!gb.ExtensionsOff)+" (from a link "+onoff(gb.PluginsFromLinks)+", on for everyone: "+orStr(strings.Join(gb.SitePlugins, ", "), "none")+")")
	}
	if ga, gb := a.Genie, b.Genie; ga.ChatHistoryOff != gb.ChatHistoryOff || ga.ChatHistoryNoGuests != gb.ChatHistoryNoGuests || ga.ChatHistoryChats != gb.ChatHistoryChats || ga.ChatHistoryTabs != gb.ChatHistoryTabs || ga.ChatHistoryKB != gb.ChatHistoryKB || ga.ChatHistoryDays != gb.ChatHistoryDays {
		out = append(out, "Genie chat history "+onoff(!gb.ChatHistoryOff)+" (guests "+onoff(!gb.ChatHistoryNoGuests)+")")
	}
	if a.Genie.AgentNativeTools != b.Genie.AgentNativeTools || a.Genie.StreamAnswers != b.Genie.StreamAnswers || fmt.Sprint(a.Genie.NativeToolModels) != fmt.Sprint(b.Genie.NativeToolModels) {
		out = append(out, "Genie: native tool calling "+onoff(b.Genie.AgentNativeTools)+", streamed answers "+onoff(b.Genie.StreamAnswers))
	}
	if ga, gb := a.Genie, b.Genie; ga.MeterOff != gb.MeterOff || ga.CreditsOn != gb.CreditsOn || ga.PlanName != gb.PlanName || ga.PlanCredits != gb.PlanCredits || ga.PlanBudget != gb.PlanBudget || fmt.Sprint(ga.CreditWeights) != fmt.Sprint(gb.CreditWeights) {
		out = append(out, "Genie credits and plan changed (usage record "+onoff(!gb.MeterOff)+", credits "+onoff(gb.CreditsOn)+")")
	}
	if a.Genie.AgentDisabled != b.Genie.AgentDisabled {
		out = append(out, "agent mode "+onoff(!b.Genie.AgentDisabled))
	}
	if a.Genie.AgentAutoOff != b.Genie.AgentAutoOff {
		out = append(out, "agent: allow everything for a session "+onoff(!b.Genie.AgentAutoOff))
	}
	if a.Genie.AutoOn != b.Genie.AutoOn || a.Genie.AutoSmall != b.Genie.AutoSmall || a.Genie.AutoStrong != b.Genie.AutoStrong || a.Genie.AutoDefault != b.Genie.AutoDefault {
		if b.Genie.AutoOn {
			out = append(out, fmt.Sprintf("Genie: Auto model choice on (small %s, strong %s, default for %s)", b.Genie.AutoSmall, b.Genie.AutoStrong, b.Genie.autoDefault()))
		} else {
			out = append(out, "Genie: Auto model choice off")
		}
	}
	if fmt.Sprint(a.Genie.EscalationModels) != fmt.Sprint(b.Genie.EscalationModels) {
		out = append(out, "Genie: models for a stuck task "+strings.Join(b.Genie.EscalationModels, ", "))
	}
	if a.Genie.AgentShellOff != b.Genie.AgentShellOff {
		out = append(out, "agent: run commands "+onoff(!b.Genie.AgentShellOff))
	}
	if a.Genie.AgentRiskyOff != b.Genie.AgentRiskyOff {
		out = append(out, "agent: allowing riskier things for a while "+onoff(!b.Genie.AgentRiskyOff))
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
		out = append(out, modelInfo{ID: m.ID, Name: m.Name, Provider: m.Provider, KeySet: providerKey(m.Provider) != ""})
	}
	return out
}

// genieDefaults are what an empty number means.
type genieDefaults struct {
	ContextEditorChars   int               `json:"contextEditorChars"`
	ContextTerminalChars int               `json:"contextTerminalChars"`
	ContextTerminalLines int               `json:"contextTerminalLines"`
	HistoryMessages      int               `json:"historyMessages"`
	ContextAttachItems   int               `json:"contextAttachItems"`
	ContextAttachChars   int               `json:"contextAttachChars"`
	ContextAttachTotal   int               `json:"contextAttachTotal"`
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
		ContextAttachItems: defaultAttachItems, ContextAttachChars: defaultAttachChars, ContextAttachTotal: defaultAttachTotal,
		AnswerCaps: caps, OpenRouterTimeoutSec: defaultOpenRouterTimeoutSec, OpenRouterHosts: hosts,
		AgentTasksPerHour: defaultAgentTasksPerHour, AgentMaxSteps: defaultAgentMaxSteps,
		Limits: map[string][2]int{
			"contextEditorChars": {minEditorChars, maxEditorChars}, "contextTerminalChars": {minTerminalChars, maxTerminalChars},
			"contextTerminalLines": {minTerminalLines, maxTerminalLines}, "historyMessages": {minHistory, maxHistory},
			"contextAttachItems": {minAttachItems, maxAttachItems}, "contextAttachChars": {minAttachChars, maxAttachChars},
			"contextAttachTotal": {minAttachTotal, maxAttachTotal},
			"answerCap":          {minAnswerCap, maxAnswerCap}, "openRouterTimeoutSec": {minORTimeout, maxORTimeout},
			"agentTasksPerHour": {minAgentTasks, maxAgentTasks}, "agentMaxSteps": {minAgentSteps, maxAgentStepsSet},
		},
	}
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

const (
	defaultSummaryTokens = 6000
	minSummaryTokens     = 1000
	maxSummaryTokens     = 40000
)

// summaryTokens is what the page uses: 0 when the summary is off.
func (g GenieSettings) summaryTokens() int {
	if g.SummaryOff {
		return 0
	}
	return orDefault(g.SummaryTokens, defaultSummaryTokens)
}
