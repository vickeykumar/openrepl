package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"ideusage"
	"webtty"
)

// the IDE data usage (ideusage.go, LLD 20): what is counted, what the limit holds back, the speed cap of a terminal

func ideTestServer(t *testing.T) *Server {
	t.Helper()
	s, _ := adminTestServer(t)
	s.upgrader = &websocket.Upgrader{}
	s.terminals = map[string]string{"ws": "", "ws_python": "python"}
	ideMeter = ideusage.New()
	oldWho := ideWho
	t.Cleanup(func() { ideWho = oldWho })
	return s
}

func asUser(key string, guest bool) {
	ideWho = func(*http.Request) ideusage.Identity {
		id := ideusage.Identity{Key: key, Guest: guest, Signed: !guest}
		if !guest {
			id.UID = "uid-" + key
		}
		return id
	}
}

func TestIDERoutesAreSortedIntoParts(t *testing.T) {
	s := ideTestServer(t)
	cases := []struct {
		method, target string
		part           ideusage.Part
		kind           ideKind
	}{
		{"GET", "/ws", ideusage.Terminal, ideTerminal},
		{"GET", "/ws_python", ideusage.Terminal, ideTerminal},
		{"GET", "/ws_filebrowser?q=load&filepath=/home/x/a.py", ideusage.Editor, ideEditor},
		{"GET", "/ws_filebrowser?q=zip", ideusage.Files, ideHeavy},
		{"GET", "/ws_filebrowser?q=usage", ideusage.Files, ideCount},
		{"GET", "/ws_filebrowser", ideusage.Files, ideCount},
		{"POST", "/chat/completions", ideusage.Genie, ideHeavy},
		{"POST", "/agent_bash", ideusage.Genie, ideHeavy},
		{"POST", "/agent_files", ideusage.Genie, ideCount},
		{"POST", "/genie_chats", ideusage.Genie, ideCount},
		{"GET", "/preview/tok/f/index.html", ideusage.Preview, ideHeavy},
		{"GET", "/preview/lib/eruda/eruda.js", 0, ideNone},
		{"GET", "/", 0, ideNone},
		{"GET", "/js/main.js", 0, ideNone},
		{"GET", "/profile", 0, ideNone},
		{"GET", "/faq", 0, ideNone},
		{"GET", "/store/api/plugins", 0, ideNone},
		{"GET", "/preview_ports", 0, ideNone},
		{"GET", "/admin/settings", 0, ideNone},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.target, nil)
		part, kind := s.ideRoute(r)
		if kind != c.kind || (kind != ideNone && part != c.part) {
			t.Errorf("%s %s: got part %v kind %v, want part %v kind %v", c.method, c.target, part, kind, c.part, c.kind)
		}
	}
}

func TestIDECountsWhatAnHTTPPartSendsAndNothingElse(t *testing.T) {
	s := ideTestServer(t)
	asUser("home1", false)
	body := strings.Repeat("z", 150<<10)
	h := s.wrapIDEMeter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws_filebrowser?q=load&filepath=/x", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/agent_files", strings.NewReader(strings.Repeat("in", 1<<20)))) // what comes in is not counted
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/js/main.js", nil))                                              // the website is not counted
	st := ideMeter.StateOf(ideusage.Identity{Key: "home1"})
	if st.Parts["editor"] != int64(len(body)) || st.Parts["genie"] != int64(len(body)) || st.Parts["files"] != 0 || st.Used != 2*int64(len(body)) {
		t.Fatalf("counted: %+v", st.Parts)
	}
}

func TestIDETrustedRequestsAreNotCountedAgain(t *testing.T) {
	s := ideTestServer(t)
	asUser("home1", false)
	h := s.wrapIDEMeter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
	r := httptest.NewRequest("GET", "/ws_filebrowser?q=load", nil)
	r = r.WithContext(context.WithValue(r.Context(), trustedConnKey{}, true))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if ideMeter.StateOf(ideusage.Identity{Key: "home1"}).Used != 0 {
		t.Fatal("a request that came over the tunnel was counted at the worker")
	}
}

func TestIDEAtTheLimitTheHeavyPartsStopAndASmallFileStillOpens(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDEUserMB: 1, IDEReserveMB: 2, IDESmallKB: 100}); err != nil {
		t.Fatal(err)
	}
	asUser("home1", false)
	ideMeter.Add(ideusage.Identity{Key: "home1", UID: "uid-home1", Signed: true}, ideusage.Terminal, 2*ideusage.MB)
	var sawMax string
	reached := 0
	h := s.wrapIDEMeter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		sawMax = r.URL.Query().Get("max")
		w.Write([]byte("ok"))
	}))
	do := func(method, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
		return rec
	}
	if rec := do("POST", "/chat/completions"); rec.Code != 429 || !strings.Contains(rec.Body.String(), "Daily data limit reached") || reached != 0 {
		t.Errorf("a new Genie question at the limit: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/agent_bash"); rec.Code != 429 {
		t.Errorf("an agent step at the limit: %d", rec.Code)
	}
	if rec := do("GET", "/ws_filebrowser?q=zip"); rec.Code != 429 {
		t.Errorf("a download at the limit: %d", rec.Code)
	}
	if rec := do("GET", "/preview/tok/f/index.html"); rec.Code != 429 || !strings.Contains(rec.Body.String(), "Daily data limit reached") {
		t.Errorf("the Preview at the limit: %d %q", rec.Code, rec.Body.String())
	}
	// what is small and kept open: the file list, a small file for the editor (at most 100 KB), saving
	if rec := do("GET", "/ws_filebrowser?q=usage"); rec.Code != 200 {
		t.Errorf("the file list at the limit: %d", rec.Code)
	}
	if rec := do("GET", "/ws_filebrowser?q=load&filepath=/x"); rec.Code != 200 || sawMax != "102400" {
		t.Errorf("a file for the editor at the limit: %d max=%q (want 102400)", rec.Code, sawMax)
	}
	if rec := do("POST", "/agent_files"); rec.Code != 200 {
		t.Errorf("a Genie file read at the limit: %d", rec.Code)
	}
	// the website is never held back
	if rec := do("GET", "/faq"); rec.Code != 200 {
		t.Errorf("the website at the limit: %d", rec.Code)
	}
	// when the reserve is spent, even the editor stops
	ideMeter.Add(ideusage.Identity{Key: "home1", UID: "uid-home1", Signed: true}, ideusage.Editor, 3*ideusage.MB)
	if rec := do("GET", "/ws_filebrowser?q=load&filepath=/x"); rec.Code != 429 {
		t.Errorf("the editor after the reserve is spent: %d", rec.Code)
	}
}

func TestIDEAdminsAreExemptUnlessCounted(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDEUserMB: 1}); err != nil {
		t.Fatal(err)
	}
	ideWho = func(*http.Request) ideusage.Identity {
		return ideusage.Identity{Key: "boss", UID: "uid-boss", Signed: true, Admin: true}
	}
	ideMeter.Add(ideusage.Identity{Key: "boss", UID: "uid-boss", Signed: true}, ideusage.Terminal, 5*ideusage.MB)
	h := s.wrapIDEMeter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/chat/completions", nil))
	if rec.Code != 200 {
		t.Fatalf("an exempt admin at the limit: %d", rec.Code)
	}
	if err := SaveSiteSettings(SiteSettings{IDEUserMB: 1, IDEAdminsCounted: true}); err != nil {
		t.Fatal(err)
	}
	ideMeter.Add(ideusage.Identity{Key: "boss", UID: "uid-boss", Signed: true, Admin: true}, ideusage.Terminal, 5*ideusage.MB)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/chat/completions", nil))
	if rec.Code != 429 {
		t.Fatalf("a counted admin at the limit: %d", rec.Code)
	}
}

// dialTerminal reads a terminal's messages until it ends: the decoded output, the end notice and the close text.
func dialIDETerminal(t *testing.T, srv *httptest.Server, path string) (out string, bytesIn int, endReason string) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c, _, err := websocket.DefaultDialer.Dial("ws://"+u.Host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			if ce, ok := err.(*websocket.CloseError); ok {
				endReason = ce.Text
			}
			return out, bytesIn, endReason
		}
		bytesIn += len(msg)
		if len(msg) > 0 && msg[0] == webtty.Output {
			if b, err := base64.StdEncoding.DecodeString(string(msg[1:])); err == nil && len(b) < 400 {
				out += string(b)
			}
		}
		if len(msg) > 0 && msg[0] == webtty.End {
			var e struct {
				Reason string `json:"reason"`
			}
			json.Unmarshal(msg[1:], &e)
			endReason = e.Reason
		}
	}
}

func floodHandler(s *Server, frames int) http.Handler {
	chunk := []byte(string([]byte{webtty.Output}) + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 48<<10))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.WriteMessage(websocket.TextMessage, []byte(string([]byte{webtty.SetWindowTitle})+"title"))
		for i := 0; i < frames; i++ {
			if err := conn.WriteMessage(websocket.TextMessage, chunk); err != nil {
				return
			}
		}
	})
}

func TestIDETerminalThatPrintsTooFastIsPausedThenEnded(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDETermMB: 1, IDETermWindowSecs: 10, IDETermGraceSecs: 1}); err != nil {
		t.Fatal(err)
	}
	asUser("home1", false)
	srv := httptest.NewServer(s.wrapIDEMeter(floodHandler(s, 100000))) // would be about 6 GB
	defer srv.Close()
	start := time.Now()
	out, got, reason := dialIDETerminal(t, srv, "/ws_python")
	if !strings.Contains(out, "Output was too fast (over 1 MB in 10 s), so this run was stopped") {
		t.Fatalf("the notice was not sent: %q", out)
	}
	if reason != "terminal output too fast" {
		t.Fatalf("end reason %q", reason)
	}
	if got > 3<<20 {
		t.Fatalf("%d bytes went out for a 1 MB cap", got)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v: the run was not ended after the grace", time.Since(start))
	}
	if used := ideMeter.StateOf(ideusage.Identity{Key: "home1"}).Parts["terminal"]; used < 900<<10 || used > 3<<20 {
		t.Fatalf("the bytes sent are counted: %d", used)
	}
}

func TestIDENormalTerminalIsNotHeldBack(t *testing.T) {
	s := ideTestServer(t)
	asUser("home1", false)
	srv := httptest.NewServer(s.wrapIDEMeter(floodHandler(s, 20))) // about 1.3 MB, far under 20 MB
	defer srv.Close()
	out, got, reason := dialIDETerminal(t, srv, "/ws_python")
	if strings.Contains(out, "too fast") || got < 1<<20 {
		t.Fatalf("a normal terminal was held back: %d bytes %q (%s)", got, out, reason)
	}
	if st := ideMeter.StateOf(ideusage.Identity{Key: "home1"}); st.Parts["terminal"] < 1<<20 {
		t.Fatalf("counted %d", st.Parts["terminal"])
	}
}

func TestIDETerminalOfAUserAtTheLimitIsEndedWithTheNotice(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDEGuestMB: 1, IDEReserveMB: 1}); err != nil {
		t.Fatal(err)
	}
	asUser("guest-abc", true)
	ideMeter.Add(ideusage.Identity{Key: "guest-abc", Guest: true}, ideusage.Terminal, 5*ideusage.MB)
	srv := httptest.NewServer(s.wrapIDEMeter(floodHandler(s, 50)))
	defer srv.Close()
	out, _, reason := dialIDETerminal(t, srv, "/ws")
	if !strings.Contains(out, "You have used this session's 1 MB") || !strings.Contains(out, "Sign in free to get 200 MB a day") || reason != "data limit reached" {
		t.Fatalf("the limit notice: %q reason %q", out, reason)
	}
}

func TestIDEPauseOnlyNeverEndsTheRun(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDETermMB: 1, IDETermWindowSecs: 1, IDETermGraceSecs: 1, IDETermPauseOnly: true}); err != nil {
		t.Fatal(err)
	}
	asUser("home1", false)
	srv := httptest.NewServer(s.wrapIDEMeter(floodHandler(s, 40))) // 2.6 MB over a 1 MB per second cap: a few seconds
	defer srv.Close()
	out, got, _ := dialIDETerminal(t, srv, "/ws_python")
	if strings.Contains(out, "too fast") || got < 2<<20 {
		t.Fatalf("pause only ended the run: %d bytes %q", got, out)
	}
}

func TestIDEUsageEndpointAndSettingsCheck(t *testing.T) {
	s := ideTestServer(t)
	asUser("home1", false)
	ideMeter.Add(ideusage.Identity{Key: "home1", UID: "uid-home1", Signed: true}, ideusage.Genie, 3*ideusage.MB)
	rec := httptest.NewRecorder()
	s.handleIDEUsage(rec, httptest.NewRequest("GET", "/ide_usage?tz=330&days=3", nil))
	var out struct {
		State   ideusage.State `json:"state"`
		History []ideusage.Day `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.State.Used != 3*ideusage.MB || out.State.Limit != 200*ideusage.MB || len(out.History) != 3 || out.History[2].Total != 3*ideusage.MB {
		t.Fatalf("endpoint: %+v", out)
	}
	if _, _, tz, has := ideMeter.Known("home1"); !has || tz != 330 {
		t.Fatal("the page's time zone was not kept")
	}
	if got := ideResetText(ideusage.Identity{Key: "home1"}, out.State); !strings.Contains(got, "5:30 AM") || !strings.Contains(got, "UTC+5:30") {
		t.Fatalf("reset text for an IST page: %q", got)
	}
	for _, bad := range []SiteSettings{{IDETermMB: -1}, {IDEWarnPct: 100}, {IDEResetMinutes: 1440}, {IDEPartsOff: []string{"nope"}}, {IDEUserMB: 99999999}} {
		if _, err := bad.normalize(); err == nil {
			t.Errorf("settings %+v were accepted", bad)
		}
	}
	good, err := SiteSettings{IDEPartsOff: []string{"preview", "terminal", "preview"}, IDEResetMinutes: 330}.normalize()
	if err != nil || strings.Join(good.IDEPartsOff, ",") != "preview,terminal" {
		t.Fatalf("good settings: %v %v", good.IDEPartsOff, err)
	}
	c := SiteSettings{}.ideConfig()
	if !c.On || !c.AdminsExempt || c.TermBytes != 20*ideusage.MB || c.GuestBytes != 50*ideusage.MB || c.UserBytes != 200*ideusage.MB || c.WarnPct != 80 || c.ReserveBytes != 2*ideusage.MB || c.SmallBytes != 256<<10 {
		t.Fatalf("the built-in numbers: %+v", c)
	}
}

// the gateway forwards a terminal with a reverse proxy: the cap and the notice hold there too
func TestIDECapHoldsWhenTheTerminalIsForwardedByTheGateway(t *testing.T) {
	s := ideTestServer(t)
	if err := SaveSiteSettings(SiteSettings{IDETermMB: 1, IDETermWindowSecs: 10, IDETermGraceSecs: 1}); err != nil {
		t.Fatal(err)
	}
	asUser("home1", false)
	backend := httptest.NewServer(floodHandler(s, 100000))
	defer backend.Close()
	bu, _ := url.Parse(backend.URL)
	proxy := httputil.NewSingleHostReverseProxy(bu)
	front := httptest.NewServer(s.wrapIDEMeter(proxy))
	defer front.Close()
	out, got, reason := dialIDETerminal(t, front, "/ws_python")
	if !strings.Contains(out, "Output was too fast") || reason != "terminal output too fast" || got > 3<<20 {
		t.Fatalf("through a proxy: %d bytes, reason %q, output %q", got, reason, out)
	}
}

func TestIDEAdminEndpointsListTheDayAndResetByHand(t *testing.T) {
	s := ideTestServer(t)
	a := ideusage.Identity{Key: "home-a", UID: "uid-a", Signed: true}
	ideMeter.Add(a, ideusage.Terminal, 120*ideusage.MB)
	ideMeter.Add(ideusage.Identity{Key: "home-b", UID: "uid-b", Signed: true}, ideusage.Genie, 30*ideusage.MB)
	ideMeter.Add(ideusage.Identity{Key: "guest-x", Guest: true}, ideusage.Terminal, 8*ideusage.MB)
	rec := httptest.NewRecorder()
	s.handleAdminIDEUsage(rec, httptest.NewRequest("GET", "/admin/ide-usage?top=1", nil))
	var sum ideusage.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.SignedCount != 2 || sum.Total != 158*ideusage.MB || sum.GuestBytes != 8*ideusage.MB || len(sum.Top) != 1 || sum.Top[0].UID != "uid-a" {
		t.Fatalf("summary: %+v", sum)
	}
	// one account
	rec = httptest.NewRecorder()
	s.handleAdminIDEUsage(rec, httptest.NewRequest("GET", "/admin/ide-usage?uid=uid-a", nil))
	var one struct {
		State ideusage.State `json:"state"`
	}
	json.Unmarshal(rec.Body.Bytes(), &one)
	if one.State.Used != 120*ideusage.MB {
		t.Fatalf("one account: %+v", one.State)
	}
	// a reset needs the dashboard's header
	rec = httptest.NewRecorder()
	s.handleAdminIDEUsage(rec, httptest.NewRequest("POST", "/admin/ide-usage?uid=uid-a", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a reset without the header: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/admin/ide-usage?uid=uid-a", nil)
	req.Header.Set(adminHeader, adminHeaderValue)
	rec = httptest.NewRecorder()
	s.handleAdminIDEUsage(rec, req)
	if rec.Code != 200 || ideMeter.StateOf(a).Used != 0 {
		t.Fatalf("reset: %d used %d", rec.Code, ideMeter.StateOf(a).Used)
	}
	req = httptest.NewRequest("POST", "/admin/ide-usage?uid=nobody", nil)
	req.Header.Set(adminHeader, adminHeaderValue)
	rec = httptest.NewRecorder()
	s.handleAdminIDEUsage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reset of an unknown account: %d", rec.Code)
	}
}

// the history for admins: the days of all accounts, one account, the watch list, the flags, and the settings that drive them
func TestIDEAdminHistoryEndpointsAndTheWatchList(t *testing.T) {
	s := ideTestServer(t)
	a := ideusage.Identity{Key: "home-h1", UID: "uid-h1", Signed: true}
	ideMeter.Add(a, ideusage.Terminal, 230*ideusage.MB) // over the limit today
	ideMeter.Add(ideusage.Identity{Key: "home-h2", UID: "uid-h2", Signed: true}, ideusage.Genie, 12*ideusage.MB)
	ideMeter.Add(ideusage.Identity{Key: "guest-h", Guest: true}, ideusage.Files, 4*ideusage.MB)
	get := func(q string, v interface{}) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleAdminIDEUsage(rec, httptest.NewRequest("GET", "/admin/ide-usage?"+q, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", q, rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatal(err)
		}
	}
	var rep struct {
		Period        int                `json:"period"`
		Days          []ideusage.DayRow  `json:"days"`
		Accounts      []ideusage.AcctRow `json:"accounts"`
		AccountsTotal int                `json:"accountsTotal"`
		Guests        int64              `json:"guests"`
		Flags         []ideusage.Flag    `json:"flags"`
		Watch         []ideWatched       `json:"watch"`
		Thresholds    ideThresholds      `json:"thresholds"`
		On            bool               `json:"on"`
	}
	get("report=30", &rep)
	if rep.Period != 30 || len(rep.Days) != 30 || rep.AccountsTotal < 2 || !rep.On || rep.Guests < 4*ideusage.MB {
		t.Fatalf("report: period %d days %d accounts %d on %v guests %d", rep.Period, len(rep.Days), rep.AccountsTotal, rep.On, rep.Guests)
	}
	if rep.Thresholds.LimitDays != 3 || rep.Thresholds.Jump != 5 || rep.Thresholds.NearPct != 90 || rep.Thresholds.Stops != 5 || rep.Thresholds.HistoryDays != 90 {
		t.Fatalf("thresholds: %+v", rep.Thresholds)
	}
	var one struct {
		Known   bool              `json:"known"`
		Watched bool              `json:"watched"`
		Account ideusage.AcctView `json:"account"`
		Limit   int64             `json:"limit"`
	}
	get("acct=uid-h1&days=14", &one)
	if !one.Known || len(one.Account.Days) != 14 || one.Account.Today != 230*ideusage.MB || one.Limit != 200*ideusage.MB || one.Watched {
		t.Fatalf("one account: %+v", one)
	}
	// the watch list needs the dashboard's header, and is kept in the settings
	post := func(q string, withHeader bool) int {
		req := httptest.NewRequest("POST", "/admin/ide-usage?"+q, nil)
		if withHeader {
			req.Header.Set(adminHeader, adminHeaderValue)
		}
		rec := httptest.NewRecorder()
		s.handleAdminIDEUsage(rec, req)
		return rec.Code
	}
	if c := post("watch=1&uid=uid-h2", false); c != http.StatusForbidden {
		t.Fatalf("a watch without the header: %d", c)
	}
	if c := post("watch=1&uid=uid-h2", true); c != 200 {
		t.Fatalf("watch: %d", c)
	}
	if w := GetSiteSettings().IDEWatch; len(w) != 1 || w[0] != "uid-h2" {
		t.Fatalf("watch list: %v", w)
	}
	get("report=7", &rep)
	if len(rep.Watch) != 1 || rep.Watch[0].UID != "uid-h2" {
		t.Fatalf("watch in the report: %+v", rep.Watch)
	}
	found := map[string]string{}
	for _, f := range rep.Flags {
		found[f.UID] = f.Kind
	}
	if found["uid-h1"] != "limit" && found["uid-h1"] != "near" { // 230 MB today: over the limit, but only one day
		// one day at the limit is not a run of 3: the account is not flagged "limit"; near does not apply over the limit
		if _, ok := found["uid-h1"]; ok {
			t.Fatalf("flags: %+v", rep.Flags)
		}
	}
	if found["uid-h2"] != "watched" {
		t.Fatalf("the watched account is not in the flags: %+v", rep.Flags)
	}
	if c := post("watch=0&uid=uid-h2", true); c != 200 || len(GetSiteSettings().IDEWatch) != 0 {
		t.Fatalf("unwatch: %d %v", c, GetSiteSettings().IDEWatch)
	}
	// saving the settings form leaves the watch list alone
	post("watch=1&uid=uid-h3", true)
	if w := GetSiteSettings().IDEWatch; len(w) != 1 || w[0] != "uid-h3" {
		t.Fatalf("watch list before the form: %v", w)
	}
}

func TestIDEHistorySettingsAreCheckedAndFollowTheMeter(t *testing.T) {
	_ = ideTestServer(t)
	for _, bad := range []SiteSettings{{IDEHistoryDays: 10}, {IDEHistoryDays: 400}, {IDEFlagLimitDays: 31}, {IDEFlagJump: 1}, {IDEFlagNearPct: 40}, {IDEFlagNearPct: 100}, {IDEFlagStops: 1001}} {
		if err := SaveSiteSettings(bad, -1); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if err := SaveSiteSettings(SiteSettings{IDEHistoryDays: 365, IDEFlagLimitDays: 2, IDEFlagJump: 3, IDEFlagNearPct: 80, IDEFlagStops: 9, IDEFlagLog: true}, -1); err != nil {
		t.Fatal(err)
	}
	c := ideusage.ConfigFunc()
	if c.HistoryDays != 365 || c.FlagLimitDays != 2 || c.FlagJump != 3 || c.FlagNearPct != 80 || c.FlagStops != 9 || !c.FlagLog {
		t.Fatalf("the meter did not follow: %+v", c)
	}
	// empty settings are the built-in numbers
	if err := SaveSiteSettings(SiteSettings{}, -1); err != nil {
		t.Fatal(err)
	}
	if c := ideusage.ConfigFunc(); c.HistoryDays != 90 || c.FlagLimitDays != 3 || c.FlagJump != 5 || c.FlagNearPct != 90 || c.FlagStops != 5 || c.FlagLog {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestTheSettingsFormLeavesTheIDEWatchListAlone(t *testing.T) {
	_, h := adminTestServer(t)
	if err := setIDEWatch("uid-w", true); err != nil {
		t.Fatal(err)
	}
	w := post(h, "/admin/settings", `{"ideFlagJump":8,"ideWatch":["someone-else"]}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	s := GetSiteSettings()
	if s.IDEFlagJump != 8 || len(s.IDEWatch) != 1 || s.IDEWatch[0] != "uid-w" {
		t.Fatalf("the form changed the watch list: jump %d, watch %v", s.IDEFlagJump, s.IDEWatch)
	}
	// at most 50 are watched
	for i := 0; i < 60; i++ {
		setIDEWatch(fmt.Sprintf("uid-%02d", i), true)
	}
	if n := len(GetSiteSettings().IDEWatch); n != 50 {
		t.Fatalf("the watch list holds %d", n)
	}
}
