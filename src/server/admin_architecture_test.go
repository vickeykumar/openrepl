package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meters"
)

func TestTheMapShowsEveryPartAndLine(t *testing.T) {
	_, h := adminTestServer(t)
	var r archReply
	decode(t, get(h, "/admin/architecture"), &r)
	if r.At == 0 {
		t.Error("no time")
	}
	have := map[string]archNode{}
	for _, n := range r.Nodes {
		have[n.ID] = n
		switch n.Status {
		case archOK, archWarn, archFail, archOff:
		default:
			t.Errorf("%s has the status %q", n.ID, n.Status)
		}
		if n.Text == "" {
			t.Errorf("%s says nothing", n.ID)
		}
	}
	for _, id := range []string{"genie", "terminals", "page", "http", "ws", "chat", "procs", "files", "sandbox", "repls",
		"data", "store", "external", "fb-db", "fb-auth", "openai", "openrouter"} {
		if _, ok := have[id]; !ok {
			t.Errorf("no part %q on the map", id)
		}
	}
	lines := map[string]string{}
	for _, l := range r.Links {
		lines[l.ID] = l.Level
	}
	for _, id := range []string{"page-http", "term-ws", "genie-chat", "ws-exec", "data-persist", "persist-store", "chat-llm", "browser-firebase"} {
		if _, ok := lines[id]; !ok {
			t.Errorf("no line %q on the map", id)
		}
	}
	if lines["browser-firebase"] != archState {
		t.Errorf("the Firebase line shows traffic it cannot see: %q", lines["browser-firebase"])
	}
	// no MongoDB or Firestore is set up in a test, so that box is off and dashed
	if have["external"].Status != archOff {
		t.Errorf("the optional database: %+v", have["external"])
	}
}

func TestOnlyAdminsGetTheMap(t *testing.T) {
	_, h := adminTestServer(t)
	if w := (adminCall{method: "GET", path: "/admin/architecture"}).do(h); w.Code != http.StatusUnauthorized {
		t.Errorf("a visitor: %d", w.Code)
	}
	w := (adminCall{method: "POST", path: "/admin/architecture", admin: true, header: true}).do(h)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestADotTurnsAmberAndRedByTheRules(t *testing.T) {
	// a host whose requests all just failed is red
	for i := 0; i < 3; i++ {
		noteChat("rule-red", false, time.Now())
	}
	if st, _, errPct, _ := hostHealth("rule-red"); st != archFail || errPct != 100 {
		t.Errorf("all failed: %s, %.0f%%", st, errPct)
	}
	// a few failures among many answers are amber
	for i := 0; i < 3; i++ {
		noteChat("rule-amber", false, time.Now())
	}
	for i := 0; i < 10; i++ {
		noteChat("rule-amber", true, time.Now())
	}
	if st, _, errPct, _ := hostHealth("rule-amber"); st != archWarn || errPct < 20 || errPct > 25 {
		t.Errorf("23%% failed: %s, %.0f%%", st, errPct)
	}
	// answers that took over 4 s on average are amber, even with no failure
	for i := 0; i < 4; i++ {
		noteChat("rule-slow", true, time.Now().Add(-5*time.Second))
	}
	if st, _, _, avg := hostHealth("rule-slow"); st != archWarn || avg < 4500 {
		t.Errorf("slow: %s, %.0f ms", st, avg)
	}
	// good answers, a few of them, are fine; and too few requests prove nothing
	for i := 0; i < 8; i++ {
		noteChat("rule-fine", true, time.Now())
	}
	if st, _, _, _ := hostHealth("rule-fine"); st != archOK {
		t.Errorf("fine: %s", st)
	}
	noteChat("rule-few", false, time.Now().Add(-90*time.Second))
	if st, _, _, _ := hostHealth("rule-few"); st == archFail {
		t.Errorf("one failure is not an outage: %s", st)
	}
}

func TestLevelsAndTheWorseStatus(t *testing.T) {
	for rate, want := range map[float64]string{0: archNone, 0.5: archLow, 5.9: archLow, 6: archMid, 29: archMid, 30: archHigh, 500: archHigh} {
		if got := level(rate); got != want {
			t.Errorf("level(%v) = %s, want %s", rate, got, want)
		}
	}
	if worse(archOK, archWarn) != archWarn || worse(archFail, archWarn) != archFail || worse(archOff, archOK) != archOK || worse(archOK, archOff) != archOK {
		t.Error("the worse of two statuses")
	}
}

func TestRecentProblemsAreNewestFirstAndAFewOnly(t *testing.T) {
	archMu.Lock()
	archProblems = nil
	archMu.Unlock()
	for i := 0; i < 14; i++ {
		noteChat(providerOpenRouter, false, time.Now())
	}
	noteChat(providerOpenAI, true, time.Now()) // a good answer is not a problem
	got := recentProblems()
	if len(got) != 10 {
		t.Fatalf("%d problems kept", len(got))
	}
	if !strings.Contains(got[0].Text, "OpenRouter") || got[0].At < got[9].At {
		t.Errorf("not newest first: %+v", got)
	}
}

func TestTheMinutesAreSixBucketsEach(t *testing.T) {
	var s meters.Series
	s.Add(3)
	got := minutes(&s)
	if len(got) != 30 {
		t.Fatalf("%d minutes", len(got))
	}
	var sum int64
	for _, v := range got {
		sum += v
	}
	if sum != 3 || got[len(got)-1] == 0 && got[len(got)-2] == 0 {
		t.Errorf("minutes: %v", got)
	}
}

func TestTheProgramsOfTerminalsAreNamedForTheMap(t *testing.T) {
	if replLabel("") != "bash" || replLabel("gointerpreter") != "gomacro" || replLabel("python") != "python" {
		t.Error("labels")
	}
}

func TestTheSiteCountsItsRequestsButNotTheDashboardsPolling(t *testing.T) {
	server, _ := adminTestServer(t)
	count := func() (req, errs int64) {
		return meters.Get(meters.HTTPReq).Sum(300), meters.Get(meters.HTTPErr).Sum(300)
	}
	handler := server.wrapLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/boom") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	req0, err0 := count()
	for _, path := range []string{"/", "/about.html", "/boom"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/"+strings.TrimPrefix(server.admin.prefix+"admin/health", "/"), nil)) // the dashboard polling
	req1, err1 := count()
	if req1-req0 != 3 || err1-err0 != 1 {
		t.Errorf("requests +%d, errors +%d (want +3 and +1: the poll is not counted)", req1-req0, err1-err0)
	}
}
