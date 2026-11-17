package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The files of the site are sent with a Cache-Control and the ETag of the build, and a file that is asked for again with that ETag is answered 304.
func TestStaticFilesAreCachedWithTheETagOfTheBuild(t *testing.T) {
	old := buildCommit
	buildCommit = "abc1234"
	defer func() { buildCommit = old }()
	h := cacheStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("file")) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/js/x.js", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != staticCacheControl || w.Header().Get("ETag") != `W/"abc1234"` || w.Body.String() != "file" {
		t.Fatalf("first answer: %d %v", w.Code, w.Header())
	}
	r := httptest.NewRequest("GET", "/js/x.js", nil)
	r.Header.Set("If-None-Match", `W/"abc1234"`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("asked again with the ETag: %d %q", w.Code, w.Body.String())
	}
	// another build: the file is sent again
	r.Header.Set("If-None-Match", `W/"old0000"`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("another build: %d", w.Code)
	}
	// a build with no commit has no ETag, and the file is still served
	buildCommit = ""
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/js/x.js", nil))
	if w.Code != 200 || w.Header().Get("ETag") != "" || w.Header().Get("Cache-Control") != staticCacheControl {
		t.Fatalf("no commit: %d %v", w.Code, w.Header())
	}
}

// What is made for one visitor is never kept.
func TestWhatIsMadeForOneVisitorIsNeverKept(t *testing.T) {
	srv := &Server{options: &Options{Credential: "x", Term: "xterm"}}
	for name, h := range map[string]http.HandlerFunc{"auth_token.js": srv.handleAuthToken, "config.js": srv.handleConfig} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", "/"+name, nil))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", name, w.Header().Get("Cache-Control"))
		}
	}
}
