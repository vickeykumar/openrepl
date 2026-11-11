package server

import (
	"github.com/gorilla/websocket"
	"sync"
	"sync/atomic"
	"time"
)

type wsWrapper struct {
	*websocket.Conn
	peer *peerEnd // told when a read fails: the client's close, or the connection lost
}

// peerEnd lets the handler wait for the client's answer to the close frame without a fixed sleep.
type peerEnd struct {
	gone   chan struct{} // closed when the terminal's reader sees the connection end
	once   sync.Once
	reader int32 // 1 once the terminal's reader owns reads on the connection
}

func newPeerEnd() *peerEnd { return &peerEnd{gone: make(chan struct{})} }

func (p *peerEnd) reading() { atomic.StoreInt32(&p.reader, 1) }

func (p *peerEnd) ended() { p.once.Do(func() { close(p.gone) }) }

// waitForReply returns when the client has answered the close frame (or the connection is lost), or after
// max. While the terminal's reader runs, it is the one that sees the answer; otherwise nobody reads the
// connection and this reads it.
func (p *peerEnd) waitForReply(conn *websocket.Conn, max time.Duration) {
	if atomic.LoadInt32(&p.reader) == 1 {
		select {
		case <-p.gone:
		case <-time.After(max):
		}
		return
	}
	conn.SetReadDeadline(time.Now().Add(max))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (wsw *wsWrapper) Write(p []byte) (n int, err error) {
	writer, err := wsw.Conn.NextWriter(websocket.TextMessage)
	if err != nil {
		return 0, err
	}
	defer writer.Close()
	return writer.Write(p)
}

func (wsw *wsWrapper) Read(p []byte) (n int, err error) {
	for {
		wsw.Conn.SetReadDeadline(time.Now().Add(5 * time.Minute)) // only 5 min idle timeout for services are allowed
		msgType, reader, err := wsw.Conn.NextReader()
		if err != nil {
			if wsw.peer != nil {
				wsw.peer.ended()
			}
			return 0, err
		}

		if msgType != websocket.TextMessage {
			continue
		}

		return reader.Read(p)
	}
}
