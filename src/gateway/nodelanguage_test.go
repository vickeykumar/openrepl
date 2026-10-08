package gateway

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// nodeRules is a Config.NodeLanguage that answers from a table: node -> rel -> rule.
func nodeRules(table map[string]map[string]LanguageRule) func(http.ResponseWriter, *http.Request, string, string) LanguageRule {
	return func(_ http.ResponseWriter, _ *http.Request, node, rel string) LanguageRule {
		return table[node][rel]
	}
}

func asTerminal(r *http.Request) {
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
}

func nodeRouter(w Backend, rules map[string]map[string]LanguageRule, refused *[]string) *Router {
	rt := NewRouter(Config{
		Site:         &recorder{},
		PathPrefix:   "/",
		Secret:       []byte("secret"),
		GuestTTL:     time.Hour,
		Terminal:     terminalFor(map[string]string{"ws_python": "python", "ws_go": "gointerpreter"}),
		NodeLanguage: nodeRules(rules),
		RefuseTerminal: func(rw http.ResponseWriter, r *http.Request, reason string) bool {
			*refused = append(*refused, reason)
			rw.WriteHeader(http.StatusSwitchingProtocols) // stands in for the close with a reason
			return true
		},
	})
	rt.AddBackend(w)
	return rt
}

func TestALanguageSwitchedOffOnANodeIsRefusedWithAReasonThePageShows(t *testing.T) {
	w := &named{id: "worker-1", weight: 10, langs: []string{"python", "gointerpreter"}}
	var refused []string
	rt := nodeRouter(w, map[string]map[string]LanguageRule{"worker-1": {"ws_go": LanguageOff}}, &refused)

	first := do(rt, "GET", "/ws_python", asTerminal)
	c := cookieOf(t, first)
	if first.Code != http.StatusOK || w.hits != 1 {
		t.Fatalf("python -> %d, hits %d", first.Code, w.hits)
	}
	// the node declares go, but an admin switched it off: the page is told why
	rec := do(rt, "GET", "/ws_go", func(r *http.Request) { asTerminal(r); r.AddCookie(c) })
	if rec.Code != http.StatusSwitchingProtocols || w.hits != 1 || len(refused) != 1 || !strings.Contains(refused[0], "switched off") {
		t.Fatalf("go -> %d, hits %d, reasons %q", rec.Code, w.hits, refused)
	}
	// without a WebSocket there is nobody to tell with a close: a plain 503 with the same text
	rec = do(rt, "GET", "/ws_go", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "switched off") || w.hits != 1 {
		t.Fatalf("go without websocket -> %d %q", rec.Code, rec.Body.String())
	}
	// files and the other language still work; a rule is for one language only
	do(rt, "GET", "/ws_filebrowser", func(r *http.Request) { r.AddCookie(c) })
	do(rt, "GET", "/ws_python", func(r *http.Request) { asTerminal(r); r.AddCookie(c) })
	if w.hits != 3 {
		t.Fatalf("hits = %d, want 3", w.hits)
	}
}

func TestALanguageSwitchedOnLetsInWhatTheNodeDidNotDeclare(t *testing.T) {
	// started with --worker-languages python; go was installed later
	w := &named{id: "worker-1", weight: 10, langs: []string{"python"}}
	var refused []string
	rules := map[string]map[string]LanguageRule{}
	rt := nodeRouter(w, rules, &refused)

	first := do(rt, "GET", "/ws_python", asTerminal)
	c := cookieOf(t, first)
	rec := do(rt, "GET", "/ws_go", func(r *http.Request) { asTerminal(r); r.AddCookie(c) })
	if w.hits != 1 || len(refused) != 1 || !strings.Contains(refused[0], "not available") {
		t.Fatalf("undeclared go, no rule -> %d, hits %d, reasons %q", rec.Code, w.hits, refused)
	}

	rules["worker-1"] = map[string]LanguageRule{"ws_go": LanguageOn}
	rec = do(rt, "GET", "/ws_go", func(r *http.Request) { asTerminal(r); r.AddCookie(c) })
	if rec.Code != http.StatusOK || w.hits != 2 || len(refused) != 1 {
		t.Fatalf("go switched on -> %d, hits %d, reasons %q", rec.Code, w.hits, refused)
	}

	// back to the default: refused again; a rule changes the next terminal at once
	rules["worker-1"] = nil
	do(rt, "GET", "/ws_go", func(r *http.Request) { asTerminal(r); r.AddCookie(c) })
	if w.hits != 2 || len(refused) != 2 {
		t.Fatalf("go back to default: hits %d, reasons %q", w.hits, refused)
	}
}

func TestTheGatewaysOwnNodeCanHaveALanguageSwitchedOff(t *testing.T) {
	site := &recorder{}
	var refused []string
	rt := NewRouter(Config{
		Site: site, PathPrefix: "/", Secret: []byte("secret"), GuestTTL: time.Hour,
		Local:        LocalConfig{Weight: 10},
		Terminal:     terminalFor(map[string]string{"ws_python": "python"}),
		NodeLanguage: nodeRules(map[string]map[string]LanguageRule{LocalID: {"ws_python": LanguageOff}}),
		RefuseTerminal: func(rw http.ResponseWriter, r *http.Request, reason string) bool {
			refused = append(refused, reason)
			rw.WriteHeader(http.StatusSwitchingProtocols)
			return true
		},
	})
	rec := do(rt, "GET", "/ws_python", asTerminal)
	if rec.Code != http.StatusSwitchingProtocols || len(refused) != 1 || len(site.paths) != 0 {
		t.Fatalf("local python -> %d, reasons %q, site %v", rec.Code, refused, site.paths)
	}
}

func TestNodeOfOnlyLooksAtTheRequestersSession(t *testing.T) {
	w := &named{id: "worker-1", weight: 10, langs: []string{"python"}}
	var refused []string
	rt := nodeRouter(w, nil, &refused)

	if got := rt.NodeOf(requestWith(&http.Cookie{Name: "nobody", Value: "x"})); got != "" {
		t.Fatalf("a visitor without a session is on %q", got)
	}
	if rt.Registry().Len() != 0 {
		t.Fatal("looking up a node created a session")
	}
	c := cookieOf(t, do(rt, "GET", "/ws_python", asTerminal))
	if got := rt.NodeOf(requestWith(c)); got != "worker-1" {
		t.Fatalf("the session's node = %q", got)
	}
	if rt.Registry().Len() != 1 {
		t.Fatalf("sessions = %d", rt.Registry().Len())
	}
}
