package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meters"
	"persist"
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

func TestMongoDBShowsUsedOfTotalFromTheSettingOrTheDisk(t *testing.T) {
	mongo := persist.Stats{Backend: "mongodb", DataBytes: 400 << 20, IndexBytes: 12 << 20, StorageBytes: 430 << 20}
	// the admin says the cluster has 512 MB: 412 of 512, 80%, amber
	n := externalDBNode(mongo, 512, archOK)
	if n.Status != archWarn || n.Text != "MongoDB: 412 MB of 512 MB (80%)" {
		t.Errorf("with a capacity: %s %q", n.Status, n.Text)
	}
	// plenty of room is fine, and 95% is red
	if n := externalDBNode(mongo, 4096, archOK); n.Status != archOK || !strings.Contains(n.Text, "of 4.0 GB (10%)") {
		t.Errorf("roomy: %s %q", n.Status, n.Text)
	}
	if n := externalDBNode(mongo, 420, archOK); n.Status != archFail {
		t.Errorf("98%% full: %s %q", n.Status, n.Text)
	}
	// with no setting the disk MongoDB reports is the total
	disk := mongo
	disk.DiskUsed, disk.DiskTotal = 18<<30, 20<<30
	if n := externalDBNode(disk, 0, archOK); n.Status != archWarn || !strings.Contains(n.Text, "18.0 GB of 20.0 GB (90%)") {
		t.Errorf("disk: %s %q", n.Status, n.Text)
	}
	// with neither, only what is used can be told, and it says what is missing
	n = externalDBNode(mongo, 0, archOK)
	if n.Status != archOK || n.Text != "MongoDB: 412 MB used" || !strings.Contains(n.Rows[len(n.Rows)-1].Value, "Site settings") {
		t.Errorf("no capacity: %s %q %+v", n.Status, n.Text, n.Rows)
	}
	// errors in the operations keep the box amber whatever the size
	if n := externalDBNode(mongo, 4096, archWarn); n.Status != archWarn {
		t.Errorf("base status lost: %s", n.Status)
	}
	// a database that cannot be asked its size says so and is amber
	bad := persist.Stats{Backend: "mongodb", Err: errors.New("timeout")}
	if n := externalDBNode(bad, 512, archOK); n.Status != archWarn || !strings.Contains(n.Text, "cannot read its size") {
		t.Errorf("unreadable: %s %q", n.Status, n.Text)
	}
}

func TestFirestoreShowsTodaysOperationsAgainstTheFreeAllowance(t *testing.T) {
	if n := externalDBNode(persist.Stats{Backend: "firestore"}, 0, archOK); n.Status != archOK || n.Text != "Firestore: no operations today" {
		t.Errorf("quiet: %s %q", n.Status, n.Text)
	}
	busy := persist.Stats{Backend: "firestore", Reads: 1200, Writes: 16000, Deletes: 3}
	n := externalDBNode(busy, 0, archOK)
	if n.Status != archWarn || n.Text != "Firestore: writes today 16,000 of 20,000" {
		t.Errorf("80%% of the writes: %s %q", n.Status, n.Text)
	}
	if len(n.Rows) < 4 || n.Rows[0].Value != "1,200 of 50,000 (2%)" || n.Rows[1].Status != archWarn {
		t.Errorf("rows: %+v", n.Rows)
	}
	if n := externalDBNode(persist.Stats{Backend: "firestore", Reads: 49000}, 0, archOK); n.Status != archFail {
		t.Errorf("98%% of the reads: %s %q", n.Status, n.Text)
	}
	if thousands(1234567) != "1,234,567" || thousands(12) != "12" || size(3<<30) != "3.0 GB" {
		t.Error("number words")
	}
}

func TestTheDatabaseCapacityIsChecked(t *testing.T) {
	for _, mb := range []int{5, -1, 1 << 30} {
		if _, err := (SiteSettings{DBCapacityMB: mb}).normalize(); err == nil {
			t.Errorf("%d MB was accepted", mb)
		}
	}
	if s, err := (SiteSettings{DBCapacityMB: 512}).normalize(); err != nil || s.DBCapacityMB != 512 {
		t.Errorf("512 MB: %+v %v", s.DBCapacityMB, err)
	}
	if _, err := (SiteSettings{}).normalize(); err != nil {
		t.Errorf("not set: %v", err)
	}
}
