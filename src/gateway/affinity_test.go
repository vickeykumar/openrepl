package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == AffinityCookie {
			return c
		}
	}
	t.Fatal("no affinity cookie set")
	return nil
}

func TestAffinityIssueAndRead(t *testing.T) {
	a := NewAffinity([]byte("secret"), time.Hour)
	rec := httptest.NewRecorder()
	id, err := a.Issue(rec)
	if err != nil || id == "" {
		t.Fatalf("Issue = %q, %v", id, err)
	}
	c := cookieOf(t, rec)
	if !c.HttpOnly || c.MaxAge != 3600 {
		t.Fatalf("cookie attrs: %+v", c)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	if got := a.GuestID(req); got != id {
		t.Fatalf("GuestID = %q, want %q", got, id)
	}
}

func TestAffinityRejectsTamperedAndForeignCookies(t *testing.T) {
	a := NewAffinity([]byte("secret"), time.Hour)
	rec := httptest.NewRecorder()
	if _, err := a.Issue(rec); err != nil {
		t.Fatal(err)
	}
	c := cookieOf(t, rec)

	tampered := *c
	tampered.Value = c.Value[:len(c.Value)-2] + "xx"
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&tampered)
	if got := a.GuestID(req); got != "" {
		t.Fatalf("tampered cookie accepted: %q", got)
	}

	other := NewAffinity([]byte("other-secret"), time.Hour)
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	if got := other.GuestID(req); got != "" {
		t.Fatalf("cookie signed with another secret accepted: %q", got)
	}

	if got := a.GuestID(httptest.NewRequest("GET", "/", nil)); got != "" {
		t.Fatalf("missing cookie returned %q", got)
	}
}

func TestGuestIDIsShort(t *testing.T) {
	id, err := NewGuestID()
	if err != nil {
		t.Fatal(err)
	}
	// "guest-" + id is the folder name a user sees on a worker.
	if len(id) != 10 {
		t.Fatalf("guest id %q has %d characters, want 10", id, len(id))
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			t.Fatalf("guest id %q is not lowercase hex", id)
		}
	}
}

func TestAffinityIDsAreUnique(t *testing.T) {
	a := NewAffinity([]byte("secret"), time.Hour)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := a.Issue(httptest.NewRecorder())
		if err != nil || seen[id] {
			t.Fatalf("duplicate or failed id: %q %v", id, err)
		}
		seen[id] = true
	}
}
