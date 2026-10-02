package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

// A terminal's WebSocket is bridged to a worker. When the worker goes away
// under an open terminal, its connection just ends: the browser sees an
// abnormal close, says "connection lost", and has no way to know that the
// session will be placed again in a minute or two. closeGuard sits on the
// worker's end of the bridge and, when the stream ends without a close frame
// from the worker and the worker is known to be away, ends the browser's
// WebSocket with the same reason the gateway gives a terminal that cannot
// start (AwayReason), so the page starts its countdown at once.

// AwayReason is the WebSocket close reason that says the execution node is
// away and for how long: "execution node is away: retry in 80s". The page
// (src/js/src/webtty.ts) parses it to count down. The time is in whole
// seconds, rounded up, and never 0.
func AwayReason(retryIn time.Duration) string {
	secs := int(math.Ceil(retryIn.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("execution node is away: retry in %ds", secs)
}

// EndedReason is the WebSocket close reason of a terminal that an admin ended.
// The page shows what follows "site notice: " (src/js/src/webtty.ts).
const EndedReason = "site notice: An admin ended this session."

type awayKey struct{}

// withAway attaches a function that returns how long until the session of a
// request is placed again, 0 if it is not going to be, and a negative time if
// the terminal was closed on purpose (by an admin), which the browser is told
// with EndedReason.
func withAway(r *http.Request, retryIn func() time.Duration) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), awayKey{}, retryIn))
}

func awayOf(r *http.Request) func() time.Duration {
	f, _ := r.Context().Value(awayKey{}).(func() time.Duration)
	return f
}

// wsScan follows the frames of a WebSocket, in the direction from the worker
// to the browser, far enough to know whether a close frame has gone by and
// whether the stream is at a frame boundary.
type wsScan struct {
	hdr    [14]byte
	hlen   int    // header bytes of the current frame seen so far
	hneed  int    // header bytes it has in all, once known
	skip   uint64 // payload bytes of the current frame still to come
	closed bool   // a close frame has been seen
}

func (s *wsScan) feed(b []byte) {
	for len(b) > 0 {
		if s.skip > 0 {
			n := uint64(len(b))
			if n > s.skip {
				n = s.skip
			}
			s.skip -= n
			b = b[n:]
			continue
		}
		s.hdr[s.hlen] = b[0]
		s.hlen++
		b = b[1:]
		if s.hlen == 2 {
			need := 2
			switch s.hdr[1] & 0x7f {
			case 126:
				need += 2
			case 127:
				need += 8
			}
			if s.hdr[1]&0x80 != 0 {
				need += 4 // a mask; a server does not send one, but the format allows it
			}
			s.hneed = need
		}
		if s.hlen >= 2 && s.hlen == s.hneed {
			if s.hdr[0]&0x0f == 0x8 {
				s.closed = true
			}
			n := uint64(s.hdr[1] & 0x7f)
			switch n {
			case 126:
				n = uint64(binary.BigEndian.Uint16(s.hdr[2:4]))
			case 127:
				n = binary.BigEndian.Uint64(s.hdr[2:10])
			}
			s.skip = n
			s.hlen, s.hneed = 0, 0
		}
	}
}

func (s *wsScan) atBoundary() bool { return s.skip == 0 && s.hlen == 0 }

const (
	guardHeaders = iota // reading the HTTP response that upgrades the connection
	guardFrames         // after "101 Switching Protocols": WebSocket frames
	guardOther          // not an upgrade; left alone
)

// closeGuard is the worker's end of a bridged WebSocket (see above). It only
// ever adds bytes at the end, and only when the stream has ended abnormally at
// a frame boundary; everything else passes through unchanged.
type closeGuard struct {
	net.Conn
	away func() time.Duration
	// wait is how long to wait for the gateway to notice that the worker is
	// gone: the stream usually ends a moment before the worker is marked away.
	wait time.Duration

	phase   int
	status  []byte // the first bytes of the response, to see that it is a 101
	tail    int    // how much of the blank line that ends the headers has been seen
	scan    wsScan
	pending []byte
	ended   bool
}

func (g *closeGuard) Read(p []byte) (int, error) {
	if len(g.pending) > 0 {
		n := copy(p, g.pending)
		g.pending = g.pending[n:]
		return n, nil
	}
	if g.ended {
		return 0, io.EOF
	}
	n, err := g.Conn.Read(p)
	if n > 0 {
		g.observe(p[:n])
	}
	if err == nil {
		return n, nil
	}
	frame := g.closeFrame()
	if frame == nil {
		return n, err
	}
	g.ended = true
	g.pending = frame
	if n > 0 {
		return n, nil // the data first, the close frame on the next read
	}
	m := copy(p, g.pending)
	g.pending = g.pending[m:]
	return m, nil
}

func (g *closeGuard) observe(b []byte) {
	for len(b) > 0 {
		switch g.phase {
		case guardHeaders:
			c := b[0]
			b = b[1:]
			if len(g.status) < 12 {
				g.status = append(g.status, c)
				if len(g.status) == 12 && string(g.status) != "HTTP/1.1 101" {
					g.phase = guardOther
					return
				}
			}
			switch {
			case c == '\r' && (g.tail == 0 || g.tail == 2):
				g.tail++
			case c == '\n' && (g.tail == 1 || g.tail == 3):
				g.tail++
			case c == '\r':
				g.tail = 1
			default:
				g.tail = 0
			}
			if g.tail == 4 {
				if len(g.status) < 12 {
					g.phase = guardOther
					return
				}
				g.phase = guardFrames
			}
		case guardFrames:
			g.scan.feed(b)
			return
		default:
			return
		}
	}
}

// closeFrame returns the close frame to send to the browser in place of the
// worker's missing one, or nil if the stream did not end the way that
// warrants one.
func (g *closeGuard) closeFrame() []byte {
	if g.phase != guardFrames || g.scan.closed || !g.scan.atBoundary() || g.away == nil {
		return nil
	}
	deadline := time.Now().Add(g.wait)
	var retryIn time.Duration
	for {
		if retryIn = g.away(); retryIn < 0 {
			return closeFrame(EndedReason) // ended on purpose: no reason to wait
		} else if retryIn > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			return nil // the worker is not known to be away: say nothing
		}
		time.Sleep(50 * time.Millisecond)
	}
	return closeFrame(AwayReason(retryIn))
}

// closeFrame is an unmasked WebSocket close frame with status 1000 and the reason.
func closeFrame(reason string) []byte {
	payload := append([]byte{0x03, 0xE8}, reason...)            // 1000, then the reason
	return append([]byte{0x88, byte(len(payload))}, payload...) // FIN + close, unmasked
}
