package wsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ManagerConfig configures the synchronization of one node: a gateway
// (talking to several workers) or a worker (talking to the gateway).
type ManagerConfig struct {
	// Node is this node's id: "gateway", or the worker's id.
	Node      string
	IsGateway bool
	// BaseDir holds the homes (utils.HOME_DIR); StateDir holds the records.
	BaseDir, StateDir string
	MaxFileSize       int64
	// OwnerOf (gateway only) names the backend that runs a home's sessions.
	// A home with no known owner is accepted only from a worker that has
	// synchronized it before, which the gateway's records show.
	OwnerOf func(home string) (backend string, known bool)
	// OnDrop (worker only) is called when the gateway says a home expired or
	// moved away.
	OnDrop func(home string)
	// ReadyTimeout is how long a worker may take to reconcile its homes
	// after connecting. Default 5 minutes.
	ReadyTimeout time.Duration
	// Link tunables, passed through. Zero values use the defaults.
	Debounce, MaxDelay, ReconcileEvery time.Duration
	Window                             int
	Logf                               func(format string, args ...interface{})
}

// Manager runs the synchronization of one node: a single file watcher, and
// one Link per peer.
type Manager struct {
	cfg     ManagerConfig
	records RecordStore
	watcher *Watcher

	mu       sync.Mutex
	links    map[string]*Link  // peer -> conversation
	homeLink map[string]string // home -> peer that holds the other copy
	closed   bool
}

// NewManager creates a Manager and its watcher.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...interface{}) {}
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 5 * time.Minute
	}
	m := &Manager{
		cfg:      cfg,
		records:  RecordStore{Dir: cfg.StateDir},
		links:    make(map[string]*Link),
		homeLink: make(map[string]string),
	}
	w, err := NewWatcher(cfg.BaseDir, m.notify, cfg.Logf)
	if err != nil {
		return nil, err
	}
	m.watcher = w
	return m, nil
}

// Close stops the watcher and every conversation.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	links := make([]*Link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.Unlock()
	for _, l := range links {
		l.Close()
	}
	// Wait for the records to be written, so the next start reads what was
	// really agreed.
	for _, l := range links {
		select {
		case <-l.Stopped():
		case <-time.After(3 * time.Second):
		}
	}
	m.watcher.Close()
}

// notify is the watcher's callback: it hands a change to the conversation
// that holds the home's other copy.
func (m *Manager) notify(home, rel string) {
	m.mu.Lock()
	if home == "" {
		links := make([]*Link, 0, len(m.links))
		for _, l := range m.links {
			links = append(links, l)
		}
		m.mu.Unlock()
		for _, l := range links {
			l.NotifyAll()
		}
		return
	}
	l := m.links[m.homeLink[home]]
	m.mu.Unlock()
	if l != nil {
		l.Notify(home, rel)
	}
}

// Homes lists the homes that have been synchronized with a peer before.
func (m *Manager) Homes(peer string) []string {
	if ValidHomeName(peer) != nil {
		return nil
	}
	files, err := ioutil.ReadDir(m.cfg.StateDir + "/" + peer)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		n := f.Name()
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	return out
}

// Offset returns the clock offset measured for a connected peer, and whether
// there is a conversation with it.
func (m *Manager) Offset(peer string) (time.Duration, bool) {
	l := m.link(peer)
	if l == nil {
		return 0, false
	}
	return l.Offset(), true
}

// HasRecord reports whether any peer has synchronized the home with this
// node before.
func (m *Manager) HasRecord(home string) bool {
	entries, err := ioutil.ReadDir(m.cfg.StateDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && m.hasRecord(e.Name(), home) {
			return true
		}
	}
	return false
}

// StaleHome is a home whose record has not changed for a long time.
type StaleHome struct {
	Peer, Home string
	Age        time.Duration
}

// StaleHomes lists the homes synchronized with any peer whose record is older
// than d. A record is written a moment after the last change, so its age is
// how long the home has been idle. The gateway uses it to find guest homes
// that outlived its own memory of them (a restart).
func (m *Manager) StaleHomes(d time.Duration) []StaleHome {
	var out []StaleHome
	peers, err := ioutil.ReadDir(m.cfg.StateDir)
	if err != nil {
		return nil
	}
	now := time.Now()
	for _, p := range peers {
		if !p.IsDir() || ValidHomeName(p.Name()) != nil {
			continue
		}
		files, err := ioutil.ReadDir(filepath.Join(m.cfg.StateDir, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			n := f.Name()
			if !strings.HasSuffix(n, ".json") || strings.HasPrefix(n, ".") {
				continue
			}
			if age := now.Sub(f.ModTime()); age > d {
				out = append(out, StaleHome{Peer: p.Name(), Home: strings.TrimSuffix(n, ".json"), Age: age})
			}
		}
	}
	return out
}

func (m *Manager) hasRecord(peer, home string) bool {
	for _, h := range m.Homes(peer) {
		if h == home {
			return true
		}
	}
	return false
}

// accept decides whether a peer may start synchronizing a home with us. It
// only runs on the gateway.
func (m *Manager) accept(peer, home string) error {
	if !m.cfg.IsGateway {
		return nil
	}
	if m.cfg.OwnerOf != nil {
		if owner, known := m.cfg.OwnerOf(home); known {
			if owner != peer {
				return fmt.Errorf("home %s belongs to %s, not %s", home, owner, peer)
			}
			return nil
		}
	}
	if m.hasRecord(peer, home) {
		return nil // it synchronized this home before; the gateway just restarted
	}
	return fmt.Errorf("%s does not own home %s", peer, home)
}

func (m *Manager) link(peer string) *Link {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[peer]
}

// Serve runs the conversation with a peer over conn until it ends. On a
// worker it first reconciles every home it has synchronized before, and
// calls onReady when all are in step; the worker takes no new sessions
// before that. It returns the reason the conversation ended.
func (m *Manager) Serve(ctx context.Context, peer string, conn io.ReadWriteCloser, onReady func()) error {
	if err := ValidHomeName(peer); err != nil {
		conn.Close()
		return err
	}
	var l *Link
	l = NewLink(Config{
		Node: m.cfg.Node, Peer: peer, IsGateway: m.cfg.IsGateway,
		BaseDir: m.cfg.BaseDir, Records: m.records, MaxFileSize: m.cfg.MaxFileSize,
		Accept:         func(home string) error { return m.accept(peer, home) },
		OnAttach:       func(home string) { m.homeAttached(peer, home) },
		OnDrop:         func(home string) { m.handleDrop(peer, home) },
		OnRefused:      func(home string) { m.homeRefused(peer, l, home) },
		Debounce:       m.cfg.Debounce,
		MaxDelay:       m.cfg.MaxDelay,
		Window:         m.cfg.Window,
		ReconcileEvery: m.cfg.ReconcileEvery,
		Logf:           m.cfg.Logf,
	}, conn)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		conn.Close()
		return io.ErrClosedPipe
	}
	old := m.links[peer]
	m.links[peer] = l
	m.mu.Unlock()
	if old != nil {
		// The peer reconnected. Let the old conversation finish writing its
		// records before the new one reads them.
		old.Close()
		select {
		case <-old.Stopped():
		case <-time.After(5 * time.Second):
			m.cfg.Logf("wsync: the previous conversation with %s did not stop in time", peer)
		}
	}
	defer func() {
		m.mu.Lock()
		if m.links[peer] == l {
			delete(m.links, peer)
			for home, p := range m.homeLink {
				if p == peer {
					delete(m.homeLink, home)
					m.watcher.RemoveHome(home)
				}
			}
		}
		m.mu.Unlock()
	}()

	runDone := make(chan error, 1)
	go func() { runDone <- l.Run(ctx) }()

	if m.cfg.IsGateway {
		m.deliverPendingDrops(peer, l)
	} else {
		go m.reconcileOwned(ctx, peer, l, onReady)
	}
	return <-runDone
}

// reconcileOwned attaches every home this worker synchronized before and
// waits for all of them, then reports ready. If that takes too long or fails
// the conversation is closed, so the worker reconnects and tries again.
func (m *Manager) reconcileOwned(ctx context.Context, peer string, l *Link, onReady func()) {
	ctx, cancel := context.WithTimeout(ctx, m.cfg.ReadyTimeout)
	defer cancel()
	homes := m.Homes(peer)
	for _, home := range homes {
		if err := l.Attach(home); err != nil {
			m.cfg.Logf("wsync: cannot attach %s: %v", home, err)
			l.Close()
			return
		}
	}
	for _, home := range homes {
		if err := l.WaitSynced(ctx, home); err != nil {
			if droppedMeanwhile(err) {
				continue // dropped by the gateway while we were reconciling it
			}
			var refused *RefusedError
			if errors.As(err, &refused) {
				// The gateway does not take this home now: it was dropped
				// while this worker was away, or the gateway has no session
				// for it yet. That is not a reason to stay out of rotation.
				// The home is asked for again when a request needs it.
				continue
			}
			m.cfg.Logf("wsync: reconciling %s with %s failed: %v", home, peer, err)
			l.Close()
			return
		}
	}
	if onReady != nil {
		onReady()
	}
}

// droppedMeanwhile reports whether a wait for a home ended because the
// conversation no longer holds it: the gateway dropped it, which it does for a
// home that expired or moved to another node, or refused it before the wait
// began. The gateway sends its pending drop orders as soon as a worker
// connects, so one can arrive while the worker is still reconciling the home.
func droppedMeanwhile(err error) bool {
	return errors.Is(err, errDetached) || errors.Is(err, errNotAttached)
}

// homeAttached starts watching a home as its conversation starts, before the
// first scan. A change made between the scan and the start of the watch would
// otherwise produce no event, and would wait for the next full reconcile.
// Events that arrive while the first reconcile runs only mark paths to look at
// again afterwards; a path that matches what was agreed sends nothing.
func (m *Manager) homeAttached(peer, home string) {
	m.mu.Lock()
	m.homeLink[home] = peer
	m.mu.Unlock()
	if err := m.watcher.AddHome(home); err != nil {
		m.cfg.Logf("wsync: cannot watch %s: %v", home, err)
		// Without events the periodic reconcile still keeps it in step.
	}
}

// homeRefused runs when the peer would not synchronize a home. Nothing will be
// exchanged for it, so there is nothing to watch, unless a request has
// attached the home again meanwhile: that one keeps its watch.
func (m *Manager) homeRefused(peer string, l *Link, home string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.home(home) != nil || m.homeLink[home] != peer {
		return
	}
	delete(m.homeLink, home)
	m.watcher.RemoveHome(home)
}

// EnsureHome makes sure a home is synchronized with a peer and waits until
// it is. On a worker, call it before the first request for a home runs; on a
// gateway, to send a home to a worker that is about to run a session for it.
// A peer that refuses the home is asked again by the next call.
func (m *Manager) EnsureHome(ctx context.Context, peer, home string) error {
	l := m.link(peer)
	if l == nil {
		return errors.New("wsync: no conversation with " + peer)
	}
	if l.Synced(home) {
		return nil
	}
	m.mu.Lock()
	m.homeLink[home] = peer
	m.mu.Unlock()
	if err := l.Attach(home); err != nil {
		return err
	}
	return l.WaitSynced(ctx, home)
}

// Synced reports whether a home is in step with a peer.
func (m *Manager) Synced(peer, home string) bool {
	l := m.link(peer)
	return l != nil && l.Synced(home)
}

// Connected reports whether there is a conversation with a peer.
func (m *Manager) Connected(peer string) bool { return m.link(peer) != nil }

// ErrNoCopy means this node's copy of a home cannot stand in for a peer's:
// the two were never brought in step.
var ErrNoCopy = errors.New("wsync: this node holds no complete copy of the home")

// SecureCopy makes sure that this node's copy of a home can stand in for a
// peer's, which must hold before the peer's copy is given up. With a
// connected peer the two copies are brought in step now, if they were not
// yet. For a peer that is away, a complete reconcile with it must be on
// record and the files must still be here; what the peer changed after it
// was last seen is not in the copy.
func (m *Manager) SecureCopy(ctx context.Context, peer, home string) error {
	if err := ValidHomeName(home); err != nil {
		return err
	}
	if err := ValidHomeName(peer); err != nil {
		return err
	}
	if m.link(peer) != nil {
		return m.EnsureHome(ctx, peer, home)
	}
	if !m.hasRecord(peer, home) {
		return ErrNoCopy
	}
	rec, err := m.records.Load(peer, home)
	if err != nil {
		return err
	}
	if rec.Partial {
		return ErrNoCopy
	}
	if fi, err := os.Stat(filepath.Join(m.cfg.BaseDir, home)); err != nil || !fi.IsDir() {
		return ErrNoCopy
	}
	return nil
}

// DropMoved is Drop for a home whose sessions now run on another node: the
// peer is told to delete its copy only once SecureCopy says that the copy
// here can stand in for it. Otherwise nothing changes on either side and the
// error says why, because the peer's copy may be the only complete one.
func (m *Manager) DropMoved(ctx context.Context, peer, home string) error {
	if err := m.SecureCopy(ctx, peer, home); err != nil {
		return err
	}
	return m.Drop(peer, home)
}

// Drop tells a peer to delete its copy of a home, and forgets the home on
// this side: its record, its watch and its place in the conversation. If the
// peer is not connected the order is kept and delivered when it connects.
// It does not check that another copy exists: use it for a home that is to be
// deleted everywhere, and DropMoved for one that moved.
func (m *Manager) Drop(peer, home string) error {
	if err := ValidHomeName(home); err != nil {
		return err
	}
	if err := ValidHomeName(peer); err != nil {
		return err
	}
	if l := m.link(peer); l != nil {
		l.Drop(home)
		l.Detach(home)
	} else if err := m.markDrop(peer, home); err != nil {
		return err
	}
	m.watcher.RemoveHome(home)
	m.mu.Lock()
	delete(m.homeLink, home)
	m.mu.Unlock()
	return m.records.Remove(peer, home)
}

func (m *Manager) dropMarker(peer, home string) string {
	return filepath.Join(m.cfg.StateDir, peer, home+".drop")
}

func (m *Manager) markDrop(peer, home string) error {
	p := m.dropMarker(peer, home)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return ioutil.WriteFile(p, nil, 0600)
}

// deliverPendingDrops sends the drop orders that were waiting for a peer.
func (m *Manager) deliverPendingDrops(peer string, l *Link) {
	files, err := ioutil.ReadDir(filepath.Join(m.cfg.StateDir, peer))
	if err != nil {
		return
	}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".drop") || strings.HasPrefix(name, ".") {
			continue
		}
		home := strings.TrimSuffix(name, ".drop")
		if ValidHomeName(home) != nil {
			continue
		}
		l.Drop(home)
		os.Remove(filepath.Join(m.cfg.StateDir, peer, name))
	}
}

// handleDrop runs on a worker when the gateway says a home expired or moved
// away: the home is forgotten here, and the owner of the node removes its
// files.
func (m *Manager) handleDrop(peer, home string) {
	if m.cfg.IsGateway || ValidHomeName(home) != nil {
		return
	}
	if l := m.link(peer); l != nil {
		l.Detach(home)
	}
	m.watcher.RemoveHome(home)
	m.mu.Lock()
	delete(m.homeLink, home)
	m.mu.Unlock()
	m.records.Remove(peer, home)
	if m.cfg.OnDrop != nil {
		m.cfg.OnDrop(home)
	}
}
