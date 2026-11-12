package livepreview

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	http.ServeContent(rw, req, filepath.Base(resolved), info.ModTime(), f)
}
