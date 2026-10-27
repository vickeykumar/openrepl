package wsync

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is bumped when the messages change incompatibly.
const ProtocolVersion = 1

// Message types.
const (
	MsgHello  = "hello"  // open the conversation
	MsgPing   = "ping"   // clock offset measurement (gateway to worker)
	MsgPong   = "pong"   // answer to a ping
	MsgOffset = "offset" // the measured clock offset (gateway to worker)
	MsgAttach = "attach" // start synchronizing a home; carries its manifest
	MsgPut    = "put"    // a file, directory or symbolic link
	MsgDelete = "delete" // remove a path
	MsgRename = "rename" // move a path
	MsgAck    = "ack"    // outcome of a put, delete or rename
	MsgSynced = "synced" // this side has sent everything the reconcile needs
	MsgDrop   = "drop"   // the home expired or moved away: delete the copy
	MsgRefuse = "refuse" // the home cannot be synchronized with this peer
	MsgFatal  = "fatal"  // the conversation ends
)

// Ack statuses.
const (
	AckOK      = "ok"      // applied
	AckSkipped = "skipped" // not applied, and nothing is wrong (a newer local change, a directory with content)
	AckError   = "error"   // not applied because of a failure
)

// Message is one frame header. Only the fields a type uses are set.
type Message struct {
	Type    string  `json:"t"`
	Seq     uint64  `json:"seq,omitempty"`
	Home    string  `json:"home,omitempty"`
	Path    string  `json:"path,omitempty"`
	From    string  `json:"from,omitempty"`
	Entry   *Entry  `json:"entry,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
	// Base is the agreed state this side holds, sent with a manifest so that
	// records that differ only in a few paths still work for the rest.
	Base    []Entry `json:"base,omitempty"`
	Digest  string  `json:"digest,omitempty"`
	Status  string  `json:"status,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	Node    string  `json:"node,omitempty"`
	Version int     `json:"v,omitempty"`
	Now     int64   `json:"now,omitempty"`
	T0      int64   `json:"t0,omitempty"`
	Offset  int64   `json:"offset,omitempty"`
	// Body is the number of raw content bytes that follow the header.
	Body int64 `json:"body,omitempty"`
}

// maxHeader bounds a frame header; a manifest of a large home is the biggest.
const maxHeader = 32 << 20

// errFrame reports a broken frame; the conversation cannot continue.
var errFrame = errors.New("wsync: bad frame")

// writeFrame writes a frame: a 4-byte big-endian header length, the JSON
// header, and exactly msg.Body bytes of content from body. A body that turns
// out shorter than declared (a file that shrank while it was read) is padded
// with zeros, so the framing stays intact and the receiver's hash check
// rejects the content; one that is longer is cut.
func writeFrame(w io.Writer, msg Message, body io.Reader) error {
	hdr, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(hdr) > maxHeader {
		return fmt.Errorf("wsync: frame header of %d bytes is too large", len(hdr))
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(hdr)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if msg.Body <= 0 {
		return nil
	}
	if body == nil {
		body = zeroReader{}
	}
	copied, err := io.Copy(w, io.LimitReader(body, msg.Body))
	if err != nil {
		return err
	}
	if copied < msg.Body {
		if _, err := io.Copy(w, io.LimitReader(zeroReader{}, msg.Body-copied)); err != nil {
			return err
		}
	}
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// frameReader reads frames from a stream.
type frameReader struct {
	r *bufio.Reader
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// next reads the next header. The caller must read or discard msg.Body bytes
// with body() before calling next again.
func (f *frameReader) next() (Message, error) {
	var n [4]byte
	if _, err := io.ReadFull(f.r, n[:]); err != nil {
		return Message{}, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size == 0 || size > maxHeader {
		return Message{}, errFrame
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(f.r, buf); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(buf, &m); err != nil || m.Type == "" || m.Body < 0 {
		return Message{}, errFrame
	}
	return m, nil
}

// body returns a reader for the msg.Body bytes that follow a header.
func (f *frameReader) body(m Message) *bodyReader {
	return &bodyReader{r: io.LimitReader(f.r, m.Body), left: m.Body}
}

// bodyReader reads a frame's content and can discard what is left of it.
type bodyReader struct {
	r    io.Reader
	left int64
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

// drain reads and discards the rest of the content, to keep the stream in
// step with the framing.
func (b *bodyReader) drain() error {
	if b.left <= 0 {
		return nil
	}
	n, err := io.Copy(io.Discard, b.r)
	b.left -= n
	if err == nil && b.left > 0 {
		err = io.ErrUnexpectedEOF
	}
	return err
}
