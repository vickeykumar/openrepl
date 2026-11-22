package egress

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDeniedRanges(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "fd00::1", "::ffff:10.0.0.1", "0.0.0.0", "224.0.0.1"} {
		if !Denied(net.ParseIP(s)) {
			t.Errorf("%s should be refused", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "151.101.1.1", "2606:4700::1111", "::ffff:8.8.8.8"} {
		if Denied(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !DeniedPort(25) || DeniedPort(443) {
		t.Error("only port 25 is refused")
	}
}

func TestModeIsOffByDefault(t *testing.T) {
	t.Setenv(EnvEgress, "")
	if Enabled() {
		t.Error("off by default")
	}
	t.Setenv(EnvEgress, "public")
	if !Enabled() {
		t.Error("public turns it on")
	}
	t.Setenv(EnvEgress, "nonsense")
	if Enabled() {
		t.Error("anything else is off")
	}
	// the admin's switch turns it on without the variable
	on := true
	SiteSwitch = func() bool { return on }
	defer func() { SiteSwitch = nil }()
	if !Enabled() {
		t.Error("the site switch turns it on")
	}
	on = false
	if Enabled() {
		t.Error("and off")
	}
}

// a name that resolves to a private address is refused: the check is on what the name resolves to
func TestResolvedPrivateAddressIsRefused(t *testing.T) {
	p := &Proxy{Lookup: func(ctx context.Context, h string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.5")}, nil }}
	if _, err := p.dial(context.Background(), "tcp", "evil.example:80"); err == nil {
		t.Error("a name that resolves to 10.0.0.5 must be refused")
	}
	if _, err := p.dial(context.Background(), "tcp", "169.254.169.254:80"); err == nil {
		t.Error("the metadata address must be refused")
	}
	if _, err := p.dial(context.Background(), "tcp", "8.8.8.8:25"); err == nil {
		t.Error("port 25 must be refused")
	}
	_, recent := Snapshot()
	if len(recent) == 0 {
		t.Error("refusals are kept for the admin page")
	}
}

// the proxy carries a plain http request to a public address (the test dials its own server instead of the internet)
func TestForwardsPlainHTTPAndTunnels(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello "+r.URL.Path) }))
	defer origin.Close()
	_, port, _ := net.SplitHostPort(origin.Listener.Addr().String())
	p := &Proxy{
		Lookup: func(ctx context.Context, h string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8")}, nil },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
		},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	pu, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	resp, err := client.Get("http://pypi.example/simple/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hello /simple/" {
		t.Errorf("got %q", b)
	}
	// a CONNECT to a private address is refused with 403
	req, _ := http.NewRequest("CONNECT", srv.URL, nil)
	req.Host = "10.0.0.1:443"
	r2, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		r2.Body.Close()
		if r2.StatusCode != http.StatusForbidden {
			t.Errorf("CONNECT to a private address gave %d", r2.StatusCode)
		}
	}
	// the limit of connections
	q := &Proxy{MaxConns: 1}
	q.conns = 1
	rec := httptest.NewRecorder()
	q.ServeHTTP(rec, httptest.NewRequest("GET", "http://x.example/", strings.NewReader("")))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("over the limit gave %d", rec.Code)
	}
}

// the proxy as the tools that ignore HTTPS_PROXY read it: Java system properties, and Perl asked to read the variables
func TestEnvAlsoReachesTheJavaAndPerlTools(t *testing.T) {
	env := strings.Join(Env(), "\n")
	for _, want := range []string{"HTTPS_PROXY=http://127.0.0.1:3128", "PERL_LWP_ENV_PROXY=1", "MAVEN_OPTS=-Dhttp.proxyHost=127.0.0.1 -Dhttp.proxyPort=3128 -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=3128", "GRADLE_OPTS=-Dhttp.proxyHost=127.0.0.1"} {
		if !strings.Contains(env, want) {
			t.Errorf("%q is not in the environment", want)
		}
	}
	if strings.Contains(env, "JAVA_TOOL_OPTIONS") || strings.Contains(env, "|") {
		t.Error("no JAVA_TOOL_OPTIONS (it prints on every start), and no | in a value a launcher script reads")
	}
}
