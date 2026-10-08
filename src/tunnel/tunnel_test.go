package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

const testToken = "test-token"

// events records the callbacks a Server makes, in order.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	e.log = append(e.log, s)
	e.mu.Unlock()
}

func (e *events) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

func (e *events) has(s string) bool {
	for _, v := range e.snapshot() {
		if v == s {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type gateway struct {
	srv  *Server
	http *httptest.Server
	ev   *events
	url  string // ws://.../api/tunnel
}

func newGateway(t *testing.T, mod func(*ServerConfig)) *gateway {
	t.Helper()
	key, err := NewEphemeralHostKey()
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	cfg := ServerConfig{
		Token:             testToken,
		HostKey:           key,
		CookieSecret:      func() []byte { return []byte("cookie-secret") },
		Secret:            func() string { return "server-secret" },
		AuthToken:         func() string { return "auth-token" },
		HeartbeatInterval: 50 * time.Millisecond,
		Timeout:           time.Second,
		OnOnline:          func(w *Worker) { ev.add("online:" + w.ID()) },
		OnOffline:         func(w *Worker) { ev.add("offline:" + w.ID()) },
		OnRoute: func(w *Worker, r RouteEvent, open bool) {
			if open {
				ev.add("open:" + w.ID() + ":" + r.Kind + ":" + r.Key)
			} else {
				ev.add("close:" + w.ID() + ":" + r.Kind + ":" + r.Key)
			}
		},
		Logf: func(string, ...interface{}) {},
	}
	if mod != nil {
		mod(&cfg)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/tunnel", srv)
	hs := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close(); hs.Close() })
	return &gateway{srv: srv, http: hs, ev: ev, url: "ws" + strings.TrimPrefix(hs.URL, "http") + "/api/tunnel"}
}

type worker struct {
	client *Client
	cancel context.CancelFunc
	reply  chan RegisterReply
	done   chan struct{}
}

// newWorker starts a client that serves handler on the streams it is given.
func newWorker(t *testing.T, url, id string, handler http.Handler, mod func(*ClientConfig)) *worker {
	t.Helper()
	w := &worker{reply: make(chan RegisterReply, 16), done: make(chan struct{})}
	cfg := ClientConfig{
		ServerURL:    url,
		Token:        testToken,
		Register:     RegisterRequest{WorkerID: id, Capacity: 100, Weight: 10},
		Load:         func() Heartbeat { return Heartbeat{Used: 7, Active: 2} },
		OnRegistered: func(r RegisterReply) { w.reply <- r },
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		Logf:         func(string, ...interface{}) {},
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.client = c
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	hs := &http.Server{Handler: handler}
	go hs.Serve(c.Listener())
	go func() {
		c.Run(ctx)
		hs.Close()
		close(w.done)
	}()
	t.Cleanup(func() { cancel(); <-w.done })
	return w
}

// via returns an HTTP client whose connections are streams to the worker.
func via(w *Worker) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return w.Dial(ctx) },
	}}
}

func TestRegisterDeliversSecretsAndProxiesHTTP(t *testing.T) {
	gw := newGateway(t, nil)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Cookie"))
		w.Write(bytes.ToUpper(body))
	})
	wk := newWorker(t, gw.url, "worker-1", handler, nil)

	rep := <-wk.reply
	if string(rep.CookieSecret) != "cookie-secret" || rep.AuthToken != "auth-token" || rep.Secret != "server-secret" || rep.ConnectionID == "" {
		t.Fatalf("unexpected reply: %+v", rep)
	}
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	w := gw.srv.Worker("worker-1")
	if info := w.Info(); info.Capacity != 100 || info.Weight != 10 {
		t.Fatalf("info = %+v", info)
	}

	req, _ := http.NewRequest("POST", "http://worker/upload_file?x=1&y=2", strings.NewReader("hello"))
	req.Header.Set("Cookie", "user-session=abc")
	resp, err := via(w).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	if string(body) != "HELLO" {
		t.Fatalf("body = %q", body)
	}
	if got := resp.Header.Get("X-Seen"); got != "POST /upload_file?x=1&y=2 user-session=abc" {
		t.Fatalf("worker saw %q", got)
	}
	if !gw.ev.has("online:worker-1") {
		t.Fatalf("events = %v", gw.ev.snapshot())
	}
}

func TestManyStreamsShareOneConnection(t *testing.T) {
	// Production timings: the short test heartbeat would count a busy link as
	// a dead one.
	gw := newGateway(t, func(c *ServerConfig) {
		c.HeartbeatInterval = time.Second
		c.Timeout = 30 * time.Second
	})
	// Read the whole body first: an HTTP/1 server cannot reliably read the
	// request while it is already writing the response.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(body)
	})
	newWorker(t, gw.url, "worker-1", handler, nil)
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	w := gw.srv.Worker("worker-1")

	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1 MiB
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A fresh transport per request, so each one is its own stream.
			resp, err := via(w).Post("http://worker/echo", "application/octet-stream", bytes.NewReader(payload))
			if err != nil {
				errs <- err.Error()
				return
			}
			defer resp.Body.Close()
			got, err := ioutil.ReadAll(resp.Body)
			if err != nil || !bytes.Equal(got, payload) {
				errs <- fmt.Sprintf("status %d, read %d of %d bytes, err %v", resp.StatusCode, len(got), len(payload), err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if n := len(gw.srv.Workers()); n != 1 {
		t.Fatalf("workers = %d", n)
	}
}

func TestEndpointRejectsBadTokenAndBrowsers(t *testing.T) {
	gw := newGateway(t, nil)
	httpURL := strings.Replace(gw.url, "ws://", "http://", 1)

	get := func(mod func(*http.Request)) int {
		req, _ := http.NewRequest("GET", httpURL, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		mod(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := get(func(r *http.Request) {}); code != http.StatusUnauthorized {
		t.Fatalf("no token -> %d", code)
	}
	if code := get(func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); code != http.StatusUnauthorized {
		t.Fatalf("wrong token -> %d", code)
	}
	if code := get(func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Origin", "https://evil.example")
	}); code != http.StatusForbidden {
		t.Fatalf("browser origin -> %d", code)
	}
	if len(gw.srv.Workers()) != 0 {
		t.Fatal("a worker registered")
	}

	// After enough failures the address is refused even with the right token.
	for i := 0; i < maxAuthFailures; i++ {
		get(func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") })
	}
	if code := get(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken) }); code != http.StatusTooManyRequests {
		t.Fatalf("after repeated failures -> %d", code)
	}
}

func TestWrongTokenNeverRegisters(t *testing.T) {
	gw := newGateway(t, nil)
	newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) { c.Token = "wrong" })
	time.Sleep(200 * time.Millisecond)
	if len(gw.srv.Workers()) != 0 {
		t.Fatal("worker with a wrong token registered")
	}
}

func TestHeartbeatReportsLoad(t *testing.T) {
	gw := newGateway(t, nil)
	newWorker(t, gw.url, "worker-1", nil, nil)
	waitFor(t, "heartbeat", func() bool {
		w := gw.srv.Worker("worker-1")
		return w != nil && w.Used() == 7 && w.Active() == 2
	})
}

// rawWorker registers without ever sending a heartbeat.
func rawWorker(t *testing.T, gw *gateway, reg RegisterRequest) (ssh.Conn, bool, string) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testToken)
	ws, _, err := (&websocket.Dialer{Subprotocols: []string{Subprotocol}}).Dial(gw.url, h)
	if err != nil {
		t.Fatal(err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(newWSConn(ws), "gateway", &ssh.ClientConfig{
		User:            reg.WorkerID,
		Auth:            []ssh.AuthMethod{ssh.Password(testToken)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			nc.Reject(ssh.Prohibited, "test")
		}
	}()
	payload, _ := json.Marshal(reg)
	ok, data, err := conn.SendRequest(ReqRegister, true, payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, ok, string(data)
}

func TestSilentWorkerTimesOut(t *testing.T) {
	gw := newGateway(t, func(c *ServerConfig) {
		c.HeartbeatInterval = 20 * time.Millisecond
		c.Timeout = 100 * time.Millisecond
	})
	_, ok, msg := rawWorker(t, gw, RegisterRequest{WorkerID: "quiet", ProtocolVersion: ProtocolVersion})
	if !ok {
		t.Fatalf("registration refused: %s", msg)
	}
	waitFor(t, "timeout", func() bool { return gw.ev.has("offline:quiet") })
	if gw.srv.Worker("quiet") != nil {
		t.Fatal("timed-out worker is still registered")
	}
}

func TestRegistrationIsValidated(t *testing.T) {
	gw := newGateway(t, nil)
	cases := []RegisterRequest{
		{WorkerID: "w1", ProtocolVersion: ProtocolVersion + 1},
		{WorkerID: "local", ProtocolVersion: ProtocolVersion},
		{WorkerID: "bad id!", ProtocolVersion: ProtocolVersion},
	}
	for _, reg := range cases {
		if _, ok, _ := rawWorker(t, gw, reg); ok {
			t.Errorf("registration %+v was accepted", reg)
		}
	}
	if len(gw.srv.Workers()) != 0 {
		t.Fatalf("workers = %d", len(gw.srv.Workers()))
	}
}

func TestReconnectReregistersAndReannouncesRoutes(t *testing.T) {
	gw := newGateway(t, nil)
	wk := newWorker(t, gw.url, "worker-1", nil, nil)
	// Connected is what a handler sees: streams are served only after the
	// client has finished registering.
	waitFor(t, "registration", func() bool { return wk.client.Connected() })

	wk.client.RouteOpen(RouteJID, "abc")
	if !gw.ev.has("open:worker-1:jid:abc") {
		t.Fatalf("RouteOpen returned before the gateway knew the route: %v", gw.ev.snapshot())
	}
	first := gw.srv.Worker("worker-1")

	first.conn.Close() // the link drops
	waitFor(t, "offline", func() bool { return gw.ev.has("offline:worker-1") })
	waitFor(t, "reconnect", func() bool {
		w := gw.srv.Worker("worker-1")
		return w != nil && w.ConnectionID() != first.ConnectionID()
	})
	if first.Online() {
		t.Fatal("old connection still reports online")
	}
	if _, err := first.Dial(context.Background()); err == nil {
		t.Fatal("Dial on an offline worker succeeded")
	}
	waitFor(t, "route re-announced", func() bool {
		n := 0
		for _, e := range gw.ev.snapshot() {
			if e == "open:worker-1:jid:abc" {
				n++
			}
		}
		return n == 2
	})

	wk.client.RouteClose(RouteJID, "abc")
	if !gw.ev.has("close:worker-1:jid:abc") {
		t.Fatalf("events = %v", gw.ev.snapshot())
	}
}

func TestDisconnectMakesTheWorkerReconnect(t *testing.T) {
	gw := newGateway(t, nil)
	newWorker(t, gw.url, "worker-1", nil, nil)
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	first := gw.srv.Worker("worker-1")

	first.Disconnect() // the gateway wants a fresh connection, e.g. to restart workspace sync
	waitFor(t, "offline", func() bool { return gw.ev.has("offline:worker-1") })
	waitFor(t, "reconnect", func() bool {
		w := gw.srv.Worker("worker-1")
		return w != nil && w.ConnectionID() != first.ConnectionID() && w.Online()
	})
	if first.Online() {
		t.Fatal("the old connection still reports online")
	}
}

func TestSameIDReplacesOldConnectionInOrder(t *testing.T) {
	gw := newGateway(t, nil)
	reg := RegisterRequest{WorkerID: "dup", ProtocolVersion: ProtocolVersion}
	if _, ok, msg := rawWorker(t, gw, reg); !ok {
		t.Fatal(msg)
	}
	first := gw.srv.Worker("dup")
	if _, ok, msg := rawWorker(t, gw, reg); !ok {
		t.Fatal(msg)
	}
	waitFor(t, "old connection closed", func() bool { return !first.Online() })
	time.Sleep(50 * time.Millisecond) // let the old connection's cleanup run

	got := gw.ev.snapshot()
	want := []string{"online:dup", "offline:dup", "online:dup"}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	if w := gw.srv.Worker("dup"); w == nil || w == first {
		t.Fatal("the new connection is not the registered one")
	}
}

func TestClosingAStreamReachesTheOtherEnd(t *testing.T) {
	gw := newGateway(t, nil)
	released := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the worker notices the stream going away
		close(released)
	})
	newWorker(t, gw.url, "worker-1", handler, nil)
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })

	conn, err := gw.srv.Worker("worker-1").Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "GET /hang HTTP/1.1\r\nHost: worker\r\n\r\n")
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("worker handler was not released when the gateway closed the stream")
	}
}

func TestRawSSHTransportPinsHostKey(t *testing.T) {
	gw := newGateway(t, nil)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go gw.srv.Serve(l)
	url := "ssh://" + l.Addr().String()

	if _, err := NewClient(ClientConfig{ServerURL: url, Token: testToken, Register: RegisterRequest{WorkerID: "w"}}); err == nil {
		t.Fatal("ssh:// without a pinned host key was accepted")
	}

	newWorker(t, url, "wrong-key", nil, func(c *ClientConfig) { c.HostKey = "SHA256:not-the-gateway" })
	newWorker(t, url, "right-key", nil, func(c *ClientConfig) { c.HostKey = Fingerprint(gw.srv.cfg.HostKey) })
	waitFor(t, "pinned worker", func() bool { return gw.srv.Worker("right-key") != nil })
	if gw.srv.Worker("wrong-key") != nil {
		t.Fatal("worker connected to a gateway whose key it did not pin")
	}
}

func TestClientConfigValidation(t *testing.T) {
	base := ClientConfig{ServerURL: "wss://gw.example/api/tunnel", Token: "t", Register: RegisterRequest{WorkerID: "w1"}}
	if _, err := NewClient(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []func(*ClientConfig){
		func(c *ClientConfig) { c.ServerURL = "https://gw.example/api/tunnel" },
		func(c *ClientConfig) { c.ServerURL = "gw.example:2222" },
		func(c *ClientConfig) { c.Token = "" },
		func(c *ClientConfig) { c.Register.WorkerID = "" },
		func(c *ClientConfig) { c.Register.WorkerID = "local" },
	}
	for i, mod := range bad {
		cfg := base
		mod(&cfg)
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("bad config %d was accepted", i)
		}
	}
}

func TestHostKeyIsPersisted(t *testing.T) {
	dir, err := ioutil.TempDir("", "hostkey")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "key")
	a, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(a) != Fingerprint(b) || !strings.HasPrefix(Fingerprint(a), "SHA256:") {
		t.Fatalf("fingerprints %s / %s", Fingerprint(a), Fingerprint(b))
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
		t.Fatalf("key file mode = %v", fi.Mode().Perm())
	}
}

func TestDrainSurvivesReconnect(t *testing.T) {
	gw := newGateway(t, nil)
	if gw.srv.SetDraining("worker-1", true) {
		t.Fatal("drained a worker that is not registered")
	}
	newWorker(t, gw.url, "worker-1", nil, nil)
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	first := gw.srv.Worker("worker-1")
	if first.Draining() {
		t.Fatal("a new worker starts draining")
	}
	if !gw.srv.SetDraining("worker-1", true) || !first.Draining() {
		t.Fatal("drain was not applied")
	}

	first.conn.Close()
	waitFor(t, "reconnect", func() bool {
		w := gw.srv.Worker("worker-1")
		return w != nil && w.ConnectionID() != first.ConnectionID()
	})
	if !gw.srv.Worker("worker-1").Draining() {
		t.Fatal("the worker lost its drain flag by reconnecting")
	}

	gw.srv.SetDraining("worker-1", false)
	if gw.srv.Worker("worker-1").Draining() {
		t.Fatal("undrain was not applied")
	}
}

func TestSyncChannelAndSyncingStateLifecycle(t *testing.T) {
	var ready sync.Mutex
	readyCalls := 0
	gw := newGateway(t, func(c *ServerConfig) {
		c.WorkspaceSync = true
		c.OnSyncReady = func(w *Worker) { ready.Lock(); readyCalls++; ready.Unlock() }
	})
	var gotReply RegisterReply
	syncStreams := make(chan net.Conn, 4)
	wk := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) {
		c.OnSyncStream = func(conn net.Conn) { syncStreams <- conn }
		orig := c.OnRegistered
		c.OnRegistered = func(r RegisterReply) { gotReply = r; orig(r) }
	})
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil && wk.client.Connected() })
	w := gw.srv.Worker("worker-1")
	if !gotReply.WorkspaceSync {
		t.Fatal("the registration reply did not say that workspace sync is on")
	}
	if !w.Syncing() {
		t.Fatal("a new worker must be SYNCING until it reports its homes reconciled")
	}

	// The gateway opens the sync channel and the worker's handler gets it.
	conn, err := w.DialSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case peer := <-syncStreams:
		defer peer.Close()
		go func() { io.Copy(peer, peer) }()
		io.WriteString(conn, "ping")
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
			t.Fatalf("sync channel echo: %q %v", buf, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never received the sync channel")
	}

	if !wk.client.SyncReady() {
		t.Fatal("the gateway did not acknowledge sync-ready")
	}
	if w.Syncing() {
		t.Fatal("still SYNCING after the worker reported ready")
	}
	wk.client.SyncReady() // a second report changes nothing
	ready.Lock()
	defer ready.Unlock()
	if readyCalls != 1 {
		t.Fatalf("OnSyncReady called %d times, want 1", readyCalls)
	}
}

func TestWorkerIsNeverSyncingWhenSyncIsOff(t *testing.T) {
	gw := newGateway(t, nil)
	var gotReply RegisterReply
	wk := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) {
		orig := c.OnRegistered
		c.OnRegistered = func(r RegisterReply) { gotReply = r; orig(r) }
	})
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil && wk.client.Connected() })
	if gw.srv.Worker("worker-1").Syncing() || gotReply.WorkspaceSync {
		t.Fatal("a worker is SYNCING although workspace sync is off")
	}
	// A sync channel is refused when the worker has no handler.
	if _, err := gw.srv.Worker("worker-1").DialSync(context.Background()); err == nil {
		t.Fatal("the worker accepted a sync channel it cannot serve")
	}
}

func TestWorkerThatNeverFinishesSyncingIsDropped(t *testing.T) {
	gw := newGateway(t, func(c *ServerConfig) {
		c.WorkspaceSync = true
		c.SyncTimeout = 200 * time.Millisecond
	})
	newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) { c.OnSyncStream = func(net.Conn) {} })
	waitFor(t, "the first registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	first := gw.srv.Worker("worker-1")
	waitFor(t, "the gateway to drop it", func() bool { return !first.Online() })
	// It reconnects and gets another try.
	waitFor(t, "a new registration", func() bool {
		w := gw.srv.Worker("worker-1")
		return w != nil && w.ConnectionID() != first.ConnectionID()
	})
}

// ---- the config a worker follows --------------------------------------------------

func TestAWorkerFollowsTheGatewaysConfig(t *testing.T) {
	var mu sync.Mutex
	current := &WorkerConfig{Revision: 11, Maintenance: true, MaintenanceMessage: "back soon", DisabledLanguages: []string{"python"}}
	gw := newGateway(t, func(c *ServerConfig) {
		c.Config = func() *WorkerConfig { mu.Lock(); defer mu.Unlock(); cp := *current; return &cp }
	})
	var got []WorkerConfig
	var gmu sync.Mutex
	wk := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) {
		c.OnConfig = func(cfg *WorkerConfig) { gmu.Lock(); got = append(got, *cfg); gmu.Unlock() }
	})
	rep := <-wk.reply
	if rep.Config == nil || rep.Config.Revision != 11 {
		t.Fatalf("the register reply: %+v", rep.Config)
	}
	waitFor(t, "the first config", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 1 })
	if got[0].MaintenanceMessage != "back soon" || len(got[0].DisabledLanguages) != 1 {
		t.Fatalf("config: %+v", got[0])
	}
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	if gw.srv.Worker("worker-1").ConfigRev() != 11 || gw.srv.ConfigRevision() != 11 {
		t.Fatalf("revisions: worker %d gateway %d", gw.srv.Worker("worker-1").ConfigRev(), gw.srv.ConfigRevision())
	}

	// the gateway's settings change: the next heartbeat brings the new config, once
	mu.Lock()
	current = &WorkerConfig{Revision: 12, Maintenance: false, DisabledLanguages: []string{"python", "cpp"}}
	mu.Unlock()
	waitFor(t, "the changed config", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got) == 2 })
	if got[1].Revision != 12 || len(got[1].DisabledLanguages) != 2 || got[1].Maintenance {
		t.Fatalf("second config: %+v", got[1])
	}
	waitFor(t, "the worker to report it", func() bool { return gw.srv.Worker("worker-1").ConfigRev() == 12 })
	time.Sleep(200 * time.Millisecond) // several heartbeats with nothing new
	gmu.Lock()
	n := len(got)
	gmu.Unlock()
	if n != 2 {
		t.Fatalf("the same config was handed over again: %d times", n)
	}
}

func TestAGatewayWithoutConfigSendsNone(t *testing.T) {
	gw := newGateway(t, nil)
	called := 0
	wk := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) { c.OnConfig = func(*WorkerConfig) { called++ } })
	if rep := <-wk.reply; rep.Config != nil {
		t.Fatalf("config: %+v", rep.Config)
	}
	time.Sleep(150 * time.Millisecond)
	if called != 0 || gw.srv.ConfigRevision() != 0 {
		t.Fatalf("called %d times, revision %d", called, gw.srv.ConfigRevision())
	}
}

func TestAReconnectingWorkerGetsTheConfigAgainOnlyIfItChanged(t *testing.T) {
	cfg := &WorkerConfig{Revision: 5}
	gw := newGateway(t, func(c *ServerConfig) { c.Config = func() *WorkerConfig { return cfg } })
	calls := 0
	var mu sync.Mutex
	wk := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) {
		c.OnConfig = func(*WorkerConfig) { mu.Lock(); calls++; mu.Unlock() }
	})
	<-wk.reply
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil })
	gw.srv.Worker("worker-1").conn.Close() // the connection drops; the worker reconnects
	<-wk.reply
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("OnConfig was called %d times for one revision", calls)
	}
}

// ---- settings that differ from worker to worker ------------------------------------

func TestEachWorkerGetsItsOwnConfigWhenTheGatewayHasPerWorkerSettings(t *testing.T) {
	var mu sync.Mutex
	off := map[string][]string{"worker-1": {"rappel"}, "worker-2": {"cpp"}}
	gw := newGateway(t, func(c *ServerConfig) {
		c.WorkerConfig = func(w *Worker) *WorkerConfig {
			mu.Lock()
			defer mu.Unlock()
			return &WorkerConfig{Revision: int64(w.ID()[len(w.ID())-1])*1000 + int64(len(off[w.ID()])), DisabledLanguages: off[w.ID()]}
		}
		// the global Config is ignored when WorkerConfig is set
		c.Config = func() *WorkerConfig { return &WorkerConfig{Revision: 1, DisabledLanguages: []string{"wrong"}} }
	})
	var got1, got2 []WorkerConfig
	var gmu sync.Mutex
	w1 := newWorker(t, gw.url, "worker-1", nil, func(c *ClientConfig) {
		c.OnConfig = func(cfg *WorkerConfig) { gmu.Lock(); got1 = append(got1, *cfg); gmu.Unlock() }
	})
	w2 := newWorker(t, gw.url, "worker-2", nil, func(c *ClientConfig) {
		c.OnConfig = func(cfg *WorkerConfig) { gmu.Lock(); got2 = append(got2, *cfg); gmu.Unlock() }
	})
	<-w1.reply
	<-w2.reply
	waitFor(t, "both configs", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got1) == 1 && len(got2) == 1 })
	if len(got1[0].DisabledLanguages) != 1 || got1[0].DisabledLanguages[0] != "rappel" || got2[0].DisabledLanguages[0] != "cpp" {
		t.Fatalf("worker-1 %+v, worker-2 %+v", got1[0], got2[0])
	}
	waitFor(t, "registration", func() bool { return gw.srv.Worker("worker-1") != nil && gw.srv.Worker("worker-2") != nil })
	if r1, r2 := gw.srv.ConfigRevisionFor(gw.srv.Worker("worker-1")), gw.srv.ConfigRevisionFor(gw.srv.Worker("worker-2")); r1 == r2 || r1 == 0 {
		t.Fatalf("revisions %d and %d", r1, r2)
	}

	// an admin changes one worker's languages: only that worker is handed a new config
	mu.Lock()
	off["worker-1"] = []string{"rappel", "go"}
	mu.Unlock()
	waitFor(t, "worker-1 follows the change", func() bool { gmu.Lock(); defer gmu.Unlock(); return len(got1) == 2 })
	time.Sleep(150 * time.Millisecond)
	gmu.Lock()
	defer gmu.Unlock()
	if len(got1[1].DisabledLanguages) != 2 || len(got2) != 1 {
		t.Fatalf("worker-1 %+v; worker-2 was handed %d configs", got1[1], len(got2))
	}
}

func TestAWorkerSaysWhetherItsHostCanTracePrograms(t *testing.T) {
	gw := newGateway(t, nil)
	wk := newWorker(t, gw.url, "pi-1", nil, func(c *ClientConfig) { c.Register.Ptrace = "traceme: not implemented" })
	<-wk.reply
	waitFor(t, "registration", func() bool { return gw.srv.Worker("pi-1") != nil })
	if got := gw.srv.Worker("pi-1").Info().Ptrace; got != "traceme: not implemented" {
		t.Fatalf("ptrace = %q", got)
	}
}
