package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appstore"
	"tunnel"
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

func TestStoreHealthSaysWhatIsWrong(t *testing.T) {
	svc := testStoreService(t, appstore.Missing{Reason: "pixi is not installed"}, "flask")
	if st, detail := appStoreHealth(); st != healthWarn || !strings.Contains(detail, "pixi") {
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

// The catalog goes to a worker over the tunnel, once: it is sent when the worker's revision is not the gateway's, a command to reinstall
// is given once, and what the worker says of itself is what the admin sees.
func TestTheCatalogIsSentToAWorkerOnlyWhenItChangedOrACommandWaits(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{}, "flask")
	svc.Install("ann", "flask", "lib", false)
	svc.Wait()
	storeCatalog.Lock()
	storeCatalog.raw = nil // made fresh for this service
	storeCatalog.Unlock()
	defer func() { appStoreMu.Lock(); delete(appNodes, "w1"); delete(appCommands, "w1"); appStoreMu.Unlock() }()
	u := storeUpdateFor(svc, "w1", 0)
	if u == nil || u.Rev == 0 || !strings.Contains(string(u.Catalog), "flask-1.0.0") {
		t.Fatalf("a worker with no catalog must get it: %+v", u)
	}
	if again := storeUpdateFor(svc, "w1", u.Rev); again != nil {
		t.Fatalf("a worker that is up to date must get nothing: %+v", again)
	}
	appStoreMu.Lock()
	appCommands["w1"] = []string{"flask-1.0.0"}
	appStoreMu.Unlock()
	cmd := storeUpdateFor(svc, "w1", u.Rev)
	if cmd == nil || len(cmd.Reinstall) != 1 || cmd.Rev != u.Rev {
		t.Fatalf("the command: %+v", cmd)
	}
	if storeUpdateFor(svc, "w1", u.Rev) != nil {
		t.Error("a command is given once")
	}
	if storeUpdateFor(svc, "../x", 0) != nil {
		t.Error("a bad node id gets nothing")
	}
	// what the worker says of itself shows in the admin's list, even a worker that has said nothing yet (a null list is an empty one)
	storeStateFrom("w1", json.RawMessage(`{"have":["flask-1.0.0"],"failed":{"jq-1.7":"no internet"},"retry":{"jq-1.7":1800000000},"sizeMB":14}`))
	appStoreMu.Lock()
	n := appNodes["w1"]
	appStoreMu.Unlock()
	if len(n.Have) != 1 || n.Failed["jq-1.7"] != "no internet" || n.Retry["jq-1.7"] != 1800000000 {
		t.Errorf("%+v", n)
	}
}

// A newer catalog replaces one that has not been applied yet, but a command to reinstall is never lost.
func TestANewerCatalogKeepsTheCommandsOfTheOneItReplaces(t *testing.T) {
	workerStore.Lock()
	workerStore.pending = nil
	workerStore.Unlock()
	queueCatalog(appstore.Snapshot{Full: true}, []string{"flask-1.0.0"})
	queueCatalog(appstore.Snapshot{Full: true, Apt: []string{"x"}}, nil)
	workerStore.Lock()
	p := workerStore.pending
	workerStore.pending = nil
	workerStore.Unlock()
	if p == nil || len(p.Apt) != 1 || len(p.Reinstall) != 1 || p.Reinstall[0] != "flask-1.0.0" {
		t.Fatalf("%+v", p)
	}
	<-workerStore.wake
}

// flakyInstaller fails its first install of a name and then works (a network that came back).
type flakyInstaller struct {
	appstore.Fake
	mu     *sync.Mutex
	failed map[string]bool
}

func (f flakyInstaller) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	f.mu.Lock()
	first := !f.failed[name]
	f.failed[name] = true
	f.mu.Unlock()
	if first {
		return errors.New("no internet")
	}
	return f.Fake.Install(ctx, name, version, kind, dest, log)
}

// A real gateway and a real worker over a real tunnel: the worker gets the catalog, installs by itself, fails once and says so (with the
// time of its next try), tries again by itself, and the gateway ends up knowing it has the app. Nothing is asked of the gateway.
func TestAWorkerInstallsFromTheCatalogOverTheTunnelAndTriesAgainByItself(t *testing.T) {
	old := appstore.Backoff
	appstore.Backoff = []time.Duration{300 * time.Millisecond}
	defer func() { appstore.Backoff = old }()
	gw := appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: appstore.Fake{}, Safe: func() []string { return []string{"flask"} }})
	gw.Install("ann", "flask", "lib", false)
	gw.Wait()
	storeCatalog.Lock()
	storeCatalog.raw = nil
	storeCatalog.Unlock()
	workerStore.Lock()
	workerStore.pending = nil
	workerStore.Unlock()
	defer func() { appStoreMu.Lock(); delete(appNodes, "w1"); appStoreMu.Unlock() }()

	key, err := tunnel.NewEphemeralHostKey()
	if err != nil {
		t.Fatal(err)
	}
	var hmu sync.Mutex
	var history []appstore.NodeState
	srv, err := tunnel.NewServer(tunnel.ServerConfig{
		Token: "tok", HostKey: key, HeartbeatInterval: 50 * time.Millisecond, Timeout: time.Second, Logf: func(string, ...interface{}) {},
		CookieSecret: func() []byte { return []byte("c") }, Secret: func() string { return "" }, AuthToken: func() string { return "" },
		Store: func(w *tunnel.Worker, have int64) *tunnel.StoreUpdate { return storeUpdateFor(gw, w.ID(), have) },
		OnStoreState: func(w *tunnel.Worker, raw json.RawMessage) {
			storeStateFrom(w.ID(), raw)
			var st appstore.NodeState
			if json.Unmarshal(raw, &st) == nil {
				hmu.Lock()
				history = append(history, st)
				hmu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/tunnel", srv)
	hs := httptest.NewServer(mux)
	defer func() { srv.Close(); hs.Close() }()

	var fmu sync.Mutex
	ws := appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: flakyInstaller{mu: &fmu, failed: map[string]bool{}}})
	server := &Server{options: &Options{}}
	client, err := tunnel.NewClient(tunnel.ClientConfig{
		ServerURL: "ws" + strings.TrimPrefix(hs.URL, "http") + "/api/tunnel", Token: "tok",
		Register: tunnel.RegisterRequest{WorkerID: "w1", Capacity: 100, Weight: 10}, Load: func() tunnel.Heartbeat { return tunnel.Heartbeat{} },
		OnStore: server.workerStoreUpdate, OnRegistered: func(tunnel.RegisterReply) { storeResend() },
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, Logf: func(string, ...interface{}) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.startWorkerStore(ctx, ws, client)
	go client.Run(ctx)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		appStoreMu.Lock()
		n := appNodes["w1"]
		appStoreMu.Unlock()
		if len(n.Have) == 1 && n.Have[0] == "flask-1.0.0" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	hmu.Lock()
	defer hmu.Unlock()
	sawFailure := false
	for _, st := range history {
		if st.Failed["flask-1.0.0"] != "" && st.Retry["flask-1.0.0"] > 0 {
			sawFailure = true
		}
	}
	last := history[len(history)-1]
	if !sawFailure || len(last.Have) != 1 || len(last.Failed) != 0 {
		t.Fatalf("the worker did not fail once and then get there by itself (saw a failure: %v); last state %+v of %d", sawFailure, last, len(history))
	}
	if !ws.Store().HasBundle("flask-1.0.0") {
		t.Error("the bundle is not on the worker")
	}
}

// An admin installs an app for everyone and turns it on for every signed-in user in the same step; and can change that later.
func TestAnAdminInstallsForEveryoneAndTurnsItOnForEveryone(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{})
	srv := &Server{options: &Options{}}
	post := func(body string) int {
		w := httptest.NewRecorder()
		srv.handleAdminStore(w, httptest.NewRequest("POST", "http://openrepl.test/admin/store", strings.NewReader(body)))
		return w.Code
	}
	if c := post(`{"action":"install","name":"flask","kind":"lib","everyone":true}`); c != http.StatusOK {
		t.Fatalf("install: %d", c)
	}
	svc.Wait()
	if d := svc.Defaults(); len(d) != 1 || d[0] != "flask" || len(svc.MyList("zed")) != 1 {
		t.Fatalf("defaults %v, zed %v", d, svc.MyList("zed"))
	}
	w := httptest.NewRecorder()
	srv.handleAdminStore(w, httptest.NewRequest("GET", "http://openrepl.test/admin/store", nil))
	var out struct {
		Defaults []string `json:"defaults"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Defaults) != 1 {
		t.Fatalf("the admin list: %s", w.Body.String())
	}
	if c := post(`{"action":"default","name":"flask","on":false}`); c != http.StatusOK || len(svc.Defaults()) != 0 || len(svc.MyList("zed")) != 0 {
		t.Fatalf("off: %d %v", c, svc.Defaults())
	}
	if c := post(`{"action":"default","name":"flask","on":true}`); c != http.StatusOK || len(svc.MyList("zed")) != 1 {
		t.Fatalf("on again: %d", c)
	}
	if c := post(`{"action":"default","name":"nothing","on":true}`); c == http.StatusOK {
		t.Error("an app that does not exist cannot be turned on")
	}
}

// Switching an app on runs its check for the user on the node of their terminals: here (no worker holds their workspace), and on a
// worker through the tunnel, which needs the tunnel AND the worker token. A failure is kept so that the user can report it.
func TestAnAppIsCheckedForTheUserBeforeItIsOnAndAFailureCanBeReported(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{}, "tool:zzcli")
	svc.Install("ann", "zzcli", "tool", false)
	svc.Wait()
	srv := &Server{options: &Options{WorkerToken: "s3cret"}}
	app, _ := svc.AppByID("zzcli-1.0.0")
	if _, wait, err := srv.verifyOnNode(context.Background(), svc, "ann", app); err != nil || wait {
		t.Fatalf("a working tool: %v %v", err, wait)
	}
	os.WriteFile(filepath.Join(svc.Store().BundlePath("zzcli-1.0.0"), "bin", "zzcli"), []byte("#!/bin/sh\necho 'libonig missing'\nexit 3\n"), 0755)
	node, wait, err := srv.verifyOnNode(context.Background(), svc, "ann", app)
	if err == nil || wait || node != "gateway" || !strings.Contains(err.Error(), "libonig missing") {
		t.Fatalf("a tool that does not start: %q %v %v", node, wait, err)
	}
	ghost := app
	ghost.ID = "nothing-1.0.0"
	if _, wait, err := srv.verifyOnNode(context.Background(), svc, "ann", ghost); err == nil || !wait {
		t.Errorf("a node that does not have it yet says to wait: %v %v", wait, err)
	}
	// the failed check goes to the admin's list by itself, with the node and the log; a user who then reports it is added to the same entry
	if e := svc.NoteCheckFailure("zzcli", node, err.Error()); e != nil {
		t.Fatal(e)
	}
	if e := svc.ReportUse("uid-1", "zzcli", "", "Turning it on", "", "it does not start for me"); e != nil {
		t.Fatal(e)
	}
	reps := svc.Reports()
	if len(reps) != 1 || reps[0].Step != "Turning it on" || !strings.Contains(reps[0].Log, "libonig missing") || reps[0].Note != "it does not start for me" || reps[0].Node != "gateway" || reps[0].Fails != 1 || reps[0].Auto || len(reps[0].Reporters) != 1 {
		t.Fatalf("the report: %+v", reps)
	}

	// the worker's side: through the tunnel and with the token
	hs := httptest.NewUnstartedServer(http.HandlerFunc(srv.handleStoreVerify))
	hs.Config.ConnContext = trustTunnel
	hs.Start()
	defer hs.Close()
	dial := func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", hs.Listener.Addr().String()) }
	rep, derr := callWorkerVerify(context.Background(), "s3cret", dial, verifyRequest{Home: "ann", ID: "zzcli-1.0.0", Name: "zzcli", Kind: "tool"})
	if derr != nil || rep.OK || !strings.Contains(rep.Error, "libonig missing") {
		t.Fatalf("the worker answer: %+v %v", rep, derr)
	}
	if _, derr := callWorkerVerify(context.Background(), "wrong", dial, verifyRequest{Home: "ann", ID: "zzcli-1.0.0", Name: "zzcli", Kind: "tool"}); derr == nil {
		t.Error("a wrong token must be refused")
	}
	plain := httptest.NewServer(http.HandlerFunc(srv.handleStoreVerify)) // a request that did not come through the tunnel
	defer plain.Close()
	if _, derr := callWorkerVerify(context.Background(), "s3cret", func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", plain.Listener.Addr().String()) },
		verifyRequest{Home: "ann", ID: "zzcli-1.0.0", Name: "zzcli", Kind: "tool"}); derr == nil {
		t.Error("a request outside the tunnel must be refused")
	}
}
