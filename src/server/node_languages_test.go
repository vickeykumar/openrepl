package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway"
)

// ---- the stored choices ------------------------------------------------------------

func TestNodeChoicesAreCleanedAndRefuseNonsense(t *testing.T) {
	got, err := normalizeNodeLanguages(map[string]NodeLanguages{
		"pi-1":   {Off: []string{" Rappel ", "cpp", "rappel", ""}, On: []string{"go"}},
		"local":  {Off: []string{"python"}},
		"worker": {}, // nothing chosen: not kept
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || strings.Join(got["pi-1"].Off, ",") != "cpp,rappel" || strings.Join(got["pi-1"].On, ",") != "go" {
		t.Fatalf("cleaned: %+v", got)
	}
	if got, err := normalizeNodeLanguages(map[string]NodeLanguages{"a": {}}); err != nil || got != nil {
		t.Fatalf("an empty entry was kept: %+v %v", got, err)
	}
	for name, in := range map[string]map[string]NodeLanguages{
		"a bad node id":          {"../x": {Off: []string{"go"}}},
		"a bad language":         {"pi-1": {Off: []string{"Go Lang!"}}},
		"on and off together":    {"pi-1": {Off: []string{"go"}, On: []string{"go"}}},
		"a node id with a slash": {"a/b": {Off: []string{"go"}}},
	} {
		if _, err := normalizeNodeLanguages(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	tooMany := map[string]NodeLanguages{}
	for i := 0; i <= maxNodeLanguageNodes; i++ {
		tooMany["w"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = NodeLanguages{Off: []string{"go"}}
	}
	if _, err := normalizeNodeLanguages(tooMany); err == nil {
		t.Error("more nodes than the limit were accepted")
	}
}

func TestARuleIsSetWithoutChangingTheSettingsItCameFrom(t *testing.T) {
	var s SiteSettings
	s1 := s.withNodeRule("pi-1", "rappel", gateway.LanguageOff)
	s2 := s1.withNodeRule("pi-1", "go", gateway.LanguageOn)
	s3 := s2.withNodeRule("pi-1", "rappel", gateway.LanguageOn) // off -> on moves it
	s4 := s3.withNodeRule("pi-1", "go", gateway.LanguageDefault)
	if s.NodeRule("pi-1", "rappel") != gateway.LanguageDefault || s1.NodeRule("pi-1", "go") != gateway.LanguageDefault {
		t.Fatal("an earlier copy changed")
	}
	if s2.NodeRule("pi-1", "rappel") != gateway.LanguageOff || s2.NodeRule("pi-1", "go") != gateway.LanguageOn {
		t.Fatalf("s2: %+v", s2.NodeLanguages)
	}
	if s3.NodeRule("pi-1", "rappel") != gateway.LanguageOn || s2.NodeRule("pi-1", "rappel") != gateway.LanguageOff {
		t.Fatalf("s3: %+v / s2: %+v", s3.NodeLanguages, s2.NodeLanguages)
	}
	if s4.NodeRule("pi-1", "go") != gateway.LanguageDefault || s4.NodeRule("other", "go") != gateway.LanguageDefault {
		t.Fatalf("s4: %+v", s4.NodeLanguages)
	}
	if clean, err := s4.normalize(); err != nil || len(clean.NodeLanguages["pi-1"].Off) != 0 || strings.Join(clean.NodeLanguages["pi-1"].On, ",") != "rappel" {
		t.Fatalf("normalized: %+v %v", clean.NodeLanguages, err)
	}
}

func TestTheAuditLogSaysWhatChangedOnWhichNode(t *testing.T) {
	var a SiteSettings
	b := a.withNodeRule("pi-1", "rappel", gateway.LanguageOff).withNodeRule("local", "go", gateway.LanguageOn)
	got := strings.Join(nodeLanguageChanges(a, b), "\n")
	for _, want := range []string{"the gateway: go switched on", "worker pi-1: rappel switched off"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit lacks %q: %s", want, got)
		}
	}
	back := strings.Join(nodeLanguageChanges(b, a), "\n")
	if !strings.Contains(back, "worker pi-1: rappel back to what the node declares") {
		t.Errorf("going back: %s", back)
	}
	if len(nodeLanguageChanges(b, b)) != 0 {
		t.Error("no change was reported as one")
	}
}

// ---- what a node refuses -----------------------------------------------------------

func languageTestServer() *Server {
	return &Server{terminals: map[string]string{
		"ws": "", "ws_python": "python", "ws_c": "cling", "ws_cpp": "cling", "ws_go": "gointerpreter",
		"ws_evcxr": "evcxr", "ws_rappel": "rappel",
		"ws_javascript": "javascript", // a route like every demo, though its console runs in the browser
	}}
}

func TestANodeRefusesWhatItCannotRunWhatAnAdminSwitchedOffAndWhatTheSiteSwitchedOff(t *testing.T) {
	s := languageTestServer()
	declared := []string{"python", "cling", "rappel"}
	canRun := func(command string) bool { return nodeCanRun(declared, command) }
	has := func(list []string, v string) bool { return contains(list, v) }

	// nothing chosen: the languages the worker did not list are off for its visitors
	got := s.disabledOnNode(SiteSettings{}, "pi-1", canRun)
	if !has(got, "go") || !has(got, "evcxr") || has(got, "python") || has(got, "cpp") || has(got, "rappel") {
		t.Fatalf("by declaration: %v", got)
	}
	// JavaScript runs in the browser: a node's list says nothing about it
	if has(got, "javascript") {
		t.Fatalf("javascript was hidden because the worker did not declare it: %v", got)
	}
	// a node that did not list its languages runs everything
	if got := s.disabledOnNode(SiteSettings{}, "pi-1", nil); len(got) != 0 {
		t.Fatalf("no list: %v", got)
	}
	// the admin switches rappel off, go on (installed since), and the site switches python off
	set := SiteSettings{DisabledLanguages: []string{"python"}}.
		withNodeRule("pi-1", "rappel", gateway.LanguageOff).
		withNodeRule("pi-1", "go", gateway.LanguageOn)
	got = s.disabledOnNode(set, "pi-1", canRun)
	if !has(got, "rappel") || has(got, "go") || !has(got, "python") || !has(got, "evcxr") {
		t.Fatalf("with choices: %v", got)
	}
	// the rules are for pi-1 only
	if other := s.disabledOnNode(set, "pi-2", nil); has(other, "rappel") || !has(other, "python") {
		t.Fatalf("another node: %v", other)
	}
	// a language on a node can not undo the site-wide switch
	both := SiteSettings{DisabledLanguages: []string{"go"}}.withNodeRule("pi-1", "go", gateway.LanguageOn)
	if got := s.disabledOnNode(both, "pi-1", canRun); !has(got, "go") {
		t.Fatalf("switched on beat the site: %v", got)
	}
	// the list is sorted and never nil (the page reads it as an array)
	if got := s.disabledOnNode(SiteSettings{}, "x", nil); got == nil {
		t.Fatal("nil list")
	}
}

func TestEachWorkersConfigFollowsItsOwnLanguages(t *testing.T) {
	adminTestServer(t)
	s := languageTestServer()
	plain := s.workerConfigWith(nil)
	again := s.workerConfigWith([]string{})
	one := s.workerConfigWith([]string{"rappel"})
	two := s.workerConfigWith([]string{"go", "rappel"})
	if plain.Revision != again.Revision {
		t.Fatal("the same rules got another revision")
	}
	if one.Revision == plain.Revision || two.Revision == one.Revision || len(two.DisabledLanguages) != 2 {
		t.Fatalf("revisions %d %d %d, %v", plain.Revision, one.Revision, two.Revision, two.DisabledLanguages)
	}
	if g := s.gatewayWorkerConfig(); g.Revision != plain.Revision {
		t.Fatal("the config for workers without choices changed")
	}
}

func TestAnAdminIsLetThroughALanguageSwitchedOffOnTheNode(t *testing.T) {
	s, _ := adminTestServer(t)
	s.terminals = languageTestServer().terminals
	if err := SaveSiteSettings(SiteSettings{}.withNodeRule("pi-1", "python", gateway.LanguageOff).withNodeRule("pi-1", "go", gateway.LanguageOn)); err != nil {
		t.Fatal(err)
	}
	visitor := httptest.NewRequest("GET", "/ws_python", nil)
	admin := httptest.NewRequest("GET", "/ws_python", nil)
	admin.Header.Set("X-Test-Admin", "yes")
	w := httptest.NewRecorder()
	if got := s.nodeLanguageRule(w, visitor, "pi-1", "ws_python"); got != gateway.LanguageOff {
		t.Fatalf("visitor: %v", got)
	}
	if got := s.nodeLanguageRule(w, admin, "pi-1", "ws_python"); got != gateway.LanguageDefault {
		t.Fatalf("admin: %v", got)
	}
	// "on" is for everybody; another node and the plain shell have no rule
	if got := s.nodeLanguageRule(w, visitor, "pi-1", "ws_go"); got != gateway.LanguageOn {
		t.Fatalf("go on: %v", got)
	}
	if s.nodeLanguageRule(w, visitor, "pi-2", "ws_python") != gateway.LanguageDefault || s.nodeLanguageRule(w, visitor, "pi-1", "ws") != gateway.LanguageDefault {
		t.Fatal("a rule leaked to another node or to the shell")
	}
}

func TestThePickerOfAVisitorHidesWhatTheirNodeRefuses(t *testing.T) {
	adminTestServer(t)
	defer func() { nodeDisabledForRequest = nil }()
	nodeDisabledForRequest = func(r *http.Request) []string {
		if r.Header.Get("X-Node") == "pi-1" {
			return []string{"go", "rappel"}
		}
		return nil
	}
	SaveSiteSettings(SiteSettings{DisabledLanguages: []string{"python"}})

	on := httptest.NewRequest("GET", "/settings.js", nil)
	on.Header.Set("X-Node", "pi-1")
	w := httptest.NewRecorder()
	handleSettingsJS(w, on)
	if !strings.Contains(w.Body.String(), `"disabledLanguages":["go","rappel"]`) {
		t.Fatalf("on pi-1: %s", w.Body.String())
	}
	// a visitor whose node is not known gets the site's list
	w = httptest.NewRecorder()
	handleSettingsJS(w, httptest.NewRequest("GET", "/settings.js", nil))
	if !strings.Contains(w.Body.String(), `"disabledLanguages":["python"]`) {
		t.Fatalf("unknown node: %s", w.Body.String())
	}
}

// ---- the dashboard's route ---------------------------------------------------------

type stubNode struct {
	id    string
	langs []string
}

func (b *stubNode) ID() string                                         { return b.id }
func (b *stubNode) State() gateway.State                               { return gateway.Online }
func (b *stubNode) Weight() int                                        { return 10 }
func (b *stubNode) Capacity() (int64, int64)                           { return 0, 0 }
func (b *stubNode) HasLanguage(c string) bool                          { return nodeCanRun(b.langs, c) }
func (b *stubNode) Serve(w http.ResponseWriter, r *http.Request) error { return nil }

func nodeLanguagesTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, _ := adminTestServer(t)
	s.terminals = languageTestServer().terminals
	router := gateway.NewRouter(gateway.Config{Site: http.NotFoundHandler(), PathPrefix: "/", Secret: []byte("s"), Local: gateway.LocalConfig{Weight: 10}})
	router.AddBackend(&stubNode{id: "pi-1", langs: []string{"python", "cling"}})
	s.admin.router = router
	s.admin.prefix = "/"
	s.gatewayAdmin = http.NotFoundHandler()
	mux := http.NewServeMux()
	mux.Handle("/admin/workers/", s.adminAPI(http.HandlerFunc(s.handleGatewayAdmin)))
	mux.Handle("/admin/audit", s.adminAPI(http.HandlerFunc(s.handleAdminAudit)))
	return s, mux
}

func rowOf(t *testing.T, w *httptest.ResponseRecorder, lang string) nodeLanguageRow {
	t.Helper()
	var reply nodeLanguagesReply
	decode(t, w, &reply)
	for _, r := range reply.Languages {
		if r.Value == lang {
			return r
		}
	}
	t.Fatalf("no row for %s in %s", lang, w.Body.String())
	return nodeLanguageRow{}
}

func TestTheLanguagesCardOfAWorker(t *testing.T) {
	_, h := nodeLanguagesTestServer(t)

	w := get(h, "/admin/workers/pi-1/languages")
	if w.Code != 200 {
		t.Fatalf("GET -> %d %s", w.Code, w.Body.String())
	}
	var reply nodeLanguagesReply
	decode(t, w, &reply)
	if !reply.Online || reply.Node != "pi-1" || len(reply.Languages) == 0 {
		t.Fatalf("reply: %+v", reply)
	}
	for _, r := range reply.Languages {
		if r.Value == "javascript" {
			t.Fatal("a language with no terminal is in the list")
		}
	}
	if r := rowOf(t, w, "python"); !r.Declared || !r.Takes || r.Rule != "default" {
		t.Fatalf("python: %+v", r)
	}
	if r := rowOf(t, w, "go"); r.Declared || r.Takes || r.Command != "gointerpreter" {
		t.Fatalf("go (not declared): %+v", r)
	}
	if r := rowOf(t, w, "rappel"); !r.NeedsPtrace || r.Declared {
		t.Fatalf("rappel: %+v", r)
	}

	// switch go on although the worker did not declare it: it takes terminals now
	w = post(h, "/admin/workers/pi-1/languages", `{"language":"go","rule":"on"}`)
	if w.Code != 200 || !rowOf(t, w, "go").Takes || rowOf(t, w, "go").Rule != "on" || rowOf(t, w, "go").Declared {
		t.Fatalf("go on -> %d %s", w.Code, w.Body.String())
	}
	if GetSiteSettings().NodeRule("pi-1", "go") != gateway.LanguageOn {
		t.Fatal("the rule was not saved")
	}
	// and python off although it is declared
	w = post(h, "/admin/workers/pi-1/languages", `{"language":"python","rule":"off"}`)
	if r := rowOf(t, w, "python"); w.Code != 200 || r.Takes || r.Rule != "off" || !r.Declared {
		t.Fatalf("python off -> %d %+v", w.Code, r)
	}
	// the site switch wins over a rule that says on
	SaveSiteSettings(GetSiteSettings().withNodeRule("pi-1", "cpp", gateway.LanguageOn))
	SaveSiteSettings(func() SiteSettings { s := GetSiteSettings(); s.DisabledLanguages = []string{"cpp"}; return s }())
	if r := rowOf(t, get(h, "/admin/workers/pi-1/languages"), "cpp"); r.Takes || !r.SiteOff {
		t.Fatalf("cpp: %+v", r)
	}
	// back to default
	w = post(h, "/admin/workers/pi-1/languages", `{"language":"python","rule":"default"}`)
	if r := rowOf(t, w, "python"); r.Rule != "default" || !r.Takes {
		t.Fatalf("python default: %+v", r)
	}

	// the audit log says what changed, once per change and on which node
	var audit struct{ Entries []auditEntry }
	decode(t, get(h, "/admin/audit"), &audit)
	var lines []string
	for _, e := range audit.Entries {
		lines = append(lines, e.Detail)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"worker pi-1: go switched on", "worker pi-1: python switched off", "worker pi-1: python back to what the node declares"} {
		if strings.Count(all, want) != 1 {
			t.Errorf("audit log should have %q once: %s", want, all)
		}
	}
}

func TestTheLanguagesCardRefusesWhatItShould(t *testing.T) {
	_, h := nodeLanguagesTestServer(t)
	for name, c := range map[string]struct {
		call adminCall
		want int
	}{
		"a visitor":                      {adminCall{method: "GET", path: "/admin/workers/pi-1/languages"}, 401},
		"a change without the header":    {adminCall{method: "POST", path: "/admin/workers/pi-1/languages", body: `{"language":"go","rule":"on"}`, admin: true}, 403},
		"an unknown node":                {adminCall{method: "GET", path: "/admin/workers/ghost/languages", admin: true}, 404},
		"a bad node id":                  {adminCall{method: "GET", path: "/admin/workers/a%20b/languages", admin: true}, 400},
		"a language without a terminal":  {adminCall{method: "POST", path: "/admin/workers/pi-1/languages", body: `{"language":"javascript","rule":"off"}`, admin: true, header: true}, 400},
		"a language that does not exist": {adminCall{method: "POST", path: "/admin/workers/pi-1/languages", body: `{"language":"cobol","rule":"off"}`, admin: true, header: true}, 400},
		"a rule that does not exist":     {adminCall{method: "POST", path: "/admin/workers/pi-1/languages", body: `{"language":"go","rule":"maybe"}`, admin: true, header: true}, 400},
		"not JSON":                       {adminCall{method: "POST", path: "/admin/workers/pi-1/languages", body: `nope`, admin: true, header: true}, 400},
		"another method":                 {adminCall{method: "DELETE", path: "/admin/workers/pi-1/languages", admin: true, header: true}, 405},
	} {
		if w := c.call.do(h); w.Code != c.want {
			t.Errorf("%s -> %d, want %d (%s)", name, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
	if len(GetSiteSettings().NodeLanguages) != 0 {
		t.Fatalf("a refused request changed the settings: %+v", GetSiteSettings().NodeLanguages)
	}
}

func TestTheGatewayItselfHasALanguagesCardWithItsPtraceAnswer(t *testing.T) {
	_, h := nodeLanguagesTestServer(t)
	old := localPtrace
	localPtrace = "getregs: Input/output error"
	defer func() { localPtrace = old }()

	w := get(h, "/admin/workers/local/languages")
	var reply nodeLanguagesReply
	decode(t, w, &reply)
	if w.Code != 200 || reply.Ptrace != "getregs: Input/output error" || !reply.Online {
		t.Fatalf("local: %d %+v", w.Code, reply)
	}
	// the gateway runs everything it has: every language is declared and taken
	if r := rowOf(t, w, "go"); !r.Declared || !r.Takes {
		t.Fatalf("go on the gateway: %+v", r)
	}
	w = post(h, "/admin/workers/local/languages", `{"language":"rappel","rule":"off"}`)
	if r := rowOf(t, w, "rappel"); w.Code != 200 || r.Takes {
		t.Fatalf("rappel off on the gateway: %d %+v", w.Code, r)
	}
}

func TestSavingTheSettingsPageKeepsTheChoicesOfNodes(t *testing.T) {
	s, mux := adminTestServer(t)
	_ = s
	if err := SaveSiteSettings(SiteSettings{}.withNodeRule("pi-1", "rappel", gateway.LanguageOff)); err != nil {
		t.Fatal(err)
	}
	// the page sends its own fields and no nodeLanguages
	w := post(mux, "/admin/settings", `{"disabledLanguages":["cpp"],"colorOfTheDay":true}`)
	if w.Code != 200 {
		t.Fatalf("save -> %d %s", w.Code, w.Body.String())
	}
	got := GetSiteSettings()
	if got.NodeRule("pi-1", "rappel") != gateway.LanguageOff || !got.LanguageDisabled("cpp") {
		t.Fatalf("after the Settings page: %+v", got)
	}
	// and a page that tries to set them does not
	post(mux, "/admin/settings", `{"nodeLanguages":{"pi-9":{"off":["go"]}}}`)
	if got := GetSiteSettings(); got.NodeRule("pi-9", "go") != gateway.LanguageDefault || got.NodeRule("pi-1", "rappel") != gateway.LanguageOff {
		t.Fatalf("the Settings page set node choices: %+v", got.NodeLanguages)
	}
}

// ---- asking the host --------------------------------------------------------------

func TestThePtraceProbeAnswerIsItsFirstLineAndAMissingProbeIsNotAnAnswer(t *testing.T) {
	dir := t.TempDir()
	script := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "openrepl-ptrace-probe"), []byte("#!/bin/sh\n"+body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	script("echo ok\nexit 0\n")
	if got := ptraceProbe(); got != "ok" || !ptraceWorks(got) {
		t.Fatalf("a host that can: %q", got)
	}
	script("echo ok-aslr\nexit 2\n")
	if got := ptraceProbe(); got != "ok-aslr" || !ptraceWorks(got) {
		t.Fatalf("a host that cannot switch randomization off: %q", got)
	}
	script("echo 'getregs: Input/output error'\necho more\nexit 1\n")
	if got := ptraceProbe(); got != "getregs: Input/output error" || ptraceWorks(got) {
		t.Fatalf("a host that cannot: %q", got)
	}
	// an image from before the probe: unknown, not a failure
	os.Remove(filepath.Join(dir, "openrepl-ptrace-probe"))
	t.Setenv("PATH", dir)
	if got := ptraceProbe(); got != "" {
		t.Fatalf("no probe installed: %q", got)
	}
}
