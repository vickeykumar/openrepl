package server

import (
	"encoding/json"
	"fmt"
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

// Chat history in the workspace (docs/lld/19-genie-agent.md, section 9). Each chat of the Genie panel is one JSON Lines
// file, .genie/chats/<id>.jsonl, in the user's own workspace; .genie/tabs.json says which chats are open as tabs. A line
// is one thing that happened (the title, the model, a message, an answer, a task) and is only ever added, so reading is
// reading the lines in order and the last "title" and "state" win. A line that cannot be read (a write that was cut
// short) is skipped.
//
//	POST /genie_chats {"op":"state"}                          the chats, the open tabs and the limits
//	POST /genie_chats {"op":"read","id":"k3x9"}                the lines of one chat
//	POST /genie_chats {"op":"append","id":"k3x9","lines":[…]}  add lines (the first line of a new chat is its "meta")
//	POST /genie_chats {"op":"tabs","open":["k3x9"]}            which chats are open
//	POST /genie_chats {"op":"delete","id":"k3x9"} / {"op":"deleteAll"}
//	POST /genie_chats {"op":"export","id":"k3x9"} / {"op":"exportAll"}    Markdown
//	POST /genie_chats {"op":"off","off":true}                  stop (or start) saving for this person
//
// Only the owner of a workspace reaches it (a shared session's viewer names the owner's workspace and is refused). It is
// execution-bound in the gateway like agent_files, because the workspace is on the node that runs it.

const (
	chatsDirName       = ".genie"
	chatsSubdir        = "chats"
	chatsTabsFile      = "tabs.json"
	defaultChatsMax    = 50
	defaultChatsTabs   = 8
	defaultChatsKB     = 200
	maxChatsMaxSetting = 200
	maxChatsTabsSet    = 20
	maxChatsKBSetting  = 2000
	maxChatLineChars   = 64 << 10 // one message
	maxChatsAppend     = 20       // lines in one call
	chatsPerMinute     = 240
	maxChatsBody       = 1 << 20
)

var chatIDPattern = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

// the kinds of line, and the keys a line may have (anything else is left out when it is stored)
var chatLineKinds = map[string]bool{"meta": true, "title": true, "state": true, "user": true, "answer": true, "task": true, "summary": true, "checkpoint": true, "star": true}
var chatLineKeys = map[string]bool{"t": true, "id": true, "created": true, "context": true, "language": true, "file": true, "text": true, "at": true, "ts": true,
	"model": true, "effort": true, "mode": true, "tokens": true, "steps": true, "checked": true, "changed": true, "outcome": true, "on": true}

func (g GenieSettings) chatsMax() int  { return orDefault(g.ChatHistoryChats, defaultChatsMax) }
func (g GenieSettings) chatsTabs() int { return orDefault(g.ChatHistoryTabs, defaultChatsTabs) }
func (g GenieSettings) chatsBytes() int {
	return orDefault(g.ChatHistoryKB, defaultChatsKB) * 1024
}

// normalizeChatHistory checks the numbers of the chat history settings.
func (g *GenieSettings) normalizeChatHistory() error {
	for _, n := range []struct {
		name     string
		v        int
		min, max int
	}{
		{"The most chats a workspace keeps", g.ChatHistoryChats, 1, maxChatsMaxSetting},
		{"The most open chat tabs", g.ChatHistoryTabs, 1, maxChatsTabsSet},
		{"The most size of one chat (KB)", g.ChatHistoryKB, 20, maxChatsKBSetting},
		{"The days chats are kept", g.ChatHistoryDays, 0, 3650},
	} {
		if n.v != 0 && (n.v < n.min || n.v > n.max) {
			return fmt.Errorf("%s must be between %d and %d (or empty for the built-in value)", n.name, n.min, n.max)
		}
	}
	return nil
}

// chatLine is a line as it is kept: the keys that are allowed, with strings and numbers only.
func cleanChatLine(raw map[string]interface{}) (map[string]interface{}, bool) {
	kind, _ := raw["t"].(string)
	if !chatLineKinds[kind] {
		return nil, false
	}
	out := map[string]interface{}{}
	for k, v := range raw {
		if !chatLineKeys[k] {
			continue
		}
		switch x := v.(type) {
		case string:
			if len(x) > maxChatLineChars || !utf8.ValidString(x) || strings.ContainsRune(x, 0) {
				return nil, false
			}
			out[k] = x
		case float64:
			out[k] = x
		}
	}
	if kind == "meta" {
		id, _ := out["id"].(string)
		if !chatIDPattern.MatchString(id) {
			return nil, false
		}
	}
	return out, true
}

// maxStarredChats is how many chats can be starred (a star line with on 1 keeps a chat first in History and out of Delete all).
const maxStarredChats = 20

type chatSummary struct {
	Starred  bool   `json:"starred,omitempty"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Created  string `json:"created,omitempty"`
	Updated  int64  `json:"updated"`
	Messages int    `json:"messages"`
	Context  string `json:"context,omitempty"`
	Language string `json:"language,omitempty"`
	Size     int64  `json:"size"`
}

type chatTabs struct {
	Open []string `json:"open"`
	Off  bool     `json:"off,omitempty"`
	V    int      `json:"v"`
}

// chatsFolder is where the chats are: made when asked, and never reached through a link out of the workspace (the folder
// and its chats folder must be folders of the workspace itself, which is checked before anything is made).
func chatsFolder(realHome string, create bool) (string, error) {
	dir := filepath.Join(realHome, chatsDirName)
	for _, d := range []string{dir, filepath.Join(dir, chatsSubdir)} {
		if st, err := os.Lstat(d); err == nil && !st.IsDir() {
			return "", fmt.Errorf("%s is not a folder", d)
		}
	}
	if create {
		if err := os.MkdirAll(filepath.Join(dir, chatsSubdir), 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func readTabs(dir string) chatTabs {
	t := chatTabs{Open: []string{}, V: 1}
	if b, err := os.ReadFile(filepath.Join(dir, chatsTabsFile)); err == nil {
		json.Unmarshal(b, &t)
	}
	if t.Open == nil {
		t.Open = []string{}
	}
	return t
}

// writeFileAtomic writes the whole file and renames it into place, so that a crash leaves the old one.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeTabs(dir string, t chatTabs) error {
	t.V = 1
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, chatsTabsFile), b)
}

// chatLines reads the lines of a chat file, leaving out one that cannot be read.
func chatLines(path string) ([]map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal([]byte(l), &m) != nil {
			continue // a line cut short by a crash
		}
		out = append(out, m)
	}
	return out, nil
}

func summaryOf(path, id string) (chatSummary, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return chatSummary{}, false
	}
	lines, err := chatLines(path)
	if err != nil {
		return chatSummary{}, false
	}
	s := chatSummary{ID: id, Updated: st.ModTime().Unix(), Size: st.Size()}
	for _, l := range lines {
		switch l["t"] {
		case "meta":
			s.Created, _ = l["created"].(string)
			s.Context, _ = l["context"].(string)
		case "title":
			if t, ok := l["text"].(string); ok && strings.TrimSpace(t) != "" {
				s.Title = t
			}
		case "star":
			on, _ := l["on"].(float64)
			s.Starred = on == 1
		case "user", "answer":
			s.Messages++
			if l["t"] == "user" {
				if lang, ok := l["language"].(string); ok && lang != "" {
					s.Language = lang
				}
			}
		}
	}
	return s, true
}

func listChats(dir string) []chatSummary {
	entries, _ := os.ReadDir(filepath.Join(dir, chatsSubdir))
	out := []chatSummary{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if !chatIDPattern.MatchString(id) {
			continue
		}
		if s, ok := summaryOf(filepath.Join(dir, chatsSubdir, name), id); ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

// starredElsewhere counts the starred chats other than the one given.
func starredElsewhere(dir, except string) int {
	n := 0
	for _, c := range listChats(dir) {
		if c.Starred && c.ID != except {
			n++
		}
	}
	return n
}

// pruneChats removes the chats past their age, and the oldest chats that are not open while there are more than max.
func pruneChats(dir string, g GenieSettings, tabs chatTabs, now time.Time) {
	open := map[string]bool{}
	for _, id := range tabs.Open {
		open[id] = true
	}
	chats := listChats(dir)
	days := g.ChatHistoryDays
	keep := []chatSummary{}
	for _, c := range chats {
		if days > 0 && !open[c.ID] && !c.Starred && now.Sub(time.Unix(c.Updated, 0)) > time.Duration(days)*24*time.Hour {
			os.Remove(filepath.Join(dir, chatsSubdir, c.ID+".jsonl"))
			continue
		}
		keep = append(keep, c)
	}
	// newest first: from the end, the oldest closed chats go
	for i := len(keep) - 1; i >= 0 && len(keep) > g.chatsMax(); i-- {
		if open[keep[i].ID] || keep[i].Starred { // an open chat and a starred one stay
			continue
		}
		os.Remove(filepath.Join(dir, chatsSubdir, keep[i].ID+".jsonl"))
		keep = append(keep[:i], keep[i+1:]...)
	}
}

// encodeChatLines is the lines of a chat as the file holds them: one JSON object a line.
func encodeChatLines(ls []map[string]interface{}) []byte {
	var b []byte
	for _, l := range ls {
		b = append(append(b, asciiJSON(l)...), '\n')
	}
	return b
}

// trimChat rewrites a chat that is over its size without its oldest messages, keeping its meta, title and state.
func trimChat(path string, limit int) error {
	lines, err := chatLines(path)
	if err != nil {
		return err
	}
	var head, body []map[string]interface{}
	for _, l := range lines {
		switch l["t"] {
		case "meta", "title", "state", "star":
			head = append(head, l)
		default:
			body = append(body, l)
		}
	}
	enc := func(ls []map[string]interface{}) []byte {
		var b []byte
		for _, l := range ls {
			j := asciiJSON(l)
			b = append(append(b, j...), '\n')
		}
		return b
	}
	for len(body) > 1 && len(enc(head))+len(enc(body)) > limit*9/10 {
		body = body[1:]
	}
	return writeFileAtomic(path, append(enc(head), enc(body)...))
}

// chatMarkdown is a chat as a text to read.
func chatMarkdown(lines []map[string]interface{}) string {
	var b strings.Builder
	title := "Genie chat"
	for _, l := range lines {
		if l["t"] == "title" {
			if t, ok := l["text"].(string); ok && t != "" {
				title = t
			}
		}
	}
	fmt.Fprintf(&b, "# %s\n\n", strings.ReplaceAll(title, "\n", " "))
	for _, l := range lines {
		text, _ := l["text"].(string)
		at, _ := l["at"].(string)
		switch l["t"] {
		case "user":
			fmt.Fprintf(&b, "**You** %s\n\n%s\n\n", at, text)
		case "answer":
			model, _ := l["model"].(string)
			fmt.Fprintf(&b, "**Genie** %s %s\n\n%s\n\n", at, model, text)
		case "task":
			fmt.Fprintf(&b, "**Agent task** %s: %s", at, text)
			if s, ok := l["steps"].(float64); ok {
				fmt.Fprintf(&b, " (%d steps", int(s))
				if c, ok := l["checked"].(string); ok && c != "" {
					fmt.Fprintf(&b, ", checked with %s", c)
				}
				if c, ok := l["changed"].(string); ok && c != "" {
					fmt.Fprintf(&b, ", changed %s", c)
				}
				b.WriteString(")")
			}
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

type chatsRequest struct {
	Op    string                   `json:"op"`
	ID    string                   `json:"id"`
	Lines []map[string]interface{} `json:"lines"`
	Open  []string                 `json:"open"`
	Off   bool                     `json:"off"`
	// IncludeStarred makes deleteAll remove the starred chats as well
	IncludeStarred bool `json:"includeStarred"`
}

// handleGenieChats answers POST /genie_chats.
func (server *Server) handleGenieChats(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	g := GetSiteSettings().Genie
	if g.Disabled || g.ChatHistoryOff {
		jsonError(rw, http.StatusForbidden, "off", "Chat history is switched off on this site.")
		return
	}
	req.ParseForm()
	// a collaborator of a shared session names the owner's workspace: only the owner reaches the chats in it
	if homeOverridden(req) {
		jsonError(rw, http.StatusForbidden, "not_owner", "Only the owner of a workspace has chats in it.")
		return
	}
	uid, home := server.requestIdentity(rw, req)
	if !cookie.Is_UserLoggedIn(req) && g.ChatHistoryNoGuests {
		jsonError(rw, http.StatusForbidden, "guests_off", "Chat history is saved for signed-in users only on this site.")
		return
	}
	var in chatsRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, maxChatsBody)).Decode(&in); err != nil {
		jsonError(rw, http.StatusBadRequest, "bad_request", "send {\"op\": ...}")
		return
	}
	who := uid
	if who == "" {
		who = home
	}
	if !chatsAllowed(who, time.Now()) {
		jsonError(rw, http.StatusTooManyRequests, "rate", "Too many requests in a minute: wait a little.")
		return
	}
	os.MkdirAll(home, 0o755)
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		jsonError(rw, http.StatusNotFound, "no_workspace", "The workspace is not there yet.")
		return
	}
	writes := in.Op == "append" || in.Op == "tabs" || in.Op == "off"
	dir, err := chatsFolder(realHome, writes)
	if err != nil {
		jsonError(rw, http.StatusInternalServerError, "folder", "The chat folder could not be used.")
		return
	}
	reply := func(v interface{}) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(v)
	}
	chatPath := func(id string) (string, bool) {
		if !chatIDPattern.MatchString(id) {
			return "", false
		}
		return filepath.Join(dir, chatsSubdir, id+".jsonl"), true
	}
	switch in.Op {
	case "state":
		tabs := readTabs(dir)
		pruneChats(dir, g, tabs, time.Now())
		chats := listChats(dir)
		have := map[string]bool{}
		for _, c := range chats {
			have[c.ID] = true
		}
		open := []string{}
		for _, id := range tabs.Open {
			if have[id] && len(open) < g.chatsTabs() {
				open = append(open, id)
			}
		}
		var size int64
		for _, c := range chats {
			size += c.Size
		}
		reply(map[string]interface{}{"enabled": true, "off": tabs.Off, "open": open, "chats": chats, "bytes": size,
			"limits": map[string]int{"chats": g.chatsMax(), "tabs": g.chatsTabs(), "bytes": g.chatsBytes()}})
	case "read":
		p, ok := chatPath(in.ID)
		if !ok {
			jsonError(rw, http.StatusBadRequest, "bad_id", "That is not a chat id.")
			return
		}
		lines, err := chatLines(p)
		if err != nil {
			jsonError(rw, http.StatusNotFound, "no_chat", "There is no such chat.")
			return
		}
		reply(map[string]interface{}{"id": in.ID, "lines": lines})
	case "append":
		if readTabs(dir).Off {
			jsonError(rw, http.StatusConflict, "saving_off", "Saving chats is switched off for this person.")
			return
		}
		p, ok := chatPath(in.ID)
		if !ok || len(in.Lines) == 0 || len(in.Lines) > maxChatsAppend {
			jsonError(rw, http.StatusBadRequest, "bad_request", "A chat id and up to 20 lines.")
			return
		}
		_, statErr := os.Stat(p)
		isNew := os.IsNotExist(statErr)
		var data []byte
		var written []map[string]interface{} // the lines, cleaned, for a checkpoint that replaces the last one
		hasCheckpoint := false
		for i, raw := range in.Lines {
			l, ok := cleanChatLine(raw)
			if !ok {
				jsonError(rw, http.StatusBadRequest, "bad_line", "A line of the chat is not allowed.")
				return
			}
			if isNew && i == 0 {
				if l["t"] != "meta" || l["id"] != in.ID {
					jsonError(rw, http.StatusBadRequest, "bad_line", "A new chat starts with its meta line.")
					return
				}
			}
			if l["t"] == "star" {
				if on, _ := l["on"].(float64); on == 1 && starredElsewhere(dir, in.ID) >= maxStarredChats {
					jsonError(rw, http.StatusConflict, "star_limit", "You can star 20 chats. Unstar one first.")
					return
				}
			}
			j := asciiJSON(l)
			data = append(append(data, j...), '\n')
			written = append(written, l)
			hasCheckpoint = hasCheckpoint || l["t"] == "checkpoint"
		}
		if isNew && len(listChats(dir)) >= g.chatsMax() {
			pruneChats(dir, g, readTabs(dir), time.Now()) // the oldest closed chat makes room
		}
		// A checkpoint (the state of a running agent task, for Resume) replaces the one before it: the file keeps one, so a task of
		// many steps does not grow the chat. The file is written whole, atomically, as when it is trimmed.
		var werr error
		if hasCheckpoint && !isNew {
			old, _ := chatLines(p)
			var all []map[string]interface{}
			for _, l := range old {
				if l["t"] != "checkpoint" {
					all = append(all, l)
				}
			}
			// of the checkpoints in this call (a page that was offline sends them together) only the last is kept
			last := -1
			for i, l := range written {
				if l["t"] == "checkpoint" {
					last = i
				}
			}
			for i, l := range written {
				if l["t"] != "checkpoint" || i == last {
					all = append(all, l)
				}
			}
			werr = writeFileAtomic(p, encodeChatLines(all))
		} else {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				jsonError(rw, http.StatusInsufficientStorage, "write", "The chat could not be written. Is the workspace full?")
				return
			}
			_, werr = f.Write(data)
			f.Close()
		}
		if werr != nil {
			jsonError(rw, http.StatusInsufficientStorage, "write", "The chat could not be written. Is the workspace full?")
			return
		}
		st, _ := os.Stat(p)
		trimmed := false
		if st != nil && st.Size() > int64(g.chatsBytes()) {
			trimmed = trimChat(p, g.chatsBytes()) == nil
			st, _ = os.Stat(p)
		}
		var size int64
		if st != nil {
			size = st.Size()
		}
		reply(map[string]interface{}{"ok": true, "size": size, "trimmed": trimmed})
	case "tabs":
		if len(in.Open) > g.chatsTabs() {
			in.Open = in.Open[:g.chatsTabs()]
		}
		open := []string{}
		seen := map[string]bool{}
		for _, id := range in.Open {
			if chatIDPattern.MatchString(id) && !seen[id] {
				seen[id] = true
				open = append(open, id)
			}
		}
		t := readTabs(dir)
		t.Open = open
		if err := writeTabs(dir, t); err != nil {
			jsonError(rw, http.StatusInsufficientStorage, "write", "The tabs could not be written.")
			return
		}
		reply(map[string]interface{}{"ok": true, "open": open})
	case "off":
		t := readTabs(dir)
		t.Off = in.Off
		if err := writeTabs(dir, t); err != nil {
			jsonError(rw, http.StatusInsufficientStorage, "write", "The setting could not be written.")
			return
		}
		reply(map[string]interface{}{"ok": true, "off": t.Off})
	case "delete":
		p, ok := chatPath(in.ID)
		if !ok {
			jsonError(rw, http.StatusBadRequest, "bad_id", "That is not a chat id.")
			return
		}
		os.Remove(p)
		t := readTabs(dir)
		open := []string{}
		for _, id := range t.Open {
			if id != in.ID {
				open = append(open, id)
			}
		}
		if len(open) != len(t.Open) {
			t.Open = open
			writeTabs(dir, t)
		}
		reply(map[string]interface{}{"ok": true})
	case "deleteAll":
		// the starred chats stay unless the person asked for them too
		kept := map[string]bool{}
		if !in.IncludeStarred {
			for _, c := range listChats(dir) {
				if c.Starred {
					kept[c.ID] = true
				}
			}
		}
		if len(kept) == 0 {
			os.RemoveAll(filepath.Join(dir, chatsSubdir))
		} else {
			for _, c := range listChats(dir) {
				if !kept[c.ID] {
					os.Remove(filepath.Join(dir, chatsSubdir, c.ID+".jsonl"))
				}
			}
		}
		os.Remove(filepath.Join(dir, chatsTabsFile+".tmp"))
		t := readTabs(dir)
		open := []string{}
		for _, id := range t.Open {
			if kept[id] {
				open = append(open, id)
			}
		}
		t.Open = open
		if _, err := os.Stat(dir); err == nil {
			writeTabs(dir, t)
		}
		reply(map[string]interface{}{"ok": true, "kept": len(kept)})
	case "export":
		p, ok := chatPath(in.ID)
		if !ok {
			jsonError(rw, http.StatusBadRequest, "bad_id", "That is not a chat id.")
			return
		}
		lines, err := chatLines(p)
		if err != nil {
			jsonError(rw, http.StatusNotFound, "no_chat", "There is no such chat.")
			return
		}
		reply(map[string]interface{}{"markdown": chatMarkdown(lines)})
	case "exportAll":
		var b strings.Builder
		chats := listChats(dir)
		for i := len(chats) - 1; i >= 0; i-- {
			if lines, err := chatLines(filepath.Join(dir, chatsSubdir, chats[i].ID+".jsonl")); err == nil {
				b.WriteString(chatMarkdown(lines))
				b.WriteString("\n---\n\n")
			}
		}
		reply(map[string]interface{}{"markdown": b.String()})
	default:
		jsonError(rw, http.StatusBadRequest, "bad_request", "op has to be state, read, append, tabs, off, delete, deleteAll, export or exportAll")
	}
}

// a person's calls in the last minute
var (
	chatsRateMu sync.Mutex
	chatsRate   = map[string][]time.Time{}
)

func chatsAllowed(who string, now time.Time) bool {
	chatsRateMu.Lock()
	defer chatsRateMu.Unlock()
	recent := chatsRate[who][:0]
	for _, t := range chatsRate[who] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= chatsPerMinute {
		chatsRate[who] = recent
		return false
	}
	chatsRate[who] = append(recent, now)
	return true
}

// jsonError is an error the page can read: {"error":{"code":"..","message":".."}}
func jsonError(rw http.ResponseWriter, status int, code, message string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}

// asciiJSON is json.Marshal with every character above 127 written as \uXXXX (a pair for those past the first plane), so
// a chat file is plain ASCII: an editor that does not read it as UTF-8 shows the text right, and nothing is garbled.
// Reading it back gives the same text.
func asciiJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	out := make([]byte, 0, len(b))
	for _, r := range string(b) {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r < 0x10000:
			out = append(out, fmt.Sprintf("\\u%04x", r)...)
		default:
			r -= 0x10000
			out = append(out, fmt.Sprintf("\\u%04x\\u%04x", 0xd800+(r>>10), 0xdc00+(r&0x3ff))...)
		}
	}
	return out
}
