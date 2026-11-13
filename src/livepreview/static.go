package livepreview

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// MaxFile is the largest file the preview route sends.
const MaxFile = 50 << 20

func init() {
	// a few types the system table may lack or give a download for
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".md", "text/plain; charset=utf-8")
	mime.AddExtensionType(".wasm", "application/wasm")
}

// within reports whether resolved (a real path) is dir or below it.
func within(dir, resolved string) bool {
	return resolved == dir || strings.HasPrefix(resolved, dir+string(filepath.Separator))
}

// ServeFile sends the file rel of the workspace home, read-only, and nothing outside the workspace: not through
// "..", not through a link that leads out. A folder shows its index.html. The answer carries SandboxHeaders.
func ServeFile(rw http.ResponseWriter, req *http.Request, home, rel string) {
	ServeFileWith(rw, req, home, rel, nil)
}

// ServeFileWith is ServeFile with the storage of st for the pages it sends.
func ServeFileWith(rw http.ResponseWriter, req *http.Request, home, rel string, st *StorageInfo) {
	SandboxHeaders(rw.Header())
	if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
		rw.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(rw, "read-only", http.StatusMethodNotAllowed)
		return
	}
	if req.Method == http.MethodOptions {
		rw.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		rw.Header().Set("Access-Control-Allow-Headers", "*")
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.ContainsRune(rel, 0) {
		http.NotFound(rw, req)
		return
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		http.NotFound(rw, req)
		return
	}
	// path.Clean on a rooted path cannot climb above the root
	full := filepath.Join(realHome, filepath.FromSlash(path.Clean("/"+rel)))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil || !within(realHome, resolved) {
		http.NotFound(rw, req)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil {
		http.NotFound(rw, req)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(req.URL.Path, "/") {
			target := req.URL.Path + "/"
			if req.URL.RawQuery != "" {
				target += "?" + req.URL.RawQuery
			}
			http.Redirect(rw, req, target, http.StatusMovedPermanently)
			return
		}
		resolved = filepath.Join(resolved, "index.html")
		if resolved, err = filepath.EvalSymlinks(resolved); err != nil || !within(realHome, resolved) {
			http.Error(rw, "No index.html in this folder.", http.StatusNotFound)
			return
		}
		if info, err = os.Stat(resolved); err != nil || info.IsDir() {
			http.Error(rw, "No index.html in this folder.", http.StatusNotFound)
			return
		}
	}
	if !info.Mode().IsRegular() {
		http.NotFound(rw, req)
		return
	}
	if info.Size() > MaxFile {
		http.Error(rw, "file too large to preview", http.StatusRequestEntityTooLarge)
		return
	}
	f, err := os.Open(resolved)
	if err != nil {
		http.NotFound(rw, req)
		return
	}
	defer f.Close()
	switch req.URL.Query().Get("render") {
	case "md":
		serveMarkdown(rw, req, f, info, filepath.Base(resolved))
		return
	case "image":
		servePage(rw, req, ImagePage(filepath.Base(resolved)), "img-src 'self'")
		return
	}
	if strings.EqualFold(filepath.Ext(resolved), ".pdf") {
		// A PDF is not a page: it is sent as a PDF and cannot be anything else (nosniff), so the browser's own viewer
		// can show it; its viewer does not work under the sandbox policy, which a PDF has no use for.
		rw.Header().Del("Content-Security-Policy")
		rw.Header().Set("Content-Type", "application/pdf")
		rw.Header().Set("Content-Disposition", "inline")
	}
	if ext := strings.ToLower(filepath.Ext(resolved)); (ext == ".html" || ext == ".htm") && req.Method == http.MethodGet && info.Size() <= MaxBridged {
		// a page of the workspace gets the script that reports to the pane (see BridgeScript)
		if page, err := io.ReadAll(f); err == nil {
			out := InjectBridge(page)
			if sc := st.script(storageScope(rel)); sc != "" {
				out = injectAtTop(out, sc)
			}
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			rw.Header().Set("Content-Length", strconv.Itoa(len(out)))
			rw.Write(out)
			return
		}
		f.Seek(0, io.SeekStart)
	}
	if plainFile(req, resolved) && info.Size() <= MaxBridged {
		if text, err := io.ReadAll(f); err == nil {
			out := PlainPage(text)
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			rw.Header().Set("Content-Length", strconv.Itoa(len(out)))
			rw.Write(out)
			return
		}
		f.Seek(0, io.SeekStart)
	}
	http.ServeContent(rw, req, filepath.Base(resolved), info.ModTime(), f)
}

// MaxBridged is the largest page of the workspace that gets the script of the pane (a larger one is sent as it is).
const MaxBridged = 8 << 20

// MaxMarkdown is the largest Markdown file that is rendered (a larger one is shown as the plain text it is).
const MaxMarkdown = 2 << 20

// pagePolicy is for a page the server wrote: no script, nothing but its own style, and the pictures it is given.
func pagePolicy(img string) string {
	return "sandbox allow-popups allow-downloads; default-src 'none'; style-src 'unsafe-inline'; " + img
}

func servePage(rw http.ResponseWriter, req *http.Request, body, img string) {
	rw.Header().Set("Content-Security-Policy", pagePolicy(img))
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if req.Method == http.MethodHead {
		return
	}
	rw.Write([]byte(body))
}

func serveMarkdown(rw http.ResponseWriter, req *http.Request, f *os.File, info os.FileInfo, name string) {
	if info.Size() > MaxMarkdown {
		http.Error(rw, "this file is too large to render: open it in the editor", http.StatusRequestEntityTooLarge)
		return
	}
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(f, data); err != nil {
		http.Error(rw, "the file could not be read", http.StatusInternalServerError)
		return
	}
	servePage(rw, req, RenderMarkdown(string(data), name), "img-src 'self' https: http:")
}
