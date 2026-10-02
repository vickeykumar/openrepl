package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"

	"containers"
	"cookie"
	"gateway"
	"pkg/homedir"
	"trusted"
	"tunnel"
	"user"
	"utils"
)

// localRoutes records the keys owned by the gateway's own backend.
type localRoutes struct{ routes *gateway.RouteMap }

func (l localRoutes) RouteOpen(kind, key string)  { l.routes.Set(kind, key, gateway.LocalID) }
func (l localRoutes) RouteClose(kind, key string) { l.routes.Delete(kind, key, gateway.LocalID) }

// wrapGateway puts the gateway router in front of the full handler tree. The
// router decides, per request, whether it runs here or on the worker that
// owns the session. Workers are accepted only when a worker token is set.
func (server *Server) wrapGateway(ctx context.Context, site http.Handler, pathPrefix string, counter *counter) (http.Handler, error) {
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
