package tunnel

import (
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// pipeStream adapts one end of a net.Pipe to the stream interface.
func newTestStreamPair() (*streamConn, net.Conn) {
	a, b := net.Pipe()
	return newStreamConn(a, "a", "b"), b
}

func TestStreamConnReadWrite(t *testing.T) {
	c, peer := newTestStreamPair()
	defer c.Close()
	go peer.Write([]byte("hello world"))

	buf := make([]byte, 5)
	if n, err := io.ReadFull(c, buf); err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
	rest := make([]byte, 6) // the remainder of the same chunk
	if n, err := io.ReadFull(c, rest); err != nil || string(rest[:n]) != " world" {
		t.Fatalf("read %q, %v", rest[:n], err)
	}

	go c.Write([]byte("pong"))
	got := make([]byte, 4)
	if _, err := io.ReadFull(peer, got); err != nil || string(got) != "pong" {
		t.Fatalf("peer read %q, %v", got, err)
	}
}

func TestStreamConnReadDeadline(t *testing.T) {
	c, peer := newTestStreamPair()
	defer c.Close()
	defer peer.Close()

	c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	start := time.Now()
	if _, err := c.Read(make([]byte, 1)); err != os.ErrDeadlineExceeded {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("read did not return at the deadline")
	}

	// Clearing the deadline makes the connection usable again.
	c.SetReadDeadline(time.Time{})
	go peer.Write([]byte("x"))
	if _, err := c.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read after clearing the deadline: %v", err)
	}
}

func TestStreamConnWriteDeadline(t *testing.T) {
	c, peer := newTestStreamPair()
	defer c.Close()
	defer peer.Close()

	c.SetWriteDeadline(time.Now().Add(-time.Second))
	if _, err := c.Write([]byte("x")); err != os.ErrDeadlineExceeded {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	c.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
	time.Sleep(60 * time.Millisecond)
	if _, err := c.Write([]byte("x")); err != os.ErrDeadlineExceeded {
		t.Fatalf("err = %v, want deadline exceeded after the timer fired", err)
	}
}

func TestStreamConnCloseUnblocksRead(t *testing.T) {
	c, peer := newTestStreamPair()
	defer peer.Close()
	done := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 1))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	c.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not unblock the read")
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("write succeeded after close")
	}
}

func TestStreamConnPeerClose(t *testing.T) {
	c, peer := newTestStreamPair()
	defer c.Close()
	peer.Close()
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("read succeeded after the peer closed")
	}
}

func TestStreamListener(t *testing.T) {
	l := newStreamListener("w")
	a, b := net.Pipe()
	defer b.Close()
	go l.deliver(a)
	got, err := l.Accept()
	if err != nil || got != a {
		t.Fatalf("Accept = %v, %v", got, err)
	}
	l.Close()
	if _, err := l.Accept(); err == nil {
		t.Fatal("Accept succeeded after Close")
	}
	// A stream delivered after Close is closed, not leaked.
	c, d := net.Pipe()
	l.deliver(c)
	d.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := d.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("stream delivered after Close was not closed: %v", err)
	}
}
