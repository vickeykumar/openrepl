package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// The agent's read-only actions files_search and files_read: the page of the owner asks
// the server to look in the user's workspace, and gets lines back. Nothing is changed, so
// the page asks the user nothing. The server keeps the limits: only text files, never a
// hidden name or a path out of the workspace, and a few thousand files and a few seconds
// at the most.
//
//	POST /agent_files {"op":"search","query":"login","path":"src"}
//	POST /agent_files {"op":"read","path":"src/auth.py","start_line":30,"end_line":70}

const (
	filesMaxQuery     = 200
	filesMaxMatches   = 40
	filesMaxLineChars = 160
	filesMaxScanFiles = 5000
	filesMaxFileBytes = 1 << 20
	filesSearchBudget = 4 * time.Second
	filesReadChars    = 3600 // a read returns as many lines as fit in this
	filesReadMaxLines = 400
	filesPerMinute    = 120
)

// directories that are never searched: other people's code and caches
var filesSkipDirs = map[string]bool{"node_modules": true, "__pycache__": true, "vendor": true, "target": true, "venv": true, "site-packages": true}

type filesMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type filesReply struct {
	Matches   []filesMatch `json:"matches,omitempty"`
	Files     int          `json:"files_scanned,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
	// read
	Path       string `json:"path,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
	Text       string `json:"text,omitempty"`
	Next       int    `json:"next_line,omitempty"` // where to go on when the read stopped early
}

// plainRel is a path relative to the workspace: no "..", no absolute path, no hidden name.
func plainRel(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return "", true
	}
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || len(p) > 400 || !utf8.ValidString(p) {
		return "", false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") || len(seg) > 100 {
			return "", false
		}
	}
	return filepath.FromSlash(strings.TrimSuffix(p, "/")), true
}

// inside says whether a path, with its symbolic links followed, is under the workspace.
func inside(realHome, path string) (string, bool) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realHome, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}

// textFile says whether a file is something to read as text: a regular file, not large, with no zero byte at its start.
func textFile(path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() > filesMaxFileBytes {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	return !strings.ContainsRune(string(head[:n]), 0)
}

func clipLine(s string) string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\t", "    "), "\r\n ")
	if len(s) > filesMaxLineChars {
		return strings.ToValidUTF8(s[:filesMaxLineChars], "") + "..."
	}
	return strings.ToValidUTF8(s, "")
}

// searchFiles looks for the text, without regard to case, in the text files under dir.
func searchFiles(realHome, dir, query string, now func() time.Time) filesReply {
	var out filesReply
	needle := strings.ToLower(query)
	deadline := now().Add(filesSearchBudget)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if path != dir && strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if filesSkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if out.Files >= filesMaxScanFiles || now().After(deadline) {
			out.Truncated = true
			return filepath.SkipAll
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, ok := inside(realHome, path); !ok {
				return nil
			}
			if st, err := os.Stat(path); err != nil {
				return nil
			} else {
				info = st
			}
		}
		if !textFile(path, info) {
			return nil
		}
		out.Files++
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(realHome, path)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for n := 1; sc.Scan(); n++ {
			if strings.Contains(strings.ToLower(sc.Text()), needle) {
				out.Matches = append(out.Matches, filesMatch{File: filepath.ToSlash(rel), Line: n, Text: clipLine(sc.Text())})
				if len(out.Matches) >= filesMaxMatches {
					out.Truncated = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return out
}

// readLines returns the lines from start to end of a text file, as many as fit in filesReadChars,
// with where to go on when it stopped before the end of the range.
func readLines(path string, start, end int) (filesReply, error) {
	f, err := os.Open(path)
	if err != nil {
		return filesReply{}, err
	}
	defer f.Close()
	if end < start || end-start+1 > filesReadMaxLines {
		end = start + filesReadMaxLines - 1
	}
	out := filesReply{StartLine: start}
	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	stopped := false
	for !stopped && sc.Scan() {
		n++
		if n < start {
			continue
		}
		if n > end {
			out.Next, stopped = n, true
			continue
		}
		line := clipLine(sc.Text())
		if b.Len()+len(line)+8 > filesReadChars {
			out.Next, out.Truncated, stopped = n, true, true
			continue
		}
		fmt.Fprintf(&b, "%5d  %s\n", n, line)
		out.EndLine = n
	}
	for sc.Scan() { // the rest is only counted
		n++
	}
	out.TotalLines = n
	out.Text = b.String()
	return out, nil
}

// handleAgentFiles answers POST /agent_files.
func (server *Server) handleAgentFiles(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	uid, home := server.requestIdentity(rw, req)
	if uid == "" {
		http.Error(rw, "Genie can look in the files of signed-in users only.", http.StatusForbidden)
		return
	}
	// a collaborator of a shared session names the owner's workspace: only the owner's own Genie looks in it
	if homeOverridden(req) {
		http.Error(rw, "Only the owner of a workspace can have Genie look in it.", http.StatusForbidden)
		return
	}
	if g := GetSiteSettings().Genie; g.Disabled || g.AgentDisabled {
		http.Error(rw, "Agent mode is switched off on this site.", http.StatusForbidden)
		return
	}
	var in struct {
		Op        string `json:"op"`
		Query     string `json:"query"`
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, 8<<10)).Decode(&in); err != nil {
		http.Error(rw, "send {\"op\": \"search\" or \"read\", ...}", http.StatusBadRequest)
		return
	}
	if !shellAllowed("files:"+uid, time.Now()) {
		http.Error(rw, "Too many requests in a minute: wait a little.", http.StatusTooManyRequests)
		return
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		http.Error(rw, "The workspace is not there yet.", http.StatusNotFound)
		return
	}
	rel, ok := plainRel(in.Path)
	if !ok {
		http.Error(rw, "The path has to be relative to the workspace, with no hidden name.", http.StatusBadRequest)
		return
	}
	target, inWorkspace := inside(realHome, filepath.Join(realHome, rel))
	if !inWorkspace {
		http.Error(rw, "There is no such path in the workspace.", http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	switch in.Op {
	case "search":
		if strings.TrimSpace(in.Query) == "" || len(in.Query) > filesMaxQuery || strings.ContainsAny(in.Query, "\x00\r\n") || !utf8.ValidString(in.Query) {
			http.Error(rw, "The query has to be one line of text of at most 200 characters.", http.StatusBadRequest)
			return
		}
		if st, err := os.Stat(target); err != nil || !st.IsDir() {
			http.Error(rw, "The path is not a folder.", http.StatusBadRequest)
			return
		}
		json.NewEncoder(rw).Encode(searchFiles(realHome, target, strings.TrimSpace(in.Query), time.Now))
	case "read":
		st, err := os.Stat(target)
		if err != nil || st.IsDir() {
			http.Error(rw, "There is no such file in the workspace.", http.StatusNotFound)
			return
		}
		if !textFile(target, st) {
			http.Error(rw, "That file is not text, or it is larger than 1 MB.", http.StatusUnprocessableEntity)
			return
		}
		start := in.StartLine
		if start < 1 {
			start = 1
		}
		end := in.EndLine
		if end < start {
			end = start + filesReadMaxLines - 1
		}
		res, err := readLines(target, start, end)
		if err != nil {
			http.Error(rw, "The file could not be read.", http.StatusInternalServerError)
			return
		}
		res.Path = filepath.ToSlash(rel)
		json.NewEncoder(rw).Encode(res)
	default:
		http.Error(rw, "op has to be search or read", http.StatusBadRequest)
	}
}
