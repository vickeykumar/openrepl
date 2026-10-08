package server

import (
	"bufio"
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/pkg/errors"

	"cookie"
	"tunnel"
	"utils"
)

// runWorker serves the handlers on the streams a gateway opens. The worker
// dials the gateway and keeps reconnecting, so it needs no inbound address.
// local, when not nil, is the worker's own port (see Options.LocalListen):
// the same handlers, but its visitors are not the gateway's.
func (server *Server) runWorker(ctx context.Context, handlers http.Handler, counter *counter, local *http.Server) error {
	id := server.options.WorkerID
	if id == "" {
		id, _ = os.Hostname()
	}
	server.options.WorkerID = id
	capacity := int64(server.options.WorkerCapacity)
	if capacity <= 0 {
		capacity = memoryCapacityMB()
	}
	// The worker enforces the budget it advertises with the server's own
	// admission check, which refuses a terminal with a message once the
	// running REPLs' memory weights exceed --max-connection.
	if server.options.MaxConnection == 0 {
		server.options.MaxConnection = int(capacity)
	}
	var languages []string
	for _, l := range strings.Split(server.options.WorkerLanguages, ",") {
		if l = strings.TrimSpace(l); l != "" {
			languages = append(languages, l)
		}
	}

	var client *tunnel.Client
	var err error
	client, err = tunnel.NewClient(tunnel.ClientConfig{
		ServerURL: server.options.WorkerServer,
		Token:     server.options.WorkerToken,
		HostKey:   server.options.WorkerHostKey,
		Register: tunnel.RegisterRequest{
			WorkerID:  id,
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			Languages: languages,
			Capacity:  capacity,
			Weight:    server.options.WorkerWeight,
			Ptrace:    probeHost("worker"),
		},
		Load: func() tunnel.Heartbeat {
			return tunnel.Heartbeat{Used: int64(counter.weight()), Active: counter.count()}
		},
		OnSyncStream: func(c net.Conn) { server.serveWorkerSync(ctx, client)(c) },
		OnConfig: applyWorkerConfig,
		OnRegistered: func(rep tunnel.RegisterReply) {
			if rep.WorkspaceSync {
				atomic.StoreInt32(&server.workerSync.enabled, 1)
				// A worker never decides on its own that a home expired: the
				// gateway tells it (a drop), or the files stay.
				utils.RemoveDirGuard = server.keepHome
			}
			// Held in memory only; replaced on every (re)connect.
			if len(rep.CookieSecret) > 0 && !bytes.Equal(rep.CookieSecret, cookie.SECRET_KEY) {
				cookie.SECRET_KEY = rep.CookieSecret
				cookie.Init_SessionStore(rep.CookieSecret)
			}
			server.setCredential(rep.AuthToken)
			// the gateway's OPENREPL_SECRET, in memory only, like the cookie secret
			utils.SetSecretFromGateway(rep.Secret)
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to set up the worker tunnel")
	}
	server.routes = newRouteTracker(client)

	srv := &http.Server{Handler: handlers, ConnContext: trustTunnel}
	go srv.Serve(client.Listener())

	log.Printf("Worker %s connecting to gateway %s (capacity %d MB, weight %d)",
		id, server.options.WorkerServer, capacity, server.options.WorkerWeight)
	if local != nil {
		log.Printf("Worker %s also serves its own port; sessions opened there stay on this worker", id)
	}
	err = client.Run(ctx)
	srv.Close()
	if local != nil {
		local.Close()
	}
	if m := server.workerSync.mgr; m != nil {
		m.Close()
	}

	if conn := counter.count(); conn > 0 {
		log.Printf("Waiting for %d connections to be closed", conn)
	}
	counter.wait()
	return err
}

// memoryCapacityMB is the default session budget of a worker: the machine's
// RAM in MB, the same unit as the per-command memory weights.
func memoryCapacityMB() int64 {
	const fallback = 2048
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return fallback
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil && kb > 0 {
				return kb / 1024
			}
		}
	}
	return fallback
}
