package server

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"sort"
	"sync"
	"time"

	"appstore"
	"tunnel"
)

// The App Store catalog travels over the worker's tunnel, not over HTTP (LLD 18):
//
//   - a worker that connects says in its heartbeat which revision of the catalog it has (0 at first); the gateway answers with the
//     whole catalog when the revision is not its own, and with nothing when it is. A catalog is a few KB, so it is sent whole: a
//     missed message heals itself with the next heartbeat, and no worker ever asks again and again;
//   - the worker installs what is missing, by itself, and tells the gateway how it stands (ReqStoreState) after each bundle;
//   - a bundle that fails is tried again after 1, 2, 5, 15, then 60 minutes by the worker itself (appstore.Backoff).

// catalogTTL is the longest the gateway reuses the catalog it made: a change of an admin or a user reaches the workers within this
// time and the next heartbeat.
const catalogTTL = 15 * time.Second

var storeCatalog struct {
	sync.Mutex
	at  time.Time
	rev int64
	raw json.RawMessage
}

// catalogNow is the catalog of this server as JSON and its revision (a hash of the content), made again at most every catalogTTL.
func catalogNow(svc *appstore.Service) (int64, json.RawMessage) {
	storeCatalog.Lock()
	defer storeCatalog.Unlock()
	if storeCatalog.raw == nil || time.Since(storeCatalog.at) > catalogTTL {
		snap := svc.Snapshot()
		sort.Slice(snap.Apps, func(i, j int) bool { return snap.Apps[i].ID < snap.Apps[j].ID })
		raw, err := json.Marshal(snap)
		if err != nil {
			return 0, nil
		}
		h := fnv.New64a()
		h.Write(raw)
		rev := int64(h.Sum64() >> 1)
		if rev == 0 {
			rev = 1
		}
		storeCatalog.at, storeCatalog.rev, storeCatalog.raw = time.Now(), rev, raw
	}
	return storeCatalog.rev, storeCatalog.raw
}

// storeUpdateFor is what the gateway answers a heartbeat of worker id that has the catalog revision have: nil when it is up to date
// and no command waits for it. A heartbeat also shows that the worker is there.
func storeUpdateFor(svc *appstore.Service, id string, have int64) *tunnel.StoreUpdate {
	if svc == nil || !nodeIDPattern.MatchString(id) {
		return nil
	}
	rev, raw := catalogNow(svc)
	appStoreMu.Lock()
	n := appNodes[id]
	n.Seen = time.Now()
	appNodes[id] = n
	cmds := appCommands[id]
	delete(appCommands, id)
	appStoreMu.Unlock()
	if raw == nil || (have == rev && len(cmds) == 0) {
		return nil
	}
	return &tunnel.StoreUpdate{Rev: rev, Catalog: raw, Reinstall: cmds}
}

// storeStateFrom takes how worker id stands (what it has, what failed and when it tries again) for the admin's table of that server.
func storeStateFrom(id string, raw json.RawMessage) {
	var st appstore.NodeState
	if !nodeIDPattern.MatchString(id) || len(raw) > 1<<20 || json.Unmarshal(raw, &st) != nil {
		return
	}
	appStoreMu.Lock()
	appNodes[id] = appNode{NodeState: st, Seen: time.Now()}
	appStoreMu.Unlock()
}

func (server *Server) gatewayStoreUpdate(w *tunnel.Worker, have int64) *tunnel.StoreUpdate {
	return storeUpdateFor(storeService(), w.ID(), have)
}

// gatewayStoreLock is the lock of a bundle for a worker that asks for it over the tunnel.
func (server *Server) gatewayStoreLock(w *tunnel.Worker, id string) (json.RawMessage, bool) {
	svc := storeService()
	if svc == nil {
		return nil, false
	}
	l, ok := svc.LockOf(id)
	if !ok {
		return nil, false
	}
	raw, err := json.Marshal(l)
	return raw, err == nil
}

func (server *Server) gatewayStoreState(w *tunnel.Worker, raw json.RawMessage) {
	storeStateFrom(w.ID(), raw)
}

// ---- the worker's side ---------------------------------------------------------------------------------------------------

var workerStore struct {
	sync.Mutex
	pending *appstore.Snapshot // the newest catalog that has not been applied yet
	wake    chan struct{}
	resend  chan struct{}
}

func init() {
	workerStore.wake = make(chan struct{}, 1)
	workerStore.resend = make(chan struct{}, 1)
}

// workerStoreUpdate takes the catalog the gateway sent (the tunnel client calls it, so it only queues): the newest wins, but a command
// to reinstall something is never lost to a newer catalog.
func (server *Server) workerStoreUpdate(u *tunnel.StoreUpdate) {
	var snap appstore.Snapshot
	if len(u.Catalog) > 0 {
		if err := json.Unmarshal(u.Catalog, &snap); err != nil {
			log.Printf("appstore: the catalog of the gateway could not be read: %v", err)
			return
		}
	}
	queueCatalog(snap, u.Reinstall)
}

func queueCatalog(snap appstore.Snapshot, reinstall []string) {
	workerStore.Lock()
	if workerStore.pending != nil {
		reinstall = append(append([]string(nil), workerStore.pending.Reinstall...), reinstall...)
	}
	snap.Reinstall = reinstall
	workerStore.pending = &snap
	workerStore.Unlock()
	select {
	case workerStore.wake <- struct{}{}:
	default:
	}
}

// storeResend asks the worker to tell the gateway how it stands again (a gateway that has just started knows nothing of it).
func storeResend() {
	select {
	case workerStore.resend <- struct{}{}:
	default:
	}
}

// startWorkerStore is a worker's App Store: it applies each catalog the gateway sends, tells the gateway how it stands after every
// bundle, and tries a failed bundle again by itself when its time has come. It never asks the gateway for anything.
func (server *Server) startWorkerStore(ctx context.Context, svc *appstore.Service, client *tunnel.Client) {
	if svc == nil {
		return
	}
	var last appstore.NodeState
	var haveLast bool
	send := func(st appstore.NodeState) {
		last, haveLast = st, true
		client.SendStoreState(st)
	}
	svc.SetOnStep(send)
	svc.SetLock(func(ctx context.Context, id string) (appstore.Lock, error) {
		var l appstore.Lock
		raw, err := client.StoreLock(id)
		if err == nil {
			err = json.Unmarshal(raw, &l)
		}
		if err == nil && l.Lock == "" {
			err = errors.New("the lock is empty")
		}
		return l, err
	})
	var current appstore.Snapshot
	go func() {
		for {
			var timer <-chan time.Time
			if next, ok := svc.NextRetry(); ok {
				d := time.Until(next)
				if d < time.Second {
					d = time.Second
				}
				timer = time.After(d)
			}
			select {
			case <-ctx.Done():
				return
			case <-workerStore.wake:
				workerStore.Lock()
				p := workerStore.pending
				workerStore.pending = nil
				workerStore.Unlock()
				if p == nil {
					continue
				}
				current = *p
				send(svc.Apply(ctx, current))
				current.Reinstall = nil // a command is given once
			case <-timer:
				send(svc.Apply(ctx, current))
			case <-workerStore.resend:
				if haveLast {
					send(last)
				}
			}
		}
	}()
}
