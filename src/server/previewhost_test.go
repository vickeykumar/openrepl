package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gateway"
	"livepreview"
	"trusted"
	"utils"
)

func TestSameHostComparesTheHostNameOnly(t *testing.T) {
	for _, c := range []struct {
		host, base string
		want       bool
	}{
		{"preview.example.test", "https://preview.example.test", true},
		{"PREVIEW.example.test:443", "https://preview.example.test", true},
		{"preview.example.test", "https://openrepl.com", false},
		{"localhost:9000", "http://localhost:9000", true},
		{"localhost:8000", "http://localhost:9000", true}, // the port is not compared
		{"[::1]:8000", "http://[::1]:9000", true},
		{"preview.localhost:8000", "http://localhost:9000", false},
		{"preview.example.test", "", false},
	} {
		if got := sameHost(c.host, c.base); got != c.want {
			t.Errorf("%q against %q: %v", c.host, c.base, got)
		}
	}
}

// the preview host: every path of that host ends below /preview/, marked; the site's own host is left alone
func TestThePreviewHostIsAThinLayerOverThePreviewRoutes(t *testing.T) {
	withSecret(t)
	old := GetSiteSettings()
	defer SaveSiteSettings(old)
	if err := SaveSiteSettings(SiteSettings{PreviewBaseURL: "https://preview.example.test"}); err != nil {
		t.Fatal(err)
	}
	var seen string
	var marked bool
	h := (&Server{}).wrapPreviewHost(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, marked = r.URL.Path, gateway.IsPreviewHost(r)
		w.WriteHeader(204)
	}), "/")
	do := func(host, path string) int {
		r := httptest.NewRequest("GET", "http://"+host+path, nil)
		rec := httptest.NewRecorder()
		seen, marked = "", false
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := do("preview.example.test", "/TOKEN/f/index.html"); c != 204 || seen != "/preview/TOKEN/f/index.html" || !marked {
		t.Errorf("a file on the host: %d %q %v", c, seen, marked)
	}
	if c := do("preview.example.test", "/TOKEN/p/8000/x"); seen != "/preview/TOKEN/p/8000/x" || !marked || c != 204 {
		t.Errorf("a program on the host: %q %v", seen, marked)
	}
	if do("preview.example.test", "/preview/TOKEN/f/a.html"); seen != "/preview/TOKEN/f/a.html" || !marked {
		t.Errorf("the site's form is accepted on the host: %q %v", seen, marked)
	}
	// nothing of the site is reachable there: /admin becomes /preview/admin (a token that is none), the root is 404
	if do("preview.example.test", "/admin"); seen != "/preview/admin" || !marked {
		t.Errorf("/admin on the host: %q", seen)
	}
	if c := do("preview.example.test", "/"); c != http.StatusNotFound || seen != "" {
		t.Errorf("the root of the host: %d %q", c, seen)
	}
	// the site's own host: untouched, not marked
	if c := do("openrepl.test", "/preview/TOKEN/f/index.html"); c != 204 || seen != "/preview/TOKEN/f/index.html" || marked {
		t.Errorf("the site: %d %q %v", c, seen, marked)
	}
	if do("openrepl.test", "/admin"); seen != "/admin" || marked {
		t.Errorf("the site's /admin: %q %v", seen, marked)
	}
	// no base set: the host means nothing
	SaveSiteSettings(SiteSettings{})
	if do("preview.example.test", "/TOKEN/f/index.html"); seen != "/TOKEN/f/index.html" || marked {
		t.Errorf("no base URL: %q %v", seen, marked)
	}
}

// a worker believes the mark only over the tunnel
func TestTheAddressesAFileOrProgramLinksToFollowTheForm(t *testing.T) {
	plain := httptest.NewRequest("GET", "/preview/T/f/x", nil)
	if previewPath(plain, "T") != "/preview/T" || publicPath(plain, "/preview/T/p/8000") != "/preview/T/p/8000" {
		t.Error("the site's own form")
	}
	host := gateway.MarkPreviewHost(httptest.NewRequest("GET", "/preview/T/f/x", nil))
	if previewPath(host, "T") != "/T" || publicPath(host, "/preview/T/p/8000") != "/T/p/8000" {
		t.Errorf("the host's form: %q %q", previewPath(host, "T"), publicPath(host, "/preview/T/p/8000"))
	}
	// the trusted header of the gateway, on a request that did not come over the tunnel, means nothing
	forged := httptest.NewRequest("GET", "/preview/T/f/x", nil)
	forged.Header.Set(trusted.HeaderPreviewHost, "1")
	if previewPath(forged, "T") != "/preview/T" {
		t.Error("a client's copy of the header was believed")
	}
}

// the whole route on the host: a file of the workspace is served at /<token>/f/<path>, and the site's form still works
func TestAFileIsServedOnThePreviewHostAndOnTheSite(t *testing.T) {
	withSecret(t)
	old := GetSiteSettings()
	defer SaveSiteSettings(old)
	SaveSiteSettings(SiteSettings{PreviewBaseURL: "https://preview.example.test"})
	home := filepath.Join(t.TempDir(), "guest-abc")
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, "index.html"), []byte("<h1>mine</h1>"), 0o644)
	tok, err := livepreview.NewToken(utils.Secret(), home, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	h := s.wrapPreviewHost(http.HandlerFunc(s.handlePreview), "/")
	get := func(host, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://"+host+path, nil))
		return rec
	}
	for _, c := range []struct{ host, path string }{
		{"preview.example.test", "/" + tok + "/f/index.html"},
		{"preview.example.test", "/preview/" + tok + "/f/index.html"},
		{"openrepl.test", "/preview/" + tok + "/f/index.html"},
	} {
		rec := get(c.host, c.path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<h1>mine</h1>") {
			t.Errorf("%s%s: %d %q", c.host, c.path, rec.Code, rec.Body.String())
		}
	}
	// the site's host has no /<token>/ form
	if rec := get("openrepl.test", "/"+tok+"/f/index.html"); rec.Code == 200 {
		t.Error("the site answered the host's form")
	}
	// a token that ended is the same "ended" page on the host
	if rec := get("preview.example.test", "/bad.token/f/index.html"); rec.Code != http.StatusGone {
		t.Errorf("a bad token on the host: %d", rec.Code)
	}
}
