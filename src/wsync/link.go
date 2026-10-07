package wsync

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Config configures one side of a sync conversation.
type Config struct {
	// Node is this side's id; Peer is the other side's. Peer names the
	// records on disk, so a home has one record per peer.
	Node, Peer string
	// IsGateway says which side wins a tie and defines the clock offset.
	IsGateway bool
	// BaseDir is the directory that holds the homes (utils.HOME_DIR).
	BaseDir string
	// Records stores the base records.
	Records RecordStore
	// Accept is asked before a home the peer starts is synchronized. A
	// gateway uses it to check that the peer owns the home. Nil accepts all.
	Accept func(home string) error
	// OnAttach is called when a home's conversation starts, before its first
	// scan. A node starts watching the home here, so that a change made
	// between the scan and the first event is not missed.
	OnAttach func(home string)
	// OnSynced is called when a home has been reconciled.
	OnSynced func(home string)
	// OnDrop is called when the peer says a home expired or moved away.
	OnDrop func(home string)
	// OnRefused is called when the peer would not synchronize a home, after
	// the home has been forgotten here.
	OnRefused func(home string)
	// MaxFileSize is the largest file synchronized. Zero means the default.
	MaxFileSize int64
	// Debounce is how long a home must be quiet before local changes are
	// sent; MaxDelay is the longest they are held back. Defaults 300ms, 2s.
	Debounce, MaxDelay time.Duration
	// Window is how many changes may be waiting for an acknowledgement.
	// Default 8.
	Window int
	// ReconcileEvery makes every attached home be reconciled in full this
	// often, as a safety net for a change that was never reported or a
	// disagreement that built up. Default 10 minutes; negative turns it off.
	ReconcileEvery time.Duration
	// Now is the clock, for tests. Defaults to time.Now.
	Now func() time.Time
	// Logf receives diagnostics. Defaults to discarding them.
	Logf func(format string, args ...interface{})
}

func (c *Config) defaults() {
	if c.MaxFileSize <= 0 {
		c.MaxFileSize = DefaultMaxFileSize
	}
	if c.Debounce <= 0 {
		c.Debounce = 300 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 2 * time.Second
	}
	if c.Window <= 0 {
		c.Window = 8
	}
	if c.ReconcileEvery == 0 {
		c.ReconcileEvery = 10 * time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logf == nil {
		c.Logf = func(string, ...interface{}) {}
	}
}

// maxManifest bounds the entries accepted in one attach.
const maxManifest = 1 << 20

// retryDelay is how long a failed reconcile or change waits before trying again.
const retryDelay = 5 * time.Second

// saveDelay is how long after the last change the record is written.
const saveDelay = 400 * time.Millisecond

// Link is one conversation with a peer over a stream. It synchronizes every
// home attached to it, in both directions.
type Link struct {
	cfg Config
	rwc io.ReadWriteCloser
	fr  *frameReader
	q   *outQueue
	win chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool // no more goroutines may be started
	homes  map[string]*homeSync
	sent   map[uint64]*sentOp
	seq    uint64

	pong        chan Message
	offsetReady chan struct{}
	offsetOnce  sync.Once
	offsetNs    int64 // worker clock minus gateway clock, set before offsetReady closes

	failOnce sync.Once
	errMu    sync.Mutex
	err      error
	done     chan struct{}
	stopped  chan struct{} // closed when Run has returned and the records are saved

	statMu sync.Mutex
	stats  map[string]int

	// hookBeforeOpen is called before a file's content is opened for sending.
	// Tests use it to change a file at the worst moment.
	hookBeforeOpen func(home, rel string)
	// hookBeforeSettle is called in a round once the peer's manifest is in
	// hand and before this side decides what the two sides agreed on.
	hookBeforeSettle func(home string)
}

// NewLink creates a Link over rwc. Call Run to start it.
func NewLink(cfg Config, rwc io.ReadWriteCloser) *Link {
	cfg.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &Link{
		ctx:         ctx,
		cancel:      cancel,
		cfg:         cfg,
		rwc:         rwc,
		fr:          newFrameReader(rwc),
		q:           newOutQueue(),
		win:         make(chan struct{}, cfg.Window),
		homes:       make(map[string]*homeSync),
		sent:        make(map[uint64]*sentOp),
		pong:        make(chan Message, 8),
		offsetReady: make(chan struct{}),
		done:        make(chan struct{}),
		stopped:     make(chan struct{}),
		stats:       make(map[string]int),
	}
}

// Stats returns how many messages of each type were sent ("sent:put") and
// received ("recv:put").
func (l *Link) Stats() map[string]int {
	l.statMu.Lock()
	defer l.statMu.Unlock()
	out := make(map[string]int, len(l.stats))
	for k, v := range l.stats {
		out[k] = v
	}
	return out
}

func (l *Link) count(kind, typ string) {
	l.statMu.Lock()
	l.stats[kind+":"+typ]++
	l.statMu.Unlock()
}

// Offset is the worker's clock minus the gateway's, as measured when the
// conversation started.
func (l *Link) Offset() time.Duration { return time.Duration(atomic.LoadInt64(&l.offsetNs)) }

// Done is closed when the conversation has ended.
func (l *Link) Done() <-chan struct{} { return l.done }

// Stopped is closed when Run has returned: every goroutine of the link has
// ended and the records are written. A new conversation with the same peer
// should wait for it, or it may start from a record that is about to be
// replaced.
func (l *Link) Stopped() <-chan struct{} { return l.stopped }

// Err is why the conversation ended.
func (l *Link) Err() error {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.err
}

// fail ends the conversation. The first error is kept.
func (l *Link) fail(err error) {
	l.failOnce.Do(func() {
		l.errMu.Lock()
		l.err = err
		l.errMu.Unlock()
		if l.cancel != nil {
			l.cancel()
		}
		l.q.close()
		l.rwc.Close()
		close(l.done)
	})
}

// Run runs the conversation until the stream ends, an error occurs, or ctx
// is cancelled. It returns nil when the peer closed the stream cleanly.
func (l *Link) Run(ctx context.Context) error {
	defer close(l.stopped)
	l.wg.Add(1)
	go l.writeLoop()
	go func() {
		select {
		case <-ctx.Done():
			l.fail(ctx.Err())
		case <-l.ctx.Done():
			l.fail(l.ctx.Err())
		case <-l.done:
		}
	}()

	l.send(Message{Type: MsgHello, Node: l.cfg.Node, Version: ProtocolVersion, Now: l.cfg.Now().UnixNano()}, nil, nil)
	err := l.readLoop()
	if err == io.EOF || err == io.ErrClosedPipe || errors.Is(err, os.ErrClosed) {
		err = nil
	}
	l.fail(err)
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.wg.Wait()
	l.mu.Lock()
	for _, hs := range l.homes {
		hs.finish(l.Err())
		hs.mu.Lock()
		if hs.saveTimer != nil {
			hs.saveTimer.Stop()
			hs.saveTimer = nil
		}
		// Only a home that completed a reconcile has a record worth keeping.
		if hs.synced {
			l.saveLocked(hs)
		}
		hs.mu.Unlock()
	}
	l.mu.Unlock()
	if e := l.Err(); e != nil && !errors.Is(e, context.Canceled) {
		return e
	}
	return nil
}

// --- Outgoing frames ---

type outFrame struct {
	msg  Message
	body io.Reader
	done func(error)
}

type outQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []outFrame
	closed bool
}

func newOutQueue() *outQueue {
	q := &outQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *outQueue) push(f outFrame) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, f)
	q.cond.Signal()
	return true
}

func (q *outQueue) pop() (outFrame, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return outFrame{}, false
	}
	f := q.items[0]
	q.items = q.items[1:]
	return f, true
}

func (q *outQueue) close() {
	q.mu.Lock()
	q.closed = true
	rest := q.items
	q.items = nil
	q.cond.Broadcast()
	q.mu.Unlock()
	for _, f := range rest {
		if f.done != nil {
			f.done(io.ErrClosedPipe)
		}
	}
}

// send queues a frame. It never blocks, so a handler that must answer (an
// acknowledgement) cannot get stuck behind a large transfer.
func (l *Link) send(m Message, body io.Reader, done func(error)) {
	l.count("sent", m.Type)
	if !l.q.push(outFrame{msg: m, body: body, done: done}) && done != nil {
		done(io.ErrClosedPipe)
	}
}

func (l *Link) writeLoop() {
	defer l.wg.Done()
	w := bufio.NewWriterSize(l.rwc, 64<<10)
	for {
		f, ok := l.q.pop()
		if !ok {
			return
		}
		err := writeFrame(w, f.msg, f.body)
		if err == nil {
			err = w.Flush()
		}
		if f.done != nil {
			f.done(err)
		}
		if err != nil {
			l.fail(err)
			return
		}
	}
}

// --- Incoming frames ---

func (l *Link) readLoop() error {
	for {
		m, err := l.fr.next()
		if err != nil {
			return err
		}
		l.count("recv", m.Type)
		body := l.fr.body(m)
		herr := l.handle(m, body)
		if derr := body.drain(); herr == nil && derr != nil {
			herr = derr
		}
		if herr != nil {
			return herr
		}
	}
}

func (l *Link) handle(m Message, body *bodyReader) error {
	switch m.Type {
	case MsgHello:
		if m.Version != ProtocolVersion {
			l.send(Message{Type: MsgFatal, Reason: fmt.Sprintf("protocol version %d is not supported", m.Version)}, nil, nil)
			return fmt.Errorf("wsync: peer speaks protocol version %d, we speak %d", m.Version, ProtocolVersion)
		}
		if l.cfg.IsGateway {
			l.mu.Lock()
			if !l.closed {
				l.wg.Add(1)
				go l.measureOffset()
			}
			l.mu.Unlock()
		}
	case MsgPing:
		l.send(Message{Type: MsgPong, T0: m.T0, Now: l.cfg.Now().UnixNano()}, nil, nil)
	case MsgPong:
		select {
		case l.pong <- m:
		default:
		}
	case MsgOffset:
		l.setOffset(m.Offset)
	case MsgAttach:
		return l.handleAttach(m)
	case MsgPut, MsgDelete, MsgRename:
		l.handleChange(m, body)
	case MsgAck:
		l.handleAck(m)
	case MsgSynced:
		if hs := l.home(m.Home); hs != nil {
			select {
			case hs.peerSynced <- struct{}{}:
			default:
			}
		}
	case MsgDrop:
		if l.cfg.OnDrop != nil && ValidHomeName(m.Home) == nil {
			l.cfg.OnDrop(m.Home)
		}
	case MsgRefuse:
		// The peer will not synchronize the home now. That can change: a
		// gateway refuses a home that has no session yet, and has one for it
		// a moment later. So the home is forgotten here, and whoever attaches
		// it next asks the peer again.
		if l.forget(m.Home, &RefusedError{Home: m.Home, Reason: m.Reason}) {
			l.cfg.Logf("wsync: %s refused home %q: %s", l.cfg.Peer, m.Home, m.Reason)
			if l.cfg.OnRefused != nil {
				l.cfg.OnRefused(m.Home)
			}
		}
	case MsgFatal:
		return fmt.Errorf("wsync: peer ended the conversation: %s", m.Reason)
	default:
		l.cfg.Logf("wsync: ignoring unknown message %q", m.Type)
	}
	return nil
}

// --- Clock offset ---

func (l *Link) setOffset(ns int64) {
	l.offsetOnce.Do(func() {
		atomic.StoreInt64(&l.offsetNs, ns)
		close(l.offsetReady)
	})
}

// measureOffset runs on the gateway: three round trips, keeping the one with
// the shortest delay, then tells the worker the result so both sides use the
// same number.
func (l *Link) measureOffset() {
	defer l.wg.Done()
	var best int64 = -1
	var offset int64
	for i := 0; i < 3; i++ {
		t0 := l.cfg.Now().UnixNano()
		l.send(Message{Type: MsgPing, T0: t0}, nil, nil)
		select {
		case p := <-l.pong:
			t1 := l.cfg.Now().UnixNano()
			rtt := t1 - t0
			if best < 0 || rtt < best {
				best = rtt
				offset = p.Now - (t0 + rtt/2)
			}
		case <-l.ctx.Done():
			return
		case <-time.After(10 * time.Second):
			l.fail(errors.New("wsync: the peer did not answer a clock ping"))
			return
		}
	}
	const warn = 2 * time.Second
	if d := time.Duration(offset); d > warn || d < -warn {
		l.cfg.Logf("wsync: the clock of %s differs from the gateway's by %v; file times are corrected for it, but check its time synchronisation", l.cfg.Peer, d)
	}
	l.setOffset(offset)
	l.send(Message{Type: MsgOffset, Offset: offset}, nil, nil)
}

// --- Homes ---

// homeSync is the state of one home on this link.
type homeSync struct {
	name string
	home *Home

	// ctx ends when the link ends or the home is detached.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	rec     *Record
	pending map[string]pend // changes sent and not yet acknowledged
	mine    map[string]Entry
	synced  bool
	failed  error
	retries int

	// The record is saved a moment after it last changed, and when the
	// conversation ends, so a reconnect starts from what was really agreed.
	recDirty  bool
	saveTimer *time.Timer

	outstanding int

	// dmu guards the set of changed paths. It is separate from mu so that a
	// file watcher is never held up by a transfer in progress.
	dmu   sync.Mutex
	dirty map[string]struct{}
	full  bool // a whole-home reconcile is wanted

	// unsettled counts the peer manifests that have arrived and whose round
	// has not yet settled this side's base; settled is closed when the last
	// one has. Both are guarded by mu. See waitSettled.
	unsettled int
	settled   chan struct{}

	// detached is set when the home is dropped. Nothing may be written to its
	// record after that, whatever a round that was already running does.
	detached bool

	peerAttach chan Message
	peerSynced chan struct{}
	wake       chan struct{}
	ackCh      chan struct{}
	syncedCh   chan struct{}
	doneOnce   sync.Once
}

// manifestArrived notes that the peer's manifest for a round has arrived.
func (hs *homeSync) manifestArrived() {
	hs.mu.Lock()
	if hs.unsettled == 0 {
		hs.settled = make(chan struct{})
	}
	hs.unsettled++
	hs.mu.Unlock()
}

// settleLocked notes that a round has settled its base. The caller holds
// hs.mu.
func (hs *homeSync) settleLocked() {
	if hs.unsettled == 0 {
		return
	}
	hs.unsettled--
	if hs.unsettled == 0 {
		close(hs.settled)
	}
}

// waitSettled holds back a change from the peer while a round has the peer's
// manifest but has not yet decided what the two sides agreed on. A round may
// drop paths from the live record that the two records disagree on, and a
// change judged against them before that would be skipped as "deleted here,
// unchanged there", although the round treats the path as new on one side.
// Before the peer's manifest has arrived nothing is held back: the peer sends
// its changes for a round only after it has our manifest, and our manifest
// goes out from the round itself, so this cannot wait for a message that is
// queued behind the change. It returns false if the home ended meanwhile.
func (hs *homeSync) waitSettled() bool {
	hs.mu.Lock()
	ch, wait := hs.settled, hs.unsettled > 0
	hs.mu.Unlock()
	if !wait {
		return true
	}
	select {
	case <-ch:
		return true
	case <-hs.ctx.Done():
		return false
	}
}

// pend is a sent change that is not yet acknowledged.
type pend struct {
	e       Entry
	present bool
}

// recordChanged notes that the record changed and schedules it to be saved.
// The caller holds hs.mu.
func (l *Link) recordChanged(hs *homeSync) {
	hs.recDirty = true
	if hs.saveTimer != nil {
		return
	}
	hs.saveTimer = time.AfterFunc(saveDelay, func() {
		hs.mu.Lock()
		hs.saveTimer = nil
		l.saveLocked(hs)
		hs.mu.Unlock()
	})
}

// saveLocked writes the record if it changed. The caller holds hs.mu.
func (l *Link) saveLocked(hs *homeSync) {
	if !hs.recDirty || hs.detached {
		return
	}
	hs.recDirty = false
	if err := l.cfg.Records.Save(hs.rec); err != nil && l.ctx.Err() == nil {
		l.cfg.Logf("wsync: saving the record of %s: %v", hs.name, err)
	}
}

func (hs *homeSync) finish(err error) {
	hs.doneOnce.Do(func() {
		hs.mu.Lock()
		hs.failed = err
		hs.mu.Unlock()
		close(hs.syncedCh)
	})
}

func (hs *homeSync) markDirty(rel string) {
	hs.dmu.Lock()
	hs.dirty[rel] = struct{}{}
	hs.dmu.Unlock()
	hs.signal(hs.wake)
}

func (hs *homeSync) markFull() {
	hs.dmu.Lock()
	hs.full = true
	hs.dmu.Unlock()
	hs.signal(hs.wake)
}

func (hs *homeSync) wantsFull() bool {
	hs.dmu.Lock()
	defer hs.dmu.Unlock()
	return hs.full
}

func (hs *homeSync) takeFull() bool {
	hs.dmu.Lock()
	defer hs.dmu.Unlock()
	f := hs.full
	hs.full = false
	return f
}

func (hs *homeSync) takeDirty() map[string]struct{} {
	hs.dmu.Lock()
	defer hs.dmu.Unlock()
	d := hs.dirty
	hs.dirty = make(map[string]struct{})
	return d
}

func (hs *homeSync) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (l *Link) home(name string) *homeSync {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.homes[name]
}

// homeFor returns the state of a home, starting to synchronize it if this is
// the first time. incoming is true when the peer asked for it.
func (l *Link) homeFor(name string, incoming bool) (*homeSync, error) {
	if err := ValidHomeName(name); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if hs := l.homes[name]; hs != nil {
		return hs, nil
	}
	if l.closed || l.ctx.Err() != nil {
		return nil, io.ErrClosedPipe
	}
	if incoming && l.cfg.Accept != nil {
		if err := l.cfg.Accept(name); err != nil {
			return nil, err
		}
	}
	h, err := OpenHome(l.cfg.BaseDir, name, true)
	if err != nil {
		return nil, err
	}
	h.MaxFileSize = l.cfg.MaxFileSize
	rec, err := l.cfg.Records.Load(l.cfg.Peer, name)
	if err != nil {
		// A damaged record is not trusted and not replaced by an empty one
		// silently: start from nothing, which deletes nothing.
		l.cfg.Logf("wsync: %v; reconciling %s from scratch", err, name)
		rec = NewRecord(name, l.cfg.Peer)
	}
	if !rec.Valid {
		// Nothing was agreed with this peer before. Until the first reconcile
		// is complete the record lists only what has arrived so far.
		rec.Partial = true
	}
	hctx, hcancel := context.WithCancel(l.ctx)
	hs := &homeSync{
		name:       name,
		home:       h,
		ctx:        hctx,
		cancel:     hcancel,
		rec:        rec,
		pending:    make(map[string]pend),
		dirty:      make(map[string]struct{}),
		peerAttach: make(chan Message, 4),
		peerSynced: make(chan struct{}, 4),
		wake:       make(chan struct{}, 1),
		ackCh:      make(chan struct{}, 1),
		syncedCh:   make(chan struct{}),
	}
	l.homes[name] = hs
	l.wg.Add(1)
	go l.homeLoop(hs)
	return hs, nil
}

// Attach starts synchronizing a home. It returns once the work is queued;
// use WaitSynced to wait for the reconcile.
func (l *Link) Attach(home string) error {
	_, err := l.homeFor(home, false)
	return err
}

// Synced reports whether a home has been reconciled.
func (l *Link) Synced(home string) bool {
	hs := l.home(home)
	if hs == nil {
		return false
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.synced
}

// errNotAttached is returned by WaitSynced for a home the conversation does
// not hold: it was never attached, or it was dropped while the caller waited.
var errNotAttached = errors.New("wsync: home is not attached")

// WaitSynced waits until a home has been reconciled.
func (l *Link) WaitSynced(ctx context.Context, home string) error {
	hs := l.home(home)
	if hs == nil {
		return fmt.Errorf("%w: %q", errNotAttached, home)
	}
	select {
	case <-hs.syncedCh:
		hs.mu.Lock()
		defer hs.mu.Unlock()
		return hs.failed
	case <-l.done:
		if err := l.Err(); err != nil {
			return err
		}
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify says that rel changed in a home. The change is sent after the home
// has been quiet for a moment. An empty rel means the whole home.
func (l *Link) Notify(home, rel string) {
	hs := l.home(home)
	if hs == nil {
		return
	}
	if rel == "" {
		hs.markFull()
	} else if ValidRel(rel) == nil {
		hs.markDirty(rel)
	}
}

// NotifyAll asks for a reconcile of every attached home, for when events may
// have been missed.
func (l *Link) NotifyAll() {
	l.mu.Lock()
	names := make([]string, 0, len(l.homes))
	for n := range l.homes {
		names = append(names, n)
	}
	l.mu.Unlock()
	for _, n := range names {
		l.Notify(n, "")
	}
}

// Detach stops synchronizing a home and forgets it, without touching its
// files or its record. Changes the peer sends for it are refused.
func (l *Link) Detach(home string) { l.forget(home, errDetached) }

// forget takes a home out of the conversation, without touching its files or
// its record, and ends the wait of whoever waits for it with err. It reports
// whether the conversation held the home. The home can be attached again.
func (l *Link) forget(home string, err error) bool {
	l.mu.Lock()
	hs := l.homes[home]
	delete(l.homes, home)
	l.mu.Unlock()
	if hs == nil {
		return false
	}
	hs.cancel()
	hs.mu.Lock()
	if hs.saveTimer != nil {
		hs.saveTimer.Stop()
		hs.saveTimer = nil
	}
	hs.recDirty = false // the record on disk must stay as it is, or go with a drop
	hs.detached = true
	hs.mu.Unlock()
	hs.finish(err)
	return true
}

var errDetached = errors.New("wsync: home was detached")

// RefusedError is returned by WaitSynced when the peer would not synchronize
// a home, for example because it does not own it.
type RefusedError struct {
	Home, Reason string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("wsync: peer refused home %q: %s", e.Home, e.Reason)
}

// Drop tells the peer that a home expired or moved away.
func (l *Link) Drop(home string) {
	l.send(Message{Type: MsgDrop, Home: home}, nil, nil)
}

// Close ends the conversation.
func (l *Link) Close() { l.fail(io.ErrClosedPipe) }

// --- The per-home loop ---

func (l *Link) homeLoop(hs *homeSync) {
	defer l.wg.Done()
	defer hs.home.Close()
	if l.cfg.OnAttach != nil {
		l.cfg.OnAttach(hs.name)
	}
	select {
	case <-l.offsetReady:
	case <-hs.ctx.Done():
		return
	}
	// The first round: both sides send their manifest, then act on both.
	if err := l.round(hs, nil); err != nil {
		l.homeFailed(hs, err)
		return
	}
	var periodic <-chan time.Time
	if l.cfg.ReconcileEvery > 0 {
		t := time.NewTicker(l.cfg.ReconcileEvery)
		defer t.Stop()
		periodic = t.C
	}
	for {
		select {
		case <-hs.ctx.Done():
			return
		case <-periodic:
			hs.markFull()
		case peer := <-hs.peerAttach:
			// The peer asked for another reconcile of this home.
			if err := l.round(hs, &peer); err != nil {
				l.homeFailed(hs, err)
				return
			}
		case <-hs.wake:
			if !hs.wantsFull() && !l.settle(hs) {
				return
			}
			if hs.takeFull() {
				if err := l.round(hs, nil); err != nil {
					l.homeFailed(hs, err)
					return
				}
				continue
			}
			if err := l.flushDirty(hs); err != nil {
				l.homeFailed(hs, err)
				return
			}
		}
	}
}

func (l *Link) homeFailed(hs *homeSync, err error) {
	if hs.ctx.Err() != nil {
		hs.finish(err) // detached, or the link ended: not a failure
		return
	}
	l.cfg.Logf("wsync: home %s: %v", hs.name, err)
	hs.finish(err)
	if !errors.Is(err, context.Canceled) {
		l.fail(err)
	}
}

// settle waits until the home has been quiet for the debounce time, or the
// longest delay has passed. It reports false if the link ended.
func (l *Link) settle(hs *homeSync) bool {
	quiet := time.NewTimer(l.cfg.Debounce)
	defer quiet.Stop()
	longest := time.NewTimer(l.cfg.MaxDelay)
	defer longest.Stop()
	for {
		select {
		case <-hs.wake:
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(l.cfg.Debounce)
		case <-quiet.C:
			return true
		case <-longest.C:
			return true
		case <-hs.ctx.Done():
			return false
		}
	}
}

// round reconciles a home with the peer. peer is the peer's manifest if it
// started the round.
func (l *Link) round(hs *homeSync, peer *Message) error {
	// However the round ends, it must not leave the reader waiting for it.
	settledRound := false
	defer func() {
		if !settledRound {
			hs.mu.Lock()
			hs.settleLocked()
			hs.mu.Unlock()
		}
	}()
	// Send our manifest.
	hs.mu.Lock()
	res, err := hs.home.Scan(ScanOptions{Base: hs.rec, MaxFileSize: l.cfg.MaxFileSize, Now: l.cfg.Now()})
	if err != nil {
		hs.mu.Unlock()
		return err
	}
	hs.mine = res.Entries
	for _, s := range res.Skipped {
		l.cfg.Logf("wsync: %s/%s not synchronized: %s", hs.name, s.Path, s.Reason)
	}
	// Freeze the agreed state together with the scan and the digest. The peer
	// decides what to send from exactly these three, so we must decide what to
	// send from the same ones, even if changes from the peer are applied to
	// the live record before we get to compare.
	snap := NewRecord(hs.name, l.cfg.Peer)
	snap.Valid = hs.rec.Valid
	for p, e := range hs.rec.Entries {
		snap.Entries[p] = e
	}
	digest := hs.rec.Digest()
	mine := make([]Entry, 0, len(res.Entries))
	for _, p := range sortedPaths(res.Entries) {
		mine = append(mine, res.Entries[p])
	}
	baseEntries := make([]Entry, 0, len(snap.Entries))
	for _, p := range sortedPaths(snap.Entries) {
		baseEntries = append(baseEntries, snap.Entries[p])
	}
	hs.mu.Unlock()
	l.send(Message{Type: MsgAttach, Home: hs.name, Digest: digest, Entries: mine, Base: baseEntries}, nil, nil)

	// Wait for the peer's manifest.
	if peer == nil {
		select {
		case m := <-hs.peerAttach:
			peer = &m
		case <-hs.ctx.Done():
			return hs.ctx.Err()
		}
	}
	remote := make(map[string]Entry, len(peer.Entries))
	if len(peer.Entries) > maxManifest || len(peer.Base) > maxManifest {
		return errors.New("wsync: manifest is too large")
	}
	for _, e := range peer.Entries {
		if ValidRel(e.Path) == nil {
			remote[e.Path] = e
		}
	}

	if l.hookBeforeSettle != nil {
		l.hookBeforeSettle(hs.name)
	}
	hs.mu.Lock()
	base := snap
	if digest != peer.Digest {
		// The two records do not describe the same agreement (a change was
		// in flight when a connection dropped, or one side lost its record).
		// Trust only what both records say and nothing else: for a path the
		// records disagree on, the reconcile is a union and deletes nothing.
		// Both sides compute the same intersection. What the frozen record
		// said and is not in it is forgotten from the live record, and is
		// recorded again when the path turns out to agree; anything agreed
		// since (a change just applied from the peer) stays.
		peerBase := make(map[string]Entry, len(peer.Base))
		for _, e := range peer.Base {
			if ValidRel(e.Path) == nil {
				peerBase[e.Path] = e
			}
		}
		base = NewRecord(hs.name, l.cfg.Peer)
		base.Valid = true
		dropped := 0
		for p, e := range snap.Entries {
			if pe, ok := peerBase[p]; ok && pe.Equal(e) {
				base.Entries[p] = e
				continue
			}
			dropped++
			if cur, ok := hs.rec.Entries[p]; ok && cur == e {
				delete(hs.rec.Entries, p)
			}
		}
		l.cfg.Logf("wsync: records of %s differ; %d path(s) are reconciled without them", hs.name, dropped)
	}
	// The base is settled: changes from the peer may now be applied.
	settledRound = true
	hs.settleLocked()
	actions := Compare(Params{
		Base: base, Local: hs.mine, Remote: remote,
		LocalIsGateway: l.cfg.IsGateway, Offset: l.Offset(),
	})
	var gives []Action
	for _, a := range actions {
		switch a.Kind {
		case Agree:
			// Only if nothing newer was agreed for the path meanwhile.
			cur, ok := hs.rec.Entries[a.Path]
			old, hadOld := snap.Entries[a.Path]
			if _, trusted := base.Entries[a.Path]; !trusted {
				hadOld = false // forgotten above
			}
			if ok != hadOld || (ok && cur != old) {
				continue
			}
			if a.Local != nil {
				hs.rec.Set(*a.Local)
			} else {
				hs.rec.Delete(a.Path)
			}
			hs.recDirty = true
		case GiveLocal, RenameRemote:
			gives = append(gives, a)
		}
	}
	hs.mu.Unlock()

	// Send what the peer needs, then say so.
	for _, a := range gives {
		if err := l.sendAction(hs, a); err != nil {
			return err
		}
	}
	l.send(Message{Type: MsgSynced, Home: hs.name}, nil, nil)

	// Done when the peer has sent everything for us and has acknowledged
	// everything we sent.
	select {
	case <-hs.peerSynced:
	case <-hs.ctx.Done():
		return hs.ctx.Err()
	}
	if err := l.waitIdle(hs); err != nil {
		return err
	}
	hs.mu.Lock()
	first := !hs.synced
	hs.synced = true
	hs.retries = 0
	hs.rec.Partial = false
	hs.recDirty = true
	l.saveLocked(hs)
	hs.mu.Unlock()
	hs.doneOnce.Do(func() { close(hs.syncedCh) })
	if first && l.cfg.OnSynced != nil {
		l.cfg.OnSynced(hs.name)
	}
	return nil
}

// waitIdle waits until everything sent has been acknowledged.
func (l *Link) waitIdle(hs *homeSync) error {
	for {
		hs.mu.Lock()
		n := hs.outstanding
		hs.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-hs.ackCh:
		case <-hs.ctx.Done():
			return hs.ctx.Err()
		}
	}
}

func (l *Link) handleAttach(m Message) error {
	hs, err := l.homeFor(m.Home, true)
	if err != nil {
		l.cfg.Logf("wsync: refusing home %q: %v", m.Home, err)
		l.send(Message{Type: MsgRefuse, Home: m.Home, Reason: err.Error()}, nil, nil)
		return nil
	}
	hs.manifestArrived()
	select {
	case hs.peerAttach <- m:
	default:
		return errors.New("wsync: the peer sent too many attach messages")
	}
	return nil
}

// --- Sending changes ---

// sentOp is a change waiting for its acknowledgement.
type sentOp struct {
	hs   *homeSync
	kind string
	path string
	from string
	e    *Entry // the entry at path for a put or rename
}

func (l *Link) acquire(ctx context.Context) error {
	select {
	case l.win <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Link) release() {
	select {
	case <-l.win:
	default:
	}
}

// sendAction sends one change to the peer and records it as pending.
func (l *Link) sendAction(hs *homeSync, a Action) error {
	if err := l.acquire(hs.ctx); err != nil {
		return err
	}
	l.mu.Lock()
	l.seq++
	seq := l.seq
	op := &sentOp{hs: hs, path: a.Path}
	l.sent[seq] = op
	l.mu.Unlock()

	msg := Message{Seq: seq, Home: hs.name}
	var body io.Reader
	var closer func()
	switch a.Kind {
	case RenameRemote:
		op.kind, op.from, op.e = MsgRename, a.From, a.Local
		msg.Type, msg.From, msg.Entry = MsgRename, a.From, a.Local
	case GiveLocal:
		if a.Local == nil {
			op.kind = MsgDelete
			msg.Type, msg.Path = MsgDelete, a.Path
		} else {
			op.kind, op.e = MsgPut, a.Local
			msg.Type, msg.Entry = MsgPut, a.Local
			if a.Local.Type == File {
				if l.hookBeforeOpen != nil {
					l.hookBeforeOpen(hs.name, a.Path)
				}
				f, err := hs.home.OpenRead(a.Path)
				if err != nil {
					// Gone or replaced since it was scanned; it will be
					// noticed again.
					l.abandon(seq, op, hs)
					hs.markDirty(a.Path)
					return nil
				}
				msg.Body = a.Local.Size
				body, closer = f, func() { f.Close() }
			}
		}
	}

	hs.mu.Lock()
	hs.outstanding++
	switch op.kind {
	case MsgDelete:
		hs.pending[a.Path] = pend{}
	case MsgPut:
		hs.pending[a.Path] = pend{e: *op.e, present: true}
	case MsgRename:
		hs.pending[op.from] = pend{}
		hs.pending[a.Path] = pend{e: *op.e, present: true}
	}
	hs.mu.Unlock()

	l.send(msg, body, func(err error) {
		if closer != nil {
			closer()
		}
	})
	return nil
}

// abandon forgets an operation that was never sent.
func (l *Link) abandon(seq uint64, op *sentOp, hs *homeSync) {
	l.mu.Lock()
	delete(l.sent, seq)
	l.mu.Unlock()
	l.release()
}

func (l *Link) handleAck(m Message) {
	l.mu.Lock()
	op := l.sent[m.Seq]
	delete(l.sent, m.Seq)
	l.mu.Unlock()
	if op == nil {
		return
	}
	hs := op.hs
	hs.mu.Lock()
	clear := func(p string) {
		delete(hs.pending, p)
	}
	switch m.Status {
	case AckOK:
		switch op.kind {
		case MsgPut:
			hs.rec.Set(*op.e)
		case MsgDelete:
			hs.rec.Delete(op.path)
		case MsgRename:
			hs.rec.Rename(op.from, op.path)
			hs.rec.Set(*op.e)
		}
	case AckError:
		l.cfg.Logf("wsync: %s: the peer could not apply %s %s: %s", hs.name, op.kind, op.path, m.Reason)
		// Try again later; a changing file is the usual cause.
		defer hs.markDirty(op.path)
		if op.from != "" {
			defer hs.markDirty(op.from)
		}
	case AckSkipped:
		if m.Reason == reasonRename {
			defer hs.markFull()
		}
	}
	clear(op.path)
	if op.from != "" {
		clear(op.from)
	}
	hs.outstanding--
	if m.Status == AckOK {
		l.recordChanged(hs)
	}
	retry := m.Status == AckError || (m.Status == AckSkipped && m.Reason == reasonRename)
	hs.mu.Unlock()
	l.release()
	hs.signal(hs.ackCh)
	if retry {
		time.AfterFunc(retryDelay, func() { hs.signal(hs.wake) })
	}
}

// reasonRename marks an ack that asks for a whole-home reconcile.
const reasonRename = "rename-mismatch"

// flushDirty sends the local changes that were noticed since the last pass.
// It compares what is on disk now with the agreed state, for only the paths
// that changed, using the same rules as a full reconcile.
func (l *Link) flushDirty(hs *homeSync) error {
	dirty := hs.takeDirty()
	if len(dirty) == 0 {
		return nil
	}
	hs.mu.Lock()

	local := make(map[string]Entry)
	touched := make(map[string]struct{})
	for rel := range dirty {
		touched[rel] = struct{}{}
		res, err := hs.home.ScanPath(rel, ScanOptions{Base: hs.rec, MaxFileSize: l.cfg.MaxFileSize, Now: l.cfg.Now()})
		if err != nil {
			hs.mu.Unlock()
			return err
		}
		for p, e := range res.Entries {
			local[p] = e
			touched[p] = struct{}{}
		}
		for _, s := range res.Skipped {
			l.cfg.Logf("wsync: %s/%s not synchronized: %s", hs.name, s.Path, s.Reason)
		}
	}
	// What is under a changed path in the agreed state may be gone now.
	for rel := range dirty {
		for p := range hs.rec.Entries {
			if hasPrefixPath(p, rel) {
				touched[p] = struct{}{}
			}
		}
		for p := range hs.pending {
			if hasPrefixPath(p, rel) {
				touched[p] = struct{}{}
			}
		}
	}
	// The agreed state of those paths, counting changes already sent.
	base := NewRecord(hs.name, l.cfg.Peer)
	base.Valid = true
	remote := make(map[string]Entry)
	for p := range touched {
		if e, ok := hs.effective(p); ok {
			base.Set(e)
			remote[p] = e
		}
	}
	actions := Compare(Params{
		Base: base, Local: local, Remote: remote,
		LocalIsGateway: l.cfg.IsGateway, Offset: l.Offset(),
	})
	hs.mu.Unlock()

	for _, a := range actions {
		if a.Kind != GiveLocal && a.Kind != RenameRemote {
			continue
		}
		if err := l.sendAction(hs, a); err != nil {
			return err
		}
	}
	return nil
}

// effective is the agreed state of a path, counting a change that was sent
// and is still waiting for its acknowledgement.
func (hs *homeSync) effective(path string) (Entry, bool) {
	if p, ok := hs.pending[path]; ok {
		return p.e, p.present
	}
	return hs.rec.Get(path)
}

// --- Applying changes from the peer ---

// localEntry describes a path as it is on disk now. A nil entry with a nil
// error means it does not exist.
func (hs *homeSync) localEntry(rel string, max int64) (*Entry, error) {
	e, found, skip := hs.home.entryAt(rel, hs.rec, max)
	if skip != "" {
		return nil, errors.New(skip)
	}
	if !found {
		return nil, nil
	}
	return &e, nil
}

func recEntry(r *Record, path string) *Entry {
	if e, ok := r.Get(path); ok {
		return &e
	}
	return nil
}

func (l *Link) handleChange(m Message, body *bodyReader) {
	hs := l.home(m.Home)
	if hs == nil {
		l.send(Message{Type: MsgAck, Seq: m.Seq, Status: AckError, Reason: "home is not attached"}, nil, nil)
		return
	}
	if !hs.waitSettled() {
		l.send(Message{Type: MsgAck, Seq: m.Seq, Status: AckError, Reason: "home is not attached"}, nil, nil)
		return
	}
	status, reason := l.apply(hs, m, body)
	if status == AckOK {
		hs.mu.Lock()
		l.recordChanged(hs)
		hs.mu.Unlock()
	}
	l.send(Message{Type: MsgAck, Seq: m.Seq, Status: status, Reason: reason}, nil, nil)
}

// apply applies one change from the peer unless this side has a newer change
// of its own to the same path, which is decided by the same rule as in a
// reconcile.
func (l *Link) apply(hs *homeSync, m Message, body *bodyReader) (status, reason string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	params := Params{LocalIsGateway: l.cfg.IsGateway, Offset: l.Offset()}
	max := l.cfg.MaxFileSize

	switch m.Type {
	case MsgPut:
		if m.Entry == nil || ValidRel(m.Entry.Path) != nil {
			return AckError, "invalid path"
		}
		e := *m.Entry
		cur, err := hs.localEntry(e.Path, max)
		if err != nil {
			return AckSkipped, err.Error()
		}
		act, differs := decide(params, e.Path, cur, &e, recEntry(hs.rec, e.Path))
		if !differs || act.Kind == Agree {
			hs.rec.Set(e)
			return AckOK, ""
		}
		if act.Kind == GiveLocal {
			return AckSkipped, "a newer local change wins"
		}
		var perr error
		switch e.Type {
		case File:
			perr = hs.home.WriteFile(e, body)
		case Dir:
			perr = hs.home.PutDir(e)
		case Symlink:
			perr = hs.home.PutSymlink(e)
		default:
			perr = ErrNotRegular
		}
		if perr != nil {
			if perr == ErrIsDirectory {
				return AckSkipped, "a directory with content is in the way"
			}
			return AckError, perr.Error()
		}
		hs.rec.Set(e)
		return AckOK, ""

	case MsgDelete:
		if ValidRel(m.Path) != nil {
			return AckError, "invalid path"
		}
		cur, err := hs.localEntry(m.Path, max)
		if err != nil {
			return AckSkipped, err.Error()
		}
		act, differs := decide(params, m.Path, cur, nil, recEntry(hs.rec, m.Path))
		if !differs || act.Kind == Agree {
			hs.rec.Delete(m.Path)
			return AckOK, ""
		}
		if act.Kind == GiveLocal {
			return AckSkipped, "a local change wins over the deletion"
		}
		if err := hs.home.Remove(m.Path); err != nil {
			if err == ErrNotEmpty {
				return AckSkipped, "directory is not empty"
			}
			return AckError, err.Error()
		}
		hs.rec.Delete(m.Path)
		return AckOK, ""

	case MsgRename:
		if m.Entry == nil || ValidRel(m.Entry.Path) != nil || ValidRel(m.From) != nil {
			return AckError, "invalid path"
		}
		e := *m.Entry
		curFrom, err1 := hs.localEntry(m.From, max)
		curTo, err2 := hs.localEntry(e.Path, max)
		if err1 != nil || err2 != nil {
			return AckSkipped, reasonRename
		}
		baseFrom := recEntry(hs.rec, m.From)
		switch {
		case curFrom == nil && curTo != nil && curTo.Equal(e):
			// Already moved.
		case curFrom != nil && baseFrom != nil && curFrom.Equal(*baseFrom) && curTo == nil && curFrom.Equal(withPath(e, m.From)):
			if err := hs.home.Rename(m.From, e.Path); err != nil {
				return AckError, err.Error()
			}
			hs.home.SetTime(e.Path, e.ModTime)
		default:
			return AckSkipped, reasonRename
		}
		hs.rec.Rename(m.From, e.Path)
		hs.rec.Set(e)
		return AckOK, ""
	}
	return AckError, "unknown change"
}

// withPath returns e with another path, for comparing content.
func withPath(e Entry, path string) Entry {
	e.Path = path
	return e
}

var _ = sort.Strings
