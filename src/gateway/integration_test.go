package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"trusted"
	"tunnel"
)

// cluster is a gateway with its tunnel endpoint, served over real HTTP.
type cluster struct {
	t      *testing.T
	router *Router
	tunnel *tunnel.Server
	http   *httptest.Server
	local  *recorder
}

// newCluster starts a gateway whose own backend has the given weight; 0
// makes it routing-only, so every session runs on a worker.
func newCluster(t *testing.T, localWeight int) *cluster {
	t.Helper()
	local := &recorder{}
	rt := NewRouter(Config{
		Site:        local,
		PathPrefix:  "/",
		IsEntryPage: func(rel string) bool { return rel == "" },
		Secret:      []byte("secret"),
		GuestTTL:    time.Hour,
		Local:       LocalConfig{Weight: localWeight},
		Identity: func(w http.ResponseWriter, r *http.Request, ec ExecutionContext) trusted.Identity {
			return trusted.Identity{UID: ec.UID, Guest: ec.GuestID, Session: ec.Key, Privilege: "guest"}
		},
	})
	key, err := tunnel.NewEphemeralHostKey()
	if err != nil {
		t.Fatal(err)
	}
	tcfg := tunnel.ServerConfig{
		Token:             "tok",
		HostKey:           key,
		HeartbeatInterval: 100 * time.Millisecond,
		Timeout:           5 * time.Second,
		Logf:              func(string, ...interface{}) {},
	}
	rt.BindTunnel(&tcfg)
	ts, err := tunnel.NewServer(tcfg)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetTunnel("/api/tunnel", ts)
	hs := httptest.NewServer(rt)
	t.Cleanup(func() { ts.Close(); hs.Close() })
	return &cluster{t: t, router: rt, tunnel: ts, http: hs, local: local}
}

// testWorker is a worker process: a tunnel client plus the handlers a real
// worker would run. Its replies name the worker and the identity it was told.
type testWorker struct {
	id     string
	client *tunnel.Client
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *cluster) startWorker(id string) *testWorker {
	return c.startWorkerWith(id, 10)
}

func (c *cluster) startWorkerWith(id string, weight int) *testWorker {
	c.t.Helper()
	client, err := tunnel.NewClient(tunnel.ClientConfig{
		ServerURL:  "ws" + strings.TrimPrefix(c.http.URL, "http") + "/api/tunnel",
		Token:      "tok",
		Register:   tunnel.RegisterRequest{WorkerID: id, Capacity: 100000, Weight: weight},
		MinBackoff: 10 * time.Millisecond,
		MaxBackoff: 50 * time.Millisecond,
		Logf:       func(string, ...interface{}) {},
	})
	if err != nil {
		c.t.Fatal(err)
	}
	up := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := trusted.FromHeader(r.Header)
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				typ, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				conn.WriteMessage(typ, []byte(id+":"+string(msg)))
			}
		}
		fmt.Fprintf(w, "%s|%s|%s", id, who.Session, r.URL.Path)
	})
	ctx, cancel := context.WithCancel(context.Background())
	w := &testWorker{id: id, client: client, cancel: cancel, done: make(chan struct{})}
	srv := &http.Server{Handler: handler}
	go srv.Serve(client.Listener())
	go func() {
		client.Run(ctx)
		srv.Close()
		close(w.done)
	}()
	c.t.Cleanup(w.stop)
	c.waitState(id, Online)
	for deadline := time.Now().Add(5 * time.Second); !client.Connected() && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	return w
}

func (w *testWorker) stop() {
	w.cancel()
	<-w.done
}

// waitState waits until the worker is a backend in the given state; Offline
// also matches a worker that is no longer a backend at all.
func (c *cluster) waitState(id string, want State) {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, ok := c.router.Backend(id)
		if (ok && b.State() == want) || (!ok && want == Offline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.t.Fatalf("worker %s did not become %v", id, want)
}

// browser is an HTTP client with its own cookie jar: one guest session.
type browser struct {
	c      *cluster
	client *http.Client
}

func (c *cluster) newBrowser() *browser {
	jar, _ := cookiejar.New(nil)
	b := &browser{c: c, client: &http.Client{Jar: jar}}
	// Loading the page is what issues the guest id.
	b.get("/")
	return b
}

func (b *browser) get(path string) (int, string) {
	b.c.t.Helper()
	resp, err := b.client.Get(b.c.http.URL + path)
	if err != nil {
		b.c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// servedBy returns the worker id that answered, or "local".
func (b *browser) servedBy(path string) string {
	b.c.t.Helper()
	code, body := b.get(path)
	if code != http.StatusOK {
		return fmt.Sprintf("HTTP %d", code)
	}
	if body == "" {
		return LocalID // the recorder site writes no body
	}
	return strings.SplitN(body, "|", 2)[0]
}

func (b *browser) terminal(path string) (*websocket.Conn, error) {
	u := "ws" + strings.TrimPrefix(b.c.http.URL, "http") + path
	hdr := http.Header{}
	req, _ := http.NewRequest("GET", b.c.http.URL+path, nil)
	for _, ck := range b.client.Jar.Cookies(req.URL) {
		hdr.Add("Cookie", ck.Name+"="+ck.Value)
	}
	conn, _, err := (&websocket.Dialer{}).Dial(u, hdr)
	return conn, err
}

func TestClusterAffinityAcrossTerminalAndFiles(t *testing.T) {
	c := newCluster(t, 0)
	c.startWorker("worker-1")
	b := c.newBrowser()

	// Every execution-bound request of one session reaches the same worker.
	for _, p := range []string{"/ws_filebrowser?q=usage", "/upload_file", "/ws_filebrowser"} {
		if got := b.servedBy(p); got != "worker-1" {
			t.Fatalf("%s served by %s", p, got)
		}
	}
	conn, err := b.terminal("/ws_python")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.WriteMessage(websocket.TextMessage, []byte("hi"))
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "worker-1:hi" {
		t.Fatalf("terminal reply %q, %v", msg, err)
	}

	// The worker is told which session the request belongs to.
	_, body := b.get("/ws_filebrowser")
	parts := strings.Split(body, "|")
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "g:") || parts[2] != "/ws_filebrowser" {
		t.Fatalf("worker reply %q", body)
	}

	// Admin and site routes never leave the gateway.
	before := c.local.served()
	b.get("/admin/workers")
	b.get("/login")
	if c.local.served() != before+2 {
		t.Fatalf("admin/site routes were not served locally")
	}
}

func TestClusterWorkerLossAndReturn(t *testing.T) {
	c := newCluster(t, 0)
	w1 := c.startWorker("worker-1")
	owner := c.newBrowser()
	if got := owner.servedBy("/ws_filebrowser"); got != "worker-1" {
		t.Fatalf("served by %s", got)
	}

	// The worker goes away.
	w1.stop()
	c.waitState("worker-1", Offline)

	// Its session fails; it is not moved to another node.
	if code, _ := owner.get("/ws_filebrowser"); code != http.StatusServiceUnavailable {
		t.Fatalf("session on a lost worker -> HTTP %d, want 503", code)
	}
	// With no node left a new visitor still gets the page, and a clear error
	// from the terminal.
	fresh := c.newBrowser()
	if code, _ := fresh.get("/"); code != http.StatusOK {
		t.Fatalf("page with no node online -> HTTP %d", code)
	}
	if code, body := fresh.get("/ws_filebrowser"); code != http.StatusServiceUnavailable || !strings.Contains(body, "no execution node") {
		t.Fatalf("new session with no node online -> HTTP %d %q", code, body)
	}
	// Admin keeps working with no worker online.
	if code, _ := owner.get("/admin/workers"); code != http.StatusOK {
		t.Fatalf("admin -> HTTP %d", code)
	}

	// The worker returns: it takes new sessions again and the old session,
	// whose workspace is still on it, works again.
	c.startWorker("worker-1")
	if got := c.newBrowser().servedBy("/ws_filebrowser"); got != "worker-1" {
		t.Fatalf("after reconnect new session served by %s", got)
	}
	if got := owner.servedBy("/ws_filebrowser"); got != "worker-1" {
		t.Fatalf("after reconnect old session served by %s", got)
	}
}

func TestClusterSpreadsSessionsAndAvoidsALostWorker(t *testing.T) {
	c := newCluster(t, 0)
	c.startWorker("worker-1")
	w2 := c.startWorker("worker-2")

	// Sessions are spread over both workers, and each one stays put.
	count := map[string]int{}
	var browsers []*browser
	for i := 0; i < 60; i++ {
		b := c.newBrowser()
		home := b.servedBy("/ws_filebrowser")
		count[home]++
		for j := 0; j < 3; j++ {
			if got := b.servedBy("/upload_file"); got != home {
				t.Fatalf("session moved from %s to %s", home, got)
			}
		}
		browsers = append(browsers, b)
	}
	if count["worker-1"] < 10 || count["worker-2"] < 10 || count["worker-1"]+count["worker-2"] != 60 {
		t.Fatalf("sessions were not spread over both workers: %v", count)
	}

	// One worker is lost. New sessions all go to the other.
	w2.stop()
	c.waitState("worker-2", Offline)
	for i := 0; i < 20; i++ {
		if got := c.newBrowser().servedBy("/ws_filebrowser"); got != "worker-1" {
			t.Fatalf("new session served by %s while worker-2 is offline", got)
		}
	}

	// It reconnects and is chosen again.
	c.startWorker("worker-2")
	again := 0
	for i := 0; i < 40; i++ {
		if c.newBrowser().servedBy("/ws_filebrowser") == "worker-2" {
			again++
		}
	}
	if again == 0 {
		t.Fatal("the reconnected worker received no new session")
	}
}

func TestClusterGatewaySharesLoadByWeight(t *testing.T) {
	c := newCluster(t, 10)            // the gateway takes sessions too
	c.startWorkerWith("worker-1", 30) // three times the gateway's share
	count := map[string]int{}
	for i := 0; i < 200; i++ {
		count[c.newBrowser().servedBy("/ws_filebrowser")]++
	}
	// Expect about 50 local and 150 on the worker.
	if count[LocalID] < 25 || count[LocalID] > 80 || count["worker-1"] < 120 {
		t.Fatalf("split = %v, want about 50 local / 150 worker-1", count)
	}
}

func TestClusterCrossSessionRoutes(t *testing.T) {
	c := newCluster(t, 0)
	workers := map[string]*testWorker{
		"worker-1": c.startWorker("worker-1"),
		"worker-2": c.startWorker("worker-2"),
	}

	owner := c.newBrowser()
	home := owner.servedBy("/ws_filebrowser")
	other := "worker-1"
	if home == "worker-1" {
		other = "worker-2"
	}

	// The other worker owns a process and a workspace this session links to.
	w := workers[other]
	w.client.RouteOpen(tunnel.RouteJID, "JID2")
	w.client.RouteOpen(tunnel.RouteHome, "/tmp/home/shared")
	if got := owner.servedBy("/ws_filebrowser?jid=JID2"); got != other {
		t.Fatalf("fork link served by %s, want %s", got, other)
	}
	if got := owner.servedBy("/ws_filebrowser?q=load&homedir=/tmp/home/shared"); got != other {
		t.Fatalf("shared workspace served by %s, want %s", got, other)
	}
	conn, err := owner.terminal("/ws_python?jid=JID2")
	if err != nil {
		t.Fatal(err)
	}
	conn.WriteMessage(websocket.TextMessage, []byte("fork"))
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != other+":fork" {
		t.Fatalf("fork terminal reply %q, %v", msg, err)
	}
	conn.Close()

	// Without the key the session is back on its own worker.
	if got := owner.servedBy("/ws_filebrowser"); got != home {
		t.Fatalf("served by %s, want %s", got, home)
	}

	// Withdrawn keys stop routing.
	w.client.RouteClose(tunnel.RouteJID, "JID2")
	if got := owner.servedBy("/ws_filebrowser?jid=JID2"); got != home {
		t.Fatalf("withdrawn jid still routed to %s", got)
	}
}

func TestClusterManyConcurrentSessions(t *testing.T) {
	c := newCluster(t, 0)
	c.startWorker("worker-1")
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := c.newBrowser()
			conn, err := b.terminal("/ws_python")
			if err != nil {
				errs <- err.Error()
				return
			}
			defer conn.Close()
			for j := 0; j < 20; j++ {
				msg := fmt.Sprintf("s%d-m%d", i, j)
				conn.WriteMessage(websocket.TextMessage, []byte(msg))
				_, reply, err := conn.ReadMessage()
				if err != nil || string(reply) != "worker-1:"+msg {
					errs <- fmt.Sprintf("session %d: reply %q, %v", i, reply, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// admin calls the gateway's admin API the way the server mounts it.
func (c *cluster) admin(method, path string) (int, string) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	c.router.AdminHandler(c.tunnel).ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (c *cluster) workers() map[string]WorkerInfo {
	c.t.Helper()
	code, body := c.admin("GET", "/admin/workers")
	if code != http.StatusOK {
		c.t.Fatalf("GET /admin/workers -> %d %s", code, body)
	}
	var out struct{ Workers []WorkerInfo }
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		c.t.Fatal(err)
	}
	m := map[string]WorkerInfo{}
	for _, w := range out.Workers {
		m[w.ID] = w
	}
	return m
}

func TestClusterDrainThroughAdminAPI(t *testing.T) {
	c := newCluster(t, 0)
	c.startWorker("worker-1")
	c.startWorker("worker-2")

	// A session that lands on worker-2 before it is drained.
	var owner *browser
	for owner == nil {
		if b := c.newBrowser(); b.servedBy("/ws_filebrowser") == "worker-2" {
			owner = b
		}
	}

	ws := c.workers()
	if len(ws) != 3 || ws["local"].Weight != 0 || ws["worker-2"].State != "ONLINE" || ws["worker-2"].MaxMB != 100000 {
		t.Fatalf("workers = %+v", ws)
	}
	if ws["worker-2"].Sessions == 0 || ws["worker-2"].RemoteAddr == "" || ws["worker-2"].LastSeen == "" {
		t.Fatalf("worker-2 row = %+v", ws["worker-2"])
	}

	if code, body := c.admin("POST", "/admin/workers/worker-2/drain"); code != http.StatusOK || !strings.Contains(body, "DRAINING") {
		t.Fatalf("drain -> %d %s", code, body)
	}
	if got := c.workers()["worker-2"].State; got != "DRAINING" {
		t.Fatalf("state = %s", got)
	}
	// No new session reaches the draining worker; its existing one carries on.
	for i := 0; i < 30; i++ {
		if got := c.newBrowser().servedBy("/ws_filebrowser"); got != "worker-1" {
			t.Fatalf("new session served by %s while worker-2 is draining", got)
		}
	}
	if got := owner.servedBy("/ws_filebrowser"); got != "worker-2" {
		t.Fatalf("existing session on the draining worker served by %s", got)
	}

	if code, _ := c.admin("POST", "/admin/workers/worker-2/undrain"); code != http.StatusOK {
		t.Fatalf("undrain -> %d", code)
	}
	back := false
	for i := 0; i < 40 && !back; i++ {
		back = c.newBrowser().servedBy("/ws_filebrowser") == "worker-2"
	}
	if !back {
		t.Fatal("worker-2 received no session after undrain")
	}
}

func TestAdminAPIErrorsAndSessions(t *testing.T) {
	c := newCluster(t, 0)
	c.startWorker("worker-1")
	c.newBrowser().servedBy("/ws_filebrowser")

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/admin/workers", http.StatusMethodNotAllowed},
		{"GET", "/admin/workers/worker-1/drain", http.StatusMethodNotAllowed},
		{"POST", "/admin/workers/nobody/drain", http.StatusNotFound},
		{"POST", "/admin/workers/local/drain", http.StatusBadRequest},
		{"GET", "/admin/unknown", http.StatusNotFound},
		{"POST", "/admin/sessions", http.StatusMethodNotAllowed},
	} {
		if code, _ := c.admin(tc.method, tc.path); code != tc.want {
			t.Errorf("%s %s -> %d, want %d", tc.method, tc.path, code, tc.want)
		}
	}

	code, body := c.admin("GET", "/admin/sessions")
	var out struct{ Sessions []SessionInfo }
	if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
		t.Fatalf("sessions -> %d %v", code, err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].Backend != "worker-1" || !strings.HasPrefix(out.Sessions[0].Key, "g:") || out.Sessions[0].Expires == "" {
		t.Fatalf("sessions = %+v", out.Sessions)
	}

	// With workers disabled the API still lists the local backend.
	solo := newTestRouter(&recorder{}, "/", nil)
	rec := httptest.NewRecorder()
	solo.AdminHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workers", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"local"`) {
		t.Fatalf("workers without a tunnel -> %d %s", rec.Code, rec.Body.String())
	}
}
