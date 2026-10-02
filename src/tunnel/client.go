package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// routeAckTimeout bounds how long RouteOpen waits for the gateway.
const routeAckTimeout = 2 * time.Second

// ClientConfig configures the worker end of the tunnel.
type ClientConfig struct {
	// ServerURL is where the gateway listens: wss://host/api/tunnel,
	// ws://host/api/tunnel (no TLS, for tests) or ssh://host:port.
	ServerURL string
	// Token is the shared worker token.
	Token string
	// HostKey pins the gateway's SSH host key by SHA256 fingerprint. It is
	// required for ssh://. Over wss:// the TLS certificate already
	// authenticates the gateway, so it is optional.
	HostKey string
	// Register describes this worker. ProtocolVersion is filled in.
	Register RegisterRequest
	// Load is sampled for every heartbeat.
	Load func() Heartbeat
	// OnRegistered runs after each successful registration, before any
	// stream is served.
	OnRegistered func(RegisterReply)
	// TLSConfig is used for wss://. Optional.
	TLSConfig *tls.Config
	// MinBackoff and MaxBackoff bound the reconnect delay. Defaults: 1s, 30s.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// Logf defaults to log.Printf.
	Logf func(format string, args ...interface{})
}

// Client keeps a worker connected to its gateway.
type Client struct {
	cfg    ClientConfig
	scheme string
	target *url.URL
	ln     *streamListener

	mu     sync.Mutex
	conn   ssh.Conn
	routes map[RouteEvent]struct{}
}

// NewClient validates cfg and creates a Client. Call Run to connect.
func NewClient(cfg ClientConfig) (*Client, error) {
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("tunnel: invalid server url: %v", err)
	}
	switch u.Scheme {
	case "ws", "wss":
	case "ssh":
		if cfg.HostKey == "" {
			return nil, errors.New("tunnel: ssh:// needs the gateway host key fingerprint")
		}
	default:
		return nil, fmt.Errorf("tunnel: server url must start with wss://, ws:// or ssh://, got %q", cfg.ServerURL)
	}
	if u.Host == "" {
		return nil, errors.New("tunnel: server url has no host")
	}
	if cfg.Token == "" {
		return nil, errors.New("tunnel: a worker token is required")
	}
	if !workerIDPattern.MatchString(cfg.Register.WorkerID) || cfg.Register.WorkerID == "local" {
		return nil, fmt.Errorf("tunnel: invalid worker id %q", cfg.Register.WorkerID)
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	cfg.Register.ProtocolVersion = ProtocolVersion
	return &Client{
		cfg:    cfg,
		scheme: u.Scheme,
		target: u,
		ln:     newStreamListener(cfg.Register.WorkerID),
		routes: make(map[RouteEvent]struct{}),
	}, nil
}

// Listener yields the streams the gateway opens. Serve HTTP on it.
func (c *Client) Listener() net.Listener { return c.ln }

// Connected reports whether the worker is currently registered.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Run connects, registers and serves until ctx is cancelled, reconnecting
// with backoff whenever the connection is lost.
func (c *Client) Run(ctx context.Context) error {
	defer c.ln.Close()
	backoff := c.cfg.MinBackoff
	for {
		registered, err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if registered {
			backoff = c.cfg.MinBackoff
		}
		// Up to 25% jitter so a fleet does not reconnect in step.
		wait := backoff + time.Duration(rand.Int63n(int64(backoff)/4+1))
		c.cfg.Logf("tunnel: disconnected from gateway (%v), retrying in %v", err, wait.Round(time.Millisecond))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		if backoff *= 2; backoff > c.cfg.MaxBackoff {
			backoff = c.cfg.MaxBackoff
		}
	}
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	if c.scheme == "ssh" {
		d := net.Dialer{Timeout: handshakeTimeout}
		return d.DialContext(ctx, "tcp", c.target.Host)
	}
	d := websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
		Subprotocols:     []string{Subprotocol},
		TLSClientConfig:  c.cfg.TLSConfig,
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)
	ws, resp, err := d.Dial(c.target.String(), header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("%v (HTTP %d)", err, resp.StatusCode)
		}
		return nil, err
	}
	return newWSConn(ws), nil
}

func (c *Client) hostKeyCallback() ssh.HostKeyCallback {
	if c.cfg.HostKey == "" {
		// ws/wss only: the TLS certificate authenticates the gateway.
		return ssh.InsecureIgnoreHostKey()
	}
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if got := ssh.FingerprintSHA256(key); got != c.cfg.HostKey {
			return fmt.Errorf("gateway host key %s does not match the pinned key", got)
		}
		return nil
	}
}

// session runs one connection. It reports whether registration succeeded.
func (c *Client) session(ctx context.Context) (bool, error) {
	carrier, err := c.dial(ctx)
	if err != nil {
		return false, err
	}
	guard := time.AfterFunc(handshakeTimeout, func() { carrier.Close() })
	conn, chans, reqs, err := ssh.NewClientConn(carrier, c.target.Host, &ssh.ClientConfig{
		User:            c.cfg.Register.WorkerID,
		Auth:            []ssh.AuthMethod{ssh.Password(c.cfg.Token)},
		HostKeyCallback: c.hostKeyCallback(),
	})
	if err != nil {
		guard.Stop()
		carrier.Close()
		return false, err
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	payload, _ := json.Marshal(c.cfg.Register)
	ok, data, err := conn.SendRequest(ReqRegister, true, payload)
	guard.Stop()
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("registration refused: %s", data)
	}
	var rep RegisterReply
	if err := json.Unmarshal(data, &rep); err != nil {
		return false, fmt.Errorf("malformed registration reply: %v", err)
	}
	if c.cfg.OnRegistered != nil {
		c.cfg.OnRegistered(rep)
	}

	c.mu.Lock()
	c.conn = conn
	known := make([]RouteEvent, 0, len(c.routes))
	for ev := range c.routes {
		known = append(known, ev)
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()
	c.cfg.Logf("tunnel: registered with gateway as %s", c.cfg.Register.WorkerID)

	// The gateway forgets a worker's routes when it goes offline.
	for _, ev := range known {
		c.send(conn, ReqRouteOpen, ev)
	}

	stop := make(chan struct{})
	defer close(stop)
	go c.heartbeat(conn, time.Duration(rep.HeartbeatMillis)*time.Millisecond, stop)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()

	for nc := range chans {
		if nc.ChannelType() != ChannelHTTP {
			nc.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go ssh.DiscardRequests(chReqs)
		c.ln.deliver(newStreamConn(ch, c.cfg.Register.WorkerID, "gateway"))
	}
	return true, conn.Wait()
}

func (c *Client) heartbeat(conn ssh.Conn, every time.Duration, stop <-chan struct{}) {
	if every <= 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			var hb Heartbeat
			if c.cfg.Load != nil {
				hb = c.cfg.Load()
			}
			// A gateway that does not answer within three intervals is gone.
			if !c.sendWithin(conn, ReqHeartbeat, hb, 3*every) {
				conn.Close()
				return
			}
		case <-stop:
			return
		}
	}
}

func (c *Client) send(conn ssh.Conn, name string, v interface{}) bool {
	return c.sendWithin(conn, name, v, routeAckTimeout)
}

func (c *Client) sendWithin(conn ssh.Conn, name string, v interface{}, d time.Duration) bool {
	payload, _ := json.Marshal(v)
	done := make(chan bool, 1)
	go func() {
		ok, _, err := conn.SendRequest(name, true, payload)
		done <- ok && err == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(d):
		return false
	}
}

// RouteOpen tells the gateway that requests carrying this key belong to this
// worker. It waits briefly for the gateway to acknowledge, so the key is
// routable before the caller hands it to a client. Open routes are announced
// again after a reconnect.
func (c *Client) RouteOpen(kind, key string) {
	ev := RouteEvent{Kind: kind, Key: key}
	c.mu.Lock()
	_, known := c.routes[ev]
	c.routes[ev] = struct{}{}
	conn := c.conn
	c.mu.Unlock()
	if conn != nil && !known {
		c.send(conn, ReqRouteOpen, ev)
	}
}

// RouteClose withdraws a key announced with RouteOpen.
func (c *Client) RouteClose(kind, key string) {
	ev := RouteEvent{Kind: kind, Key: key}
	c.mu.Lock()
	_, known := c.routes[ev]
	delete(c.routes, ev)
	conn := c.conn
	c.mu.Unlock()
	if conn != nil && known {
		c.send(conn, ReqRouteClose, ev)
	}
}
