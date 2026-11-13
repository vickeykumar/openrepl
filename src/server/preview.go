package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"containers"
	"livepreview"
	"netemu"
	"utils"
)

// The Preview pane (docs/preview-pane-design.md). The pane's frame is sandboxed and has no origin, so none of the
// visitor's cookies reach the route it loads: what names the workspace is a token in the address (package preview).

// previewTokenLife is how long a preview link works: the admin's setting (Settings, Preview), or the built-in 2 hours.
func previewTokenLife(s SiteSettings) time.Duration {
	if d := s.previewLinkLife(); d != 0 {
		return d
	}
	return livepreview.TokenLife
}

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
	life := previewTokenLife(GetSiteSettings())
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
		pid := livepreview.FindPID(home)
		if pid == 0 {
			return nil, livepreview.ErrNoProgram
		}
		if !livepreview.SameNetwork(pid) {
			return livepreview.DialIn(fmt.Sprintf("/proc/%d/ns/net", pid), port, 10*time.Second)
		}
		if containers.NetEmuActive() {
			var d net.Dialer
			return d.DialContext(ctx, "unix", netemu.Name(netemu.WorkspaceID(home), "t", uint16(port)))
		}
		return nil, errNoIsolation
	}
}

// previewPorts lists the ports a workspace's programs listen on, from the sockets of its own network.
func previewPorts(home string) []int {
	pid := livepreview.FindPID(home)
	if pid == 0 {
		return []int{}
	}
	if !livepreview.SameNetwork(pid) {
		base := fmt.Sprintf("/proc/%d/net/", pid)
		all := append(livepreview.ParseTCP(livepreview.ReadProc(base+"tcp")), livepreview.ParseTCP(livepreview.ReadProc(base+"tcp6"))...)
		return uniqueSorted(all)
	}
	if containers.NetEmuActive() {
		// the names of the workspace's TCP sockets all start "@openrepl-ne/<workspace>/t/", then the port
		prefix := strings.TrimSuffix(netemu.Name(netemu.WorkspaceID(home), "t", 0), "0")
		return livepreview.ParseUnixAbstract(livepreview.ReadProc("/proc/net/unix"), prefix)
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
