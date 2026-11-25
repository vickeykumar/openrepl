package ideusage

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
)

// Gate decides about each data frame a terminal's WebSocket is about to send to the browser. first is the first byte of its
// payload (the type of a message of the terminal protocol: '1' is output), size is the whole frame in bytes. It may block (a
// pause) and returns nil to send the frame. An error ends the terminal: End gives the frames that tell the page why.
type Gate interface {
	Frame(first byte, size int64) error
	End(err error) []byte
}

// GateConn is the connection of a hijacked WebSocket, seen from the server: every frame the server writes goes through gate before it
// goes on. It follows the frames of the stream without keeping their payload, so that a frame is decided on at its start, and a frame
// that is let through goes on as it came. The bytes of the HTTP answer that opens the connection are passed.
func GateConn(c net.Conn, g Gate) net.Conn { return &gateConn{Conn: c, gate: g} }

type gateConn struct {
	net.Conn
	gate Gate

	mu     sync.Mutex
	state  int // 0 start, 1 in the HTTP answer, 2 at a frame start, 3 in a payload
	tail   []byte
	hdr    []byte // the header of the frame being read, and its first payload byte
	remain int64  // payload bytes of the current frame still to pass
	dead   bool
}

const (
	stStart = iota
	stHTTP
	stFrame
	stPayload
)

// header size of a server frame (no mask) from its first two bytes: 2, 4 or 10.
func headerSize(b1 byte) int {
	switch b1 & 0x7f {
	case 126:
		return 4
	case 127:
		return 10
	}
	return 2
}

func payloadLen(h []byte) int64 {
	switch h[1] & 0x7f {
	case 126:
		return int64(binary.BigEndian.Uint16(h[2:4]))
	case 127:
		return int64(binary.BigEndian.Uint64(h[2:10]))
	}
	return int64(h[1] & 0x7f)
}

func (g *gateConn) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dead {
		return 0, io.ErrClosedPipe
	}
	total := len(p)
	for len(p) > 0 {
		switch g.state {
		case stStart:
			if bytes.HasPrefix(p, []byte("HTTP/")) {
				g.state = stHTTP
				continue
			}
			g.state = stFrame
		case stHTTP:
			// pass bytes up to the empty line that ends the answer
			buf := append(g.tail, p...)
			i := bytes.Index(buf, []byte("\r\n\r\n"))
			if i < 0 {
				if _, err := g.Conn.Write(p); err != nil {
					return 0, err
				}
				if len(buf) > 3 {
					buf = buf[len(buf)-3:]
				}
				g.tail = append([]byte(nil), buf...)
				return total, nil
			}
			n := i + 4 - len(g.tail)
			if _, err := g.Conn.Write(p[:n]); err != nil {
				return 0, err
			}
			p = p[n:]
			g.tail = nil
			g.state = stFrame
		case stFrame:
			need := 2
			if len(g.hdr) >= 2 {
				need = headerSize(g.hdr[1])
			}
			op := byte(0)
			if len(g.hdr) > 0 {
				op = g.hdr[0] & 0x0f
			}
			// a data frame is decided on once its header and its first payload byte are here
			for len(g.hdr) < need && len(p) > 0 {
				g.hdr = append(g.hdr, p[0])
				p = p[1:]
				if len(g.hdr) == 2 {
					need = headerSize(g.hdr[1])
				}
			}
			if len(g.hdr) < need {
				return total, nil
			}
			op = g.hdr[0] & 0x0f
			plen := payloadLen(g.hdr)
			data := (op == 0x1 || op == 0x2 || op == 0x0) && plen > 0
			if data && len(g.hdr) == need {
				if len(p) == 0 {
					return total, nil
				}
				g.hdr = append(g.hdr, p[0])
				p = p[1:]
			}
			if data {
				first := g.hdr[need]
				if op == 0x0 {
					first = 0 // a continuation: not the start of a message
				}
				if err := g.gate.Frame(first, int64(need)+plen); err != nil {
					g.dead = true
					g.Conn.Write(g.gate.End(err))
					g.Conn.Close()
					return 0, err
				}
			}
			if _, err := g.Conn.Write(g.hdr); err != nil {
				return 0, err
			}
			g.remain = plen
			if len(g.hdr) > need {
				g.remain--
			}
			g.hdr = g.hdr[:0]
			if g.remain > 0 {
				g.state = stPayload
			}
		case stPayload:
			n := int64(len(p))
			if n > g.remain {
				n = g.remain
			}
			if _, err := g.Conn.Write(p[:n]); err != nil {
				return 0, err
			}
			p = p[n:]
			g.remain -= n
			if g.remain == 0 {
				g.state = stFrame
			}
		}
	}
	return total, nil
}

// TextFrame is a final, unmasked text frame, as a server sends it.
func TextFrame(payload []byte) []byte { return frame(0x81, payload) }

// CloseFrame is a close frame with a code and a reason.
func CloseFrame(code int, reason string) []byte {
	if len(reason) > 120 {
		reason = reason[:120]
	}
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p, uint16(code))
	copy(p[2:], reason)
	return frame(0x88, p)
}

func frame(first byte, payload []byte) []byte {
	var h []byte
	switch n := len(payload); {
	case n < 126:
		h = []byte{first, byte(n)}
	case n < 1<<16:
		h = []byte{first, 126, byte(n >> 8), byte(n)}
	default:
		h = make([]byte, 10)
		h[0], h[1] = first, 127
		binary.BigEndian.PutUint64(h[2:], uint64(n))
	}
	return append(h, payload...)
}
