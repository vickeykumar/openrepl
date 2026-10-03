package tunnel

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// deadline is a resettable timer that closes a channel when it expires. It
// follows the pipeDeadline type in the Go standard library's net/pipe.go.
type deadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{} // closed when the deadline passes
}

func makeDeadline() deadline {
	return deadline{cancel: make(chan struct{})}
}

// set arms the deadline; the zero time disarms it.
func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // wait for the timer callback to finish and close cancel
	}
	d.timer = nil

	closed := isClosed(d.cancel)
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		d.timer = time.AfterFunc(dur, func() { close(d.cancel) })
		return
	}
	if !closed {
		close(d.cancel)
	}
}

// wait returns a channel that is closed once the deadline has passed.
func (d *deadline) wait() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// tunnelAddr is the address of one end of a tunnel stream.
type tunnelAddr string

func (a tunnelAddr) Network() string { return "openrepl-tunnel" }
func (a tunnelAddr) String() string  { return string(a) }

// stream is the part of an ssh.Channel a streamConn needs.
type stream interface {
	io.Reader
	io.Writer
	io.Closer
}

// streamConn turns one SSH channel into a net.Conn with working deadlines,
// so that an http.Server, an http.Transport or a WebSocket can run on it.
type streamConn struct {
	s      stream
	local  net.Addr
	remote net.Addr

	chunks chan []byte // from the pump goroutine
	rest   []byte      // unread part of the last chunk
	rdErr  error       // set before chunks is closed

	rd deadline
	wr deadline

	closeOnce sync.Once
	closed    chan struct{}
}

func newStreamConn(s stream, local, remote string) *streamConn {
	c := &streamConn{
		s:      s,
		local:  tunnelAddr(local),
		remote: tunnelAddr(remote),
		chunks: make(chan []byte),
		rd:     makeDeadline(),
		wr:     makeDeadline(),
		closed: make(chan struct{}),
	}
	go c.pump()
	return c
}

// pump moves data from the stream to Read one chunk at a time, so a slow
// reader still applies back-pressure to the sender.
func (c *streamConn) pump() {
	for {
		buf := make([]byte, 32*1024)
		n, err := c.s.Read(buf)
		if n > 0 {
			select {
			case c.chunks <- buf[:n]:
			case <-c.closed:
				return
			}
		}
		if err != nil {
			c.rdErr = err
			close(c.chunks)
			return
		}
	}
}

func (c *streamConn) Read(p []byte) (int, error) {
	if len(c.rest) == 0 {
		select {
		case <-c.closed:
			return 0, io.ErrClosedPipe
		case <-c.rd.wait():
			return 0, os.ErrDeadlineExceeded
		default:
		}
		select {
		case chunk, ok := <-c.chunks:
			if !ok {
				return 0, c.rdErr
			}
			c.rest = chunk
		case <-c.closed:
			return 0, io.ErrClosedPipe
		case <-c.rd.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
	n := copy(p, c.rest)
	c.rest = c.rest[n:]
	return n, nil
}

// Write checks the deadline before writing. A write that is already blocked
// on the peer's flow-control window is not interrupted when the deadline
// passes; it fails on the next call.
func (c *streamConn) Write(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, io.ErrClosedPipe
	case <-c.wr.wait():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	return c.s.Write(p)
}

func (c *streamConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		err = c.s.Close()
	})
	return err
}

func (c *streamConn) LocalAddr() net.Addr  { return c.local }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

func (c *streamConn) SetDeadline(t time.Time) error {
	c.rd.set(t)
	c.wr.set(t)
	return nil
}

func (c *streamConn) SetReadDeadline(t time.Time) error {
	c.rd.set(t)
	return nil
}

func (c *streamConn) SetWriteDeadline(t time.Time) error {
	c.wr.set(t)
	return nil
}

// errListenerClosed is returned by Accept after Close.
var errListenerClosed = errors.New("tunnel: listener closed")

// streamListener hands the streams a gateway opens to an http.Server. It
// outlives reconnects: streams from every connection arrive on it.
type streamListener struct {
	conns     chan net.Conn
	closeOnce sync.Once
	closed    chan struct{}
	addr      net.Addr
}

func newStreamListener(addr string) *streamListener {
	return &streamListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
		addr:   tunnelAddr(addr),
	}
}

func (l *streamListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errListenerClosed
	}
}

// deliver gives a stream to Accept, or closes it if the listener is closed.
func (l *streamListener) deliver(c net.Conn) {
	select {
	case l.conns <- c:
	case <-l.closed:
		c.Close()
	}
}

func (l *streamListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *streamListener) Addr() net.Addr { return l.addr }
