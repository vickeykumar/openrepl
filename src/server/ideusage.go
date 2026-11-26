package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cookie"
	"ideusage"
	"livepreview"
	"user"
	"utils"
	"webtty"
)

// The IDE data usage (package ideusage, docs/lld/20-ide-usage.md): what the IDE sends to a user is counted where it leaves the server
// that faces the user (the gateway, or a single server), the terminal's speed is capped, and a user at the limit has the heavy parts
// stopped. Requests that came over the gateway's tunnel are not counted again: the gateway counted them.

var ideMeter = ideusage.New()

var ideStart sync.Once

func init() {
	ideusage.ConfigFunc = func() ideusage.Config { return GetSiteSettings().ideConfig() }
}

// startIDEUsage reads the saved record and keeps saving it until ctx ends.
func startIDEUsage(ctx context.Context) {
	ideStart.Do(func() {
		ideMeter.SetStore(userDBStore{})
		go ideMeter.Run(ctx.Done())
		go func() { // the accounts that need a look go to the server log, when the setting says so
			t := time.NewTicker(30 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					logIDEFlags()
				case <-ctx.Done():
					return
				}
			}
		}()
	})
}

// userDBStore keeps the records in the user database, the one that holds the profile (so they follow the profile to the remote
// database and the backups).
type userDBStore struct{}

func (userDBStore) Get(key string) ([]byte, bool) {
	db := user.GetUserDBHandle()
	if db == nil {
		return nil, false
	}
	v, err := db.Fetch([]byte(key))
	if err != nil || len(v) == 0 {
		return nil, false
	}
	return v, true
}

func (userDBStore) Put(key string, data []byte) error {
	db := user.GetUserDBHandle()
	if db == nil {
		return fmt.Errorf("the user database is not open")
	}
	if err := db.Store([]byte(key), data); err != nil {
		return err
	}
	return db.Commit()
}

// the admin check of a request is by account, kept for a minute: an admin's email is looked up in the user database
type adminMemo struct {
	ok bool
	at time.Time
}

var (
	adminMemos   = map[string]adminMemo{}
	adminMemosMu sync.Mutex
)

func uidIsAdmin(uid string) bool {
	adminMemosMu.Lock()
	defer adminMemosMu.Unlock()
	if m, ok := adminMemos[uid]; ok && time.Since(m.at) < time.Minute {
		return m.ok
	}
	up, err := user.FetchUserProfileData(uid)
	ok := err == nil && utils.IsAdminEmail(up.Email)
	if len(adminMemos) > 5000 {
		adminMemos = map[string]adminMemo{}
	}
	adminMemos[uid] = adminMemo{ok, time.Now()}
	return ok
}

// ideIdentity is whose count a request belongs to: the workspace of its session cookie. The Preview carries no cookie, only a token
// in the address that names the workspace. Key is empty when there is nobody to count for.
var ideWho = ideIdentity

func ideIdentity(r *http.Request) ideusage.Identity {
	if home, ok := previewHomeOf(r); ok {
		admin, _, _, _ := ideMeter.Known(home)
		return ideusage.Identity{Key: home, Guest: strings.HasPrefix(home, "guest-"), Signed: !strings.HasPrefix(home, "guest-"), Admin: admin}
	}
	uid, home := cookie.QuietIdentity(r)
	if home == "" {
		return ideusage.Identity{}
	}
	id := ideusage.Identity{Key: home, UID: uid, Guest: uid == "", Signed: uid != ""}
	id.Admin = uid != "" && uidIsAdmin(uid)
	return id
}

// previewHomeOf is the workspace a preview address names (/preview/<token>/...), if the token is good.
func previewHomeOf(r *http.Request) (string, bool) {
	p := r.URL.Path
	i := strings.Index(p, "/preview/")
	if i < 0 {
		return "", false
	}
	parts := strings.SplitN(p[i+len("/preview/"):], "/", 2)
	if len(parts) == 0 || parts[0] == "" || parts[0] == "lib" {
		return "", false
	}
	home, _, ok := livepreview.Viewer(utils.Secret(), parts[0], time.Now())
	if !ok {
		return "", false
	}
	return filepath.Base(home), true
}

// ideKind is how a request is metered and what happens to it at the limit.
type ideKind int

const (
	ideNone     ideKind = iota
	ideTerminal         // a terminal's WebSocket
	ideCount            // counted, never held back
	ideHeavy            // counted, and refused at the limit
	ideEditor           // a file for the editor: counted, and at the limit only a small one from the reserve
)

// ideRoute is the part of the IDE a request belongs to; ideNone for anything else (the website).
func (server *Server) ideRoute(r *http.Request) (ideusage.Part, ideKind) {
	p := r.URL.Path
	if strings.Contains(p, "/preview/") && !strings.HasSuffix(p, "/preview/") {
		if strings.Contains(p, "/preview/lib/") {
			return 0, ideNone // the libraries of the catalogue are the site's own files
		}
		return ideusage.Preview, ideHeavy
	}
	rel := strings.TrimPrefix(p, server.admin.prefix)
	rel = strings.TrimPrefix(rel, "/")
	if _, ok := server.terminals[rel]; ok && rel != "" {
		return ideusage.Terminal, ideTerminal
	}
	switch rel {
	case "ws_filebrowser":
		switch r.URL.Query().Get("q") {
		case "load":
			return ideusage.Editor, ideEditor
		case "zip":
			return ideusage.Files, ideHeavy
		}
		return ideusage.Files, ideCount
	case "upload_file":
		return ideusage.Files, ideCount
	case "chat/completions", "agent_bash":
		return ideusage.Genie, ideHeavy
	case "agent_files", "agent_restore", "genie_chats", "genie_canvas", "genie_ext", "genie_conn":
		return ideusage.Genie, ideCount
	}
	return 0, ideNone
}

// wrapIDEMeter counts the IDE's traffic and holds back the heavy parts of a user who reached the limit.
func (server *Server) wrapIDEMeter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTrusted(r) {
			next.ServeHTTP(w, r)
			return
		}
		part, kind := server.ideRoute(r)
		c := ideusage.ConfigFunc()
		if kind == ideNone || !c.On || !c.Counted[part] {
			next.ServeHTTP(w, r)
			return
		}
		id := ideWho(r)
		if id.Key == "" || (id.Admin && c.AdminsExempt) {
			next.ServeHTTP(w, r)
			return
		}
		ideMeter.Touch(id, nil)
		st := ideMeter.StateOf(id)
		if st.AtLimit {
			switch kind {
			case ideHeavy:
				ideRefuse(w, r, part, id, st)
				return
			case ideEditor:
				left := ideMeter.ReserveLeft(id)
				if left <= 0 {
					ideRefuse(w, r, part, id, st)
					return
				}
				max := c.SmallBytes
				if left < max {
					max = left
				}
				r2 := r.Clone(r.Context())
				q := r2.URL.Query()
				q.Set("max", strconv.FormatInt(max, 10))
				r2.URL.RawQuery = q.Encode()
				r = r2
			}
		}
		iw := &ideWriter{ResponseWriter: w, id: id, part: part}
		if kind == ideTerminal {
			iw.gate = newIDEGate(id)
		}
		defer iw.flushCount()
		next.ServeHTTP(iw, r)
	})
}

// ideRefuse answers a request that the limit holds back, in the form its part expects.
func ideRefuse(w http.ResponseWriter, r *http.Request, part ideusage.Part, id ideusage.Identity, st ideusage.State) {
	msg := ideLimitText(id, st)
	switch part {
	case ideusage.Genie:
		if strings.HasSuffix(r.URL.Path, "chat/completions") {
			handleChatProxyError(w, r, http.StatusTooManyRequests, NewErrorResponse(msg, "ide_data_limit", "StatusTooManyRequests", ""))
			return
		}
	case ideusage.Preview:
		livepreview.SandboxHeaders(w.Header())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><body style=\"font-family:system-ui,sans-serif;padding:24px\"><p>%s</p>", htmlEscape(msg))
		return
	}
	w.Header().Set("Retry-After", "3600")
	http.Error(w, msg, http.StatusTooManyRequests)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(s)
}

// ideLimitText is what a user is told at the limit (the canvas, board 5).
func ideLimitText(id ideusage.Identity, st ideusage.State) string {
	mb := st.Limit / ideusage.MB
	if id.Guest {
		return fmt.Sprintf("You have used this session's %d MB. Sign in free to get %d MB a day. Your code and files stay here.", mb, st.SignInLimit/ideusage.MB)
	}
	return fmt.Sprintf("Daily data limit reached (%d MB). Output is paused until %s. Your files are safe.", mb, ideResetText(id, st))
}

// ideResetText is the time the day resets, in the user's own time zone when the page told it.
func ideResetText(id ideusage.Identity, st ideusage.State) string {
	at := time.UnixMilli(st.ResetsAt).UTC()
	_, _, tz, has := ideMeter.Known(id.Key)
	if !has {
		return at.Format("15:04") + " UTC"
	}
	loc := at.Add(time.Duration(tz) * time.Minute)
	sign, a := "+", tz
	if tz < 0 {
		sign, a = "-", -tz
	}
	zone := fmt.Sprintf("UTC%s%d", sign, a/60)
	if a%60 != 0 {
		zone += fmt.Sprintf(":%02d", a%60)
	}
	return loc.Format("3:04 PM") + " (" + zone + ")"
}

// ideWriter counts what a response writes.
type ideWriter struct {
	http.ResponseWriter
	id      ideusage.Identity
	part    ideusage.Part
	pending int64
	gate    ideusage.Gate
}

func (w *ideWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.pending += int64(n)
	if w.pending >= 64<<10 {
		w.flushCount()
	}
	return n, err
}

func (w *ideWriter) flushCount() {
	if w.pending > 0 {
		ideMeter.Add(w.id, w.part, w.pending)
		w.pending = 0
	}
}

func (w *ideWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *ideWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack hands a terminal's WebSocket over through the gate, which counts its frames and holds back the output of a terminal that
// is too fast or whose user is at the limit. Any other hijacked connection goes through as it is.
func (w *ideWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, brw, err := h.Hijack()
	if err == nil && w.gate != nil {
		conn = ideusage.GateConn(conn, w.gate)
	}
	return conn, brw, err
}

// ideGate is the speed cap and the limit of one terminal.
type ideGate struct {
	id   ideusage.Identity
	rate *ideusage.Rate
	mu   sync.Mutex
	why  string

	lastPause time.Time // when a frame was last held, so that a run of held frames is one pause
}

func newIDEGate(id ideusage.Identity) *ideGate { return &ideGate{id: id, rate: ideusage.NewRate()} }

var errIDELimit = fmt.Errorf("data limit reached")

// Frame counts a frame of the terminal and decides whether it goes. Only the output of the program is held back: the titles, the
// answers to a ping and the like always go.
func (g *ideGate) Frame(first byte, size int64) error {
	c := ideusage.ConfigFunc()
	if !c.On || (g.id.Admin && c.AdminsExempt) {
		return nil
	}
	if first == webtty.Output {
		if ideMeter.StateOf(g.id).AtLimit { // the reserve is for saving and small files, not for output
			return errIDELimit
		}
		grace := c.TermGrace
		if c.TermPauseOnly {
			grace = c.TermWindow + time.Second // the window empties by itself: it is a pause, never an end
		}
		paused, err := g.rate.WaitPaused(size, c.TermBytes, c.TermWindow, grace)
		if paused && time.Since(g.lastPause) > 2*time.Second { // one pause for a run of held frames
			ideMeter.NoteStop(g.id, false)
		}
		if paused {
			g.lastPause = time.Now()
		}
		if err != nil {
			ideMeter.NoteStop(g.id, true) // the run is ended
			return err
		}
	}
	ideMeter.Add(g.id, ideusage.Terminal, size)
	return nil
}

// End is what the page is told when the run is ended: the reason as output, the end notice, and the close.
func (g *ideGate) End(err error) []byte {
	c := ideusage.ConfigFunc()
	st := ideMeter.StateOf(g.id)
	var msg, reason string
	if err == ideusage.ErrTooFast {
		msg = fmt.Sprintf("Output was too fast (over %d MB in %d s), so this run was stopped. Your files are untouched. Try printing less, or add a stop condition.",
			c.TermBytes/ideusage.MB, int(c.TermWindow/time.Second))
		reason = "terminal output too fast"
	} else {
		msg = ideLimitText(g.id, st)
		reason = "data limit reached"
	}
	out := "\x1b[33m\r\n" + msg + "\x1b[0m\r\n"
	notice, _ := json.Marshal(struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}{1000, reason})
	var b []byte
	b = append(b, ideusage.TextFrame(append([]byte{webtty.Output}, []byte(base64.StdEncoding.EncodeToString([]byte(out)))...))...)
	b = append(b, ideusage.TextFrame(append([]byte{webtty.End}, notice...))...)
	b = append(b, ideusage.CloseFrame(1000, reason)...)
	return b
}

// handleIDEUsage is what the page asks for (GET /ide_usage?tz=<minutes east of UTC>&days=<n>): where the user stands, and the last days.
func (server *Server) handleIDEUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	id := ideWho(r)
	var tz *int
	if v, err := strconv.Atoi(r.URL.Query().Get("tz")); err == nil {
		tz = &v
	}
	ideMeter.Touch(id, tz)
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 0 || days > 90 {
		days = 90
	}
	out := map[string]interface{}{"state": ideMeter.StateOf(id), "guest": id.Guest, "signedIn": id.Signed}
	if days > 0 && id.Key != "" && !id.Guest {
		out["history"] = ideMeter.History(id.Key, days)
	}
	json.NewEncoder(w).Encode(out)
}

// handleAdminIDEUsage is the admin's view: today for all users (GET), and the button that resets one account's count (POST ?uid=).
func (server *Server) handleAdminIDEUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		if !adminRequestOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if on := r.URL.Query().Get("watch"); on != "" { // the watch list: ?watch=1|0&uid=
			uid := r.URL.Query().Get("uid")
			if uid == "" || len(uid) > 128 {
				http.Error(w, "no account", http.StatusBadRequest)
				return
			}
			if err := setIDEWatch(uid, on == "1"); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			server.audit(r, "IDE data watch list", map[bool]string{true: "added ", false: "removed "}[on == "1"]+uid)
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "watch": GetSiteSettings().IDEWatch})
			return
		}
		key := ideMeter.KeyOfUID(r.URL.Query().Get("uid"))
		if key == "" || !ideMeter.Reset(key) {
			http.Error(w, "no count to reset", http.StatusNotFound)
			return
		}
		server.audit(r, "reset IDE data count", r.URL.Query().Get("uid"))
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	if rep := r.URL.Query().Get("report"); rep != "" { // the days of all accounts
		server.adminIDEReport(w, r, atoiDefault(rep, 30))
		return
	}
	if acct := r.URL.Query().Get("acct"); acct != "" { // one account over a period
		server.adminIDEAccount(w, r, acct, atoiDefault(r.URL.Query().Get("days"), 30))
		return
	}
	if uid := r.URL.Query().Get("uid"); uid != "" {
		key := ideMeter.KeyOfUID(uid)
		id := ideusage.Identity{Key: key, UID: uid, Signed: true}
		json.NewEncoder(w).Encode(map[string]interface{}{"state": ideMeter.StateOf(id), "history": ideMeter.History(key, 7)})
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("top"))
	if n <= 0 || n > 200 {
		n = 5
	}
	sum := ideMeter.Summarize(n)
	for i := range sum.Top {
		if up, err := user.FetchUserProfileData(sum.Top[i].UID); err == nil {
			sum.Top[i].Name = up.Name
		}
	}
	json.NewEncoder(w).Encode(sum)
}
