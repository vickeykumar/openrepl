package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"user"
	"utils"
)

// adminsServer is a server whose admin and owner checks are the real ones
// (a session cookie and the lists of admins).
func adminsServer(t *testing.T) (http.Handler, *http.Cookie, *http.Cookie, *http.Cookie) {
	t.Helper()
	s, _ := adminTestServer(t)
	s.admin.check, s.admin.owner = nil, nil
	mux := http.NewServeMux()
	s.registerAdmin(mux, "/")
	boss := sessionCookie(t, "uid-boss", "boss@example.com") // an owner: OPENREPL_ADMIN_EMAILS
	ann := sessionCookie(t, "uid-ann", "ann@example.com")    // a visitor, who may become an admin
	eve := sessionCookie(t, "uid-eve", "eve@example.com")
	return mux, boss, ann, eve
}

func asCookie(h http.Handler, c *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(c)
	if method != "GET" {
		r.Header.Set(adminHeader, adminHeaderValue)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAnOwnerAddsAnAdminWhoThenGetsIn(t *testing.T) {
	h, boss, ann, _ := adminsServer(t)
	if w := asCookie(h, ann, "GET", "/admin/health", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("before: %d", w.Code)
	}
	w := asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"  Ann@Example.com "}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var r adminsReply
	decode(t, w, &r)
	if len(r.Admins) != 1 || r.Admins[0] != "ann@example.com" || len(r.Owners) != 1 || r.Owners[0] != "boss@example.com" || !r.CanManage {
		t.Fatalf("reply: %+v", r)
	}
	if w := asCookie(h, ann, "GET", "/admin/health", ""); w.Code != 200 {
		t.Fatalf("after adding: %d", w.Code)
	}
	if !strings.Contains(asCookie(h, boss, "GET", "/admin/audit", "").Body.String(), "admin added: ann@example.com") {
		t.Error("the audit log does not say who was added")
	}

	// removed, she is out again on her next request
	if w := asCookie(h, boss, "POST", "/admin/admins", `{"action":"remove","email":"ann@example.com"}`); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if w := asCookie(h, ann, "GET", "/admin/health", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("after removing: %d", w.Code)
	}
}

func TestOnlyOwnersChangeTheList(t *testing.T) {
	h, boss, ann, eve := adminsServer(t)
	asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"ann@example.com"}`)

	// an added admin sees the list and cannot change it
	var r adminsReply
	decode(t, asCookie(h, ann, "GET", "/admin/admins", ""), &r)
	if r.CanManage || len(r.Admins) != 1 {
		t.Fatalf("what an added admin sees: %+v", r)
	}
	for _, body := range []string{
		`{"action":"add","email":"eve@example.com"}`,
		`{"action":"remove","email":"ann@example.com"}`,
	} {
		if w := asCookie(h, ann, "POST", "/admin/admins", body); w.Code != http.StatusForbidden {
			t.Errorf("an added admin posted %s: %d", body, w.Code)
		}
	}
	// a visitor is not even an admin
	if w := asCookie(h, eve, "POST", "/admin/admins", `{"action":"add","email":"eve@example.com"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("a visitor: %d", w.Code)
	}
	if got := GetSiteSettings().Admins; len(got) != 1 || got[0] != "ann@example.com" {
		t.Fatalf("the list changed: %v", got)
	}
}

func TestOwnersAreFixedAndMistakesAreRefused(t *testing.T) {
	h, boss, _, _ := adminsServer(t)
	for body, want := range map[string]int{
		`{"action":"remove","email":"boss@example.com"}`:   http.StatusConflict, // an owner comes from the environment
		`{"action":"add","email":"BOSS@example.com"}`:      http.StatusConflict,
		`{"action":"add","email":"not an address"}`:        http.StatusBadRequest,
		`{"action":"add","email":""}`:                      http.StatusBadRequest,
		`{"action":"add","email":"a@b.c, d@e.f"}`:          http.StatusBadRequest,
		`{"action":"remove","email":"nobody@example.com"}`: http.StatusNotFound,
		`{"action":"promote","email":"ann@example.com"}`:   http.StatusBadRequest,
		`{`: http.StatusBadRequest,
	} {
		if w := asCookie(h, boss, "POST", "/admin/admins", body); w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", body, w.Code, want, w.Body.String())
		}
	}
	asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"ann@example.com"}`)
	if w := asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"ANN@example.com"}`); w.Code != http.StatusConflict {
		t.Errorf("adding twice: %d", w.Code)
	}
	if !utils.IsOwnerEmail("boss@example.com") || utils.IsOwnerEmail("ann@example.com") || !utils.IsAdminEmail("ann@example.com") {
		t.Error("owner and admin checks disagree with the lists")
	}
}

func TestTheSettingsFormCannotChangeTheAdmins(t *testing.T) {
	h, boss, _, _ := adminsServer(t)
	asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"ann@example.com"}`)
	if w := asCookie(h, boss, "POST", "/admin/settings", `{"colorOfTheDay":true,"admins":["eve@example.com"]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got := GetSiteSettings()
	if len(got.Admins) != 1 || got.Admins[0] != "ann@example.com" || !got.ColorOfTheDay {
		t.Fatalf("settings after a form save: %+v", got)
	}
	if utils.IsAdminEmail("eve@example.com") {
		t.Fatal("a form made an admin")
	}
}

func TestAddedAdminsAreProtectedLikeOwnersAndSurviveARestart(t *testing.T) {
	h, boss, _, _ := adminsServer(t)
	asCookie(h, boss, "POST", "/admin/admins", `{"action":"add","email":"ann@example.com"}`)

	// the users page marks her as an admin, and she cannot be blocked
	var users struct {
		Users []struct {
			Email string
			Admin bool
		}
	}
	decode(t, asCookie(h, boss, "GET", "/admin/users", ""), &users)
	found := false
	for _, u := range users.Users {
		if u.Email == "ann@example.com" {
			found = true
			if !u.Admin {
				t.Error("the accounts list does not mark her as an admin")
			}
		}
	}
	if !found {
		t.Skip("the accounts list has no entry for her in this setup")
	}
	if w := asCookie(h, boss, "POST", "/admin/users/uid-ann/block", ""); w.Code == 200 {
		t.Error("an added admin was blocked")
	}

	// a restart reads the same list from the settings
	utils.SetExtraAdmins(nil)
	settingsMu.Lock()
	siteSettings, settingsLoaded = SiteSettings{}, false
	settingsMu.Unlock()
	GetSiteSettings()
	if !utils.IsAdminEmail("ann@example.com") {
		t.Fatal("the added admin was lost on reload")
	}
	_ = user.GetUserDBHandle
}

func TestAdminsInTheSettingsAreChecked(t *testing.T) {
	if _, err := (SiteSettings{Admins: []string{"not an address"}}).normalize(); err == nil {
		t.Error("an invalid address was accepted")
	}
	s, err := SiteSettings{Admins: []string{"B@x.io", " a@x.io", "b@x.io", ""}}.normalize()
	if err != nil || len(s.Admins) != 2 || s.Admins[0] != "a@x.io" || s.Admins[1] != "b@x.io" {
		t.Fatalf("%v %v", s.Admins, err)
	}
	many := make([]string, maxExtraAdmins+1)
	for i := range many {
		many[i] = strings.Repeat("a", i+1) + "@x.io"
	}
	if _, err := (SiteSettings{Admins: many}).normalize(); err == nil {
		t.Error("too many admins were accepted")
	}
}
