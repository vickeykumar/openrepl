package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"containers"
	"egress"
	"livepreview"
	"netemu"
	"utils"
)

// The Preview pane (docs/preview-pane-design.md). The pane's frame is sandboxed and has no origin, so none of the
// visitor's cookies reach the route it loads: what names the workspace is a token in the address (package preview).

// previewTokenLife is how long a preview link works: the admin's setting for guests or for signed-in users (Settings, Preview),
// or the built-in 1 hour and 12 hours.
func previewTokenLife(s SiteSettings, guest bool) time.Duration { return s.previewLinkLife(guest) }

// viewerID is the random id a pane makes for itself, kept to letters and digits and 32 of them (it ends in a token).
func viewerID(v string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(v) && len(out) < 32; i++ {
		if c := v[i]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	return string(out)
}

// handlePreviewCookies forgets the cookies the server keeps for the caller's preview of a port (POST /preview_cookies?port=N&v=ID).
func (server *Server) handlePreviewCookies(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	uid, homedir := server.requestIdentity(rw, req)
	if why := previewRefused(uid == ""); why != "" {
		http.Error(rw, why, http.StatusForbidden)
		return
	}
	viewer := viewerID(req.Form.Get("v"))
	if port, err := strconv.Atoi(req.Form.Get("port")); err == nil {
		previewJar.Clear(fmt.Sprintf("%s|%s|%d", homedir, viewer, port))
	} else {
		for _, p := range previewPorts(homedir) {
			previewJar.Clear(fmt.Sprintf("%s|%s|%d", homedir, viewer, p))
		}
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handlePreviewToken gives the editor page a token for its own workspace, to put in the address of the pane's frame.
// It is asked with the visitor's own cookies, like every other call of the page.
func (server *Server) handlePreviewToken(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	uid, homedir := server.requestIdentity(rw, req)
	if err := server.waitWorkspace(req, homedir); err != nil {
		http.Error(rw, "workspace is synchronizing, please try again", http.StatusServiceUnavailable)
		return
	}
	if why := previewRefused(uid == ""); why != "" {
		http.Error(rw, why, http.StatusForbidden)
		return
	}
	life := previewTokenLife(GetSiteSettings(), uid == "")
	token, err := livepreview.NewViewerToken(utils.Secret(), homedir, viewerID(req.Form.Get("v")), time.Now(), life)
	if err != nil {
		http.Error(rw, "the preview is not available on this server", http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(map[string]interface{}{
		"token":   token,
		"expires": time.Now().Add(life).Unix(),
	})
}

// previewRefused says why the preview is switched off for this visitor, or "" when it is on (an admin's setting).
func previewRefused(guest bool) string {
	s := GetSiteSettings()
	if s.PreviewOff {
		return "The preview is switched off on this site."
	}
	if guest && s.PreviewGuestsOff {
		return "The preview is switched off for guests. Sign in to use it."
	}
	return ""
}

// handlePreview answers /preview/<token>/f/<path> (a file of the workspace) and, later, /preview/<token>/p/<port>/...
// (a program of the workspace).
func (server *Server) handlePreview(rw http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	i := strings.Index(path, "/preview/")
	if i < 0 {
		http.NotFound(rw, req)
		return
	}
	parts := strings.SplitN(path[i+len("/preview/"):], "/", 3)
	if len(parts) < 2 {
		http.NotFound(rw, req)
		return
	}
	token, kind := parts[0], parts[1]
	if token == "lib" {
		server.handlePreviewLib(rw, req, strings.TrimPrefix(path[i+len("/preview/"):], "lib/"))
		return
	}
	rest := ""
	if len(parts) == 3 {
		rest = parts[2]
	}
	home, viewer, ok := livepreview.Viewer(utils.Secret(), token, time.Now())
	if !ok {
		livepreview.SandboxHeaders(rw.Header())
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.WriteHeader(http.StatusGone)
		rw.Write([]byte(previewEnded))
		return
	}
	if why := previewRefused(strings.Contains(home, "guest-")); why != "" {
		livepreview.SandboxHeaders(rw.Header())
		http.Error(rw, why, http.StatusForbidden)
		return
	}
	st := &livepreview.StorageInfo{Store: previewStore, Home: home, Viewer: viewer, URL: "/preview/" + token + "/s"}
	if svc := storeService(); svc != nil && !GetSiteSettings().StoreOff {
		st.Tools = svc.PreviewHead(filepath.Base(home), path[:i]) // the tools of the preview this visitor has on (Eruda, vConsole): scripts of this site
	}
	switch kind {
	case "f":
		livepreview.ServeFileWith(rw, req, home, rest, st)
	case "s":
		livepreview.HandleStorage(rw, req, previewStore, home, viewer)
	case "p":
		server.previewProgram(rw, req, token, home, viewer, rest, st)
	default:
		http.NotFound(rw, req)
	}
}

// handlePreviewLib serves /preview/lib/<package>/<file>: a file of an installed browser library or third party plugin of the catalogue. It is
// public (the page that asks is a sandboxed frame with no cookies), read only, and limited to what the catalogue names; everything else is 404.
func (server *Server) handlePreviewLib(rw http.ResponseWriter, req *http.Request, rest string) {
	svc := storeService()
	if svc == nil || GetSiteSettings().StoreOff || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		http.NotFound(rw, req)
		return
	}
	pkg, file := rest, ""
	// a scoped package (@picocss/pico) has a slash in its name
	if strings.HasPrefix(rest, "@") {
		seg := strings.SplitN(rest, "/", 3)
		if len(seg) < 3 {
			http.NotFound(rw, req)
			return
		}
		pkg, file = seg[0]+"/"+seg[1], seg[2]
	} else if j := strings.Index(rest, "/"); j > 0 {
		pkg, file = rest[:j], rest[j+1:]
	}
	p, ok := svc.LibFile(pkg, file)
	if !ok {
		http.NotFound(rw, req)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(rw, req)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	rw.Header().Set("Access-Control-Allow-Origin", "*") // fonts of a library are asked for by a page with no origin
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(rw, req, filepath.Base(p), fi.ModTime(), f)
}

// previewJar keeps the cookies of the programs that are previewed (the page in the frame cannot keep them).
var previewJar = livepreview.NewJar()

// previewStore keeps the localStorage, sessionStorage and cookies that the previewed pages write (the frame has none of its own).
var previewStore = livepreview.NewStore()

// previewProgram passes a request to the program of the workspace that listens on a port: /preview/<token>/p/<port>/<path>.
func (server *Server) previewProgram(rw http.ResponseWriter, req *http.Request, token, home, viewer, rest string, st *livepreview.StorageInfo) {
	seg := strings.SplitN(rest, "/", 2)
	port, err := strconv.Atoi(seg[0])
	if err != nil || port < livepreview.MinPort || port > 65535 {
		livepreview.SandboxHeaders(rw.Header())
		http.Error(rw, "a preview reaches the ports from "+strconv.Itoa(livepreview.MinPort)+" up", http.StatusBadRequest)
		return
	}
	if len(seg) == 1 {
		// the program's pages use relative links: the address ends in a slash
		target := req.URL.Path + "/"
		if req.URL.RawQuery != "" {
			target += "?" + req.URL.RawQuery
		}
		http.Redirect(rw, req, target, http.StatusMovedPermanently)
		return
	}
	prefix := "/preview/" + token + "/p/" + seg[0]
	livepreview.Proxy(rw, req, seg[1], livepreview.Options{
		Home: home, Port: port, Prefix: prefix, Dial: previewDialer(home), Jar: previewJar, Viewer: viewer, Hook: livepreview.HTMLHookWith(prefix, st, livepreview.ProgramScope(port)),
	})
}

var errNoIsolation = errors.New("a program can be previewed only where each workspace has a network of its own, and this server has none (network isolation is off)")

// previewDialer reaches the ports of a workspace and nothing else: inside its network namespace when it has one, through
// the network emulation's sockets (LLD 16) when it has not, and not at all when the machine's network is shared (a port of
// the server, or of another visitor, would then be reachable).
func previewDialer(home string) livepreview.Dialer {
	return func(ctx context.Context, port int) (net.Conn, error) {
		pids := previewProc.find(home)
		var tried []string // what was attempted and how it ended, for the log of a Preview that fails
		fail := func(err error) (net.Conn, error) {
			logPreviewFailure(home, port, pids, tried, err)
			return nil, err
		}
		emu := func() (net.Conn, error) {
			var d net.Dialer
			c, err := d.DialContext(ctx, "unix", netemu.Name(netemu.WorkspaceID(home), "t", uint16(port)))
			tried = append(tried, fmt.Sprintf("emulation: %v", err))
			return c, err
		}
		if len(pids) == 0 {
			// No process was found by its HOME or its helper. Under the network emulation the program's socket has a name that holds only the
			// workspace and the port, so it can be reached without knowing a process: a REPL that /proc did not show (a process the server may
			// not read) must not hide a program that is listening (reported: "Nothing is running yet" with `python -m http.server` running).
			if netEmuActive() {
				if c, err := emu(); err == nil {
					logNoProcess(home, port, true)
					return c, nil
				}
			}
			logNoProcess(home, port, false)
			return fail(livepreview.ErrNoProgram)
		}
		// each REPL of a workspace may have a network of its own: the program is in the one that listens on the port
		var lastErr error
		own := false
		admin := false // a REPL with no sandbox at all (an admin's) listens on the machine's real network
		for _, pid := range pids {
			if previewProc.same(pid) {
				own = true
				if netEmuActive() && previewProc.unsandboxed(pid) {
					admin = true
				}
				continue
			}
			c, err := previewProc.dialIn(pid, port)
			tried = append(tried, fmt.Sprintf("namespace of %d: %v", pid, err))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		if lastErr != nil && !own {
			return fail(lastErr)
		}
		if !own {
			return fail(errNoIsolation)
		}
		// The processes in the server's own network. A workspace can hold both kinds at once (an admin's terminal opened in a user's workspace, the
		// transient helper of a fork): the emulation's socket is tried first (a sandboxed REPL's program), then the real network (an unsandboxed
		// one's), so that the one kind does not hide the other. (Before, one unsandboxed process sent every port to the real network, where the
		// program was not and the site itself is.)
		if netEmuActive() {
			c, err := emu()
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		if admin {
			// the real network is where the site itself listens: its own port is not a program to preview
			if isOwnPort(port) {
				tried = append(tried, "real network: the port of the site itself")
				return fail(errOwnPort)
			}
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
			tried = append(tried, fmt.Sprintf("real network: %v", err))
			if err != nil {
				return fail(err)
			}
			return c, nil
		}
		if lastErr != nil {
			return fail(lastErr)
		}
		return fail(errNoIsolation)
	}
}

// previewProc is what the dialer asks of /proc and of the namespaces; the tests replace it.
var previewProc = struct {
	find        func(home string) []int
	same        func(pid int) bool
	unsandboxed func(pid int) bool
	dialIn      func(pid, port int) (net.Conn, error)
}{
	find:        livepreview.FindPIDs,
	same:        livepreview.SameNetwork,
	unsandboxed: livepreview.Unsandboxed,
	dialIn: func(pid, port int) (net.Conn, error) {
		return livepreview.DialIn(fmt.Sprintf("/proc/%d/ns/net", pid), port, 10*time.Second)
	},
}

// previewPorts lists the ports a workspace's programs listen on, from the sockets of its own network. The proxy's own port (LLD 16
// "Egress") is the server's listener, not a program of the user: it is not listed.
func previewPorts(home string) []int {
	return withoutOwnPorts(withoutPort(workspacePorts(home), egress.Port))
}

// the ports this server itself listens on: an admin's REPL has no sandbox, so its sockets are the machine's own and the site would show up
var (
	ownPortsMu sync.Mutex
	ownPorts   = map[int]bool{}
)

func noteOwnPort(port string) {
	if n, err := strconv.Atoi(port); err == nil && n > 0 {
		ownPortsMu.Lock()
		ownPorts[n] = true
		ownPortsMu.Unlock()
	}
}

var errOwnPort = errors.New("this is the port of the site itself, not of a program of yours")

func isOwnPort(port int) bool {
	ownPortsMu.Lock()
	defer ownPortsMu.Unlock()
	return ownPorts[port]
}

func withoutOwnPorts(in []int) []int {
	ownPortsMu.Lock()
	defer ownPortsMu.Unlock()
	out := make([]int, 0, len(in))
	for _, p := range in {
		if !ownPorts[p] {
			out = append(out, p)
		}
	}
	return out
}

func withoutPort(in []int, port int) []int {
	out := make([]int, 0, len(in))
	for _, p := range in {
		if p != port {
			out = append(out, p)
		}
	}
	return out
}

func workspacePorts(home string) []int {
	pids := livepreview.FindPIDs(home)
	if len(pids) == 0 {
		return []int{}
	}
	var all []int
	shared := false
	for _, pid := range pids {
		if livepreview.SameNetwork(pid) {
			shared = true
			// an admin's REPL has no sandbox: its sockets are the machine's own
			if !(containers.NetEmuActive() && livepreview.Unsandboxed(pid)) {
				continue
			}
		}
		base := fmt.Sprintf("/proc/%d/net/", pid)
		all = append(all, livepreview.ParseTCP(livepreview.ReadProc(base+"tcp"))...)
		all = append(all, livepreview.ParseTCP(livepreview.ReadProc(base+"tcp6"))...)
	}
	if !shared {
		return uniqueSorted(all)
	}
	if containers.NetEmuActive() {
		// the names of the workspace's TCP sockets all start "@openrepl-ne/<workspace>/t/", then the port
		prefix := strings.TrimSuffix(netemu.Name(netemu.WorkspaceID(home), "t", 0), "0")
		return uniqueSorted(append(all, livepreview.ParseUnixAbstract(livepreview.ReadProc("/proc/net/unix"), prefix)...))
	}
	return []int{}
}

func uniqueSorted(in []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// handlePreviewPorts tells the editor page which ports its workspace's programs listen on (the pane's port list, and
// the "your program is listening" prompt). Asked with the visitor's own cookies.
func (server *Server) handlePreviewPorts(rw http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	uid, homedir := server.requestIdentity(rw, req)
	if why := previewRefused(uid == ""); why != "" {
		http.Error(rw, why, http.StatusForbidden)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(rw).Encode(map[string]interface{}{
		"ports":    previewPorts(homedir),
		"isolated": previewIsolated(homedir),
	})
}

// previewIsolated says whether a program of the workspace could be previewed here (see previewDialer).
func previewIsolated(home string) bool {
	pid := livepreview.FindPID(home)
	if pid != 0 && !livepreview.SameNetwork(pid) {
		return true
	}
	return containers.NetEmuActive()
}

const previewEnded = `<!doctype html><meta charset="utf-8"><title>Preview ended</title>
<body style="font:16px system-ui;margin:3rem;color:#333"><h1 style="font-size:20px">This preview has ended.</h1>
<p>Open it again from the editor.</p>`

// netEmuActive is containers.NetEmuActive; the tests replace it.
var netEmuActive = containers.NetEmuActive

var (
	noProcessMu   sync.Mutex
	noProcessSeen = map[string]time.Time{}
)

// logNoProcess says, at most once a minute for a workspace, that the Preview found no process of the workspace (and whether the emulation's socket
// answered all the same), so that a report of "Nothing is running yet" can be told from a program that is really not there.
func logNoProcess(home string, port int, reached bool) {
	noProcessMu.Lock()
	defer noProcessMu.Unlock()
	if len(noProcessSeen) > 512 {
		noProcessSeen = map[string]time.Time{}
	}
	if time.Since(noProcessSeen[home]) < time.Minute {
		return
	}
	noProcessSeen[home] = time.Now()
	log.Printf("preview: no process of %s was found for port %d (the emulation's socket answered: %v)", filepath.Base(home), port, reached)
}

var (
	previewFailMu   sync.Mutex
	previewFailSeen = map[string]time.Time{}
)

// logPreviewFailure says, at most once in 30 seconds for a workspace and port, why a Preview could not reach the program: the processes that were
// found (with their network: "same" is the server's own) and each attempt with how it ended. A Preview that fails with no word in the log cannot
// be told from a program that is not there (reported: 502 on every port after a session moved to a worker).
func logPreviewFailure(home string, port int, pids []int, tried []string, err error) {
	key := fmt.Sprintf("%s|%d", home, port)
	previewFailMu.Lock()
	defer previewFailMu.Unlock()
	if len(previewFailSeen) > 512 {
		previewFailSeen = map[string]time.Time{}
	}
	if time.Since(previewFailSeen[key]) < 30*time.Second {
		return
	}
	previewFailSeen[key] = time.Now()
	facts := make([]string, 0, len(pids))
	for _, pid := range pids {
		facts = append(facts, fmt.Sprintf("%d(same=%v unsandboxed=%v)", pid, previewProc.same(pid), previewProc.unsandboxed(pid)))
	}
	log.Printf("preview: %s port %d failed: %v; processes %v; tried %v; emulation %v", filepath.Base(home), port, err, facts, tried, netEmuActive())
}
