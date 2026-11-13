package livepreview

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PingEvery is how long a proxied WebSocket may be quiet before the server sends it a ping. An edge in front of the site (the
// host's proxy, a CDN) drops a connection that carries nothing for a few minutes; a ping every so often keeps an idle one, a
// dev server waiting for a change, alive.
var PingEvery = 25 * time.Second

// keepAlive wraps the hijacked connection of a proxied WebSocket so that pings are sent while it is quiet. The proxy copies the
// program's bytes to the page; this reads them as frames (after the HTTP answer that opens the socket) and sends a ping, the two
// bytes 0x89 0x00, only between two frames, so it can never land inside a message. A pong that the page sends back goes to the
// program, which has to ignore a pong it did not ask for (RFC 6455, 5.5.3).
type keepAlive struct {
	net.Conn
	mu       sync.Mutex
	head     []byte // the HTTP answer, until its blank line
	inHeader bool
	state    int    // 0 reading a frame header, 1 in a payload
	hdr      []byte // the bytes of the header so far
	left     int64  // payload bytes still to come
	last     time.Time
	stop     chan struct{}
	once     sync.Once
}

func newKeepAlive(c net.Conn, every time.Duration) *keepAlive {
	k := &keepAlive{Conn: c, inHeader: true, last: time.Now(), stop: make(chan struct{})}
	go func() {
		t := time.NewTicker(every / 2)
		defer t.Stop()
		for {
			select {
			case <-k.stop:
				return
			case <-t.C:
				k.mu.Lock()
				if !k.inHeader && k.state == 0 && len(k.hdr) == 0 && time.Since(k.last) >= every {
					k.Conn.Write([]byte{0x89, 0x00})
					k.last = time.Now()
				}
				k.mu.Unlock()
			}
		}
	}()
	return k
}

func (k *keepAlive) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.last = time.Now()
	k.scan(p)
	n, err := k.Conn.Write(p)
	return n, err
}

func (k *keepAlive) Close() error {
	k.once.Do(func() { close(k.stop) })
	return k.Conn.Close()
}

// scan follows the bytes that are being sent, to know where the frames end.
func (k *keepAlive) scan(p []byte) {
	for len(p) > 0 {
		if k.inHeader {
			k.head = append(k.head, p...)
			if i := bytes.Index(k.head, []byte("\r\n\r\n")); i >= 0 {
				p = k.head[i+4:]
				k.inHeader = false
				k.head = nil
				p = append([]byte{}, p...)
				continue
			}
			if len(k.head) > 64<<10 {
				k.head = k.head[len(k.head)-4:]
			}
			return
		}
		if k.state == 1 {
			n := int64(len(p))
			if n > k.left {
				n = k.left
			}
			k.left -= n
			p = p[n:]
			if k.left == 0 {
				k.state = 0
			}
			continue
		}
		// a frame header: 2 bytes, then 2 or 8 bytes of length when the length field says so (a server does not mask)
		for len(p) > 0 {
			k.hdr = append(k.hdr, p[0])
			p = p[1:]
			if size, done := frameHeader(k.hdr); done {
				k.hdr = k.hdr[:0]
				k.left = size
				if size > 0 {
					k.state = 1
				}
				break
			}
		}
	}
}

// frameHeader says whether h is a whole frame header, and the length of the payload that follows it.
func frameHeader(h []byte) (payload int64, done bool) {
	if len(h) < 2 {
		return 0, false
	}
	need := 2
	l := int64(h[1] & 0x7f)
	switch l {
	case 126:
		need = 4
	case 127:
		need = 10
	}
	if h[1]&0x80 != 0 {
		need += 4
	}
	if len(h) < need {
		return 0, false
	}
	switch l {
	case 126:
		l = int64(h[2])<<8 | int64(h[3])
	case 127:
		l = 0
		for _, b := range h[2:10] {
			l = l<<8 | int64(b)
		}
	}
	return l, true
}

// keepAliveWriter is the ResponseWriter of an upgrade request: when the proxy takes the connection over, the ping wrapper is put on it.
type keepAliveWriter struct {
	http.ResponseWriter
	every time.Duration
}

func (w keepAliveWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	c, rw, err := h.Hijack()
	if err != nil {
		return nil, nil, err
	}
	k := newKeepAlive(c, w.every)
	// the proxy writes the answer that opens the socket through the buffer it is given, so the buffer has to write to k
	return k, bufio.NewReadWriter(rw.Reader, bufio.NewWriter(k)), nil
}

func (w keepAliveWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func isWebSocket(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}
