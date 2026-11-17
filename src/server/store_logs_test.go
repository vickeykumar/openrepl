package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"appstore"
	"tunnel"
)

// A worker's log is asked for over the tunnel: the request waits for the worker's next heartbeat, which takes it, and the answer comes back by id.
func TestAnAdminAsksAWorkerForAnInstallLog(t *testing.T) {
	oldWait := logWait
	logWait = 3 * time.Second
	defer func() { logWait = oldWait }()
	appStoreMu.Lock()
	appNodes["pi-1"] = appNode{Seen: time.Now()}
	appStoreMu.Unlock()
	defer func() { appStoreMu.Lock(); delete(appNodes, "pi-1"); appStoreMu.Unlock() }()

	type res struct {
		raw json.RawMessage
		err error
	}
	got := make(chan res, 1)
	go func() {
		raw, err := askWorkerLog(context.Background(), "pi-1", "git", 0, 0)
		got <- res{raw, err}
	}()
	// the worker's heartbeat takes the request
	var u *tunnel.StoreUpdate
	for i := 0; i < 100 && u == nil; i++ {
		time.Sleep(20 * time.Millisecond)
		u = storeUpdateFor(appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: appstore.Fake{}, Safe: func() []string { return nil }}), "pi-1", 0)
	}
	if u == nil || len(u.Logs) != 1 || u.Logs[0].App != "git" || u.Logs[0].ID == "" {
		t.Fatalf("the heartbeat reply did not carry the request: %+v", u)
	}
	// the worker answers
	chunk, _ := json.Marshal(map[string]interface{}{"id": u.Logs[0].ID, "chunk": appstore.LogChunk{Found: true, Next: 42, Lines: []appstore.LogLine{{Step: "start", Level: "info", Text: "hello"}}}})
	(&Server{}).gatewayStoreLog(nil, chunk)
	r := <-got
	if r.err != nil {
		t.Fatal(r.err)
	}
	var ch appstore.LogChunk
	if json.Unmarshal(r.raw, &ch) != nil || ch.Next != 42 || len(ch.Lines) != 1 || ch.Lines[0].Text != "hello" {
		t.Fatalf("the chunk: %s", r.raw)
	}
	// a server that does not answer: an error, and the request is not left behind
	if _, err := askWorkerLog(context.Background(), "pi-1", "git", 0, 0); err == nil {
		t.Fatal("a worker that does not answer must give an error")
	}
	if _, err := askWorkerLog(context.Background(), "pi-9", "git", 0, 0); err == nil {
		t.Fatal("an unknown server must give an error")
	}
	// a heartbeat with nothing to carry and the catalog up to date gets nothing
	appLog.Lock()
	appLog.reqs["pi-1"] = nil
	appLog.Unlock()
}

// The admin route reads the gateway's own log, refuses what is not a name, and is admin-only like the rest of /admin (the router wraps it).
func TestTheAdminLogRouteReadsTheGatewaysLog(t *testing.T) {
	svc := appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: appstore.Fake{}, Safe: func() []string { return []string{"flask"} }})
	svc.Install("ann", "flask", appstore.KindLib, false)
	svc.Wait()
	appStoreMu.Lock()
	old := appStoreSvc
	appStoreSvc = svc
	appStoreMu.Unlock()
	defer func() { appStoreMu.Lock(); appStoreSvc = old; appStoreMu.Unlock() }()
	srv := &Server{}
	w := httptest.NewRecorder()
	srv.handleAdminStoreLog(w, httptest.NewRequest("GET", "/admin/store/log?app=flask&node=gateway&attempt=0&from=0", nil))
	var out struct {
		Node  string            `json:"node"`
		Chunk appstore.LogChunk `json:"chunk"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Node != "gateway" || !out.Chunk.Found || len(out.Chunk.Lines) == 0 || len(out.Chunk.Attempts) != 1 {
		t.Fatalf("the gateway's log: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.handleAdminStoreLog(w, httptest.NewRequest("GET", "/admin/store/log?app=../etc&node=gateway", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a name that is not an app: %d", w.Code)
	}
	w = httptest.NewRecorder()
	srv.handleAdminStoreLog(w, httptest.NewRequest("POST", "/admin/store/log?app=flask", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("a POST: %d", w.Code)
	}
}

// A user is told how far the other servers are, never which ones.
func TestAUserIsToldHowManyServersHaveAnApp(t *testing.T) {
	appStoreMu.Lock()
	appNodes["pi-a"] = appNode{NodeState: appstore.NodeState{Have: []string{"x-1"}}}
	appNodes["pi-b"] = appNode{NodeState: appstore.NodeState{Failed: map[string]string{"x-1": "no build"}}}
	appNodes["pi-c"] = appNode{}
	appStoreMu.Unlock()
	defer func() {
		appStoreMu.Lock()
		delete(appNodes, "pi-a")
		delete(appNodes, "pi-b")
		delete(appNodes, "pi-c")
		appStoreMu.Unlock()
	}()
	if total, ready, failed := userServers("x-1"); total != 3 || ready != 1 || failed != 1 {
		t.Errorf("total %d ready %d failed %d", total, ready, failed)
	}
	svc := appstore.New(appstore.Config{Store: appstore.Store{Dir: t.TempDir()}, KV: &memKV{}, Installer: appstore.Fake{}, Safe: func() []string { return nil }})
	if out := userLogResult(svc, "no-such-app", 0); len(out["lines"].([]appstore.UserLine)) != 0 || out["servers"] != nil {
		t.Errorf("an app that is not installing gives nothing: %v", out)
	}
}
