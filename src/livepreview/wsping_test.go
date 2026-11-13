package livepreview

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// a text frame of a server: no mask
func textFrame(payload string) []byte {
	return append([]byte{0x81, byte(len(payload))}, payload...)
}

func TestAPingIsSentOnlyBetweenFramesAndOnlyWhenQuiet(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	k := newKeepAlive(a, 40*time.Millisecond)
	defer k.Close()
	got := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := b.Read(buf)
			if err != nil {
				close(got)
				return
			}
			got <- append([]byte{}, buf[:n]...)
		}
	}()
	// the HTTP answer that opens the socket, then half of a frame: nothing may be inserted yet
	k.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n"))
	frame := textFrame("hello world")
	k.Write(frame[:5])
	time.Sleep(120 * time.Millisecond)
	k.mu.Lock()
	mid := k.state == 1 || len(k.hdr) > 0
	k.mu.Unlock()
	if !mid {
		t.Fatalf("the scan lost the frame: state %d", k.state)
	}
	k.Write(frame[5:])
	var all []byte
	deadline := time.After(400 * time.Millisecond)
loop:
	for {
		select {
		case c, ok := <-got:
			if !ok {
				break loop
			}
			all = append(all, c...)
			if bytes.HasSuffix(all, []byte{0x89, 0x00}) {
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	want := append(append([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n"), frame...), 0x89, 0x00)
	if !bytes.HasPrefix(all, want) {
		t.Errorf("the frame has to come whole, and the ping after it:\n got %q\nwant %q", all, want)
	}
}

func TestFrameHeaderLengths(t *testing.T) {
	for _, c := range []struct {
		h    []byte
		n    int64
		done bool
	}{
		{[]byte{0x81}, 0, false},
		{[]byte{0x81, 5}, 5, true},
		{[]byte{0x81, 126, 1}, 0, false},
		{[]byte{0x81, 126, 1, 0}, 256, true},
		{[]byte{0x82, 127, 0, 0, 0, 0, 0, 1, 0, 0}, 65536, true},
		{[]byte{0x81, 0x85, 1, 2, 3}, 0, false}, // a masked frame needs 4 more bytes
		{[]byte{0x81, 0x85, 1, 2, 3, 4}, 5, true},
	} {
		if n, done := frameHeader(c.h); n != c.n || done != c.done {
			t.Errorf("frameHeader(%v) = %d %v, want %d %v", c.h, n, done, c.n, c.done)
		}
	}
}
