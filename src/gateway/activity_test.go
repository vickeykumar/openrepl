package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestExpireGuestsOnlyWhenIdleWithNoOpenTerminal(t *testing.T) {
	rt := syncRouter(&recorder{}, nil)
	w := &named{id: "worker-1", weight: 10}
	rt.AddBackend(w)

	idle := do(rt, "GET", "/ws_filebrowser", nil)
	busy := do(rt, "GET", "/ws_filebrowser", nil)
	idleHome := "guest-" + rt.affinity.GuestID(requestWith(cookieOf(t, idle)))
	busyHome := "guest-" + rt.affinity.GuestID(requestWith(cookieOf(t, busy)))
	user := do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.Header.Set("X-Test-Uid", "42") })
	_ = user

	// A terminal is open for the busy guest.
	rt.terminalOpened(busyHome)

	later := time.Now().Add(2 * time.Hour)
	expired := rt.ExpireGuests(later, time.Hour)
	if len(expired) != 1 || expired[0].Home != idleHome || expired[0].Backend != "worker-1" {
		t.Fatalf("expired = %+v, want only the idle guest %s on worker-1", expired, idleHome)
	}
	if _, ok := rt.OwnerOf(idleHome); ok {
		t.Fatal("an expired guest still owns its home")
	}
	if _, ok := rt.OwnerOf(busyHome); !ok {
		t.Fatal("a guest with an open terminal lost its session")
	}
	if _, ok := rt.OwnerOf("home-42"); !ok {
		t.Fatal("a signed-in user was expired")
	}

	// Once the terminal closes the clock starts from then, not from before.
	rt.terminalClosed(busyHome)
	if got := rt.ExpireGuests(time.Now().Add(30*time.Minute), time.Hour); len(got) != 0 {
		t.Fatalf("expired %+v only 30 minutes after the terminal closed", got)
	}
	if got := rt.ExpireGuests(time.Now().Add(2*time.Hour), time.Hour); len(got) != 1 || got[0].Home != busyHome {
		t.Fatalf("expired = %+v after the terminal had been closed for 2 hours", got)
	}
}

func TestRequestsKeepAGuestFromExpiring(t *testing.T) {
	rt := syncRouter(&recorder{}, nil)
	rt.AddBackend(&named{id: "worker-1", weight: 10})
	first := do(rt, "GET", "/ws_filebrowser", nil)
	c := cookieOf(t, first)
	do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
	// Right after a request, an hour is not yet over.
	if got := rt.ExpireGuests(time.Now().Add(59*time.Minute), time.Hour); len(got) != 0 {
		t.Fatalf("expired %+v 59 minutes after the last request", got)
	}
	if got := rt.ExpireGuests(time.Now().Add(61*time.Minute), time.Hour); len(got) != 1 {
		t.Fatalf("expired %+v 61 minutes after the last request", got)
	}
}

func TestTheRegistryDoesNotExpireGuestsBehindTheBackWhenSyncIsOn(t *testing.T) {
	rt := syncRouter(&recorder{}, nil)
	rt.AddBackend(&named{id: "worker-1", weight: 10})
	first := do(rt, "GET", "/ws_filebrowser", nil)
	home := "guest-" + rt.affinity.GuestID(requestWith(cookieOf(t, first)))
	// Whatever time passes, only ExpireGuests ends the session; otherwise the
	// home would be forgotten while its files still exist on two machines.
	if ctx := rt.Registry().Snapshot(); len(ctx) != 1 || !ctx[0].ExpiresAt.IsZero() {
		t.Fatalf("a guest context has an expiry although workspace sync is on: %+v", ctx)
	}
	if _, ok := rt.OwnerOf(home); !ok {
		t.Fatal("no owner")
	}
}

func TestExpireGuestsDoesNothingWithoutWorkspaceSync(t *testing.T) {
	rt := newTestRouter(&recorder{}, "/", nil)
	do(rt, "GET", "/ws_filebrowser", nil)
	if got := rt.ExpireGuests(time.Now().Add(100*time.Hour), time.Hour); got != nil {
		t.Fatalf("expired %+v without workspace sync", got)
	}
}

func TestOpenTerminalHoldsItsHomeThroughTheRouter(t *testing.T) {
	// A real WebSocket terminal through the router marks its home busy for as
	// long as it stays open.
	up := websocket.Upgrader{}
	open := make(chan struct{}, 1)
	worker := newWSBackend(&up, open)
	defer worker.Close()
	rt := NewRouter(Config{
		Site:       &recorder{},
		PathPrefix: "/",
		Secret:     []byte("secret"),
		GuestTTL:   time.Hour,
		HomeOf:     func(id Identity) string { return "guest-" + id.GuestID },
		Terminal:   terminalFor(map[string]string{"ws_python": "python"}),
	})
	rt.AddBackend(remoteTo(worker, "worker-1", "/"))
	gw := newTestServer(rt)
	defer gw.Close()

	url := "ws" + strings.TrimPrefix(gw.URL, "http") + "/ws_python"
	conn, _, err := (&websocket.Dialer{}).Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-open // the worker has the connection
	if got := rt.ExpireGuests(time.Now().Add(100*time.Hour), time.Hour); len(got) != 0 {
		t.Fatalf("a guest with an open terminal expired: %+v", got)
	}
	conn.Close()
	waitUntil2(t, "the terminal to be recorded as closed", func() bool {
		return len(rt.ExpireGuests(time.Now().Add(100*time.Hour), time.Hour)) == 1
	})
}

func newWSBackend(up *websocket.Upgrader, open chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		select {
		case open <- struct{}{}:
		default:
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func newTestServer(h http.Handler) *httptest.Server { return httptest.NewServer(h) }

func waitUntil2(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
