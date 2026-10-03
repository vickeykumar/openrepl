package server

import (
	"encoding/base64"
	"log"
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
