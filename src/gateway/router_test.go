package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouteClassification(t *testing.T) {
	admin := []string{"admin", "admin/settings", "admin/workers/w1/drain"}
	notAdmin := []string{"", "administrator", "adminx/foo", "practice/admin", "js/admin"}
	for _, p := range admin {
		if !IsAdmin(p) {
			t.Errorf("IsAdmin(%q) = false", p)
		}
	}
	for _, p := range notAdmin {
		if IsAdmin(p) {
			t.Errorf("IsAdmin(%q) = true", p)
		}
	}

	exec := []string{"ws", "ws_go", "ws_c", "ws_python", "ws_filebrowser", "upload_file"}
	site := []string{"", "login", "logout", "profile", "chat/completions", "blog", "snippet",
		"s/abc", "practice/progress", "js/gotty-bundle.js", "config.js", "auth_token.js",
		"wsx", "ws/x", "upload_files", "admin"}
	for _, p := range exec {
		if !IsExecutionBound(p) {
			t.Errorf("IsExecutionBound(%q) = false", p)
		}
	}
	for _, p := range site {
		if IsExecutionBound(p) {
			t.Errorf("IsExecutionBound(%q) = true", p)
		}
	}
}

// recorder is a Site handler that remembers which paths reached it.
type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (h *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.paths = append(h.paths, r.URL.Path)
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// served is the number of requests the recorder has handled.
func (h *recorder) served() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.paths)
}

func newTestRouter(site http.Handler, prefix string, picker Picker) *Router {
	return NewRouter(Config{
		Site:       site,
		PathPrefix: prefix,
		IsEntryPage: func(rel string) bool {
			return rel == "" || rel == "practice" || rel == "python"
		},
		UID:      func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:   []byte("secret"),
		GuestTTL: time.Hour,
		Picker:   picker,
		Local:    LocalConfig{Weight: 10},
	})
}

func do(h http.Handler, method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminAndSiteRoutesNeverTouchTheRegistry(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/", nil)
	for _, p := range []string{"/admin", "/admin/settings", "/admin/workers", "/login", "/blog", "/js/x.js", "/practice/progress"} {
		rec := do(rt, "GET", p, nil)
		if rec.Code != 200 {
			t.Fatalf("%s -> %d", p, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s issued a cookie", p)
		}
	}
	if rt.Registry().Len() != 0 {
		t.Fatalf("registry len = %d, want 0", rt.Registry().Len())
	}
	if len(site.paths) != 7 {
		t.Fatalf("site saw %v", site.paths)
	}
}

func TestExecutionRouteIsServedLocallyAndAssigned(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/", nil)
	rec := do(rt, "GET", "/ws_go", nil)
	if rec.Code != 200 || len(site.paths) != 1 || site.paths[0] != "/ws_go" {
		t.Fatalf("code=%d paths=%v", rec.Code, site.paths)
	}
	if rt.Registry().Len() != 1 {
		t.Fatalf("registry len = %d", rt.Registry().Len())
	}
	if got := cookieOf(t, rec); got.Value == "" {
		t.Fatal("empty affinity cookie")
	}
}

func TestEntryPageAssignsBeforeServing(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/", nil)
	rec := do(rt, "GET", "/", nil)
	c := cookieOf(t, rec)
	if rt.Registry().Len() != 1 {
		t.Fatal("entry page did not create a context")
	}
	// The terminal request that follows carries the cookie and reuses it.
	do(rt, "GET", "/ws_go", func(r *http.Request) { r.AddCookie(c) })
	if rt.Registry().Len() != 1 {
		t.Fatalf("registry len = %d, want 1 (same guest)", rt.Registry().Len())
	}
	rec = do(rt, "GET", "/ws_go", func(r *http.Request) { r.AddCookie(c) })
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("known guest was issued a new cookie")
	}
}

type countingPicker struct {
	calls int32
	id    string
	err   error
}

func (p *countingPicker) Pick(Identity) (string, error) {
	atomic.AddInt32(&p.calls, 1)
	return p.id, p.err
}

func TestPickerIsCalledOncePerSession(t *testing.T) {
	site := &recorder{}
	p := &countingPicker{id: LocalID}
	rt := newTestRouter(site, "/", p)
	first := do(rt, "GET", "/ws_go", nil)
	c := cookieOf(t, first)
	for i := 0; i < 5; i++ {
		do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
		do(rt, "POST", "/upload_file", func(r *http.Request) { r.AddCookie(c) })
	}
	if n := atomic.LoadInt32(&p.calls); n != 1 {
		t.Fatalf("picker called %d times, want 1", n)
	}
}

func TestSignedInUserKeyedByUID(t *testing.T) {
	site := &recorder{}
	p := &countingPicker{id: LocalID}
	rt := newTestRouter(site, "/", p)
	with := func(r *http.Request) { r.Header.Set("X-Test-Uid", "42") }
	rec := do(rt, "GET", "/ws_go", with)
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("signed-in user was issued a guest cookie")
	}
	do(rt, "GET", "/ws_filebrowser", with)
	if n := atomic.LoadInt32(&p.calls); n != 1 {
		t.Fatalf("picker called %d times", n)
	}
	if _, ok := rt.Registry().Resolve("u:42"); !ok {
		t.Fatal("no context for u:42")
	}
}

func TestUnknownBackendAndPickerErrorReturn503(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/", &countingPicker{id: "worker-9"})
	if rec := do(rt, "GET", "/ws_go", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown backend -> %d", rec.Code)
	}
	rt = newTestRouter(site, "/", &countingPicker{err: errors.New("none")})
	if rec := do(rt, "GET", "/ws_go", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("picker error -> %d", rec.Code)
	}
	if len(site.paths) != 0 {
		t.Fatalf("site was reached: %v", site.paths)
	}
}

type fakeBackend struct {
	state State
	hits  int
}

func (b *fakeBackend) ID() string               { return "worker-1" }
func (b *fakeBackend) State() State             { return b.state }
func (b *fakeBackend) Weight() int              { return 10 }
func (b *fakeBackend) Capacity() (int64, int64) { return 0, 0 }
func (b *fakeBackend) HasLanguage(string) bool  { return true }
func (b *fakeBackend) Serve(w http.ResponseWriter, r *http.Request) error {
	b.hits++
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func TestRemoteBackendServesItsSessionsAndOfflineFails(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/", &countingPicker{id: "worker-1"})
	fb := &fakeBackend{state: Online}
	rt.backends["worker-1"] = fb

	first := do(rt, "GET", "/ws_go", nil)
	c := cookieOf(t, first)
	if first.Code != http.StatusAccepted || fb.hits != 1 || len(site.paths) != 0 {
		t.Fatalf("code=%d hits=%d site=%v", first.Code, fb.hits, site.paths)
	}

	fb.state = Offline
	rec := do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline backend -> %d", rec.Code)
	}
	if _, ok := rt.Registry().Resolve("g:" + rt.affinity.GuestID(requestWith(c))); !ok {
		t.Fatal("context was dropped; the session must not move to another backend")
	}
	// Admin stays reachable while every worker is offline.
	if rec := do(rt, "GET", "/admin/workers", nil); rec.Code != 200 {
		t.Fatalf("admin -> %d", rec.Code)
	}
}

func requestWith(c *http.Cookie) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	return req
}

func TestPathPrefix(t *testing.T) {
	site := &recorder{}
	rt := newTestRouter(site, "/abc123/", nil)
	if rec := do(rt, "GET", "/abc123/admin/settings", nil); rec.Code != 200 || rt.Registry().Len() != 0 {
		t.Fatal("prefixed admin route was treated as execution-bound")
	}
	do(rt, "GET", "/abc123/ws_go", nil)
	if rt.Registry().Len() != 1 {
		t.Fatal("prefixed execution route was not assigned")
	}
	// A path outside the prefix is not an execution route.
	rt2 := newTestRouter(site, "/abc123/", nil)
	do(rt2, "GET", "/ws_go", nil)
	if rt2.Registry().Len() != 0 {
		t.Fatal("path outside the prefix was treated as execution-bound")
	}
}
