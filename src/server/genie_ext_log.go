package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"utils"
)

// The plugin installs: who put which plugin in a workspace, from where, and when, so that an admin can see what is in use and where a plugin
// from a link came from. One JSON object per line in a file for each day (so that keeping it N days is deleting files), in the server's log folder.
// What is held: the time, the user's account id, install / update / remove, the plugin's name and version, where it came from (the catalogue, or
// the link in full) and the digest of what was installed. Never the text of a skill or anything of the workspace.

var pluginLogDir = utils.LOG_PATH + "/genie-plugins"

const (
	pluginLogKeepDays = 90
	pluginLogDayMax   = 5 << 20
)

var pluginLogMu sync.Mutex

type pluginInstallEntry struct {
	T       string `json:"t"`
	User    string `json:"user"`
	Op      string `json:"op"` // install, update, remove
	Plugin  string `json:"plugin"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"` // catalogue:<id> or link:<url>
	Digest  string `json:"digest,omitempty"`
	Skills  int    `json:"skills,omitempty"`
	Notes   int    `json:"notes,omitempty"`
}

func clean1Line(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return clipRunes(strings.TrimSpace(s), n)
}

// logPluginInstall adds one line. A failure to write is only logged: an install does not fail because the record could not be kept.
func logPluginInstall(e pluginInstallEntry, now time.Time) {
	e.T = now.UTC().Format(time.RFC3339)
	e.User, e.Plugin, e.Version, e.Source = clean1Line(e.User, 80), clean1Line(e.Plugin, 60), clean1Line(e.Version, 20), clean1Line(e.Source, 320)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	pluginLogMu.Lock()
	defer pluginLogMu.Unlock()
	if err := os.MkdirAll(pluginLogDir, 0o755); err != nil {
		logExt("the plugin log folder could not be made: %v", err)
		return
	}
	name := filepath.Join(pluginLogDir, now.UTC().Format("2006-01-02")+".jsonl")
	if st, err := os.Stat(name); err == nil && st.Size() > pluginLogDayMax {
		return
	}
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logExt("the plugin log could not be written: %v", err)
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
	prunePluginLog(now)
}

// prunePluginLog deletes the files older than the days that are kept.
func prunePluginLog(now time.Time) {
	entries, err := os.ReadDir(pluginLogDir)
	if err != nil {
		return
	}
	cut := now.UTC().AddDate(0, 0, -pluginLogKeepDays).Format("2006-01-02")
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".jsonl")
		if strings.HasSuffix(e.Name(), ".jsonl") && n < cut {
			os.Remove(filepath.Join(pluginLogDir, e.Name()))
		}
	}
}

// recentPluginInstalls is the newest entries first, at most n.
func recentPluginInstalls(n int) []pluginInstallEntry {
	pluginLogMu.Lock()
	defer pluginLogMu.Unlock()
	entries, err := os.ReadDir(pluginLogDir)
	out := []pluginInstallEntry{}
	if err != nil {
		return out
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		f, err := os.Open(filepath.Join(pluginLogDir, name))
		if err != nil {
			continue
		}
		var day []pluginInstallEntry
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e pluginInstallEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Plugin != "" {
				day = append(day, e)
			}
		}
		f.Close()
		for i := len(day) - 1; i >= 0 && len(out) < n; i-- {
			out = append(out, day[i])
		}
		if len(out) >= n {
			break
		}
	}
	return out
}

// handleAdminPluginInstalls answers GET admin/plugin-installs: the newest installs, updates and removals, and how many workspaces-worth of each plugin.
func (server *Server) handleAdminPluginInstalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		adminError(w, http.StatusMethodNotAllowed, "Use GET.")
		return
	}
	list := recentPluginInstalls(200)
	// a plugin installed and not removed since, by how many people (counted over the entries that are kept)
	state := map[string]map[string]bool{}
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		if state[e.Plugin] == nil {
			state[e.Plugin] = map[string]bool{}
		}
		state[e.Plugin][e.User] = e.Op != "remove"
	}
	type count struct {
		Plugin string `json:"plugin"`
		Users  int    `json:"users"`
	}
	counts := []count{}
	for p, users := range state {
		n := 0
		for _, on := range users {
			if on {
				n++
			}
		}
		counts = append(counts, count{p, n})
	}
	sort.Slice(counts, func(i, j int) bool {
		return counts[i].Users > counts[j].Users || (counts[i].Users == counts[j].Users && counts[i].Plugin < counts[j].Plugin)
	})
	adminJSON(w, http.StatusOK, map[string]interface{}{"entries": list, "counts": counts, "days": pluginLogKeepDays, "note": fmt.Sprintf("The last %d days. A user is the account id: the Users view has the email.", pluginLogKeepDays)})
}
