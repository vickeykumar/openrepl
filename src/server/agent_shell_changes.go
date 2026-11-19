package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// What a shell command of the agent changed in the workspace, for "Review all changes" (canvas "Genie: top five improvements", board 6,
// LLD 19 section 12): the workspace is looked at before and after the command, and the files that are new, changed or gone are told to the
// page with the text of the small ones, so that the page can show the lines and offer to put a file back. A file is put back by
// POST agent_restore, which writes the text only if the file is still as the command left it.
//
// Limits: hidden names, caches and the Trash folder are not looked at; a workspace of more than shellSnapMaxFiles files is not tracked
// (the page is told); only text files of at most shellSnapFileBytes are kept as text, shellSnapTotalBytes in all; at most
// shellChangesMax changes are told.

const (
	shellSnapMaxFiles   = 4000
	shellSnapFileBytes  = 32 << 10
	shellSnapTotalBytes = 768 << 10
	shellChangesMax     = 40
	restoreMaxText      = 64 << 10
)

type snapFile struct {
	size int64
	mod  int64
	text *string // the text, when the file is small text and the total allowed it
}

type shellChange struct {
	Path   string  `json:"path"`
	Kind   string  `json:"kind"` // new, changed or deleted
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
	Size   int64   `json:"size"`
	Undo   bool    `json:"undo"` // there is what is needed to put it back
}

func smallText(b []byte) bool {
	return len(b) <= shellSnapFileBytes && utf8.Valid(b) && !strings.ContainsRune(string(b), 0)
}

// snapshotWorkspace lists the files of a workspace with their size and time, and keeps the text of the small ones. ok is false when the
// workspace has too many files to follow.
func snapshotWorkspace(realHome string) (snap map[string]snapFile, ok bool) {
	snap = map[string]snapFile{}
	ok = true
	filepath.WalkDir(realHome, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if path != realHome && (strings.HasPrefix(name, ".") || (d.IsDir() && (filesSkipDirs[name] || name == TrashDirName))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if len(snap) >= shellSnapMaxFiles {
			ok = false
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(realHome, path)
		if err != nil {
			return nil
		}
		snap[filepath.ToSlash(rel)] = snapFile{size: info.Size(), mod: info.ModTime().UnixNano()}
		return nil
	})
	if !ok {
		return snap, false
	}
	// the text of the small files, the most recently changed first, until the total is reached
	var names []string
	for n, f := range snap {
		if f.size <= shellSnapFileBytes {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if snap[names[i]].mod != snap[names[j]].mod {
			return snap[names[i]].mod > snap[names[j]].mod
		}
		return names[i] < names[j]
	})
	total := 0
	for _, n := range names {
		if total >= shellSnapTotalBytes {
			break
		}
		b, err := os.ReadFile(filepath.Join(realHome, filepath.FromSlash(n)))
		if err != nil || !smallText(b) {
			continue
		}
		t := string(b)
		f := snap[n]
		f.text = &t
		snap[n] = f
		total += len(b)
	}
	return snap, true
}

// diffWorkspace compares the workspace now with a snapshot of before: the files that are new, changed (by text when it is known, else by size
// and time) or gone, with the text that is kept. more says how many changes were left out of the list.
func diffWorkspace(realHome string, before map[string]snapFile) (changes []shellChange, more int) {
	after, ok := snapshotWorkspace(realHome)
	if !ok {
		return nil, 0
	}
	var all []shellChange
	for p, a := range after {
		b, was := before[p]
		switch {
		case !was:
			c := shellChange{Path: p, Kind: "new", Size: a.size, After: a.text, Undo: true}
			all = append(all, c)
		case b.size != a.size || b.mod != a.mod:
			if b.text != nil && a.text != nil && *b.text == *a.text {
				continue // touched, not changed
			}
			all = append(all, shellChange{Path: p, Kind: "changed", Size: a.size, Before: b.text, After: a.text, Undo: b.text != nil && a.text != nil})
		}
	}
	for p, b := range before {
		if _, still := after[p]; !still {
			all = append(all, shellChange{Path: p, Kind: "deleted", Size: b.size, Before: b.text, Undo: b.text != nil})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	if len(all) > shellChangesMax {
		more = len(all) - shellChangesMax
		all = all[:shellChangesMax]
	}
	return all, more
}

// TrashDirName is the folder deleted files of the agent go to (the page's TRASH, agent-protocol.ts).
const TrashDirName = "Trash"

// ---- putting a file back ---------------------------------------------------------------------------------

// POST agent_restore {"path":"a.py","text":"...","expect":"...","must_not_exist":false} writes a file back as it was before a command of
// the agent, and only when the file is still as the command left it: the text it has now is "expect", or it is not there and
// "must_not_exist" says so (a file the command deleted). The path is relative to the workspace with no hidden name, nothing is followed
// out of the workspace, and only text of at most 64 KB is written. 409 says the file is not as expected (the user changed it since).
func (server *Server) handleAgentRestore(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	uid, home := server.requestIdentity(rw, req)
	if uid == "" {
		http.Error(rw, "Genie can put files back for signed-in users only.", http.StatusForbidden)
		return
	}
	if homeOverridden(req) {
		http.Error(rw, "Only the owner of a workspace can have Genie put files back in it.", http.StatusForbidden)
		return
	}
	if g := GetSiteSettings().Genie; g.Disabled || g.AgentDisabled {
		http.Error(rw, "Agent mode is switched off on this site.", http.StatusForbidden)
		return
	}
	var in struct {
		Path         string `json:"path"`
		Text         string `json:"text"`
		Expect       string `json:"expect"`
		MustNotExist bool   `json:"must_not_exist"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, 3*restoreMaxText)).Decode(&in); err != nil {
		http.Error(rw, "send {\"path\", \"text\", \"expect\"}", http.StatusBadRequest)
		return
	}
	if len(in.Text) > restoreMaxText || len(in.Expect) > restoreMaxText || !utf8.ValidString(in.Text) || strings.ContainsRune(in.Text, 0) {
		http.Error(rw, "The text has to be text of at most 64 KB.", http.StatusBadRequest)
		return
	}
	if !shellAllowed("restore:"+uid, time.Now()) {
		http.Error(rw, "Too many requests in a minute: wait a little.", http.StatusTooManyRequests)
		return
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		http.Error(rw, "The workspace is not there yet.", http.StatusNotFound)
		return
	}
	rel, ok := plainRel(in.Path)
	if !ok || rel == "" {
		http.Error(rw, "The path has to be a file relative to the workspace, with no hidden name.", http.StatusBadRequest)
		return
	}
	abs := filepath.Join(realHome, rel)
	// the nearest folder that is there must be inside the workspace (a link out of it is not followed)
	anc := filepath.Dir(abs)
	for {
		if _, err := os.Stat(anc); err == nil || anc == realHome {
			break
		}
		anc = filepath.Dir(anc)
	}
	if _, in := inside(realHome, anc); !in {
		http.Error(rw, "There is no such place in the workspace.", http.StatusNotFound)
		return
	}
	if st, err := os.Lstat(abs); err == nil {
		if !st.Mode().IsRegular() {
			http.Error(rw, "That is not a plain file.", http.StatusBadRequest)
			return
		}
		if in.MustNotExist {
			http.Error(rw, "The file is there again: it was not put back.", http.StatusConflict)
			return
		}
		cur, err := os.ReadFile(abs)
		if err != nil || int64(len(cur)) > 2*restoreMaxText || string(cur) != in.Expect {
			http.Error(rw, "The file has changed since: it was not put back.", http.StatusConflict)
			return
		}
	} else if !in.MustNotExist {
		http.Error(rw, "The file is gone: it was not put back.", http.StatusConflict)
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		http.Error(rw, "The folder could not be made.", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(abs, []byte(in.Text), 0o644); err != nil {
		http.Error(rw, "The file could not be written.", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]interface{}{"ok": true})
}
