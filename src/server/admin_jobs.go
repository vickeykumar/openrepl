package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"utils"
)

// The dashboard's Jobs view and the job scheduler node of the live map:
//
//	GET /admin/jobs
//
// Two kinds of job. The recurring ones (utils.Jobs: the saver of the waiting removals, the sweep of
// forgotten guest folders, the /tmp cleaner, the reaper, the settings poll, the gateway's guest expiry)
// report each run. The removals of a guest's folder, an hour after the last visit, are the jobs of
// utils.GottyJobs, and are counted and listed from there. Nothing is stored: a restart starts again.

const maxRemovalsListed = 20

type removalItem struct {
	Folder string `json:"folder"` // the folder's name, not its path
	DueAt  int64  `json:"dueAt"`  // Unix seconds
}

type removalsInfo struct {
	Pending     int           `json:"pending"`
	NextAt      int64         `json:"nextAt"`
	Fired       int64         `json:"fired"`
	LastFiredAt int64         `json:"lastFiredAt"`
	Items       []removalItem `json:"items"`
}

type jobsReply struct {
	At       int64           `json:"at"`
	Jobs     []utils.JobInfo `json:"jobs"`
	Removals removalsInfo    `json:"removals"`
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// currentJobs is what the Jobs view and the map show now.
func currentJobs(now time.Time) jobsReply {
	reply := jobsReply{At: now.Unix(), Jobs: utils.Jobs.Snapshot(now), Removals: removalsInfo{Items: []removalItem{}}}
	if utils.GottyJobs == nil {
		return reply
	}
	st := utils.GottyJobs.Stats()
	reply.Removals.Pending = st.Pending
	reply.Removals.NextAt = unixOrZero(st.Next)
	reply.Removals.Fired = st.Fired
	reply.Removals.LastFiredAt = unixOrZero(st.LastFired)
	for _, p := range utils.GottyJobs.Waiting() {
		if len(reply.Removals.Items) >= maxRemovalsListed {
			break
		}
		if !strings.HasPrefix(p.Name, utils.REMOVE_JOB_KEY) {
			continue
		}
		reply.Removals.Items = append(reply.Removals.Items, removalItem{
			Folder: filepath.Base(strings.TrimPrefix(p.Name, utils.REMOVE_JOB_KEY)),
			DueAt:  p.DueAt.Unix(),
		})
	}
	return reply
}

// handleAdminJobs lists the recurring jobs and the removals that wait (GET). It runs behind adminAPI.
func (server *Server) handleAdminJobs(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		rw.Header().Set("Allow", "GET")
		adminError(rw, http.StatusMethodNotAllowed, "Use GET.")
		return
	}
	adminJSON(rw, http.StatusOK, currentJobs(time.Now()))
}
