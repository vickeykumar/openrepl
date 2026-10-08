package server

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"gateway"
	"tunnel"
)

// Languages per node.
//
// The site-wide switch (SiteSettings.DisabledLanguages) turns a language off for
// everybody. A node (the gateway itself, "local", or a worker) has its own
// answer on top of that, in two parts:
//
//   - what the node says it can run: a worker started with --worker-languages
//     lists its REPL commands, any other node runs everything;
//   - what an admin decided for one language on that node
//     (SiteSettings.NodeLanguages): off (refuse new terminals of it, although the
//     node can run it) or on (take it, although the node did not declare it,
//     because it was installed since the worker started).
//
// Only new terminals are refused; open ones keep running. The router enforces
// the answer where it picks the backend of a terminal (gateway.Config.NodeLanguage);
// the page hides the languages that are off on the visitor's node (settings.js);
// a worker also gets its own list in its WorkerConfig, for the people who open
// the worker's own port.

// maxNodeLanguageNodes bounds how many nodes can have choices saved.
const maxNodeLanguageNodes = 200

// nodeIDPattern is the id of a node: a worker id (tunnel) or "local".
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// NodeLanguages is what an admin decided about the languages of one node.
// Languages are the picker's values (python, cpp, evcxr, ...).
type NodeLanguages struct {
	// Off are refused on the node although it can run them.
	Off []string `json:"off,omitempty"`
	// On are taken on the node although it did not declare them.
	On []string `json:"on,omitempty"`
}

func cleanLanguageList(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" || seen[l] {
			continue
		}
		if !languagePattern.MatchString(l) {
			return nil, fmt.Errorf("%q is not a language name", l)
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) > maxDisabledLangs {
		return nil, fmt.Errorf("at most %d languages can be set for one node", maxDisabledLangs)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeNodeLanguages cleans the choices of an untrusted source: valid node
// ids and language names, sorted, no language both on and off, no empty entry.
func normalizeNodeLanguages(in map[string]NodeLanguages) (map[string]NodeLanguages, error) {
	if len(in) > maxNodeLanguageNodes {
		return nil, fmt.Errorf("choices can be saved for at most %d nodes", maxNodeLanguageNodes)
	}
	out := map[string]NodeLanguages{}
	for id, nl := range in {
		if id != "local" && !nodeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%q is not a node id", id)
		}
		off, err := cleanLanguageList(nl.Off)
		if err != nil {
			return nil, err
		}
		on, err := cleanLanguageList(nl.On)
		if err != nil {
			return nil, err
		}
		for _, l := range off {
			for _, o := range on {
				if l == o {
					return nil, fmt.Errorf("%s cannot be both on and off for node %s", l, id)
				}
			}
		}
		if len(off) > 0 || len(on) > 0 {
			out[id] = NodeLanguages{Off: off, On: on}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func without(list []string, v string) []string {
	var out []string
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// NodeRule is what the admin decided for the language on the node.
func (s SiteSettings) NodeRule(node, lang string) gateway.LanguageRule {
	nl := s.NodeLanguages[node]
	switch {
	case contains(nl.Off, lang):
		return gateway.LanguageOff
	case contains(nl.On, lang):
		return gateway.LanguageOn
	}
	return gateway.LanguageDefault
}

// withNodeRule returns the settings with the rule set; s is not changed.
func (s SiteSettings) withNodeRule(node, lang string, rule gateway.LanguageRule) SiteSettings {
	nodes := map[string]NodeLanguages{}
	for id, nl := range s.NodeLanguages {
		nodes[id] = NodeLanguages{Off: append([]string(nil), nl.Off...), On: append([]string(nil), nl.On...)}
	}
	nl := nodes[node]
	nl.Off, nl.On = without(nl.Off, lang), without(nl.On, lang)
	switch rule {
	case gateway.LanguageOff:
		nl.Off = append(nl.Off, lang)
	case gateway.LanguageOn:
		nl.On = append(nl.On, lang)
	}
	nodes[node] = nl
	s.NodeLanguages = nodes
	return s
}

func ruleName(r gateway.LanguageRule) string {
	switch r {
	case gateway.LanguageOff:
		return "off"
	case gateway.LanguageOn:
		return "on"
	}
	return "default"
}

func nodeLabel(id string) string {
	if id == "local" {
		return "the gateway"
	}
	return "worker " + id
}

// nodeLanguageChanges describes the differences in the choices for the audit log.
func nodeLanguageChanges(a, b SiteSettings) []string {
	ids := map[string]bool{}
	for id := range a.NodeLanguages {
		ids[id] = true
	}
	for id := range b.NodeLanguages {
		ids[id] = true
	}
	var sorted []string
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	var out []string
	for _, id := range sorted {
		langs := map[string]bool{}
		for _, nl := range []NodeLanguages{a.NodeLanguages[id], b.NodeLanguages[id]} {
			for _, l := range nl.Off {
				langs[l] = true
			}
			for _, l := range nl.On {
				langs[l] = true
			}
		}
		var names []string
		for l := range langs {
			names = append(names, l)
		}
		sort.Strings(names)
		for _, l := range names {
			from, to := a.NodeRule(id, l), b.NodeRule(id, l)
			if from == to {
				continue
			}
			what := map[gateway.LanguageRule]string{
				gateway.LanguageOff:     "switched off",
				gateway.LanguageOn:      "switched on (the node did not declare it)",
				gateway.LanguageDefault: "back to what the node declares",
			}[to]
			out = append(out, fmt.Sprintf("%s: %s %s", nodeLabel(id), l, what))
		}
	}
	return out
}

// ---- what a node refuses -----------------------------------------------------

// browserLanguages run in the visitor's browser (JavaScript is a console in an
// iframe, LLD 04): they have a route on the server, but no node ever runs them,
// so what a node declares or an admin chooses does not apply to them.
var browserLanguages = map[string]bool{"javascript": true}

// nodeCommand is the REPL command a node starts for the picker's language, and
// whether the language is one a node runs at all.
func (server *Server) nodeCommand(lang string) (string, bool) {
	if browserLanguages[lang] {
		return "", false
	}
	command, routed := server.terminals["ws_"+lang]
	return command, routed
}

// nodeCanRun reports whether a node says it can run the REPL command: a worker
// that declared a list can run those, any other node everything.
func nodeCanRun(declared []string, command string) bool {
	return len(declared) == 0 || contains(declared, command)
}

// disabledOnNode lists the languages (the picker's values) a node does not take
// new terminals for: the site-wide ones, the ones switched off on the node, and
// the ones it cannot run unless an admin switched them on. canRun may be nil
// (the node runs everything).
func (server *Server) disabledOnNode(s SiteSettings, node string, canRun func(command string) bool) []string {
	out := []string{}
	for _, l := range languageChoices() {
		command, routed := server.nodeCommand(l.Value)
		if !routed {
			// nothing a node runs (JavaScript is a console in the browser): only the site-wide switch
			if s.LanguageDisabled(l.Value) {
				out = append(out, l.Value)
			}
			continue
		}
		switch s.NodeRule(node, l.Value) {
		case gateway.LanguageOff:
			out = append(out, l.Value)
		case gateway.LanguageOn:
			if s.LanguageDisabled(l.Value) {
				out = append(out, l.Value)
			}
		default:
			if s.LanguageDisabled(l.Value) || (canRun != nil && !canRun(command)) {
				out = append(out, l.Value)
			}
		}
	}
	sort.Strings(out)
	return out
}

// workerConfigWith is the site rules as a WorkerConfig with the given languages
// switched off, and a revision made from the content.
func (server *Server) workerConfigWith(disabled []string) *tunnel.WorkerConfig {
	s := GetSiteSettings()
	level := s.Announcement.Level
	if level == "" {
		level = "info" // what normalize makes of an empty level; the revision must not tell them apart
	}
	cfg := &tunnel.WorkerConfig{
		ColorOfTheDay:      s.ColorOfTheDay,
		AnnouncementText:   s.Announcement.Text,
		AnnouncementLevel:  level,
		Maintenance:        s.Maintenance.Enabled,
		MaintenanceMessage: s.Maintenance.Message,
		DisabledLanguages:  append([]string(nil), disabled...),
	}
	data, _ := json.Marshal(cfg)
	h := fnv.New64a()
	h.Write(data)
	cfg.Revision = int64(h.Sum64()&0x7fffffffffffffff) | 1 // never 0, which means "none yet"
	return cfg
}

// gatewayWorkerConfigFor is what the gateway hands to one worker: the site rules
// with the languages that are off on that worker.
func (server *Server) gatewayWorkerConfigFor(w *tunnel.Worker) *tunnel.WorkerConfig {
	declared := w.Info().Languages
	disabled := server.disabledOnNode(GetSiteSettings(), w.ID(), func(command string) bool {
		return nodeCanRun(declared, command)
	})
	return server.workerConfigWith(disabled)
}

// nodeLanguageRule is the router's hook: the rule for the language of a terminal
// route on a node. An admin is let through a language that is off on the node,
// as the site-wide switch lets them through.
func (server *Server) nodeLanguageRule(w http.ResponseWriter, r *http.Request, node, rel string) gateway.LanguageRule {
	lang, ok := server.terminalLanguage(rel)
	if !ok || lang == "" {
		return gateway.LanguageDefault
	}
	rule := GetSiteSettings().NodeRule(node, lang)
	if rule == gateway.LanguageOff && server.isAdmin(w, r) {
		return gateway.LanguageDefault
	}
	return rule
}

// refuseTerminal closes a terminal's WebSocket with a reason the page shows.
func (server *Server) refuseTerminal(w http.ResponseWriter, r *http.Request, reason string) bool {
	server.closeWithNotice(w, r, reason)
	return true
}

// nodeDisabledForRequest, when set (on a gateway), returns the languages that
// are off on the node that serves the visitor, or nil when it is not known.
// settings.js uses it, so that the picker hides what the visitor's node does not
// take.
var nodeDisabledForRequest func(r *http.Request) []string

func (server *Server) bindNodeLanguages(router *gateway.Router) {
	nodeDisabledForRequest = func(r *http.Request) []string {
		node := router.NodeOf(r)
		if node == "" || node == "-" {
			return nil
		}
		var canRun func(string) bool
		if b, found := router.Backend(node); found {
			canRun = b.HasLanguage
		}
		return server.disabledOnNode(GetSiteSettings(), node, canRun)
	}
}

// ---- asking the host whether programs can be traced -------------------------------

// ptraceProbe asks the host, with the probe the image ships
// (scripts/ptrace-probe.c), whether programs can be traced: "ok", "ok-aslr", or
// why not. "" means it could not find out (no probe installed). Debug and the
// assembly REPL need it; the dashboard shows the answer.
func ptraceProbe() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "openrepl-ptrace-probe").Output()
	text := strings.TrimSpace(string(out))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if text == "" {
		if _, notFound := err.(*exec.Error); notFound {
			return ""
		}
		if err != nil {
			log.Println("ptrace probe: ", err)
		}
		return ""
	}
	return text
}

// localPtrace is the gateway's own answer, asked once when it starts.
var localPtrace string

// probeHost asks the host and logs the answer, for the operator reading the log.
func probeHost(who string) string {
	answer := ptraceProbe()
	switch {
	case answer == "":
		log.Printf("%s: no ptrace probe installed (openrepl-ptrace-probe); not known whether programs can be traced here", who)
	case ptraceWorks(answer):
		log.Printf("%s: programs can be traced on this host (%s)", who, answer)
	default:
		log.Printf("%s: programs CANNOT be traced on this host (%s): Debug will run them under QEMU, and the assembly REPL cannot work", who, answer)
	}
	return answer
}

// ptraceWorks reports whether the probe's answer says programs can be traced.
func ptraceWorks(answer string) bool { return strings.HasPrefix(answer, "ok") }

// languagesNeedingPtrace are the picker languages that cannot work without it:
// the assembly REPL traces its child process. (Debug in the others falls back to
// QEMU, see scripts/openrepl-gdb.)
var languagesNeedingPtrace = map[string]bool{"rappel": true}
