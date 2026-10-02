package gateway

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync/atomic"
	"time"

	"trusted"
)

type identityKey struct{}

// withIdentity attaches the identity a remote backend should forward.
func withIdentity(r *http.Request, id trusted.Identity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
}

type weightKey struct{}

// withSessionWeight records the memory weight of the REPL a request starts.
func withSessionWeight(r *http.Request, weight int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), weightKey{}, weight))
}

// RemoteConfig describes a worker reached through a tunnel.
type RemoteConfig struct {
	ID string
	// Dial opens a new stream to the worker.
	Dial func(ctx context.Context) (net.Conn, error)
	// State reports the worker's availability.
	State func() State
	// StripPrefix is the gateway's URL prefix. The worker mounts its routes
	// at "/", so the prefix is removed from forwarded paths.
	StripPrefix string
	// Weight is the worker's share of new sessions.
	Weight int
	// Capacity returns the load the worker last reported and its budget.
	Capacity func() (used, max int64)
	// Languages lists the REPL commands the worker can run; empty means all.
	Languages []string
}

// RemoteBackend forwards requests to a worker. Method, path, query, headers,
// cookies and body go through unchanged, and WebSocket upgrades are bridged
// in both directions. It adds the trusted identity headers and never lets a
// worker set a browser cookie.
type RemoteBackend struct {
	cfg       RemoteConfig
	proxy     *httputil.ReverseProxy
	transport *http.Transport
	// tracked is the weight of the terminals open through this gateway. It
	// is exact and immediate, where the worker's own report lags by up to a
	// heartbeat; without it a burst of new sessions would all see the same
	// stale free capacity.
	tracked int64
}

func NewRemoteBackend(cfg RemoteConfig) *RemoteBackend {
	b := &RemoteBackend{cfg: cfg}
	b.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return cfg.Dial(ctx)
		},
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     60 * time.Second,
		// Pass the worker's encoding through as is.
		DisableCompression: true,
	}
	b.proxy = &httputil.ReverseProxy{
		Director:      b.direct,
		Transport:     b.transport,
		FlushInterval: -1, // stream downloads and long responses
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("gateway: worker %s: %s %s: %v", cfg.ID, r.Method, r.URL.Path, err)
			http.Error(w, "execution node unavailable", http.StatusServiceUnavailable)
		},
	}
	return b
}

func (b *RemoteBackend) direct(req *http.Request) {
	req.URL.Scheme = "http"
	req.URL.Host = b.cfg.ID // names the connection pool; Dial ignores it
	if p := b.cfg.StripPrefix; p != "" && p != "/" {
		req.URL.Path = "/" + strings.TrimPrefix(req.URL.Path, p)
		req.URL.RawPath = ""
	}
	// Only the gateway may state who the user is.
	trusted.Strip(req.Header)
	if id, ok := req.Context().Value(identityKey{}).(trusted.Identity); ok {
		trusted.Set(req.Header, id)
	}
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header.Set("User-Agent", "") // do not add Go's default
	}
}

func (b *RemoteBackend) ID() string { return b.cfg.ID }

func (b *RemoteBackend) State() State {
	if b.cfg.State == nil {
		return Online
	}
	return b.cfg.State()
}

func (b *RemoteBackend) Weight() int { return b.cfg.Weight }

// Capacity returns the larger of the worker's reported load and the load
// this gateway has forwarded to it.
func (b *RemoteBackend) Capacity() (used, max int64) {
	if b.cfg.Capacity != nil {
		used, max = b.cfg.Capacity()
	}
	if t := atomic.LoadInt64(&b.tracked); t > used {
		used = t
	}
	return used, max
}

func (b *RemoteBackend) HasLanguage(command string) bool {
	if command == "" || len(b.cfg.Languages) == 0 {
		return true
	}
	for _, l := range b.cfg.Languages {
		if l == command {
			return true
		}
	}
	return false
}

func (b *RemoteBackend) Serve(w http.ResponseWriter, r *http.Request) error {
	if weight, _ := r.Context().Value(weightKey{}).(int64); weight > 0 {
		// A terminal holds its memory for as long as its WebSocket is open.
		atomic.AddInt64(&b.tracked, weight)
		defer atomic.AddInt64(&b.tracked, -weight)
	}
	b.proxy.ServeHTTP(w, r)
	return nil
}

// Close releases the idle streams kept for reuse.
func (b *RemoteBackend) Close() {
	b.transport.CloseIdleConnections()
}
