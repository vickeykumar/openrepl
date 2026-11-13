package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The agent's "bash" action (design canvas "Genie agent: a bash action"): the page of the owner asks the server to run one command in its
// own shell, in the sandbox of the user's workspace (the filesystem rules, memory limit and private network of the terminal), with no input
// and a time limit, and gets its exit code and output back. The terminal and its language are not touched. The page asks the user first
// (agent.ts); the server checks who asks and what the admin allows, and keeps the limits.

const (
	shellDefaultTimeout = 30 * time.Second
	shellMaxTimeout     = 120 * time.Second
	shellMaxCommand     = 4000
	shellHead           = 32 << 10 // the first and the last bytes of the output are kept
	shellTail           = 32 << 10
	shellBusyWait       = 5 * time.Second
	shellPerMinute      = 60
)

// shellResult is what the page gets back.
type shellResult struct {
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	TimedOut   bool   `json:"timed_out"`
	DurationMS int64  `json:"duration_ms"`
}

// outputKeeper keeps the first shellHead and the last shellTail bytes of what a command printed, and how much was cut between them.
type outputKeeper struct {
	mu   sync.Mutex
	head []byte
	tail []byte
	cut  int64
}

func (o *outputKeeper) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if room := shellHead - len(o.head); room > 0 {
		take := room
		if take > len(p) {
			take = len(p)
		}
		o.head = append(o.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		o.tail = append(o.tail, p...)
		if over := len(o.tail) - shellTail; over > 0 {
			o.cut += int64(over)
			o.tail = append([]byte{}, o.tail[over:]...)
		}
	}
	return n, nil
}

// text is the output as valid UTF-8, with a line where bytes were cut.
func (o *outputKeeper) text() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := string(o.head)
	if o.cut > 0 {
		s += fmt.Sprintf("\n[... %d bytes of output cut ...]\n", o.cut)
	}
	s += string(o.tail)
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\r\n", "\n"), "�"), o.cut > 0
}

// one command at a time for a workspace; the next waits a few seconds, then is refused
var shellLocks sync.Map // workspace -> chan struct{}

func lockShell(home string) (release func(), ok bool) {
	v, _ := shellLocks.LoadOrStore(home, make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
	case <-time.After(shellBusyWait):
		return nil, false
	}
	return func() { <-ch }, true
}

// a command's count for each user in the last minute
var (
	shellRateMu sync.Mutex
	shellRate   = map[string][]time.Time{}
)

func shellAllowed(uid string, now time.Time) bool {
	shellRateMu.Lock()
	defer shellRateMu.Unlock()
	recent := shellRate[uid][:0]
	for _, t := range shellRate[uid] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= shellPerMinute {
		shellRate[uid] = recent
		return false
	}
	shellRate[uid] = append(recent, now)
	return true
}

func shellTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return shellDefaultTimeout
	}
	d := time.Duration(seconds) * time.Second
	if d > shellMaxTimeout {
		return shellMaxTimeout
	}
	return d
}

// handleAgentBash answers POST /agent_bash {"command": "...", "timeout_seconds": 30}.
func (server *Server) handleAgentBash(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	uid, home := server.requestIdentity(rw, req)
	if uid == "" {
		http.Error(rw, "Genie can run commands for signed-in users only.", http.StatusForbidden)
		return
	}
	// a collaborator of a shared session names the owner's workspace: only the owner's own Genie runs commands in it
	if homeOverridden(req) {
		http.Error(rw, "Only the owner of a workspace can have Genie run commands in it.", http.StatusForbidden)
		return
	}
	if g := GetSiteSettings().Genie; g.Disabled || g.AgentDisabled {
		http.Error(rw, "Agent mode is switched off on this site.", http.StatusForbidden)
		return
	} else if g.AgentShellOff {
		http.Error(rw, "Running commands is switched off on this site.", http.StatusForbidden)
		return
	}
	var in struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Command) == "" {
		http.Error(rw, "send {\"command\": \"...\"}", http.StatusBadRequest)
		return
	}
	if len(in.Command) > shellMaxCommand || !utf8.ValidString(in.Command) || strings.ContainsRune(in.Command, 0) {
		http.Error(rw, "the command has to be text of at most 4000 characters", http.StatusBadRequest)
		return
	}
	if !shellAllowed(uid, time.Now()) {
		http.Error(rw, "Too many commands in a minute: wait a little.", http.StatusTooManyRequests)
		return
	}
	release, ok := lockShell(home)
	if !ok {
		http.Error(rw, "Another command of this workspace is still running.", http.StatusConflict)
		return
	}
	defer release()
	res, err := runShell(req.Context(), home, in.Command, shellTimeout(in.TimeoutSeconds))
	if err != nil {
		http.Error(rw, "The command could not be started: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// one line for the admin: where, how it ended; never the command's text or output
	log.Printf("agent bash: %s exit %d in %d ms (timed out: %v)", home, res.ExitCode, res.DurationMS, res.TimedOut)
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(res)
}
