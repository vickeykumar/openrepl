package server

import (
	"encoding/base64"
	"log"
	"meters"
	"net/http"
	"strings"
)

func (server *Server) wrapLogger(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &logResponseWriter{w, 200}
		handler.ServeHTTP(rw, r)
		if server.isAdminPoll(r, rw.status) {
			return
		}
		// the dashboard's live map counts the site's own requests, not its polling
		meters.Add(meters.HTTPReq, 1)
		if rw.status >= 500 {
			meters.Add(meters.HTTPErr, 1)
		}
		log.Printf("%s %d %s %s", r.RemoteAddr, rw.status, r.Method, r.URL.Path)
	})
}

// isAdminPoll reports whether a request is the dashboard reading its data,
// which it does every few seconds and which would fill the log. A refused
// read and every change are still logged.
func (server *Server) isAdminPoll(r *http.Request, status int) bool {
	if r.Method != http.MethodGet || status >= 400 {
		return false
	}
	rel := strings.TrimPrefix(r.URL.Path, server.admin.prefix)
	return strings.HasPrefix(rel, "admin/")
}

func (server *Server) wrapHeaders(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// todo add version
		w.Header().Set("Server", "GoTTY")
		handler.ServeHTTP(w, r)
	})
}

func (server *Server) wrapBasicAuth(handler http.Handler, credential string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.SplitN(r.Header.Get("Authorization"), " ", 2)

		if len(token) != 2 || strings.ToLower(token[0]) != "basic" {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoTTY"`)
			http.Error(w, "Bad Request", http.StatusUnauthorized)
			return
		}

		payload, err := base64.StdEncoding.DecodeString(token[1])
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if credential != string(payload) {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoTTY"`)
			http.Error(w, "authorization failed", http.StatusUnauthorized)
			return
		}

		log.Printf("Basic Authentication Succeeded: %s", r.RemoteAddr)
		handler.ServeHTTP(w, r)
	})
}

func (server *Server) wrapAdmin(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if !server.isAdmin(rw, req) {
	        errorHandler(rw, req, "Unauthorized Access!! Please Sign in again as Admin.", http.StatusUnauthorized)
	        return
	    }
	    handler.ServeHTTP(rw, req)
	})
}

// staticCacheControl is what the files of the site are sent with: a few minutes in a browser or a CDN, then the file may still be used for a day while
// it is asked for again (stale-while-revalidate). The names of the files have no version, so it is kept short.
const staticCacheControl = "public, max-age=300, stale-while-revalidate=86400"

// cacheStatic sends the files of the site with a Cache-Control and the ETag of the build (the commit the binary was made from): a file that is asked
// for again with the same ETag is answered 304, with no body. Without a build commit there is no ETag and the files are only given the Cache-Control
// (the last-modified time of the files then lets a browser ask again).
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", staticCacheControl)
		if buildCommit != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			etag := `W/"` + buildCommit + `"`
			h.Set("ETag", etag)
			if m := r.Header.Get("If-None-Match"); m != "" && (m == etag || m == "*") {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// noStore marks an answer that is made for one visitor (a token, a cookie, a secret): nothing may keep it.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
