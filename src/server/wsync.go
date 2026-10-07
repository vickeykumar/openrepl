package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"

	"tunnel"
	"utils"
	"wsync"
)

// workspaceWait is how long a request waits for its home to be in step
// before it gets "workspace is synchronizing".
const workspaceWait = 10 * time.Second

// defaultSyncStateDir is where the base records of workspace sync are kept.
const defaultSyncStateDir = utils.GOTTY_PATH + "/wsync"

func (server *Server) syncStateDir() string {
	if d := server.options.SyncStateDir; d != "" {
		return d
	}
	return defaultSyncStateDir
}

// homeNameOf returns the home a path belongs to: the first element under
// utils.HOME_DIR. It returns "" for a path outside it.
func homeNameOf(dir string) string {
	base := filepath.Clean(utils.HOME_DIR)
	rel, err := filepath.Rel(base, filepath.Clean(dir))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	name := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	if wsync.ValidHomeName(name) != nil {
		return ""
	}
	return name
}

// workerSyncState is the workspace-sync state of a worker.
type workerSyncState struct {
	enabled int32 // atomic: the gateway keeps a copy of our homes
	once    sync.Once
	mgr     *wsync.Manager
	err     error
}

func (s *workerSyncState) on() bool { return atomic.LoadInt32(&s.enabled) == 1 }

// manager creates the worker's synchronization manager the first time it is
// needed, so a worker whose gateway does not use workspace sync never pays
// for a file watcher.
func (server *Server) workerSyncManager() (*wsync.Manager, error) {
	s := &server.workerSync
	s.once.Do(func() {
		s.mgr, s.err = wsync.NewManager(wsync.ManagerConfig{
			Node:      server.options.WorkerID,
			IsGateway: false,
			BaseDir:   utils.HOME_DIR,
			StateDir:  server.syncStateDir(),
			OnDrop: func(home string) {
				log.Printf("Workspace sync: the gateway dropped home %s; removing it", home)
				utils.RemoveDirNow(utils.HOME_DIR + home)
			},
			Logf: log.Printf,
		})
	})
	return s.mgr, s.err
}

// serveWorkerSync is the handler of the synchronization channel the gateway
// opens on a worker. It runs the conversation, and tells the gateway the
// worker may take sessions once its homes are in step.
func (server *Server) serveWorkerSync(ctx context.Context, client *tunnel.Client) func(net.Conn) {
	return func(conn net.Conn) {
		m, err := server.workerSyncManager()
		if err != nil {
			log.Printf("Workspace sync: cannot start: %v", err)
			conn.Close()
			return
		}
		err = m.Serve(ctx, "gateway", conn, func() {
			if !client.SyncReady() {
				log.Printf("Workspace sync: the gateway did not acknowledge that this worker is ready")
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("Workspace sync: the conversation with the gateway ended: %v", err)
		}
	}
}

// keepHome is the worker's utils.RemoveDirGuard. A home the gateway keeps a
// copy of is the gateway's to expire, so the worker keeps it. A home that was
// never synchronized, such as one made for a visitor of the worker's own
// port, expires on the worker's own timer like on a standalone server.
func (server *Server) keepHome(dir string) bool {
	home := homeNameOf(dir)
	if home == "" {
		return false
	}
	m, err := server.workerSyncManager()
	if err != nil {
		return true // cannot tell: keep the files
	}
	return m.HasRecord(home)
}

// waitWorkspace makes a worker wait until the home a request uses is in step
// with the gateway's copy, so a session never starts on an unsynchronized
// home. It does nothing when sync is off, or for a request that did not come
// from the gateway: a visitor of the worker's own port has a home of their
// own, which is not synchronized.
func (server *Server) waitWorkspace(r *http.Request, homedir string) error {
	err := server.waitWorkspaceHome(r, homedir)
	if err != nil {
		// the visitor is only told that the workspace is synchronizing: the reason is here
		log.Printf("workspace sync: the home %q is not ready for %s: %v", homeNameOf(homedir), r.URL.Path, err)
	}
	return err
}

func (server *Server) waitWorkspaceHome(r *http.Request, homedir string) error {
	s := &server.workerSync
	if !s.on() || !isTrusted(r) {
		return nil
	}
	home := homeNameOf(homedir)
	if home == "" {
		return nil
	}
	m, err := server.workerSyncManager()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), workspaceWait)
	defer cancel()
	// Right after a (re)connect the conversation may not exist yet.
	for !m.Connected("gateway") {
		select {
		case <-ctx.Done():
			return errors.New("workspace sync is not connected yet")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return m.EnsureHome(ctx, "gateway", home)
}
