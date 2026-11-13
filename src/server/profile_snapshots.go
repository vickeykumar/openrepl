package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"homesnap"
	"utils"
)

// The signed-in user's own workspace snapshots (the profile page): the list read from the object store at the time, getting
// one back into a new folder or as a zip, or making the workspace exactly that snapshot. The home is always the one of the
// signed-in account: the request names a snapshot id and nothing else.

type snapshotList struct {
	Enabled   bool            `json:"enabled"`
	Keep      int             `json:"keep"`
	Minutes   int             `json:"minutes"` // how often a snapshot is made
	Snapshots []homesnap.Info `json:"snapshots"`
	Error     string          `json:"error,omitempty"`
}

// snapshotHome is the home of the signed-in account, or "" after the answer has been written.
func (server *Server) snapshotHome(w http.ResponseWriter, r *http.Request) string {
	uid, homedir := server.requestIdentity(w, r)
	if uid == "" {
		http.Error(w, "Sign in to see your workspace snapshots.", http.StatusUnauthorized)
		return ""
	}
	if filepath.Dir(filepath.Clean(homedir)) != filepath.Clean(utils.HOME_DIR) || !homesnap.IsKept(filepath.Base(homedir)) {
		http.Error(w, "This workspace is not one that snapshots are kept of.", http.StatusForbidden)
		return ""
	}
	return filepath.Base(homedir)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// sameOrigin says that a request that changes things comes from this site's own page: it is JSON (a form cannot send that
// without a preflight) and, when the browser says where it comes from, from this host.
func sameOrigin(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

func snapshotError(err error) (int, string) {
	switch {
	case errors.Is(err, homesnap.ErrNotAvailable):
		return http.StatusNotFound, "This server keeps no workspace snapshots."
	case errors.Is(err, homesnap.ErrNoSuchSnapshot):
		return http.StatusNotFound, "That snapshot is no longer there. Refresh the list."
	case errors.Is(err, homesnap.ErrTooSoon), errors.Is(err, homesnap.ErrBusy):
		return http.StatusTooManyRequests, err.Error()
	}
	log.Println("profile snapshots:", redactErr(err))
	if errors.Is(err, homesnap.ErrBadSnapshot) {
		return http.StatusUnprocessableEntity, "That snapshot cannot be read as a whole. Download it as a zip, or pick another one."
	}
	return http.StatusBadGateway, "The object store did not answer. Try again in a minute."
}

// GET /profile/snapshots
func (server *Server) handleProfileSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	home := server.snapshotHome(w, r)
	if home == "" {
		return
	}
	reply := snapshotList{Keep: homesnap.Keep, Minutes: int(homesnap.Default.CurrentInterval() / time.Minute), Snapshots: []homesnap.Info{}}
	if !homesnap.Default.Enabled() {
		writeJSON(w, http.StatusOK, reply)
		return
	}
	reply.Enabled = true
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	infos, err := homesnap.Default.List(ctx, home)
	if err != nil {
		status, msg := snapshotError(err)
		reply.Error = msg
		writeJSON(w, status, reply)
		return
	}
	if infos != nil {
		reply.Snapshots = infos
	}
	writeJSON(w, http.StatusOK, reply)
}

type restoreRequest struct {
	ID      string `json:"id"`
	Mode    string `json:"mode"` // "folder" or "replace"
	Confirm string `json:"confirm"`
}

// POST /profile/snapshots/restore
func (server *Server) handleProfileSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "This request must come from the profile page.", http.StatusForbidden)
		return
	}
	home := server.snapshotHome(w, r)
	if home == "" {
		return
	}
	var req restoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "Say which snapshot.", http.StatusBadRequest)
		return
	}
	if req.Mode == "replace" && req.Confirm != "REPLACE" {
		http.Error(w, "Type REPLACE to replace your workspace.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := homesnap.Default.Restore(ctx, home, req.ID, req.Mode)
	if err != nil {
		status, msg := snapshotError(err)
		http.Error(w, msg, status)
		return
	}
	log.Printf("profile snapshots: %s got back %s (%s): %d files %s", home, req.ID, req.Mode, res.Files, res.Folder)
	writeJSON(w, http.StatusOK, res)
}

// GET /profile/snapshots/zip?id=<id>
func (server *Server) handleProfileSnapshotZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	home := server.snapshotHome(w, r)
	if home == "" {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Say which snapshot.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	data, info, err := homesnap.Default.Archive(ctx, home, id)
	if err != nil {
		status, msg := snapshotError(err)
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.zip"`, home, info.At.UTC().Format("2006-01-02-1504")))
	w.Header().Set("Cache-Control", "no-store")
	if err := homesnap.ToZip(data, w); err != nil {
		log.Println("profile snapshots: the zip of", home, id, "stopped:", err)
	}
}
