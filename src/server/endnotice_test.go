package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"webtty"
)

// The reason a terminal ended must reach the page as an ordinary message before the close frame: a proxy in
// front of the server can hold the close frame back for ~20 s or lose it (Render's edge did).
func TestEndNoticeComesBeforeTheCloseFrame(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		writeEndNotice(conn, websocket.CloseNormalClosure, "bash: parent ended")
		conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bash: parent ended"), time.Now().Add(time.Second))
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))

	_, m, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("the first thing read must be the notice, got error %v", err)
	}
	if len(m) == 0 || m[0] != webtty.End {
		t.Fatalf("first message = %q, want type %q", m, string(rune(webtty.End)))
	}
	var end struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(m[1:], &end); err != nil {
		t.Fatal(err)
	}
	if end.Code != websocket.CloseNormalClosure || end.Reason != "bash: parent ended" {
		t.Errorf("notice = %+v", end)
	}
	// and the close frame itself still follows, for clients that do get it
	_, _, err = c.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok || ce.Code != websocket.CloseNormalClosure || ce.Text != "bash: parent ended" {
		t.Errorf("after the notice: %v, want the close frame", err)
	}
}
