package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"cookie"
	"utils"
)

// Signing in to a service for a connection, instead of pasting a token (the review of 2026-11-20, phases 2 and 3).
//
// A service that has an oauthDef can be connected with the authorization code flow with PKCE: the person is sent to the service, comes back to
// /genie_conn_oauth with a code, and the server swaps it for an access token (and a refresh token) that it seals like any other token. The client id
// (and the secret, for a service that has one) are the admin's, from the environment, never the person's.
//
// GitHub is the first service: a GitHub App with user authorization. Its tokens are user-to-server tokens: they act as the person, within what the
// app was given and the person can do, and they end after hours, so they are renewed with the refresh token (which GitHub replaces at each renewal,
// so the new pair is written before the new access token is used). A pasted personal access token stays as the fallback.
//
// Not built: finding the service's endpoints and registering the client by itself (MCP's protected-resource metadata and client registration).
// Each service's endpoints are given here, as data.

type oauthDef struct {
	AuthURL         string
	TokenURL        string
	ClientIDEnv     string // the environment variable that holds the client id
	ClientSecretEnv string // ... the secret ("" for a public client: PKCE alone)
	Scopes          []string
	ScopesEnv       string // the environment variable that holds the scopes (space or comma separated) for a service that wants them asked for
	Issuer          string // when set, a response that names another issuer (RFC 9207) is refused
	RequireIssuer   bool   // ... and one that names none
	// an integration of the registry (genie_integrations.go): the client the admin made, the server's registration address, whether Genie
	// registers itself, and the resource a token is asked for (RFC 8707)
	ClientID        string
	Auto            bool
	RegistrationURL string
	IntegrationID   string
	Resource        string
}

// scopes are the ones asked for: the definition's own, else those the admin set in the environment ("" for none: a GitHub App has no scopes).
func (o *oauthDef) scopes() []string {
	if len(o.Scopes) > 0 || o.ScopesEnv == "" {
		return o.Scopes
	}
	return strings.FieldsFunc(os.Getenv(o.ScopesEnv), func(r rune) bool { return r == ' ' || r == ',' })
}

// configured: a person can sign in: Genie registers itself (Auto), the admin gave the site a client (ClientID, or the variables of the environment).
func (o *oauthDef) configured() bool {
	if o == nil {
		return false
	}
	if o.Auto || o.ClientID != "" {
		return true
	}
	if o.ClientIDEnv == "" || strings.TrimSpace(oauthEnv(o.ClientIDEnv)) == "" {
		return false
	}
	return o.ClientSecretEnv == "" || oauthEnv(o.ClientSecretEnv) != ""
}

// oauthEnv reads a variable of the environment that holds a client id or secret: the GitHub App's two may also be saved in the dashboard (API keys),
// and that wins.
func oauthEnv(name string) string {
	switch name {
	case utils.EnvGitHubAppID:
		return utils.GitHubAppID()
	case utils.EnvGitHubAppSecret:
		return utils.GitHubAppSecret()
	}
	return os.Getenv(name)
}

// ---- the state of a sign-in that was started -------------------------------------------------------------------------------------------------

type oauthPending struct {
	uid, id, verifier, redirect string
	exp                         time.Time
}

var oauthStates = struct {
	sync.Mutex
	m map[string]oauthPending
}{m: map[string]oauthPending{}}

const oauthStateTTL = 10 * time.Minute

func randomURLString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// publicBase is the address of this site as the service has to be told it: OPENREPL_PUBLIC_URL, else what the request says.
func publicBase(req *http.Request) string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENREPL_PUBLIC_URL")), "/"); u != "" {
		return u
	}
	scheme := "https"
	if req.TLS == nil && req.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + req.Host
}

// oauthStart makes the address the person is sent to.
func oauthStart(uid string, def connectionDef, redirect string, now time.Time) (string, error) {
	o := def.OAuth
	if o == nil || !o.configured() {
		return "", fmt.Errorf("signing in to %s is not set up on this site: paste a token instead", def.Title)
	}
	state, verifier := randomURLString(18), randomURLString(32)
	oauthStates.Lock()
	for k, v := range oauthStates.m {
		if !v.exp.After(now) {
			delete(oauthStates.m, k)
		}
	}
	if len(oauthStates.m) >= 500 {
		oauthStates.Unlock()
		return "", fmt.Errorf("too many sign-ins are waiting: try again in a few minutes")
	}
	oauthStates.Unlock()
	// the client of this site's address: the admin's app, or the one Genie registered by itself (registered now when there is none)
	cid, _ := clientFor(def.ID, o, redirect)
	if cid == "" && o.Auto {
		var err error
		if cid, _, err = registerClient(def, redirect); err != nil {
			return "", err
		}
	}
	if cid == "" {
		return "", fmt.Errorf("signing in to %s is not set up on this site: paste a token instead", def.Title)
	}
	oauthStates.Lock()
	oauthStates.m[state] = oauthPending{uid: uid, id: def.ID, verifier: verifier, redirect: redirect, exp: now.Add(oauthStateTTL)}
	oauthStates.Unlock()
	ch := sha256.Sum256([]byte(verifier))
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cid)
	if o.Resource != "" {
		q.Set("resource", o.Resource)
	}
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(ch[:]))
	q.Set("code_challenge_method", "S256")
	if sc := o.scopes(); len(sc) > 0 {
		q.Set("scope", strings.Join(sc, " "))
	}
	sep := "?"
	if strings.Contains(o.AuthURL, "?") {
		sep = "&"
	}
	return o.AuthURL + sep + q.Encode(), nil
}

// takeOAuthState uses a state up: once, and only by the person it was made for.
func takeOAuthState(state, uid string, now time.Time) (oauthPending, error) {
	oauthStates.Lock()
	defer oauthStates.Unlock()
	p, ok := oauthStates.m[state]
	if ok {
		delete(oauthStates.m, state)
	}
	switch {
	case !ok || !p.exp.After(now):
		return oauthPending{}, fmt.Errorf("this sign-in was not started here, or it took too long: start it again")
	case p.uid != uid:
		return oauthPending{}, fmt.Errorf("this sign-in was started by another account")
	}
	return p, nil
}

// ---- the credentials -------------------------------------------------------------------------------------------------------------------------

type oauthCred struct {
	Access     string `json:"a"`
	Refresh    string `json:"r,omitempty"`
	RefreshEnd string `json:"re,omitempty"` // when the refresh token ends (RFC 3339), "" when it does not say
	Expires    string `json:"-"`
}

type oauthTokenReply struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

var errOAuthRefused = fmt.Errorf("the service refused the sign-in")

// oauthHTTP is the client for the service's token endpoint. Tests replace it.
var oauthHTTP = func() *http.Client { return newExtClientFor(15 * time.Second) }

// oauthToken posts to the token endpoint. A refusal by the service (an error in the answer, or a 4xx) is errOAuthRefused: the credential is no good.
// Anything else (the service cannot be reached, a 5xx) is another error: try again later.
func oauthToken(o *oauthDef, clientID, secret string, form url.Values) (oauthCred, error) {
	form.Set("client_id", clientID)
	if secret != "" {
		form.Set("client_secret", secret)
	}
	if o.Resource != "" {
		form.Set("resource", o.Resource)
	}
	req, err := http.NewRequest(http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthCred{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "openrepl-genie-connection")
	resp, err := oauthHTTP().Do(req)
	if err != nil {
		return oauthCred{}, fmt.Errorf("the service could not be reached")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 500 {
		return oauthCred{}, fmt.Errorf("the service answered %d", resp.StatusCode)
	}
	var r oauthTokenReply
	if json.Unmarshal(b, &r) != nil {
		if resp.StatusCode >= 400 {
			return oauthCred{}, errOAuthRefused
		}
		return oauthCred{}, fmt.Errorf("the service did not answer as expected")
	}
	if r.Error != "" || resp.StatusCode >= 400 || r.AccessToken == "" {
		return oauthCred{}, errOAuthRefused
	}
	c := oauthCred{Access: r.AccessToken, Refresh: r.RefreshToken}
	now := time.Now().UTC()
	if r.ExpiresIn > 0 {
		c.Expires = now.Add(time.Duration(r.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if r.RefreshExpiresIn > 0 {
		c.RefreshEnd = now.Add(time.Duration(r.RefreshExpiresIn) * time.Second).Format(time.RFC3339)
	}
	return c, nil
}

// saveConnOAuth seals the access and refresh tokens as the person's credential for a connection.
func saveConnOAuth(uid string, def connectionDef, c oauthCred, redirect ...string) error {
	payload, _ := json.Marshal(c)
	rd := ""
	if len(redirect) > 0 {
		rd = redirect[0]
	}
	sealed, err := sealKey(utils.Secret(), connAAD(uid, def.ID), string(payload))
	if err != nil {
		return err
	}
	return storeConnRecord(uid, connRecord{User: uid, ID: def.ID, URL: def.URL, Title: def.Title, Sealed: sealed, At: time.Now().UTC().Format(time.RFC3339), Kind: "oauth", Expires: c.Expires, Redirect: rd})
}

func openOAuthCred(uid string, rec connRecord) (oauthCred, error) {
	raw, err := openKey(utils.Secret(), connAAD(uid, rec.ID), rec.Sealed)
	if err != nil {
		return oauthCred{}, err
	}
	var c oauthCred
	if err := json.Unmarshal([]byte(raw), &c); err != nil || c.Access == "" {
		return oauthCred{}, fmt.Errorf("the saved sign-in cannot be read")
	}
	c.Expires = rec.Expires
	return c, nil
}

func (c oauthCred) stale(now time.Time) bool {
	if c.Expires == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, c.Expires)
	return err == nil && !t.After(now.Add(60*time.Second))
}

var refreshLocks sync.Map // uid|id -> *sync.Mutex: one renewal at a time for a connection

// oauthAccessToken gives the access token of a sign-in, renewed first when it is about to end. The renewal is single-flight (calls that arrive
// while one runs wait for it and use its result), and the new pair is written before the new access token is used: a service that replaces the
// refresh token at each renewal would otherwise leave the file with one that no longer works.
func oauthAccessToken(uid string, rec connRecord) (connRecord, string, bool) {
	now := time.Now()
	cred, err := openOAuthCred(uid, rec)
	if err != nil {
		return rec, "", false
	}
	if !cred.stale(now) {
		return rec, cred.Access, true
	}
	l, _ := refreshLocks.LoadOrStore(uid+"|"+rec.ID, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	// a call that held the lock before this one may have renewed it
	if latest, ok := readConnRecord(uid, rec.ID); ok {
		rec = latest
		if rec.Reconnect {
			return rec, "", false
		}
		if cred, err = openOAuthCred(uid, rec); err != nil {
			return rec, "", false
		}
		if !cred.stale(time.Now()) {
			return rec, cred.Access, true
		}
	}
	def, ok := resolveConnection(uid, rec.ID)
	if !ok || def.OAuth == nil || cred.Refresh == "" {
		return markReconnect(uid, rec)
	}
	cid, sec := clientFor(def.ID, def.OAuth, rec.Redirect)
	fresh, err := oauthToken(def.OAuth, cid, sec, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.Refresh}})
	if err == errOAuthRefused {
		return markReconnect(uid, rec)
	}
	if err != nil {
		// not reached just now: the old token still serves while it has not ended
		if t, perr := time.Parse(time.RFC3339, cred.Expires); perr == nil && t.After(time.Now()) {
			return rec, cred.Access, true
		}
		return rec, "", false
	}
	if fresh.Refresh == "" {
		fresh.Refresh = cred.Refresh
	}
	if err := saveConnOAuth(uid, def, fresh, rec.Redirect); err != nil {
		return rec, "", false
	}
	rec.Expires = fresh.Expires
	return rec, fresh.Access, true
}

// markReconnect records that the refresh was refused: the person has to sign in again, and nothing retries.
func markReconnect(uid string, rec connRecord) (connRecord, string, bool) {
	connMu.Lock()
	defer connMu.Unlock()
	recs := readConnRecords(uid)
	for i := range recs {
		if recs[i].ID == rec.ID {
			recs[i].Reconnect = true
			rec = recs[i]
		}
	}
	writeConnRecords(uid, recs)
	return rec, "", false
}

// ---- the route: where the service sends the person back to ----------------------------------------------------------------------------------

func oauthPage(rw http.ResponseWriter, status int, title, msg, back string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("Referrer-Policy", "no-referrer")
	rw.WriteHeader(status)
	fmt.Fprintf(rw, "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>%s</title><body style=\"font:16px system-ui;max-width:32em;margin:3em auto;padding:0 1em\"><h1>%s</h1><p>%s</p><p><a href=\"%s\">Back to OpenREPL</a></p>",
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg), html.EscapeString(back))
}

// GET genie_conn_oauth?code=…&state=…
func (server *Server) handleGenieConnOAuth(rw http.ResponseWriter, req *http.Request) {
	back := strings.TrimSuffix(req.URL.Path, "genie_conn_oauth")
	if back == "" {
		back = "/"
	}
	if req.Method != http.MethodGet {
		http.Error(rw, "GET only", http.StatusMethodNotAllowed)
		return
	}
	g := GetSiteSettings().Genie
	if g.Disabled || g.ConnectionsOff {
		oauthPage(rw, http.StatusForbidden, "Connections are off", "Connections are switched off on this site.", back)
		return
	}
	if !cookie.Is_UserLoggedIn(req) || homeOverridden(req) || cookie.Get_Uid(req) == "" {
		oauthPage(rw, http.StatusForbidden, "Sign in first", "Sign in to your own OpenREPL account, then connect the service again.", back)
		return
	}
	uid := cookie.Get_Uid(req)
	q := req.URL.Query()
	pending, err := takeOAuthState(q.Get("state"), uid, time.Now())
	if err != nil {
		oauthPage(rw, http.StatusBadRequest, "Not connected", err.Error()+".", back)
		return
	}
	def, ok := resolveConnection(uid, pending.id)
	if !ok || def.OAuth == nil {
		oauthPage(rw, http.StatusNotFound, "Not connected", "That connection is not offered here.", back)
		return
	}
	if e := q.Get("error"); e != "" {
		oauthPage(rw, http.StatusBadRequest, "Not connected", def.Title+" did not give access ("+clipRunes(e, 60)+"). Nothing was saved.", back)
		return
	}
	// RFC 9207: the response says who sent it, and it has to be the service this sign-in was started with
	if iss := q.Get("iss"); def.OAuth.Issuer != "" && iss != "" && iss != def.OAuth.Issuer || def.OAuth.RequireIssuer && iss == "" {
		oauthPage(rw, http.StatusBadRequest, "Not connected", "The answer did not come from "+def.Title+". Nothing was saved.", back)
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 2048 {
		oauthPage(rw, http.StatusBadRequest, "Not connected", "The answer had no code. Nothing was saved.", back)
		return
	}
	cid, sec := clientFor(def.ID, def.OAuth, pending.redirect)
	cred, err := oauthToken(def.OAuth, cid, sec, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {pending.redirect}, "code_verifier": {pending.verifier}})
	if err != nil {
		oauthPage(rw, http.StatusBadGateway, "Not connected", def.Title+" did not accept the sign-in. Nothing was saved.", back)
		return
	}
	// the sign-in is tried with a real call before it is kept
	ctx, cancel := context.WithTimeout(req.Context(), mcpCallTimeout)
	defer cancel()
	sess, err := dialMCP(ctx, def.URL, cred.Access)
	var tools []mcpTool
	if err == nil {
		tools, err = sess.listTools(ctx)
		sess.close()
	}
	if err != nil {
		logConnCall(connCallEntry{User: uid, Conn: def.ID, Op: "connect"})
		oauthPage(rw, http.StatusBadGateway, "Not connected", "Signed in, but "+def.Title+" did not accept the call that tests it: "+err.Error()+". Nothing was saved.", back)
		return
	}
	if utils.Secret() == "" {
		oauthPage(rw, http.StatusServiceUnavailable, "Not connected", "This server has no secret to keep sign-ins with: an admin has to set OPENREPL_SECRET.", back)
		return
	}
	if err := saveConnOAuth(uid, def, cred, pending.redirect); err != nil {
		oauthPage(rw, http.StatusBadRequest, "Not connected", err.Error()+".", back)
		return
	}
	cacheTools(uid, def.ID, toConnTools(def, tools), tools)
	revokeGrants(uid, def.ID)
	logConnCall(connCallEntry{User: uid, Conn: def.ID, Op: "connect", OK: true})
	http.Redirect(rw, req, back+"#genie-connected-"+def.ID, http.StatusSeeOther)
}
