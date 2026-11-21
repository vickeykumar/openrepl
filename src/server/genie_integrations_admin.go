package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cookie"
	"utils"
)

// The admin's side of the integrations (docs/lld/19 section 25): the routes of /admin/integrations, the test of a server (what it speaks, what its
// sign-in is, its tools) and the policy of its tools. The data is in genie_integrations.go.

// ---- finding out how a server signs people in --------------------------------------------------------------------------------------------------

type signInInfo struct {
	NeedsAuth        bool
	ResourceMetadata string
	Mode             string // auto, oauth, token, none
	Issuer           string
	AuthURL          string
	TokenURL         string
	RegistrationURL  string
	Scopes           []string
	IssParam         bool
	Detail           string
}

func probeClient() *http.Client {
	c := newExtClientFor(12 * time.Second)
	inner := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return fmt.Errorf("a redirect to another host")
		}
		return inner(req, via)
	}
	return c
}

var wwwAuthMeta = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// probeNeedsAuth asks the server to initialize without a token: a 401 says it wants one (and may say where its metadata is).
func probeNeedsAuth(ctx context.Context, mcpURL string) (needs bool, meta string, err error) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"openrepl-genie","version":"1"}}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "openrepl-genie-connection")
	resp, err := probeClient().Do(req)
	if err != nil {
		return false, "", fmt.Errorf("the server could not be reached")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if m := wwwAuthMeta.FindStringSubmatch(resp.Header.Get("WWW-Authenticate")); m != nil {
			meta = m[1]
		}
		return true, meta, nil
	}
	return false, "", nil
}

// fetchMetadata reads a small JSON document of a well-known address (https, public address, same rules as every call).
func fetchMetadata(ctx context.Context, raw string) (map[string]interface{}, error) {
	if _, err := integrationURLOK(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "openrepl-genie-connection")
	resp, err := probeClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %d", resp.StatusCode)
	}
	var m map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func strOf(m map[string]interface{}, k string) string { s, _ := m[k].(string); return s }
func strsOf(m map[string]interface{}, k string) []string {
	var out []string
	if a, ok := m[k].([]interface{}); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// detectSignIn reads what a server says about how people sign in to it: whether it wants a credential, which server issues them (RFC 9728), and
// that server's endpoints (RFC 8414), and above all whether it lets Genie register itself (RFC 7591).
func detectSignIn(ctx context.Context, mcpURL string) signInInfo {
	var si signInInfo
	needs, meta, err := probeNeedsAuth(ctx, mcpURL)
	if err != nil {
		si.Detail = err.Error()
		si.Mode = "unknown"
		return si
	}
	if !needs {
		si.Mode = "none"
		si.Detail = "the server answered without a credential"
		return si
	}
	si.NeedsAuth, si.ResourceMetadata = true, meta
	u, err := url.Parse(mcpURL)
	if err != nil {
		si.Mode = "token"
		return si
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.Path, "/")
	var cands []string
	if meta != "" {
		cands = append(cands, meta)
	}
	cands = append(cands, origin+"/.well-known/oauth-protected-resource"+path, origin+"/.well-known/oauth-protected-resource")
	issuer := ""
	for _, c := range cands {
		if m, err := fetchMetadata(ctx, c); err == nil {
			if as := strsOf(m, "authorization_servers"); len(as) > 0 {
				issuer = as[0]
				si.Scopes = strsOf(m, "scopes_supported")
				break
			}
		}
	}
	if issuer == "" {
		issuer = origin // a server that is its own authorization server
	}
	iu, err := url.Parse(issuer)
	if err != nil || iu.Host == "" {
		si.Mode, si.Detail = "token", "no sign-in was found: the server wants a token"
		return si
	}
	ipath := strings.TrimRight(iu.Path, "/")
	asCands := []string{iu.Scheme + "://" + iu.Host + "/.well-known/oauth-authorization-server" + ipath,
		strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"}
	for _, c := range asCands {
		m, err := fetchMetadata(ctx, c)
		if err != nil {
			continue
		}
		si.AuthURL, si.TokenURL, si.RegistrationURL = strOf(m, "authorization_endpoint"), strOf(m, "token_endpoint"), strOf(m, "registration_endpoint")
		si.Issuer = strOf(m, "issuer")
		if len(si.Scopes) == 0 {
			si.Scopes = strsOf(m, "scopes_supported")
		}
		if b, ok := m["authorization_response_iss_parameter_supported"].(bool); ok {
			si.IssParam = b
		}
		pkce := false
		for _, x := range strsOf(m, "code_challenge_methods_supported") {
			if x == "S256" {
				pkce = true
			}
		}
		_, _ = integrationURLOK(si.AuthURL)
		endpointsOK := si.AuthURL != "" && si.TokenURL != ""
		for _, e := range []string{si.AuthURL, si.TokenURL, si.RegistrationURL} {
			if e != "" {
				if _, err := integrationURLOK(e); err != nil {
					endpointsOK = false
				}
			}
		}
		switch {
		case endpointsOK && si.RegistrationURL != "" && pkce:
			si.Mode, si.Detail = "auto", "the server offers registration: nothing to fill in"
		case endpointsOK:
			si.Mode, si.Detail = "oauth", "the server has a sign-in but does not let Genie register: an OAuth app has to be made by hand"
		default:
			si.Mode, si.Detail = "token", "the sign-in of the server is not usable here: a token"
		}
		return si
	}
	si.Mode, si.Detail = "token", "no sign-in was found: the server wants a token"
	return si
}

// registerClient registers Genie with a server that offers it (RFC 7591), once for each address of this site, and keeps what it gave.
var regMu sync.Mutex

func registerClient(def connectionDef, redirect string) (string, string, error) {
	regMu.Lock()
	defer regMu.Unlock()
	if cid, sec := clientFor(def.ID, def.OAuth, redirect); cid != "" {
		return cid, sec, nil
	}
	i, ok := findIntegration(def.ID)
	if !ok || i.Auth.RegistrationURL == "" {
		return "", "", fmt.Errorf("signing in to %s is not set up on this site", def.Title)
	}
	if _, err := integrationURLOK(i.Auth.RegistrationURL); err != nil {
		return "", "", fmt.Errorf("the registration address of %s is not usable", def.Title)
	}
	body, _ := json.Marshal(map[string]interface{}{"client_name": "OpenREPL Genie", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
	req, err := http.NewRequest(http.MethodPost, i.Auth.RegistrationURL, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "openrepl-genie-connection")
	resp, err := oauthHTTP().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("%s could not be reached to register", def.Title)
	}
	defer resp.Body.Close()
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 || json.Unmarshal(b, &out) != nil || out.ClientID == "" || len(out.ClientID) > 400 {
		return "", "", fmt.Errorf("%s did not let Genie register", def.Title)
	}
	sealed := ""
	if out.ClientSecret != "" && utils.Secret() != "" {
		sealed, _ = sealKey(utils.Secret(), integSecretAAD(def.ID, "client:"+redirect), out.ClientSecret)
	}
	err = updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
		k := integIndex(*list, def.ID)
		if k < 0 {
			return fmt.Errorf("the integration is gone")
		}
		if (*list)[k].Auth.Clients == nil {
			(*list)[k].Auth.Clients = map[string]RegisteredClient{}
		}
		(*list)[k].Auth.Clients[redirect] = RegisteredClient{ClientID: out.ClientID, SecretSealed: sealed, At: time.Now().UTC().Format(time.RFC3339)}
		return nil
	})
	if err != nil {
		return "", "", fmt.Errorf("the registration could not be kept")
	}
	return out.ClientID, out.ClientSecret, nil
}

// ---- the tools: what a test found, what the admin decided ---------------------------------------------------------------------------------------

// mergeToolPolicy puts what a test found next to what the admin decided. A tool not seen before is off and marked new; one the server changed since
// the admin looked goes off and is marked changed; one that is gone is dropped.
func mergeToolPolicy(old []ToolPolicy, live []mcpTool, now string) (out []ToolPolicy, skipped int) {
	byName := map[string]ToolPolicy{}
	for _, p := range old {
		byName[p.Name] = p
	}
	for _, t := range live {
		if !connToolPattern.MatchString(t.Name) {
			skipped++
			continue
		}
		d := toolDigest(t)
		p, ok := byName[t.Name]
		switch {
		case !ok:
			p = ToolPolicy{Name: t.Name, Digest: d, New: true, FirstSeen: now}
		case p.Digest != d:
			p.Digest, p.Enabled, p.Changed = d, false, true
		}
		p.LastSeen = now
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, skipped
}

// applySafeDefaults switches on the tools that are new and that the server calls read-only, at the level read. Everything else stays off.
func applySafeDefaults(tools []ToolPolicy, live []mcpTool) []ToolPolicy {
	ro := map[string]bool{}
	for _, t := range live {
		if t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint && (t.Annotations.DestructiveHint == nil || !*t.Annotations.DestructiveHint) {
			ro[t.Name] = true
		}
	}
	for k := range tools {
		if tools[k].New && ro[tools[k].Name] {
			tools[k].Enabled, tools[k].Level, tools[k].New = true, "read", false
		}
	}
	return tools
}

func policyOn(tools []ToolPolicy) (on int) {
	for _, t := range tools {
		if t.Enabled {
			on++
		}
	}
	return on
}

// ---- the test ----------------------------------------------------------------------------------------------------------------------------------

// testIntegration reads a server: how it signs people in, what it speaks, and its tools. It makes no call of a tool. It lists the tools with the
// credential an admin gave it for this (the discovery token), else the admin's own sign-in to it; without one, a server that wants a credential is
// only looked at. defaults switches on the read-only tools that are new (the one-click approval of a request).
func testIntegration(ctx context.Context, id, adminUID string, defaults bool) (Integration, error) {
	i, ok := findIntegration(id)
	if !ok {
		return i, fmt.Errorf("there is no such integration")
	}
	si := detectSignIn(ctx, i.URL)
	res := &IntegrationTest{At: time.Now().UTC().Format(time.RFC3339), SignIn: si.Mode, NeedsAuth: si.NeedsAuth, Detail: si.Detail}
	token := integrationSecret(id, secretDiscovery)
	source := ""
	if token != "" {
		source = "the discovery token"
	} else if adminUID != "" {
		if _, tok, ok := connToken(adminUID, id); ok {
			token, source = tok, "your own sign-in"
		}
	}
	var live []mcpTool
	switch {
	case si.Mode == "unknown":
		res.Error = si.Detail
	case si.NeedsAuth && token == "":
		res.Error = "The server wants a credential to list its tools. Sign in to it yourself (Connect, in the catalogue) and test again, or give a discovery token."
	default:
		sess, err := dialMCP(ctx, i.URL, token)
		if err != nil {
			res.Error = "The server did not accept the test: " + err.Error()
			break
		}
		live, err = sess.listTools(ctx)
		sess.close()
		if err != nil {
			res.Error = "The tools could not be listed: " + err.Error()
			break
		}
		res.OK = true
		res.Protocol = "2025-06-18"
		mcpModes.Lock()
		if mcpModes.m[i.URL] == "modern" {
			res.Protocol = mcpVersion2026
		}
		mcpModes.Unlock()
		if source != "" {
			res.Detail = strings.TrimSpace(res.Detail + " (tools listed with " + source + ")")
		}
	}
	err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
		k := integIndex(*list, id)
		if k < 0 {
			return fmt.Errorf("the integration is gone")
		}
		cur := &(*list)[k]
		if res.OK {
			var skipped int
			cur.Tools, skipped = mergeToolPolicy(cur.Tools, live, res.At)
			res.Skipped, res.Tools = skipped, len(cur.Tools)
			if defaults {
				cur.Tools = applySafeDefaults(cur.Tools, live)
			}
		}
		// what the test found about the sign-in, unless the admin set it by hand
		if cur.Auth.Detected || (cur.Auth.ClientID == "" && cur.Auth.ClientIDEnv == "" && cur.Auth.AuthURL == "" && cur.LastTest == nil) {
			switch si.Mode {
			case "auto":
				cur.Auth = IntegrationAuth{Mode: "oauth", Auto: true, AuthURL: si.AuthURL, TokenURL: si.TokenURL, RegistrationURL: si.RegistrationURL, Issuer: si.Issuer,
					RequireIssuer: si.IssParam, Scopes: cur.Auth.Scopes, Clients: cur.Auth.Clients, Detected: true}
			case "oauth":
				cur.Auth = IntegrationAuth{Mode: "oauth", AuthURL: si.AuthURL, TokenURL: si.TokenURL, Issuer: si.Issuer, RequireIssuer: si.IssParam, Scopes: cur.Auth.Scopes, Detected: true}
			case "none":
				cur.Auth = IntegrationAuth{Mode: "none", Detected: true}
			case "token":
				cur.Auth = IntegrationAuth{Mode: "token", Detected: true}
			}
		}
		cur.LastTest = res
		if cur.Status == integDraft && (res.OK || res.NeedsAuth) {
			cur.Status = integTested
		}
		cur.UpdatedAt = res.At
		i = *cur
		return nil
	})
	return i, err
}

// ---- the routes -----------------------------------------------------------------------------------------------------------------------------------

type integrationView struct {
	Integration
	Usage       integrationUsage `json:"usage"`
	Secrets     map[string]bool  `json:"secrets"`
	Publishable string           `json:"publishable"`
	ToolsOn     int              `json:"toolsOn"`
}

func viewOfIntegration(i Integration, use map[string]integrationUsage, sec map[string]IntegrationSecret) integrationView {
	if len(i.Auth.Clients) > 0 {
		cl := map[string]RegisteredClient{}
		for k, c := range i.Auth.Clients {
			cl[k] = RegisteredClient{ClientID: c.ClientID, At: c.At}
		}
		i.Auth.Clients = cl
	}
	s := sec[i.ID]
	return integrationView{Integration: i, Usage: use[i.ID], Secrets: map[string]bool{secretOAuthClient: s.OAuthClientSecret != "", secretDiscovery: s.DiscoveryToken != ""},
		Publishable: i.publishable(), ToolsOn: policyOn(i.Tools)}
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugID(title string, taken func(string) bool) string {
	base := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if base == "" {
		base = "server"
	}
	if len(base) > 34 {
		base = strings.Trim(base[:34], "-")
	}
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

type integrationOp struct {
	Op          string       `json:"op"`
	ID          string       `json:"id"`
	Integration *Integration `json:"integration"`
	Tools       []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Level   string `json:"level"`
		Note    string `json:"note"`
	} `json:"tools"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// GET admin/integrations: every integration with how many people use it; POST: one of the ops (save, test, tools, publish, unpublish, delete, secret).
func (server *Server) handleAdminIntegrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s := GetSiteSettings()
		list := registry()
		use := usageOfIntegrations()
		views := make([]integrationView, 0, len(list))
		for _, i := range list {
			views = append(views, viewOfIntegration(i, use, GetSiteSettings().IntegrationSecrets))
		}
		_ = s
		adminJSON(w, http.StatusOK, map[string]interface{}{"integrations": views, "canStore": utils.Secret() != "", "redirect": publicBase(r) + strings.TrimSuffix(strings.TrimSuffix(r.URL.Path, "admin/integrations"), "/") + "/genie_conn_oauth",
			"requests": countWaitingServers(readConnRequests())})
	case http.MethodPost:
		var in integrationOp
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			adminError(w, http.StatusBadRequest, "Send {op, ...}.")
			return
		}
		server.integrationOp(w, r, in)
	default:
		w.Header().Set("Allow", "GET, POST")
		adminError(w, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}

func (server *Server) integrationReply(w http.ResponseWriter, id string) {
	i, _ := findIntegration(id)
	adminJSON(w, http.StatusOK, map[string]interface{}{"integration": viewOfIntegration(i, usageOfIntegrations(), GetSiteSettings().IntegrationSecrets)})
}

func (server *Server) integrationOp(w http.ResponseWriter, r *http.Request, in integrationOp) {
	adminUID := cookie.Get_Uid(r)
	adminEmail, _ := personOf(adminUID)
	now := time.Now().UTC().Format(time.RFC3339)
	fail := func(status int, msg string) { adminError(w, status, msg) }
	switch in.Op {
	case "save":
		if in.Integration == nil {
			fail(http.StatusBadRequest, "Send the integration.")
			return
		}
		ed := *in.Integration
		var saved string
		err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
			k := -1
			if ed.ID != "" {
				k = integIndex(*list, ed.ID)
			}
			if k < 0 {
				if len(*list) >= maxIntegrations {
					return fmt.Errorf("at most %d servers", maxIntegrations)
				}
				if ed.ID == "" {
					ed.ID = slugID(ed.Title, func(id string) bool { return integIndex(*list, id) >= 0 })
				} else if integIndex(*list, ed.ID) >= 0 {
					return fmt.Errorf("that id is taken")
				}
			}
			n, err := normalizeIntegration(ed)
			if err != nil {
				return err
			}
			if k < 0 {
				n.Status, n.CreatedBy, n.CreatedAt, n.UpdatedAt = integDraft, adminEmail, now, now
				n.Tools, n.LastTest, n.LegacyLevels = nil, nil, nil
				n.Auth.Detected = false
				*list = append(*list, n)
			} else {
				old := (*list)[k]
				n.Status, n.Tools, n.LastTest, n.LegacyLevels, n.CreatedBy, n.CreatedAt, n.FromRequest = old.Status, old.Tools, old.LastTest, old.LegacyLevels, old.CreatedBy, old.CreatedAt, old.FromRequest
				n.Auth.Clients = old.Auth.Clients
				n.Auth.Detected = old.Auth.Detected && sameAuth(old.Auth, n.Auth) // what the admin changed by hand is theirs: a test does not fill it again
				if n.URL != old.URL {
					// another server: what was found about the old one does not hold, and nobody keeps using the old address
					n.Tools, n.LastTest, n.Status = nil, nil, integDraft
					n.Auth.Clients = nil
				}
				n.UpdatedAt = now
				(*list)[k] = n
			}
			saved = n.ID
			return nil
		})
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		server.audit(r, "integration", "saved "+saved)
		dropCachedToolsFor(saved)
		server.integrationReply(w, saved)
	case "test":
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		i, err := testIntegration(ctx, in.ID, adminUID, false)
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		result := "ok"
		if i.LastTest != nil && !i.LastTest.OK {
			result = "not ok"
		}
		server.audit(r, "integration", "tested "+in.ID+": "+result)
		dropCachedToolsFor(in.ID)
		server.integrationReply(w, in.ID)
	case "tools":
		err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
			k := integIndex(*list, in.ID)
			if k < 0 {
				return fmt.Errorf("there is no such integration")
			}
			byName := map[string]int{}
			for n, t := range (*list)[k].Tools {
				byName[t.Name] = n
			}
			for _, t := range in.Tools {
				n, ok := byName[t.Name]
				if !ok {
					return fmt.Errorf("%s is not a tool of this server", clipRunes(t.Name, 40))
				}
				if t.Enabled && levelOfPolicy[t.Level] == "" {
					return fmt.Errorf("%s needs a level (read, write or restricted) before it can be on", t.Name)
				}
				p := &(*list)[k].Tools[n]
				p.Enabled, p.Note, p.New, p.Changed = t.Enabled, clipRunes(strings.TrimSpace(t.Note), 120), false, false
				if levelOfPolicy[t.Level] != "" {
					p.Level = t.Level
				}
			}
			(*list)[k].UpdatedAt = now
			return nil
		})
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		server.audit(r, "integration", "tools of "+in.ID+" changed")
		dropCachedToolsFor(in.ID)
		server.integrationReply(w, in.ID)
	case "publish", "unpublish":
		err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
			k := integIndex(*list, in.ID)
			if k < 0 {
				return fmt.Errorf("there is no such integration")
			}
			if in.Op == "publish" {
				if why := (*list)[k].publishable(); why != "" {
					return fmt.Errorf("%s", strings.TrimSuffix(why, "."))
				}
				(*list)[k].Status = integPublished
			} else {
				(*list)[k].Status = integOff
			}
			(*list)[k].UpdatedAt = now
			return nil
		})
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		server.audit(r, "integration", in.Op+"ed "+in.ID)
		for _, d := range []string{in.ID} {
			dropCachedToolsFor(d)
		}
		server.integrationReply(w, in.ID)
	case "delete":
		err := updateRegistry(func(list *[]Integration, secrets map[string]IntegrationSecret) error {
			k := integIndex(*list, in.ID)
			if k < 0 {
				return fmt.Errorf("there is no such integration")
			}
			if (*list)[k].Status == integPublished {
				return fmt.Errorf("switch it off first")
			}
			*list = append((*list)[:k], (*list)[k+1:]...)
			delete(secrets, in.ID)
			return nil
		})
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		server.audit(r, "integration", "deleted "+in.ID)
		adminJSON(w, http.StatusOK, map[string]interface{}{"deleted": in.ID})
	case "secret":
		if _, ok := findIntegration(in.ID); !ok {
			fail(http.StatusNotFound, "There is no such integration.")
			return
		}
		if (in.Kind != secretOAuthClient && in.Kind != secretDiscovery) || len(in.Value) > 2048 || strings.ContainsAny(in.Value, "\r\n\x00") {
			fail(http.StatusBadRequest, "That is not a value this server keeps.")
			return
		}
		err := updateRegistry(func(_ *[]Integration, secrets map[string]IntegrationSecret) error {
			return setIntegrationSecret(secrets, in.ID, in.Kind, strings.TrimSpace(in.Value))
		})
		if err == errNoSettingsSecret {
			fail(http.StatusServiceUnavailable, "OPENREPL_SECRET is not set on the server, so secrets cannot be kept here.")
			return
		}
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		what := "set"
		if in.Value == "" {
			what = "removed"
		}
		server.audit(r, "integration", in.Kind+" of "+in.ID+" "+what) // never the value
		server.integrationReply(w, in.ID)
	default:
		fail(http.StatusBadRequest, "That is not something integrations can do.")
	}
}

// dropCachedToolsFor forgets the tools of an integration for every person (a server was published, switched off or changed).
func dropCachedToolsFor(id string) {
	toolsCache.Lock()
	for k := range toolsCache.m {
		if strings.HasSuffix(k, "|"+id) {
			delete(toolsCache.m, k)
		}
	}
	toolsCache.Unlock()
}

func countWaitingServers(all []connRequest) int {
	n := 0
	for _, r := range all {
		if r.Status == "waiting" && r.Kind == "server" {
			n++
		}
	}
	return n
}

// sameAuth says that two sign-ins are the same but for what a test fills in or Genie registers.
func sameAuth(a, b IntegrationAuth) bool {
	a.Detected, b.Detected, a.Clients, b.Clients = false, false, nil, nil
	return reflect.DeepEqual(a, b)
}
