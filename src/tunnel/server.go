package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

const (
	handshakeTimeout = 15 * time.Second
	// A client address that fails authentication this often is refused for
	// the rest of the window.
	maxAuthFailures   = 10
	authFailureWindow = time.Minute
)

var workerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ServerConfig configures the gateway end of the tunnel.
type ServerConfig struct {
	// Token is the shared secret workers authenticate with. Required.
	Token string
	// HostKey identifies the gateway to workers. Required.
	HostKey ssh.Signer
	// CookieSecret and AuthToken are handed to a worker when it registers.
	CookieSecret func() []byte
	AuthToken    func() string
	// HeartbeatInterval is how often workers report; Timeout is how long a
	// silent worker stays ONLINE. Defaults: 10s and 30s.
	HeartbeatInterval time.Duration
	Timeout           time.Duration
	// WorkspaceSync turns on workspace synchronization: a worker is SYNCING
	// from registration until it reports its homes reconciled.
	WorkspaceSync bool
	// SyncTimeout is how long a worker may stay SYNCING before the gateway
	// drops the connection so that it tries again. Default 5 minutes.
	SyncTimeout time.Duration
	// OnOnline and OnOffline are called, in order, as workers come and go.
	OnOnline  func(*Worker)
	OnOffline func(*Worker)
	// OnSyncReady is called when a worker reports its homes reconciled.
	OnSyncReady func(*Worker)
	// OnRoute is called when a worker announces or withdraws a key it owns.
	OnRoute func(w *Worker, ev RouteEvent, open bool)
	// Logf defaults to log.Printf.
	Logf func(format string, args ...interface{})
}

// Server accepts worker connections and tracks the registered workers.
type Server struct {
	cfg      ServerConfig
	upgrader websocket.Upgrader

	mu      sync.Mutex
	workers map[string]*Worker
	drained map[string]bool // worker ids an admin put in drain

	cbMu sync.Mutex // keeps OnOnline/OnOffline calls in order

	failMu sync.Mutex
	fails  map[string]*authFailures
}

type authFailures struct {
	count int
	reset time.Time
}

// NewServer validates cfg and creates a Server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Token == "" {
		return nil, errors.New("tunnel: a worker token is required")
	}
	if cfg.HostKey == nil {
		return nil, errors.New("tunnel: a host key is required")
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * cfg.HeartbeatInterval
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = 5 * time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &Server{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: handshakeTimeout,
			ReadBufferSize:   32 * 1024,
			WriteBufferSize:  32 * 1024,
			Subprotocols:     []string{Subprotocol},
			// Requests that carry an Origin are refused before the upgrade.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		workers: make(map[string]*Worker),
		drained: make(map[string]bool),
		fails:   make(map[string]*authFailures),
	}, nil
}

// Worker is one registered worker connection.
type Worker struct {
	id     string
	connID string
	info   RegisterRequest
	conn   ssh.Conn

	used     int64 // atomic
	active   int64 // atomic
	lastSeen int64 // atomic, unix nanoseconds
	draining int32 // atomic
	offline  int32 // atomic
	syncing  int32 // atomic: registered but its homes are not reconciled yet
	syncFrom int64 // atomic, unix nanoseconds: when syncing began
	replaced bool  // guarded by Server.mu
}

func (w *Worker) ID() string            { return w.id }
func (w *Worker) ConnectionID() string  { return w.connID }
func (w *Worker) Info() RegisterRequest { return w.info }
func (w *Worker) Used() int64           { return atomic.LoadInt64(&w.used) }
func (w *Worker) Active() int64         { return atomic.LoadInt64(&w.active) }
func (w *Worker) Online() bool          { return atomic.LoadInt32(&w.offline) == 0 }
func (w *Worker) Draining() bool        { return atomic.LoadInt32(&w.draining) == 1 }

// Syncing reports whether the worker is still reconciling its homes with the
// gateway. A syncing worker takes no new sessions.
func (w *Worker) Syncing() bool      { return atomic.LoadInt32(&w.syncing) == 1 }
func (w *Worker) RemoteAddr() string { return w.conn.RemoteAddr().String() }

// Disconnect closes the worker's connection. The worker reconnects by itself,
// which starts everything that depends on the connection afresh.
func (w *Worker) Disconnect() { w.conn.Close() }

// LastSeen is when the worker last sent anything.
func (w *Worker) LastSeen() time.Time {
	return time.Unix(0, atomic.LoadInt64(&w.lastSeen))
}

func (w *Worker) setDraining(on bool) {
	v := int32(0)
	if on {
		v = 1
	}
	atomic.StoreInt32(&w.draining, v)
}

func (w *Worker) touch() { atomic.StoreInt64(&w.lastSeen, time.Now().UnixNano()) }

// Dial opens a new stream to the worker. The worker serves HTTP on it.
func (w *Worker) Dial(ctx context.Context) (net.Conn, error) {
	return w.dialChannel(ctx, ChannelHTTP, "gateway")
}

func (w *Worker) dialChannel(ctx context.Context, channel, local string) (net.Conn, error) {
	if !w.Online() {
		return nil, errors.New("tunnel: worker " + w.id + " is offline")
	}
	type result struct {
		ch  ssh.Channel
		err error
	}
	done := make(chan result, 1)
	go func() {
		ch, reqs, err := w.conn.OpenChannel(channel, nil)
		if err == nil {
			go ssh.DiscardRequests(reqs)
		}
		done <- result{ch, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return newStreamConn(r.ch, local, w.id), nil
	case <-ctx.Done():
		go func() {
			if r := <-done; r.err == nil {
				r.ch.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// DialSync opens the workspace synchronization channel to the worker.
func (w *Worker) DialSync(ctx context.Context) (net.Conn, error) {
	return w.dialChannel(ctx, ChannelSync, "sync")
}

// Workers returns the registered workers ordered by id.
func (s *Server) Workers() []*Worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Worker returns the registered worker with the given id, or nil.
func (s *Server) Worker(id string) *Worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workers[id]
}

// SetDraining stops (or resumes) new sessions being placed on a registered
// worker; its existing sessions carry on. The setting belongs to the worker
// id, so it survives the worker reconnecting. It reports whether a worker
// with that id is registered.
func (s *Server) SetDraining(id string, on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workers[id]
	if w == nil {
		return false
	}
	if on {
		s.drained[id] = true
	} else {
		delete(s.drained, id)
	}
	w.setDraining(on)
	return true
}

// Close disconnects every worker.
func (s *Server) Close() {
	for _, w := range s.Workers() {
		w.conn.Close()
	}
}

func (s *Server) tokenOK(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) == 1
}

func clientIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func (s *Server) blocked(ip string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[ip]
	if f == nil {
		return false
	}
	if time.Now().After(f.reset) {
		delete(s.fails, ip)
		return false
	}
	return f.count >= maxAuthFailures
}

func (s *Server) recordFailure(ip string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	now := time.Now()
	f := s.fails[ip]
	if f == nil || now.After(f.reset) {
		// Forget addresses whose window has passed so the map stays small.
		for k, v := range s.fails {
			if now.After(v.reset) {
				delete(s.fails, k)
			}
		}
		f = &authFailures{reset: now.Add(authFailureWindow)}
		s.fails[ip] = f
	}
	f.count++
}

// ServeHTTP is the WebSocket tunnel endpoint (by default /api/tunnel). It is
// for workers only: browsers always send an Origin, and cannot send the
// Authorization header, so both are checked before the upgrade.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r.RemoteAddr)
	if s.blocked(ip) {
		http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	auth := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") || !s.tokenOK(auth[1]) {
		s.recordFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has already replied
	}
	s.ServeConn(newWSConn(ws))
}

// Serve accepts raw TCP worker connections (the ssh:// transport).
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		if s.blocked(clientIP(c.RemoteAddr().String())) {
			c.Close()
			continue
		}
		go s.ServeConn(c)
	}
}

// ServeConn runs the SSH server side of one worker connection until it ends.
func (s *Server) ServeConn(c net.Conn) {
	defer c.Close()
	ip := clientIP(c.RemoteAddr().String())

	sshCfg := &ssh.ServerConfig{
		MaxAuthTries: 3,
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if !s.tokenOK(string(password)) {
				s.recordFailure(ip)
				return nil, errors.New("invalid worker token")
			}
			return nil, nil
		},
	}
	sshCfg.AddHostKey(s.cfg.HostKey)

	// Bound the handshake and the wait for registration.
	guard := time.AfterFunc(handshakeTimeout, func() { c.Close() })
	conn, chans, reqs, err := ssh.NewServerConn(c, sshCfg)
	if err != nil {
		guard.Stop()
		s.cfg.Logf("tunnel: handshake with %s failed: %v", ip, err)
		return
	}
	defer conn.Close()

	go func() {
		for nc := range chans {
			nc.Reject(ssh.Prohibited, "workers cannot open channels")
		}
	}()

	var worker *Worker
	defer func() {
		if worker != nil {
			s.unregister(worker)
		}
	}()

	for req := range reqs {
		switch req.Type {
		case ReqRegister:
			if worker != nil {
				reply(req, false, "already registered")
				continue
			}
			w, rep, err := s.register(conn, req.Payload)
			if err == nil && w.id != conn.User() {
				// The id must match the SSH user the connection logged in as.
				s.unregister(w)
				err = fmt.Errorf("worker id %q does not match the connection user %q", w.id, conn.User())
			}
			if err != nil {
				reply(req, false, err.Error())
				s.cfg.Logf("tunnel: registration from %s refused: %v", ip, err)
				return
			}
			guard.Stop()
			worker = w
			data, _ := json.Marshal(rep)
			req.Reply(true, data)
			go s.watch(w)
		case ReqHeartbeat:
			if worker == nil {
				reply(req, false, "not registered")
				continue
			}
			var hb Heartbeat
			if json.Unmarshal(req.Payload, &hb) == nil {
				atomic.StoreInt64(&worker.used, hb.Used)
				atomic.StoreInt64(&worker.active, int64(hb.Active))
			}
			reply(req, true, "")
		case ReqSyncReady:
			if worker == nil {
				reply(req, false, "not registered")
				continue
			}
			if atomic.CompareAndSwapInt32(&worker.syncing, 1, 0) {
				s.cfg.Logf("tunnel: worker %s finished reconciling its homes", worker.id)
				if s.cfg.OnSyncReady != nil {
					s.cfg.OnSyncReady(worker)
				}
			}
			reply(req, true, "")
		case ReqRouteOpen, ReqRouteClose:
			if worker == nil {
				reply(req, false, "not registered")
				continue
			}
			var ev RouteEvent
			if err := json.Unmarshal(req.Payload, &ev); err != nil || ev.Kind == "" || ev.Key == "" {
				reply(req, false, "bad route event")
				continue
			}
			if s.cfg.OnRoute != nil {
				s.cfg.OnRoute(worker, ev, req.Type == ReqRouteOpen)
			}
			reply(req, true, "")
		default:
			reply(req, false, "")
		}
		if worker != nil {
			worker.touch()
		}
	}
}

func reply(req *ssh.Request, ok bool, msg string) {
	if req.WantReply {
		req.Reply(ok, []byte(msg))
	}
}

func (s *Server) register(conn ssh.Conn, payload []byte) (*Worker, RegisterReply, error) {
	var reg RegisterRequest
	if err := json.Unmarshal(payload, &reg); err != nil {
		return nil, RegisterReply{}, errors.New("malformed registration")
	}
	if reg.ProtocolVersion != ProtocolVersion {
		return nil, RegisterReply{}, fmt.Errorf("protocol version %d is not supported, the gateway speaks %d", reg.ProtocolVersion, ProtocolVersion)
	}
	if !workerIDPattern.MatchString(reg.WorkerID) || reg.WorkerID == "local" {
		return nil, RegisterReply{}, fmt.Errorf("invalid worker id %q", reg.WorkerID)
	}
	if reg.Weight <= 0 {
		reg.Weight = 1
	}

	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, RegisterReply{}, err
	}
	w := &Worker{
		id:     reg.WorkerID,
		connID: hex.EncodeToString(idBytes),
		info:   reg,
		conn:   conn,
	}
	w.touch()
	if s.cfg.WorkspaceSync {
		atomic.StoreInt32(&w.syncing, 1)
		atomic.StoreInt64(&w.syncFrom, time.Now().UnixNano())
	}

	s.cbMu.Lock()
	defer s.cbMu.Unlock()

	s.mu.Lock()
	w.setDraining(s.drained[w.id])
	old := s.workers[w.id]
	if old != nil {
		old.replaced = true
		atomic.StoreInt32(&old.offline, 1)
	}
	s.workers[w.id] = w
	s.mu.Unlock()

	if old != nil {
		// A worker that reconnects before its old connection timed out.
		s.cfg.Logf("tunnel: worker %s reconnected, dropping its previous connection", w.id)
		if s.cfg.OnOffline != nil {
			s.cfg.OnOffline(old)
		}
		old.conn.Close()
	}
	s.cfg.Logf("tunnel: worker %s registered from %s (capacity %d, weight %d)", w.id, w.RemoteAddr(), reg.Capacity, reg.Weight)
	if s.cfg.OnOnline != nil {
		s.cfg.OnOnline(w)
	}

	rep := RegisterReply{
		ConnectionID:    w.connID,
		HeartbeatMillis: int(s.cfg.HeartbeatInterval / time.Millisecond),
		WorkspaceSync:   s.cfg.WorkspaceSync,
	}
	if s.cfg.CookieSecret != nil {
		rep.CookieSecret = s.cfg.CookieSecret()
	}
	if s.cfg.AuthToken != nil {
		rep.AuthToken = s.cfg.AuthToken()
	}
	return w, rep, nil
}

func (s *Server) unregister(w *Worker) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()

	s.mu.Lock()
	replaced := w.replaced
	if s.workers[w.id] == w {
		delete(s.workers, w.id)
	}
	s.mu.Unlock()

	atomic.StoreInt32(&w.offline, 1)
	if replaced {
		return // OnOffline already ran when the new connection took over
	}
	s.cfg.Logf("tunnel: worker %s is offline", w.id)
	if s.cfg.OnOffline != nil {
		s.cfg.OnOffline(w)
	}
}

// watch closes the connection of a worker that stopped sending heartbeats.
func (s *Server) watch(w *Worker) {
	t := time.NewTicker(s.cfg.HeartbeatInterval)
	defer t.Stop()
	for range t.C {
		if !w.Online() {
			return
		}
		if time.Since(w.LastSeen()) > s.cfg.Timeout {
			s.cfg.Logf("tunnel: worker %s timed out", w.id)
			w.conn.Close()
			return
		}
		if w.Syncing() && time.Since(time.Unix(0, atomic.LoadInt64(&w.syncFrom))) > s.cfg.SyncTimeout {
			// It never finished; reconnecting gives it another try.
			s.cfg.Logf("tunnel: worker %s did not finish reconciling its homes in %v", w.id, s.cfg.SyncTimeout)
			w.conn.Close()
			return
		}
	}
}
