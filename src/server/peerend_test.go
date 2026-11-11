package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The handler used to sleep 2 s after the close frame. It now waits for the client's answer, and no longer
// than that.
func peerServer(t *testing.T, reader bool, answer bool) (elapsed chan time.Duration, url string) {
	t.Helper()
	elapsed = make(chan time.Duration, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		peer := newPeerEnd()
		if reader {
			peer.reading()
			go func() { // the terminal's reader
				w := &wsWrapper{Conn: conn, peer: peer}
				buf := make([]byte, 64)
				for {
					if _, err := w.Read(buf); err != nil {
						return
					}
				}
			}()
		}
		start := time.Now()
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "x"), time.Now().Add(time.Second))
		peer.waitForReply(conn, 2*time.Second)
		elapsed <- time.Since(start)
	}))
	t.Cleanup(srv.Close)
	return elapsed, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialAndMaybeAnswer(t *testing.T, url string, answer bool) {
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		for { // the browser answers a close frame by itself, while it reads
			if answer {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			} else {
				time.Sleep(10 * time.Second)
				return
			}
		}
	}()
}

func TestTheCloseDoesNotWaitAFixedTime(t *testing.T) {
	for _, reader := range []bool{true, false} {
		el, url := peerServer(t, reader, true)
		dialAndMaybeAnswer(t, url, true)
		if d := <-el; d > time.Second {
			t.Errorf("reader=%v: waited %v although the client answered", reader, d)
		}
	}
}

func TestTheCloseGivesUpOnAClientThatDoesNotAnswer(t *testing.T) {
	for _, reader := range []bool{true, false} {
		el, url := peerServer(t, reader, false)
		dialAndMaybeAnswer(t, url, false)
		d := <-el
		if d < 1500*time.Millisecond || d > 3*time.Second {
			t.Errorf("reader=%v: waited %v, want about 2 s", reader, d)
		}
	}
}
