package gateway

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// When a worker is away its sessions are placed again only after a grace
// period. Until then a terminal cannot start, and the page is told how long
// to wait (it shows a countdown), which is what the notice is for.

type noticeLog struct {
	mu      sync.Mutex
	waits   []time.Duration
	handled bool // what the callback reports
}

func (n *noticeLog) notice(w http.ResponseWriter, r *http.Request, retryIn time.Duration) bool {
	n.mu.Lock()
	n.waits = append(n.waits, retryIn)
	handled := n.handled
	n.mu.Unlock()
	if handled {
		w.WriteHeader(http.StatusSwitchingProtocols)
	}
	return handled
}

func (n *noticeLog) calls() []time.Duration {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]time.Duration(nil), n.waits...)
}

func terminalOf(rel string) (string, int64, bool) {
	if rel == "ws" || strings.HasPrefix(rel, "ws_") {
		return strings.TrimPrefix(strings.TrimPrefix(rel, "ws"), "_"), 1, true
	}
	return "", 0, false
}

func noticeRouter(after time.Duration, picker Picker, pins PinStore, sync bool, n *noticeLog) *Router {
	cfg := Config{
		Site:           &recorder{},
		PathPrefix:     "/",
		UID:            func(r *http.Request) string { return r.Header.Get("X-Test-Uid") },
		Secret:         []byte("secret"),
		GuestTTL:       time.Hour,
		Picker:         picker,
		Pins:           pins,
		Terminal:       terminalOf,
		RelocateAfter:  after,
		TerminalNotice: n.notice,
	}
	if sync {
		cfg.HomeOf = func(id Identity) string { return "home-" + id.UID + id.GuestID }
	}
	return NewRouter(cfg)
}

func asWebSocket(r *http.Request) {
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
}

func TestTerminalIsToldHowLongToWaitWhileItsWorkerIsAway(t *testing.T) {
	n := &noticeLog{handled: true}
	w := &named{id: "worker-1", weight: 10}
	grace := time.Minute + 20*time.Second
	rt := noticeRouter(grace, &countingPicker{id: "worker-1"}, nil, true, n)
	rt.AddBackend(w)
	c := cookieOf(t, do(rt, "GET", "/ws_filebrowser", nil))
	rt.RemoveBackend(w)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(c); asWebSocket(r) })
	calls := n.calls()
	if len(calls) != 1 {
		t.Fatalf("the page was told %d time(s): %v", len(calls), calls)
	}
	if calls[0] <= 0 || calls[0] > grace {
		t.Fatalf("the wait told to the page is %v, want what is left of %v", calls[0], grace)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 80 {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("the notice handled the response, there must be no 503 on top of it")
	}
}

func TestPlainRequestsStillGetA503WithRetryAfter(t *testing.T) {
	n := &noticeLog{handled: true}
	w := &named{id: "worker-1", weight: 10}
	rt := noticeRouter(time.Minute, &countingPicker{id: "worker-1"}, nil, true, n)
	rt.AddBackend(w)
	c := cookieOf(t, do(rt, "GET", "/ws_filebrowser", nil))
	rt.RemoveBackend(w)

	// Not a WebSocket upgrade: nothing could show a message.
	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "unavailable") {
		t.Fatalf("-> %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if len(n.calls()) != 0 {
		t.Fatalf("a request that is not a WebSocket was given a terminal notice: %v", n.calls())
	}
}

func TestNoticeThatCannotBeShownFallsBackToA503(t *testing.T) {
	n := &noticeLog{handled: false}
	w := &named{id: "worker-1", weight: 10}
	rt := noticeRouter(time.Minute, &countingPicker{id: "worker-1"}, nil, true, n)
	rt.AddBackend(w)
	c := cookieOf(t, do(rt, "GET", "/ws_filebrowser", nil))
	rt.RemoveBackend(w)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(c); asWebSocket(r) })
	if rec.Code != http.StatusServiceUnavailable || len(n.calls()) != 1 {
		t.Fatalf("-> %d after %d notice(s)", rec.Code, len(n.calls()))
	}
}

func TestNoNoticeOnceTheSessionIsPlacedAgain(t *testing.T) {
	n := &noticeLog{handled: true}
	w := &named{id: "worker-1", weight: 10}
	picker := &countingPicker{id: "worker-1"}
	rt := noticeRouter(40*time.Millisecond, picker, nil, true, n)
	rt.AddBackend(w)
	c := cookieOf(t, do(rt, "GET", "/ws_filebrowser", nil))
	rt.RemoveBackend(w)
	picker.id = LocalID
	time.Sleep(80 * time.Millisecond)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(c); asWebSocket(r) })
	if rec.Code != http.StatusOK || len(n.calls()) != 0 {
		t.Fatalf("-> %d, notices %v: after the grace period the session must simply start on another node", rec.Code, n.calls())
	}
}

func TestNoNoticeWithoutWorkspaceSync(t *testing.T) {
	// Nothing will move the session, so "try again in N seconds" would be a lie.
	n := &noticeLog{handled: true}
	w := &named{id: "worker-1", weight: 10}
	rt := noticeRouter(time.Minute, &countingPicker{id: "worker-1"}, nil, false, n)
	rt.AddBackend(w)
	c := cookieOf(t, do(rt, "GET", "/ws_filebrowser", nil))
	rt.RemoveBackend(w)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.AddCookie(c); asWebSocket(r) })
	if rec.Code != http.StatusServiceUnavailable || len(n.calls()) != 0 || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("-> %d, notices %v, Retry-After %q", rec.Code, n.calls(), rec.Header().Get("Retry-After"))
	}
}

func TestSignedInUserWhoseWorkerIsAwayIsToldToo(t *testing.T) {
	// After a gateway restart the user has no live context, only the pin to
	// their worker: placing fails, and the failure carries the wait.
	n := &noticeLog{handled: true}
	pins := &memPins{m: map[string]string{"u1": "worker-1"}}
	rt := noticeRouter(time.Minute, &countingPicker{id: LocalID}, pins, true, n)

	rec := do(rt, "GET", "/ws_python", func(r *http.Request) { r.Header.Set("X-Test-Uid", "u1"); asWebSocket(r) })
	calls := n.calls()
	if len(calls) != 1 || calls[0] <= 0 || calls[0] > time.Minute {
		t.Fatalf("notices = %v (response %d)", calls, rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}

	// A plain request keeps the text it always had.
	rec = do(rt, "GET", "/ws_python", func(r *http.Request) { r.Header.Set("X-Test-Uid", "u1") })
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "workspace node unavailable") {
		t.Fatalf("-> %d %q", rec.Code, rec.Body.String())
	}
}

func TestRetryInIsOnlyForAWorkerThatIsAwayWithSync(t *testing.T) {
	rt := noticeRouter(time.Minute, &countingPicker{id: LocalID}, nil, true, &noticeLog{})
	if rt.retryIn(LocalID) != 0 || rt.retryIn("") != 0 {
		t.Fatal("the gateway's own backend is never waited for")
	}
	if d := rt.retryIn("worker-9"); d <= 0 || d > time.Minute {
		t.Fatalf("a worker the gateway has not seen since it started has the whole grace period: %v", d)
	}
	plain := noticeRouter(time.Minute, &countingPicker{id: LocalID}, nil, false, &noticeLog{})
	if plain.retryIn("worker-9") != 0 {
		t.Fatal("without workspace sync nothing moves the session")
	}
}
