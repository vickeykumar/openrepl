package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cookie"
)

// Genie skills and plugins (LLD 19 section 23).
//
// A skill is a Markdown file with a short header: a name, a description, and a few limits. It is text for the model: nothing in it
// runs by itself, and whatever it asks Genie to do goes through the same actions, cards and permissions as any task. Skills live in
// the user's workspace, in .genie/skills/<name>.md, or come from a plugin (.genie/plugins/<plugin>/skills/<name>.md), or are built
// in or switched on for the whole site by an admin.
//
// A plugin is a folder with a plugin.json and skills, never code. It comes from the catalogue the site ships (genie_catalogue.go)
// or, when an admin allows it, from a GitHub repository or a .zip file at a link (genie_ext_fetch.go). The server fetches the link,
// never the browser, and checks it before the person sees what it brings.
//
//	POST /genie_ext {"op":"list"}                                the skills and plugins in effect, and what the site allows
//	POST /genie_ext {"op":"read","source":"workspace","name":"x"} one skill with its text
//	POST /genie_ext {"op":"save","name":"x","text":"---…"}        write .genie/skills/x.md
//	POST /genie_ext {"op":"delete","name":"x"}                    remove it
//	POST /genie_ext {"op":"catalogue"}                            the plugins of the catalogue, with what is installed
//	POST /genie_ext {"op":"plugin_fetch","id"|"url"|"name":…}     what a plugin brings (nothing is written)
//	POST /genie_ext {"op":"plugin_apply","id"|"url"|"name":…,"digest":…}  install or update it, if it is still what was shown
//	POST /genie_ext {"op":"plugin_remove","name":"x"}
//
// Only the owner of a workspace reaches the workspace's skills; a guest and a collaborator of a shared session get the built-in
// chat skills. It is execution-bound in the gateway like genie_chats, because the workspace is on the node that runs it.

const (
	skillsDirName     = ".genie"
	skillsSub         = "skills"
	pluginsSub        = "plugins"
	maxSkillBytes     = 16 << 10
	maxSkillDesc      = 160
	maxSkillsInList   = 100
	maxPluginsInstall = 20
	maxExtBody        = 64 << 10
	extPerMinute      = 120
)

var skillNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var pluginNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// names the panel itself uses for its own commands: a skill cannot take them
var reservedSkillNames = map[string]bool{"context": true, "tour": true, "remember": true, "skill": true, "skills": true, "add-plugin": true, "save-skill": true, "help": true, "clear": true}

// skillInfo is a skill as the list shows it (without its text).
type skillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	Args        string   `json:"args,omitempty"`
	Actions     []string `json:"actions,omitempty"`
	Effort      string   `json:"effort,omitempty"`
	// Source is "builtin", "workspace", "plugin" or "site"; Plugin is the plugin's name for the last two plugin kinds
	Source string `json:"source"`
	Plugin string `json:"plugin,omitempty"`
	// Path is where a workspace or plugin skill is, relative to the workspace: the agent reads its text with files_read
	Path  string `json:"path,omitempty"`
	Chars int    `json:"chars"`
	// Link is set on a skill of a plugin that was installed from a link
	Link bool `json:"link,omitempty"`
}

// parseSkill reads a skill file: a header between two --- lines, then the text. It says what is wrong in words a person can act on.
func parseSkill(text string) (skillInfo, string, error) {
	var info skillInfo
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return info, "", fmt.Errorf("the file is not text")
	}
	if len(text) > maxSkillBytes {
		return info, "", fmt.Errorf("the skill is longer than %d KB", maxSkillBytes>>10)
	}
	text = strings.TrimPrefix(text, "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return info, "", fmt.Errorf("the file has to start with a header: a line of three dashes, name and description lines, and another line of three dashes")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return info, "", fmt.Errorf("the header is not closed with a line of three dashes")
	}
	head := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\n")
	if i := strings.Index(body, "\n"); i >= 0 && strings.TrimSpace(body[:i]) == "" {
		body = body[i+1:]
	}
	info.Mode = "chat"
	seen := map[string]bool{}
	for _, raw := range strings.Split(head, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			return info, "", fmt.Errorf("a header line has to look like \"name: value\": %q", clipRunes(line, 40))
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		// a trailing comment after the value ("mode: agent   # agent or chat") is left out
		if j := strings.Index(val, "  #"); j >= 0 {
			val = strings.TrimSpace(val[:j])
		}
		val = strings.Trim(val, "\"'")
		if seen[key] {
			return info, "", fmt.Errorf("the header has %q twice", key)
		}
		seen[key] = true
		switch key {
		case "name":
			info.Name = val
		case "description":
			info.Description = val
		case "mode":
			if val != "chat" && val != "agent" {
				return info, "", fmt.Errorf("mode has to be chat or agent")
			}
			info.Mode = val
		case "args":
			info.Args = clipRunes(val, 80)
		case "effort":
			if val != "low" && val != "medium" && val != "high" {
				return info, "", fmt.Errorf("effort has to be low, medium or high")
			}
			info.Effort = val
		case "actions":
			list, err := parseActionList(val)
			if err != nil {
				return info, "", err
			}
			info.Actions = list
		default:
			return info, "", fmt.Errorf("the header has a line the skill format does not know: %q (it knows name, description, mode, args, actions and effort)", key)
		}
	}
	if !skillNamePattern.MatchString(info.Name) {
		return info, "", fmt.Errorf("the name has to be lower case letters, digits and dashes, starting with a letter, at most 40 characters")
	}
	if reservedSkillNames[info.Name] {
		return info, "", fmt.Errorf("%q is a command of the panel itself: choose another name", info.Name)
	}
	if info.Description == "" {
		return info, "", fmt.Errorf("the description is empty: it is what the menu and Genie show")
	}
	if len([]rune(info.Description)) > maxSkillDesc {
		return info, "", fmt.Errorf("the description is longer than %d characters", maxSkillDesc)
	}
	if strings.TrimSpace(body) == "" {
		return info, "", fmt.Errorf("the skill has no text after the header")
	}
	info.Chars = len(body)
	return info, body, nil
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// parseActionList reads "[bash, files_read]" or "bash, files_read" and keeps only actions that exist: a name that does not is an error,
// since a skill that limits a task to an action that is not there would limit it to nothing.
func parseActionList(v string) ([]string, error) {
	v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), "[]"))
	if v == "" {
		return nil, nil
	}
	known := map[string]bool{}
	for _, a := range agentActions {
		known[a] = true
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.Trim(strings.TrimSpace(p), "\"'")
		if p == "" {
			continue
		}
		if !known[p] {
			return nil, fmt.Errorf("actions names %q, which is not an action", p)
		}
		out = append(out, p)
	}
	if len(out) > 40 {
		return nil, fmt.Errorf("actions names too many actions")
	}
	return out, nil
}

// ---- the files of a workspace ----------------------------------------------------------------------------------------

// extDirs are the skills folder and the plugins folder of a workspace; with create they are made. A folder that is a link or a
// file is refused, so a symbolic link cannot take the server out of the workspace.
func extDirs(realHome string, create bool) (skills, plugins string, err error) {
	root := filepath.Join(realHome, skillsDirName)
	skills = filepath.Join(root, skillsSub)
	plugins = filepath.Join(root, pluginsSub)
	for _, d := range []string{root, skills, plugins} {
		if st, e := os.Lstat(d); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
			return "", "", fmt.Errorf("%s is not a plain folder", d)
		}
	}
	if create {
		for _, d := range []string{skills, plugins} {
			if e := os.MkdirAll(d, 0o755); e != nil {
				return "", "", e
			}
		}
	}
	return skills, plugins, nil
}

func writeTextFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// plainFile reads a regular file (not a link) of at most max bytes.
func plainFile(path string, max int64) ([]byte, bool) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > max {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

type skillProblem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// installedPlugin is a plugin that is in the workspace.
type installedPlugin struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Source      string `json:"source"` // "catalogue:<id>" or "link:<url>"
	Link        bool   `json:"link"`
	Skills      int    `json:"skills"`
	Digest      string `json:"digest,omitempty"`
}

// installRecord is what the server writes beside a plugin it installed: where it came from and what it was.
type installRecord struct {
	Source string `json:"source"`
	Digest string `json:"digest"`
	At     string `json:"at"`
}

func readInstalled(pluginsDir, name string) (installedPlugin, bool) {
	var p installedPlugin
	dir := filepath.Join(pluginsDir, name)
	b, ok := plainFile(filepath.Join(dir, "plugin.json"), 32<<10)
	if !ok {
		return p, false
	}
	var m pluginManifest
	if json.Unmarshal(b, &m) != nil || m.Name != name {
		return p, false
	}
	p = installedPlugin{Name: name, Title: orStr(m.Title, name), Version: m.Version, Description: m.Description}
	if rb, ok := plainFile(filepath.Join(dir, "install.json"), 4<<10); ok {
		var r installRecord
		if json.Unmarshal(rb, &r) == nil {
			p.Source, p.Digest = r.Source, r.Digest
			p.Link = strings.HasPrefix(r.Source, "link:")
		}
	}
	return p, true
}

// workspaceSkills lists the skills of a workspace: its own and those of its plugins. A file that does not parse is a problem
// that the list reports, and is not a skill.
func workspaceSkills(realHome string) (skills []skillInfo, plugins []installedPlugin, problems []skillProblem) {
	skillsDir, pluginsDir, err := extDirs(realHome, false)
	if err != nil {
		return nil, nil, []skillProblem{{Path: ".genie", Error: "the folder cannot be used"}}
	}
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if len(skills) >= maxSkillsInList || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			rel := ".genie/skills/" + e.Name()
			b, ok := plainFile(filepath.Join(skillsDir, e.Name()), maxSkillBytes+1024)
			if !ok {
				problems = append(problems, skillProblem{rel, "not a plain text file, or too long"})
				continue
			}
			info, _, perr := parseSkill(string(b))
			if perr != nil {
				problems = append(problems, skillProblem{rel, perr.Error()})
				continue
			}
			if info.Name+".md" != e.Name() {
				problems = append(problems, skillProblem{rel, "the file name has to be " + info.Name + ".md, the name in its header"})
				continue
			}
			info.Source, info.Path = "workspace", rel
			skills = append(skills, info)
		}
	}
	if entries, err := os.ReadDir(pluginsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || len(plugins) >= maxPluginsInstall || !pluginNamePattern.MatchString(e.Name()) {
				continue
			}
			p, ok := readInstalled(pluginsDir, e.Name())
			if !ok {
				continue
			}
			sdir := filepath.Join(pluginsDir, e.Name(), "skills")
			if files, err := os.ReadDir(sdir); err == nil {
				for _, f := range files {
					if !strings.HasSuffix(f.Name(), ".md") || len(skills) >= maxSkillsInList {
						continue
					}
					rel := ".genie/plugins/" + e.Name() + "/skills/" + f.Name()
					b, ok := plainFile(filepath.Join(sdir, f.Name()), maxSkillBytes+1024)
					if !ok {
						continue
					}
					info, _, perr := parseSkill(string(b))
					if perr != nil {
						problems = append(problems, skillProblem{rel, perr.Error()})
						continue
					}
					info.Source, info.Plugin, info.Path, info.Link = "plugin", e.Name(), rel, p.Link
					skills = append(skills, info)
					p.Skills++
				}
			}
			plugins = append(plugins, p)
		}
	}
	return skills, plugins, problems
}

// workspaceNotes lists the notes of the plugins installed in a workspace (knowledge/<name>.md): Genie reads one when a task is about what it says.
func workspaceNotes(realHome string) []noteInfo {
	out := []noteInfo{}
	_, pluginsDir, err := extDirs(realHome, false)
	if err != nil {
		return out
	}
	plugins, err := os.ReadDir(pluginsDir)
	if err != nil {
		return out
	}
	for _, pe := range plugins {
		if !pe.IsDir() || !pluginNamePattern.MatchString(pe.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(pluginsDir, pe.Name(), "knowledge"))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := strings.TrimSuffix(f.Name(), ".md")
			if !strings.HasSuffix(f.Name(), ".md") || !noteNamePattern.MatchString(name) || len(out) >= 60 {
				continue
			}
			if b, ok := plainFile(filepath.Join(pluginsDir, pe.Name(), "knowledge", f.Name()), pluginMaxNote+1024); ok {
				out = append(out, noteInfo{Name: name, Title: noteTitle(string(b), name), Plugin: pe.Name(), Source: "plugin", Path: ".genie/plugins/" + pe.Name() + "/knowledge/" + f.Name(), Chars: len(b)})
			}
		}
	}
	return out
}

// mergeSkills puts the sources in order of who wins a name: the workspace's own, then plugins, then the site's, then the built-in
// ones. A name that is taken keeps the first; the others are dropped from the list and counted.
func mergeSkills(lists ...[]skillInfo) ([]skillInfo, int) {
	seen := map[string]bool{}
	var out []skillInfo
	dropped := 0
	for _, l := range lists {
		for _, s := range l {
			if seen[s.Name] {
				dropped++
				continue
			}
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	return out, dropped
}

// ---- the handler ---------------------------------------------------------------------------------------------------------

var extRate = struct {
	sync.Mutex
	seen map[string][]time.Time
}{seen: map[string][]time.Time{}}

func extAllowed(who string, now time.Time) bool {
	extRate.Lock()
	defer extRate.Unlock()
	cut := now.Add(-time.Minute)
	keep := extRate.seen[who][:0]
	for _, t := range extRate.seen[who] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= extPerMinute {
		extRate.seen[who] = keep
		return false
	}
	extRate.seen[who] = append(keep, now)
	return true
}

type extRequest struct {
	Op     string `json:"op"`
	Source string `json:"source"`
	Name   string `json:"name"`
	Text   string `json:"text"`
	ID     string `json:"id"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
}

func (server *Server) handleGenieExt(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	g := GetSiteSettings().Genie
	if g.Disabled {
		jsonError(rw, http.StatusForbidden, "off", "Genie is switched off on this site.")
		return
	}
	req.ParseForm()
	var in extRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, maxExtBody)).Decode(&in); err != nil {
		jsonError(rw, http.StatusBadRequest, "bad_request", "send {\"op\": ...}")
		return
	}
	signedIn := cookie.Is_UserLoggedIn(req)
	owner := signedIn && !homeOverridden(req)
	// a guest or a collaborator gets only the built-in skills: nothing here may look up or start an identity for them (a guest's workspace
	// is made by the page's own session, and a second one made here would take the page's files away)
	who, home := "ip:"+clientIP(req), ""
	if owner {
		uid, h := server.requestIdentity(rw, req)
		home = h
		if uid != "" {
			who = uid
		} else {
			who = h
		}
	}
	if !extAllowed(who, time.Now()) {
		jsonError(rw, http.StatusTooManyRequests, "rate", "Too many requests in a minute: wait a little.")
		return
	}
	reply := func(v interface{}) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(v)
	}
	if g.ExtensionsOff {
		if in.Op == "list" {
			reply(map[string]interface{}{"off": true, "skills": []skillInfo{}, "plugins": []installedPlugin{}, "problems": []skillProblem{}})
			return
		}
		jsonError(rw, http.StatusForbidden, "off", "Skills and plugins are switched off on this site.")
		return
	}
	builtin := builtinSkillInfos(owner)
	// what a person who is not the owner of a signed-in workspace may do: see the built-in chat skills
	if !owner {
		switch in.Op {
		case "list":
			reply(map[string]interface{}{"skills": builtin, "plugins": []installedPlugin{}, "problems": []skillProblem{}, "canWrite": false, "fromLinks": false})
		case "read":
			if body, info, ok := builtinSkillText(in.Name); ok && info.Mode == "chat" {
				reply(map[string]interface{}{"skill": info, "text": body})
				return
			}
			jsonError(rw, http.StatusNotFound, "no_skill", "There is no such skill.")
		default:
			jsonError(rw, http.StatusForbidden, "sign_in", "Sign in to your own workspace to keep skills and plugins.")
		}
		return
	}
	os.MkdirAll(home, 0o755)
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		jsonError(rw, http.StatusNotFound, "no_workspace", "The workspace is not there yet.")
		return
	}
	switch in.Op {
	case "list":
		ws, plugins, problems := workspaceSkills(realHome)
		var site []skillInfo
		for _, id := range g.SitePlugins {
			if cp, ok := catalogueByID(id); ok {
				site = append(site, cp.skillInfos("site")...)
			}
		}
		all, dropped := mergeSkills(ws, site, builtin)
		sortSkills(all)
		if problems == nil {
			problems = []skillProblem{}
		}
		if plugins == nil {
			plugins = []installedPlugin{}
		}
		notes := workspaceNotes(realHome)
		for _, id := range g.SitePlugins {
			if cp, ok := catalogueByID(id); ok {
				for _, n := range cp.Notes {
					notes = append(notes, noteInfo{Name: n.Name, Title: noteTitle(n.Text, n.Name), Plugin: cp.Name, Source: "site", Path: sitePluginDir + cp.Name + "/knowledge/" + n.Name + ".md", Chars: len(n.Text)})
				}
			}
		}
		reply(map[string]interface{}{"skills": all, "plugins": plugins, "problems": problems, "dropped": dropped, "notes": notes,
			"canWrite": true, "fromLinks": g.PluginsFromLinks})
	case "read":
		reply2, status, code, msg := server.readSkill(realHome, in, g)
		if code != "" {
			jsonError(rw, status, code, msg)
			return
		}
		reply(reply2)
	case "save":
		skillsDir, _, err := extDirs(realHome, true)
		if err != nil {
			jsonError(rw, http.StatusInternalServerError, "folder", "The skills folder could not be used.")
			return
		}
		info, _, perr := parseSkill(in.Text)
		if perr != nil {
			jsonError(rw, http.StatusBadRequest, "bad_skill", "This skill cannot be saved: "+perr.Error()+".")
			return
		}
		if in.Name != "" && in.Name != info.Name {
			jsonError(rw, http.StatusBadRequest, "bad_skill", "The name in the header (\""+info.Name+"\") is not the name asked for (\""+in.Name+"\").")
			return
		}
		existing, _, _ := workspaceSkills(realHome)
		own := 0
		for _, s := range existing {
			if s.Source == "workspace" {
				own++
			}
		}
		target := filepath.Join(skillsDir, info.Name+".md")
		if _, statErr := os.Lstat(target); statErr != nil && own >= maxSkillsInList {
			jsonError(rw, http.StatusBadRequest, "too_many", fmt.Sprintf("A workspace keeps at most %d skills of its own.", maxSkillsInList))
			return
		}
		if err := writeTextFile(target, []byte(strings.ReplaceAll(in.Text, "\r\n", "\n"))); err != nil {
			jsonError(rw, http.StatusInternalServerError, "write", "The skill could not be saved.")
			return
		}
		info.Source, info.Path = "workspace", ".genie/skills/"+info.Name+".md"
		reply(map[string]interface{}{"skill": info})
	case "delete":
		if !skillNamePattern.MatchString(in.Name) {
			jsonError(rw, http.StatusBadRequest, "bad_name", "That is not a skill name.")
			return
		}
		skillsDir, _, err := extDirs(realHome, false)
		if err != nil {
			jsonError(rw, http.StatusInternalServerError, "folder", "The skills folder could not be used.")
			return
		}
		if err := os.Remove(filepath.Join(skillsDir, in.Name+".md")); err != nil {
			jsonError(rw, http.StatusNotFound, "no_skill", "There is no such skill in this workspace.")
			return
		}
		reply(map[string]interface{}{"deleted": in.Name})
	case "catalogue", "plugin_fetch", "plugin_apply", "plugin_remove":
		server.handlePluginOp(rw, in, realHome, g, who, reply)
	default:
		jsonError(rw, http.StatusBadRequest, "bad_op", "That is not something Genie's skills can do.")
	}
}

func sortSkills(l []skillInfo) {
	rank := map[string]int{"workspace": 0, "plugin": 1, "site": 2, "builtin": 3}
	sort.SliceStable(l, func(i, j int) bool {
		if rank[l[i].Source] != rank[l[j].Source] {
			return rank[l[i].Source] < rank[l[j].Source]
		}
		return l[i].Name < l[j].Name
	})
}

func (server *Server) readSkill(realHome string, in extRequest, g GenieSettings) (map[string]interface{}, int, string, string) {
	if !skillNamePattern.MatchString(in.Name) {
		return nil, http.StatusBadRequest, "bad_name", "That is not a skill name."
	}
	switch in.Source {
	case "builtin":
		if body, info, ok := builtinSkillText(in.Name); ok {
			return map[string]interface{}{"skill": info, "text": body}, 0, "", ""
		}
	case "site":
		for _, id := range g.SitePlugins {
			if cp, ok := catalogueByID(id); ok {
				for _, s := range cp.Skills {
					if info, body, err := parseSkill(s); err == nil && info.Name == in.Name {
						info.Source, info.Plugin = "site", cp.Name
						return map[string]interface{}{"skill": info, "text": body}, 0, "", ""
					}
				}
			}
		}
	case "workspace", "plugin":
		skillsDir, pluginsDir, err := extDirs(realHome, false)
		if err != nil {
			return nil, http.StatusInternalServerError, "folder", "The skills folder could not be used."
		}
		path := filepath.Join(skillsDir, in.Name+".md")
		rel := ".genie/skills/" + in.Name + ".md"
		if in.Source == "plugin" {
			if !pluginNamePattern.MatchString(in.ID) {
				return nil, http.StatusBadRequest, "bad_name", "That is not a plugin name."
			}
			path = filepath.Join(pluginsDir, in.ID, "skills", in.Name+".md")
			rel = ".genie/plugins/" + in.ID + "/skills/" + in.Name + ".md"
		}
		if b, ok := plainFile(path, maxSkillBytes+1024); ok {
			if info, body, perr := parseSkill(string(b)); perr == nil {
				info.Source, info.Path = in.Source, rel
				if in.Source == "plugin" {
					info.Plugin = in.ID
				}
				return map[string]interface{}{"skill": info, "text": body}, 0, "", ""
			} else {
				return nil, http.StatusBadRequest, "bad_skill", "This skill cannot be read: " + perr.Error() + "."
			}
		}
	}
	return nil, http.StatusNotFound, "no_skill", "There is no such skill."
}

// logExt writes one line for an install from a link: who and where from, never the text.
func logExt(format string, a ...interface{}) { log.Printf("genie ext: "+format, a...) }
