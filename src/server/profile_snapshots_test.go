package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"homesnap"
)

func TestOnlyThePagesOwnJSONMayChangeTheWorkspace(t *testing.T) {
	post := func(contentType, origin, site string) int {
		r := httptest.NewRequest("POST", "http://openrepl.test/profile/snapshots/restore", strings.NewReader(`{"id":"d:2026-11-10","mode":"folder"}`))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		(&Server{}).handleProfileSnapshotRestore(w, r)
		return w.Code
	}
	// a form post, a request from another site, and one with no content type never reach the account
	for name, code := range map[string]int{
		"a form":             post("application/x-www-form-urlencoded", "", ""),
		"no content type":    post("", "", ""),
		"another origin":     post("application/json", "https://evil.example", ""),
		"another site":       post("application/json", "", "cross-site"),
		"a sibling site":     post("application/json", "", "same-site"),
		"text/plain trick":   post("text/plain", "http://openrepl.test", "same-origin"),
		"a different scheme": post("application/json", "http://openrepl.test.evil.example", ""),
	} {
		if code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, code)
		}
	}
	if sameOrigin(httptest.NewRequest("POST", "http://openrepl.test/x", nil)) {
		t.Error("a request with no content type was taken")
	}
	r := httptest.NewRequest("POST", "http://openrepl.test/x", nil)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.Header.Set("Origin", "http://openrepl.test")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOrigin(r) {
		t.Error("the page's own request was refused")
	}
	// the wrong method is refused before anything else
	w := httptest.NewRecorder()
	(&Server{}).handleProfileSnapshotRestore(w, httptest.NewRequest("GET", "http://openrepl.test/profile/snapshots/restore", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on restore: %d", w.Code)
	}
}

func TestSnapshotErrorsAreSaidInWordsAndNeverLeakTheStore(t *testing.T) {
	for err, want := range map[error]int{
		homesnap.ErrNotAvailable:      http.StatusNotFound,
		homesnap.ErrNoSuchSnapshot:    http.StatusNotFound,
		homesnap.ErrTooSoon:           http.StatusTooManyRequests,
		homesnap.ErrBusy:              http.StatusTooManyRequests,
		errString("dial tcp 1.2.3.4"): http.StatusBadGateway,
	} {
		code, msg := snapshotError(err)
		if code != want || msg == "" {
			t.Errorf("%v: %d %q", err, code, msg)
		}
	}
	if _, msg := snapshotError(errString("Get https://secret-bucket.s3.example/key?X-Amz-Signature=abc: boom")); strings.Contains(msg, "secret") || strings.Contains(msg, "Signature") {
		t.Errorf("an error of the store reached the page: %q", msg)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
