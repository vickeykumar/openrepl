//go:build linux
// +build linux

package gateway

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"trusted"
	"tunnel"
	"wsync"
)

// syncCluster is a gateway with workspace sync on, and workers that keep
// their homes in step with it.
type syncCluster struct {
	t       *testing.T
	router  *Router
	tunnel  *tunnel.Server
	http    *httptest.Server
	gwBase  string
	gwState string
	gwMgr   *wsync.Manager
	local   *homeFiles
	pins    *memPins
}

// homeFiles is the gateway's own handler: it answers a file request from the
// directory the router says to use.
type homeFiles struct {
	base string
	mu   sync.Mutex
	got  []string
}

func (h *homeFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	home := WorkspaceOf(r)
	h.mu.Lock()
	h.got = append(h.got, home)
	h.mu.Unlock()
	if home == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	b, err := ioutil.ReadFile(filepath.Join(h.base, home, r.URL.Query().Get("f")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Write(b)
}

func newSyncCluster(t *testing.T) *syncCluster {
	t.Helper()
	c := &syncCluster{t: t, gwBase: t.TempDir(), gwState: t.TempDir()}
	c.local = &homeFiles{base: c.gwBase}
	var gwMgr *wsync.Manager
	pins := &memPins{}
	c.pins = pins
	rt := NewRouter(Config{
		Site:       c.local,
		PathPrefix: "/",
		UID:        func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Local:      LocalConfig{Weight: 0}, // routing only: sessions run on workers
		Identity: func(w http.ResponseWriter, r *http.Request, ec ExecutionContext) trusted.Identity {
			id := trusted.Identity{UID: ec.UID, Guest: ec.GuestID, Session: ec.Key}
			if ec.UID != "" {
				id.HomeID = "home-" + ec.UID
			}
			return id
		},
		HomeOf: func(id Identity) string {
			if id.UID != "" {
				return "home-" + id.UID
			}
			return "guest-" + id.GuestID
		},
		Pins:          pins,
		RelocateAfter: 150 * time.Millisecond,
		PrepareHome: func(ctx context.Context, home, backend string) error {
			if _, err := os.Stat(filepath.Join(c.gwBase, home)); os.IsNotExist(err) {
				return nil
			}
			return gwMgr.EnsureHome(ctx, backend, home)
		},
		// As the server wires them (server/gateway.go).
		SecureHome: func(home, backend string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return gwMgr.SecureCopy(ctx, backend, home)
		},
		OnMoved: func(home, from, to string) {
			if from == LocalID {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gwMgr.DropMoved(ctx, from, home)
		},
	})
	c.router = rt

	key, err := tunnel.NewEphemeralHostKey()
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := wsync.NewManager(wsync.ManagerConfig{
		Node: "gateway", IsGateway: true, BaseDir: c.gwBase, StateDir: c.gwState,
		OwnerOf: rt.OwnerOf, Debounce: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.gwMgr = mgr
	gwMgr = mgr
	tcfg := tunnel.ServerConfig{
		Token: "tok", HostKey: key, WorkspaceSync: true,
		HeartbeatInterval: 100 * time.Millisecond, Timeout: 5 * time.Second,
		Logf: func(string, ...interface{}) {},
	}
	rt.BindTunnel(&tcfg)
	addBackend := tcfg.OnOnline
	ctx, cancel := context.WithCancel(context.Background())
	tcfg.OnOnline = func(w *tunnel.Worker) {
		addBackend(w)
		go func() {
			conn, err := w.DialSync(ctx)
			if err != nil {
				return
			}
			mgr.Serve(ctx, w.ID(), conn, nil)
		}()
	}
	ts, err := tunnel.NewServer(tcfg)
	if err != nil {
		t.Fatal(err)
	}
	c.tunnel = ts
	rt.SetTunnel("/api/tunnel", ts)
	c.http = httptest.NewServer(rt)
	t.Cleanup(func() { cancel(); ts.Close(); mgr.Close(); c.http.Close() })
	return c
}

// syncWorker is a worker process: a tunnel client, its own disk, and a sync
// manager. Its HTTP handler waits for the session's home to be in step, as a
// real worker does, then answers.
type syncWorker struct {
	id     string
	base   string
	state  string
	client *tunnel.Client
	mgr    *wsync.Manager
	cancel context.CancelFunc
	done   chan struct{}
	// hold, if set, delays the sync conversation, keeping the worker SYNCING.
	hold chan struct{}
}

func (c *syncCluster) startWorker(id, base, state string, hold chan struct{}) *syncWorker {
	c.t.Helper()
	w := &syncWorker{id: id, base: base, state: state, done: make(chan struct{}), hold: hold}
	mgr, err := wsync.NewManager(wsync.ManagerConfig{
		Node: id, BaseDir: base, StateDir: state,
		Debounce: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, Logf: c.t.Logf,
	})
	if err != nil {
		c.t.Fatal(err)
	}
	w.mgr = mgr
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	var client *tunnel.Client
	client, err = tunnel.NewClient(tunnel.ClientConfig{
		ServerURL:  "ws" + strings.TrimPrefix(c.http.URL, "http") + "/api/tunnel",
		Token:      "tok",
		Register:   tunnel.RegisterRequest{WorkerID: id, Capacity: 100000, Weight: 10},
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		Logf: func(string, ...interface{}) {},
		OnSyncStream: func(conn net.Conn) {
			if hold != nil {
				select {
				case <-hold:
				case <-ctx.Done():
					conn.Close()
					return
				}
			}
			mgr.Serve(ctx, "gateway", conn, func() { client.SyncReady() })
		},
	})
	if err != nil {
		c.t.Fatal(err)
	}
	w.client = client
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		who := trusted.FromHeader(r.Header)
		home := "guest-" + who.Guest
		if who.UID != "" {
			home = who.HomeID
		}
		wctx, wcancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer wcancel()
		for !mgr.Connected("gateway") {
			select {
			case <-wctx.Done():
				http.Error(rw, "workspace is synchronizing", http.StatusServiceUnavailable)
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		if err := mgr.EnsureHome(wctx, "gateway", home); err != nil {
			http.Error(rw, err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(rw, "%s|%s", id, home)
	})
	srv := &http.Server{Handler: handler}
	go srv.Serve(client.Listener())
	go func() {
		client.Run(ctx)
		srv.Close()
		mgr.Close()
		close(w.done)
	}()
	c.t.Cleanup(w.stop)
	return w
}

func (w *syncWorker) stop() {
	w.cancel()
	<-w.done
}

func (c *syncCluster) state(id string) State {
	if b, ok := c.router.Backend(id); ok {
		return b.State()
	}
	return Offline
}

func (c *syncCluster) waitState(id string, want State) {
	c.t.Helper()
	waitUntil(c.t, fmt.Sprintf("%s to be %v", id, want), func() bool { return c.state(id) == want })
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type guest struct {
	c      *syncCluster
	client *http.Client
	home   string
}

func (c *syncCluster) newGuest() *guest {
	jar, _ := cookiejar.New(nil)
	g := &guest{c: c, client: &http.Client{Jar: jar}}
	g.get("/") // the entry page assigns the session
	return g
}

func (g *guest) get(path string) (int, string) {
	g.c.t.Helper()
	resp, err := g.client.Get(g.c.http.URL + path)
	if err != nil {
		g.c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// firstFile requests the file browser and returns "worker|home".
func (g *guest) servedBy() (string, string) {
	code, body := g.get("/ws_filebrowser")
	if code != http.StatusOK {
		return "", fmt.Sprintf("HTTP %d %s", code, body)
	}
	parts := strings.SplitN(body, "|", 2)
	if len(parts) != 2 {
		return "", body
	}
	g.home = parts[1]
	return parts[0], parts[1]
}

func TestSyncingWorkerIsOutOfRotationUntilItsHomesAreInStep(t *testing.T) {
	c := newSyncCluster(t)
	hold := make(chan struct{})
	w := c.startWorker("worker-1", t.TempDir(), t.TempDir(), hold)
	_ = w

	waitUntil(t, "worker-1 to register", func() bool { _, ok := c.router.Backend("worker-1"); return ok })
	c.waitState("worker-1", Syncing)
	// While SYNCING it takes no new sessions.
	for i := 0; i < 10; i++ {
		g := c.newGuest()
		code, body := g.get("/ws_filebrowser")
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "no execution node") {
			t.Fatalf("a new session while the only worker is SYNCING -> %d %q", code, body)
		}
	}

	close(hold) // the worker's conversation starts; it has no homes yet
	c.waitState("worker-1", Online)
	g := c.newGuest()
	if by, home := g.servedBy(); by != "worker-1" {
		t.Fatalf("after ONLINE: %q %q", by, home)
	}
}

func TestWorkerHomeReachesTheGatewayAndIsServedFromThereWhenTheWorkerIsAway(t *testing.T) {
	c := newSyncCluster(t)
	wkBase, wkState := t.TempDir(), t.TempDir()
	w := c.startWorker("worker-1", wkBase, wkState, nil)
	c.waitState("worker-1", Online)

	g := c.newGuest()
	by, home := g.servedBy()
	if by != "worker-1" {
		t.Fatalf("served by %q", by)
	}
	// A program in the session writes a file on the worker.
	if err := ioutil.WriteFile(filepath.Join(wkBase, home, "result.txt"), []byte("42"), 0644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the file on the gateway", func() bool {
		b, err := ioutil.ReadFile(filepath.Join(c.gwBase, home, "result.txt"))
		return err == nil && string(b) == "42"
	})

	// The worker is lost. The file browser is still served, from the gateway.
	w.stop()
	c.waitState("worker-1", Offline)
	if code, body := g.get("/ws_filebrowser?f=result.txt"); code != http.StatusOK || body != "42" {
		t.Fatalf("file request with the worker away -> %d %q, want the synced content", code, body)
	}
	if code, _ := g.get("/ws_python"); code != http.StatusServiceUnavailable {
		t.Fatalf("a terminal with the worker away -> %d, want 503", code)
	}
	c.local.mu.Lock()
	defer c.local.mu.Unlock()
	if len(c.local.got) == 0 || c.local.got[len(c.local.got)-1] != "" && !contains(c.local.got, home) {
		t.Fatalf("the gateway never served home %s: %v", home, c.local.got)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestWorkerReconnectsSyncsAndReturnsToRotationOnlyThen(t *testing.T) {
	c := newSyncCluster(t)
	wkBase, wkState := t.TempDir(), t.TempDir()
	w := c.startWorker("worker-1", wkBase, wkState, nil)
	c.waitState("worker-1", Online)

	g := c.newGuest()
	_, home := g.servedBy()
	os.WriteFile(filepath.Join(wkBase, home, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(wkBase, home, "b.txt"), []byte("b"), 0644)
	waitUntil(t, "a.txt and b.txt on the gateway", func() bool {
		_, e1 := os.Stat(filepath.Join(c.gwBase, home, "a.txt"))
		_, e2 := os.Stat(filepath.Join(c.gwBase, home, "b.txt"))
		return e1 == nil && e2 == nil
	})

	// Let both sides finish recording what they agreed before the connection
	// goes (a record is written a moment after its last change). If it went
	// at this very moment the sides would disagree about b.txt, and the
	// reconcile would keep it rather than risk deleting it.
	recorded := func(dir, peer string) bool {
		b, err := ioutil.ReadFile(filepath.Join(dir, peer, home+".json"))
		return err == nil && strings.Contains(string(b), "a.txt") && strings.Contains(string(b), "b.txt")
	}
	waitUntil(t, "both records to include the files", func() bool {
		return recorded(wkState, "gateway") && recorded(c.gwState, "worker-1")
	})

	// The worker goes away. While it is gone: the worker's disk changes by
	// itself (a process that kept running), and the gateway receives a save.
	w.stop()
	c.waitState("worker-1", Offline)
	os.WriteFile(filepath.Join(wkBase, home, "a.txt"), []byte("a, changed on the worker"), 0644)
	os.Remove(filepath.Join(wkBase, home, "b.txt"))
	os.WriteFile(filepath.Join(c.gwBase, home, "saved.txt"), []byte("saved on the gateway"), 0644)

	// It reconnects: SYNCING first, and ONLINE only once the home is in step.
	hold := make(chan struct{})
	c.startWorker("worker-1", wkBase, wkState, hold)
	c.waitState("worker-1", Syncing)
	close(hold)
	c.waitState("worker-1", Online)

	read := func(base, name string) string {
		b, _ := ioutil.ReadFile(filepath.Join(base, home, name))
		return string(b)
	}
	if read(c.gwBase, "a.txt") != "a, changed on the worker" || read(wkBase, "saved.txt") != "saved on the gateway" {
		t.Fatalf("not reconciled: gateway a=%q, worker saved=%q", read(c.gwBase, "a.txt"), read(wkBase, "saved.txt"))
	}
	if _, err := os.Stat(filepath.Join(c.gwBase, home, "b.txt")); err == nil {
		t.Fatal("a deletion made on the worker while it was disconnected did not reach the gateway")
	}
	// And the session works again.
	if by, _ := g.servedBy(); by != "worker-1" {
		t.Fatalf("after the reconnect the session is served by %q", by)
	}
}

func TestUserMovedToAnotherWorkerFindsTheirFiles(t *testing.T) {
	c := newSyncCluster(t)
	w1Base, w1State := t.TempDir(), t.TempDir()
	w2Base, w2State := t.TempDir(), t.TempDir()
	w1 := c.startWorker("worker-1", w1Base, w1State, nil)
	c.waitState("worker-1", Online)

	user := &http.Client{}
	asUser := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", c.http.URL+path, nil)
		req.Header.Set("X-Test-Uid", "42")
		resp, err := user.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := ioutil.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := asUser("/ws_filebrowser")
	if code != http.StatusOK || !strings.HasPrefix(body, "worker-1|") {
		t.Fatalf("first placement -> %d %q", code, body)
	}
	home := "home-42"
	os.MkdirAll(filepath.Join(w1Base, home, "project"), 0755)
	os.WriteFile(filepath.Join(w1Base, home, "project", "main.py"), []byte("print('work done on worker-1')"), 0644)
	waitUntil(t, "the project on the gateway", func() bool {
		b, err := ioutil.ReadFile(filepath.Join(c.gwBase, home, "project", "main.py"))
		return err == nil && strings.Contains(string(b), "worker-1")
	})

	// A second worker joins, and the first one is retired (lost for good).
	c.startWorker("worker-2", w2Base, w2State, nil)
	c.waitState("worker-2", Online)
	w1.stop()
	c.waitState("worker-1", Offline)

	// Within the grace period the user waits for their own worker.
	if code, body := asUser("/ws_python"); code != http.StatusServiceUnavailable || !strings.Contains(body, "workspace node unavailable") {
		t.Fatalf("within the grace period -> %d %q", code, body)
	}
	// Meanwhile the file browser is served from the gateway's copy.
	if code, body := asUser("/ws_filebrowser?f=project/main.py"); code != http.StatusOK || !strings.Contains(body, "worker-1") {
		t.Fatalf("file request while the worker is away -> %d %q", code, body)
	}

	// After it, they are placed on worker-2, which is sent their files first.
	time.Sleep(250 * time.Millisecond)
	waitUntil(t, "the user to be placed on worker-2", func() bool {
		code, body := asUser("/ws_filebrowser")
		return code == http.StatusOK && strings.HasPrefix(body, "worker-2|")
	})
	b, err := ioutil.ReadFile(filepath.Join(w2Base, home, "project", "main.py"))
	if err != nil || !strings.Contains(string(b), "work done on worker-1") {
		t.Fatalf("worker-2 does not have the user's files: %q %v", b, err)
	}
	if got, _ := c.pins.Get("42"); got != "worker-2" {
		t.Fatalf("pin = %q", got)
	}
	// From now on worker-2's changes reach the gateway.
	os.WriteFile(filepath.Join(w2Base, home, "project", "more.py"), []byte("x"), 0644)
	waitUntil(t, "worker-2's new file on the gateway", func() bool {
		_, err := os.Stat(filepath.Join(c.gwBase, home, "project", "more.py"))
		return err == nil
	})

	// The old worker comes back: it is told to drop the home, and does not
	// get back into the rotation of that user.
	c.startWorker("worker-1", w1Base, w1State, nil)
	c.waitState("worker-1", Online)
	waitUntil(t, "worker-1's record of the moved home to go", func() bool {
		files, _ := ioutil.ReadDir(filepath.Join(w1State, "gateway"))
		for _, f := range files {
			if f.Name() == home+".json" {
				return false
			}
		}
		return true
	})
}

// userGet makes a request as a signed-in user.
func (c *syncCluster) userGet(uid, path string) (int, string) {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.http.URL+path, nil)
	req.Header.Set("X-Test-Uid", uid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// workerHoldingAUsersFiles returns the disk of a worker that held the files
// of user 42 in step with a gateway. That gateway is not used again: the
// tests continue with a new one, which starts on an empty disk, as a gateway
// does on a host that keeps no files between deploys.
func workerHoldingAUsersFiles(t *testing.T) (base, state string) {
	t.Helper()
	old := newSyncCluster(t)
	base, state = t.TempDir(), t.TempDir()
	w := old.startWorker("worker-1", base, state, nil)
	old.waitState("worker-1", Online)
	if code, body := old.userGet("42", "/ws_filebrowser"); code != http.StatusOK || !strings.HasPrefix(body, "worker-1|") {
		t.Fatalf("first placement -> %d %q", code, body)
	}
	os.MkdirAll(filepath.Join(base, "home-42", "project"), 0755)
	os.WriteFile(filepath.Join(base, "home-42", "project", "main.py"), []byte("print('my work')"), 0644)
	waitUntil(t, "the worker's record of the file", func() bool {
		b, err := ioutil.ReadFile(filepath.Join(state, "gateway", "home-42.json"))
		return err == nil && strings.Contains(string(b), "main.py")
	})
	w.stop()
	return base, state
}

func TestUserFindsTheirFilesAfterTheGatewayLostItsDisk(t *testing.T) {
	wkBase, wkState := workerHoldingAUsersFiles(t)
	const home = "home-42"

	// The new gateway knows where the user belongs (the pins are kept in the
	// user database) and nothing else. The worker reconnects and offers the
	// home, which no session uses yet.
	c := newSyncCluster(t)
	c.pins.Set("42", "worker-1")
	c.startWorker("worker-1", wkBase, wkState, nil)
	c.waitState("worker-1", Online)
	if _, err := os.Stat(filepath.Join(c.gwBase, home)); err == nil {
		t.Fatal("the gateway took a home that had no session")
	}

	// The user returns. Their worker has the files and serves them.
	if code, body := c.userGet("42", "/ws_filebrowser"); code != http.StatusOK || body != "worker-1|"+home {
		t.Fatalf("the user's first request after the gateway restarted -> %d %q", code, body)
	}
	waitUntil(t, "the user's files on the new gateway", func() bool {
		b, err := ioutil.ReadFile(filepath.Join(c.gwBase, home, "project", "main.py"))
		return err == nil && string(b) == "print('my work')"
	})
	if b, _ := ioutil.ReadFile(filepath.Join(wkBase, home, "project", "main.py")); string(b) != "print('my work')" {
		t.Fatalf("the worker's file = %q", b)
	}

	// With the gateway's copy in step, an admin can move the session to the
	// gateway: the worker gives up its copy and the files are still there.
	if _, err := c.router.MoveSession("u:42", LocalID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the worker's record of the moved home to go", func() bool {
		_, err := os.Stat(filepath.Join(wkState, "gateway", home+".json"))
		return os.IsNotExist(err)
	})
	if b, _ := ioutil.ReadFile(filepath.Join(c.gwBase, home, "project", "main.py")); string(b) != "print('my work')" {
		t.Fatalf("the gateway's file after the move = %q", b)
	}
}

func TestUserIsNotStartedElsewhereWhileOnlyTheirWorkerHasTheirFiles(t *testing.T) {
	wkBase, wkState := workerHoldingAUsersFiles(t)
	const home = "home-42"

	// The new gateway comes up, and the user's worker stays away for longer
	// than the grace period. Another worker is there.
	c := newSyncCluster(t)
	c.pins.Set("42", "worker-1")
	w2Base := t.TempDir()
	c.startWorker("worker-2", w2Base, t.TempDir(), nil)
	c.waitState("worker-2", Online)
	time.Sleep(250 * time.Millisecond)

	for i := 0; i < 3; i++ {
		if code, body := c.userGet("42", "/ws_filebrowser"); code != http.StatusServiceUnavailable || !strings.Contains(body, "workspace node unavailable") {
			t.Fatalf("with the files only on a worker that is away -> %d %q", code, body)
		}
	}
	if got, _ := c.pins.Get("42"); got != "worker-1" {
		t.Fatalf("pin = %q, want worker-1", got)
	}
	if _, err := os.Stat(filepath.Join(w2Base, home)); err == nil {
		t.Fatal("the user was given an empty home on another worker")
	}
	if _, err := os.Stat(filepath.Join(c.gwState, "worker-1", home+".drop")); err == nil {
		t.Fatal("the worker that has the only copy was ordered to drop it")
	}

	// Their worker returns: they are back on it, with their files.
	c.startWorker("worker-1", wkBase, wkState, nil)
	c.waitState("worker-1", Online)
	if code, body := c.userGet("42", "/ws_filebrowser"); code != http.StatusOK || body != "worker-1|"+home {
		t.Fatalf("after the worker returned -> %d %q", code, body)
	}
	if b, _ := ioutil.ReadFile(filepath.Join(wkBase, home, "project", "main.py")); string(b) != "print('my work')" {
		t.Fatalf("the worker's file = %q", b)
	}
	waitUntil(t, "the user's files on the gateway", func() bool {
		_, err := os.Stat(filepath.Join(c.gwBase, home, "project", "main.py"))
		return err == nil
	})
}
