package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"appstore"
	"containers"
	"gateway"
	"utils"
)

// Turning an app on is checked first (LLD 18): the verification of the app runs in the sandbox of the user, with the user's
// environment, on the node where the user's terminals run (a worker's own copy is proved on the worker, through the tunnel it already
// has). Only when it passes is the app on; when it does not it is off again and the user may report it to the admin.

type verifyRequest struct {
	Home string `json:"home"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type verifyReply struct {
	OK    bool   `json:"ok"`
	Wait  bool   `json:"wait,omitempty"` // the node does not have the app yet: not a fault, try again in a moment
	Error string `json:"error,omitempty"`
}

// localVerify runs the check on this node, in the sandbox of the user's workspace.
func localVerify(ctx context.Context, st appstore.Store, home, id, name, kind string) error {
	workspace := filepath.Join(utils.HOME_DIR, home)
	return appstore.RunVerify(ctx, st, home, id, name, kind, workspace, func(argv []string) []string {
		return containers.WrapForVerification(argv, workspace, id)
	})
}

// handleStoreVerify is what a worker answers the gateway over the tunnel: only a request that came through it is taken.
func (server *Server) handleStoreVerify(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	// it must come through the tunnel AND carry the shared worker token: a visitor's request that a gateway forwards has the first only
	if !isTrusted(r) || server.options.WorkerToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(server.options.WorkerToken)) != 1 ||
		r.Method != http.MethodPost || svc == nil {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	var in verifyRequest
	if json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in) != nil || !appstore.Kept(in.Home) || !appstore.ValidBundle(in.ID) || !appstore.ValidName(in.Name) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := localVerify(r.Context(), svc.Store(), in.Home, in.ID, in.Name, in.Kind)
	rep := verifyReply{OK: err == nil}
	if err != nil {
		rep.Error, rep.Wait = err.Error(), errors.Is(err, appstore.ErrNotHere)
	}
	writeJSON(w, http.StatusOK, rep)
}

// verifyOnNode runs the check where the user's terminals are: on the worker that holds the user's workspace, else here. It returns the
// node it ran on and what went wrong (nil: it works).
func (server *Server) verifyOnNode(ctx context.Context, svc *appstore.Service, home string, a appstore.App) (node string, wait bool, err error) {
	node = "gateway"
	if r := server.admin.router; r != nil && server.admin.tunnel != nil {
		if owner, known := r.OwnerOf(home); known && owner != gateway.LocalID {
			if tw := server.admin.tunnel.Worker(owner); tw != nil {
				rep, derr := callWorkerVerify(ctx, server.options.WorkerToken, func(c context.Context) (net.Conn, error) { return tw.Dial(c) }, verifyRequest{Home: home, ID: a.ID, Name: a.Name, Kind: a.Kind})
				if derr != nil {
					return owner, false, errors.New("the server your terminal runs on could not be asked: " + derr.Error())
				}
				if rep.OK {
					return owner, false, nil
				}
				return owner, rep.Wait, errors.New(rep.Error)
			}
		}
	}
	err = localVerify(ctx, svc.Store(), home, a.ID, a.Name, a.Kind)
	return node, errors.Is(err, appstore.ErrNotHere), err
}

// callWorkerVerify posts the request to a worker over a connection of the tunnel.
func callWorkerVerify(ctx context.Context, token string, dial func(context.Context) (net.Conn, error), in verifyRequest) (verifyReply, error) {
	var rep verifyReply
	body, _ := json.Marshal(in)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext:       func(c context.Context, _, _ string) (net.Conn, error) { return dial(c) },
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://worker/store/verify", bytes.NewReader(body))
	if err != nil {
		return rep, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		return rep, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return rep, errors.New("it answered " + res.Status)
	}
	return rep, json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&rep)
}

// useFailures remember, for a short while, what the check said when a user could not turn an app on, so that "Report to admin" can
// send it (the user's report names the app, not the log).
var useFailures = struct {
	sync.Mutex
	m map[string]useFailure
}{m: map[string]useFailure{}}

type useFailure struct {
	node, log string
	at        time.Time
}

func rememberUseFailure(uid, name, node, log string) {
	useFailures.Lock()
	defer useFailures.Unlock()
	for k, f := range useFailures.m {
		if time.Since(f.at) > time.Hour {
			delete(useFailures.m, k)
		}
	}
	useFailures.m[uid+"|"+name] = useFailure{node: node, log: log, at: time.Now()}
}

func takeUseFailure(uid, name string) (useFailure, bool) {
	useFailures.Lock()
	defer useFailures.Unlock()
	f, ok := useFailures.m[uid+"|"+name]
	if ok && time.Since(f.at) > time.Hour {
		ok = false
	}
	delete(useFailures.m, uid+"|"+name)
	return f, ok
}
