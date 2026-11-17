package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cookie"
	"user"
)

func loginAnswer(t *testing.T, c *http.Cookie) map[string]interface{} {
	t.Helper()
	r := httptest.NewRequest("GET", "/login", nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleLoginSession(w, r)
	out := map[string]interface{}{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("not json: %q %v", w.Body.String(), err)
	}
	return out
}

func TestTheLoginAnswerSaysWhyAWholeSessionIsNotLoggedIn(t *testing.T) {
	adminTestServer(t)
	good := sessionCookie(t, "uid-good", "good@example.com")
	got := loginAnswer(t, good)
	if got["loggedIn"] != true || got["reason"] != nil {
		t.Fatalf("a session the server knows: %v", got)
	}

	// a cookie whose session this server has no record of (the sign-in was stored somewhere else, or lost)
	rec := httptest.NewRecorder()
	if err := cookie.Set_SessionCookie(rec, httptest.NewRequest("GET", "/", nil), user.UserSession{Uid: "uid-good", SessionID: "a-token-nobody-stored", ExpirationTime: 9999999999999, LoggedIn: true}); err != nil {
		t.Fatal(err)
	}
	lost := rec.Result().Cookies()[0]
	got = loginAnswer(t, lost)
	if got["loggedIn"] != false || got["reason"] != "not_found" || got["uid"] != "uid-good" {
		t.Fatalf("a session the server does not know: %v", got)
	}

	// no cookie at all: a visitor, nothing to explain
	got = loginAnswer(t, nil)
	if got["loggedIn"] != false || got["reason"] != nil {
		t.Fatalf("a visitor: %v", got)
	}
}
