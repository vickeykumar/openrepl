package gateway

import (
	"context"
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"trusted"
)

// seen is what a fake worker recorded about the request it received.
type seen struct {
	mu      sync.Mutex
	method  string
	uri     string
	host    string
	body    string
	headers http.Header
}

func (s *seen) record(r *http.Request) {
	body, _ := ioutil.ReadAll(r.Body)
	s.mu.Lock()
	s.method, s.uri, s.host, s.body, s.headers = r.Method, r.URL.RequestURI(), r.Host, string(body), r.Header.Clone()
	s.mu.Unlock()
}

// remoteTo returns a RemoteBackend whose streams are TCP connections to hs.
func remoteTo(hs *httptest.Server, id, prefix string) *RemoteBackend {
	return NewRemoteBackend(RemoteConfig{
		ID:          id,
		StripPrefix: prefix,
		Weight:      10,
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", hs.Listener.Addr().String())
		},
	})
}

// routerWith builds a router whose only worker is b; every session goes there.
func routerWith(b Backend, prefix string) *Router {
	rt := NewRouter(Config{
		Site:       http.NotFoundHandler(),
		PathPrefix: prefix,
		UID:        func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		Identity: func(w http.ResponseWriter, r *http.Request, ec ExecutionContext) trusted.Identity {
			return trusted.Identity{UID: ec.UID, Guest: ec.GuestID, HomeID: "home-" + ec.UID, Privilege: "guest", Session: ec.Key}
		},
	})
	rt.AddBackend(b)
	return rt
}

func TestRemoteBackendForwardsRequestUnchanged(t *testing.T) {
	var got seen
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.record(r)
		w.Header().Add("Set-Cookie", "user-session=forged; Path=/")
		w.Header().Set("X-From-Worker", "yes")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("done"))
	}))
	defer worker.Close()
	rt := routerWith(remoteTo(worker, "worker-1", "/"), "/")
	gw := httptest.NewServer(rt)
	defer gw.Close()

	req, _ := http.NewRequest("POST", gw.URL+"/upload_file?jid=abc&q=save&filepath=%2Ftmp%2Fa%20b", strings.NewReader("file-bytes"))
	req.Header.Set("Cookie", "user-session=real; other=1")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Custom", "kept")
	req.Header.Set("X-Test-Uid", "42")
	req.Header.Set("X-OpenREPL-Uid", "attacker") // must not survive
	req.Header.Set("X-Openrepl-Priv", "admin")   // must not survive
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated || string(body) != "done" || resp.Header.Get("X-From-Worker") != "yes" {
		t.Fatalf("response: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if len(resp.Header["Set-Cookie"]) != 0 {
		t.Fatalf("a worker set a browser cookie: %v", resp.Header["Set-Cookie"])
	}
	if got.method != "POST" || got.uri != "/upload_file?jid=abc&q=save&filepath=%2Ftmp%2Fa%20b" || got.body != "file-bytes" {
		t.Fatalf("worker saw %s %s %q", got.method, got.uri, got.body)
	}
	if got.host != strings.TrimPrefix(gw.URL, "http://") {
		t.Fatalf("Host = %q, want the gateway's", got.host)
	}
	h := got.headers
	if h.Get("Cookie") != "user-session=real; other=1" || h.Get("X-Custom") != "kept" || h.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("headers not preserved: %v", h)
	}
	id := trusted.FromHeader(h)
	want := trusted.Identity{UID: "42", HomeID: "home-42", Privilege: "guest", Session: "u:42"}
	if id != want {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
}

func TestRemoteBackendStripsGatewayPrefix(t *testing.T) {
	var got seen
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got.record(r) }))
	defer worker.Close()
	rt := routerWith(remoteTo(worker, "worker-1", "/abc123/"), "/abc123/")
	gw := httptest.NewServer(rt)
	defer gw.Close()

	resp, err := http.Get(gw.URL + "/abc123/ws_filebrowser?q=usage")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.uri != "/ws_filebrowser?q=usage" {
		t.Fatalf("worker saw %q", got.uri)
	}
}

func TestRemoteBackendBridgesWebSocket(t *testing.T) {
	var got seen
	workerClosed := make(chan struct{})
	up := websocket.Upgrader{Subprotocols: []string{"webtty"}}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		got.headers = r.Header.Clone()
		got.mu.Unlock()
		hdr := http.Header{}
		hdr.Add("Set-Cookie", "forged=1")
		c, err := up.Upgrade(w, r, hdr)
		if err != nil {
			return
		}
		defer c.Close()
		defer close(workerClosed)
		for {
			typ, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			c.WriteMessage(typ, append([]byte("echo:"), msg...))
		}
	}))
	defer worker.Close()
	rt := routerWith(remoteTo(worker, "worker-1", "/"), "/")
	gw := httptest.NewServer(rt)
	defer gw.Close()

	d := websocket.Dialer{Subprotocols: []string{"webtty"}}
	hdr := http.Header{}
	hdr.Set("X-Openrepl-Uid", "attacker")
	c, resp, err := d.Dial("ws"+strings.TrimPrefix(gw.URL, "http")+"/ws_python", hdr)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subprotocol() != "webtty" {
		t.Fatalf("subprotocol = %q", c.Subprotocol())
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "forged" {
			t.Fatal("a worker set a cookie on the upgrade response")
		}
	}
	for _, msg := range []string{"one", "two", strings.Repeat("x", 100000)} {
		if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		_, reply, err := c.ReadMessage()
		if err != nil || string(reply) != "echo:"+msg {
			t.Fatalf("reply %q, %v", reply, err)
		}
	}
	if id := trusted.FromHeader(got.headers); id.UID != "" || id.Guest == "" || id.Session != "g:"+id.Guest {
		t.Fatalf("identity on upgrade = %+v", id)
	}

	// Closing the browser side must end the worker's stream.
	c.Close()
	select {
	case <-workerClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("worker stream stayed open after the browser disconnected")
	}
}

func TestRemoteBackendUnavailable(t *testing.T) {
	b := NewRemoteBackend(RemoteConfig{
		ID:     "worker-1",
		Weight: 10,
		Dial:   func(context.Context) (net.Conn, error) { return nil, errors.New("tunnel down") },
	})
	rt := routerWith(b, "/")
	rec := do(rt, "GET", "/ws_filebrowser", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestRouteMap(t *testing.T) {
	rm := NewRouteMap()
	rm.Set(RouteJID, "j1", "w1")
	rm.Set(RouteHome, "/tmp/home/a", "w1")
	rm.Set(RouteJID, "j2", "w2")
	if b, ok := rm.Lookup(RouteJID, "j1"); !ok || b != "w1" {
		t.Fatalf("lookup = %q, %v", b, ok)
	}
	if _, ok := rm.Lookup(RouteHome, "j1"); ok {
		t.Fatal("kinds are not separate")
	}
	rm.Delete(RouteJID, "j1", "w2") // not the owner
	if _, ok := rm.Lookup(RouteJID, "j1"); !ok {
		t.Fatal("a non-owner deleted the route")
	}
	rm.DropBackend("w1")
	if _, ok := rm.Lookup(RouteJID, "j1"); ok {
		t.Fatal("route survived DropBackend")
	}
	if rm.Len() != 1 {
		t.Fatalf("len = %d", rm.Len())
	}
}

// named is a backend that records how many requests it served.
type named struct {
	id        string
	state     State
	weight    int
	used, max int64
	langs     []string // nil means every language
	hits      int
}

func (b *named) ID() string               { return b.id }
func (b *named) State() State             { return b.state }
func (b *named) Weight() int              { return b.weight }
func (b *named) Capacity() (int64, int64) { return b.used, b.max }
func (b *named) HasLanguage(c string) bool {
	if b.langs == nil {
		return true
	}
	for _, l := range b.langs {
		if l == c {
			return true
		}
	}
	return false
}
func (b *named) Serve(w http.ResponseWriter, r *http.Request) error {
	b.hits++
	return nil
}

func TestCrossSessionKeysFollowTheirOwner(t *testing.T) {
	a, b := &named{id: "worker-a"}, &named{id: "worker-b"}
	rt := newTestRouter(&recorder{}, "/", &countingPicker{id: "worker-a"})
	rt.AddBackend(a)
	rt.AddBackend(b)
	rt.Routes().Set(RouteJID, "J-on-b", "worker-b")
	rt.Routes().Set(RouteHome, "/tmp/home/owner", "worker-b")

	do(rt, "GET", "/ws_python", nil)                                     // own affinity
	do(rt, "GET", "/ws_python?jid=J-on-b", nil)                          // fork link
	do(rt, "GET", "/ws_filebrowser?q=load&homedir=/tmp/home/owner", nil) // shared viewer
	do(rt, "GET", "/ws_python?jid=unknown", nil)                         // falls back
	if a.hits != 2 || b.hits != 2 {
		t.Fatalf("a=%d b=%d, want 2 and 2", a.hits, b.hits)
	}

	// When the owner goes away its keys go with it; nothing is re-balanced.
	rt.RemoveBackend(b)
	if rt.Routes().Len() != 0 {
		t.Fatalf("routes left: %d", rt.Routes().Len())
	}
	do(rt, "GET", "/ws_python?jid=J-on-b", nil)
	if a.hits != 3 {
		t.Fatalf("a=%d", a.hits)
	}
}

func TestExistingLocalWorkspaceStaysLocal(t *testing.T) {
	site := &recorder{}
	w := &named{id: "worker-1", weight: 10}
	// The gateway is routing-only (weight 0), so the pool never picks it.
	rt := NewRouter(Config{
		Site:              site,
		PathPrefix:        "/",
		UID:               func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:            []byte("secret"),
		GuestTTL:          time.Hour,
		HasLocalWorkspace: func(uid string) bool { return uid == "old-user" },
	})
	rt.AddBackend(w)

	do(rt, "GET", "/ws_go", func(r *http.Request) { r.Header.Set("X-Test-Uid", "old-user") })
	if len(site.paths) != 1 || w.hits != 0 {
		t.Fatalf("user with a local workspace was moved: local=%d worker=%d", len(site.paths), w.hits)
	}
	do(rt, "GET", "/ws_go", func(r *http.Request) { r.Header.Set("X-Test-Uid", "new-user") })
	do(rt, "GET", "/ws_go", nil)
	if w.hits != 2 {
		t.Fatalf("new user and guest should go to the worker, hits=%d", w.hits)
	}
}

func TestRemoveBackendLeavesItsReplacement(t *testing.T) {
	rt := newTestRouter(&recorder{}, "/", nil)
	old, cur := &named{id: "worker-1"}, &named{id: "worker-1"}
	rt.AddBackend(old)
	rt.AddBackend(cur) // the worker reconnected
	rt.Routes().Set(RouteJID, "j", "worker-1")
	rt.RemoveBackend(old) // the old connection's cleanup arrives late
	if b, ok := rt.Backend("worker-1"); !ok || b != Backend(cur) {
		t.Fatal("removing the old connection removed the new one")
	}
	if rt.Routes().Len() != 1 {
		t.Fatal("the new connection's routes were dropped")
	}
}

func TestClientCannotSpoofIdentityOnTheGateway(t *testing.T) {
	var got http.Header
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() })
	rt := newTestRouter(site, "/", nil)
	for _, p := range []string{"/", "/ws_go", "/admin/settings", "/login"} {
		do(rt, "GET", p, func(r *http.Request) {
			r.Header.Set("X-Openrepl-Uid", "attacker")
			r.Header.Set("X-OpenREPL-Priv", "admin")
		})
		if id := trusted.FromHeader(got); id != (trusted.Identity{}) {
			t.Fatalf("%s: spoofed identity reached the handler: %+v", p, id)
		}
	}
}

func TestTunnelPathBypassesRouting(t *testing.T) {
	hits := 0
	rt := NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/abc/",
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
	})
	rt.SetTunnel("/api/tunnel", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	rec := do(rt, "GET", "/abc/api/tunnel", nil)
	if hits != 1 || rt.Registry().Len() != 0 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("hits=%d registry=%d", hits, rt.Registry().Len())
	}
}
