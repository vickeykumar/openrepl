package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// pingInterval keeps the carrier active through proxies that drop idle
// connections. A peer that misses pings for readTimeout is considered gone.
const (
	pingInterval = 20 * time.Second
	readTimeout  = 3 * pingInterval
	writeTimeout = 30 * time.Second
)

// wsConn carries a byte stream over a WebSocket as binary frames, so the SSH
// connection can run on the gateway's normal HTTP(S) port.
type wsConn struct {
	ws *websocket.Conn

	rmu    sync.Mutex
	reader io.Reader // current frame, nil when it is used up

	wmu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}
}

func newWSConn(ws *websocket.Conn) *wsConn {
	c := &wsConn{ws: ws, closed: make(chan struct{})}
	extend := func() { ws.SetReadDeadline(time.Now().Add(readTimeout)) }
	extend()
	ws.SetPongHandler(func(string) error { extend(); return nil })
	ws.SetPingHandler(func(data string) error {
		extend()
		// WriteControl may run alongside a data write.
		err := ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout))
		if _, ok := err.(net.Error); ok {
			return nil
		}
		return err
	})
	go c.pingLoop()
	return c
}

func (c *wsConn) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				c.Close()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.reader == nil {
			typ, r, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if typ != websocket.BinaryMessage {
				continue // not part of the stream
			}
			c.reader = r
			c.ws.SetReadDeadline(time.Now().Add(readTimeout))
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.ws.Close()
	})
	return err
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

// The carrier manages its own deadlines (ping/pong). The SSH layer sets one
// only for the handshake, which handshakeTimeout bounds instead.
func (c *wsConn) SetDeadline(time.Time) error      { return nil }
func (c *wsConn) SetReadDeadline(time.Time) error  { return nil }
func (c *wsConn) SetWriteDeadline(time.Time) error { return nil }
