package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"scratch"
	"utils"
)

// The scratch folders of the terminals (LLD 18): the server makes the root, measures every live terminal's folder every 2 seconds, clears
// the caches of one that is over its soft cap, stops one that still is over it, and sweeps the folders of ended terminals. A soft cap only:
// no mount, no quota, no privilege.

type scratchStat struct {
	bytes   int64
	strikes int
}

var (
	scratchMu    sync.Mutex
	scratchStats = map[string]*scratchStat{} // folder -> last measure
	spaceMu      sync.Mutex
	spaceCache   = map[string]struct {
		mb int
		at time.Time
	}{}
)

// startScratch makes the root and starts the watcher; it does nothing where the root cannot be made (terminals then start with no scratch).
func startScratch(ctx context.Context) {
	root := os.Getenv(utils.EnvScratchDir)
	if root == "" {
		root = scratch.DefaultRoot
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		log.Printf("scratch: %s cannot be made (%v): terminals start with no scratch folder", root, err)
		return
	}
	root = utils.ScratchDir()
	if root == "" {
		return
	}
	scratch.Sweep(root) // what a server that stopped left behind
	go func() {
		measure := time.NewTicker(2 * time.Second)
		sweep := time.NewTicker(time.Minute)
		defer measure.Stop()
		defer sweep.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sweep.C:
				scratch.Sweep(root)
			case <-measure.C:
				watchScratch(root)
			}
		}
	}()
}

// watchScratch is one pass of the watcher.
func watchScratch(root string) {
	soft := int64(scratchSoftMB()) << 20
	serverCap := int64(scratchServerMB()) << 20
	list := scratch.List(root)
	var total int64
	type live struct {
		t scratch.Terminal
		n int64
	}
	var lives []live
	scratchMu.Lock()
	seen := map[string]bool{}
	for _, t := range list {
		if !t.Live {
			continue
		}
		n := scratch.Usage(t.Dir)
		total += n
		lives = append(lives, live{t, n})
		seen[t.Dir] = true
		st := scratchStats[t.Dir]
		if st == nil {
			st = &scratchStat{}
			scratchStats[t.Dir] = st
		}
		st.bytes = n
	}
	for d := range scratchStats {
		if !seen[d] {
			delete(scratchStats, d)
		}
	}
	scratchMu.Unlock()
	for _, l := range lives {
		over := l.n > soft
		if !over && total > serverCap {
			// the server's folders together are over their cap: the biggest ones are treated as over theirs
			over = l.n > serverCap/int64(len(lives))
		}
		scratchMu.Lock()
		st := scratchStats[l.t.Dir]
		if !over {
			st.strikes = 0
			scratchMu.Unlock()
			continue
		}
		scratchMu.Unlock()
		scratch.ClearCaches(l.t.Dir) // the caches go first
		after := scratch.Usage(l.t.Dir)
		scratchMu.Lock()
		st.bytes = after
		if after <= soft {
			st.strikes = 0
			scratchMu.Unlock()
			continue
		}
		st.strikes++
		stop := st.strikes >= 2 // still over after the caches were cleared, twice in a row
		scratchMu.Unlock()
		if stop && l.t.PID > 1 {
			log.Printf("scratch: the terminal %s (pid %d) used %d MB of scratch space (the soft cap is %d MB) and was stopped", filepath.Base(l.t.Dir), l.t.PID, after>>20, soft>>20)
			stopTerminal(l.t.PID, soft>>20)
		}
	}
}

// spaceOf is what a terminal's status bar shows for one workspace: its size against the quota, and the scratch folder of its biggest live terminal
// against the soft cap. The numbers are the user's own.
func spaceOf(home string) map[string]int {
	out := map[string]int{"workspaceMB": workspaceMB(home), "workspaceLimitMB": utils.WorkspaceQuotaMB, "scratchMB": 0, "scratchLimitMB": scratchSoftMB(), "scratch": 0}
	root := utils.ScratchDir()
	if root == "" {
		return out
	}
	out["scratch"] = 1
	var max int64
	scratchMu.Lock()
	for _, t := range scratch.List(root) {
		if t.User != home || !t.Live {
			continue
		}
		if st := scratchStats[t.Dir]; st != nil && st.bytes > max {
			max = st.bytes
		}
	}
	scratchMu.Unlock()
	out["scratchMB"] = int(max >> 20)
	return out
}

// workspaceMB is the size of a workspace, measured at most every 20 seconds.
func workspaceMB(home string) int {
	spaceMu.Lock()
	defer spaceMu.Unlock()
	if c, ok := spaceCache[home]; ok && time.Since(c.at) < 20*time.Second {
		return c.mb
	}
	mb := int(scratch.Usage(filepath.Join(utils.HOME_DIR, home)) >> 20)
	spaceCache[home] = struct {
		mb int
		at time.Time
	}{mb, time.Now()}
	if len(spaceCache) > 2000 {
		spaceCache = map[string]struct {
			mb int
			at time.Time
		}{}
	}
	return mb
}

// handleStoreSpace answers GET /store/api/space for anyone with a workspace, guests too: the numbers of the status bar.
func (server *Server) handleStoreSpace(w http.ResponseWriter, r *http.Request) {
	uid, homedir := server.requestIdentity(w, r)
	_ = uid
	if homedir == "" || filepath.Dir(filepath.Clean(homedir)) != filepath.Clean(utils.HOME_DIR) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no workspace"})
		return
	}
	writeJSON(w, http.StatusOK, spaceOf(filepath.Base(homedir)))
}

// stopTerminal tells the terminal why it ends, then stops it. The message goes to the terminal's own screen (the pty its shell has open) and does not
// name the server. An interactive shell ignores SIGTERM, so it gets SIGHUP, which a terminal that is closed gets too, and SIGKILL if it is still there.
func stopTerminal(pid int, capMB int64) {
	if tty, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/1"); err == nil && strings.HasPrefix(tty, "/dev/pts/") {
		if f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0); err == nil {
			fmt.Fprintf(f, "\r\n\x1b[31mThis terminal used its %d MB of scratch space and was stopped. Your files are safe.\x1b[0m\r\n", capMB)
			f.Close()
		}
	}
	syscall.Kill(pid, syscall.SIGHUP)
	go func() {
		time.Sleep(3 * time.Second)
		if scratch.Alive(pid) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
}
