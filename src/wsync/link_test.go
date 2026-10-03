//go:build linux
// +build linux

package wsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// site is one machine: a directory that holds homes, and one that holds the
// records.
type site struct {
	t     *testing.T
	base  string
	state string
}

func newSite(t *testing.T) *site {
	return &site{t: t, base: t.TempDir(), state: t.TempDir()}
}

func (s *site) root(home string) string { return filepath.Join(s.base, home) }

func (s *site) put(home, rel, content string) {
	put(s.t, s.root(home), rel, content)
	setMtime(s.t, s.root(home), rel, tick())
}

func (s *site) read(home, rel string) string { return read(s.t, s.root(home), rel) }

func (s *site) has(home, rel string) bool { return exists(s.root(home), rel) }

func (s *site) scan(home string) map[string]Entry {
	h, err := OpenHome(s.base, home, true)
	if err != nil {
		s.t.Fatal(err)
	}
	defer h.Close()
	res, err := h.Scan(ScanOptions{MaxFileSize: 1 << 30})
	if err != nil {
		s.t.Fatal(err)
	}
	return res.Entries
}

// conn is a gateway and a worker joined by an in-memory stream.
type conn struct {
	t      *testing.T
	gw, wk *Link
	cancel context.CancelFunc
	wg     sync.WaitGroup
	gwErr  error
	wkErr  error
}

type tweaks struct {
	gw, wk func(*Config)
	wrap   func(a, b net.Conn) (net.Conn, net.Conn)
}

func connect(t *testing.T, gs, ws *site, tw tweaks) *conn {
	t.Helper()
	a, b := net.Pipe()
	if tw.wrap != nil {
		a, b = tw.wrap(a, b)
	}
	gcfg := Config{
		Node: "gateway", Peer: "worker-1", IsGateway: true, BaseDir: gs.base, Records: RecordStore{Dir: gs.state},
		Debounce: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, Logf: t.Logf,
	}
	wcfg := Config{
		Node: "worker-1", Peer: "gateway", IsGateway: false, BaseDir: ws.base, Records: RecordStore{Dir: ws.state},
		Debounce: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond, Logf: t.Logf,
	}
	if tw.gw != nil {
		tw.gw(&gcfg)
	}
	if tw.wk != nil {
		tw.wk(&wcfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{t: t, gw: NewLink(gcfg, a), wk: NewLink(wcfg, b), cancel: cancel}
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.gwErr = c.gw.Run(ctx) }()
	go func() { defer c.wg.Done(); c.wkErr = c.wk.Run(ctx) }()
	t.Cleanup(c.close)
	return c
}

func (c *conn) close() {
	c.cancel()
	c.gw.Close()
	c.wk.Close()
	c.wg.Wait()
}

// syncHome attaches a home from the worker and waits until both sides have
// reconciled it.
func (c *conn) syncHome(home string) {
	c.t.Helper()
	if err := c.wk.Attach(home); err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.wk.WaitSynced(ctx, home); err != nil {
		c.t.Fatalf("worker: %v", err)
	}
	waitFor(c.t, "the gateway to attach the home", func() bool { return c.gw.home(home) != nil })
	if err := c.gw.WaitSynced(ctx, home); err != nil {
		c.t.Fatalf("gateway: %v", err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func sameHomes(gs, ws *site, home string) bool {
	return sameTree(gs.scan(home), ws.scan(home))
}

func (c *conn) waitEqual(gs, ws *site, home string) {
	c.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if sameHomes(gs, ws, home) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatalf("the homes never became equal:\n gateway %s\n worker  %s", describe(gs.scan(home)), describe(ws.scan(home)))
}

// watch feeds a site's file events to a link, as the manager will.
func watchFor(t *testing.T, s *site, l *Link, home string) {
	t.Helper()
	w, err := NewWatcher(s.base, func(h, rel string) {
		if h == "" {
			l.NotifyAll()
		} else {
			l.Notify(h, rel)
		}
	}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.AddHome(home); err != nil {
		t.Fatal(err)
	}
}

const hm = "guest-1"

func TestLinkFirstContactSyncsBothWays(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "a.txt", "from gateway")
	gs.put(hm, "dir/b.txt", "nested")
	ws.put(hm, "w.txt", "from worker")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)

	if !sameHomes(gs, ws, hm) {
		t.Fatalf("not equal:\n gateway %s\n worker  %s", describe(gs.scan(hm)), describe(ws.scan(hm)))
	}
	if ws.read(hm, "a.txt") != "from gateway" || gs.read(hm, "w.txt") != "from worker" || ws.read(hm, "dir/b.txt") != "nested" {
		t.Fatal("a side is missing what only the other had")
	}
	if !c.gw.Synced(hm) || !c.wk.Synced(hm) {
		t.Fatal("not reported as synced")
	}
	// Both sides saved the same record.
	g, _ := RecordStore{Dir: gs.state}.Load("worker-1", hm)
	w, _ := RecordStore{Dir: ws.state}.Load("gateway", hm)
	if !g.Valid || g.Digest() == "" || g.Digest() != w.Digest() {
		t.Fatalf("records differ or were not saved: %q vs %q", g.Digest(), w.Digest())
	}
}

func TestLinkMeasuresTheClockOffset(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	c := connect(t, gs, ws, tweaks{
		wk: func(cfg *Config) { cfg.Now = func() time.Time { return time.Now().Add(time.Hour) } },
	})
	c.syncHome(hm)
	for name, l := range map[string]*Link{"gateway": c.gw, "worker": c.wk} {
		off := l.Offset()
		if off < time.Hour-200*time.Millisecond || off > time.Hour+200*time.Millisecond {
			t.Errorf("%s sees offset %v, want about 1h", name, off)
		}
	}
	if c.gw.Offset() != c.wk.Offset() {
		t.Fatalf("the two sides must use the same offset: %v vs %v", c.gw.Offset(), c.wk.Offset())
	}
}

func TestLinkIncrementalChangesFlowBothWays(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "keep.txt", "k")
	gs.put(hm, "edit.txt", "v1")
	gs.put(hm, "old.txt", "o")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	watchFor(t, gs, c.gw, hm)
	watchFor(t, ws, c.wk, hm)

	// Gateway side.
	gs.put(hm, "edit.txt", "v2 on gateway")
	gs.put(hm, "new-on-gw.txt", "n")
	os.Remove(filepath.Join(gs.root(hm), "old.txt"))
	os.MkdirAll(filepath.Join(gs.root(hm), "sub/deeper"), 0755)
	gs.put(hm, "sub/deeper/f.txt", "deep")
	c.waitEqual(gs, ws, hm)
	if ws.read(hm, "edit.txt") != "v2 on gateway" || ws.has(hm, "old.txt") || ws.read(hm, "sub/deeper/f.txt") != "deep" {
		t.Fatalf("the worker did not follow the gateway: %s", describe(ws.scan(hm)))
	}

	// Worker side: what a program would do in a terminal.
	ws.put(hm, "output.txt", "result")
	ws.put(hm, "keep.txt", "k edited on worker")
	os.Remove(filepath.Join(ws.root(hm), "new-on-gw.txt"))
	os.Rename(filepath.Join(ws.root(hm), "sub"), filepath.Join(ws.root(hm), "moved"))
	c.waitEqual(gs, ws, hm)
	if gs.read(hm, "output.txt") != "result" || gs.read(hm, "keep.txt") != "k edited on worker" || gs.has(hm, "new-on-gw.txt") || gs.read(hm, "moved/deeper/f.txt") != "deep" || gs.has(hm, "sub") {
		t.Fatalf("the gateway did not follow the worker: %s", describe(gs.scan(hm)))
	}
}

func TestLinkAppliedChangesAreNeverEchoed(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	watchFor(t, gs, c.gw, hm)
	watchFor(t, ws, c.wk, hm)

	gs.put(hm, "a.txt", "one")
	gs.put(hm, "b/c.txt", "two")
	c.waitEqual(gs, ws, hm)
	time.Sleep(600 * time.Millisecond) // let any echo happen

	changes := func(l *Link, dir string) int {
		st := l.Stats()
		return st[dir+":put"] + st[dir+":delete"] + st[dir+":rename"]
	}
	if n := changes(c.wk, "sent"); n != 0 {
		t.Fatalf("the worker sent %d change(s) back for what it received: %v", n, c.wk.Stats())
	}
	if n := changes(c.gw, "recv"); n != 0 {
		t.Fatalf("the gateway received %d change(s) it did not make", n)
	}
	before := changes(c.gw, "sent")
	time.Sleep(600 * time.Millisecond)
	if changes(c.gw, "sent") != before || changes(c.wk, "sent") != 0 {
		t.Fatalf("an idle link keeps sending: %v / %v", c.gw.Stats(), c.wk.Stats())
	}

	// And the same the other way round.
	ws.put(hm, "w.txt", "three")
	c.waitEqual(gs, ws, hm)
	time.Sleep(600 * time.Millisecond)
	if changes(c.gw, "sent") != before {
		t.Fatalf("the gateway echoed a worker change back: %v", c.gw.Stats())
	}
}

func TestLinkRenameMovesNoContent(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	big := strings.Repeat("0123456789", 100_000)
	gs.put(hm, "big.txt", big)
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	watchFor(t, gs, c.gw, hm)

	putsBefore := c.gw.Stats()["sent:put"]
	os.Rename(filepath.Join(gs.root(hm), "big.txt"), filepath.Join(gs.root(hm), "renamed.txt"))
	c.waitEqual(gs, ws, hm)
	st := c.gw.Stats()
	if st["sent:rename"] != 1 {
		t.Fatalf("expected one rename, got %v", st)
	}
	if st["sent:put"] != putsBefore {
		t.Fatalf("a rename sent file content: %v", st)
	}
	if ws.read(hm, "renamed.txt") != big || ws.has(hm, "big.txt") {
		t.Fatal("the worker did not move the file")
	}
}

func TestLinkConflictLatestWins(t *testing.T) {
	for _, workerLater := range []bool{true, false} {
		gs, ws := newSite(t), newSite(t)
		gs.put(hm, "f.txt", "base")
		c := connect(t, gs, ws, tweaks{})
		c.syncHome(hm)
		c.close()

		// Both sides edit while apart; the later edit has the later time.
		if workerLater {
			gs.put(hm, "f.txt", "gateway edit")
			ws.put(hm, "f.txt", "worker edit, later")
		} else {
			ws.put(hm, "f.txt", "worker edit")
			gs.put(hm, "f.txt", "gateway edit, later")
		}
		c2 := connect(t, gs, ws, tweaks{})
		c2.syncHome(hm)
		want := "gateway edit, later"
		if workerLater {
			want = "worker edit, later"
		}
		if gs.read(hm, "f.txt") != want || ws.read(hm, "f.txt") != want {
			t.Fatalf("workerLater=%v: gateway %q worker %q, want %q", workerLater, gs.read(hm, "f.txt"), ws.read(hm, "f.txt"), want)
		}
	}
}

func TestLinkReconnectReconcilesOfflineChanges(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "main.c", "int main(){return 0;}")
	gs.put(hm, "old.txt", "old")
	gs.put(hm, "shared.txt", "base")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	c.close() // the worker goes away

	// Worker, while disconnected: main.c modified, output.txt created, old.txt deleted.
	ws.put(hm, "main.c", "int main(){return 1;}")
	ws.put(hm, "output.txt", "result")
	os.Remove(filepath.Join(ws.root(hm), "old.txt"))
	// Gateway, while the worker was away: an upload and a save.
	gs.put(hm, "upload.txt", "uploaded")
	gs.put(hm, "shared.txt", "saved on gateway")

	c2 := connect(t, gs, ws, tweaks{})
	c2.syncHome(hm)
	if !sameHomes(gs, ws, hm) {
		t.Fatalf("not equal:\n gateway %s\n worker  %s", describe(gs.scan(hm)), describe(ws.scan(hm)))
	}
	for name, s := range map[string]*site{"gateway": gs, "worker": ws} {
		if s.read(hm, "main.c") != "int main(){return 1;}" || s.read(hm, "output.txt") != "result" ||
			s.read(hm, "upload.txt") != "uploaded" || s.read(hm, "shared.txt") != "saved on gateway" || s.has(hm, "old.txt") {
			t.Fatalf("%s: %s", name, describe(s.scan(hm)))
		}
	}
	// Nothing was deleted by mistake and nothing is wiped and re-downloaded:
	// the unchanged file was not transferred again.
	if c2.gw.Stats()["sent:put"]+c2.wk.Stats()["sent:put"] > 4 {
		t.Fatalf("a reconnect transferred unchanged files: %v %v", c2.gw.Stats(), c2.wk.Stats())
	}
}

func TestLinkRecordsThatDisagreeFallBackToAUnion(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "a.txt", "a")
	gs.put(hm, "b.txt", "b")
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	c.close()

	// The worker loses its record, and a file is deleted on the gateway.
	os.RemoveAll(ws.state)
	os.MkdirAll(ws.state, 0755)
	os.Remove(filepath.Join(gs.root(hm), "a.txt"))

	c2 := connect(t, gs, ws, tweaks{})
	c2.syncHome(hm)
	if !gs.has(hm, "a.txt") || !ws.has(hm, "a.txt") {
		t.Fatal("with records that disagree nothing may be deleted: the file must come back")
	}
	if !sameHomes(gs, ws, hm) {
		t.Fatal("not equal")
	}
	// Having reconciled, the records agree again, and deletes work.
	os.Remove(filepath.Join(gs.root(hm), "b.txt"))
	c2.close()
	c3 := connect(t, gs, ws, tweaks{})
	c3.syncHome(hm)
	if gs.has(hm, "b.txt") || ws.has(hm, "b.txt") {
		t.Fatal("a delete was not applied once the records agreed again")
	}
}

func TestLinkRefusesAHomeThePeerDoesNotOwn(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	ws.put("guest-b", "x.txt", "x")
	c := connect(t, gs, ws, tweaks{gw: func(cfg *Config) {
		cfg.Accept = func(home string) error {
			if home != "guest-a" {
				return fmt.Errorf("worker-1 does not own %s", home)
			}
			return nil
		}
	}})
	if err := c.wk.Attach("guest-b"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.wk.WaitSynced(ctx, "guest-b")
	if err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("worker got %v, want the refusal", err)
	}
	if gs.has("guest-b", "x.txt") || exists(gs.base, "guest-b") {
		t.Fatal("the gateway created or changed a home it refused")
	}
	// A home it owns still works on the same link.
	ws.put("guest-a", "y.txt", "y")
	c.syncHome("guest-a")
	if gs.read("guest-a", "y.txt") != "y" {
		t.Fatal("the allowed home was not synchronized")
	}
}

func TestLinkSyncsManyFilesThroughTheWindow(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	for i := 0; i < 150; i++ {
		gs.put(hm, fmt.Sprintf("dir%d/file%03d.txt", i%7, i), strings.Repeat("x", i))
	}
	c := connect(t, gs, ws, tweaks{gw: func(cfg *Config) { cfg.Window = 3 }, wk: func(cfg *Config) { cfg.Window = 3 }})
	c.syncHome(hm)
	if !sameHomes(gs, ws, hm) || len(ws.scan(hm)) != 150+7 {
		t.Fatalf("worker has %d entries", len(ws.scan(hm)))
	}
}

func TestLinkBothSidesSendLargeFilesAtTheSameTime(t *testing.T) {
	// Over an unbuffered stream, two sides that each block writing a large
	// body while the other also writes would deadlock if reading and writing
	// were not independent.
	gs, ws := newSite(t), newSite(t)
	for i := 0; i < 4; i++ {
		gs.put(hm, fmt.Sprintf("g%d.bin", i), strings.Repeat(string(rune('a'+i)), 3<<20))
		ws.put(hm, fmt.Sprintf("w%d.bin", i), strings.Repeat(string(rune('A'+i)), 3<<20))
	}
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	if !sameHomes(gs, ws, hm) || len(gs.scan(hm)) != 8 {
		t.Fatal("homes differ")
	}
}

func TestLinkLargeFileIsStreamed(t *testing.T) {
	size := 64 << 20
	if testing.Short() {
		size = 8 << 20
	}
	gs, ws := newSite(t), newSite(t)
	// Written in pieces, so the test never holds the file either.
	h, err := OpenHome(gs.base, hm, true)
	if err != nil {
		t.Fatal(err)
	}
	h.MaxFileSize = int64(size)
	hash, _ := hashReader(io.LimitReader(patternReader{}, int64(size)))
	e := Entry{Path: "big.bin", Type: File, Size: int64(size), Mode: 0644, ModTime: tick().UnixNano(), Hash: hash}
	if err := h.WriteFile(e, io.LimitReader(patternReader{}, int64(size))); err != nil {
		t.Fatal(err)
	}
	h.Close()
	big := func(cfg *Config) { cfg.MaxFileSize = int64(size) }
	c := connect(t, gs, ws, tweaks{gw: big, wk: big})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	c.syncHome(hm)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(size)/2 {
		t.Fatalf("syncing a %d MB file allocated %d MB; it must be streamed", size>>20, allocated>>20)
	}
	if !sameHomes(gs, ws, hm) || ws.scan(hm)["big.bin"].Hash != hash {
		t.Fatal("the large file differs")
	}
}

func TestLinkFileChangingDuringTransferIsSentAgain(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "live.txt", "AAAAAAAAAA")
	var once int32
	c := connect(t, gs, ws, tweaks{gw: func(cfg *Config) {}})
	c.gw.hookBeforeOpen = func(home, rel string) {
		// The file changes after it was scanned and before it is sent, with
		// the same size, so only the hash tells.
		if rel == "live.txt" && atomic.AddInt32(&once, 1) == 1 {
			put(t, gs.root(home), rel, "BBBBBBBBBB")
			setMtime(t, gs.root(home), rel, tick())
		}
	}
	c.syncHome(hm)
	watchFor(t, gs, c.gw, hm)
	c.waitEqual(gs, ws, hm)
	waitFor(t, "the corrected content", func() bool { return ws.has(hm, "live.txt") && ws.read(hm, "live.txt") == "BBBBBBBBBB" })
	noTemps(t, ws.root(hm))
}

// cutConn closes both ends after n bytes have been written through a.
type cutConn struct {
	net.Conn
	left int64
	peer net.Conn
}

func (c *cutConn) Write(p []byte) (int, error) {
	if atomic.LoadInt64(&c.left) <= 0 {
		c.Conn.Close()
		c.peer.Close()
		return 0, io.ErrClosedPipe
	}
	if int64(len(p)) > atomic.LoadInt64(&c.left) {
		n, _ := c.Conn.Write(p[:atomic.LoadInt64(&c.left)])
		atomic.StoreInt64(&c.left, 0)
		c.Conn.Close()
		c.peer.Close()
		return n, io.ErrClosedPipe
	}
	atomic.AddInt64(&c.left, -int64(len(p)))
	return c.Conn.Write(p)
}

func TestLinkInterruptedTransferLeavesNoPartialFile(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	gs.put(hm, "small.txt", "s")
	gs.put(hm, "big.bin", strings.Repeat("0123456789abcdef", 1<<18)) // 4 MB
	c := connect(t, gs, ws, tweaks{wrap: func(a, b net.Conn) (net.Conn, net.Conn) {
		// The gateway's end stops after 1.5 MB.
		return &cutConn{Conn: a, left: 3 << 19, peer: b}, b
	}})
	if err := c.wk.Attach(hm); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link to fail", func() bool {
		select {
		case <-c.wk.Done():
			return true
		default:
			return false
		}
	})
	<-c.gw.Done()
	noTemps(t, ws.root(hm))
	if ws.has(hm, "big.bin") {
		t.Fatal("a partly received file was left under its real name")
	}

	// The next connection completes the job.
	c2 := connect(t, gs, ws, tweaks{})
	c2.syncHome(hm)
	if !sameHomes(gs, ws, hm) {
		t.Fatalf("not equal after the retry:\n gateway %s\n worker  %s\n skipped? %v", describe(gs.scan(hm)), describe(ws.scan(hm)), c2.gw.Stats())
	}
	noTemps(t, ws.root(hm))
}

func TestLinkRejectsAnotherProtocolVersion(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	gs := newSite(t)
	l := NewLink(Config{Node: "gateway", Peer: "w", IsGateway: true, BaseDir: gs.base, Records: RecordStore{Dir: gs.state}}, a)
	errc := make(chan error, 1)
	go func() { errc <- l.Run(context.Background()) }()
	go io.Copy(io.Discard, b)
	writeFrame(b, Message{Type: MsgHello, Node: "w", Version: ProtocolVersion + 1}, nil)
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "version") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the link kept running with an unsupported peer")
	}
}

// peerScript drives a gateway link from the other end of the stream by hand,
// the way a hostile or buggy worker would.
type peerScript struct {
	t  *testing.T
	b  net.Conn
	fr *frameReader
	mu sync.Mutex
	w  io.Writer
}

func newPeerScript(t *testing.T, gs *site, accept func(string) error) (*peerScript, *Link) {
	a, b := net.Pipe()
	l := NewLink(Config{
		Node: "gateway", Peer: "worker-1", IsGateway: true, BaseDir: gs.base, Records: RecordStore{Dir: gs.state},
		Accept: accept, Logf: t.Logf, Debounce: 20 * time.Millisecond,
	}, a)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); l.Run(ctx) }()
	t.Cleanup(func() { cancel(); l.Close(); b.Close(); wg.Wait() })
	p := &peerScript{t: t, b: b, fr: newFrameReader(b), w: b}
	return p, l
}

func (p *peerScript) send(m Message, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if body != "" {
		m.Body = int64(len(body))
	}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	if err := writeFrame(p.w, m, r); err != nil {
		p.t.Fatal(err)
	}
}

// next reads frames, answering the gateway's pings, until one that is not a
// ping or hello.
func (p *peerScript) next() Message {
	for {
		m, err := p.fr.next()
		if err != nil {
			p.t.Fatalf("reading from the gateway: %v", err)
		}
		if m.Body > 0 {
			p.fr.body(m).drain()
		}
		switch m.Type {
		case MsgPing:
			p.send(Message{Type: MsgPong, T0: m.T0, Now: time.Now().UnixNano()}, "")
		case MsgHello, MsgOffset:
		default:
			return m
		}
	}
}

// handshake completes the hello and clock exchange, then attaches a home
// with an empty manifest and consumes the gateway's manifest.
func (p *peerScript) handshake(home string) {
	p.send(Message{Type: MsgHello, Node: "worker-1", Version: ProtocolVersion, Now: time.Now().UnixNano()}, "")
	p.send(Message{Type: MsgAttach, Home: home}, "")
	for {
		m := p.next()
		if m.Type == MsgAttach {
			break
		}
		if m.Type == MsgRefuse {
			p.t.Fatalf("home refused: %s", m.Reason)
		}
	}
	p.send(Message{Type: MsgSynced, Home: home}, "")
	for {
		m := p.next()
		if m.Type == MsgSynced {
			return
		}
	}
}

func TestLinkHostilePeerCannotEscapeTheHome(t *testing.T) {
	gs := newSite(t)
	outside := t.TempDir()
	put(t, outside, "victim.txt", "secret")
	p, _ := newPeerScript(t, gs, nil)
	p.handshake(hm)
	home := gs.root(hm)

	// A link that points outside was created earlier by a user of the home.
	os.Symlink(outside, filepath.Join(home, "out"))

	hostile := []Message{
		{Type: MsgPut, Seq: 1, Home: hm, Entry: &Entry{Path: "../escape.txt", Type: File, Size: 3, Mode: 0644, Hash: sum("abc")}},
		{Type: MsgPut, Seq: 2, Home: hm, Entry: &Entry{Path: "/etc/escape.txt", Type: File, Size: 3, Mode: 0644, Hash: sum("abc")}},
		{Type: MsgPut, Seq: 3, Home: hm, Entry: &Entry{Path: "out/new.txt", Type: File, Size: 3, Mode: 0644, Hash: sum("abc")}},
		{Type: MsgPut, Seq: 4, Home: hm, Entry: &Entry{Path: "a/../../escape.txt", Type: File, Size: 3, Mode: 0644, Hash: sum("abc")}},
		{Type: MsgPut, Seq: 5, Home: hm, Entry: &Entry{Path: TempPrefix + "x", Type: File, Size: 3, Mode: 0644, Hash: sum("abc")}},
		{Type: MsgDelete, Seq: 6, Home: hm, Path: "../victim.txt"},
		{Type: MsgDelete, Seq: 7, Home: hm, Path: "out/victim.txt"},
		{Type: MsgRename, Seq: 8, Home: hm, From: "out/victim.txt", Entry: &Entry{Path: "stolen.txt", Type: File, Size: 6, Hash: sum("secret")}},
		{Type: MsgRename, Seq: 9, Home: hm, From: "../victim.txt", Entry: &Entry{Path: "stolen2.txt", Type: File, Size: 6, Hash: sum("secret")}},
		{Type: MsgPut, Seq: 10, Home: hm, Entry: &Entry{Path: "mode", Type: File, Size: 3, Mode: 04777, Hash: sum("abc")}},
	}
	for _, m := range hostile {
		body := ""
		if m.Type == MsgPut && m.Entry.Type == File {
			body = "abc"
		}
		p.send(m, body)
	}
	acks := map[uint64]Message{}
	for len(acks) < len(hostile) {
		m := p.next()
		if m.Type == MsgAck {
			acks[m.Seq] = m
		}
	}
	for seq := uint64(1); seq <= 9; seq++ {
		if acks[seq].Status == AckOK {
			t.Errorf("hostile change %d was accepted: %+v", seq, hostile[seq-1])
		}
	}
	// The one with a privilege bit is stored, without the bit.
	if acks[10].Status != AckOK {
		t.Fatalf("a normal file with odd mode bits was refused: %+v", acks[10])
	}
	if fi, _ := os.Stat(filepath.Join(home, "mode")); fi.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatal("a set-user-id bit was applied")
	}

	// Nothing outside the home changed, and nothing was created in it.
	files, _ := os.ReadDir(outside)
	if len(files) != 1 || read(t, outside, "victim.txt") != "secret" {
		t.Fatalf("the outside directory changed: %v", files)
	}
	for _, p := range []string{"escape.txt", "stolen.txt", "stolen2.txt", "new.txt"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(home), p)); err == nil {
			t.Fatalf("%s appeared next to the home", p)
		}
		if exists(home, p) {
			t.Fatalf("%s appeared in the home", p)
		}
	}
	if _, err := os.Stat("/etc/escape.txt"); err == nil {
		t.Fatal("a file was written to /etc")
	}
}

func TestLinkRejectsAHomeNameThatIsAPath(t *testing.T) {
	gs := newSite(t)
	p, _ := newPeerScript(t, gs, nil)
	p.send(Message{Type: MsgHello, Node: "worker-1", Version: ProtocolVersion}, "")
	p.send(Message{Type: MsgAttach, Home: "../outside"}, "")
	m := p.next()
	if m.Type != MsgRefuse {
		t.Fatalf("got %+v, want a refusal", m)
	}
	if exists(filepath.Dir(gs.base), "outside") {
		t.Fatal("a directory was created outside the base")
	}
}

func TestLinkIgnoresInvalidPathsInAManifest(t *testing.T) {
	gs := newSite(t)
	gs.put(hm, "real.txt", "r")
	p, _ := newPeerScript(t, gs, nil)
	p.send(Message{Type: MsgHello, Node: "worker-1", Version: ProtocolVersion}, "")
	p.send(Message{Type: MsgAttach, Home: hm, Entries: []Entry{
		{Path: "../evil", Type: File, Size: 1, Mode: 0644, Hash: "h"},
		{Path: "/abs", Type: File, Size: 1, Mode: 0644, Hash: "h"},
		{Path: "ok.txt", Type: File, Size: 1, Mode: 0644, Hash: "h"},
	}}, "")
	for {
		m := p.next()
		if m.Type == MsgAttach {
			break
		}
	}
	// The gateway acts on the valid entry only; it asks for nothing outside.
	p.send(Message{Type: MsgSynced, Home: hm}, "")
	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case <-deadline:
			done = true
		default:
		}
		if m := p.next(); m.Type == MsgSynced {
			done = true
		} else if m.Type == MsgPut && (strings.Contains(m.Entry.Path, "..") || strings.HasPrefix(m.Entry.Path, "/")) {
			t.Fatalf("the gateway acted on an invalid path: %+v", m.Entry)
		}
	}
}

func TestLinkAppliesAChangeOnlyIfNoNewerLocalChangeExists(t *testing.T) {
	gs := newSite(t)
	p, _ := newPeerScript(t, gs, nil)
	p.handshake(hm)
	home := gs.root(hm)

	// Two files edited locally after the home was attached and not yet sent.
	put(t, home, "newer-here.txt", "local, later")
	setMtime(t, home, "newer-here.txt", time.Unix(2_000_000_100, 0))
	put(t, home, "older-here.txt", "local, earlier")
	setMtime(t, home, "older-here.txt", time.Unix(2_000_000_100, 0))
	put(t, home, "keep-me.txt", "edited here")
	setMtime(t, home, "keep-me.txt", time.Unix(2_000_000_100, 0))

	early := func(path, content string) Message {
		return Message{Type: MsgPut, Home: hm, Entry: &Entry{Path: path, Type: File, Size: int64(len(content)), Mode: 0644, Hash: sum(content), ModTime: time.Unix(2_000_000_000, 0).UnixNano()}}
	}
	late := func(path, content string) Message {
		m := early(path, content)
		m.Entry.ModTime = time.Unix(2_000_000_200, 0).UnixNano()
		return m
	}
	msgs := []Message{
		early("newer-here.txt", "peer, earlier"), // the local change is later: keep it
		late("older-here.txt", "peer, later"),    // the peer's change is later: apply it
		{Type: MsgDelete, Home: hm, Path: "keep-me.txt"},
		late("brand-new.txt", "peer"),
	}
	for i := range msgs {
		msgs[i].Seq = uint64(i + 1)
		body := ""
		if msgs[i].Type == MsgPut {
			body = map[string]string{"newer-here.txt": "peer, earlier", "older-here.txt": "peer, later", "brand-new.txt": "peer"}[msgs[i].Entry.Path]
		}
		p.send(msgs[i], body)
	}
	acks := map[uint64]Message{}
	for len(acks) < len(msgs) {
		if m := p.next(); m.Type == MsgAck {
			acks[m.Seq] = m
		}
	}
	if acks[1].Status != AckSkipped || read(t, home, "newer-here.txt") != "local, later" {
		t.Errorf("a later local edit was overwritten: %+v, content %q", acks[1], read(t, home, "newer-here.txt"))
	}
	if acks[2].Status != AckOK || read(t, home, "older-here.txt") != "peer, later" {
		t.Errorf("a later peer edit was not applied: %+v, content %q", acks[2], read(t, home, "older-here.txt"))
	}
	if acks[3].Status != AckSkipped || !exists(home, "keep-me.txt") {
		t.Errorf("a file edited here was deleted by the peer: %+v", acks[3])
	}
	if acks[4].Status != AckOK || read(t, home, "brand-new.txt") != "peer" {
		t.Errorf("a plain new file was refused: %+v", acks[4])
	}
	noTemps(t, home)
}

// The peer's change for a path the two records disagree on must not be judged
// against the record before the receiving round has dropped that path from
// it. The receiving side is slowed down here so that the change always
// arrives first; without the hold-back it is skipped as "deleted here,
// unchanged there" and the file never reaches the worker.
func TestLinkAPeerChangeWaitsForTheReceiversBaseToBeSettled(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		gs.put(hm, n, n)
	}
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	c.close()

	// The gateway's record does not know c.txt; the worker's does.
	store := RecordStore{Dir: gs.state}
	rec, err := store.Load("worker-1", hm)
	if err != nil || !rec.Valid {
		t.Fatalf("no record to edit: %v", err)
	}
	delete(rec.Entries, "c.txt")
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(ws.root(hm), "c.txt")) // deleted on the worker while apart

	c2 := connect(t, gs, ws, tweaks{})
	c2.wk.hookBeforeSettle = func(string) { time.Sleep(300 * time.Millisecond) }
	c2.syncHome(hm)

	if !gs.has(hm, "c.txt") || !ws.has(hm, "c.txt") {
		t.Fatalf("c.txt must be kept (gateway has=%v worker has=%v)", gs.has(hm, "c.txt"), ws.has(hm, "c.txt"))
	}
	if !sameHomes(gs, ws, hm) {
		t.Fatal("the homes differ")
	}
}

// A node starts watching a home before its first scan, so a program that
// writes right as a session opens is noticed. What the watcher reports while
// the first reconcile runs must therefore be sent once it is done.
func TestLinkAChangeNoticedDuringTheFirstReconcileIsSentAfterwards(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	ws.put(hm, "early.txt", "e")
	c := connect(t, gs, ws, tweaks{})
	once := make(chan struct{}, 1)
	once <- struct{}{}
	c.wk.hookBeforeSettle = func(string) {
		select {
		case <-once:
		default:
			return
		}
		// The scan is over and the round is not: a program writes a file and
		// the watcher reports it.
		ws.put(hm, "late.txt", "l")
		c.wk.Notify(hm, "late.txt")
	}
	c.syncHome(hm)
	waitFor(t, "the late file on the gateway", func() bool { return gs.has(hm, "late.txt") && gs.has(hm, "early.txt") })
	if gs.read(hm, "late.txt") != "l" {
		t.Fatalf("late.txt = %q", gs.read(hm, "late.txt"))
	}
}

// The gateway sends its pending drop orders as soon as a worker connects, so
// a home can be dropped while the worker still waits for it to reconcile.
// That is not a failure of the conversation.
func TestLinkWaitingForAHomeThatWasDroppedMeanwhileIsNotAFailure(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	ws.put(hm, "a.txt", "a")
	c := connect(t, gs, ws, tweaks{})
	if err := c.wk.Attach(hm); err != nil {
		t.Fatal(err)
	}
	c.wk.Detach(hm) // the drop arrives while the worker waits
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.wk.WaitSynced(ctx, hm); err == nil || !droppedMeanwhile(err) {
		t.Fatalf("waiting for a dropped home: %v", err)
	}
	if err := c.wk.WaitSynced(ctx, "guest-never"); !droppedMeanwhile(err) {
		t.Fatalf("waiting for a home that was never attached: %v", err)
	}
	for _, err := range []error{context.DeadlineExceeded, io.ErrClosedPipe, errors.New("disk full")} {
		if droppedMeanwhile(err) {
			t.Fatalf("%v must still end the conversation", err)
		}
	}
}

func TestLinkRecordsThatDifferInOnePathStillHonourDeletesElsewhere(t *testing.T) {
	gs, ws := newSite(t), newSite(t)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		gs.put(hm, n, n)
	}
	c := connect(t, gs, ws, tweaks{})
	c.syncHome(hm)
	c.close()

	// The gateway's record loses one path, as if a connection had dropped
	// while a change to it was in flight.
	store := RecordStore{Dir: gs.state}
	rec, err := store.Load("worker-1", hm)
	if err != nil || !rec.Valid {
		t.Fatalf("no record to edit: %v", err)
	}
	delete(rec.Entries, "c.txt")
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}

	// Offline, the worker deletes a.txt (recorded by both) and c.txt (not).
	os.Remove(filepath.Join(ws.root(hm), "a.txt"))
	os.Remove(filepath.Join(ws.root(hm), "c.txt"))

	c2 := connect(t, gs, ws, tweaks{})
	c2.syncHome(hm)
	if gs.has(hm, "a.txt") || ws.has(hm, "a.txt") {
		t.Fatal("a delete of a path both records agree on was lost because another path disagreed")
	}
	if !gs.has(hm, "c.txt") || !ws.has(hm, "c.txt") {
		t.Fatalf("for the path the records disagree on, the file must be kept, not deleted (gateway has=%v worker has=%v)", gs.has(hm, "c.txt"), ws.has(hm, "c.txt"))
	}
	if !gs.has(hm, "b.txt") || !sameHomes(gs, ws, hm) {
		t.Fatal("the homes differ")
	}
	// The records agree again afterwards.
	c2.close()
	g, _ := store.Load("worker-1", hm)
	w, _ := (RecordStore{Dir: ws.state}).Load("gateway", hm)
	if g.Digest() != w.Digest() {
		t.Fatalf("records still differ after the reconcile: %v vs %v", sortedPaths(g.Entries), sortedPaths(w.Entries))
	}
}
