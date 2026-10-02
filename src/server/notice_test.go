package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"webtty"
)

func noticeServer(t *testing.T, retryIn time.Duration) string {
	t.Helper()
	s := &Server{upgrader: &websocket.Upgrader{Subprotocols: webtty.Protocols}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.terminalNotice(w, r, retryIn)
	}))
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}

// The page starts its countdown from the close reason, so the format is a
// contract with src/js/src/webtty.ts.
func TestTerminalNoticeTellsThePageHowLongToWait(t *testing.T) {
	url := noticeServer(t, 80*time.Second)
	d := websocket.Dialer{Subprotocols: webtty.Protocols}
	c, _, err := d.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The page sends its init message first, as it does for any terminal.
	c.WriteMessage(websocket.TextMessage, []byte(`{"Arguments":"","AuthToken":"","Payload":{}}`))

	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, m, err := c.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("the first thing the page gets must be the close, got message %q, error %v", m, err)
	}
	if ce.Code != websocket.CloseNormalClosure || ce.Text != "execution node is away: retry in 80s" {
		t.Fatalf("close = %d %q", ce.Code, ce.Text)
	}
}

func TestTerminalNoticeRefusesAnotherOriginLikeAnyTerminal(t *testing.T) {
	url := noticeServer(t, time.Minute)
	d := websocket.Dialer{Subprotocols: webtty.Protocols}
	h := http.Header{"Origin": []string{"http://evil.example"}}
	_, resp, err := d.Dial(url, h)
	if err == nil {
		t.Fatal("a cross-origin WebSocket was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response %v, want 403 from the upgrader", resp)
	}
}

func TestRelocateAfterOption(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"2m", 2 * time.Minute, true},
		{"30s", 30 * time.Second, true},
		{"1s", time.Second, true},
		{"1m30s", 90 * time.Second, true},
		{"", 0, false},
		{"0s", 0, false},
		{"500ms", 0, false},
		{"-1m", 0, false},
		{"soon", 0, false},
		{"120", 0, false}, // a bare number is ambiguous
	} {
		o := &Options{RelocateAfter: c.in}
		d, err := o.RelocateAfterDuration()
		if (err == nil) != c.ok || d != c.want {
			t.Errorf("%q -> %v, %v; want %v, ok=%v", c.in, d, err, c.want, c.ok)
		}
	}
	bad := &Options{Mode: ModeStandalone, RelocateAfter: "soon"}
	if bad.Validate() == nil {
		t.Error("Validate accepted a --relocate-after that is not a duration")
	}
	good := &Options{Mode: ModeStandalone, RelocateAfter: "2m"}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
