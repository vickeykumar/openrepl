package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The review of 2026-11-20: tickets, grants, the same-origin check, the arguments, redirects, what a server speaks, and signing in.

func TestAConnectionRequestFromAnotherSiteOrNotJSONIsRefused(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	cases := []struct {
		name string
		set  func(r *http.Request)
	}{
		{"a form", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }},
		{"no content type", func(r *http.Request) { r.Header.Del("Content-Type") }},
		{"another origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"cross-site fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/genie_conn", strings.NewReader(`{"op":"list"}`))
		req.Header.Set("Content-Type", "application/json")
		c.set(req)
		req.AddCookie(e.c)
		w := httptest.NewRecorder()
		e.s.handleGenieConn(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cross_site") {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	// the page's own request (same host in Origin) goes through
	req := httptest.NewRequest("POST", "/genie_conn", strings.NewReader(`{"op":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(e.c)
	w := httptest.NewRecorder()
	e.s.handleGenieConn(w, req)
	if w.Code != 200 {
		t.Errorf("the page's own request: %d %s", w.Code, w.Body.String())
	}
}

func TestATicketIsUsedOnceAndOnlyForTheCallItWasMadeFor(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	call := func(tool, args, task, ticket string) int {
		code, _ := e.call(t, `{"op":"call","id":"github","tool":"`+tool+`","args":`+args+`,"task":"`+task+`","ticket":"`+ticket+`"}`, e.c)
		return code
	}
	// no ticket, an invented one, and one that was proposed but not answered
	if c := call("create_issue", `{"title":"a"}`, "t1", ""); c != http.StatusConflict {
		t.Errorf("no ticket: %d", c)
	}
	if c := call("create_issue", `{"title":"a"}`, "t1", "0123456789abcdef"); c != http.StatusConflict {
		t.Errorf("an invented ticket: %d", c)
	}
	p := e.ok(t, `{"op":"propose","id":"github","tool":"create_issue","args":{"title":"a"},"task":"t1"}`)
	if p["approved"] == true {
		t.Fatalf("a write is not approved by proposing it: %v", p)
	}
	if c := call("create_issue", `{"title":"a"}`, "t1", p["ticket"].(string)); c != http.StatusConflict {
		t.Errorf("a ticket the person did not answer: %d", c)
	}
	// each thing that differs from what was approved refuses it, and uses it up
	for name, f := range map[string]func(ticket string) int{
		"other arguments": func(tk string) int { return call("create_issue", `{"title":"b"}`, "t1", tk) },
		"other task":      func(tk string) int { return call("create_issue", `{"title":"a"}`, "t2", tk) },
		"other tool":      func(tk string) int { return call("merge_pull_request", `{"title":"a"}`, "t1", tk) },
	} {
		tk := e.allow(t, "create_issue", `{"title":"a"}`, "t1", "once")
		if c := f(tk); c != http.StatusConflict {
			t.Errorf("%s: %d", name, c)
		}
		if c := call("create_issue", `{"title":"a"}`, "t1", tk); c != http.StatusConflict {
			t.Errorf("%s: the ticket was not used up by the refusal: %d", name, c)
		}
	}
	// the right call works once, never twice
	tk := e.allow(t, "create_issue", `{"title":"a"}`, "t1", "once")
	if c := call("create_issue", `{"title":"a"}`, "t1", tk); c != 200 {
		t.Fatalf("the approved call: %d", c)
	}
	if c := call("create_issue", `{"title":"a"}`, "t1", tk); c != http.StatusConflict {
		t.Errorf("the same ticket twice: %d", c)
	}
	// two calls at once with one ticket: one runs
	tk = e.allow(t, "create_issue", `{"title":"c"}`, "t1", "once")
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call("create_issue", `{"title":"c"}`, "t1", tk) == 200 {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Errorf("eight calls with one ticket: %d ran", ok)
	}
	// another person cannot answer or use it
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	tk = e.allow(t, "create_issue", `{"title":"d"}`, "t1", "once")
	if code, _ := e.call(t, `{"op":"approve","ticket":"`+tk+`"}`, other); code != http.StatusConflict {
		t.Errorf("another person answering: %d", code)
	}
}

func TestATicketThatRanOutOrWhoseToolChangedIsRefused(t *testing.T) {
	now := time.Now()
	id, _, err := openTicket(connTicket{uid: "u", task: "t", conn: "github", tool: "x", argsHash: argsHash(nil), digest: "d1", level: "write"}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	approveTicket("u", id, "once", now)
	if err := useTicket("u", id, "t", "github", "x", argsHash(nil), "d1", now.Add(connTicketTTL+time.Second)); err == nil {
		t.Error("a ticket past its time was accepted")
	}
	id, _, _ = openTicket(connTicket{uid: "u", task: "t", conn: "github", tool: "x", argsHash: argsHash(nil), digest: "d1", level: "write"}, false, now)
	approveTicket("u", id, "once", now)
	if err := useTicket("u", id, "t", "github", "x", argsHash(nil), "d2-the-tool-changed", now); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("a tool that changed: %v", err)
	}
}

func TestASessionGrantCoversWritesOnlyAndNotAfterTextFromOutside(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	prop := func(tool, args string, tainted bool) map[string]interface{} {
		tb := "false"
		if tainted {
			tb = "true"
		}
		return e.ok(t, `{"op":"propose","id":"github","tool":"`+tool+`","args":`+args+`,"task":"g1","tainted":`+tb+`}`)
	}
	tk := e.allow(t, "create_issue", `{"title":"first"}`, "g1", "session")
	if c, _ := e.call(t, `{"op":"call","id":"github","tool":"create_issue","args":{"title":"first"},"task":"g1","ticket":"`+tk+`"}`, e.c); c != 200 {
		t.Fatalf("the first write: %d", c)
	}
	// the grant approves the next write at once; a tool that cannot be undone, and a call after text from outside, still need an answer
	if p := prop("create_issue", `{"title":"second"}`, false); p["approved"] != true {
		t.Errorf("a write under the grant: %v", p)
	}
	if p := prop("create_issue", `{"title":"third"}`, true); p["approved"] == true {
		t.Errorf("a write after text from outside was approved by the grant: %v", p)
	}
	for _, tool := range []string{"merge_pull_request", "wipe"} {
		if p := prop(tool, `{}`, false); p["approved"] == true {
			t.Errorf("%s was approved by a grant", tool)
		}
	}
	// a read needs no ticket
	if p := prop("get_issue", `{"number":1}`, false); p["level"] != "read" || p["ticket"] != "" {
		t.Errorf("a read: %v", p)
	}
	// disconnecting ends the grant
	e.ok(t, `{"op":"disconnect","id":"github"}`)
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	if p := prop("create_issue", `{"title":"after"}`, false); p["approved"] == true {
		t.Errorf("the grant outlived the connection: %v", p)
	}
}

func TestAServicesOwnWordCanOnlyMakeAToolOfALinkMoreCareful(t *testing.T) {
	ro, destructive := true, true
	readTool := mcpTool{Name: "look"}
	readTool.Annotations.ReadOnlyHint = &ro
	if l := toolLevel(connectionDef{}, readTool); l != "read" {
		t.Errorf("a service the site ships may say read: %s", l)
	}
	if l := toolLevel(connectionDef{Custom: true}, readTool); l != "write" {
		t.Errorf("a service from a link may not make a tool a read: %s", l)
	}
	boom := mcpTool{Name: "boom"}
	boom.Annotations.DestructiveHint = &destructive
	if l := toolLevel(connectionDef{Custom: true}, boom); l != "irreversible" {
		t.Errorf("it may make one more careful: %s", l)
	}
	if l := toolLevel(connectionDef{Levels: map[string]string{"look": "irreversible"}}, readTool); l != "irreversible" {
		t.Errorf("the catalogue's word is first: %s", l)
	}
}

func TestArgumentsAreCheckedAgainstTheToolsSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},"title":{"type":"string"},"labels":{"type":"array"}},"required":["number"],"additionalProperties":false}`)
	good := map[string]interface{}{"number": float64(3), "title": "x", "labels": []interface{}{"a"}}
	if err := validateArgs(schema, good); err != nil {
		t.Errorf("good arguments: %v", err)
	}
	for name, args := range map[string]map[string]interface{}{
		"missing":    {"title": "x"},
		"wrong type": {"number": "3"},
		"fraction":   {"number": 3.5},
		"unknown":    {"number": float64(1), "extra": true},
		"not array":  {"number": float64(1), "labels": "a"},
	} {
		if err := validateArgs(schema, args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if validateArgs(nil, map[string]interface{}{"a": 1}) != nil || validateArgs(json.RawMessage(`not json`), nil) != nil {
		t.Error("a schema that is absent or unreadable is not a reason to refuse")
	}
}

func TestAServiceThatRedirectsToAnotherHostGetsNoTokenThere(t *testing.T) {
	extAllowLoopback = true
	defer func() { extAllowLoopback = false }()
	var sawB int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&sawB, 1) }))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, b.URL, http.StatusTemporaryRedirect) }))
	defer a.Close()
	c := newMCPClient(a.URL, testToken)
	if _, err := c.connect(context.Background()); err == nil {
		t.Error("a redirect to another host was followed")
	}
	if atomic.LoadInt32(&sawB) != 0 {
		t.Error("the other host was asked")
	}
}

func TestAServerThatSpeaks2026IsNotInitializedAndGetsTheMethodHeadersAndAClassicOneIs(t *testing.T) {
	extAllowLoopback = true
	defer func() { extAllowLoopback = false }()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			ID     *int                   `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		seen = append(seen, m.Method+"|"+r.Header.Get("MCP-Protocol-Version")+"|"+r.Header.Get("Mcp-Method")+"|"+r.Header.Get("Mcp-Name"))
		mu.Unlock()
		var res interface{}
		switch m.Method {
		case "server/discover":
			res = map[string]interface{}{"supportedVersions": []string{"2026-07-28"}}
		case "tools/list":
			res = map[string]interface{}{"tools": []interface{}{map[string]interface{}{"name": "look", "description": "d"}}}
		case "tools/call":
			res = map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}}
		default:
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": *m.ID, "result": res})
	}))
	defer srv.Close()
	s, err := dialMCP(context.Background(), srv.URL, "tok-12345678")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := s.listTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	if _, err := s.callTool(context.Background(), "look", nil); err != nil {
		t.Fatal(err)
	}
	s.close()
	// the second session of the same server skips the question: it is known
	s2, err := dialMCP(context.Background(), srv.URL, "tok-12345678")
	if err != nil {
		t.Fatal(err)
	}
	s2.close()
	mu.Lock()
	defer mu.Unlock()
	want := []string{"server/discover|2026-07-28||", "tools/list|2026-07-28|tools/list|", "tools/call|2026-07-28|tools/call|look"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("a 2026 server saw %v, want %v", seen, want)
	}

	// a classic server (the fake one) answers discover with an error: initialize, and the session is ended with DELETE when it gave an id
	f := newFakeMCP(t)
	var deleted int32
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			atomic.AddInt32(&deleted, 1)
			w.WriteHeader(200)
			return
		}
		inner.ServeHTTP(w, r)
	})
	cs, err := dialMCP(context.Background(), f.srv.URL, testToken)
	if err != nil {
		t.Fatalf("a classic server: %v", err)
	}
	if _, err := cs.listTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs.close()
	if f.sessions != 1 || atomic.LoadInt32(&deleted) != 1 {
		t.Errorf("a classic server: %d initialize, %d delete", f.sessions, deleted)
	}
	// a token that is refused is an error at once, not a reason to try the older way
	if _, err := dialMCP(context.Background(), f.srv.URL+"/other", "wrong-token-12345"); err != errMCPAuth {
		t.Errorf("a refused token: %v", err)
	}
}

// ---- signing in ----------------------------------------------------------------------------------------------------------------------------

type fakeIdP struct {
	srv      *httptest.Server
	mu       sync.Mutex
	codes    map[string]string // code -> the challenge it was asked with
	refresh  int32
	rotation int32
	refuse   bool // answer a refresh with invalid_grant
	down     bool // answer a refresh with 503
	verifier string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	f := &fakeIdP{codes: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("client_id") != "app-id" || r.Form.Get("client_secret") != "app-secret" {
			w.Write([]byte(`{"error":"incorrect_client_credentials"}`))
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			f.mu.Lock()
			challenge, ok := f.codes[r.Form.Get("code")]
			delete(f.codes, r.Form.Get("code"))
			f.mu.Unlock()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				w.Write([]byte(`{"error":"bad_verification_code"}`))
				return
			}
			w.Write([]byte(`{"access_token":"` + testToken + `","refresh_token":"refresh-1","expires_in":28800,"refresh_token_expires_in":15897600}`))
		case "refresh_token":
			atomic.AddInt32(&f.refresh, 1)
			time.Sleep(30 * time.Millisecond)
			if f.down {
				w.WriteHeader(503)
				return
			}
			if f.refuse {
				w.Write([]byte(`{"error":"bad_refresh_token"}`))
				return
			}
			n := atomic.AddInt32(&f.rotation, 1)
			w.Write([]byte(`{"access_token":"` + testToken + `","refresh_token":"refresh-` + string(rune('1'+n)) + `","expires_in":28800}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// oauthSetup connects GitHub's entry to the fake service of the tests.
func oauthSetup(t *testing.T) (*connEnv, *fakeIdP) {
	e := connSetup(t, GenieSettings{})
	idp := newFakeIdP(t)
	t.Setenv("OPENREPL_GITHUB_APP_CLIENT_ID", "app-id")
	t.Setenv("OPENREPL_GITHUB_APP_CLIENT_SECRET", "app-secret")
	old := connectionCatalogue[0].OAuth
	connectionCatalogue[0].OAuth = &oauthDef{AuthURL: idp.srv.URL + "/authorize", TokenURL: idp.srv.URL + "/token", ClientIDEnv: "OPENREPL_GITHUB_APP_CLIENT_ID", ClientSecretEnv: "OPENREPL_GITHUB_APP_CLIENT_SECRET"}
	oauthStates.Lock()
	oauthStates.m = map[string]oauthPending{}
	oauthStates.Unlock()
	t.Cleanup(func() { connectionCatalogue[0].OAuth = old })
	return e, idp
}

// startSignIn does what the page does: the op, then what the service does with the address (it gives a code for the state).
func (e *connEnv) startSignIn(t *testing.T, idp *fakeIdP) (state, code string) {
	t.Helper()
	r := e.ok(t, `{"op":"oauth_start","id":"github"}`)
	u, err := url.Parse(r["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "app-id" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || !strings.HasSuffix(q.Get("redirect_uri"), "/genie_conn_oauth") {
		t.Fatalf("the address: %v", q)
	}
	if strings.Contains(r["url"].(string), "app-secret") {
		t.Error("the secret is in the address")
	}
	code = "code-" + q.Get("state")[:6]
	idp.mu.Lock()
	idp.codes[code] = q.Get("code_challenge")
	idp.mu.Unlock()
	return q.Get("state"), code
}

func (e *connEnv) callback(t *testing.T, query string, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/genie_conn_oauth?"+query, nil)
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.s.handleGenieConnOAuth(w, req)
	return w
}

func TestSigningInKeepsASealedTokenPairAfterARealCallAndTheStateWorksOnce(t *testing.T) {
	e, idp := oauthSetup(t)
	if l := e.ok(t, `{"op":"list"}`)["connections"].([]interface{})[0].(map[string]interface{}); l["oauth"] != true {
		t.Errorf("the list does not say that signing in is set up: %v", l)
	}
	state, code := e.startSignIn(t, idp)
	// another account's browser cannot finish it, and that does not use the state up for the right one
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if w := e.callback(t, "code="+code+"&state="+state, other); w.Code != http.StatusBadRequest {
		t.Errorf("another account: %d", w.Code)
	}
	state, code = e.startSignIn(t, idp)
	if w := e.callback(t, "code="+code+"&state="+state, nil); w.Code != http.StatusForbidden {
		t.Errorf("not signed in: %d", w.Code)
	}
	state, code = e.startSignIn(t, idp)
	w := e.callback(t, "code="+code+"&state="+state, e.c)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "#genie-connected-github") {
		t.Fatalf("the callback: %d %s", w.Code, w.Body.String())
	}
	// the state is gone
	if w := e.callback(t, "code="+code+"&state="+state, e.c); w.Code != http.StatusBadRequest {
		t.Errorf("a state used twice: %d", w.Code)
	}
	rec, ok := readConnRecord("uid-ann", "github")
	if !ok || rec.Kind != "oauth" || rec.Expires == "" {
		t.Fatalf("the record: %+v", rec)
	}
	if strings.Contains(rec.Sealed, testToken) || strings.Contains(rec.Sealed, "refresh-1") {
		t.Error("the tokens are in the clear")
	}
	// and it is used for a call, as the person's token
	if r := e.ok(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":7},"task":"o1"}`); r["level"] != "read" {
		t.Errorf("a call with the sign-in: %v", r)
	}
	// a service that does not accept the sign-in is not kept: a code that was never issued
	state, _ = e.startSignIn(t, idp)
	if w := e.callback(t, "code=never-issued&state="+state, e.c); w.Code != http.StatusBadGateway {
		t.Errorf("a code the service did not issue: %d", w.Code)
	}
}

func TestASignInTheServiceDeniedOrFromAnotherIssuerIsNotKept(t *testing.T) {
	e, idp := oauthSetup(t)
	connectionCatalogue[0].OAuth.Issuer = "https://issuer.example"
	state, code := e.startSignIn(t, idp)
	if w := e.callback(t, "error=access_denied&state="+state, e.c); w.Code != http.StatusBadRequest {
		t.Errorf("a denial: %d", w.Code)
	}
	state, code = e.startSignIn(t, idp)
	if w := e.callback(t, "code="+code+"&state="+state+"&iss="+url.QueryEscape("https://attacker.example"), e.c); w.Code != http.StatusBadRequest {
		t.Errorf("another issuer: %d", w.Code)
	}
	if _, ok := readConnRecord("uid-ann", "github"); ok {
		t.Error("a sign-in that was refused was kept")
	}
	editIntegration(t, "github", func(i *Integration) { i.Auth.RequireIssuer = true })
	state, code = e.startSignIn(t, idp)
	if w := e.callback(t, "code="+code+"&state="+state, e.c); w.Code != http.StatusBadRequest {
		t.Errorf("no issuer when one is required: %d", w.Code)
	}
}

func TestSigningInIsOnlyOfferedWhereTheAdminSetAClientUp(t *testing.T) {
	e, _ := oauthSetup(t)
	t.Setenv("OPENREPL_GITHUB_APP_CLIENT_ID", "")
	if l := e.ok(t, `{"op":"list"}`)["connections"].([]interface{})[0].(map[string]interface{}); l["oauth"] == true {
		t.Error("offered without a client")
	}
	if code, _ := e.call(t, `{"op":"oauth_start","id":"github"}`, e.c); code != http.StatusBadRequest {
		t.Errorf("start without a client: %d", code)
	}
}

// signedIn puts a sign-in whose access token has ended in the person's record.
func signedIn(t *testing.T, refresh string, expired bool) {
	t.Helper()
	def, _ := catalogueConnection("github")
	exp := time.Now().Add(8 * time.Hour)
	if expired {
		exp = time.Now().Add(-time.Hour)
	}
	if err := saveConnOAuth("uid-ann", def, oauthCred{Access: "old-access-token", Refresh: refresh, Expires: exp.UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
}

func TestManyCallsWithAnEndedSignInRenewItOnceAndKeepTheNewPair(t *testing.T) {
	e, idp := oauthSetup(t)
	signedIn(t, "refresh-0", true)
	var wg sync.WaitGroup
	var good int32
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, tok, ok := connToken("uid-ann", "github"); ok && tok == testToken {
				atomic.AddInt32(&good, 1)
			}
		}()
	}
	wg.Wait()
	if good != 30 || atomic.LoadInt32(&idp.refresh) != 1 {
		t.Errorf("30 calls: %d got the new token, %d renewals", good, idp.refresh)
	}
	rec, _ := readConnRecord("uid-ann", "github")
	cred, err := openOAuthCred("uid-ann", rec)
	if err != nil || cred.Refresh != "refresh-2" || cred.Access != testToken {
		t.Errorf("the pair that was kept: %+v %v", cred, err)
	}
	// it is a real call too
	if r := e.ok(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":1},"task":"r1"}`); r["level"] != "read" {
		t.Errorf("%v", r)
	}
	if atomic.LoadInt32(&idp.refresh) != 1 {
		t.Errorf("a token that is good was renewed again: %d", idp.refresh)
	}
}

func TestARefusedRenewalMakesTheConnectionNeedASignInAndNothingRetries(t *testing.T) {
	e, idp := oauthSetup(t)
	idp.refuse = true
	signedIn(t, "refresh-0", true)
	for i := 0; i < 3; i++ {
		code, out := e.call(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":1},"task":"x`+string(rune('a'+i))+`"}`, e.c)
		if code != http.StatusConflict || !strings.Contains(fmtErr(out), "reconnect") {
			t.Errorf("call %d: %d %v", i, code, out)
		}
	}
	if atomic.LoadInt32(&idp.refresh) != 1 {
		t.Errorf("the refused renewal was tried %d times", idp.refresh)
	}
	l := e.ok(t, `{"op":"list"}`)["connections"].([]interface{})[0].(map[string]interface{})
	if l["reconnect"] != true || l["connected"] != true {
		t.Errorf("the list: %v", l)
	}
	// signing in again clears it
	idp.refuse = false
	state, code := e.startSignIn(t, idp)
	if w := e.callback(t, "code="+code+"&state="+state, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("signing in again: %d", w.Code)
	}
	if rec, _ := readConnRecord("uid-ann", "github"); rec.Reconnect {
		t.Error("still marked")
	}
}

func TestARenewalThatCannotBeReachedKeepsTheOldTokenWhileItLastsAndDoesNotMarkIt(t *testing.T) {
	_, idp := oauthSetup(t)
	idp.down = true
	// ending in 30 seconds: due for renewal, but still good
	def, _ := catalogueConnection("github")
	saveConnOAuth("uid-ann", def, oauthCred{Access: "still-good-token", Refresh: "refresh-0", Expires: time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)})
	if _, tok, ok := connToken("uid-ann", "github"); !ok || tok != "still-good-token" {
		t.Errorf("the old token while the service is down: %v %q", ok, tok)
	}
	// ended and the service is down: no token, and not marked, so it works when the service is back
	signedIn(t, "refresh-0", true)
	if rec, tok, ok := connToken("uid-ann", "github"); ok || tok != "" || rec.Reconnect {
		t.Errorf("ended and down: %v %q %v", ok, tok, rec.Reconnect)
	}
	idp.down = false
	if _, tok, ok := connToken("uid-ann", "github"); !ok || tok != testToken {
		t.Errorf("after the service came back: %v %q", ok, tok)
	}
}

func TestTheTokenIsNeverSentToAnAddressThatChangedSinceItWasGiven(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	editIntegration(t, "github", func(i *Integration) { i.URL = "https://elsewhere.example/mcp/" })
	if code, out := e.call(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":1},"task":"c1"}`, e.c); code != http.StatusConflict || !strings.Contains(fmtErr(out), "changed") {
		t.Errorf("a changed address: %d %v", code, out)
	}
}

func fmtErr(out map[string]interface{}) string {
	b, _ := json.Marshal(out)
	return string(b)
}

func TestScopesAreAskedForOnlyWhenTheAdminSetThem(t *testing.T) {
	e, idp := oauthSetup(t)
	connectionCatalogue[0].OAuth.ScopesEnv = "OPENREPL_TEST_SCOPES"
	q := func() url.Values {
		u, _ := url.Parse(e.ok(t, `{"op":"oauth_start","id":"github"}`)["url"].(string))
		return u.Query()
	}
	if _, has := q()["scope"]; has {
		t.Error("a scope was asked for without being set")
	}
	t.Setenv("OPENREPL_TEST_SCOPES", "repo, read:user")
	if got := q().Get("scope"); got != "repo read:user" {
		t.Errorf("scopes: %q", got)
	}
	_ = idp
}

// ---- where the records are kept (genie_conn_store.go) ---------------------------------------------------------------------------------

func TestTheRecordsAreInTheUserDatabaseOneForEachPersonWithTheKeysTheListOfUsersSkips(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	b, err := connDB().Fetch([]byte("genie-conn:uid-ann"))
	if err != nil {
		t.Fatal("no record in the database: ", err)
	}
	var d connUserDoc
	if json.Unmarshal(b, &d) != nil || d.V != 1 || d.UID != "uid-ann" || d.Integrations["github"] == nil || !d.Integrations["github"].Enabled {
		t.Fatalf("the record: %s", b)
	}
	c := d.Integrations["github"].Connection
	if c == nil || c.Sealed == "" || c.URL == "" || c.ConnectedAt == "" || strings.Contains(string(b), testToken) {
		t.Errorf("the connection: %+v (token in clear: %v)", c, strings.Contains(string(b), testToken))
	}
	// no file of the person is made in the folder of the log
	entries, _ := os.ReadDir(connDir)
	for _, f := range entries {
		if strings.HasSuffix(f.Name(), ".json") {
			t.Errorf("a file was made: %s", f.Name())
		}
	}
	// another person has a record of their own; the admin sees who is connected to what, never the credential
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if code, _ := e.call(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`, other); code != 200 {
		t.Fatalf("a second person: %d", code)
	}
	people := connectedPeople()
	if len(people) != 2 {
		t.Errorf("who is connected: %+v", people)
	}
	if strings.Contains(fmt.Sprint(people), "sealed") {
		t.Error("the list of people holds a credential")
	}
	// Disconnect removes only the connection: the service stays enabled (the final table of 2026-11-21), and it is no longer a connection
	e.ok(t, `{"op":"disconnect","id":"github"}`)
	b, _ = connDB().Fetch([]byte("genie-conn:uid-ann"))
	var after connUserDoc
	json.Unmarshal(b, &after)
	if g := after.Integrations["github"]; g == nil || !g.Enabled || g.Connection != nil || strings.Contains(string(b), "sealed") {
		t.Errorf("after Disconnect: %s", b)
	}
	if recs := readConnRecords("uid-ann"); len(recs) != 0 {
		t.Errorf("connections after Disconnect: %+v", recs)
	}
	for _, p := range connectedPeople() {
		if p.User == "uid-ann" {
			t.Error("the admin still sees a person who disconnected")
		}
	}
	if b, _ := connDB().Fetch([]byte("genie-conn:uid-bob")); len(b) == 0 {
		t.Error("Disconnect of one person took the record of another")
	}
}

func TestAnEnabledServiceWithoutAConnectionIsKeptAndIsNotAConnection(t *testing.T) {
	connSetup(t, GenieSettings{})
	d := &connUserDoc{V: 1, UID: "uid-ann", Integrations: map[string]*connUserInteg{"notion": {Enabled: true, EnabledAt: "2026-11-21T05:10:00Z"}}}
	if err := saveConnDoc(d); err != nil {
		t.Fatal(err)
	}
	if recs := readConnRecords("uid-ann"); len(recs) != 0 {
		t.Errorf("an enabled service is not a connection: %+v", recs)
	}
	// connecting another one does not drop the enabled one, and Disconnect of it leaves the enabled one
	def, _ := catalogueConnection("github")
	if err := saveConnToken("uid-ann", def, "ghp_testtoken1234567890"); err != nil {
		t.Fatal(err)
	}
	if !deleteConnToken("uid-ann", "github") {
		t.Fatal("Disconnect found nothing")
	}
	got := loadConnDoc("uid-ann")
	if got == nil || got.Integrations["notion"] == nil || !got.Integrations["notion"].Enabled || got.Integrations["github"] == nil || got.Integrations["github"].Connection != nil {
		t.Errorf("after Disconnect: %+v", got)
	}
}

func TestTheOldFilesAreImportedOnceAndRemoved(t *testing.T) {
	connSetup(t, GenieSettings{})
	// a person's old file and the old list of requests, as the previous version wrote them
	old := []connRecord{{User: "uid-ann", ID: "github", URL: "https://api.githubcopilot.com/mcp/", Title: "GitHub", Sealed: "sealed-value", At: "2026-11-20T21:31:10Z", Kind: "oauth", Expires: "2026-11-21T13:00:00Z"}}
	b, _ := json.Marshal(old)
	os.MkdirAll(connDir, 0o700)
	if err := os.WriteFile(connFileOf("uid-ann"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	reqs := []connRequest{{ID: "r1", User: "uid-ann", Plugin: "acme", Status: "waiting", At: "2026-11-20T10:00:00Z"}}
	rb, _ := json.Marshal(reqs)
	if err := os.WriteFile(legacyReqFile(), rb, 0o600); err != nil {
		t.Fatal(err)
	}
	recs := readConnRecords("uid-ann")
	if len(recs) != 1 || recs[0].ID != "github" || recs[0].Sealed != "sealed-value" || recs[0].Kind != "oauth" || recs[0].Expires != "2026-11-21T13:00:00Z" || recs[0].At != "2026-11-20T21:31:10Z" {
		t.Fatalf("the imported record: %+v", recs)
	}
	if _, err := os.Stat(connFileOf("uid-ann")); err == nil {
		t.Error("the old file was not removed")
	}
	got := readConnRequests()
	if len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("the imported requests: %+v", got)
	}
	if _, err := os.Stat(legacyReqFile()); err == nil {
		t.Error("the old list of requests was not removed")
	}
	// it is in the database now, and a second read does not look at files
	if recs2 := readConnRecords("uid-ann"); len(recs2) != 1 || recs2[0].Sealed != "sealed-value" {
		t.Errorf("the second read: %+v", recs2)
	}
	if b, _ := connDB().Fetch([]byte("genie-conn-req:all")); len(b) == 0 {
		t.Error("the requests are not in the database")
	}
}

func TestTheCallLogIsKeptFourteenDays(t *testing.T) {
	connSetup(t, GenieSettings{})
	os.MkdirAll(connDir, 0o700)
	now := time.Now().UTC()
	for _, d := range []int{0, 1, 13, 14, 15, 40} {
		name := filepath.Join(connDir, "calls-"+now.AddDate(0, 0, -d).Format("2006-01-02")+".jsonl")
		os.WriteFile(name, []byte("{}\n"), 0o600)
	}
	other := filepath.Join(connDir, "keep-me.txt")
	os.WriteFile(other, []byte("x"), 0o600)
	lastLogPrune = ""
	connLogMu.Lock()
	pruneCallLogs(now)
	connLogMu.Unlock()
	for d, want := range map[int]bool{0: true, 1: true, 13: true, 14: true, 15: false, 40: false} {
		_, err := os.Stat(filepath.Join(connDir, "calls-"+now.AddDate(0, 0, -d).Format("2006-01-02")+".jsonl"))
		if (err == nil) != want {
			t.Errorf("the log of %d days ago: kept %v, want %v", d, err == nil, want)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("a file that is not a log was removed")
	}
}

// editIntegration changes a record of the registry, as the admin's page does.
func editIntegration(t *testing.T, id string, fn func(i *Integration)) {
	t.Helper()
	err := updateRegistry(func(list *[]Integration, _ map[string]IntegrationSecret) error {
		k := integIndex(*list, id)
		if k < 0 {
			return fmt.Errorf("no integration %s", id)
		}
		fn(&(*list)[k])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPersonSeesOnlyTheirOwnRecentCalls(t *testing.T) {
	e := connSetup(t, GenieSettings{})
	e.ok(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`)
	e.ok(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":7},"task":"c1"}`)
	other := sessionCookie(t, "uid-bob", "bob@example.com")
	if code, _ := e.call(t, `{"op":"connect","id":"github","token":"`+testToken+`"}`, other); code != 200 {
		t.Fatal(code)
	}
	for i := 0; i < 2; i++ {
		if code, _ := e.call(t, `{"op":"call","id":"github","tool":"get_issue","args":{"number":9},"task":"b1"}`, other); code != 200 {
			t.Fatalf("bob's call: %d", code)
		}
	}
	calls := func(c *http.Cookie) []interface{} {
		code, out := e.call(t, `{"op":"calls"}`, c)
		if code != 200 {
			t.Fatalf("calls: %d %v", code, out)
		}
		return out["calls"].([]interface{})
	}
	mine := calls(e.c)
	if len(mine) != 1 {
		t.Fatalf("ann's calls: %v", mine)
	}
	c0 := mine[0].(map[string]interface{})
	if c0["tool"] != "get_issue" || c0["level"] != "read" || c0["ok"] != true || c0["conn"] != "github" {
		t.Errorf("the call: %v", c0)
	}
	if _, has := c0["user"]; has && c0["user"] != "" {
		t.Errorf("the call names a person: %v", c0)
	}
	if b := calls(other); len(b) != 2 {
		t.Errorf("bob's calls: %v", b)
	}
	if raw, _ := json.Marshal(mine); strings.Contains(string(raw), "7") && strings.Contains(string(raw), "number") {
		t.Error("the arguments are in the list")
	}
}
