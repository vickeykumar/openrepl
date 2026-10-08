package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"gateway"
)

// The Languages card of a node in the dashboard:
//
//	GET  /admin/workers/<id>/languages   the languages with what each node says and what an admin decided
//	POST /admin/workers/<id>/languages   {"language": "rappel", "rule": "default" | "off" | "on"}
//
// <id> is a worker id, or "local" for the gateway itself. Both run behind
// adminAPI. The rules are saved with the site settings (SiteSettings.NodeLanguages).

// nodeLanguageRow is one language of a node's card.
type nodeLanguageRow struct {
	Value   string `json:"value"`   // the picker's name for it
	Name    string `json:"name"`    // what the picker calls it
	Command string `json:"command"` // the REPL it starts
	// Declared is whether the node says it can run it (always true for a node
	// that did not list its languages, and for one that is offline: its list is
	// not known then).
	Declared bool `json:"declared"`
	// Rule is what the admin decided: "default", "off" or "on".
	Rule string `json:"rule"`
	// SiteOff means the whole site has it switched off (Settings page).
	SiteOff bool `json:"siteOff"`
	// Takes is whether the node takes new terminals of it now.
	Takes bool `json:"takes"`
	// NeedsPtrace is true for a language that cannot work without ptrace.
	NeedsPtrace bool `json:"needsPtrace,omitempty"`
}

type nodeLanguagesReply struct {
	Node   string `json:"node"`
	Online bool   `json:"online"`
	// Ptrace is what the node's host answered about tracing programs: "ok",
	// "ok-aslr", or why not; empty when unknown.
	Ptrace    string            `json:"ptrace,omitempty"`
	Languages []nodeLanguageRow `json:"languages"`
}

// nodeInfo finds what is known of a node: whether it is connected, whether it
// can run a REPL command, and what its host says about tracing programs.
func (server *Server) nodeInfo(node string) (online bool, canRun func(string) bool, ptrace string) {
	router := server.admin.router
	if node == gateway.LocalID {
		ptrace = localPtrace
	} else if ts := server.admin.tunnel; ts != nil {
		if tw := ts.Worker(node); tw != nil {
			ptrace = tw.Info().Ptrace
		}
	}
	if router == nil {
		return false, nil, ptrace
	}
	if b, found := router.Backend(node); found && b.State() != gateway.Offline {
		return true, b.HasLanguage, ptrace
	}
	return false, nil, ptrace
}

// nodeKnown reports whether the node exists (it is connected, or an admin has
// choices saved for it, which they may want to clear).
func (server *Server) nodeKnown(node string) bool {
	if node == "local" {
		return true
	}
	if server.admin.router != nil {
		if _, found := server.admin.router.Backend(node); found {
			return true
		}
	}
	_, saved := GetSiteSettings().NodeLanguages[node]
	return saved
}

func (server *Server) nodeLanguagesReply(node string) nodeLanguagesReply {
	s := GetSiteSettings()
	online, canRun, ptrace := server.nodeInfo(node)
	reply := nodeLanguagesReply{Node: node, Online: online, Ptrace: ptrace, Languages: []nodeLanguageRow{}}
	for _, l := range languageChoices() {
		command, routed := server.nodeCommand(l.Value)
		if !routed {
			continue // nothing a node runs (JavaScript is a console in the browser)
		}
		declared := true
		if canRun != nil {
			declared = canRun(command)
		}
		rule := s.NodeRule(node, l.Value)
		row := nodeLanguageRow{
			Value: l.Value, Name: l.Name, Command: command,
			Declared: declared, Rule: ruleName(rule), SiteOff: s.LanguageDisabled(l.Value),
			NeedsPtrace: languagesNeedingPtrace[l.Value],
		}
		row.Takes = online && !row.SiteOff && (rule == gateway.LanguageOn || (rule == gateway.LanguageDefault && declared))
		reply.Languages = append(reply.Languages, row)
	}
	return reply
}

// handleNodeLanguages serves the two routes above.
func (server *Server) handleNodeLanguages(w http.ResponseWriter, r *http.Request, node string) {
	if node != "local" && !nodeIDPattern.MatchString(node) {
		adminError(w, http.StatusBadRequest, "That is not a node id.")
		return
	}
	if !server.nodeKnown(node) {
		adminError(w, http.StatusNotFound, "No such node.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		adminJSON(w, http.StatusOK, server.nodeLanguagesReply(node))
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		var in struct {
			Language string `json:"language"`
			Rule     string `json:"rule"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			adminError(w, http.StatusBadRequest, "The request was not valid JSON.")
			return
		}
		lang := strings.ToLower(strings.TrimSpace(in.Language))
		if _, routed := server.nodeCommand(lang); !routed {
			adminError(w, http.StatusBadRequest, "That language has no terminal to switch.")
			return
		}
		var rule gateway.LanguageRule
		switch in.Rule {
		case "default":
			rule = gateway.LanguageDefault
		case "off":
			rule = gateway.LanguageOff
		case "on":
			rule = gateway.LanguageOn
		default:
			adminError(w, http.StatusBadRequest, `The rule must be "default", "off" or "on".`)
			return
		}
		if err := server.setNodeRule(r, node, lang, rule); err != nil {
			switch err {
			case errSettingsConflict:
				adminError(w, http.StatusConflict, "Someone else changed the settings at the same moment. Try again.")
			case errSettingsUnavailable:
				adminError(w, http.StatusServiceUnavailable, "The settings store is not reachable right now, so nothing was saved. Try again in a moment.")
			default:
				log.Println("saving a node's languages failed: ", err)
				adminError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		adminJSON(w, http.StatusOK, server.nodeLanguagesReply(node))
	default:
		w.Header().Set("Allow", "GET, POST")
		adminError(w, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}

// setNodeRule saves one rule. It reads, changes and writes the whole settings, so
// a store that moved on in between (MongoDB, another admin) is retried.
func (server *Server) setNodeRule(r *http.Request, node, lang string, rule gateway.LanguageRule) error {
	var err error
	for try := 0; try < 3; try++ {
		before := GetSiteSettings()
		after := before.withNodeRule(node, lang, rule)
		if err = SaveSiteSettings(after, currentSettingsVersion()); err == errSettingsConflict {
			continue
		}
		if err != nil {
			return err
		}
		for _, change := range nodeLanguageChanges(before, GetSiteSettings()) {
			server.audit(r, "settings", change)
		}
		return nil
	}
	return err
}

// currentSettingsVersion is the version of the settings last read from the store
// (0 for the file store).
func currentSettingsVersion() int64 {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settingsVersion
}
