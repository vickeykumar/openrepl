package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		w.Write([]byte(`{"summary":"A simple framework for building complex web applications.","latest_version":"3.0.2","home":"https://flask.palletsprojects.com","versions":["2.0.0","2.3.0","3.0.2","../x"]}`))
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
	if len(info.Versions) != 3 || info.Versions[0] != "3.0.2" || info.Versions[2] != "2.0.0" {
		t.Errorf("the versions, newest first, and only real ones: %v", info.Versions)
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

func TestStoreBundleIsSentToAWorkerWithTheToken(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{}, "flask")
	svc.Install("ann", "flask", "lib", false)
	svc.Wait()
	srv := &Server{options: &Options{WorkerToken: "s3cret"}}
	get := func(token, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://openrepl.test/store/bundle?id="+id, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.handleStoreBundle(w, r)
		return w
	}
	if w := get("", "flask-1.0.0"); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
	if w := get("s3cret", "../etc"); w.Code != http.StatusNotFound {
		t.Errorf("a bad name: %d", w.Code)
	}
	if w := get("s3cret", "numpy-1.0.0"); w.Code != http.StatusNotFound {
		t.Errorf("a bundle that is not there: %d", w.Code)
	}
	w := get("s3cret", "flask-1.0.0")
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	dest := filepath.Join(t.TempDir(), "flask-1.0.0")
	if err := appstore.ReadBundle(w.Body, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".openrepl-bundle.json")); err != nil {
		t.Error("the marker must come with it")
	}
	if gatewayFetch("", "t", "/x") != nil || gatewayFetch("http://x", "", "/x") != nil {
		t.Error("a worker that cannot reach its gateway has no fetch")
	}
}

func TestStoreHealthSaysWhatIsWrong(t *testing.T) {
	svc := testStoreService(t, appstore.Missing{Reason: "micromamba is not installed"}, "flask")
	if st, detail := appStoreHealth(); st != healthWarn || !strings.Contains(detail, "micromamba") {
		t.Errorf("no installer: %s %s", st, detail)
	}
	svc2 := testStoreService(t, appstore.Fake{Fail: map[string]bool{"flask": true}}, "flask")
	_ = svc
	svc2.Install("ann", "flask", "lib", false)
	svc2.Wait()
	if st, detail := appStoreHealth(); st != healthWarn || !strings.Contains(detail, "1 failed") {
		t.Errorf("a failed install: %s %s", st, detail)
	}
}

func TestAWorkerSaysHowFarAFetchIsAndTheAdminGivesItCommands(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{}, "flask")
	svc.Install("ann", "flask", "lib", false)
	svc.Wait()
	srv := &Server{options: &Options{WorkerToken: "s3cret"}}
	progress := func(token, body string) int {
		r := httptest.NewRequest("POST", "http://openrepl.test/store/progress", strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		srv.handleStoreProgress(w, r)
		return w.Code
	}
	if progress("", `{"node":"w1","id":"flask-1.0.0","pct":40}`) != http.StatusUnauthorized {
		t.Error("no token")
	}
	if progress("s3cret", `{"node":"../x","id":"flask-1.0.0","pct":40}`) != http.StatusBadRequest || progress("s3cret", `{"node":"w1","id":"../x","pct":40}`) != http.StatusBadRequest {
		t.Error("a bad node or bundle")
	}
	if progress("s3cret", `{"node":"w1","id":"flask-1.0.0","pct":40}`) != http.StatusNoContent {
		t.Fatal("a good report")
	}
	appStoreMu.Lock()
	got := appNodes["w1"].Fetching["flask-1.0.0"]
	appStoreMu.Unlock()
	if got != 40 {
		t.Errorf("the percent: %d", got)
	}
	progress("s3cret", `{"node":"w1","id":"flask-1.0.0","pct":100}`)
	appStoreMu.Lock()
	_, still := appNodes["w1"].Fetching["flask-1.0.0"]
	appCommands["w1"] = []string{"flask-1.0.0"}
	appStoreMu.Unlock()
	if still {
		t.Error("a finished fetch is not shown any more")
	}
	// the worker's next ask for the catalog brings the command, once
	ask := func() appstore.Snapshot {
		r := httptest.NewRequest("POST", "http://openrepl.test/store/sync", strings.NewReader(`{"node":"w1","state":{"have":[]}}`))
		r.Header.Set("Authorization", "Bearer s3cret")
		w := httptest.NewRecorder()
		srv.handleStoreSync(w, r)
		var snap appstore.Snapshot
		json.Unmarshal(w.Body.Bytes(), &snap)
		return snap
	}
	if snap := ask(); len(snap.Reinstall) != 1 || snap.Reinstall[0] != "flask-1.0.0" {
		t.Errorf("the command: %+v", snap.Reinstall)
	}
	if snap := ask(); len(snap.Reinstall) != 0 {
		t.Errorf("a command is given once: %+v", snap.Reinstall)
	}
	appStoreMu.Lock()
	delete(appNodes, "w1")
	appStoreMu.Unlock()
}

func TestThePluginsOfAUserAreTheOnesThatAreOn(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{})
	svc.Install("root", "wordcount", "plugin", true)
	svc.Install("root", "clock", "plugin", true)
	svc.Wait()
	if got := storePlugins(svc, "ann"); len(got) != 0 {
		t.Fatalf("nothing is on: %+v", got)
	}
	svc.Switch("ann", "wordcount-1.0.0", true)
	got := storePlugins(svc, "ann")
	if len(got) != 1 || got[0].Name != "wordcount" || !strings.Contains(got[0].Code, "openreplPlugin") {
		t.Fatalf("%+v", got)
	}
	if got := storePlugins(svc, "bob"); len(got) != 0 {
		t.Error("another user's plugins are not shown")
	}
}

func TestTheSafeListMayNameToolsAndNodeLibraries(t *testing.T) {
	s := SiteSettings{StoreSafeList: []string{"flask", "tool:jq", "NPM:Express", "npm:lodash"}}
	got, err := s.normalize()
	if err != nil || strings.Join(got.StoreSafeList, ",") != "flask,tool:jq,npm:express,npm:lodash" {
		t.Fatalf("%v %v", got.StoreSafeList, err)
	}
	for _, bad := range []string{"npm:", "tool:../x", "npm:a/b", "lang:lua"} {
		if _, err := (SiteSettings{StoreSafeList: []string{bad}}).normalize(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// The admin pages call .filter on these lists: a store with nothing in it must send [] and never null (the admin's store page did not
// load on a fresh store: "Cannot read properties of null (reading 'filter')").
func TestAdminAndUserListsAreNeverNullOnAnEmptyStore(t *testing.T) {
	testStoreService(t, appstore.Fake{})
	w := httptest.NewRecorder()
	(&Server{options: &Options{}}).handleAdminStore(w, httptest.NewRequest("GET", "http://openrepl.test/admin/store", nil))
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	for _, k := range []string{"apps", "requests", "reports", "apt", "nodes", "safeList"} {
		if string(raw[k]) == "null" || len(raw[k]) == 0 {
			t.Errorf("%s is %q: it must be a list", k, raw[k])
		}
	}
	if string(raw["users"]) == "null" {
		t.Error("users must be an object")
	}
	svc := storeService()
	if v := svc.Views("", ""); v == nil {
		t.Error("views must be a list")
	}
}

// A worker that has only reported how far a fetch is has not told what it has: its row in the admin list must say "have": [], not null
// (the Servers tab called indexOf on it).
func TestAWorkerThatOnlyReportedAFetchHasAnEmptyListNotNull(t *testing.T) {
	testStoreService(t, appstore.Fake{})
	srv := &Server{options: &Options{WorkerToken: "s3cret"}}
	r := httptest.NewRequest("POST", "http://openrepl.test/store/progress", strings.NewReader(`{"node":"w9","id":"flask-1.0.0","pct":10}`))
	r.Header.Set("Authorization", "Bearer s3cret")
	srv.handleStoreProgress(httptest.NewRecorder(), r)
	defer func() { appStoreMu.Lock(); delete(appNodes, "w9"); appStoreMu.Unlock() }()
	w := httptest.NewRecorder()
	srv.handleAdminStore(w, httptest.NewRequest("GET", "http://openrepl.test/admin/store", nil))
	var out struct {
		Nodes []struct {
			Node string          `json:"node"`
			Have json.RawMessage `json:"have"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range out.Nodes {
		if n.Node == "w9" {
			found = true
			if string(n.Have) != "[]" {
				t.Errorf("have is %s", n.Have)
			}
		}
	}
	if !found {
		t.Fatalf("no row for w9: %s", w.Body.String())
	}
}
