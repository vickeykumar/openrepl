package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Stats is what the proxies of this server did since it started (the admin Health page shows it).
type Stats struct {
	Active   int64
	Total    int64
	Denied   int64
	BytesIn  int64 // from the internet to the REPLs
	BytesOut int64 // from the REPLs to the internet
}

var (
	active, total, denied, bytesIn, bytesOut int64
	recentMu                                 sync.Mutex
	recent                                   []string
)

// Snapshot is the current Stats and the latest refusals ("host: reason").
func Snapshot() (Stats, []string) {
	recentMu.Lock()
	r := append([]string(nil), recent...)
	recentMu.Unlock()
	return Stats{atomic.LoadInt64(&active), atomic.LoadInt64(&total), atomic.LoadInt64(&denied), atomic.LoadInt64(&bytesIn), atomic.LoadInt64(&bytesOut)}, r
}

func deny(host, why string) error {
	atomic.AddInt64(&denied, 1)
	recentMu.Lock()
	recent = append(recent, fmt.Sprintf("%s: %s", host, why))
	if len(recent) > 20 {
		recent = recent[len(recent)-20:]
	}
	recentMu.Unlock()
	return errors.New(why)
}

// Proxy is the forward proxy of one workspace (or one sandbox).
type Proxy struct {
	MaxConns int           // simultaneous connections (0: 64)
	MaxBytes int64         // bytes in both directions in all, 0: no limit
	Timeout  time.Duration // to connect (0: 20 s)
	// Lookup resolves a name; the default is the system's resolver.
	Lookup func(ctx context.Context, host string) ([]net.IP, error)
	// Dial opens the connection; the default is a net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	conns int64
	used  int64
}

func (p *Proxy) maxConns() int64 {
	if p.MaxConns > 0 {
		return int64(p.MaxConns)
	}
	return 64
}

func (p *Proxy) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 20 * time.Second
}

// dial resolves addr (host:port), refuses what is not public and connects to the first public address that answers.
func (p *Proxy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, deny(addr, "bad address")
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return nil, deny(addr, "bad port")
	}
	if DeniedPort(port) {
		return nil, deny(addr, "port refused")
	}
	var ips []net.IP
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		ips = []net.IP{ip}
	} else {
		lookup := p.Lookup
		if lookup == nil {
			lookup = func(ctx context.Context, h string) ([]net.IP, error) {
				a, err := net.DefaultResolver.LookupIPAddr(ctx, h)
				var out []net.IP
				for _, x := range a {
					out = append(out, x.IP)
				}
				return out, err
			}
		}
		if ips, err = lookup(ctx, host); err != nil {
			return nil, err
		}
	}
	var ok []net.IP
	for _, ip := range ips {
		if !Denied(ip) {
			ok = append(ok, ip)
		}
	}
	if len(ok) == 0 {
		return nil, deny(host, "not a public address")
	}
	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: p.timeout()}
		dial = d.DialContext
	}
	var last error
	for _, ip := range ok {
		c, err := dial(ctx, network, net.JoinHostPort(ip.String(), ps))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// ServeHTTP is the proxy: CONNECT for https, an absolute URL for plain http.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if atomic.AddInt64(&p.conns, 1) > p.maxConns() {
		atomic.AddInt64(&p.conns, -1)
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	atomic.AddInt64(&active, 1)
	atomic.AddInt64(&total, 1)
	defer func() { atomic.AddInt64(&p.conns, -1); atomic.AddInt64(&active, -1) }()
	if p.MaxBytes > 0 && atomic.LoadInt64(&p.used) > p.MaxBytes {
		http.Error(w, "the data limit of this workspace is reached", http.StatusTooManyRequests)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	p.forward(w, r)
}

func (p *Proxy) count(in bool, n int64) {
	atomic.AddInt64(&p.used, n)
	if in {
		atomic.AddInt64(&bytesIn, n)
	} else {
		atomic.AddInt64(&bytesOut, n)
	}
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout())
	up, err := p.dial(ctx, "tcp", r.Host)
	cancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	c, buf, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn, in bool, from io.Reader) {
		defer wg.Done()
		n, _ := io.Copy(dst, from)
		p.count(in, n)
		if t, ok := dst.(interface{ CloseWrite() error }); ok {
			t.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go pipe(up, c, false, buf)
	go pipe(c, up, true, up)
	wg.Wait()
	c.Close()
	up.Close()
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() || (r.URL.Scheme != "http") {
		http.Error(w, "this is a proxy: use an absolute http URL or CONNECT", http.StatusBadRequest)
		return
	}
	tr := &http.Transport{DialContext: p.dial, DisableKeepAlives: true, ResponseHeaderTimeout: 60 * time.Second}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, resp.Body)
	p.count(true, n)
	if r.ContentLength > 0 {
		p.count(false, r.ContentLength)
	}
}

// Serve answers on l until it is closed.
func (p *Proxy) Serve(l net.Listener) error {
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	return srv.Serve(l)
}
