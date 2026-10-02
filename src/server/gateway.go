package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"containers"
	"cookie"
	"gateway"
	"pkg/homedir"
	"trusted"
	"tunnel"
	"user"
	"utils"
	"wsync"
)

// localRoutes records the keys owned by the gateway's own backend.
type localRoutes struct{ routes *gateway.RouteMap }

func (l localRoutes) RouteOpen(kind, key string)  { l.routes.Set(kind, key, gateway.LocalID) }
func (l localRoutes) RouteClose(kind, key string) { l.routes.Delete(kind, key, gateway.LocalID) }

// wrapGateway puts the gateway router in front of the full handler tree. The
// router decides, per request, whether it runs here or on the worker that
// owns the session. Workers are accepted only when a worker token is set.
func (server *Server) wrapGateway(ctx context.Context, site http.Handler, pathPrefix string, counter *counter) (http.Handler, error) {
	// The sync manager is created below, once the router exists; the router's
	// hooks reach it through this variable.
	var syncMgr *wsync.Manager
	cfg := gateway.Config{
		Site:       site,
		PathPrefix: pathPrefix,
		IsEntryPage: func(rel string) bool {
			if rel == "" || rel == "practice" {
				return true
			}
			_, isLangPage := langPageFor("/" + strings.TrimPrefix(rel, "/"))
			return isLangPage
		},
		// An expired or signed-out session is a guest, as it is for the
		// workspace lookup in cookie.GetOrUpdateHomeDir.
		UID: func(r *http.Request) string {
			if !cookie.Is_UserLoggedIn(r) || cookie.IsSessionExpired(r) {
				return ""
			}
			return cookie.Get_Uid(r)
		},
		Secret:   cookie.SECRET_KEY,
		GuestTTL: utils.DEADLINE_MINUTES * time.Minute,
		Local: gateway.LocalConfig{
			Weight: server.options.LocalWeight,
			Capacity: func() (int64, int64) {
				return int64(counter.weight()), int64(server.options.MaxConnection)
			},
		},
		Pins: userPins{},
		Terminal: func(rel string) (string, int64, bool) {
			command, ok := server.terminals[rel]
			return command, containers.GetCommandWieght(command), ok
		},
		HasLocalWorkspace: func(uid string) bool {
			fi, err := os.Stat(utils.HOME_DIR + user.HomeDirID(uid))
			return err == nil && fi.IsDir()
		},
		Identity: func(w http.ResponseWriter, r *http.Request, ec gateway.ExecutionContext) trusted.Identity {
			id := trusted.Identity{UID: ec.UID, Guest: ec.GuestID, Session: ec.Key, Privilege: utils.GUEST}
			if ec.UID != "" {
				id.HomeID = user.HomeDirID(ec.UID)
				if IsUserAdmin(w, r) {
					id.Privilege = utils.ADMIN
				}
			}
			return id
		},
	}

	cfg.TerminalNotice = server.terminalNotice
	if server.options.WorkspaceSync {
		// How long a lost worker may stay away before its sessions are placed
		// again. The option was validated at start-up.
		cfg.RelocateAfter, _ = server.options.RelocateAfterDuration()
		// The name of a session's home, so the router can serve its files
		// from the gateway's copy while the worker is away, and the sync
		// manager can check which worker owns which home. It is the name the
		// worker uses for the same session (trusted.HomeDir).
		cfg.HomeOf = func(id gateway.Identity) string {
			if id.UID != "" {
				return user.HomeDirID(id.UID)
			}
			return "guest-" + id.GuestID
		}
		// A session placed on a worker first has the worker's copy of its home
		// made to match the gateway's: this is how a user moved to another
		// worker finds their files. A home the gateway has no files for needs
		// nothing; the worker creates it.
		cfg.PrepareHome = func(ctx context.Context, home, backendID string) error {
			m := syncMgr
			if m == nil {
				return nil
			}
			if _, err := os.Stat(utils.HOME_DIR + home); os.IsNotExist(err) {
				return nil
			}
			return m.EnsureHome(ctx, backendID, home)
		}
		// A user placed on another worker: the old worker's copy is dropped.
		// The gateway's copy stays; it is what the new worker receives.
		cfg.OnMoved = func(home, from, to string) {
			log.Printf("Workspace sync: home %s moves from worker %s to %s", home, from, to)
			if m := syncMgr; m != nil {
				if err := m.Drop(from, home); err != nil {
					log.Printf("Workspace sync: dropping %s on %s: %v", home, from, err)
				}
			}
		}
	}

	router := gateway.NewRouter(cfg)
	server.routes = newRouteTracker(localRoutes{router.Routes()})
	if server.options.LocalWeight <= 0 {
		log.Printf("Gateway mode: --local-weight is 0, new sessions run on workers only")
	}

	var ts *tunnel.Server
	if server.options.WorkerToken == "" {
		log.Printf("Gateway mode: no --worker-token set, workers are disabled and sessions run locally")
	} else {
		keyPath := homedir.Expand(server.options.TunnelHostKey)
		hostKey, err := tunnel.LoadOrCreateHostKey(keyPath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load the tunnel host key `%s`", keyPath)
		}
		tcfg := tunnel.ServerConfig{
			Token:        server.options.WorkerToken,
			HostKey:      hostKey,
			CookieSecret: func() []byte { return cookie.SECRET_KEY },
			AuthToken:    func() string { return server.options.Credential },
		}
		router.BindTunnel(&tcfg)
		if server.options.WorkspaceSync {
			mgr, err := wsync.NewManager(wsync.ManagerConfig{
				Node: "gateway", IsGateway: true,
				BaseDir: utils.HOME_DIR, StateDir: server.syncStateDir(),
				OwnerOf: router.OwnerOf,
				Logf:    log.Printf,
			})
			if err != nil {
				return nil, errors.Wrap(err, "failed to start workspace sync")
			}
			syncMgr = mgr
			go func() {
				<-ctx.Done()
				mgr.Close()
			}()
			// Only the gateway decides when a guest's home expires. The idle
			// timers of single nodes would delete a home that is in use on a
			// worker, and the deletion would then be synchronized.
			utils.RemoveDirGuard = func(dir string) bool {
				home := homeNameOf(dir)
				if home == "" {
					return false
				}
				if owner, known := router.OwnerOf(home); known {
					return owner != gateway.LocalID
				}
				return mgr.HasRecord(home)
			}
			go server.expireGuests(ctx, router, mgr)
			tcfg.WorkspaceSync = true
			// BindTunnel set OnOnline to register the worker as a backend; the
			// worker is SYNCING from here, and the gateway now opens the sync
			// channel to it.
			addBackend := tcfg.OnOnline
			tcfg.OnOnline = func(w *tunnel.Worker) {
				addBackend(w)
				go server.syncWithWorker(ctx, mgr, w)
			}
			log.Printf("Gateway mode: workspace sync is on (records in %s, sessions move after a worker has been away %v)",
				server.syncStateDir(), cfg.RelocateAfter)
		}
		ts, err = tunnel.NewServer(tcfg)
		if err != nil {
			return nil, err
		}
		router.SetTunnel(server.options.TunnelPath, ts)
		log.Printf("Gateway mode: workers connect to %s%s (tunnel host key %s)",
			pathPrefix, strings.Trim(server.options.TunnelPath, "/"), tunnel.Fingerprint(hostKey))
	}

	server.gatewayAdmin = router.AdminHandler(ts)

	if ts != nil && server.options.TunnelAddr != "" {
		l, err := net.Listen("tcp", server.options.TunnelAddr)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to listen for workers at `%s`", server.options.TunnelAddr)
		}
		log.Printf("Gateway mode: also accepting workers over ssh://%s", l.Addr())
		go ts.Serve(l)
		go func() {
			<-ctx.Done()
			l.Close()
			ts.Close()
		}()
	} else if ts != nil {
		go func() {
			<-ctx.Done()
			ts.Close()
		}()
	}
	return router, nil
}

// expireGuests deletes the homes of guests that have been idle for the guest
// lifetime, on the gateway and on the worker that held them, once a minute.
// A guest who still has a terminal open is never expired.
func (server *Server) expireGuests(ctx context.Context, router *gateway.Router, mgr *wsync.Manager) {
	ttl := utils.DEADLINE_MINUTES * time.Minute
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, e := range router.ExpireGuests(now, ttl) {
				log.Printf("Workspace sync: guest home %s expired (%s)", e.Home, e.Backend)
				if e.Backend != gateway.LocalID {
					if err := mgr.Drop(e.Backend, e.Home); err != nil {
						log.Printf("Workspace sync: dropping %s: %v", e.Home, err)
					}
				}
				utils.RemoveDirNow(utils.HOME_DIR + e.Home)
			}
			// Guest homes the gateway no longer remembers (it restarted) and
			// that nobody has touched for the guest lifetime.
			for _, st := range mgr.StaleHomes(ttl) {
				if !strings.HasPrefix(st.Home, "guest-") {
					continue // a signed-in user's home never expires
				}
				if _, active := router.OwnerOf(st.Home); active {
					continue
				}
				log.Printf("Workspace sync: stale guest home %s (idle %v, worker %s)", st.Home, st.Age.Round(time.Minute), st.Peer)
				mgr.Drop(st.Peer, st.Home)
				utils.RemoveDirNow(utils.HOME_DIR + st.Home)
			}
		}
	}
}

// terminalNotice answers a terminal's WebSocket request by closing it with a
// reason that says the execution node is away and for how long, so the page
// can show a countdown instead of a bare "connection closed" that invites
// pressing Reconnect at once. It reports whether it handled the response:
// when the upgrade fails, the upgrader has already answered the request.
func (server *Server) terminalNotice(w http.ResponseWriter, r *http.Request, retryIn time.Duration) bool {
	conn, err := server.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return true
	}
	defer conn.Close()
	conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, gateway.AwayReason(retryIn)),
		time.Now().Add(time.Second))
	// Let the page read the close and answer it before the connection goes
	// away; it sends its init message first.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return true
		}
	}
}

// syncWithWorker runs the workspace synchronization conversation with a
// worker for as long as it is connected.
func (server *Server) syncWithWorker(ctx context.Context, mgr *wsync.Manager, w *tunnel.Worker) {
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := w.DialSync(dctx)
	cancel()
	if err != nil {
		log.Printf("Workspace sync: cannot open the sync channel to worker %s: %v (the worker stays out of rotation)", w.ID(), err)
		return
	}
	err = mgr.Serve(ctx, w.ID(), conn, nil)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		log.Printf("Workspace sync: the conversation with worker %s ended: %v", w.ID(), err)
	}
	if w.Online() {
		// The worker is still connected but its files are no longer being
		// kept in step: it would stay out of rotation until the sync timeout
		// (or, once ready, run without a copy). Reconnecting starts a new
		// conversation.
		log.Printf("Workspace sync: asking worker %s to reconnect", w.ID())
		w.Disconnect()
	}
}

// userPins keeps each signed-in user's execution node in the user database,
// so the user returns to the same workspace after a gateway restart.
type userPins struct{}

func (userPins) Get(uid string) (string, bool) { return user.GetWorkerPin(uid) }

func (userPins) Set(uid, node string) {
	if err := user.SetWorkerPin(uid, node); err != nil {
		log.Println("Error: saving the execution node of user ", uid, ": ", err)
	}
}

// handleGatewayAdmin serves the worker and session administration API.
func (server *Server) handleGatewayAdmin(w http.ResponseWriter, r *http.Request) {
	if server.gatewayAdmin == nil {
		http.NotFound(w, r)
		return
	}
	server.gatewayAdmin.ServeHTTP(w, r)
}

// ownsWorkspace reports whether this process holds the requester's files. A
// gateway does not for a session that runs on a worker.
func ownsWorkspace(r *http.Request) bool {
	b := gateway.BackendOf(r)
	return b == "" || b == gateway.LocalID
}
