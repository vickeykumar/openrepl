package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"persist"
)

func openSnippetTestStore(t *testing.T) {
	db, err := persist.Open(filepath.Join(t.TempDir(), "snippets.db"))
	if err != nil {
		t.Fatal(err)
	}
	snippet_db_handle = db
	t.Cleanup(func() { snippet_db_handle = nil; db.Close() })
}

func TestSnippetExpiryRange(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	day := func(s string) int64 { d, _ := time.Parse("2006-01-02", s); return endOfDay(d) }
	cases := []struct {
		name string
		in   string
		max  int
		want int64
		bad  bool
	}{
		{"the default is a month ahead", "", 30, day("2026-12-14"), false},
		{"the default is the most when that is shorter", "", 7, day("2026-11-21"), false},
		{"today is allowed", "2026-11-14", 30, day("2026-11-14"), false},
		{"a day in range", "2026-12-01", 30, day("2026-12-01"), false},
		{"the latest day", "2026-12-14", 30, day("2026-12-14"), false},
		{"one day past the latest", "2026-12-15", 30, 0, true},
		{"a day that has passed", "2026-11-13", 30, 0, true},
		{"not a date", "next week", 30, 0, true},
		{"a year with a longer most", "2027-11-14", 365, day("2027-11-14"), false},
	}
	for _, c := range cases {
		got, why := snippetExpiryFor(c.in, now, c.max)
		if (why != "") != c.bad || got != c.want {
			t.Errorf("%s: %d %q, want %d bad=%v", c.name, got, why, c.want, c.bad)
		}
	}
}

func TestSnippetsHaveAnEndAndAnEndedOneIsRefused(t *testing.T) {
	openSnippetTestStore(t)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/snippet", strings.NewReader(body))
		r.RemoteAddr = "10.9.8.7:1234"
		handleSnippet(w, r)
		return w
	}
	// no day chosen: a month from today
	w := post(`{"lang":"python","code":"print(1)"}`)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var made struct {
		ID      string
		Expires int64
	}
	json.Unmarshal(w.Body.Bytes(), &made)
	if want := endOfDay(time.Now().UTC().AddDate(0, 0, 30)); made.Expires != want {
		t.Errorf("default end %d, want %d", made.Expires, want)
	}
	// a day beyond the admin's most is refused with the reason
	far := time.Now().UTC().AddDate(0, 0, 31).Format("2006-01-02")
	if w := post(`{"lang":"python","code":"x","expires":"` + far + `"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "at most 30 days") {
		t.Errorf("past the most: %d %s", w.Code, w.Body.String())
	}
	// it opens while it works
	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handleSnippet(w, httptest.NewRequest("GET", "/snippet?id="+id, nil))
		return w
	}
	if w := get(made.ID); w.Code != 200 || !strings.Contains(w.Body.String(), "print(1)") {
		t.Errorf("open: %d %s", w.Code, w.Body.String())
	}
	// an ended one: 410, and no code
	old, _ := storeSnippet(snippet{Lang: "go", Code: "secret()", Created: 1, Expires: time.Now().Add(-time.Hour).Unix()})
	w = get(old)
	if w.Code != http.StatusGone || strings.Contains(w.Body.String(), "secret()") || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("ended: %d %s", w.Code, w.Body.String())
	}
	lw := httptest.NewRecorder()
	handleSnippetLink(lw, httptest.NewRequest("GET", "/s/"+old, nil))
	if lw.Code != http.StatusGone {
		t.Errorf("/s/<id> of an ended link: %d", lw.Code)
	}
}

func TestOldSnippetsGetAnEndOnceAndEndedOnesAreRemoved(t *testing.T) {
	openSnippetTestStore(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	nothing, _ := storeSnippet(snippet{Lang: "python", Code: "a", Created: 1})                                          // made before links had an end
	running, _ := storeSnippet(snippet{Lang: "python", Code: "b", Created: 2, Expires: now.Add(48 * time.Hour).Unix()}) // has one
	ended, _ := storeSnippet(snippet{Lang: "python", Code: "c", Created: 3, Expires: now.Add(-time.Hour).Unix()})

	n, err := stampOldSnippets(now, 30)
	if err != nil || n != 1 {
		t.Fatalf("stamped %d %v", n, err)
	}
	s, _ := fetchSnippet(nothing)
	if s.Expires != endOfDay(now.AddDate(0, 0, 30)) {
		t.Errorf("the old link's end %d", s.Expires)
	}
	if again, _ := stampOldSnippets(now, 30); again != 0 {
		t.Errorf("stamped again: %d", again)
	}
	if r, _ := fetchSnippet(running); r.Expires != now.Add(48*time.Hour).Unix() {
		t.Errorf("a link with an end was changed: %d", r.Expires)
	}
	// a shorter most gives the old ones the most
	other, _ := storeSnippet(snippet{Lang: "go", Code: "d", Created: 4})
	stampOldSnippets(now, 7)
	if o, _ := fetchSnippet(other); o.Expires != endOfDay(now.AddDate(0, 0, 7)) {
		t.Errorf("with a most of 7 days: %d", o.Expires)
	}

	looked, removed, err := removeExpiredSnippets(now)
	if err != nil || removed != 1 || looked != 4 {
		t.Fatalf("cleanup: looked %d removed %d %v", looked, removed, err)
	}
	if _, ok := fetchSnippet(ended); ok {
		t.Error("the ended link is still stored")
	}
	for _, keep := range []string{nothing, running, other} {
		if _, ok := fetchSnippet(keep); !ok {
			t.Errorf("%s was removed", keep)
		}
	}
	res := runSnippetCleanup(now.AddDate(0, 0, 40), 30)
	if res.Removed != 3 {
		t.Errorf("forty days later: %+v", res)
	}
}

func TestSnippetMaxDaysSetting(t *testing.T) {
	var s SiteSettings
	if s.snippetMaxDays() != 30 {
		t.Errorf("default %d", s.snippetMaxDays())
	}
	s.SnippetMaxDays = 90
	if s.snippetMaxDays() != 90 || s.public().SnippetMaxDays != 90 {
		t.Errorf("set %d %d", s.snippetMaxDays(), s.public().SnippetMaxDays)
	}
	for _, bad := range []int{-1, 366} {
		if _, err := (SiteSettings{SnippetMaxDays: bad}).normalize(); err == nil {
			t.Errorf("%d days was accepted", bad)
		}
	}
	if _, err := (SiteSettings{SnippetMaxDays: 365}).normalize(); err != nil {
		t.Errorf("365 days was refused: %v", err)
	}
}
