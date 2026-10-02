package gateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const upgradeResponse = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: x\r\n\r\n"

// frame builds an unmasked WebSocket frame, as a server sends it.
func frame(opcode byte, payload []byte) []byte {
	out := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		out = append(out, byte(n))
	case n < 1<<16:
		out = append(out, 126, byte(n>>8), byte(n))
	default:
		out = append(out, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, payload...)
}

// scripted is a connection that yields its chunks, then an error.
type scripted struct {
	net.Conn
	chunks [][]byte
	err    error
}

func (s *scripted) Read(p []byte) (int, error) {
	if len(s.chunks) == 0 {
		return 0, s.err
	}
	n := copy(p, s.chunks[0])
	if n < len(s.chunks[0]) {
		s.chunks[0] = s.chunks[0][n:]
	} else {
		s.chunks = s.chunks[1:]
	}
	return n, nil
}

func split(b []byte, size int) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		n := size
		if n > len(b) {
			n = len(b)
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func guardOver(stream []byte, chunk int, away func() time.Duration) *closeGuard {
	return &closeGuard{
		Conn: &scripted{chunks: split(stream, chunk), err: io.EOF},
		away: away,
		wait: 40 * time.Millisecond,
	}
}

func fixed(d time.Duration) func() time.Duration { return func() time.Duration { return d } }

func TestGuardEndsABrokenStreamWithTheAwayReason(t *testing.T) {
	stream := append([]byte(upgradeResponse), frame(1, []byte("hello"))...)
	stream = append(stream, frame(1, bytes.Repeat([]byte("x"), 300))...)
	stream = append(stream, frame(2, bytes.Repeat([]byte("y"), 70000))...)
	// However the stream is cut into reads, the result is the same.
	for _, chunk := range []int{1, 2, 3, 7, 64, 4096, 1 << 20} {
		g := guardOver(stream, chunk, fixed(80*time.Second))
		got, err := io.ReadAll(g)
		if err != nil {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
		want := append(append([]byte(nil), stream...), frame(8, append([]byte{0x03, 0xE8}, "execution node is away: retry in 80s"...))...)
		if !bytes.Equal(got, want) {
			t.Fatalf("chunk %d: the bytes after the stream are %q, want the close frame", chunk, got[len(stream):])
		}
	}
}

func TestGuardLeavesAWorkersOwnCloseAlone(t *testing.T) {
	closeFrame := frame(8, append([]byte{0x03, 0xE8}, "client"...))
	stream := append(append([]byte(upgradeResponse), frame(1, []byte("bye"))...), closeFrame...)
	for _, chunk := range []int{1, 5, 4096} {
		got, _ := io.ReadAll(guardOver(stream, chunk, fixed(time.Minute)))
		if !bytes.Equal(got, stream) {
			t.Fatalf("chunk %d: the guard added %q after the worker's close", chunk, got[len(stream):])
		}
	}
}

func TestGuardAddsNothingInTheMiddleOfAFrame(t *testing.T) {
	whole := frame(1, []byte("a long message that never arrives"))
	for _, cut := range []int{1, 2, 10, len(whole) - 1} { // inside the header, then the payload
		stream := append([]byte(upgradeResponse), whole[:cut]...)
		got, _ := io.ReadAll(guardOver(stream, 4096, fixed(time.Minute)))
		if !bytes.Equal(got, stream) {
			t.Fatalf("cut at %d: bytes were added to a half-sent frame: %q", cut, got[len(stream):])
		}
	}
}

func TestGuardSaysNothingWhenTheWorkerIsNotKnownToBeAway(t *testing.T) {
	stream := append([]byte(upgradeResponse), frame(1, []byte("hello"))...)
	g := guardOver(stream, 4096, fixed(0))
	start := time.Now()
	got, err := io.ReadAll(g)
	if err != nil || !bytes.Equal(got, stream) {
		t.Fatalf("got %q, %v", got, err)
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("the guard did not wait for the gateway to notice that the worker is gone")
	}
	// An error other than EOF reaches the proxy unchanged.
	g = guardOver(stream, 4096, fixed(0))
	g.Conn.(*scripted).err = io.ErrUnexpectedEOF
	if _, err := io.ReadAll(g); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v", err)
	}
}

func TestGuardTellsTheBrowserAtOnceWhenAnAdminEndedTheTerminal(t *testing.T) {
	stream := append([]byte(upgradeResponse), frame(1, []byte("hello"))...)
	g := guardOver(stream, 4096, fixed(-1))
	g.wait = 5 * time.Second
	start := time.Now()
	got, err := io.ReadAll(g)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the guard waited %v for a terminal that an admin ended", time.Since(start))
	}
	want := append(append([]byte{}, stream...), closeFrame(EndedReason)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want the stream followed by a close frame with %q", got, EndedReason)
	}
}

func TestGuardWaitsForTheWorkerToBeMarkedAway(t *testing.T) {
	var calls int32
	away := func() time.Duration {
		if atomic.AddInt32(&calls, 1) < 3 {
			return 0 // the stream ended a moment before the gateway noticed
		}
		return 7 * time.Second
	}
	stream := append([]byte(upgradeResponse), frame(1, []byte("hello"))...)
	g := guardOver(stream, 4096, away)
	g.wait = time.Second
	got, _ := io.ReadAll(g)
	if !strings.HasSuffix(string(got), "execution node is away: retry in 7s") {
		t.Fatalf("got %q", got)
	}
}

func TestGuardIgnoresAnythingButAnUpgrade(t *testing.T) {
	for _, stream := range []string{
		"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 4\r\n\r\nbody",
		"HTTP/1.1 200 OK\r\n\r\n",
		"HTTP/1.1 10", // not even a status line
	} {
		got, _ := io.ReadAll(guardOver([]byte(stream), 3, fixed(time.Minute)))
		if string(got) != stream {
			t.Fatalf("%q was changed into %q", stream, got)
		}
	}
	// No upgrade has happened yet: nothing to close.
	got, _ := io.ReadAll(guardOver([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"), 4096, fixed(time.Minute)))
	if !strings.HasSuffix(string(got), "websocket\r\n") {
		t.Fatalf("got %q", got)
	}
}

func TestGuardReturnsDataThatArrivesWithTheError(t *testing.T) {
	// Read may return bytes and an error together.
	c := &dataThenError{data: append([]byte(upgradeResponse), frame(1, []byte("last words"))...)}
	g := &closeGuard{Conn: c, away: fixed(5 * time.Second), wait: 20 * time.Millisecond}
	got, _ := io.ReadAll(g)
	if !bytes.HasPrefix(got, c.data) || !strings.HasSuffix(string(got), "retry in 5s") {
		t.Fatalf("got %q", got)
	}
}

type dataThenError struct {
	net.Conn
	data []byte
	done bool
}

func (d *dataThenError) Read(p []byte) (int, error) {
	if d.done {
		return 0, io.EOF
	}
	d.done = true
	return copy(p, d.data), io.EOF
}

func TestAwayReasonRoundsUpAndNeverSaysZero(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "execution node is away: retry in 1s"},
		{200 * time.Millisecond, "execution node is away: retry in 1s"},
		{time.Second, "execution node is away: retry in 1s"},
		{1100 * time.Millisecond, "execution node is away: retry in 2s"},
		{2 * time.Minute, "execution node is away: retry in 120s"},
	} {
		if got := AwayReason(c.d); got != c.want {
			t.Errorf("AwayReason(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// Through the real bridge: a worker that dies under an open terminal. The
// browser must get a proper close that carries the wait, not a dropped
// connection.
func TestBrowserIsToldAtOnceWhenItsWorkerDropsUnderAnOpenTerminal(t *testing.T) {
	var abrupt int32 = 1
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{"webtty"}}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.WriteMessage(websocket.TextMessage, []byte("1prompt"))
		if atomic.LoadInt32(&abrupt) == 1 {
			c.UnderlyingConn().Close() // the worker dies: no close frame
			return
		}
		c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client"), time.Now().Add(time.Second))
		c.Close()
	}))
	defer worker.Close()
	addr := strings.TrimPrefix(worker.URL, "http://")

	b := NewRemoteBackend(RemoteConfig{
		ID:   "worker-1",
		Dial: func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", addr) },
	})
	var wait int64 = int64(50 * time.Second)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withAway(r, func() time.Duration { return time.Duration(atomic.LoadInt64(&wait)) })
		b.Serve(w, r)
	}))
	defer gw.Close()
	url := "ws" + strings.TrimPrefix(gw.URL, "http")
	d := websocket.Dialer{Subprotocols: []string{"webtty"}}

	read := func() *websocket.CloseError {
		c, _, err := d.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, m, err := c.ReadMessage()
		if err != nil || string(m) != "1prompt" {
			t.Fatalf("the worker's output did not arrive: %q, %v", m, err)
		}
		_, _, err = c.ReadMessage()
		ce, ok := err.(*websocket.CloseError)
		if !ok {
			t.Fatalf("the connection ended with %v, not a close", err)
		}
		return ce
	}

	if ce := read(); ce.Code != websocket.CloseNormalClosure || ce.Text != "execution node is away: retry in 50s" {
		t.Fatalf("the browser saw close %d %q", ce.Code, ce.Text)
	}

	// A worker that closes properly keeps its own reason.
	atomic.StoreInt32(&abrupt, 0)
	if ce := read(); ce.Code != websocket.CloseNormalClosure || ce.Text != "client" {
		t.Fatalf("a normal close became %d %q", ce.Code, ce.Text)
	}

	// A drop that is not the worker being away (nothing will place the session
	// again) stays what it was: an abnormal close.
	atomic.StoreInt32(&abrupt, 1)
	atomic.StoreInt64(&wait, 0)
	if ce := read(); ce.Code != websocket.CloseAbnormalClosure {
		t.Fatalf("the browser saw close %d %q, want an ordinary drop", ce.Code, ce.Text)
	}
}
