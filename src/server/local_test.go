package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A worker serves two kinds of visitors: the gateway, over the tunnel, and
// people who open the worker's own port. These tests pin down how they differ.

func forwarded(r *http.Request) *http.Request {
	return r.WithContext(trustTunnel(r.Context(), nil))
}

func basic(r *http.Request, credential string) *http.Request {
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credential)))
	return r
}

func TestCredentialForWorkerVisitors(t *testing.T) {
	s := &Server{options: &Options{Mode: ModeWorker, Credential: "own:pw"}}
	s.setCredential("token-from-gateway")

	direct := httptest.NewRequest("GET", "/ws", nil)
	if got := s.credentialFor(direct); got != "own:pw" {
		t.Errorf("visitor of the worker's own port must use the worker's credential, got %q", got)
	}
	if got := s.credentialFor(forwarded(direct)); got != "token-from-gateway" {
		t.Errorf("a forwarded request must use the gateway's token, got %q", got)
	}

	standalone := &Server{options: &Options{Mode: ModeStandalone, Credential: "a:b"}}
	if got := standalone.credentialFor(direct); got != "a:b" {
		t.Errorf("standalone credential = %q", got)
	}
}

func TestSiteAuthOnAWorker(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	do := func(s *Server, r *http.Request) int {
		w := httptest.NewRecorder()
		s.wrapSiteAuth(ok).ServeHTTP(w, r)
		return w.Code
	}
	req := func() *http.Request { return httptest.NewRequest("GET", "/", nil) }

	worker := &Server{options: &Options{Mode: ModeWorker, EnableBasicAuth: true, Credential: "u:p"}}
	if got := do(worker, req()); got != http.StatusUnauthorized {
		t.Errorf("direct visitor without credentials: %d, want 401", got)
	}
	if got := do(worker, basic(req(), "u:wrong")); got != http.StatusUnauthorized {
		t.Errorf("direct visitor with a wrong credential: %d, want 401", got)
	}
	if got := do(worker, basic(req(), "u:p")); got != http.StatusOK {
		t.Errorf("direct visitor with the credential: %d, want 200", got)
	}
	if got := do(worker, forwarded(req())); got != http.StatusOK {
		t.Errorf("a request from the gateway is not asked again: %d, want 200", got)
	}

	// Not a worker: everybody is asked, as before.
	single := &Server{options: &Options{Mode: ModeStandalone, EnableBasicAuth: true, Credential: "u:p"}}
	if got := do(single, req()); got != http.StatusUnauthorized {
		t.Errorf("standalone without credentials: %d, want 401", got)
	}
	if got := do(single, basic(req(), "u:p")); got != http.StatusOK {
		t.Errorf("standalone with the credential: %d, want 200", got)
	}

	// No credential configured: nobody is asked.
	open := &Server{options: &Options{Mode: ModeWorker}}
	if got := do(open, req()); got != http.StatusOK {
		t.Errorf("no credential configured: %d, want 200", got)
	}
}

func TestWorkerAnnouncesOnlyWhatItRunsForTheGateway(t *testing.T) {
	routes := &routeTracker{}
	worker := &Server{options: &Options{Mode: ModeWorker}, routes: routes}
	direct := httptest.NewRequest("GET", "/", nil)

	if worker.announce(direct) != nil {
		t.Error("a session opened on the worker's own port must not be announced to the gateway")
	}
	if worker.announce(forwarded(direct)) != routes {
		t.Error("a session the gateway forwarded must be announced")
	}

	gateway := &Server{options: &Options{Mode: ModeGateway}, routes: routes}
	if gateway.announce(direct) != routes {
		t.Error("the gateway announces its own sessions")
	}

	// Without a tracker the call is harmless, as in standalone mode.
	(&Server{options: &Options{Mode: ModeStandalone}}).announce(direct).home("/tmp/home/x")
}

func TestNoSyncWaitForVisitorsOfTheWorkersOwnPort(t *testing.T) {
	s := &Server{options: &Options{Mode: ModeWorker, WorkerID: "w1"}}
	s.workerSync.enabled = 1 // the gateway keeps copies of the homes it forwards

	direct := httptest.NewRequest("GET", "/ws", nil)
	if err := s.waitWorkspace(direct, "/tmp/home/guest-1234"); err != nil {
		t.Fatalf("a direct visitor must not wait for the gateway: %v", err)
	}
	if s.workerSync.mgr != nil {
		t.Error("a direct visitor must not start workspace sync")
	}
}

func TestKeepHomeOnlyWhatTheGatewayHasSynchronized(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "gateway"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "gateway", "guest-synced.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("/tmp/home", 0755); err != nil {
		t.Skip("cannot create /tmp/home: ", err)
	}

	s := &Server{options: &Options{Mode: ModeWorker, WorkerID: "w1", SyncStateDir: state}}
	defer func() {
		if s.workerSync.mgr != nil {
			s.workerSync.mgr.Close()
		}
	}()

	cases := []struct {
		dir  string
		keep bool
	}{
		{"/tmp/home/guest-synced", true},   // the gateway has a copy and expires it
		{"/tmp/home/guest-synced/", true},  // a trailing slash is the same home
		{"/tmp/home/guest-local", false},   // made for a visitor of the worker's own port
		{"/tmp/other/guest-synced", false}, // not a home at all
		{"/tmp/home", false},               // the base itself is not a home
	}
	for _, c := range cases {
		if got := s.keepHome(c.dir); got != c.keep {
			t.Errorf("keepHome(%q) = %v, want %v", c.dir, got, c.keep)
		}
	}
}
