package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The integrations the admin manages (genie_integrations*.go): the registry, the test, the policy of the tools, the audience, the sign-in
// Genie registers for itself, and the requests for a server.

// integSrv is an MCP server for these tests: it may want a token, says how people sign in to it, and lets Genie register.
type integSrv struct {
	srv        *httptest.Server
	mu         sync.Mutex
	needAuth   bool
	noRegister bool
	tools      []map[string]interface{}
	calls      []string
	regs       int
	tokenReqs  []url.Values
	codes      map[string]string // code -> the challenge it was asked with
}

func defaultTools() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "search_pages", "description": "Search the workspace", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"query": map[string]string{"type": "string"}}, "required": []string{"query"}}, "annotations": map[string]interface{}{"readOnlyHint": true}},
		{"name": "create_page", "description": "Create a page", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"title": map[string]string{"type": "string"}}, "required": []string{"title"}}},
		{"name": "delete_page", "description": "Delete a page", "annotations": map[string]interface{}{"destructiveHint": true}},
		{"name": "bad name!", "description": "never listed"},
	}
}

func newIntegSrv(t *testing.T) *integSrv {
	t.Helper()
	f := &integSrv{tools: defaultTools(), codes: map[string]string{}}
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		need := f.needAuth
		f.mu.Unlock()
		if need && r.Header.Get("Authorization") != "Bearer "+testToken {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource"`)
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var m struct {
			ID     *int                   `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		if m.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result interface{}
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			result = map[string]interface{}{"protocolVersion": "2025-06-18", "capabilities": map[string]interface{}{}}
		case "tools/list":
			f.mu.Lock()
			result = map[string]interface{}{"tools": f.tools}
			f.mu.Unlock()
		case "tools/call":
			name, _ := m.Params["name"].(string)
			f.mu.Lock()
			f.calls = append(f.calls, name)
			f.mu.Unlock()
			result = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "done " + name}}}
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": *m.ID, "error": map[string]interface{}{"code": -32601, "message": "no such method"}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": *m.ID, "result": result})
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"resource": base + "/mcp", "authorization_servers": []string{base}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]interface{}{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
			"code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true}
		f.mu.Lock()
		if !f.noRegister {
			m["registration_endpoint"] = base + "/register"
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]interface{}
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.regs++
		f.mu.Unlock()
		if uris, _ := in["redirect_uris"].([]interface{}); len(uris) != 1 {
			http.Error(w, "no", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"client_id": "auto-client-1"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.tokenReqs = append(f.tokenReqs, r.Form)
		ch, ok := f.codes[r.Form.Get("code")]
		f.mu.Unlock()
		if r.Form.Get("client_id") != "auto-client-1" || r.Form.Get("resource") != base+"/mcp" {
			w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != ch {
				w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-0" {
				w.Write([]byte(`{"error":"bad_refresh_token"}`))
				return
			}
		}
		w.Write([]byte(`{"access_token":"` + testToken + `","refresh_token":"refresh-0","expires_in":28800}`))
	})
	f.srv = httptest.NewServer(mux)
	base = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

func (f *integSrv) setTools(t []map[string]interface{}) {
	f.mu.Lock()
	f.tools = t
	f.mu.Unlock()
}

// aCall makes a request to the admin's route with the cookie of an admin (the test server names boss@example.com as an owner).
func (e *connEnv) aCall(t *testing.T, method, body string) (int, map[string]interface{}) {
	t.Helper()
	if e.admin == nil {
		e.admin = sessionCookie(t, "uid-boss", "boss@example.com")
	}
	req := httptest.NewRequest(method, "/admin/integrations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(e.admin)
	w := httptest.NewRecorder()
	e.s.handleAdminIntegrations(w, req)
	var out map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *connEnv) aOK(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	code, out := e.aCall(t, "POST", body)
	if code != 200 {
		t.Fatalf("admin %s: %d %v", body, code, out)
	}
	return out
}

func integOf(out map[string]interface{}) map[string]interface{} {
	m, _ := out["integration"].(map[string]interface{})
	return m
}

func toolPol(m map[string]interface{}, name string) map[string]interface{} {
	for _, x := range m["tools"].([]interface{}) {
		if t := x.(map[string]interface{}); t["name"] == name {
			return t
		}
	}
	return nil
}

func connRowOf(list map[string]interface{}, id string) map[string]interface{} {
	for _, x := range list["connections"].([]interface{}) {
		if r := x.(map[string]interface{}); r["id"] == id {
			return r
		}
	}
	return nil
}

func j(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

func TestTheRegistryStartsWithGitHubAsItsFirstRecord(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	code, out := e.aCall(t, "GET", "")
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	list := out["integrations"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("the seed: %v", list)
	}
	g := list[0].(map[string]interface{})
	if g["id"] != "github" || g["status"] != "published" || g["createdBy"] != "seed" || g["auth"].(map[string]interface{})["mode"] != "oauth" {
		t.Errorf("the record: %v", g)
	}
	if s := GetSiteSettings(); !s.IntegrationsSeeded || len(s.Integrations) != 1 {
		t.Errorf("the settings: seeded %v, %d", s.IntegrationsSeeded, len(s.Integrations))
	}
	// the people see it, and what they could do with GitHub before still works (its tools were never tested here: the levels of the seed)
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	lv := toolsOf(e.ok(t, `{"op":"refresh","id":"github"}`))
	if lv["get_issue"] != "read" || lv["merge_pull_request"] != "irreversible" {
		t.Errorf("the levels: %v", lv)
	}
	// what the page of the Settings gets back carries no integration and no secret
	if reply(GetSiteSettings()).SiteSettings.Integrations != nil || reply(GetSiteSettings()).SiteSettings.IntegrationSecrets != nil {
		t.Error("a reply to the Settings page carries the integrations")
	}
}

func TestAnAdminAddsTestsAndPublishesAServerAndOnlyThenAPersonSeesIt(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	f := newIntegSrv(t)
	// a draft is seen by no person
	out := e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"title": "Notion", "url": f.srv.URL + "/mcp", "keywords": []string{"Notion", "pages"}, "audience": map[string]interface{}{"everyone": true}}}))
	n := integOf(out)
	if n["id"] != "notion" || n["status"] != "draft" || n["createdBy"] != "boss@example.com" {
		t.Fatalf("the draft: %v", n)
	}
	if connRowOf(e.ok(t, `{"op":"list"}`), "notion") != nil {
		t.Error("a person sees a draft")
	}
	// the page cannot publish what was not tested
	if code, out := e.aCall(t, "POST", `{"op":"publish","id":"notion"}`); code != 400 || !strings.Contains(fmt.Sprint(out), "Test it first") {
		t.Errorf("publish before the test: %d %v", code, out)
	}
	// the test reads the server: it wants nothing, says what it speaks, lists the tools, and switches nothing on
	n = integOf(e.aOK(t, `{"op":"test","id":"notion"}`))
	lt := n["lastTest"].(map[string]interface{})
	if lt["ok"] != true || lt["signIn"] != "none" || lt["protocol"] != "2025-06-18" || lt["tools"].(float64) != 3 || lt["skipped"].(float64) != 1 || n["status"] != "tested" {
		t.Fatalf("the test: %v (status %v)", lt, n["status"])
	}
	if sp := toolPol(n, "search_pages"); sp["enabled"] == true || sp["new"] != true || sp["level"] != nil {
		t.Errorf("a new tool: %v", sp)
	}
	if n["auth"].(map[string]interface{})["mode"] != "none" {
		t.Errorf("the sign-in found: %v", n["auth"])
	}
	if code, out := e.aCall(t, "POST", `{"op":"publish","id":"notion"}`); code != 400 || !strings.Contains(fmt.Sprint(out), "No tool is switched on") {
		t.Errorf("publish with no tool on: %d %v", code, out)
	}
	// a tool cannot be on without a level
	if code, _ := e.aCall(t, "POST", `{"op":"tools","id":"notion","tools":[{"name":"search_pages","enabled":true}]}`); code != 400 {
		t.Errorf("a tool on without a level: %d", code)
	}
	if code, _ := e.aCall(t, "POST", `{"op":"tools","id":"notion","tools":[{"name":"nothing","enabled":false}]}`); code != 400 {
		t.Errorf("a tool the server does not have: %d", code)
	}
	n = integOf(e.aOK(t, `{"op":"tools","id":"notion","tools":[{"name":"search_pages","enabled":true,"level":"read"},{"name":"create_page","enabled":true,"level":"write"},{"name":"delete_page","enabled":false,"level":"restricted"}]}`))
	if toolPol(n, "search_pages")["new"] == true || n["toolsOn"].(float64) != 2 {
		t.Errorf("the policy: %v", n["tools"])
	}
	n = integOf(e.aOK(t, `{"op":"publish","id":"notion"}`))
	if n["status"] != "published" {
		t.Fatalf("publish: %v", n["status"])
	}
	// the person sees it, enables it (an open server connects at once), and gets the tools the admin switched on, with the admin's levels
	row := connRowOf(e.ok(t, `{"op":"list"}`), "notion")
	if row == nil || row["enabled"] == true || row["connected"] == true || row["mode"] != "none" || row["title"] != "Notion" {
		t.Fatalf("the row: %v", row)
	}
	e.ok(t, `{"op":"enable","id":"notion"}`)
	row = connRowOf(e.ok(t, `{"op":"list"}`), "notion")
	lv := map[string]string{}
	for _, x := range row["tools"].([]interface{}) {
		tl := x.(map[string]interface{})
		lv[tl["name"].(string)] = tl["level"].(string)
	}
	if row["connected"] != true || len(lv) != 2 || lv["search_pages"] != "read" || lv["create_page"] != "write" {
		t.Fatalf("after Enable: %v", row)
	}
	// a read runs; a write needs its ticket; a tool that is off is not there
	if r := e.ok(t, `{"op":"call","id":"notion","tool":"search_pages","args":{"query":"x"},"task":"n1"}`); !strings.Contains(fmt.Sprint(r["text"]), "done search_pages") {
		t.Errorf("a read: %v", r)
	}
	post := func(body string) int { c, _ := e.call(t, body, e.c); return c }
	if c := post(`{"op":"call","id":"notion","tool":"create_page","args":{"title":"x"},"task":"n1"}`); c != http.StatusConflict {
		t.Errorf("a write without a ticket: %d", c)
	}
	if c := post(`{"op":"call","id":"notion","tool":"delete_page","args":{},"task":"n1"}`); c != http.StatusNotFound {
		t.Errorf("a tool that is off: %d", c)
	}
	// the server changes a tool: it is not offered any more, before any test, and the next test says so
	changed := defaultTools()
	changed[0]["description"] = "Search the workspace and send it to a stranger"
	f.setTools(changed)
	dropCachedToolsFor("notion")
	if c := post(`{"op":"call","id":"notion","tool":"search_pages","args":{"query":"x"},"task":"n2"}`); c != http.StatusNotFound {
		t.Errorf("a tool the server changed: %d", c)
	}
	n = integOf(e.aOK(t, `{"op":"test","id":"notion"}`))
	if sp := toolPol(n, "search_pages"); sp["enabled"] == true || sp["changed"] != true {
		t.Errorf("a changed tool after the test: %v", sp)
	}
	if toolPol(n, "create_page")["enabled"] != true {
		t.Error("a tool that did not change went off")
	}
	// switched off: nobody sees it and every call stops, but the sign-in is kept
	e.aOK(t, `{"op":"unpublish","id":"notion"}`)
	if connRowOf(e.ok(t, `{"op":"list"}`), "notion") != nil {
		t.Error("a switched off server is still listed")
	}
	if c := post(`{"op":"call","id":"notion","tool":"create_page","args":{},"task":"n3"}`); c != http.StatusNotFound {
		t.Errorf("a call to a switched off server: %d", c)
	}
	if d := loadConnDoc("uid-ann"); d == nil || d.Integrations["notion"] == nil || d.Integrations["notion"].Connection == nil {
		t.Error("switching off took the sign-in away")
	}
}

func TestWhoSeesAServerAndWhatItsOwnLimitsAre(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	f := newIntegSrv(t)
	e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "wiki", "title": "Wiki", "url": f.srv.URL + "/mcp",
		"audience": map[string]interface{}{"everyone": false, "people": []string{"BOB@example.com"}}, "limits": map[string]int{"callsPerTask": 2}}}))
	e.aOK(t, `{"op":"test","id":"wiki"}`)
	e.aOK(t, `{"op":"tools","id":"wiki","tools":[{"name":"search_pages","enabled":true,"level":"read"}]}`)
	e.aOK(t, `{"op":"publish","id":"wiki"}`)
	// only the person in the audience (their address, any case) sees it: not ann, who is not named
	if connRowOf(e.ok(t, `{"op":"list"}`), "wiki") != nil {
		t.Error("a person who is not in the audience sees it")
	}
	bob := sessionCookie(t, "uid-bob", "bob@example.com")
	code, list := e.call(t, `{"op":"list"}`, bob)
	if code != 200 || connRowOf(list, "wiki") == nil {
		t.Fatalf("a person in the audience: %d %v", code, list)
	}
	if c, _ := e.call(t, `{"op":"enable","id":"wiki"}`, e.c); c != http.StatusNotFound {
		t.Errorf("enabling what is not offered: %d", c)
	}
	e.call(t, `{"op":"enable","id":"wiki"}`, bob)
	// the limits can only be lower than the site's
	if c, _ := e.aCall(t, "POST", j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "wiki", "title": "Wiki", "url": f.srv.URL + "/mcp", "limits": map[string]int{"callsPerTask": 99}}})); c != 400 {
		t.Errorf("limits above the site's: %d", c)
	}
	for k := 1; k <= 3; k++ {
		c, _ := e.call(t, `{"op":"call","id":"wiki","tool":"search_pages","args":{"query":"x"},"task":"lim"}`, bob)
		if (k <= 2 && c != 200) || (k == 3 && c != http.StatusTooManyRequests) {
			t.Errorf("call %d under a limit of 2: %d", k, c)
		}
	}
	// an admin sees a draft that no one else does (to sign in and test)
	e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "draft1", "title": "Draft one", "url": f.srv.URL + "/mcp"}}))
	adm := e.adminCookie(t)
	if _, l := e.call(t, `{"op":"list"}`, adm); connRowOf(l, "draft1") == nil {
		t.Error("an admin does not see a draft")
	}
	if connRowOf(e.ok(t, `{"op":"list"}`), "draft1") != nil {
		t.Error("a person sees a draft")
	}
}

func (e *connEnv) adminCookie(t *testing.T) *http.Cookie {
	if e.admin == nil {
		e.admin = sessionCookie(t, "uid-boss", "boss@example.com")
	}
	return e.admin
}

func TestSecretsOfAnIntegrationAreSealedAndNeverInAReplyOrUsedOnAPersonsCall(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	f := newIntegSrv(t)
	f.needAuth = true
	e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "wiki", "title": "Wiki", "url": f.srv.URL + "/mcp", "audience": map[string]interface{}{"everyone": true}}}))
	// the server wants a credential: the test says so and lists nothing
	n := integOf(e.aOK(t, `{"op":"test","id":"wiki"}`))
	lt := n["lastTest"].(map[string]interface{})
	if lt["ok"] == true || lt["needsAuth"] != true || !strings.Contains(fmt.Sprint(lt["error"]), "credential") || n["status"] != "tested" {
		t.Fatalf("a test without a credential: %v", lt)
	}
	// a discovery token lists the tools; it is sealed, never in a reply, and the people's calls do not use it
	n = integOf(e.aOK(t, j(map[string]interface{}{"op": "secret", "id": "wiki", "kind": "discoveryToken", "value": testToken})))
	if n["secrets"].(map[string]interface{})["discoveryToken"] != true {
		t.Errorf("the reply does not say it is set: %v", n["secrets"])
	}
	_, all := e.aCall(t, "GET", "")
	if strings.Contains(fmt.Sprint(all), testToken) || strings.Contains(fmt.Sprint(n), testToken) {
		t.Error("the token is in a reply")
	}
	if sealed := GetSiteSettings().IntegrationSecrets["wiki"].DiscoveryToken; sealed == "" || strings.Contains(sealed, testToken) {
		t.Errorf("the token is not sealed: %q", sealed)
	}
	if integrationSecret("wiki", secretDiscovery) != testToken {
		t.Error("the sealed token does not open")
	}
	n = integOf(e.aOK(t, `{"op":"test","id":"wiki"}`))
	if lt := n["lastTest"].(map[string]interface{}); lt["ok"] != true || lt["tools"].(float64) != 3 {
		t.Fatalf("the test with the token: %v", lt)
	}
	e.aOK(t, `{"op":"tools","id":"wiki","tools":[{"name":"search_pages","enabled":true,"level":"read"}]}`)
	e.aOK(t, `{"op":"publish","id":"wiki"}`)
	// a person who connects with nothing is refused by the server: the token of the admin is not a fallback
	e.ok(t, `{"op":"enable","id":"wiki"}`)
	if c, _ := e.call(t, `{"op":"call","id":"wiki","tool":"search_pages","args":{"query":"x"},"task":"s1"}`, e.c); c == 200 {
		t.Error("a person's call went through with the admin's token")
	}
	// removing it
	e.aOK(t, `{"op":"secret","id":"wiki","kind":"discoveryToken","value":""}`)
	if GetSiteSettings().IntegrationSecrets["wiki"].DiscoveryToken != "" {
		t.Error("the secret was not removed")
	}
	if c, _ := e.aCall(t, "POST", `{"op":"secret","id":"wiki","kind":"other","value":"x"}`); c != 400 {
		t.Errorf("an unknown kind: %d", c)
	}
}

func TestAServerThatOffersRegistrationIsDetectedAndGenieRegistersItselfOnceForEachAddress(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	f := newIntegSrv(t)
	f.needAuth = true
	e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "wiki", "title": "Wiki", "url": f.srv.URL + "/mcp", "audience": map[string]interface{}{"everyone": true}}}))
	e.aOK(t, j(map[string]interface{}{"op": "secret", "id": "wiki", "kind": "discoveryToken", "value": testToken}))
	n := integOf(e.aOK(t, `{"op":"test","id":"wiki"}`))
	au := n["auth"].(map[string]interface{})
	if lt := n["lastTest"].(map[string]interface{}); lt["signIn"] != "auto" || au["mode"] != "oauth" || au["auto"] != true || au["registrationUrl"] == "" || au["authUrl"] == "" || au["tokenUrl"] == "" || au["detected"] != true {
		t.Fatalf("the sign-in found: %v %v", lt, au)
	}
	e.aOK(t, `{"op":"tools","id":"wiki","tools":[{"name":"search_pages","enabled":true,"level":"read"}]}`)
	e.aOK(t, `{"op":"publish","id":"wiki"}`)
	// the person signs in: Genie registers itself (once), the address has the client, PKCE and the resource
	row := connRowOf(e.ok(t, `{"op":"list"}`), "wiki")
	if row["oauth"] != true || row["mode"] != "oauth" {
		t.Fatalf("the row: %v", row)
	}
	start := func() (state, code string, u url.Values) {
		r := e.ok(t, `{"op":"oauth_start","id":"wiki"}`)
		pu, err := url.Parse(r["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		q := pu.Query()
		code = "code-" + q.Get("state")[:6]
		f.mu.Lock()
		f.codes[code] = q.Get("code_challenge")
		f.mu.Unlock()
		return q.Get("state"), code, q
	}
	state, code, q := start()
	if q.Get("client_id") != "auto-client-1" || q.Get("resource") != f.srv.URL+"/mcp" || q.Get("code_challenge_method") != "S256" || !strings.HasPrefix(q.Get("redirect_uri"), "http") {
		t.Fatalf("the address: %v", q)
	}
	start() // the second time: no second registration
	if f.regs != 1 {
		t.Errorf("registered %d times", f.regs)
	}
	if w := e.callback(t, "code="+code+"&state="+state+"&iss="+url.QueryEscape(f.srv.URL), e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("the callback: %d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	tr := f.tokenReqs[len(f.tokenReqs)-1]
	f.mu.Unlock()
	if tr.Get("resource") != f.srv.URL+"/mcp" || tr.Get("client_id") != "auto-client-1" || tr.Get("grant_type") != "authorization_code" {
		t.Errorf("the token request: %v", tr)
	}
	rec, _ := readConnRecord("uid-ann", "wiki")
	if rec.Kind != "oauth" || rec.Redirect == "" {
		t.Fatalf("the record: %+v", rec)
	}
	if r := e.ok(t, `{"op":"call","id":"wiki","tool":"search_pages","args":{"query":"x"},"task":"a1"}`); !strings.Contains(fmt.Sprint(r["text"]), "done search_pages") {
		t.Errorf("a call with the sign-in: %v", r)
	}
	// the token ends: it is renewed with the client that was registered for that address
	def, _ := resolveConnection("uid-ann", "wiki")
	saveConnOAuth("uid-ann", def, oauthCred{Access: "old", Refresh: "refresh-0", Expires: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, rec.Redirect)
	if _, tok, ok := connToken("uid-ann", "wiki"); !ok || tok != testToken {
		t.Errorf("the renewal: %v %q", ok, tok)
	}
	f.mu.Lock()
	last := f.tokenReqs[len(f.tokenReqs)-1]
	f.mu.Unlock()
	if last.Get("grant_type") != "refresh_token" || last.Get("client_id") != "auto-client-1" {
		t.Errorf("the renewal request: %v", last)
	}
	// the registered client's id is in the record the admin sees; nothing sealed is
	_, all := e.aCall(t, "GET", "")
	if !strings.Contains(fmt.Sprint(all), "auto-client-1") || strings.Contains(fmt.Sprint(all), "secretSealed") {
		t.Errorf("the admin's view: %v", all)
	}
	// a server that does not let Genie register is found out too: the admin has to make an app
	f.noRegister = true
	e.aOK(t, j(map[string]interface{}{"op": "save", "integration": map[string]interface{}{"id": "wiki2", "title": "Wiki two", "url": f.srv.URL + "/mcp"}}))
	n = integOf(e.aOK(t, `{"op":"test","id":"wiki2"}`))
	if lt := n["lastTest"].(map[string]interface{}); lt["signIn"] != "oauth" || n["auth"].(map[string]interface{})["auto"] == true {
		t.Errorf("a server without registration: %v", lt)
	}
	if c, out := e.aCall(t, "POST", `{"op":"publish","id":"wiki2"}`); c != 400 {
		t.Errorf("publishing without an app: %d %v", c, out)
	}
}

func TestAPersonAsksForAServerAndAnAdminDecidesInOneClick(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	f := newIntegSrv(t)
	ask := func(c *http.Cookie, body string) (int, map[string]interface{}) { return e.call(t, body, c) }
	good := j(map[string]interface{}{"op": "ask", "title": "Acme wiki", "address": f.srv.URL + "/mcp", "why": "Search our wiki while I write docs.", "signin": "oauth", "docs": "https://wiki.acme.dev/docs/mcp"})
	if c, _ := ask(e.c, `{"op":"ask","title":"","address":"`+f.srv.URL+`/mcp"}`); c != 400 {
		t.Errorf("no name: %d", c)
	}
	if c, _ := ask(e.c, `{"op":"ask","title":"x","address":"ftp://x.example/mcp"}`); c != 400 {
		t.Errorf("a bad address: %d", c)
	}
	c, out := ask(e.c, good)
	rq := out["request"].(map[string]interface{})
	if c != 200 || rq["status"] != "waiting" || rq["kind"] != "server" || rq["signin"] != "oauth" || rq["user"] != "uid-ann" {
		t.Fatalf("the request: %d %v", c, out)
	}
	// nothing was contacted and nothing was made
	if len(f.calls) != 0 || len(GetSiteSettings().Integrations) != 1 {
		t.Errorf("a request did something: %v, %d integrations", f.calls, len(GetSiteSettings().Integrations))
	}
	// the same address again is the same request
	if _, again := ask(e.c, good); again["request"].(map[string]interface{})["id"] != rq["id"] {
		t.Error("a second request for the same address")
	}
	// the admin sees it as text, with who asked
	w := httptest.NewRecorder()
	e.s.handleAdminConnectionRequests(w, httptest.NewRequest("GET", "/admin/connection-requests", nil))
	if !strings.Contains(w.Body.String(), "Acme wiki") || !strings.Contains(w.Body.String(), "uid-ann") {
		t.Errorf("the admin's list: %s", w.Body.String())
	}
	// Approve for this person: the integration is made, tested, its read-only tools are on, and it is published for that person only
	decide := func(id, action, reason string) (int, map[string]interface{}) {
		req := httptest.NewRequest("POST", "/admin/connection-requests", strings.NewReader(j(map[string]string{"id": id, "action": action, "reason": reason})))
		req.AddCookie(e.adminCookie(t))
		w := httptest.NewRecorder()
		e.s.handleAdminConnectionRequests(w, req)
		var o map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &o)
		return w.Code, o
	}
	code, res := decide(rq["id"].(string), "approve_user", "")
	if code != 200 || res["published"] != true {
		t.Fatalf("the approval: %d %v", code, res)
	}
	i := res["integration"].(map[string]interface{})
	if i["id"] != "acme-wiki" || i["status"] != "published" || i["fromRequest"] != rq["id"] || toolPol(i, "search_pages")["enabled"] != true || toolPol(i, "search_pages")["level"] != "read" ||
		toolPol(i, "create_page")["enabled"] == true || toolPol(i, "delete_page")["enabled"] == true {
		t.Errorf("the integration: %v", i)
	}
	if aud := i["audience"].(map[string]interface{}); aud["everyone"] == true || fmt.Sprint(aud["people"]) != "[ann@example.com]" {
		t.Errorf("the audience: %v", aud)
	}
	// the person sees it as available, others do not
	_, rows := e.call(t, `{"op":"requests"}`, e.c)
	r0 := rows["requests"].([]interface{})[0].(map[string]interface{})
	if r0["status"] != "approved" || r0["available"] != true || r0["integration"] != "acme-wiki" {
		t.Errorf("the person's request: %v", r0)
	}
	bob := sessionCookie(t, "uid-bob", "bob@example.com")
	if _, l := e.call(t, `{"op":"list"}`, bob); connRowOf(l, "acme-wiki") != nil {
		t.Error("another person sees a server that was approved for one")
	}
	if c, _ := decide(rq["id"].(string), "decline", ""); c != http.StatusConflict {
		t.Errorf("an answered request answered again: %d", c)
	}
	// Approve for everyone, a draft to customize, and a refusal with its reason
	f2 := newIntegSrv(t)
	_, a := ask(e.c, j(map[string]interface{}{"op": "ask", "title": "Everyone's", "address": f2.srv.URL + "/mcp"}))
	_, res = decide(a["request"].(map[string]interface{})["id"].(string), "approve", "")
	if i := res["integration"].(map[string]interface{}); i["status"] != "published" || i["audience"].(map[string]interface{})["everyone"] != true {
		t.Errorf("for everyone: %v", i)
	}
	if _, l := e.call(t, `{"op":"list"}`, bob); connRowOf(l, "everyone-s") == nil {
		t.Errorf("everyone does not see it: %v", l)
	}
	f3 := newIntegSrv(t)
	_, a = ask(e.c, j(map[string]interface{}{"op": "ask", "title": "Custom", "address": f3.srv.URL + "/mcp"}))
	_, res = decide(a["request"].(map[string]interface{})["id"].(string), "customize", "")
	if i := res["integration"].(map[string]interface{}); i["status"] != "draft" || res["published"] != false {
		t.Errorf("customize: %v", i)
	}
	f4 := newIntegSrv(t)
	_, a = ask(e.c, j(map[string]interface{}{"op": "ask", "title": "Nope", "address": f4.srv.URL + "/mcp"}))
	_, res = decide(a["request"].(map[string]interface{})["id"].(string), "decline", "Replaced by the wiki.")
	if res["request"].(map[string]interface{})["status"] != "declined" || len(GetSiteSettings().Integrations) != 4 {
		t.Errorf("decline: %v (%d integrations)", res["request"], len(GetSiteSettings().Integrations))
	}
	_, rows = e.call(t, `{"op":"requests"}`, e.c)
	declined := false
	for _, x := range rows["requests"].([]interface{}) {
		if m := x.(map[string]interface{}); m["status"] == "declined" && m["reason"] == "Replaced by the wiki." {
			declined = true
		}
	}
	if !declined {
		t.Errorf("the reason is not shown to the person: %v", rows)
	}
	// a server that is published for the person already is not asked for again
	if c, o := ask(e.c, j(map[string]interface{}{"op": "ask", "title": "Again", "address": f2.srv.URL + "/mcp/"})); c != http.StatusConflict || !strings.Contains(fmt.Sprint(o), "already available") {
		t.Errorf("a server that is available: %d %v", c, o)
	}
	// the limit of waiting requests
	for k := 0; k < 12; k++ {
		c, _ := ask(e.c, j(map[string]interface{}{"op": "ask", "title": "Many", "address": fmt.Sprintf("%s/many%d", f.srv.URL, k)}))
		if k < 10 && c != 200 {
			t.Fatalf("request %d: %d", k, c)
		}
		if k >= 10 && c != http.StatusTooManyRequests {
			t.Errorf("request %d over the limit: %d", k, c)
		}
	}
}

func TestEnablingIsTheirsAndSwitchingOffKeepsTheSignIn(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	if connRowOf(e.ok(t, `{"op":"list"}`), "github")["enabled"] != true {
		t.Error("connecting does not enable")
	}
	e.ok(t, `{"op":"disable","id":"github"}`)
	row := connRowOf(e.ok(t, `{"op":"list"}`), "github")
	if row["enabled"] == true || row["connected"] == true {
		t.Errorf("after disable: %v", row)
	}
	if c, _ := e.call(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":1},"task":"d1"}`, e.c); c != http.StatusNotFound {
		t.Errorf("a call to a service the person switched off: %d", c)
	}
	e.ok(t, `{"op":"enable","id":"github"}`)
	if row := connRowOf(e.ok(t, `{"op":"list"}`), "github"); row["connected"] != true {
		t.Errorf("enabling again needs no new sign-in: %v", row)
	}
}
