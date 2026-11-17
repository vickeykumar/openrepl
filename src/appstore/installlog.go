package appstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The log of every install (LLD 18, section 13). Each attempt of an app on a node writes a file of JSON lines (time, step, level, text) in
// <store>/logs/<app>/<NNNN>.log, beside an index.json that lists the attempts. The last LogKeep attempts of an app are kept (at most LogMaxBytes each,
// LogMaxAge, the newest always), the text is cleaned of paths and secrets before it is written, and the admin reads it by byte offset (a live tail).
// A node's log is read from its own disk; a gateway whose disk is wiped keeps a one-line summary of every attempt in the catalog database.

var (
	// LogKeep is how many attempts of an app a node keeps.
	LogKeep = 5
	// LogMaxBytes is the most one attempt's log may hold.
	LogMaxBytes int64 = 256 << 10
	// LogMaxAge is how long an attempt's log is kept (the newest of an app is kept whatever its age).
	LogMaxAge  = 14 * 24 * time.Hour
	logMaxLine = 2000
)

// LogLine is one line of the log of an attempt.
type LogLine struct {
	T     int64  `json:"t"` // unix milliseconds
	Step  string `json:"s"` // start, env, order, look up, install, output, command, trim, check, ladder, error, result
	Level string `json:"l"` // info, output, warn, error
	Text  string `json:"x"`
}

// AttemptInfo is one attempt of an app on a node: what the admin's list of attempts shows.
type AttemptInfo struct {
	N       int    `json:"n"`
	App     string `json:"app"`
	Node    string `json:"node,omitempty"`
	Label   string `json:"label"` // install, replay, measure, compare
	Start   int64  `json:"start"` // unix seconds
	End     int64  `json:"end,omitempty"`
	Result  string `json:"result"` // running, installed, failed, stopped
	Version string `json:"version,omitempty"`
	Manager string `json:"manager,omitempty"`
	Err     string `json:"err,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	LogGone bool   `json:"logGone,omitempty"` // only the summary is left (the disk of the node was wiped)
}

// InstallLogs is the logs of one node.
type InstallLogs struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// NewInstallLogs makes the logs of a node under dir (the store's logs folder).
func NewInstallLogs(dir string, now func() time.Time) *InstallLogs {
	if now == nil {
		now = time.Now
	}
	return &InstallLogs{dir: dir, now: now}
}

func (l *InstallLogs) appDir(app string) string {
	if !ValidName(app) {
		return ""
	}
	return filepath.Join(l.dir, app)
}

func (l *InstallLogs) readIndex(app string) []AttemptInfo {
	d := l.appDir(app)
	if d == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(d, "index.json"))
	if err != nil {
		return nil
	}
	var idx struct {
		Attempts []AttemptInfo `json:"attempts"`
	}
	if json.Unmarshal(raw, &idx) != nil {
		return nil
	}
	return idx.Attempts
}

func (l *InstallLogs) writeIndex(app string, at []AttemptInfo) {
	d := l.appDir(app)
	if d == "" {
		return
	}
	raw, _ := json.Marshal(map[string]interface{}{"attempts": at})
	tmp := filepath.Join(d, "index.json.tmp")
	if os.WriteFile(tmp, raw, 0644) == nil {
		os.Rename(tmp, filepath.Join(d, "index.json"))
	}
}

func attemptFile(d string, n int) string { return filepath.Join(d, fmt.Sprintf("%04d.log", n)) }

// Attempts lists the attempts of an app on this node, newest first.
func (l *InstallLogs) Attempts(app string) []AttemptInfo {
	l.mu.Lock()
	at := l.readIndex(app)
	l.mu.Unlock()
	out := make([]AttemptInfo, 0, len(at))
	for i := len(at) - 1; i >= 0; i-- {
		out = append(out, at[i])
	}
	return out
}

// Read gives the text of attempt n (0: the newest) from byte offset from, at most max bytes and only whole lines (unless the file ends), with where
// to go on; info is the attempt, and ok is false when there is no such log.
func (l *InstallLogs) Read(app string, n int, from int64, max int) (data []byte, next int64, info AttemptInfo, ok bool) {
	l.mu.Lock()
	at := l.readIndex(app)
	l.mu.Unlock()
	if len(at) == 0 {
		return nil, 0, AttemptInfo{}, false
	}
	if n <= 0 {
		info = at[len(at)-1]
	} else {
		found := false
		for _, a := range at {
			if a.N == n {
				info, found = a, true
			}
		}
		if !found {
			return nil, 0, AttemptInfo{}, false
		}
	}
	f, err := os.Open(attemptFile(l.appDir(app), info.N))
	if err != nil {
		return nil, 0, info, false
	}
	defer f.Close()
	if max <= 0 || max > 256<<10 {
		max = 128 << 10
	}
	if from < 0 {
		from = 0
	}
	buf := make([]byte, max)
	k, _ := f.ReadAt(buf, from)
	buf = buf[:k]
	if k == max {
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i+1]
		}
	}
	return buf, from + int64(len(buf)), info, true
}

// prune drops the attempts beyond LogKeep and older than LogMaxAge (the newest stays) and their files.
func (l *InstallLogs) prune(app string, at []AttemptInfo) []AttemptInfo {
	d := l.appDir(app)
	now := l.now()
	var keep []AttemptInfo
	for i := len(at) - 1; i >= 0; i-- {
		a := at[i]
		old := a.End > 0 && now.Sub(time.Unix(a.End, 0)) > LogMaxAge
		if len(keep) >= LogKeep || (old && len(keep) >= 1) {
			os.Remove(attemptFile(d, a.N))
			continue
		}
		keep = append([]AttemptInfo{a}, keep...)
	}
	return keep
}

// Remove deletes the logs of an app (it was removed).
func (l *InstallLogs) Remove(app string) {
	if d := l.appDir(app); d != "" {
		os.RemoveAll(d)
	}
}

// Begin starts the log of a new attempt of an app (label: install, replay, measure, compare).
func (l *InstallLogs) Begin(app, label, node string) *AttemptLog {
	d := l.appDir(app)
	if d == "" || os.MkdirAll(d, 0755) != nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	at := l.readIndex(app)
	n := 1
	if len(at) > 0 {
		n = at[len(at)-1].N + 1
	}
	f, err := os.OpenFile(attemptFile(d, n), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil
	}
	info := AttemptInfo{N: n, App: app, Node: node, Label: label, Start: l.now().Unix(), Result: "running"}
	at = l.prune(app, append(at, info))
	l.writeIndex(app, at)
	return &AttemptLog{owner: l, f: f, info: info}
}

// AttemptLog is the log of one attempt; every method does nothing on a nil one (an install with no log still works).
type AttemptLog struct {
	owner     *InstallLogs
	mu        sync.Mutex
	f         *os.File
	info      AttemptInfo
	size      int64
	truncated bool
	closed    bool
	partial   []byte
}

// N is the number of the attempt.
func (a *AttemptLog) N() int {
	if a == nil {
		return 0
	}
	return a.info.N
}

var (
	redactAssign  = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|set-cookie|x-api-key|api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|passwd|credential)(["']?\s*[:=]\s*["']?)(bearer\s+|basic\s+)?([^\s"',;&]+)`)
	redactURLUser = regexp.MustCompile(`(://)[^/\s:@]+(:[^/\s@]*)?@`)
	redactKnown   = regexp.MustCompile(`\b(ghp|gho|ghs|github_pat|sk|xox[baprs]|AKIA)[-_A-Za-z0-9]{12,}`)
)

// RedactLog cleans a line before it is written: the store's folder as /store, the home folder as ~, and the values of tokens, keys, cookies, passwords and
// the user of an URL as <hidden>.
func RedactLog(s, storeDir string) string {
	if storeDir != "" {
		s = strings.ReplaceAll(s, storeDir, "/store")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" && h != "/" {
		s = strings.ReplaceAll(s, h, "~")
	}
	s = redactURLUser.ReplaceAllString(s, "${1}<hidden>@")
	s = redactAssign.ReplaceAllString(s, "${1}${2}<hidden>")
	s = redactKnown.ReplaceAllString(s, "<hidden>")
	return s
}

// Line writes one line of the log.
func (a *AttemptLog) Line(step, level, format string, args ...interface{}) {
	if a == nil {
		return
	}
	text := format
	if len(args) > 0 {
		text = fmt.Sprintf(format, args...)
	}
	a.write(step, level, text)
}

func (a *AttemptLog) write(step, level, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || (a.truncated && step != "result") {
		return
	}
	text = strings.TrimRight(RedactLog(text, a.owner.dir), "\r\n ")
	if len(text) > logMaxLine {
		text = text[:logMaxLine] + " [line cut]"
	}
	raw, _ := json.Marshal(LogLine{T: a.owner.now().UnixMilli(), Step: step, Level: level, Text: text})
	raw = append(raw, '\n')
	if step != "result" && a.size+int64(len(raw)) > LogMaxBytes {
		a.truncated = true
		end, _ := json.Marshal(LogLine{T: a.owner.now().UnixMilli(), Step: "result", Level: "warn", Text: fmt.Sprintf("the log stops here: it reached %d KB", LogMaxBytes>>10)})
		a.f.Write(append(end, '\n'))
		return
	}
	n, _ := a.f.Write(raw)
	a.size += int64(n)
}

// Output writes what a program printed, line by line (a line that is not complete yet waits for its end).
func (a *AttemptLog) Output(p []byte) {
	if a == nil || len(p) == 0 {
		return
	}
	a.mu.Lock()
	buf := append(a.partial, p...)
	a.partial = nil
	var lines []string
	for {
		i := bytes.IndexAny(buf, "\r\n")
		if i < 0 {
			break
		}
		if i > 0 {
			lines = append(lines, string(buf[:i]))
		}
		buf = buf[i+1:]
	}
	if len(buf) > logMaxLine*2 {
		lines = append(lines, string(buf))
		buf = nil
	}
	a.partial = append([]byte(nil), buf...)
	a.mu.Unlock()
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			a.write("output", "output", l)
		}
	}
}

// End closes the log with its result (installed, failed, stopped) and puts it in the index.
func (a *AttemptLog) End(result, version, manager, errText string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	rest := string(a.partial)
	a.partial = nil
	a.mu.Unlock()
	if strings.TrimSpace(rest) != "" {
		a.write("output", "output", rest)
	}
	level := "info"
	if result != "installed" {
		level = "error"
	}
	line := "result: " + result
	if errText != "" {
		line += ": " + tail(errText, 300)
	}
	a.write("result", level, line)
	a.mu.Lock()
	a.closed = true
	a.f.Close()
	a.info.End = a.owner.now().Unix()
	a.info.Result, a.info.Version, a.info.Manager, a.info.Err = result, version, manager, tail(RedactLog(errText, a.owner.dir), 300)
	a.info.Bytes = a.size
	info := a.info
	a.mu.Unlock()
	a.owner.mu.Lock()
	at := a.owner.readIndex(info.App)
	for i := range at {
		if at[i].N == info.N {
			at[i] = info
		}
	}
	a.owner.writeIndex(info.App, at)
	a.owner.mu.Unlock()
}

// Info is the attempt as it stands.
func (a *AttemptLog) Info() AttemptInfo {
	if a == nil {
		return AttemptInfo{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.info
}

// ---- the context that carries the log to the code that resolves versions ---------------------------------------------------

type logCtxKey struct{}

// WithAttemptLog carries the log of the attempt in a context, so that a resolver can say what it tried.
func WithAttemptLog(ctx context.Context, wl *workLog) context.Context {
	return context.WithValue(ctx, logCtxKey{}, wl)
}

// logf writes a line to the log of the attempt in the context (nothing when there is none).
func logf(ctx context.Context, step, level, format string, args ...interface{}) {
	if wl, ok := ctx.Value(logCtxKey{}).(*workLog); ok && wl != nil {
		wl.Step(step, level, format, args...)
	}
}

// logStep writes a line to the log behind an io.Writer when it is the log of an attempt, else as plain text.
func logStep(w io.Writer, step, level, format string, args ...interface{}) {
	if wl, ok := w.(*workLog); ok && wl != nil {
		wl.Step(step, level, format, args...)
		return
	}
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// exitText says how a command ended: exit 0, exit 2, killed, timed out.
func exitText(err error) string {
	switch e := err.(type) {
	case nil:
		return "exit 0"
	case *exec.ExitError:
		if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "killed by " + ws.Signal().String()
		}
		return "exit " + strconv.Itoa(e.ExitCode())
	}
	if err == context.DeadlineExceeded || strings.Contains(err.Error(), "deadline") {
		return "timed out"
	}
	return "error: " + tail(err.Error(), 120)
}

// logCommand writes a program that was run: its command line, how it ended and how long it ran.
func logCommand(w io.Writer, name string, args []string, err error, d time.Duration) {
	level := "info"
	if err != nil {
		level = "warn"
	}
	line := name
	if len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	logStep(w, "command", level, "%s (%s, %s)", tail(line, 300), exitText(err), d.Round(100*time.Millisecond))
}

// ---- the environment line --------------------------------------------------------------------------------------------------

var envCache struct {
	sync.Mutex
	at   time.Time
	text string
}

func toolVersion(bin string, args ...string) string {
	path, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return tail(s, 60)
}

// EnvLine is the line that opens every log: where the install runs and with what (the versions of the tools, the free disk and memory).
func EnvLine(host, storeDir string) string {
	envCache.Lock()
	defer envCache.Unlock()
	if envCache.text == "" || time.Since(envCache.at) > 10*time.Minute {
		var parts []string
		cpu := ArchAMD64
		if runtime.GOARCH == "arm64" {
			cpu = ArchARM64
		}
		parts = append(parts, cpu)
		for _, t := range []struct {
			label string
			bin   string
			args  []string
		}{{"node", "node", []string{"--version"}}, {"npm", "npm", []string{"--version"}}, {"go", "go", []string{"version"}}, {"rust", "rustc", []string{"--version"}},
			{"java", "java", []string{"-version"}}, {"pixi", "pixi", []string{"--version"}}, {"apt", "apt-get", []string{"--version"}}} {
			if v := toolVersion(t.bin, t.args...); v != "" {
				parts = append(parts, t.label+" "+v)
			}
		}
		envCache.text = strings.Join(parts, " | ")
		envCache.at = time.Now()
	}
	text := host + " | " + envCache.text
	var st syscall.Statfs_t
	if storeDir != "" && syscall.Statfs(storeDir, &st) == nil {
		text += fmt.Sprintf(" | disk free %.1f GB", float64(st.Bavail)*float64(st.Bsize)/(1<<30))
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "MemAvailable:") {
				fs := strings.Fields(sc.Text())
				if len(fs) >= 2 {
					if kb, err := strconv.ParseFloat(fs[1], 64); err == nil {
						text += fmt.Sprintf(" | memory free %.1f GB", kb/(1<<20))
					}
				}
			}
		}
		f.Close()
	}
	return text
}

// ---- the summaries in the catalog -------------------------------------------------------------------------------------------

// summaryKey is where the one-line summary of an attempt is kept in the catalog database (it survives a wipe of the disk of the gateway).
func summaryKey(app string, n int) string { return fmt.Sprintf("logsum:%s:%04d", app, n) }

// MergeAttempts adds, to the attempts a node has files for, the summaries that the catalog still has of ones whose files are gone; newest first.
func MergeAttempts(files, summaries []AttemptInfo) []AttemptInfo {
	have := map[int]bool{}
	for _, a := range files {
		have[a.N] = true
	}
	out := append([]AttemptInfo(nil), files...)
	for _, s := range summaries {
		if !have[s.N] {
			s.LogGone = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].N > out[j].N })
	return out
}

// ---- the service's side --------------------------------------------------------------------------------------------------------

// Logs is the install logs of this node.
func (s *Service) Logs() *InstallLogs {
	s.logOnce.Do(func() { s.logs = NewInstallLogs(filepath.Join(s.cfg.Store.Dir, "logs"), s.cfg.Now) })
	return s.logs
}

// beginLog starts the log of an attempt of an app on this node and remembers it, so that whoever ends the install can end the log.
func (s *Service) beginLog(app, label string) *AttemptLog {
	lg := s.Logs().Begin(app, label, hostName())
	if lg != nil {
		s.curMu.Lock()
		s.curLog[app] = lg
		s.curMu.Unlock()
	}
	return lg
}

// endLog closes the log of the attempt that is going on for an app, and keeps a one-line summary of it in the catalog (a gateway whose disk is wiped
// still lists its attempts).
func (s *Service) endLog(app, result, version, manager, errText string) {
	s.curMu.Lock()
	lg := s.curLog[app]
	delete(s.curLog, app)
	s.curMu.Unlock()
	if lg == nil {
		return
	}
	lg.End(result, version, manager, errText)
	info := lg.Info()
	s.mu.Lock()
	s.put(summaryKey(app, info.N), info)
	// the summaries of the attempts that are gone are dropped too, but a few more than the files are kept
	var keys []string
	s.each("logsum:"+app+":", func(k string, _ []byte) { keys = append(keys, k) })
	sort.Strings(keys)
	for len(keys) > LogKeep*2 {
		s.del(keys[0])
		keys = keys[1:]
	}
	s.mu.Unlock()
}

// resultOf is how an attempt ended, for the log: stopped when it made no progress, else failed.
func resultOf(err error) string {
	if err != nil && strings.Contains(err.Error(), errNoProgress.Error()) {
		return "stopped"
	}
	return "failed"
}

// LogAttempts lists the attempts of an app on this node, newest first: the ones with files, and the summaries of the ones whose files are gone.
func (s *Service) LogAttempts(app string) []AttemptInfo {
	var sums []AttemptInfo
	s.mu.Lock()
	s.each("logsum:"+app+":", func(_ string, raw []byte) {
		var a AttemptInfo
		if json.Unmarshal(raw, &a) == nil {
			sums = append(sums, a)
		}
	})
	s.mu.Unlock()
	return MergeAttempts(s.Logs().Attempts(app), sums)
}

// LogChunk is what the admin page asks for: the attempts, and the text of one attempt from an offset (parsed into lines).
type LogChunk struct {
	Attempts []AttemptInfo `json:"attempts"`
	Info     AttemptInfo   `json:"info"`
	From     int64         `json:"from"`
	Next     int64         `json:"next"`
	Lines    []LogLine     `json:"lines"`
	Running  bool          `json:"running"`
	Found    bool          `json:"found"` // the log of the attempt is on this node
}

// ReadLog reads a chunk of the log of an attempt (0: the newest) of an app on this node.
func (s *Service) ReadLog(app string, attempt int, from int64) LogChunk {
	out := LogChunk{Attempts: s.LogAttempts(app), Lines: []LogLine{}}
	data, next, info, ok := s.Logs().Read(app, attempt, from, 128<<10)
	if !ok {
		if attempt <= 0 && len(out.Attempts) > 0 {
			out.Info = out.Attempts[0]
		}
		for _, a := range out.Attempts {
			if a.N == attempt {
				out.Info = a
			}
		}
		out.From, out.Next = from, from
		return out
	}
	out.Found, out.Info, out.From, out.Next, out.Running = true, info, from, next, info.Result == "running"
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var l LogLine
		if len(line) > 0 && json.Unmarshal(line, &l) == nil {
			out.Lines = append(out.Lines, l)
		}
	}
	return out
}

// endLogOf closes an attempt log that is not tracked by app name (a replay) and keeps its summary like endLog.
func (s *Service) endLogOf(lg *AttemptLog, result, version, manager, errText string) {
	if lg == nil {
		return
	}
	lg.End(result, version, manager, errText)
	info := lg.Info()
	s.mu.Lock()
	s.put(summaryKey(info.App, info.N), info)
	s.mu.Unlock()
}

// serverLogf writes a line to the log of the server (a function of its own because some functions have a parameter called log).
func serverLogf(format string, args ...interface{}) { log.Printf(format, args...) }
