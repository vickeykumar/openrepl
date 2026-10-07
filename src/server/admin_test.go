package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"persist"

	"cookie"
	"user"
	"utils"
)

// The admin dashboard's API. Everything here uses temporary files: the real
// ones live in /opt/gotty and belong to a running server.

// adminTestServer is a standalone server whose admin is anybody that sends
// the header X-Test-Admin: yes, with every file the admin code writes moved to
// a temporary directory.
func adminTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	oldSettings, oldAudit, oldStats, oldLog := SETTINGS_FILE, auditFile, statsFile, logFile
	SETTINGS_FILE = filepath.Join(dir, "settings.json")
	auditFile = filepath.Join(dir, "audit.jsonl")
	statsFile = filepath.Join(dir, "stats.json")
	logFile = filepath.Join(dir, "gotty.log")
	settingsMu.Lock()
	siteSettings, settingsLoaded = SiteSettings{}, false
	settingsMu.Unlock()
	t.Cleanup(func() {
		SETTINGS_FILE, auditFile, statsFile, logFile = oldSettings, oldAudit, oldStats, oldLog
		settingsMu.Lock()
		siteSettings, settingsLoaded = SiteSettings{}, false
		settingsMu.Unlock()
		utils.SetGenieRates(0, 0)
		utils.SetKeyOverrides("", "")
		utils.SetExtraAdmins(nil)
		settingsMu.Lock()
		keysProblem = ""
		settingsMu.Unlock()
	})

	if err := user.OpenSessionDB(filepath.Join(dir, "users.db")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENREPL_ADMIN_EMAILS", "boss@example.com")

	s := &Server{options: &Options{
		Mode: ModeStandalone, Credential: "alice:hunter2-very-secret", EnableBasicAuth: true,
		WorkerToken: "worker-token-xyz", Address: "0.0.0.0", Port: "8080",
	}}
	s.admin.check = func(w http.ResponseWriter, r *http.Request) bool { return r.Header.Get("X-Test-Admin") == "yes" }
	s.admin.started = time.Now()
	mux := http.NewServeMux()
	s.registerAdmin(mux, "/")
	return s, mux
}

type adminCall struct {
	method, path, body string
	admin, header      bool
	origin             string
}

func (c adminCall) do(h http.Handler) *httptest.ResponseRecorder {
	r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.admin {
		r.Header.Set("X-Test-Admin", "yes")
	}
	if c.header {
		r.Header.Set(adminHeader, adminHeaderValue)
	}
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	return adminCall{method: "GET", path: path, admin: true}.do(h)
}

func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	return adminCall{method: "POST", path: path, body: body, admin: true, header: true}.do(h)
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON (%v): %s", err, w.Body.String())
	}
}

func TestAdminAPIAnswersOnlyAdmins(t *testing.T) {
	_, h := adminTestServer(t)
	for _, path := range []string{
		"/admin/gateway", "/admin/health", "/admin/audit", "/admin/stats", "/admin/logs",
		"/admin/settings", "/admin/feedback", "/admin/snippets", "/admin/users",
	} {
		w := adminCall{method: "GET", path: path}.do(h)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s for a visitor: %d, want 401", path, w.Code)
		}
		var e map[string]string
		decode(t, w, &e)
		if e["error"] == "" {
			t.Errorf("%s for a visitor: no error message in %s", path, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s is cacheable: %q", path, w.Header().Get("Cache-Control"))
		}
		if got := get(h, path); got.Code != http.StatusOK {
			t.Errorf("%s for an admin: %d %s", path, got.Code, got.Body.String())
		}
	}
	// the page too
	page := adminCall{method: "GET", path: "/admin"}.do(h)
	if page.Code != http.StatusUnauthorized {
		t.Errorf("/admin for a visitor: %d, want 401", page.Code)
	}
}

func TestAdminChangesNeedTheDashboardHeader(t *testing.T) {
	_, h := adminTestServer(t)
	body := `{"colorOfTheDay":true}`
	cases := []struct {
		name string
		call adminCall
		want int
	}{
		{"visitor", adminCall{method: "POST", path: "/admin/settings", body: body, header: true}, 401},
		{"no header", adminCall{method: "POST", path: "/admin/settings", body: body, admin: true}, 403},
		{"other origin", adminCall{method: "POST", path: "/admin/settings", body: body, admin: true, header: true, origin: "https://evil.example"}, 403},
		{"own origin", adminCall{method: "POST", path: "/admin/settings", body: body, admin: true, header: true, origin: "http://example.com"}, 200},
		{"no origin", adminCall{method: "POST", path: "/admin/settings", body: body, admin: true, header: true}, 200},
	}
	for _, c := range cases {
		if w := c.call.do(h); w.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	// a refused change changes nothing
	_, h2 := adminTestServer(t)
	adminCall{method: "POST", path: "/admin/settings", body: body, admin: true}.do(h2)
	if GetSiteSettings().ColorOfTheDay {
		t.Error("a change without the header was applied")
	}
}

func TestGatewayParametersNeverHoldSecrets(t *testing.T) {
	s, h := adminTestServer(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai-supersecret")
	t.Setenv("OPENREPL_FIREBASE_CONFIG", `{"apiKey":"AIzaSecretFirebaseKey123","authDomain":"d.firebaseapp.com","projectId":"dev-project"}`)
	s.options.Mode = ModeGateway
	s.options.WorkspaceSync = true

	w := get(h, "/admin/gateway")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{
		"hunter2", "alice", "worker-token-xyz", "sk-test-openai-supersecret", "AIzaSecretFirebaseKey123", "boss@example.com",
	} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("the parameters contain %q:\n%s", secret, w.Body.String())
		}
	}
	var p paramsReply
	decode(t, w, &p)
	if !p.Config.OpenAIKeySet || !p.Config.Firebase.Custom || p.Config.Firebase.ProjectID != "dev-project" {
		t.Errorf("config summary wrong: %+v", p.Config)
	}
	if p.Config.Admins != 1 || !p.Listen.BasicAuth || p.Gateway == nil || !p.Gateway.WorkersEnabled {
		t.Errorf("parameters wrong: %+v", p)
	}
}

func TestSettingsAreValidatedSavedAndAudited(t *testing.T) {
	_, h := adminTestServer(t)

	for name, body := range map[string]string{
		"not json":      `{`,
		"bad level":     `{"announcement":{"text":"hi","level":"scary"}}`,
		"long notice":   `{"announcement":{"text":"` + strings.Repeat("x", 281) + `"}}`,
		"long message":  `{"maintenance":{"message":"` + strings.Repeat("x", 281) + `"}}`,
		"bad language":  `{"disabledLanguages":["py thon"]}`,
		"rate too high": `{"genie":{"guestPerMinute":61}}`,
		"negative rate": `{"genie":{"userPerMinute":-1}}`,
	} {
		if w := post(h, "/admin/settings", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, w.Code, w.Body.String())
		}
	}
	if s := GetSiteSettings(); s.ColorOfTheDay || s.Announcement.Text != "" {
		t.Errorf("a refused change was applied: %+v", s)
	}

	w := post(h, "/admin/settings", `{
		"colorOfTheDay": true,
		"announcement": {"text": "  Back at noon  ", "level": ""},
		"maintenance": {"enabled": true, "message": "Upgrading"},
		"disabledLanguages": ["Python", "cpp", "python", " "],
		"genie": {"disabled": true, "guestPerMinute": 0.5, "userPerMinute": 3}
	}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got SiteSettings
	decode(t, w, &got)
	if got.Announcement.Text != "Back at noon" || got.Announcement.Level != "info" {
		t.Errorf("announcement not cleaned up: %+v", got.Announcement)
	}
	if strings.Join(got.DisabledLanguages, ",") != "cpp,python" {
		t.Errorf("languages = %v, want cpp,python", got.DisabledLanguages)
	}
	if utils.GuestFactor() != 0.5 || utils.UserFactor() != 3 {
		t.Errorf("Genie rates not applied: %v %v", utils.GuestFactor(), utils.UserFactor())
	}

	// kept on disk, and read back by a fresh start
	settingsMu.Lock()
	siteSettings, settingsLoaded = SiteSettings{}, false
	settingsMu.Unlock()
	utils.SetGenieRates(0, 0)
	if s := GetSiteSettings(); !s.Maintenance.Enabled || s.Announcement.Text != "Back at noon" || utils.UserFactor() != 3 {
		t.Errorf("settings not restored from the file: %+v", s)
	}

	// visitors see what the page needs, not the limits
	rec := httptest.NewRecorder()
	handleSettingsJS(rec, httptest.NewRequest("GET", "/settings.js", nil))
	js := rec.Body.String()
	for _, want := range []string{"Back at noon", `"genieDisabled":true`, `"disabledLanguages":["cpp","python"]`, `"colorOfTheDay":true`} {
		if !strings.Contains(js, want) {
			t.Errorf("settings.js lacks %s: %s", want, js)
		}
	}
	if strings.Contains(js, "guestPerMinute") || strings.Contains(js, "userPerMinute") {
		t.Errorf("settings.js shows the Genie limits: %s", js)
	}

	// the audit log says what changed, but does not repeat the announcement
	var audit struct{ Entries []auditEntry }
	decode(t, get(h, "/admin/audit"), &audit)
	if len(audit.Entries) < 5 {
		t.Fatalf("audit entries = %+v", audit.Entries)
	}
	all, _ := json.Marshal(audit.Entries)
	for _, want := range []string{"colour of the day on", "announcement changed", "maintenance mode on", "languages switched off: cpp, python", "Genie switched off"} {
		if !strings.Contains(string(all), want) {
			t.Errorf("audit log lacks %q: %s", want, all)
		}
	}
	if strings.Contains(string(all), "Back at noon") {
		t.Errorf("the audit log repeats the announcement: %s", all)
	}
}

func TestAuditLogIsKeptAndTrimmed(t *testing.T) {
	s, _ := adminTestServer(t)
	for i := 0; i < auditKeep+20; i++ {
		s.admin.audit.add(auditEntry{Time: time.Now().UTC().Format(time.RFC3339), Admin: "a@b.c", Action: "test", Detail: fmt.Sprint(i)})
	}
	recent := s.admin.audit.recent(3)
	if len(recent) != 3 || recent[0].Detail != fmt.Sprint(auditKeep+19) || recent[2].Detail != fmt.Sprint(auditKeep+17) {
		t.Errorf("newest first expected, got %+v", recent)
	}
	if n := len(s.admin.audit.recent(0)); n != auditKeep {
		t.Errorf("kept %d entries, want %d", n, auditKeep)
	}
	// a restart reads the file back
	var again auditLog
	if n := len(again.recent(0)); n != auditKeep {
		t.Errorf("after a restart %d entries, want %d", n, auditKeep)
	}
	if fi, err := os.Stat(auditFile); err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("audit file: %v %v", fi, err)
	}
}

// ---- terminals -----------------------------------------------------------------

func dialNotice(t *testing.T, url, path string, admin bool) (string, error) {
	t.Helper()
	hdr := http.Header{}
	if admin {
		hdr.Set("X-Test-Admin", "yes")
	}
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http")+path, hdr)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.WriteMessage(websocket.TextMessage, []byte(`{}`)) // the page sends its init message first
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = c.ReadMessage()
	if ce, ok := err.(*websocket.CloseError); ok {
		return ce.Text, nil
	}
	return "", err
}

func TestMaintenanceAndSwitchedOffLanguagesRefuseTerminals(t *testing.T) {
	s, _ := adminTestServer(t)
	s.upgrader = &websocket.Upgrader{}
	s.terminals = map[string]string{"ws": "", "ws_python": "python", "ws_c": "cling"}
	passed := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed++
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "ran"), time.Now().Add(time.Second))
		conn.SetReadDeadline(time.Now().Add(time.Second))
		conn.ReadMessage()
		conn.Close()
	})
	srv := httptest.NewServer(s.wrapControls(next, "/"))
	defer srv.Close()

	// nothing is switched off
	if reason, err := dialNotice(t, srv.URL, "/ws_python", false); err != nil || reason != "ran" {
		t.Fatalf("a normal terminal: %q %v", reason, err)
	}

	if err := SaveSiteSettings(SiteSettings{DisabledLanguages: []string{"python"}}); err != nil {
		t.Fatal(err)
	}
	before := passed
	reason, err := dialNotice(t, srv.URL, "/ws_python", false)
	if err != nil || !strings.HasPrefix(reason, noticePrefix) || !strings.Contains(reason, "switched off") {
		t.Errorf("a switched-off language: %q %v", reason, err)
	}
	if passed != before {
		t.Error("a switched-off language reached the terminal handler")
	}
	if reason, err := dialNotice(t, srv.URL, "/ws_c", false); err != nil || reason != "ran" {
		t.Errorf("another language is refused too: %q %v", reason, err)
	}
	if reason, err := dialNotice(t, srv.URL, "/ws_python", true); err != nil || reason != "ran" {
		t.Errorf("an admin may still use it: %q %v", reason, err)
	}

	if err := SaveSiteSettings(SiteSettings{Maintenance: Maintenance{Enabled: true, Message: "Back at noon"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ws", "/ws_python", "/ws_c"} {
		if reason, err := dialNotice(t, srv.URL, path, false); err != nil || reason != noticePrefix+"Back at noon" {
			t.Errorf("maintenance, %s: %q %v", path, reason, err)
		}
		if reason, err := dialNotice(t, srv.URL, path, true); err != nil || reason != "ran" {
			t.Errorf("maintenance, admin, %s: %q %v", path, reason, err)
		}
	}

	// ordinary requests and non-terminal routes are never held back
	for _, path := range []string{"/", "/ws_filebrowser", "/ws_unknown"} {
		w := httptest.NewRecorder()
		before := passed
		s.wrapControls(next, "/").ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if passed != before+1 {
			t.Errorf("%s did not reach the site", path)
		}
	}

	// a long message is cut to a valid close reason
	long := strings.Repeat("é", 100)
	SaveSiteSettings(SiteSettings{Maintenance: Maintenance{Enabled: true, Message: long}})
	reason, err = dialNotice(t, srv.URL, "/ws", false)
	if err != nil || len(reason) > 123 || !strings.HasPrefix(reason, noticePrefix) {
		t.Errorf("long message: %d bytes %q %v", len(reason), reason, err)
	}
}

// ---- usage numbers -------------------------------------------------------------

func TestUsageNumbers(t *testing.T) {
	adminTestServer(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := &statsStore{now: func() time.Time { return now }}

	s.record("v1", "python")
	s.record("v1", "python") // the same visitor again
	s.record("v2", "cpp")
	s.record("", "") // unknown visitor, plain shell
	now = now.AddDate(0, 0, 1)
	s.record("v1", "python")

	days := s.recent(3)
	if len(days) != 3 || days[0].Date != "2026-10-01" || days[2].Date != "2026-10-03" {
		t.Fatalf("days = %+v", days)
	}
	if d := days[1]; d.Date != "2026-10-02" || d.Visitors != 2 || d.Terminals != 4 || d.ByLang["python"] != 2 || d.ByLang["cpp"] != 1 || d.ByLang["default"] != 1 {
		t.Errorf("the second: %+v", d)
	}
	if d := days[2]; d.Visitors != 1 || d.Terminals != 1 {
		t.Errorf("the third: %+v", d)
	}
	if d := days[0]; d.Visitors != 0 || d.Terminals != 0 || d.ByLang == nil {
		t.Errorf("an empty day must be present and empty: %+v", d)
	}

	// kept across a restart; only the current day remembers who was seen
	s.flush()
	data, _ := ioutil.ReadFile(statsFile)
	if strings.Count(string(data), `"seen"`) != 1 {
		t.Errorf("only today should keep its visitors: %s", data)
	}
	again := &statsStore{now: func() time.Time { return now }}
	if d := again.recent(2)[0]; d.Visitors != 2 || d.Terminals != 4 {
		t.Errorf("after a restart: %+v", d)
	}
	again.record("v1", "python") // seen today before the restart: not counted again
	if d := again.recent(1)[0]; d.Visitors != 1 || d.Terminals != 2 {
		t.Errorf("a visitor counted twice across a restart: %+v", d)
	}

	// old days are forgotten
	for i := 0; i < statsKeepDays+5; i++ {
		now = now.AddDate(0, 0, 1)
		s.record("x", "go")
	}
	if len(s.days) > statsKeepDays {
		t.Errorf("%d days kept, want at most %d", len(s.days), statsKeepDays)
	}
}

func TestVisitorHashHidesTheVisitor(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	h := visitorHash(r, "2026-10-02")
	if strings.Contains(h, "203") || len(h) != 16 {
		t.Errorf("hash = %q", h)
	}
	if h == visitorHash(r, "2026-10-03") {
		t.Error("the same visitor must look different on another day")
	}
	r2 := httptest.NewRequest("GET", "/ws", nil)
	r2.RemoteAddr = "203.0.113.10:1234"
	if h == visitorHash(r2, "2026-10-02") {
		t.Error("two visitors share a hash")
	}
}

// ---- log viewer ----------------------------------------------------------------

func TestLogLinesLoseTheirCredentials(t *testing.T) {
	jwt := "eyJhbGciOiJSUzI1NiIsImtpZCI6IjEifQ.eyJpc3MiOiJodHRwczovL3NlY3VyZXRva2VuIn0.c2lnbmF0dXJlc2lnbmF0dXJl"
	cases := []struct{ in, gone, kept string }{
		{"Authorization: Bearer abcdefghijklmnop123", "abcdefghijklmnop123", "Authorization"},
		{"req header: Cookie: user-session=MTY5ODc2NTQzMnxEdi1CQkFFQ180SUFB", "MTY5ODc2NTQzMnxEdi1CQkFFQ180SUFB", "Cookie"},
		{"got token found in request, sessionID: " + jwt, jwt, "sessionID"},
		{`{"apiKey": "AIzaSyD-secret-value", "projectId": "p"}`, "AIzaSyD-secret-value", "projectId"},
		{"OPENREPL_OPENAI_API_KEY=sk-abc123def456", "sk-abc123def456", "OPENREPL_OPENAI_API_KEY"},
		{"password=hunter2 user=bob", "hunter2", "user=bob"},
		{"stored 0123456789abcdef0123456789abcdef0123456789abcdef value", "0123456789abcdef0123456789abcdef0123456789abcdef", "stored"},
		{"signed in vickey@example.com from 10.0.0.1", "ickey@example.com", "v***@example.com from 10.0.0.1"},
		{"X-Openrepl-Uid: u1234", "u1234", "X-Openrepl-Uid"},
	}
	for _, c := range cases {
		got := redactLine(c.in)
		if strings.Contains(got, c.gone) {
			t.Errorf("%q still shows %q: %q", c.in, c.gone, got)
		}
		if !strings.Contains(got, c.kept) {
			t.Errorf("%q lost %q: %q", c.in, c.kept, got)
		}
	}
	for _, plain := range []string{
		"2026/10/02 12:00:00 server.go:12: Connection closed by client: 10.0.0.1:5555, connections: 1/10",
		"HTTP server is listening at: http://0.0.0.0:8080/",
		"Exposing API for python",
	} {
		if got := redactLine(plain); got != plain {
			t.Errorf("an ordinary line was changed:\n%q\n%q", plain, got)
		}
	}
	if got := redactLine(strings.Repeat("a b ", 1000)); len(got) > logMaxLineLen+10 {
		t.Errorf("a long line was not cut: %d", len(got))
	}
}

func TestLogViewer(t *testing.T) {
	_, h := adminTestServer(t)
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "2026/10/02 12:00:%02d x.go:1: line %d\n", i%60, i)
	}
	b.WriteString("2026/10/02 12:05:00 x.go:1: Error: could not open db, token=abcd1234secret\n")
	b.WriteString("2026/10/02 12:05:01 x.go:1: user vickey@example.com signed in\n")
	if err := ioutil.WriteFile(logFile, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	type reply struct {
		Lines     []string
		Truncated bool
	}
	var r reply
	decode(t, get(h, "/admin/logs?lines=5"), &r)
	if len(r.Lines) != 5 || !strings.HasSuffix(r.Lines[4], "signed in") || !r.Truncated {
		t.Fatalf("last lines: %+v", r)
	}
	if strings.Contains(strings.Join(r.Lines, "\n"), "vickey@") || !strings.Contains(r.Lines[4], "v***@example.com") {
		t.Errorf("the email address is not masked: %v", r.Lines)
	}

	decode(t, get(h, "/admin/logs?level=error"), &r)
	if len(r.Lines) != 1 || strings.Contains(r.Lines[0], "abcd1234secret") || !strings.Contains(r.Lines[0], "could not open db") {
		t.Errorf("errors only: %v", r.Lines)
	}

	decode(t, get(h, "/admin/logs?q=LINE%2012&lines=1000"), &r)
	if len(r.Lines) != 11 { // line 12, 120-129
		t.Errorf("search found %d lines: %v", len(r.Lines), r.Lines)
	}

	decode(t, get(h, "/admin/logs?lines=99999"), &r)
	if len(r.Lines) > logMaxRows {
		t.Errorf("%d lines returned, limit %d", len(r.Lines), logMaxRows)
	}

	os.Remove(logFile)
	var none struct {
		Lines []string
		Note  string
	}
	decode(t, get(h, "/admin/logs"), &none)
	if len(none.Lines) != 0 || none.Note == "" {
		t.Errorf("no log file: %+v", none)
	}
}

// ---- feedback ------------------------------------------------------------------

func withFeedbackDB(t *testing.T, records map[string]feedback) {
	t.Helper()
	db, err := persist.Open(filepath.Join(t.TempDir(), "feedback.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := feedback_db_handle
	feedback_db_handle = db
	t.Cleanup(func() { feedback_db_handle = old; db.Close() })
	for k, fb := range records {
		data, _ := json.Marshal(fb)
		if err := db.Store([]byte(k), data); err != nil {
			t.Fatal(err)
		}
	}
	db.Commit()
}

func TestFeedbackInbox(t *testing.T) {
	_, h := adminTestServer(t)
	withFeedbackDB(t, map[string]feedback{
		"1790000000000000001": {Name: "Ann", Email: "ann@example.com", Message: "Love it", Read: true},
		"1790000000000000003": {Name: "=cmd|' /C calc'!A0", Email: "+x@y.z", Message: "-2+3"},
		"1790000000000000002": {Name: "Bob", Message: "Please add Zig, \"quoted\"\nand more"},
	})

	var list struct {
		Feedback []feedbackRow
		Unread   int
	}
	decode(t, get(h, "/admin/feedback"), &list)
	if len(list.Feedback) != 3 || list.Unread != 2 {
		t.Fatalf("list = %+v", list)
	}
	if list.Feedback[0].ID != "1790000000000000003" || list.Feedback[2].ID != "1790000000000000001" {
		t.Errorf("not newest first: %+v", list.Feedback)
	}
	if !list.Feedback[2].Read || list.Feedback[1].Read {
		t.Errorf("read flags wrong: %+v", list.Feedback)
	}
	if list.Feedback[0].Time == "" {
		t.Error("no time")
	}

	// a flag set on one record does not show on the next one read
	if w := post(h, "/admin/feedback/1790000000000000002/read", ""); w.Code != 200 {
		t.Fatalf("mark read: %d %s", w.Code, w.Body.String())
	}
	decode(t, get(h, "/admin/feedback"), &list)
	if list.Unread != 1 {
		t.Errorf("unread = %d, want 1: %+v", list.Unread, list.Feedback)
	}
	post(h, "/admin/feedback/1790000000000000002/unread", "")
	decode(t, get(h, "/admin/feedback"), &list)
	if list.Unread != 2 {
		t.Errorf("unread = %d, want 2", list.Unread)
	}

	// the CSV cannot run as a spreadsheet formula
	w := get(h, "/admin/feedback?format=csv")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("csv headers: %v", w.Header())
	}
	csv := w.Body.String()
	for _, bad := range []string{",=cmd", ",+x@y.z", ",-2+3"} {
		if strings.Contains(csv, bad) {
			t.Errorf("the CSV has a cell starting a formula (%s):\n%s", bad, csv)
		}
	}
	for _, want := range []string{"'=cmd", "'+x@y.z", "'-2+3", `"Please add Zig, ""quoted""`} {
		if !strings.Contains(csv, want) {
			t.Errorf("the CSV lacks %s:\n%s", want, csv)
		}
	}

	// delete, with an entry in the audit log
	if w := post(h, "/admin/feedback/1790000000000000003/delete", ""); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	decode(t, get(h, "/admin/feedback"), &list)
	if len(list.Feedback) != 2 {
		t.Errorf("after delete: %+v", list.Feedback)
	}
	if w := post(h, "/admin/feedback/1790000000000000003/delete", ""); w.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d", w.Code)
	}
	if w := post(h, "/admin/feedback/12ab/delete", ""); w.Code != http.StatusNotFound {
		t.Errorf("a bad id: %d", w.Code)
	}
	if w := post(h, "/admin/feedback/1790000000000000003/frobnicate", ""); w.Code != http.StatusNotFound {
		t.Errorf("a bad action: %d", w.Code)
	}
	if w := get(h, "/admin/audit"); !strings.Contains(w.Body.String(), "feedback delete") {
		t.Errorf("no audit entry: %s", w.Body.String())
	}
}

// ---- shared code ---------------------------------------------------------------

func TestSnippetModeration(t *testing.T) {
	_, h := adminTestServer(t)
	db, err := persist.Open(filepath.Join(t.TempDir(), "snippets.db"))
	if err != nil {
		t.Fatal(err)
	}
	snippet_db_handle = db
	t.Cleanup(func() { snippet_db_handle = nil; db.Close() })

	a, err := storeSnippet(snippet{Lang: "python", Code: "print('hi')", Created: 1790000000})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := storeSnippet(snippet{Lang: "go", Code: strings.Repeat("x", 500), Created: 1790000100})

	var list struct{ Snippets []snippetRow }
	decode(t, get(h, "/admin/snippets"), &list)
	if len(list.Snippets) != 2 || list.Snippets[0].ID != b || list.Snippets[1].ID != a {
		t.Fatalf("list = %+v", list.Snippets)
	}
	if list.Snippets[0].Bytes != 500 || len([]rune(list.Snippets[0].Preview)) > 161 || list.Snippets[0].Code != "" {
		t.Errorf("a listing must carry a short preview only: %+v", list.Snippets[0])
	}

	var one snippetRow
	decode(t, get(h, "/admin/snippets/"+a), &one)
	if one.Code != "print('hi')" || one.Lang != "python" {
		t.Errorf("snippet = %+v", one)
	}
	if w := get(h, "/admin/snippets/zzzzzzzz"); w.Code != http.StatusNotFound {
		t.Errorf("unknown snippet: %d", w.Code)
	}

	if w := post(h, "/admin/snippets/"+a+"/delete", ""); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, ok := fetchSnippet(a); ok {
		t.Error("the snippet is still there")
	}
	if w := post(h, "/admin/snippets/"+a+"/delete", ""); w.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d", w.Code)
	}
	if w := post(h, "/admin/snippets/not-an-id/delete", ""); w.Code != http.StatusNotFound {
		t.Errorf("a bad id: %d", w.Code)
	}

	// no snippet database: the dashboard is told
	snippet_db_handle = nil
	var off struct {
		Snippets []snippetRow
		Off      bool
	}
	decode(t, get(h, "/admin/snippets"), &off)
	if !off.Off || len(off.Snippets) != 0 {
		t.Errorf("without a database: %+v", off)
	}
}

// ---- accounts ------------------------------------------------------------------

func signIn(t *testing.T, uid, email string) {
	t.Helper()
	u := &user.User{Uid: uid, Name: uid, Email: email}
	u.StsTokenManager.AccessToken = "token-" + uid
	u.StsTokenManager.ExpirationTime = utils.GetUnixMilli() + 3600*1000
	ss := &user.UserSession{User: u}
	ss.Update(u)
	if err := user.UpdateAndStoreSessionData(uid, ss.SessionID, ss, false); err != nil {
		t.Fatal(err)
	}
}

func TestAccountsCanBeSignedOutAndBlocked(t *testing.T) {
	_, h := adminTestServer(t)
	signIn(t, "uid-ann", "ann@example.com")
	signIn(t, "uid-boss", "boss@example.com")

	type row struct {
		Uid, Email string
		Sessions   int
		Blocked    bool
		Admin      bool
	}
	var list struct{ Users []row }
	decode(t, get(h, "/admin/users"), &list)
	if len(list.Users) != 2 {
		t.Fatalf("users = %+v (the stored secret or a pin must not be listed)", list.Users)
	}
	if list.Users[0].Email != "ann@example.com" || list.Users[0].Sessions != 1 || list.Users[0].Admin {
		t.Errorf("ann = %+v", list.Users[0])
	}
	if !list.Users[1].Admin {
		t.Errorf("boss should be marked as an admin: %+v", list.Users[1])
	}

	if user.IsSessionExpired("uid-ann", "token-uid-ann") {
		t.Fatal("ann's session should be live")
	}
	if w := post(h, "/admin/users/uid-ann/signout", ""); w.Code != 200 {
		t.Fatalf("sign out: %d %s", w.Code, w.Body.String())
	}
	if !user.IsSessionExpired("uid-ann", "token-uid-ann") {
		t.Error("ann is still signed in")
	}
	signIn(t, "uid-ann", "ann@example.com") // she may sign in again

	if w := post(h, "/admin/users/uid-boss/block", ""); w.Code != http.StatusBadRequest {
		t.Errorf("blocking an admin: %d, want 400", w.Code)
	}
	if w := post(h, "/admin/users/uid-ann/block", ""); w.Code != 200 {
		t.Fatalf("block: %d %s", w.Code, w.Body.String())
	}
	if !user.IsBlocked("uid-ann") || !user.IsSessionExpired("uid-ann", "token-uid-ann") {
		t.Error("ann is not blocked")
	}
	decode(t, get(h, "/admin/users"), &list)
	if !list.Users[0].Blocked || list.Users[0].Sessions != 0 {
		t.Errorf("ann after blocking: %+v", list.Users[0])
	}
	u := &user.User{Uid: "uid-ann", Email: "ann@example.com"}
	u.StsTokenManager.AccessToken = "again"
	u.StsTokenManager.ExpirationTime = utils.GetUnixMilli() + 3600*1000
	ss := &user.UserSession{User: u}
	ss.Update(u)
	if err := user.UpdateAndStoreSessionData("uid-ann", "again", ss, false); err != user.ErrBlocked {
		t.Errorf("a blocked account signed in: %v", err)
	}

	if w := post(h, "/admin/users/uid-ann/unblock", ""); w.Code != 200 {
		t.Fatalf("unblock: %d %s", w.Code, w.Body.String())
	}
	if user.IsBlocked("uid-ann") {
		t.Error("ann is still blocked")
	}
	signIn(t, "uid-ann", "ann@example.com")

	for _, bad := range []string{"/admin/users/nobody/block", "/admin/users/uid-ann/delete", "/admin/users/bad%20id/block"} {
		if w := post(h, bad, ""); w.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", bad, w.Code)
		}
	}
	audit := get(h, "/admin/audit").Body.String()
	for _, want := range []string{"user sign out", "user block", "user unblock", "ann@example.com"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit log lacks %q: %s", want, audit)
		}
	}
}

// The dashboard shows today's colour of the day. Its list of colours is a copy
// of the one in the page script; this fails when the two drift apart.
func TestDashboardColoursMatchThePage(t *testing.T) {
	colours := func(file, start, end string) []string {
		data, err := ioutil.ReadFile(file)
		if err != nil {
			t.Skip("source file not available: ", err)
		}
		text := string(data)
		i := strings.Index(text, start)
		if i < 0 {
			t.Fatalf("%s has no %q", file, start)
		}
		text = text[i:]
		text = text[:strings.Index(text, end)]
		var out []string
		for _, m := range regexp.MustCompile(`'(#[0-9a-fA-F]{6})'`).FindAllStringSubmatch(text, -1) {
			out = append(out, strings.ToLower(m[1]))
		}
		return out
	}
	page := colours("../js/src/preprocessing.js", "var PopularColors = new Array(", ");")
	dash := colours("../resources/js/admin.js", "var POPULAR_COLOURS = [", "];")
	if len(page) == 0 || strings.Join(page, ",") != strings.Join(dash, ",") {
		t.Errorf("the colour lists differ:\npage:      %v\ndashboard: %v", page, dash)
	}
}

// ---- the real admin check ------------------------------------------------------

// sessionCookie signs the user in the way the site does and returns the cookie
// the browser would hold.
func sessionCookie(t *testing.T, uid, email string) *http.Cookie {
	t.Helper()
	signIn(t, uid, email)
	up, err := user.FetchUserProfileData(uid)
	if err != nil || len(up.SessionMap) != 1 {
		t.Fatalf("profile of %s: %+v %v", uid, up, err)
	}
	var session user.UserSession
	for _, s := range up.SessionMap {
		session = s
	}
	session.LogIn()
	rec := httptest.NewRecorder()
	if err := cookie.Set_SessionCookie(rec, httptest.NewRequest("GET", "/", nil), session); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie was issued")
	}
	return cookies[0]
}

func TestTheAdminCheckUsesTheSignedInAccount(t *testing.T) {
	s, _ := adminTestServer(t)
	s.admin.check = nil // the real check: a session cookie and the admin list
	mux := http.NewServeMux()
	s.registerAdmin(mux, "/")

	call := func(c *http.Cookie) int {
		r := httptest.NewRequest("GET", "/admin/health", nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	boss := sessionCookie(t, "uid-boss", "boss@example.com")
	ann := sessionCookie(t, "uid-ann", "ann@example.com")

	if got := call(nil); got != http.StatusUnauthorized {
		t.Errorf("no cookie -> %d, want 401", got)
	}
	if got := call(ann); got != http.StatusUnauthorized {
		t.Errorf("a signed-in user who is not an admin -> %d, want 401", got)
	}
	if got := call(boss); got != http.StatusOK {
		t.Errorf("an admin -> %d, want 200", got)
	}

	// signed out everywhere, or blocked, the admin is out
	if err := user.SignOutEverywhere("uid-boss"); err != nil {
		t.Fatal(err)
	}
	if got := call(boss); got != http.StatusUnauthorized {
		t.Errorf("an admin who was signed out -> %d, want 401", got)
	}
	boss = sessionCookie(t, "uid-boss", "boss@example.com")
	if got := call(boss); got != http.StatusOK {
		t.Errorf("an admin signed in again -> %d, want 200", got)
	}
	if err := user.SetBlocked("uid-boss", true); err != nil {
		t.Fatal(err)
	}
	if got := call(boss); got != http.StatusUnauthorized {
		t.Errorf("a blocked admin -> %d, want 401", got)
	}
}

func TestOnlyAdminsAreToldTheyAreAdmins(t *testing.T) {
	adminTestServer(t)
	for _, c := range []struct {
		email string
		code  int
		want  bool
	}{
		{"boss@example.com", http.StatusOK, true},
		{"ann@example.com", http.StatusOK, false},
		{"", http.StatusOK, false},
		{"boss@example.com", http.StatusUnauthorized, false},
	} {
		w := httptest.NewRecorder()
		handleUserProfileJson(w, httptest.NewRequest("GET", "/profile?q=json", nil), c.code, user.UserProfile{Uid: "u", Email: c.email})
		var got struct {
			IsAdmin bool `json:"isAdmin"`
			Email   string
		}
		decode(t, w, &got)
		if got.IsAdmin != c.want {
			t.Errorf("%q with status %d: isAdmin = %v, want %v", c.email, c.code, got.IsAdmin, c.want)
		}
	}
}

func TestTheAdminPageCarriesItsSecurityHeaders(t *testing.T) {
	_, mux := adminTestServer(t)
	if _, err := Asset("static/admin.html"); err != nil {
		t.Skip("admin.html is not built into this binary (run make asset)")
	}
	w := get(mux, "/admin")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "OpenREPL Admin") {
		t.Fatalf("/admin -> %d %.80s", w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows inline code: %s", csp)
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", w.Header())
	}
	// the page loads only its own script and style, and has no inline script or style attribute
	body := w.Body.String()
	if regexp.MustCompile(`<script(\s[^>]*)?>\s*[^<\s]`).MatchString(body) || strings.Contains(body, " style=") || strings.Contains(body, " onclick=") {
		t.Error("admin.html has inline script or style, which its CSP would block")
	}
}

// ---- Genie ---------------------------------------------------------------------

func TestGenieCanBeSwitchedOff(t *testing.T) {
	adminTestServer(t)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handleChatProxy(w, httptest.NewRequest("POST", "/chat/completions", strings.NewReader("{}")))
		return w
	}
	// switched on, the first check of the proxy (the origin) answers
	if w := call(); w.Code == http.StatusServiceUnavailable {
		t.Fatalf("Genie answered 503 while it is on: %s", w.Body.String())
	}
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{Disabled: true}}); err != nil {
		t.Fatal(err)
	}
	w := call()
	var e ErrorResponse
	decode(t, w, &e)
	if w.Code != http.StatusServiceUnavailable || e.Error.Type != "GenieDisabled" || !strings.Contains(e.Error.Message, "switched off") {
		t.Errorf("a visitor asking a switched-off Genie: %d %+v", w.Code, e)
	}
}
