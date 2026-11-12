package livepreview

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/gorilla/websocket"
)

// an "app" that a workspace runs, and a dialer that reaches it
func app(t *testing.T) (port int, dial Dialer) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		fmt.Fprintf(w, "path=%s host=%s origin=%q cookie=%q", r.URL.Path, r.Host, r.Header.Get("Origin"), r.Header.Get("Cookie"))
	})
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc123", HttpOnly: true, Path: "/"})
		fmt.Fprint(w, "set")
	})
	mux.HandleFunc("/unset", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", MaxAge: -1, Path: "/"})
		fmt.Fprint(w, "unset")
	})
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", http.StatusFound) })
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %d", r.Method, len(b))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return dialServer(srv)
}

// the port of a test server, and a dialer that reaches it whatever port it is asked for
func dialServer(srv *httptest.Server) (int, Dialer) {
	addr := srv.Listener.Addr().(*net.TCPAddr)
	return addr.Port, func(ctx context.Context, p int) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", addr.Port))
	}
}

func run(o Options, method, rest string, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/preview/tok/p/1/"+rest, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	Proxy(rec, req, rest, o)
	return rec
}

func TestAProgramAnswersThroughTheProxyWithTheSandboxHeaders(t *testing.T) {
	port, dial := app(t)
	o := Options{Home: "/h/a", Port: port, Prefix: "/preview/tok/p/1", Dial: dial, Jar: NewJar()}
	rec := run(o, "GET", "some/page", "", "Origin", "null", "Cookie", "openrepl=SECRET", "Authorization", "Bearer X")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "path=/some/page") || !strings.Contains(b, fmt.Sprintf("host=127.0.0.1:%d", port)) {
		t.Fatalf("%d %q", rec.Code, b)
	}
	if strings.Contains(b, "SECRET") || !strings.Contains(b, `origin=""`) {
		t.Errorf("the site's cookie reached the program, or the null origin did: %q", b)
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox ") || h.Get("X-Frame-Options") != "" {
		t.Errorf("the program's headers undid the sandbox: %v", h)
	}
}

func TestTheProxyKeepsTheCookiesOfTheProgramForThePage(t *testing.T) {
	port, dial := app(t)
	o := Options{Home: "/h/a", Port: port, Prefix: "/preview/tok/p/1", Dial: dial, Jar: NewJar()}
	set := run(o, "GET", "set", "")
	if set.Header().Get("Set-Cookie") != "" {
		t.Error("the page got a cookie it could not keep")
	}
	if got := run(o, "GET", "me", "").Body.String(); !strings.Contains(got, `cookie="sid=abc123"`) {
		t.Errorf("the cookie was not sent back: %q", got)
	}
	// another workspace, or another port, has its own jar entry
	other := o
	other.Home = "/h/b"
	if got := run(other, "GET", "me", "").Body.String(); strings.Contains(got, "abc123") {
		t.Errorf("a cookie of one workspace reached another: %q", got)
	}
	run(o, "GET", "unset", "")
	if got := run(o, "GET", "me", "").Body.String(); strings.Contains(got, "abc123") {
		t.Errorf("a cookie that was ended is still sent: %q", got)
	}
}

func TestARedirectStaysInThePreview(t *testing.T) {
	port, dial := app(t)
	o := Options{Home: "/h/a", Port: port, Prefix: "/preview/tok/p/1", Dial: dial, Jar: NewJar()}
	rec := run(o, "GET", "redir", "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/preview/tok/p/1/login" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestARequestBodyAndMethodAreMeantAndTooMuchIsRefused(t *testing.T) {
	port, dial := app(t)
	o := Options{Home: "/h/a", Port: port, Prefix: "/preview/tok/p/1", Dial: dial, Jar: NewJar()}
	if got := run(o, "POST", "body", "twelve bytes").Body.String(); got != "POST 12" {
		t.Errorf("%q", got)
	}
	rec := run(o, "POST", "body", strings.Repeat("x", MaxBody+1))
	if rec.Code == 200 {
		t.Errorf("a body over the limit was passed on: %d", rec.Code)
	}
}

func TestNothingListeningOrRunningIsSaidInWords(t *testing.T) {
	refused := func(ctx context.Context, p int) (net.Conn, error) { return nil, syscall.ECONNREFUSED }
	rec := run(Options{Home: "/h", Port: 5000, Prefix: "/p", Dial: refused, Jar: NewJar()}, "GET", "", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Nothing is listening on port 5000") || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox ") {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	none := func(ctx context.Context, p int) (net.Conn, error) { return nil, ErrNoProgram }
	rec = run(Options{Home: "/h", Port: 5000, Prefix: "/p", Dial: none, Jar: NewJar()}, "GET", "", "")
	if !strings.Contains(rec.Body.String(), "Nothing is running yet") {
		t.Errorf("%q", rec.Body.String())
	}
}

func TestACookieJarIsBounded(t *testing.T) {
	j := NewJar()
	for i := 0; i < jarKeys+20; i++ {
		j.Store(fmt.Sprintf("k%d", i), []string{"a=1"})
	}
	if len(j.m) > jarKeys {
		t.Errorf("%d workspaces kept", len(j.m))
	}
	for i := 0; i < jarCookies+20; i++ {
		j.Store("one", []string{fmt.Sprintf("c%d=v", i)})
	}
	if n := len(j.m["one"]); n > jarCookies {
		t.Errorf("%d cookies kept for one program", n)
	}
	j.Clear("one")
	if j.Header("one") != "" {
		t.Error("cleared cookies are still there")
	}
}

func TestAWebSocketOfTheProgramGoesThroughTheProxy(t *testing.T) {
	up := websocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			c.WriteMessage(mt, append([]byte("echo: "), msg...))
		}
	}))
	defer backend.Close()
	port, dial := dialServer(backend)
	o := Options{Home: "/h", Port: port, Prefix: "/preview/tok/p/1", Dial: dial, Jar: NewJar(), Hook: HTMLHook("/preview/tok/p/1")}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Proxy(w, r, "socket", o)
	}))
	defer front.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/anything", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, word := range []string{"one", "two"} {
		if err := c.WriteMessage(websocket.TextMessage, []byte(word)); err != nil {
			t.Fatal(err)
		}
		_, msg, err := c.ReadMessage()
		if err != nil || string(msg) != "echo: "+word {
			t.Fatalf("%q %v", msg, err)
		}
	}
}

func TestAStreamedAnswerArrivesAsItIsWritten(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: second\n\n")
	}))
	defer backend.Close()
	port, dial := dialServer(backend)
	o := Options{Home: "/h", Port: port, Prefix: "/p", Dial: dial, Jar: NewJar(), Hook: HTMLHook("/p")}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { Proxy(w, r, "events", o) }))
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf) // the first event comes before the program has finished
	if !strings.Contains(string(buf[:n]), "first") {
		t.Errorf("the first event did not arrive on its own: %q", buf[:n])
	}
	close(release)
}
