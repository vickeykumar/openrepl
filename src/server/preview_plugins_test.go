package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appstore"
	"livepreview"
	"utils"
)

// A third party plugin of the preview (Eruda): installed, switched on by the user, it is added to the page the server sends and served from
// this site; a user who has it off, another user, and every path outside what the catalogue names get nothing.
func TestThePreviewServesAndInjectsAThirdPartyPluginForTheUserWhoHasItOn(t *testing.T) {
	withSecret(t)
	svc := testStoreService(t, appstore.Fake{})
	id := "eruda-3.4.3"
	p := filepath.Join(svc.Store().BundlePath(id), "node_modules", "eruda")
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "eruda.js"), []byte("window.eruda={init:function(){}};"), 0o644)
	os.WriteFile(filepath.Join(p, "package.json"), []byte(`{"secret":"x"}`), 0o644)
	if _, err := svc.Store().Finish(id, "eruda", "3.4.3"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mk := func(name string) string {
		h := filepath.Join(root, name)
		os.MkdirAll(h, 0o755)
		os.WriteFile(filepath.Join(h, "index.html"), []byte("<h1>mine</h1>"), 0o644)
		return h
	}
	ann, bob := mk("ann-home"), mk("bob-home")
	if err := svc.SwitchPlugin("ann-home", "plugin:eruda", true); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	page := func(home string) string {
		tok, _ := livepreview.NewToken(utils.Secret(), home, time.Now(), time.Hour)
		rec := httptest.NewRecorder()
		s.handlePreview(rec, httptest.NewRequest("GET", "/preview/"+tok+"/f/index.html", nil))
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox ") || strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src") {
			t.Errorf("the policy of the page must stay as it was: %q", rec.Header().Get("Content-Security-Policy"))
		}
		return rec.Body.String()
	}
	if b := page(ann); !strings.Contains(b, `src="/preview/lib/eruda/eruda.js"`) || !strings.Contains(b, "eruda.init()") {
		t.Errorf("ann has it on: %s", b)
	}
	if b := page(bob); strings.Contains(b, "eruda") {
		t.Errorf("bob has it off: %s", b)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handlePreview(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/preview/lib/eruda/eruda.js"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "eruda") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the library: %d %s %v", rec.Code, rec.Body.String(), rec.Header())
	}
	for _, bad := range []string{"/preview/lib/eruda/package.json", "/preview/lib/eruda/../../x", "/preview/lib/nothing/x.js", "/preview/lib/eruda", "/preview/lib/", "/preview/lib/@a/b"} {
		if rec := get(bad); rec.Code != 404 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	// the App Store off: nothing is served or added
	old := GetSiteSettings()
	s2 := old
	s2.StoreOff = true
	SaveSiteSettings(s2)
	defer SaveSiteSettings(old)
	if rec := get("/preview/lib/eruda/eruda.js"); rec.Code != 404 {
		t.Errorf("store off: %d", rec.Code)
	}
}
