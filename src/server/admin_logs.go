package server

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"utils"
)

// The dashboard's log viewer: the end of this process's log file, with
// anything that looks like a credential masked. The log also holds visitors'
// addresses and the commands of the server itself, which an admin may read,
// but never keys, tokens, cookies or full email addresses.

var logFile = utils.LOG_PATH + "/gotty.log"

const (
	logTailBytes   = 1 << 20 // read at most the last megabyte
	logDefaultRows = 200
	logMaxRows     = 1000
	logMaxLineLen  = 2000
)

var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	// whole headers
	{regexp.MustCompile(`(?i)\b(authorization|cookie|set-cookie|x-openrepl-[a-z-]+)(["']?\s*[:=]\s*).*`), "$1$2[redacted]"},
	// a bearer or basic credential
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [redacted]"},
	// key=value and "key": "value" pairs that name a secret
	{regexp.MustCompile(`(?i)([A-Za-z_-]*(?:key|token|secret|password|passwd|credential|sessionid|session_id)s?["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}\]]+)`), "$1[redacted]"},
	// a JSON web token
	{regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), "[redacted]"},
	// any other long run of token characters
	{regexp.MustCompile(`[A-Za-z0-9+_=-]{40,}`), "[redacted]"},
	// an email address keeps its first letter and its domain
	{regexp.MustCompile(`\b([A-Za-z0-9])[A-Za-z0-9._%+-]*@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,})\b`), "$1***@$2"},
}

// redactLine masks credentials and shortens a runaway line.
func redactLine(line string) string {
	if len(line) > logMaxLineLen {
		line = line[:logMaxLineLen] + " …"
	}
	for _, r := range redactions {
		line = r.re.ReplaceAllString(line, r.with)
	}
	return line
}

// tailLines returns the last n lines of the file at path and whether it began
// partway through a longer file.
func tailLines(path string, n int) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start, partial := int64(0), false
	if fi.Size() > logTailBytes {
		start, partial = fi.Size()-logTailBytes, true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	text := string(data)
	if partial { // the first line is cut off
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if len(lines) > n {
		lines, partial = lines[len(lines)-n:], true
	}
	return lines, partial, nil
}

var errorWords = regexp.MustCompile(`(?i)\b(error|failed|panic|fatal)\b`)

func (server *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	rows := logDefaultRows
	if v, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && v > 0 {
		rows = v
	}
	if rows > logMaxRows {
		rows = logMaxRows
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	errorsOnly := r.URL.Query().Get("level") == "error"

	// Filter before cutting to the last rows, so a search reaches further back.
	lines, partial, err := tailLines(logFile, logTailBytes)
	if err != nil {
		adminJSON(w, http.StatusOK, map[string]interface{}{"lines": []string{}, "note": "No log file yet."})
		return
	}
	out := make([]string, 0, rows)
	more := partial // there is log above what is shown
	for i := len(lines) - 1; i >= 0; i-- {
		if len(out) >= rows {
			more = true
			break
		}
		l := lines[i]
		if errorsOnly && !errorWords.MatchString(l) {
			continue
		}
		l = redactLine(l)
		if query != "" && !strings.Contains(strings.ToLower(l), query) {
			continue
		}
		out = append(out, l)
	}
	// oldest first, as in the file
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	adminJSON(w, http.StatusOK, map[string]interface{}{"lines": out, "truncated": more})
}
