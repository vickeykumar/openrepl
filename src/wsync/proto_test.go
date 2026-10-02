package wsync

import (
	"bytes"
	"io"
	"io/ioutil"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	e := Entry{Path: "a/b.txt", Type: File, Size: 5, Mode: 0644, Hash: "h", ModTime: 7}
	msgs := []Message{
		{Type: MsgHello, Node: "worker-1", Version: ProtocolVersion, Now: 123},
		{Type: MsgPut, Seq: 1, Home: "guest-a", Entry: &e, Body: 5},
		{Type: MsgAttach, Home: "guest-a", Digest: "d", Entries: []Entry{e, {Path: "d", Type: Dir, Mode: 0755}}},
		{Type: MsgAck, Seq: 1, Status: AckOK},
	}
	for _, m := range msgs {
		var body io.Reader
		if m.Body > 0 {
			body = strings.NewReader("hello")
		}
		if err := writeFrame(&buf, m, body); err != nil {
			t.Fatal(err)
		}
	}
	fr := newFrameReader(&buf)
	for i, want := range msgs {
		got, err := fr.next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.Type != want.Type || got.Seq != want.Seq || got.Home != want.Home || got.Body != want.Body || got.Node != want.Node {
			t.Fatalf("frame %d = %+v, want %+v", i, got, want)
		}
		if want.Entry != nil && (got.Entry == nil || *got.Entry != *want.Entry) {
			t.Fatalf("frame %d entry differs", i)
		}
		if len(want.Entries) != len(got.Entries) {
			t.Fatalf("frame %d entries differ", i)
		}
		if want.Body > 0 {
			b, _ := ioutil.ReadAll(fr.body(got))
			if string(b) != "hello" {
				t.Fatalf("body = %q", b)
			}
		}
	}
	if _, err := fr.next(); err != io.EOF {
		t.Fatalf("after the last frame: %v", err)
	}
}

func TestFrameBodyIsPaddedOrCutToItsDeclaredSize(t *testing.T) {
	for name, content := range map[string]string{"short": "ab", "long": "abcdefgh"} {
		var buf bytes.Buffer
		if err := writeFrame(&buf, Message{Type: MsgPut, Body: 5}, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		writeFrame(&buf, Message{Type: MsgAck}, nil)
		fr := newFrameReader(&buf)
		m, _ := fr.next()
		b, _ := ioutil.ReadAll(fr.body(m))
		if len(b) != 5 {
			t.Fatalf("%s: body is %d bytes, want exactly 5", name, len(b))
		}
		if name == "short" && string(b) != "ab\x00\x00\x00" {
			t.Fatalf("a short body must be padded with zeros, got %q", b)
		}
		// The framing is intact: the next frame still reads.
		if next, err := fr.next(); err != nil || next.Type != MsgAck {
			t.Fatalf("%s: next frame: %+v %v", name, next, err)
		}
	}
}

func TestFrameDrainKeepsTheStreamInStep(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, Message{Type: MsgPut, Body: 1000}, strings.NewReader(strings.Repeat("x", 1000)))
	writeFrame(&buf, Message{Type: MsgAck, Seq: 9}, nil)
	fr := newFrameReader(&buf)
	m, _ := fr.next()
	body := fr.body(m)
	io.CopyN(io.Discard, body, 10) // a handler that stopped reading early
	if err := body.drain(); err != nil {
		t.Fatal(err)
	}
	if next, err := fr.next(); err != nil || next.Seq != 9 {
		t.Fatalf("after drain: %+v %v", next, err)
	}
}

func TestFrameDrainReportsATruncatedStream(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, Message{Type: MsgPut, Body: 1000}, strings.NewReader(strings.Repeat("x", 1000)))
	cut := bytes.NewReader(buf.Bytes()[:buf.Len()-100])
	fr := newFrameReader(cut)
	m, _ := fr.next()
	if err := fr.body(m).drain(); err == nil {
		t.Fatal("a truncated body was not reported")
	}
}

func TestFrameRejectsBrokenInput(t *testing.T) {
	cases := map[string][]byte{
		"zero length":    {0, 0, 0, 0},
		"huge length":    {0xff, 0xff, 0xff, 0xff},
		"not json":       append([]byte{0, 0, 0, 3}, "abc"...),
		"no type":        append([]byte{0, 0, 0, 2}, "{}"...),
		"negative body":  append([]byte{0, 0, 0, 22}, `{"t":"put","body":-5}`...),
		"truncated head": {0, 0, 0, 50, '{'},
	}
	for name, in := range cases {
		if _, err := newFrameReader(bytes.NewReader(in)).next(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
