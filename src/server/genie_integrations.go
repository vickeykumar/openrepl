package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"user"
	"utils"
)

// Integrations: the servers Genie can be connected to, kept as data that an admin edits (docs/lld/19 section 25; canvas claude.ai/artifact/4SvDQfUd4FR6v8Xv9mi6AF).
//
// They used to be a list in Go (connectionCatalogue). The admin portal adds, tests, configures and publishes them now; the list in Go only
// seeds the first record (GitHub) when the site has none. A record is kept in the site settings document (SiteSettings.Integrations); what is
// secret about it (the OAuth client secret, a token that only lists the tools) is sealed in SiteSettings.IntegrationSecrets and never sent to a
// browser. What a person has enabled and connected is theirs alone (genie_conn_store.go).
//
// An integration is seen by people only when it is published and the person is in its audience (admins see the ones that are not published yet,
// to sign in and test them). Its tools are listed to Genie only when the admin switched them on and gave each a level; a tool the server
// changed since goes off until the admin has looked.

const (
	integDraft     = "draft"     // being set up: only admins see it
	integTested    = "tested"    // a test passed: still only admins
	integPublished = "published" // people in the audience see it
	integOff       = "off"       // switched off: nobody sees it, every call stops, saved sign-ins are kept

	maxIntegrations   = 60
	maxIntegrationBox = 200 // people in an audience
)

// Integration is a server and everything the admin decided about it.
type Integration struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Description  string              `json:"description,omitempty"`
	URL          string              `json:"url"`
	Keywords     []string            `json:"keywords,omitempty"` // how Genie knows that a task is about it
	Help         string              `json:"help,omitempty"`     // for a token: how to get one
	Logo         string              `json:"logo,omitempty"`     // optional: an https image address; none: the first letter of the name is shown
	Status       string              `json:"status"`
	Auth         IntegrationAuth     `json:"auth"`
	Audience     IntegrationAudience `json:"audience"`
	Limits       IntegrationLimits   `json:"limits"`
	Tools        []ToolPolicy        `json:"tools,omitempty"`
	LegacyLevels map[string]string   `json:"levels,omitempty"` // the levels of a server whose tools were never tested here (GitHub's seed)
	LastTest     *IntegrationTest    `json:"lastTest,omitempty"`
	FromRequest  string              `json:"fromRequest,omitempty"`
	CreatedBy    string              `json:"createdBy,omitempty"`
	CreatedAt    string              `json:"createdAt,omitempty"`
	UpdatedAt    string              `json:"updatedAt,omitempty"`
}

// IntegrationAuth is how a person signs in to the server.
type IntegrationAuth struct {
	Mode            string   `json:"mode"`           // "none" (an open server), "token" (a person pastes their own) or "oauth"
	Auto            bool     `json:"auto,omitempty"` // oauth: the server offers registration, so Genie registers itself and the admin types nothing
	AuthURL         string   `json:"authUrl,omitempty"`
	TokenURL        string   `json:"tokenUrl,omitempty"`
	RegistrationURL string   `json:"registrationUrl,omitempty"`
	Issuer          string   `json:"issuer,omitempty"`
	RequireIssuer   bool     `json:"requireIssuer,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	ClientID        string   `json:"clientId,omitempty"` // the app the admin made (the secret is sealed apart)
	// GitHub's seed reads its client from the environment, as before
	ClientIDEnv     string `json:"clientIdEnv,omitempty"`
	ClientSecretEnv string `json:"clientSecretEnv,omitempty"`
	ScopesEnv       string `json:"scopesEnv,omitempty"`
	Detected        bool   `json:"detected,omitempty"` // filled by a test, not typed by the admin: the next test may fill it again
	// the clients that Genie registered by itself, one for each address of this site that a sign-in came back to
	Clients map[string]RegisteredClient `json:"clients,omitempty"`
}

// RegisteredClient is what a server gave when Genie registered (RFC 7591). The secret, if there is one, is sealed.
type RegisteredClient struct {
	ClientID     string `json:"clientId"`
	SecretSealed string `json:"secretSealed,omitempty"`
	At           string `json:"at,omitempty"`
}

type IntegrationAudience struct {
	Everyone bool     `json:"everyone"`
	People   []string `json:"people,omitempty"` // e-mail addresses (lower case) or accounts
}

// IntegrationLimits can only be lower than the site's (connCallsPerTask and connPerMinute); 0 is the site's.
type IntegrationLimits struct {
	CallsPerTask int `json:"callsPerTask,omitempty"`
	PerMinute    int `json:"perMinute,omitempty"`
}

// ToolPolicy is what the admin decided about one tool.
type ToolPolicy struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Level     string `json:"level,omitempty"` // read, write or restricted: a tool without a level cannot be on
	Digest    string `json:"digest,omitempty"`
	New       bool   `json:"new,omitempty"`     // found by a test and not looked at
	Changed   bool   `json:"changed,omitempty"` // the server changed it since the admin looked
	Note      string `json:"note,omitempty"`
	FirstSeen string `json:"firstSeen,omitempty"`
	LastSeen  string `json:"lastSeen,omitempty"`
}

// IntegrationTest is the result of the last test (integration_admin.go).
type IntegrationTest struct {
	At        string `json:"at"`
	OK        bool   `json:"ok"`
	Protocol  string `json:"protocol,omitempty"`
	Tools     int    `json:"tools"`
	SignIn    string `json:"signIn,omitempty"` // auto, oauth, token, none, unknown
	NeedsAuth bool   `json:"needsAuth,omitempty"`
	Error     string `json:"error,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Skipped   int    `json:"skipped,omitempty"` // tools with a name that a call cannot use
	// what the test saw, for the screen of the test (canvas board 6)
	Ms            int      `json:"ms,omitempty"`            // how long the server took to answer
	Bytes         int      `json:"bytes,omitempty"`         // the size of the list of tools
	Schemas       int      `json:"schemas,omitempty"`       // tools whose schema is valid JSON
	NoDescription int      `json:"noDescription,omitempty"` // tools that say nothing about themselves
	Refused       []string `json:"refused,omitempty"`       // names a call cannot use (at most 5)
}

// LiveTool is a tool as the server described it, sent to the admin once, with the answer to a test, and never kept: the schema is fetched live and
// only its hash is saved.
type LiveTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Args        []string        `json:"args"`
	Hint        string          `json:"hint"` // read-only, destructive or none: the server's own claim
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// IntegrationSecret holds sealed values (AES-GCM under OPENREPL_SECRET, tied to the integration and the kind): never sent to a browser,
// never used on a person's call.
type IntegrationSecret struct {
	OAuthClientSecret string `json:"oauthClientSecret,omitempty"`
	DiscoveryToken    string `json:"discoveryToken,omitempty"`
}

const (
	secretOAuthClient = "oauthClientSecret"
	secretDiscovery   = "discoveryToken"
)

func integSecretAAD(id, kind string) string { return "integration:" + id + ":" + kind }

// ---- the registry ----------------------------------------------------------------------------------------------------------------------

var integMu sync.Mutex

// integrationsFixVersion is the number of the one-time repairs that seedIntegrations makes.
const integrationsFixVersion = 1

// seedIntegrations makes the first records from the list in Go, once, and makes the one-time repairs. Nothing is lost when the settings cannot be written: the list is used as it is.
//
// Repair 1: a record the site seeded (GitHub) that an early test left with every tool off and no level goes back to having no tool policy, so it works by the
// site's own levels as it did before; the next test of it makes a policy at those levels. What the admin decided after that is kept as saved.
func seedIntegrations(s SiteSettings) (SiteSettings, bool) {
	if s.IntegrationsSeeded {
		if s.IntegrationsFixed >= integrationsFixVersion {
			return s, false
		}
		fixed := append([]Integration(nil), s.Integrations...)
		for k := range fixed {
			if fixed[k].builtInUndecided() {
				fixed[k].Tools = nil
			}
		}
		s.Integrations, s.IntegrationsFixed = fixed, integrationsFixVersion
		return s, true
	}
	have := map[string]bool{}
	for _, i := range s.Integrations {
		have[i.ID] = true
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range connectionCatalogue {
		if have[c.ID] {
			continue
		}
		i := Integration{ID: c.ID, Title: c.Title, URL: c.URL, Help: c.Help, Logo: c.Logo, Status: integPublished, Audience: IntegrationAudience{Everyone: true},
			LegacyLevels: c.Levels, CreatedBy: "seed", CreatedAt: now, UpdatedAt: now}
		i.Auth = IntegrationAuth{Mode: "token"}
		if c.OAuth != nil {
			i.Auth = IntegrationAuth{Mode: "oauth", AuthURL: c.OAuth.AuthURL, TokenURL: c.OAuth.TokenURL, ClientIDEnv: c.OAuth.ClientIDEnv, ClientSecretEnv: c.OAuth.ClientSecretEnv, ScopesEnv: c.OAuth.ScopesEnv, Scopes: c.OAuth.Scopes, Issuer: c.OAuth.Issuer, RequireIssuer: c.OAuth.RequireIssuer}
		}
		s.Integrations = append(s.Integrations, i)
	}
	s.IntegrationsSeeded, s.IntegrationsFixed = true, integrationsFixVersion
	return s, true
}

// registry gives the integrations in effect. The first call on a site that has none writes the seed.
func registry() []Integration {
	s := GetSiteSettings()
	if !s.IntegrationsSeeded || s.IntegrationsFixed < integrationsFixVersion {
		integMu.Lock()
		for try := 0; try < 3; try++ {
			cur := GetSiteSettings()
			next, changed := seedIntegrations(cur)
			if !changed {
				s = cur
				break
			}
			if err := SaveSiteSettings(next, currentSettingsVersion()); err == nil {
				s = GetSiteSettings()
				break
			} else if err != errSettingsConflict {
				s = next // cannot be written now (the store is not reachable): used as it is, tried again at the next call
				break
			}
		}
		integMu.Unlock()
	}
	out := make([]Integration, len(s.Integrations))
	copy(out, s.Integrations)
	return out
}

func findIntegration(id string) (Integration, bool) {
	for _, i := range registry() {
		if i.ID == id {
			return i, true
		}
	}
	return Integration{}, false
}

// updateRegistry changes the integrations and their secrets under the settings' own version, retried when the store moved on.
func updateRegistry(fn func(list *[]Integration, secrets map[string]IntegrationSecret) error) error {
	registry() // the seed first
	integMu.Lock()
	defer integMu.Unlock()
	var err error
	for try := 0; try < 3; try++ {
		cur := GetSiteSettings()
		list := make([]Integration, len(cur.Integrations))
		copy(list, cur.Integrations)
		secrets := map[string]IntegrationSecret{}
		for k, v := range cur.IntegrationSecrets {
			secrets[k] = v
		}
		if err = fn(&list, secrets); err != nil {
			return err
		}
		cur.Integrations = list
		if len(secrets) == 0 {
			secrets = nil
		}
		cur.IntegrationSecrets = secrets
		if err = SaveSiteSettings(cur, currentSettingsVersion()); err == errSettingsConflict {
			continue
		}
		return err
	}
	return err
}

func integIndex(list []Integration, id string) int {
	for k := range list {
		if list[k].ID == id {
			return k
		}
	}
	return -1
}

// ---- who sees what ------------------------------------------------------------------------------------------------------------------------

// personOf gives the address and admin state of an account, for the audience of an integration.
func personOf(uid string) (email string, isAdmin bool) {
	if uid == "" {
		return "", false
	}
	if up, err := user.FetchUserProfileData(uid); err == nil {
		email = strings.ToLower(strings.TrimSpace(up.Email))
	}
	return email, email != "" && utils.IsAdminEmail(email)
}

func (a IntegrationAudience) includes(uid, email string) bool {
	if a.Everyone {
		return true
	}
	for _, p := range a.People {
		if p == uid || (email != "" && p == email) {
			return true
		}
	}
	return false
}

// visibleTo: a published integration to the people in its audience; an admin sees the others too, except a switched off one.
func (i Integration) visibleTo(uid, email string, isAdmin bool) bool {
	if i.Status == integPublished && i.Audience.includes(uid, email) {
		return true
	}
	return isAdmin && i.Status != integOff
}

// ---- the integration as the gateway knows it ------------------------------------------------------------------------------------------------

var levelOfPolicy = map[string]string{"read": "read", "write": "write", "restricted": "irreversible"}

// toDef makes the connection the gateway works with.
func (i Integration) toDef() connectionDef {
	i.Logo = i.logoOrDefault()
	d := connectionDef{ID: i.ID, Title: i.Title, URL: i.URL, Help: i.Help, Logo: i.Logo, Description: i.Description, Keywords: i.Keywords, Mode: i.Auth.Mode, Managed: true,
		Levels: i.LegacyLevels, Limits: i.Limits, Status: i.Status}
	if d.Mode == "" {
		d.Mode = "token"
	}
	if len(i.Tools) > 0 {
		d.Policy = map[string]ToolPolicy{}
		for _, t := range i.Tools {
			d.Policy[t.Name] = t
		}
	}
	if i.Auth.Mode == "oauth" {
		d.OAuth = &oauthDef{AuthURL: i.Auth.AuthURL, TokenURL: i.Auth.TokenURL, ClientID: i.Auth.ClientID, ClientIDEnv: i.Auth.ClientIDEnv, ClientSecretEnv: i.Auth.ClientSecretEnv,
			ScopesEnv: i.Auth.ScopesEnv, Scopes: i.Auth.Scopes, Issuer: i.Auth.Issuer, RequireIssuer: i.Auth.RequireIssuer, Auto: i.Auth.Auto, RegistrationURL: i.Auth.RegistrationURL, IntegrationID: i.ID, Resource: i.URL}
		if !i.Auth.Auto {
			d.OAuth.Resource = ""
		}
	}
	return d
}

// ---- checking what an admin typed -------------------------------------------------------------------------------------------------------------

// integrationURLOK checks an address an admin gives: https, a host name, no password, no port. A test of this package may use a loopback address.
func integrationURLOK(raw string) (string, error) {
	if extAllowLoopback {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil {
			return strings.ToLower(u.Hostname()), nil
		}
	}
	return connectionURLOK(raw)
}

func cleanWords(in []string, max, each int) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range in {
		w = strings.ToLower(clean1Line(strings.TrimSpace(w), each))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= max {
			break
		}
	}
	return out
}

var integEmailish = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)

// normalizeIntegration checks the fields of a record that an admin edits. It keeps the tools, the test and the status as the caller set them.
func normalizeIntegration(in Integration) (Integration, error) {
	in.ID = strings.TrimSpace(in.ID)
	if !connIDPattern.MatchString(in.ID) {
		return in, fmt.Errorf("the id has to be lower case letters, digits and dashes, at most 40")
	}
	in.Title = clean1Line(strings.TrimSpace(in.Title), 40)
	if in.Title == "" {
		return in, fmt.Errorf("the name is empty")
	}
	in.Description = clipRunes(strings.TrimSpace(in.Description), 300)
	in.Help = clipRunes(strings.TrimSpace(in.Help), 600)
	in.Logo = strings.TrimSpace(in.Logo)
	if err := checkLogo(in.Logo); err != nil {
		return in, err
	}
	if _, err := integrationURLOK(in.URL); err != nil {
		return in, err
	}
	in.URL = strings.TrimSpace(in.URL)
	in.Keywords = cleanWords(in.Keywords, 12, 30)
	switch in.Auth.Mode {
	case "none", "token", "oauth":
	case "":
		in.Auth.Mode = "token"
	default:
		return in, fmt.Errorf("the sign-in is none, token or oauth")
	}
	for _, u := range []*string{&in.Auth.AuthURL, &in.Auth.TokenURL, &in.Auth.RegistrationURL} {
		*u = strings.TrimSpace(*u)
		if *u != "" {
			if _, err := integrationURLOK(*u); err != nil {
				return in, fmt.Errorf("a sign-in address: %v", err)
			}
		}
	}
	in.Auth.Issuer = strings.TrimSpace(in.Auth.Issuer)
	in.Auth.ClientID = clean1Line(strings.TrimSpace(in.Auth.ClientID), 200)
	in.Auth.Scopes = cleanWords(in.Auth.Scopes, 20, 80)
	if in.Auth.Mode == "oauth" && !in.Auth.Auto && (in.Auth.AuthURL == "" || in.Auth.TokenURL == "") && in.Auth.ClientIDEnv == "" {
		// not an error while it is a draft: the test or the admin fills it in; publish refuses it (publishable)
	}
	var people []string
	seen := map[string]bool{}
	for _, p := range in.Audience.People {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || seen[p] || len(p) > 120 {
			continue
		}
		seen[p] = true
		people = append(people, p)
	}
	if len(people) > maxIntegrationBox {
		return in, fmt.Errorf("an audience can name at most %d people", maxIntegrationBox)
	}
	sort.Strings(people)
	in.Audience.People = people
	if in.Limits.CallsPerTask < 0 || in.Limits.CallsPerTask > connCallsPerTask || in.Limits.PerMinute < 0 || in.Limits.PerMinute > connPerMinute {
		return in, fmt.Errorf("the limits can only be lower than the site's (%d calls for a task, %d a minute)", connCallsPerTask, connPerMinute)
	}
	return in, nil
}

// publishable says why a record cannot be published yet, or "".
func (i Integration) publishable() string {
	if i.LastTest == nil && i.CreatedBy != "seed" {
		return "Test it first."
	}
	if i.LastTest != nil && !i.LastTest.OK && !i.LastTest.NeedsAuth {
		return "The last test failed: " + i.LastTest.Error
	}
	if i.Auth.Mode == "oauth" && !i.Auth.Auto && (i.Auth.AuthURL == "" || i.Auth.TokenURL == "") && i.Auth.ClientIDEnv == "" {
		return "OAuth needs its sign-in and token addresses (the test finds them when the server offers them)."
	}
	if i.Auth.Mode == "oauth" && !i.Auth.Auto && i.Auth.ClientID == "" && i.Auth.ClientIDEnv == "" {
		return "OAuth needs the client ID and the client secret of the app you made."
	}
	if len(i.Tools) > 0 {
		on := 0
		for _, t := range i.Tools {
			if t.Enabled {
				on++
			}
		}
		if on == 0 {
			return "No tool is switched on."
		}
	} else if i.CreatedBy != "seed" {
		return "No tool is switched on: test the server and switch some on."
	}
	if !i.Audience.Everyone && len(i.Audience.People) == 0 {
		return "Choose who can see it."
	}
	return ""
}

// ---- secrets ---------------------------------------------------------------------------------------------------------------------------------

func setIntegrationSecret(secrets map[string]IntegrationSecret, id, kind, plain string) error {
	var sealed string
	if plain != "" {
		if utils.Secret() == "" {
			return errNoSettingsSecret
		}
		var err error
		if sealed, err = sealKey(utils.Secret(), integSecretAAD(id, kind), plain); err != nil {
			return err
		}
	}
	s := secrets[id]
	switch kind {
	case secretOAuthClient:
		s.OAuthClientSecret = sealed
	case secretDiscovery:
		s.DiscoveryToken = sealed
	default:
		return fmt.Errorf("that is not a secret of an integration")
	}
	if s == (IntegrationSecret{}) {
		delete(secrets, id)
	} else {
		secrets[id] = s
	}
	return nil
}

func integrationSecret(id, kind string) string {
	s := GetSiteSettings().IntegrationSecrets[id]
	sealed := s.OAuthClientSecret
	if kind == secretDiscovery {
		sealed = s.DiscoveryToken
	}
	if sealed == "" || utils.Secret() == "" {
		return ""
	}
	plain, err := openKey(utils.Secret(), integSecretAAD(id, kind), sealed)
	if err != nil {
		return ""
	}
	return plain
}

// ---- the client of a sign-in ---------------------------------------------------------------------------------------------------------------

// clientFor gives the client ID and secret a sign-in to an integration uses for this address of the site: the app the admin made, the one in the
// environment (GitHub's seed), or the one Genie registered by itself for this address.
func clientFor(id string, o *oauthDef, redirect string) (clientID, secret string) {
	if o == nil {
		return "", ""
	}
	if o.ClientID != "" {
		return o.ClientID, integrationSecret(id, secretOAuthClient)
	}
	if o.ClientIDEnv != "" {
		return strings.TrimSpace(oauthEnv(o.ClientIDEnv)), func() string {
			if o.ClientSecretEnv == "" {
				return ""
			}
			return oauthEnv(o.ClientSecretEnv)
		}()
	}
	if i, ok := findIntegration(id); ok {
		if c, ok := i.Auth.Clients[redirect]; ok && c.ClientID != "" {
			plain := ""
			if c.SecretSealed != "" && utils.Secret() != "" {
				plain, _ = openKey(utils.Secret(), integSecretAAD(id, "client:"+redirect), c.SecretSealed)
			}
			return c.ClientID, plain
		}
	}
	return "", ""
}

// ---- how many people use each one --------------------------------------------------------------------------------------------------------------

type integrationUsage struct {
	Enabled   int `json:"enabled"`
	Connected int `json:"connected"`
}

func usageOfIntegrations() map[string]integrationUsage {
	out := map[string]integrationUsage{}
	forEachConnDoc(func(d *connUserDoc) {
		for id, e := range d.Integrations {
			u := out[id]
			if e.Enabled {
				u.Enabled++
			}
			if e.Connection != nil {
				u.Connected++
			}
			out[id] = u
		}
	})
	return out
}

// publicHost is for messages: the host of an address.
func publicHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if net.ParseIP(h) != nil {
		return h
	}
	return strings.ToLower(h)
}

// logoOrDefault is the logo of the record, or the one the site ships for that id (a record saved before logos existed has none).
func (i Integration) logoOrDefault() string {
	if i.Logo != "" {
		return i.Logo
	}
	for _, c := range connectionCatalogue {
		if c.ID == i.ID {
			return c.Logo
		}
	}
	return ""
}

// checkLogo accepts an https image address the admin typed, or nothing. It is only ever shown in an img tag (so an svg there cannot run script);
// people's browsers ask that site for it.
func checkLogo(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(s) > 300 || strings.ContainsAny(s, " \t\r\n\"'<>") {
		return fmt.Errorf("the logo has to be an https image address")
	}
	return nil
}
