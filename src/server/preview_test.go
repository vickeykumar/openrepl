package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"livepreview"
	"utils"
)

// The Preview route (preview.go): the token in the address names the workspace, since the sandboxed frame sends no
// cookies.
func TestThePreviewRouteServesTheWorkspaceNamedByAToken(t *testing.T) {
	withSecret(t)
	home := filepath.Join(t.TempDir(), "guest-abc")
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, "index.html"), []byte("<h1>mine</h1>"), 0o644)
	other := filepath.Join(filepath.Dir(home), "guest-other")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "secret.html"), []byte("not yours"), 0o644)

	tok, err := livepreview.NewToken(utils.Secret(), home, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handlePreview(rec, httptest.NewRequest("GET", url, nil))
		return rec
	}
	rec := get("/preview/" + tok + "/f/index.html")
	if rec.Code != 200 || rec.Body.String() != livepreview.BridgeScript+"<h1>mine</h1>" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox ") {
		t.Fatalf("%d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	// the token reaches its own workspace only
	if rec := get("/preview/" + tok + "/f/../guest-other/secret.html"); rec.Code == 200 {
		t.Error("another workspace was reached through the token")
	}
	// a wrong or foreign token: gone, with the sandbox headers and a way back
	for _, bad := range []string{"nope", tok + "x", "a.b"} {
		rec := get("/preview/" + bad + "/f/index.html")
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "ended") || rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("token %q: %d %q", bad, rec.Code, rec.Body.String())
		}
	}
	if rec := get("/preview/" + tok); rec.Code != 404 {
		t.Errorf("no kind: %d", rec.Code)
	}
	if rec := get("/preview/" + tok + "/zzz/x"); rec.Code != 404 {
		t.Errorf("an unknown kind: %d", rec.Code)
	}
}

func TestThePreviewRouteRefusesATokenMadeWithAnotherSecret(t *testing.T) {
	withSecret(t)
	home := t.TempDir()
	tok, _ := livepreview.NewToken("some-other-secret", home, time.Now(), time.Hour)
	rec := httptest.NewRecorder()
	(&Server{}).handlePreview(rec, httptest.NewRequest("GET", "/preview/"+tok+"/f/x", nil))
	if rec.Code != http.StatusGone {
		t.Errorf("%d", rec.Code)
	}
}

func TestAViewerIdIsLettersAndDigitsOnlyAndShort(t *testing.T) {
	for in, want := range map[string]string{
		"abc123":                  "abc123",
		"../../x":                 "x",
		"a b|c\x00d/e":            "abcde",
		"":                        "",
		strings.Repeat("a", 80):   strings.Repeat("a", 32),
		"{\"h\":\"/home/other\"}": "hhomeother",
	} {
		if got := viewerID(in); got != want {
			t.Errorf("viewerID(%q) = %q, want %q", in, got, want)
		}
	}
}
