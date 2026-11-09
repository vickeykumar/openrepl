package server

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Time on site: what the beats add up to, and what the dashboard reads.

// usageTest points the usage file at a temporary directory and gives back a
// store whose clock the test moves.
func usageTest(t *testing.T) (*usageStore, *time.Time) {
	t.Helper()
	old, oldSite := usageFile, siteUsage
	usageFile = filepath.Join(t.TempDir(), "usage.json")
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	s := &usageStore{now: func() time.Time { return now }}
	siteUsage = s
	t.Cleanup(func() { usageFile, siteUsage = old, oldSite })
	return s, &now
}

func TestABeatOpensOneVisitAndAddsActiveTime(t *testing.T) {
	s, now := usageTest(t)
	who := beatWho{uid: "u1"}
	b := Beat{Visit: "visit-aaaa1111", Lang: "python", Device: "desktop", Ref: "www.google.com"}
	if !s.beat(who, b, "IN", "openrepl.com") {
		t.Fatal("the first beat was refused")
	}
	*now = now.Add(30 * time.Second)
	b.Secs = 30
	s.beat(who, b, "IN", "openrepl.com")
	*now = now.Add(30 * time.Second)
	s.beat(who, b, "IN", "openrepl.com")

	u := s.users["u1"]
	if u.Visits != 1 || u.Secs != 60 || u.Langs["python"] != 60 || u.Days["2026-11-09"] != 60 {
		t.Fatalf("user = %+v", u)
	}
	if len(u.Recent) != 1 || u.Recent[0].Secs != 60 || u.Recent[0].Source != "Google" || u.Recent[0].Country != "IN" || u.Recent[0].Device != "desktop" {
		t.Errorf("visit = %+v", u.Recent[0])
	}
	d := s.days["2026-11-09"]
	if d.SignedVisitors != 1 || d.SignedVisits != 1 || d.SignedSecs != 60 || d.Langs["python"] != 60 || d.Sources["Google"] != 1 || d.Countries["IN"] != 1 || d.Devices["desktop"] != 1 {
		t.Errorf("day = %+v", d)
	}
}

func TestABeatCannotClaimMoreTimeThanHasPassed(t *testing.T) {
	s, now := usageTest(t)
	who := beatWho{uid: "u1"}
	b := Beat{Visit: "visit-aaaa1111", Secs: 65}
	s.beat(who, b, "", "")
	*now = now.Add(2 * time.Second) // too soon after the last beat: adds nothing
	s.beat(who, b, "", "")
	*now = now.Add(20 * time.Second) // 20 s have passed, so at most 25 can be claimed
	s.beat(who, b, "", "")
	if got := s.users["u1"].Secs; got > 70+25 {
		t.Errorf("claimed %d seconds in 22 s", got)
	}
	// and one beat is never more than 65 seconds
	b.Visit = "visit-bbbb2222"
	*now = now.Add(time.Hour)
	s.beat(who, Beat{Visit: b.Visit, Secs: 100000}, "", "")
	if got := s.users["u1"].Recent[len(s.users["u1"].Recent)-1].Secs; got > usageMaxBeatSecs {
		t.Errorf("one beat added %d seconds", got)
	}
}

func TestGuestsAreCountedButNeverListed(t *testing.T) {
	s, now := usageTest(t)
	g := beatWho{guest: "hash-of-a-guest"}
	s.beat(g, Beat{Visit: "visit-aaaa1111", Lang: "go"}, "DE", "")
	*now = now.Add(30 * time.Second)
	s.beat(g, Beat{Visit: "visit-aaaa1111", Lang: "go", Secs: 30}, "DE", "")
	*now = now.Add(2 * time.Hour) // a second visit of the same guest, the same day
	s.beat(g, Beat{Visit: "visit-bbbb2222"}, "DE", "")
	s.beat(beatWho{guest: "another"}, Beat{Visit: "visit-cccc3333"}, "FR", "")

	if len(s.users) != 0 {
		t.Fatalf("a guest has a record: %+v", s.users)
	}
	d := s.days["2026-11-09"]
	if d.GuestVisitors != 2 || d.GuestVisits != 3 || d.GuestSecs != 30 || d.Langs["go"] != 30 || d.Countries["DE"] != 2 {
		t.Errorf("day = %+v", d)
	}
	data, _ := json.Marshal(usageFileData{Days: s.days, Users: s.users})
	if strings.Contains(string(data), "hash-of-a-guest") && !strings.Contains(string(data), `"seen"`) {
		t.Errorf("a guest's hash is kept outside the day's seen list")
	}
}

func TestGuestJourney(t *testing.T) {
	s, _ := usageTest(t)
	g := beatWho{guest: "g1"}
	s.beat(g, Beat{Visit: "visit-aaaa1111"}, "", "")
	s.noteTerminal(g)
	s.noteTerminal(g) // the same guest again: once
	s.noteGenie(g)
	s.beat(beatWho{guest: "g2"}, Beat{Visit: "visit-bbbb2222"}, "", "")
	s.noteTerminal(beatWho{uid: "u1"}) // an account's terminal is on its own record
	s.noteGenie(beatWho{uid: "u1"})    // and its Genie use in the Genie ledger

	o := s.overview(7, []int64{time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC).UnixNano() / 1e6})
	want := []int{2, 1, 1, 1}
	for i, f := range o.Funnel {
		if f.Count != want[i] {
			t.Errorf("funnel %d (%s) = %d, want %d", i, f.Label, f.Count, want[i])
		}
	}
	if s.users["u1"].Terminals != 1 {
		t.Errorf("terminals = %d", s.users["u1"].Terminals)
	}
}

func TestOneVisitorCannotFloodTheStore(t *testing.T) {
	s, _ := usageTest(t)
	who := beatWho{uid: "u1"}
	refused := 0
	for i := 0; i < 30; i++ {
		if !s.beat(who, Beat{Visit: "visit-" + strings.Repeat("a", 8) + string(rune('a'+i%26))}, "", "") {
			refused++
		}
	}
	if refused != 30-usageBeatsPerMin {
		t.Errorf("refused %d of 30", refused)
	}
	if s.beat(who, Beat{Visit: "no"}, "", "") {
		t.Error("a bad visit id was accepted")
	}
	if s.beat(beatWho{}, Beat{Visit: "visit-aaaa1111"}, "", "") {
		t.Error("a beat from nobody was accepted")
	}
}

func TestAVisitIdOfSomebodyElseStartsAVisitOfItsOwn(t *testing.T) {
	s, _ := usageTest(t)
	s.beat(beatWho{uid: "u1"}, Beat{Visit: "visit-aaaa1111"}, "", "")
	s.beat(beatWho{uid: "u2"}, Beat{Visit: "visit-aaaa1111"}, "", "")
	if s.users["u1"].Visits != 1 || s.users["u2"].Visits != 1 {
		t.Errorf("visits: %d and %d", s.users["u1"].Visits, s.users["u2"].Visits)
	}
}

func TestWhereAVisitorCameFrom(t *testing.T) {
	for ref, want := range map[string]string{
		"":                       "Direct",
		"openrepl.com":           "Direct",
		"www.openrepl.com":       "Direct",
		"www.google.com":         "Google",
		"www.google.co.in":       "Google",
		"duckduckgo.com":         "DuckDuckGo",
		"bing.com":               "Bing",
		"github.com":             "GitHub",
		"gist.github.com":        "GitHub",
		"news.ycombinator.com":   "Hacker News",
		"old.reddit.com":         "Reddit",
		"blog.example.org":       "site: blog.example.org",
		"not a host <script>":    "Direct",
		"localhost:8080":         "Direct", // not a valid host name: counted as direct
		"evilgoogle.example.com": "site: evilgoogle.example.com",
	} {
		own := "openrepl.com"
		if got := sourceOf(ref, own); got != want {
			t.Errorf("sourceOf(%q) = %q, want %q", ref, got, want)
		}
	}
	if got := sourceOf("localhost", "localhost:8080"); got != "Direct" {
		t.Errorf("own host with a port = %q", got)
	}
}

func TestCountryComesFromCloudflaresHeaderOnly(t *testing.T) {
	for header, want := range map[string]string{"IN": "IN", "de": "DE", "": "unknown", "XX": "unknown", "T1": "unknown", "India": "unknown", "1A": "unknown"} {
		r := httptest.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("CF-IPCountry", header)
		}
		if got := countryOf(r); got != want {
			t.Errorf("CF-IPCountry %q -> %q, want %q", header, got, want)
		}
	}
}

func TestUsageSurvivesARestart(t *testing.T) {
	s, now := usageTest(t)
	who := beatWho{uid: "u1"}
	s.beat(who, Beat{Visit: "visit-aaaa1111", Lang: "rust"}, "IN", "")
	*now = now.Add(30 * time.Second)
	s.beat(who, Beat{Visit: "visit-aaaa1111", Lang: "rust", Secs: 30}, "IN", "")
	s.beat(beatWho{guest: "g1"}, Beat{Visit: "visit-bbbb2222"}, "IN", "")
	s.flush()

	again := &usageStore{now: s.now}
	d, ok := again.userDetail("u1")
	if !ok || d.ActiveSecs != 30 || d.Visits != 1 || len(d.Languages) != 1 || d.Languages[0].Name != "rust" {
		t.Errorf("after a restart: %+v", d)
	}
	if again.days["2026-11-09"].GuestVisitors != 1 {
		t.Errorf("guests after a restart: %+v", again.days["2026-11-09"])
	}
	// the guest is still known for today, so another visit is not another visitor
	again.beat(beatWho{guest: "g1"}, Beat{Visit: "visit-cccc3333"}, "IN", "")
	if again.days["2026-11-09"].GuestVisitors != 1 {
		t.Errorf("the same guest was counted twice after a restart")
	}
	// but no guest hash outlives its day
	data, _ := ioutil.ReadFile(usageFile)
	tomorrow := &usageStore{now: func() time.Time { return now.AddDate(0, 0, 1) }}
	tomorrow.load()
	tomorrow.save(tomorrow.clock())
	data, _ = ioutil.ReadFile(usageFile)
	if strings.Contains(string(data), `"v:`) {
		t.Errorf("a guest's hash is still kept the next day: %s", data)
	}
}

func TestSummaryOfAUsersLast30Days(t *testing.T) {
	s, now := usageTest(t)
	s.load()
	s.users["u1"] = &userUsage{Secs: 5400, Visits: 3, Terminals: 7,
		Days:   map[string]int{"2026-11-09": 600, "2026-11-08": 3600, "2026-10-01": 1200},
		Langs:  map[string]int64{"python": 4000, "go": 1400},
		Recent: []*visitRec{{Start: 1, Secs: 100}, {Start: 2, Secs: 520}}}
	got := s.users30()["u1"]
	if got.ActiveSecs != 5400 || got.Visits != 3 || got.Terminals != 7 || got.TopLang != "python" || got.LastVisitSecs != 520 {
		t.Errorf("summary = %+v", got)
	}
	if len(got.Bars) != 30 || got.Bars[29] != 10 || got.Bars[28] != 60 || got.Days30 != 2 {
		t.Errorf("bars = %v, days %d (1 Oct is %v ago)", got.Bars, got.Days30, now.Sub(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	}
	d, ok := s.userDetail("u1")
	if !ok || len(d.Recent) != 2 || d.Recent[0].Secs != 520 || d.AvgVisit != 1800 || d.Languages[0].Share != 74 {
		t.Errorf("detail = %+v", d)
	}
	if _, ok := s.userDetail("nobody"); ok {
		t.Error("an unknown user has details")
	}
}

func TestReturningAccounts(t *testing.T) {
	s, _ := usageTest(t)
	s.load()
	s.since = "2026-10-01"
	day := func(d string) int64 {
		tt, _ := time.Parse("2006-01-02", d)
		return tt.UnixNano() / 1e6
	}
	// three accounts joined on Mon 12 Oct: one came back the next day, one on day 5, one never
	s.users["a"] = &userUsage{Days: map[string]int{"2026-10-13": 60}}
	s.users["b"] = &userUsage{Days: map[string]int{"2026-10-17": 60}}
	s.users["c"] = &userUsage{Days: map[string]int{"2026-10-12": 60}}
	// one joined on 2 Nov: too recent for the 30-day figure
	s.users["d"] = &userUsage{Days: map[string]int{"2026-11-03": 60}}
	// one joined before tracking began: no figure
	joins := []Join{{"a", day("2026-10-12")}, {"b", day("2026-10-12")}, {"c", day("2026-10-12")}, {"d", day("2026-11-02")}, {"old", day("2026-09-01")}}
	cohorts, back7 := s.cohorts(joins, 4)
	if len(cohorts) != 3 {
		t.Fatalf("cohorts = %+v", cohorts)
	}
	oct12 := cohorts[1]
	if oct12.Week != "2026-10-12" || oct12.Size != 3 || oct12.D1 != 33 || oct12.D7 != 66 || oct12.D30 != -1 { // 30 days after 12 Oct is still ahead
		t.Errorf("week of 12 Oct = %+v", oct12)
	}
	nov2 := cohorts[0]
	if nov2.Week != "2026-11-02" || nov2.D1 != 100 || nov2.D7 != 100 || nov2.D30 != -1 {
		t.Errorf("week of 2 Nov = %+v", nov2)
	}
	if sep := cohorts[2]; sep.Week != "2026-08-31" || sep.Size != 1 || sep.D1 != -1 {
		t.Errorf("a week before tracking = %+v", sep)
	}
	if back7 != 75 {
		t.Errorf("back after 7 days = %d, want 75", back7)
	}
}

func TestBeatEndpoint(t *testing.T) {
	_, h := adminTestServer(t)
	s, _ := usageTest(t)
	body := `{"vid":"visit-aaaa1111","secs":0,"lang":"python","device":"phone","ref":"www.bing.com"}`
	call := func(method, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://openrepl.test/usage/beat", strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("CF-IPCountry", "IN")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "", body); w.Code != http.StatusNoContent {
		t.Fatalf("a beat: %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "http://openrepl.test", body); w.Code != http.StatusNoContent {
		t.Errorf("a beat from the site's own page: %d", w.Code)
	}
	if w := call("POST", "https://elsewhere.example", body); w.Code != http.StatusForbidden {
		t.Errorf("a beat from another site: %d", w.Code)
	}
	if w := call("GET", "", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("a GET: %d", w.Code)
	}
	if w := call("POST", "", `not json`); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", w.Code)
	}
	d := s.days[time.Now().UTC().Format("2006-01-02")]
	if d == nil || d.GuestVisits != 1 || d.Devices["phone"] != 1 || d.Countries["IN"] != 1 || d.Sources["Bing"] != 1 {
		t.Errorf("today = %+v", d)
	}
}

func TestSiteUsageAndUserUsageAreForAdmins(t *testing.T) {
	_, h := adminTestServer(t)
	usageTest(t)
	if w := (adminCall{method: "GET", path: "/admin/usage"}).do(h); w.Code != http.StatusUnauthorized {
		t.Errorf("not an admin: %d", w.Code)
	}
	w := get(h, "/admin/usage?days=7")
	if w.Code != http.StatusOK {
		t.Fatalf("admin/usage: %d %s", w.Code, w.Body.String())
	}
	var o SiteUsage
	decode(t, w, &o)
	if o.Range != 7 || len(o.Days) != 7 || len(o.Funnel) != 4 || o.Back7Pct != -1 {
		t.Errorf("overview = %+v", o)
	}
	if w := get(h, "/admin/usage?days=5"); w.Code != http.StatusOK {
		t.Errorf("odd range: %d", w.Code)
	} else {
		decode(t, w, &o)
		if o.Range != 30 {
			t.Errorf("an odd range should fall back to 30, got %d", o.Range)
		}
	}
	if w := get(h, "/admin/users/not-a-real-uid-123456/usage"); w.Code != http.StatusNotFound {
		t.Errorf("an unknown account: %d", w.Code)
	}
}
