package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appstore"
)

func testStoreService(t *testing.T, inst appstore.Installer, safe ...string) *appstore.Service {
	t.Helper()
	svc := appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: inst, Safe: func() []string { return safe }})
	appStoreMu.Lock()
	old := appStoreSvc
	appStoreSvc = svc
	appStoreMu.Unlock()
	t.Cleanup(func() { appStoreMu.Lock(); appStoreSvc = old; appStoreMu.Unlock() })
	return svc
}

func TestStoreIsRefusedToGuests(t *testing.T) {
	testStoreService(t, appstore.Fake{}, "flask")
	for _, path := range []string{"list", "job?name=flask", "info?name=flask"} {
		w := httptest.NewRecorder()
		(&Server{options: &Options{}}).handleStoreAPI(w, httptest.NewRequest("GET", "http://openrepl.test/store/api/"+path, nil))
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "signin") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://openrepl.test/store/api/install", strings.NewReader(`{"name":"flask"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	(&Server{options: &Options{}}).handleStoreAPI(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a guest's install: %d", w.Code)
	}
}

func TestStoreOffAndMissing(t *testing.T) {
	appStoreMu.Lock()
	old := appStoreSvc
	appStoreSvc = nil
	appStoreMu.Unlock()
	defer func() { appStoreMu.Lock(); appStoreSvc = old; appStoreMu.Unlock() }()
	w := httptest.NewRecorder()
	(&Server{options: &Options{}}).handleStoreAPI(w, httptest.NewRequest("GET", "http://openrepl.test/store/api/list", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("no store: %d", w.Code)
	}
}

func TestStoreSyncNeedsTheWorkerToken(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{}, "flask")
	svc.Install("ann", "flask", "lib", false)
	svc.Wait()
	srv := &Server{options: &Options{WorkerToken: "s3cret"}}
	call := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://openrepl.test/store/sync", strings.NewReader(`{"node":"w1","state":{"have":["flask-1.0.0"],"sizeMB":1.5}}`))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.handleStoreSync(w, r)
		return w
	}
	for _, bad := range []string{"", "wrong", "s3cre"} {
		if w := call(bad); w.Code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", bad, w.Code)
		}
	}
	w := call("s3cret")
	var snap appstore.Snapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snap) != nil || len(snap.Apps) != 1 || !snap.Full {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	appStoreMu.Lock()
	n, ok := appNodes["w1"]
	delete(appNodes, "w1")
	appStoreMu.Unlock()
	if !ok || n.SizeMB != 1.5 || len(n.Have) != 1 {
		t.Errorf("the worker's state was not kept: %+v", n)
	}
	// a server with no worker token refuses every call, even an empty one
	(&Server{options: &Options{}}).handleStoreSync(w, httptest.NewRequest("POST", "http://openrepl.test/store/sync", nil))
}

func TestGatewayHTTPBase(t *testing.T) {
	for in, want := range map[string]string{
		"wss://openrepl.com/api/tunnel":     "https://openrepl.com",
		"ws://127.0.0.1:8080/api/tunnel":    "http://127.0.0.1:8080",
		"ssh://gw.example.com:2222":         "",
		"not a url":                         "",
		"wss://gw.example.com/x/api/tunnel": "https://gw.example.com",
	} {
		if got := gatewayHTTPBase(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestLookupAppReadsCondaForge(t *testing.T) {
	hits := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasSuffix(r.URL.Path, "/flask") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"summary":"A simple framework for building complex web applications.","latest_version":"3.0.2","home":"https://flask.palletsprojects.com"}`))
	}))
	defer api.Close()
	old := condaForgeAPI
	condaForgeAPI = api.URL + "/"
	defer func() { condaForgeAPI = old }()
	infoMu.Lock()
	infoCache = map[string]struct {
		info appInfo
		at   time.Time
	}{}
	infoMu.Unlock()
	info, err := lookupApp(context.Background(), "Flask")
	if err != nil || info.Version != "3.0.2" || !strings.Contains(info.Summary, "web applications") {
		t.Fatalf("%+v %v", info, err)
	}
	lookupApp(context.Background(), "flask")
	if hits != 1 {
		t.Errorf("the second look must come from the cache: %d calls", hits)
	}
	if _, err := lookupApp(context.Background(), "nothing-here"); err == nil || !strings.Contains(err.Error(), "no package") {
		t.Errorf("an unknown package: %v", err)
	}
	if _, err := lookupApp(context.Background(), "../etc"); err == nil {
		t.Error("a bad name must never be sent to conda-forge")
	}
}

func TestStoreSettingsAreChecked(t *testing.T) {
	s := SiteSettings{StoreSafeList: []string{" Flask ", "", "numpy"}, StoreLimitMB: 500}
	got, err := s.normalize()
	if err != nil || strings.Join(got.StoreSafeList, ",") != "flask,numpy" {
		t.Fatalf("%v %v", got.StoreSafeList, err)
	}
	for _, bad := range []SiteSettings{{StoreSafeList: []string{"a/b"}}, {StoreSafeList: []string{"../x"}}, {StoreLimitMB: 5}, {StoreLimitMB: 2000000}} {
		if _, err := bad.normalize(); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	if len(storeSafeList()) == 0 || storeLimitMB() != defaultStoreLimitMB {
		t.Error("the built-in list and limit")
	}
}
