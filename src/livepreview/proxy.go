package livepreview

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Dialer opens a connection to a port of a workspace: through the network emulation's abstract socket, or into the
// workspace's own network namespace. The proxy reaches nothing else.
type Dialer func(ctx context.Context, port int) (net.Conn, error)

// ErrNoProgram says that nothing of the workspace is running, so there is no network to reach.
var ErrNoProgram = errors.New("nothing is running in this workspace")

// Jar keeps the cookies of the programs a workspace previews. The page in the frame has no origin and so no cookies of
// its own: the server keeps them for it, and adds them to the requests it passes on. A login of Flask-Login or an
// Express session then works. Only name and value are kept (a cookie that is set to end is dropped).
type Jar struct {
	mu    sync.Mutex
	m     map[string]map[string]string
	order []string
}

// NewJar makes an empty jar.
func NewJar() *Jar { return &Jar{m: map[string]map[string]string{}} }

const (
	jarKeys    = 256
	jarCookies = 50
)

// Header is the value of a Cookie header for key (a workspace and a port).
func (j *Jar) Header(key string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var parts []string
	for name, value := range j.m[key] {
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, "; ")
}

// Store takes the Set-Cookie header values of an answer.
func (j *Jar) Store(key string, setCookies []string) {
	if len(setCookies) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	cookies := j.m[key]
	if cookies == nil {
		if len(j.order) >= jarKeys {
			delete(j.m, j.order[0])
			j.order = j.order[1:]
		}
		cookies = map[string]string{}
		j.m[key] = cookies
		j.order = append(j.order, key)
	}
	for _, sc := range setCookies {
		parts := strings.Split(sc, ";")
		nv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
		if len(nv) != 2 || nv[0] == "" {
			continue
		}
		gone := false
		for _, attr := range parts[1:] {
			a := strings.ToLower(strings.TrimSpace(attr))
			if a == "max-age=0" || strings.HasPrefix(a, "max-age=-") {
				gone = true
			}
			if strings.HasPrefix(a, "expires=") {
				if t, err := http.ParseTime(strings.TrimSpace(attr)[len("expires="):]); err == nil && t.Before(time.Now()) {
					gone = true
				}
			}
		}
		if gone || nv[1] == "" {
			delete(cookies, nv[0])
		} else if _, had := cookies[nv[0]]; had || len(cookies) < jarCookies {
			cookies[nv[0]] = nv[1]
		}
	}
}

// Clear forgets every cookie of key.
func (j *Jar) Clear(key string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.m, key)
}

// Options says what a proxied request is.
type Options struct {
	Home   string                     // the workspace
	Port   int                        // the program's port
	Prefix string                     // the address of the preview, "/preview/<token>/p/<port>": links the program answers with go back through it
	Dial   Dialer                     // reaches the workspace's ports
	Jar    *Jar                       // its cookies
	Viewer string                     // whose cookies: the viewer of a shared session has his own ("" for none)
	Hook   func(*http.Response) error // for the page rewriting of phase 3, after the headers are dealt with
}

// MaxBody is the largest request body the proxy passes on.
const MaxBody = 10 << 20

// Proxy passes req, a request for the path rest of the program on o.Port, to that program and sends its answer back,
// with the sandbox headers and without anything the program sets that could undo them.
func Proxy(rw http.ResponseWriter, req *http.Request, rest string, o Options) {
	SandboxHeaders(rw.Header())
	if Preflight(rw, req) {
		return
	}
	key := fmt.Sprintf("%s|%s|%d", o.Home, o.Viewer, o.Port)
	target := fmt.Sprintf("127.0.0.1:%d", o.Port)
	req.Body = http.MaxBytesReader(rw, req.Body, MaxBody)
	rp := &httputil.ReverseProxy{
		FlushInterval: -1, // streamed answers (server-sent events) go through as they come
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = target
			r.URL.Path = "/" + strings.TrimPrefix(rest, "/")
			r.URL.RawPath = ""
			r.Host = target
			// the site's own cookies are not the program's; the jar has the program's
			r.Header.Del("Cookie")
			if c := o.Jar.Header(key); c != "" {
				r.Header.Set("Cookie", c)
			}
			// a page without an origin sends "Origin: null", which a program checking its own origin refuses
			if r.Header.Get("Origin") == "null" {
				r.Header.Del("Origin")
			}
			r.Header.Del("Referer")
			r.Header.Del("Authorization")
			// the answer is read uncompressed (the site compresses it again), so that it can be rewritten
			r.Header.Set("Accept-Encoding", "identity")
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return o.Dial(ctx, o.Port)
			},
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ModifyResponse: func(res *http.Response) error {
			o.Jar.Store(key, res.Header.Values("Set-Cookie"))
			for _, h := range []string{"Set-Cookie", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "X-Frame-Options", "Strict-Transport-Security", "Public-Key-Pins"} {
				res.Header.Del(h)
			}
			// a redirect to a path of the program stays in the preview
			if loc := res.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
				res.Header.Set("Location", o.Prefix+loc)
			} else if rest, ok := ownAddress(loc, o.Port); ok {
				res.Header.Set("Location", o.Prefix+rest)
			}
			// the headers of the sandbox win over anything the program says about them
			h := http.Header{}
			SandboxHeaders(h)
			for k := range h {
				res.Header.Set(k, h.Get(k))
			}
			if o.Hook != nil {
				return o.Hook(res)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			Refused(w, o.Port, err)
		},
	}
	if isWebSocket(req) {
		rw = keepAliveWriter{ResponseWriter: rw, every: PingEvery}
	}
	rp.ServeHTTP(rw, req)
}

// Refused answers a request whose program could not be reached: the page in the frame shows why.
func Refused(rw http.ResponseWriter, port int, err error) {
	SandboxHeaders(rw.Header())
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	code, title, body := http.StatusBadGateway, fmt.Sprintf("Nothing is listening on port %d", port), "Start your program, then reload."
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, ErrNoProgram):
		title, body = "Nothing is running yet", "Run your program in the terminal, then reload."
	case errors.Is(err, syscall.ECONNREFUSED):
	case errors.As(err, &maxErr):
		code, title, body = http.StatusRequestEntityTooLarge, "The request is too large", "A preview passes on a request of up to 10 MB."
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		code, title, body = http.StatusGatewayTimeout, "The program did not answer in time", "It took longer than 30 seconds."
	case errors.Is(err, context.Canceled):
		return
	default:
		title, body = "The program could not be reached", err.Error()
	}
	rw.WriteHeader(code)
	fmt.Fprintf(rw, `<!doctype html><meta charset="utf-8"><title>%s</title><body style="font:16px system-ui;margin:3rem;color:#333"><h1 style="font-size:20px">%s</h1><p>%s</p>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
