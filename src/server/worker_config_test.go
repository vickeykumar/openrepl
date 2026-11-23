package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"egress"
	"tunnel"
	"webtty"
)

func TestTheWorkerConfigHasARevisionThatFollowsTheContent(t *testing.T) {
	adminTestServer(t)
	s := &Server{}
	first := s.gatewayWorkerConfig()
	if first.Revision == 0 {
		t.Fatal("the revision is 0, which means no config")
	}
	if again := s.gatewayWorkerConfig(); again.Revision != first.Revision {
		t.Fatal("the same rules got another revision")
	}
	// what a worker does not follow does not change it
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{HistoryMessages: 10}, Admins: []string{"a@b.co"}})
	if got := s.gatewayWorkerConfig(); got.Revision != first.Revision {
		t.Fatal("a Genie setting or an admin changed the revision")
	}
	SaveSiteSettings(SiteSettings{Maintenance: Maintenance{Enabled: true, Message: "back at noon"}, DisabledLanguages: []string{"python"}})
	got := s.gatewayWorkerConfig()
	if got.Revision == first.Revision || !got.Maintenance || got.MaintenanceMessage != "back at noon" || len(got.DisabledLanguages) != 1 {
		t.Fatalf("after a change: %+v", got)
	}
}

func TestAWorkerPutsTheGatewaysRulesInPlace(t *testing.T) {
	adminTestServer(t)
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 3, ColorOfTheDay: true, AnnouncementText: "hello", Maintenance: true,
		MaintenanceMessage: "down", DisabledLanguages: []string{"cpp"}})
	got := GetSiteSettings()
	if !got.ColorOfTheDay || got.Announcement.Text != "hello" || got.Announcement.Level != "info" || !got.Maintenance.Enabled || !got.LanguageDisabled("cpp") {
		t.Fatalf("settings: %+v", got)
	}
	// the page of the worker shows them too
	w := httptest.NewRecorder()
	handleSettingsJS(w, httptest.NewRequest("GET", "/settings.js", nil))
	if !strings.Contains(w.Body.String(), `"text":"hello"`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("settings.js: %s", w.Body.String())
	}
	// and a later config replaces them entirely
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 4})
	if got := GetSiteSettings(); got.Maintenance.Enabled || got.Announcement.Text != "" || len(got.DisabledLanguages) != 0 {
		t.Fatalf("after an empty config: %+v", got)
	}
}

// workerControls is a worker's handler with the controls in front, behind an
// http server whose connections are trusted or not.
func workerControls(t *testing.T, trusted bool) (wsURL string) {
	t.Helper()
	s := &Server{
		upgrader:  &websocket.Upgrader{Subprotocols: webtty.Protocols},
		terminals: map[string]string{"ws_python": "python", "ws_cpp": "cpp"},
	}
	s.options = &Options{Mode: ModeWorker}
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the terminal itself: accept and say so
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.WriteMessage(websocket.TextMessage, []byte("terminal reached"))
		conn.Close()
	})
	ts := httptest.NewUnstartedServer(s.wrapWorkerControls(reached, "/"))
	if trusted {
		ts.Config.ConnContext = trustTunnel // what the tunnel's listener does for the gateway's requests
	}
	ts.Start()
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}

func dialTerminal(t *testing.T, url string) (reached bool, closeText string) {
	t.Helper()
	d := websocket.Dialer{Subprotocols: webtty.Protocols}
	c, _, err := d.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.WriteMessage(websocket.TextMessage, []byte(`{"Arguments":"","AuthToken":"","Payload":{}}`))
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, m, err := c.ReadMessage()
	if err == nil && len(m) > 0 && m[0] == webtty.End {
		_, m, err = c.ReadMessage() // the end notice comes before the close frame
	}
	if ce, ok := err.(*websocket.CloseError); ok {
		return false, ce.Text
	}
	return string(m) == "terminal reached", ""
}

func TestAWorkerRefusesItsOwnVisitorsDuringMaintenanceAndForSwitchedOffLanguages(t *testing.T) {
	adminTestServer(t)
	url := workerControls(t, false)

	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 1})
	if ok, text := dialTerminal(t, url+"/ws_python"); !ok {
		t.Fatalf("with no rules: reached=%v %q", ok, text)
	}

	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 2, DisabledLanguages: []string{"python"}})
	if ok, text := dialTerminal(t, url+"/ws_python"); ok || text != "site notice: This language is switched off for now. Please pick another one." {
		t.Fatalf("a language that is off: reached=%v %q", ok, text)
	}
	if ok, _ := dialTerminal(t, url+"/ws_cpp"); !ok {
		t.Fatal("another language was refused")
	}

	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 3, Maintenance: true, MaintenanceMessage: "Back at noon"})
	if ok, text := dialTerminal(t, url+"/ws_cpp"); ok || text != "site notice: Back at noon" {
		t.Fatalf("maintenance: reached=%v %q", ok, text)
	}
}

func TestWhatTheGatewayForwardsIsNotCheckedAgainOnTheWorker(t *testing.T) {
	adminTestServer(t)
	url := workerControls(t, true)
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 5, Maintenance: true, DisabledLanguages: []string{"python"}})
	// the gateway decided, knowing who is an admin; the worker does not ask again
	if ok, text := dialTerminal(t, url+"/ws_python"); !ok {
		t.Fatalf("a forwarded terminal was refused by the worker: %q", text)
	}
}

// the admin's switch for the internet of signed-in users' REPLs holds on every node: the gateway hands it to the workers with the site rules,
// and a worker's proxy follows it
func TestTheInternetSwitchReachesTheWorkers(t *testing.T) {
	adminTestServer(t)
	t.Setenv("OPENREPL_EGRESS", "")
	s := &Server{}
	before := s.gatewayWorkerConfig()
	if before.Egress {
		t.Fatal("off by default")
	}
	SaveSiteSettings(SiteSettings{EgressOn: true})
	got := s.gatewayWorkerConfig()
	if !got.Egress || got.Revision == before.Revision {
		t.Fatalf("the switch must be in the config and change its revision: %+v", got)
	}
	// the worker puts it in place, and its proxy is on
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 9, Egress: true})
	if !GetSiteSettings().EgressOn || !egress.Enabled() {
		t.Fatal("a worker did not follow the gateway's switch")
	}
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 10})
	if egress.Enabled() {
		t.Fatal("a worker kept the switch after the gateway turned it off")
	}
}

// the preview host reaches the workers with the other site rules, and changes the revision a worker compares
func TestThePreviewBaseURLReachesTheWorkers(t *testing.T) {
	adminTestServer(t)
	s := &Server{}
	before := s.gatewayWorkerConfig()
	SaveSiteSettings(SiteSettings{PreviewBaseURL: "https://preview.example.test"})
	got := s.gatewayWorkerConfig()
	if got.PreviewBaseURL != "https://preview.example.test" || got.Revision == before.Revision {
		t.Fatalf("the base URL must be in the config and change its revision: %+v", got)
	}
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 11, PreviewBaseURL: "https://preview.example.test"})
	if GetSiteSettings().previewBaseURL() != "https://preview.example.test" {
		t.Fatal("a worker did not follow the gateway's preview host")
	}
	applyWorkerConfig(&tunnel.WorkerConfig{Revision: 12})
	if GetSiteSettings().previewBaseURL() != "" {
		t.Fatal("a worker kept the preview host after the gateway cleared it")
	}
}
