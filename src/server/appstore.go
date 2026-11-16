package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"appstore"
	"containers"
	"homesnap"
	"persist"
	"utils"
)

// The App Store (LLD 18). The catalog (which apps are installed, who asked for what, the users' lists) lives in the site's
// database on the gateway or a standalone server; every node (the gateway, each worker) keeps the same bundles in its own store
// folder and makes them match the catalog at its start and from then on. A worker takes the catalog from the gateway over the
// tunnel it already has (storetunnel.go): once when it connects, and again only when the catalog changes.

// APPSTORE_DB is the database of the catalog.
const APPSTORE_DB = utils.GOTTY_PATH + "/appstore.db"

// defaultSafeList is what a signed-in user may install without asking, until an admin saves a list of their own.
var defaultSafeList = appstore.DefaultSafeList

const defaultStoreLimitMB = 15360 // 15 GB for each node (physical size)

// defaultStoreReserveMB is kept free for installs in progress; defaultRequestsPerDay is how many apps one user may ask for in a day.
const (
	defaultStoreReserveMB = 3072
	defaultRequestsPerDay = 5
)

var (
	appStoreMu  sync.Mutex
	appStoreSvc *appstore.Service
	appStoreKV  appstore.KV
	appNodes    = map[string]appNode{} // what the workers said they have (gateway)
	appKick     = make(chan struct{}, 1)
	appStoreErr string
	appLocal    appstore.NodeState // what the last replay of this node did
)

type appNode struct {
	appstore.NodeState
	Seen time.Time `json:"-"`
}

// appCommands are what an admin asked of a node (the bundles to make again), handed to a worker with its next heartbeat reply.
var appCommands = map[string][]string{}

func storeService() *appstore.Service {
	appStoreMu.Lock()
	defer appStoreMu.Unlock()
	return appStoreSvc
}

func storeSafeList() []string {
	if l := GetSiteSettings().StoreSafeList; len(l) > 0 {
		return l
	}
	return defaultSafeList
}

func storeLimitMB() int {
	if n := GetSiteSettings().StoreLimitMB; n > 0 {
		return n
	}
	return defaultStoreLimitMB
}

func storeReserveMB() int {
	if n := GetSiteSettings().StoreReserveMB; n > 0 {
		return n
	}
	return defaultStoreReserveMB
}

func storeRequestsPerDay() int {
	if n := GetSiteSettings().StoreRequestsPerDay; n > 0 {
		return n
	}
	return defaultRequestsPerDay
}

// newInstaller picks the installer: the fake one for tests and development (OPENREPL_STORE_INSTALLER=fake), else pixi
// (OPENREPL_PIXI, or the PATH), else one that says what is missing.
func newInstaller(dir string) appstore.Installer {
	if os.Getenv("OPENREPL_STORE_INSTALLER") == "fake" {
		return appstore.Fake{}
	}
	multi := appstore.Multi{Plugin: appstore.PluginInstaller{}}
	bin := os.Getenv("OPENREPL_PIXI")
	if bin == "" {
		bin, _ = exec.LookPath("pixi")
	}
	if bin != "" {
		py := os.Getenv("OPENREPL_STORE_PYTHON")
		if py == "" {
			py = appstore.PythonOf("python3")
		}
		multi.Conda = appstore.Pixi{Bin: bin, Dir: dir, Python: py, Constraints: runtimeConstraints}
	}
	if npm := findNpm(); npm != "" {
		multi.Node = appstore.Npm{Bin: npm, Dir: dir}
	}
	// the mirrors (LLD 18): the image's own go, java and cargo fetch the libraries; the catalogue says which package an app is
	lookup := func(kind, app string) (appstore.Entry, bool) {
		if svc := storeService(); svc != nil {
			return svc.EntryFor(app, kind)
		}
		return appstore.Entry{}, false
	}
	multi.GoMod = appstore.GoMod{Dir: dir, Lookup: lookup}
	multi.Jar = appstore.Maven{Dir: dir, Lookup: lookup}
	multi.Crate = appstore.Cargo{Dir: dir, Lookup: lookup}
	return multi
}

// findNpm is npm: OPENREPL_NPM, the PATH, or the one of the nvm of the image (/root/.nvm, where the Node REPLs get theirs).
func findNpm() string {
	if p := os.Getenv("OPENREPL_NPM"); p != "" {
		return p
	}
	if p, err := exec.LookPath("npm"); err == nil {
		return p
	}
	if m, _ := filepath.Glob("/root/.nvm/versions/node/*/bin/npm"); len(m) > 0 {
		return m[len(m)-1]
	}
	return ""
}

// memKV keeps the catalog in memory: a worker has no database and only applies what the gateway sends.
type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKV) Store(key, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string][]byte{}
	}
	k.m[string(key)] = append([]byte(nil), v...)
	return nil
}
func (k *memKV) Fetch(key []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.m[string(key)]; ok {
		return v, nil
	}
	return nil, persist.ErrNotFound
}
func (k *memKV) Delete(key []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, string(key))
	return nil
}
func (k *memKV) Commit() error { return nil }
func (k *memKV) Each(fn func(k, v []byte) bool) error {
	k.mu.Lock()
	var keys, vals [][]byte
	for key, v := range k.m {
		keys, vals = append(keys, []byte(key)), append(vals, v)
	}
	k.mu.Unlock()
	for i := range keys {
		if !fn(keys[i], vals[i]) {
			break
		}
	}
	return nil
}

// startAppStore makes the service and keeps this node's store in step with the catalog until ctx ends.
func (server *Server) startAppStore(ctx context.Context) {
	startScratch(ctx)
	dir := utils.StoreDir()
	if dir == "" {
		log.Printf("appstore: switched off (%s=off)", utils.EnvStoreDir)
		return
	}
	appStoreMu.Lock()
	if appStoreSvc != nil {
		appStoreMu.Unlock()
		return
	}
	appStoreMu.Unlock()
	worker := server.options.Mode == ModeWorker
	var kv appstore.KV = &memKV{}
	if !worker {
		db, err := persist.Open(APPSTORE_DB)
		if err != nil {
			log.Printf("ERROR: appstore: the catalog could not be opened: %v; the App Store is off", err)
			appStoreErr = err.Error()
			return
		}
		kv = db
	}
	cfg := appstore.Config{Store: appstore.Store{Dir: dir}, KV: kv, Installer: newInstaller(dir), Safe: storeSafeList, LimitMB: storeLimitMB, ReserveMB: storeReserveMB, PerDay: storeRequestsPerDay, Apt: aptRunner()}
	svc := appstore.New(cfg)
	go func() { svc.SetBuiltins(appstore.DetectBuiltins(ctx)) }() // what the image already has, found at start
	appStoreMu.Lock()
	appStoreSvc, appStoreKV = svc, kv
	appStoreMu.Unlock()
	if err := svc.Store().Ready(); err != nil {
		log.Printf("WARNING: appstore: the folder %s cannot be made: %v (installs will fail until it can)", dir, err)
	}
	log.Printf("appstore: store %s, installer %s", dir, svc.Installer().Name())
	if worker {
		return // the loop starts when the tunnel client exists (startWorkerStore)
	}
	svc.Recover()
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			st := svc.Apply(ctx, svc.Snapshot()) // at start, and every ten minutes: what is missing here is made again
			appStoreMu.Lock()
			appLocal = st
			appStoreMu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-appKick:
			}
		}
	}()
}

func kickStore() {
	select {
	case appKick <- struct{}{}:
	default:
	}
}

// ---- the user's side -----------------------------------------------------------------------------------------------------

// storeUser says who is asking: the signed-in account and the name of its workspace folder. Guests, and a store that is off, are
// answered here and "" is returned.
func (server *Server) storeUser(w http.ResponseWriter, r *http.Request) (uid, home string, svc *appstore.Service) {
	svc = storeService()
	if svc == nil || GetSiteSettings().StoreOff {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The App Store is not available on this site."})
		return "", "", nil
	}
	uid, homedir := server.requestIdentity(w, r)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signin", "message": "The App Store is for signed-in users."})
		return "", "", nil
	}
	if filepath.Dir(filepath.Clean(homedir)) != filepath.Clean(utils.HOME_DIR) || !homesnap.IsKept(filepath.Base(homedir)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This workspace cannot use the App Store."})
		return "", "", nil
	}
	return uid, filepath.Base(homedir), svc
}

type storeCall struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	On     bool   `json:"on"`
	Reason string `json:"reason"`
	Note   string `json:"note"`
	Repl   string `json:"repl"`
}

func builtinsOrEmpty(svc *appstore.Service) []appstore.Builtin {
	if b := svc.Builtins(); b != nil {
		return b
	}
	return []appstore.Builtin{}
}

func storeError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch err {
	case appstore.ErrNeedsApproval:
		code = http.StatusForbidden
	case appstore.ErrFull:
		code = http.StatusInsufficientStorage
	case appstore.ErrTooSoon:
		code = http.StatusTooManyRequests
	}
	if errors.Is(err, appstore.ErrDaily) {
		code = http.StatusTooManyRequests
	}
	if errors.Is(err, appstore.ErrInUse) {
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// handleStoreAPI answers /store/api/<what> for a signed-in user.
func (server *Server) handleStoreAPI(w http.ResponseWriter, r *http.Request) {
	what := ""
	if i := strings.Index(r.URL.Path, "store/api/"); i >= 0 {
		what = strings.Trim(r.URL.Path[i+len("store/api/"):], "/")
	}
	if what == "space" && r.Method == http.MethodGet {
		server.handleStoreSpace(w, r) // the status bar of a terminal: also for a guest, and with the App Store off
		return
	}
	uid, home, svc := server.storeUser(w, r)
	if svc == nil {
		return
	}
	if r.Method == http.MethodGet {
		switch what {
		case "list":
			admin := IsUserAdmin(w, r)
			out := map[string]interface{}{
				"views": storeViews(svc, uid, home, admin), "on": svc.MyList(home), "sandbox": containers.LandlockStatus().Active,
				"maxOn": appstore.MaxOn, "admin": admin, "requestsToday": svc.RequestsToday(uid), "requestsPerDay": storeRequestsPerDay(), "python": storePython(), "builtins": builtinsOrEmpty(svc),
			}
			if admin {
				// a path, the home folder and the installer are for the admin: a user is told none of them
				out["home"] = home
				out["envScript"] = filepath.Join(svc.Store().Dir, "envs", home, "env.sh")
				out["installer"] = svc.Installer().Name()
			}
			writeJSON(w, http.StatusOK, out)
		case "notices":
			n := svc.Notices(uid)
			if n == nil {
				n = []appstore.Notice{}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"notices": n})
		case "job":
			j, ok := svc.Job(r.URL.Query().Get("name"))
			writeJSON(w, http.StatusOK, map[string]interface{}{"running": ok, "step": j.Step})
		case "info":
			info, err := lookupApp(r.Context(), r.URL.Query().Get("name"))
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, info)
		case "search":
			if !allowStoreSearch(uid) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Searching too fast: wait a second."})
				return
			}
			hits, err := svc.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("kind"))
			if hits == nil {
				hits = []appstore.Hit{}
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"hits": hits, "error": msg})
		case "size":
			if k := r.URL.Query().Get("kind"); appstore.IsMirrorKind(k) {
				// a mirror is fetched to be measured: that is done by the install, not by a visitor of the page
				writeJSON(w, http.StatusOK, map[string]string{"error": "worked out when it is installed"})
				return
			}
			res, err := svc.Estimate(r.Context(), r.URL.Query().Get("name"), r.URL.Query().Get("kind"))
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"version": res.Version, "packages": res.Packages, "downloadMB": res.DownloadMB, "diskMB": res.DiskMB})
		case "plugins":
			writeJSON(w, http.StatusOK, map[string]interface{}{"plugins": storePlugins(svc, home)})
		case "preview-plugins":
			writeJSON(w, http.StatusOK, map[string]interface{}{"plugins": svc.PreviewPlugins(home)})
		case "catalogue":
			writeJSON(w, http.StatusOK, catalogueFor(svc, uid, home, queryFilter(r)))
		default:
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	var c storeCall
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&c) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var err error
	admin := IsUserAdmin(w, r)
	switch what {
	case "install", "retry":
		if err = svc.Install(uid, c.Name, c.Kind, false); err == nil {
			kickStore()
		}
	case "request":
		_, err = svc.RequestFor(uid, c.Name, c.Kind, c.Repl, c.Reason)
	case "switch":
		if err = svc.Switch(home, c.ID, c.On); err != nil || !c.On {
			break
		}
		// it is on only when it works: the check runs for this user, on the node of their terminals
		if a, ok := svc.AppByID(c.ID); ok {
			node, wait, verr := server.verifyOnNode(r.Context(), svc, home, a)
			if verr != nil {
				svc.Switch(home, c.ID, false)
				// the user gets a short plain message; the node and the raw log go only to the admin's failure list
				body := map[string]interface{}{"error": "verify", "name": a.Name, "wait": wait, "views": storeViews(svc, uid, home, admin), "on": svc.MyList(home)}
				if wait {
					body["message"] = a.Name + " is not ready on your server yet. Try again in a minute."
				} else {
					svc.NoteCheckFailure(a.Name, node, verr.Error())
					body["message"] = a.Name + " did not work for your terminals, so it is off."
				}
				writeJSON(w, http.StatusUnprocessableEntity, body)
				return
			}
		}
	case "report":
		err = svc.Report(uid, c.Name, c.Note)
		if errors.Is(err, appstore.ErrUnknown) {
			// an app that is installed but did not work for this user: add them to the entry the failed check made
			err = svc.ReportUse(uid, c.Name, "", "Turning it on", "", c.Note)
		}
	case "uninstall":
		err = svc.Uninstall(uid, home, c.Name)
		if err == nil {
			kickStore()
		}
	case "plugin-switch":
		err = svc.SwitchPlugin(home, c.ID, c.On)
	case "ack":
		svc.Ack(uid, c.Name)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "views": storeViews(svc, uid, home, admin), "on": svc.MyList(home)})
}

// storeViews are the apps as a user may see them: an admin gets the full version (errors, logs, the server a failure ran on).
func storeViews(svc *appstore.Service, uid, home string, admin bool) []appstore.View {
	if admin {
		return svc.ViewsFull(uid, home)
	}
	return svc.Views(uid, home)
}

// ---- the lookup of one package (what a user sees before installing) ----------------------------------------------------

// condaForgeAPI is where the description of a package comes from; a test points it at its own server.
var condaForgeAPI = "https://api.anaconda.org/package/conda-forge/"

type appInfo struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Version  string   `json:"version"`
	Home     string   `json:"home,omitempty"`
	Versions []string `json:"versions,omitempty"` // the newest first, for an admin who picks one
}

var (
	infoMu    sync.Mutex
	infoCache = map[string]struct {
		info appInfo
		at   time.Time
	}{}
)

func lookupApp(ctx context.Context, name string) (appInfo, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !appstore.ValidName(name) {
		return appInfo{}, fmt.Errorf("%q is not a package name", name)
	}
	infoMu.Lock()
	if c, ok := infoCache[name]; ok && time.Since(c.at) < time.Hour {
		infoMu.Unlock()
		return c.info, nil
	}
	infoMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, condaForgeAPI+url.PathEscape(name), nil)
	if err != nil {
		return appInfo{}, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return appInfo{}, fmt.Errorf("conda-forge could not be reached")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return appInfo{}, fmt.Errorf("conda-forge has no package called %s", name)
	}
	if res.StatusCode != http.StatusOK {
		return appInfo{}, fmt.Errorf("conda-forge answered %s", res.Status)
	}
	var raw struct {
		Summary  string   `json:"summary"`
		Latest   string   `json:"latest_version"`
		Home     string   `json:"home"`
		Versions []string `json:"versions"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&raw) != nil {
		return appInfo{}, fmt.Errorf("the answer of conda-forge could not be read")
	}
	info := appInfo{Name: name, Summary: strings.TrimSpace(raw.Summary), Version: raw.Latest, Home: raw.Home}
	for i := len(raw.Versions) - 1; i >= 0 && len(info.Versions) < 25; i-- { // the API lists the oldest first
		if appstore.ValidVersion(raw.Versions[i]) {
			info.Versions = append(info.Versions, raw.Versions[i])
		}
	}
	if len(info.Summary) > 300 {
		info.Summary = info.Summary[:300]
	}
	infoMu.Lock()
	infoCache[name] = struct {
		info appInfo
		at   time.Time
	}{info, time.Now()}
	infoMu.Unlock()
	return info, nil
}

// ---- the admin's side ----------------------------------------------------------------------------------------------------

type storeNodeRow struct {
	Node    string             `json:"node"`
	Have    []string           `json:"have"`
	Failed  map[string]string  `json:"failed,omitempty"`
	SizeMB  float64            `json:"sizeMB"`
	Seen    string             `json:"seen,omitempty"`
	Sizes   map[string]float64 `json:"sizes,omitempty"`   // MB of each bundle on the node
	Pending []string           `json:"pending,omitempty"` // bundles an admin asked it to make again, not taken yet
	Retry   map[string]int64   `json:"retry,omitempty"`   // when (unix seconds) the node tries a failed bundle again
}

// handleAdminStore is the Apps page of the dashboard: GET the whole picture, POST one decision.
func (server *Server) handleAdminStore(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "error": appStoreErr})
		return
	}
	if r.Method == http.MethodGet {
		local := appstore.NodeState{Have: svc.Store().Bundles(), SizeMB: float64(svc.Store().Size()) / (1 << 20), At: time.Now().Unix(), Sizes: map[string]float64{}}
		for _, id := range local.Have {
			local.Sizes[id] = float64(svc.Store().BundleSize(id)) / (1 << 20)
		}
		appStoreMu.Lock()
		rows := []storeNodeRow{{Node: "gateway", Have: local.Have, Failed: appLocal.Failed, Retry: appLocal.Retry, SizeMB: local.SizeMB, Sizes: local.Sizes}}
		ids := make([]string, 0, len(appNodes))
		for id := range appNodes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			n := appNodes[id]
			have := n.Have
			if have == nil {
				have = []string{} // a worker that has only reported a fetch has not told what it has: an empty list, not null
			}
			rows = append(rows, storeNodeRow{Node: id, Have: have, Failed: n.Failed, Retry: n.Retry, SizeMB: n.SizeMB, Seen: n.Seen.UTC().Format(time.RFC3339), Sizes: n.Sizes,
				Pending: append([]string(nil), appCommands[id]...)})
		}
		appStoreMu.Unlock()
		users := map[string]int{}
		snap := svc.Snapshot()
		for _, ids := range snap.Users {
			for _, id := range ids {
				users[id]++
			}
		}
		s := GetSiteSettings()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled": !s.StoreOff, "installer": svc.Installer().Name(), "dir": svc.Store().Dir,
			"apps": svc.Apps(), "defaults": svc.Defaults(), "requests": svc.Requests(), "reports": svc.Reports(), "apt": svc.Apt(), "users": users, "nodes": rows,
			"safeList": storeSafeList(), "limitMB": storeLimitMB(), "limitSet": s.StoreLimitMB, "safeSet": len(s.StoreSafeList) > 0,
			"sizeMB": local.SizeMB, "channel": "conda-forge", "python": storePython(), "now": time.Now().Unix(),
		})
		return
	}
	var c struct {
		Action          string   `json:"action"`
		Node            string   `json:"node"`
		ID              string   `json:"id"`
		Version         string   `json:"version"`
		Name            string   `json:"name"`
		Kind            string   `json:"kind"`
		Answer          string   `json:"answer"`
		Apt             []string `json:"apt"`
		Safe            []string `json:"safeList"`
		Limit           int      `json:"limitMB"`
		Off             bool     `json:"off"`
		ReserveMB       int      `json:"reserveMB"`
		PerDay          int      `json:"perDay"`
		ScratchSoftMB   int      `json:"scratchSoftMB"`
		ScratchServerMB int      `json:"scratchServerMB"`
		// Everyone: with an install, turn the app on for every signed-in user (default is the same, for an app that is installed)
		Everyone bool `json:"everyone"`
		On       bool `json:"on"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&c) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	who := adminActor(r)
	var err error
	detail := c.Name
	switch c.Action {
	case "install":
		err = svc.Install(who, c.Name, c.Kind, true)
		if err == nil && c.Everyone {
			err = svc.SetDefault(who, c.Name, true)
		}
	case "default":
		err = svc.SetDefault(who, c.Name, c.On)
		detail = c.Name + map[bool]string{true: " on for everyone", false: " not on for everyone"}[c.On]
	case "reinstall":
		// make a bundle again on one server: the gateway at once, a worker the next time it asks for the catalog (within a minute)
		if !appstore.ValidBundle(c.ID) {
			err = fmt.Errorf("%q is not a bundle", c.ID)
		} else if c.Node == "gateway" {
			err = svc.Store().RemoveBundle(c.ID)
		} else {
			appStoreMu.Lock()
			if _, known := appNodes[c.Node]; !known {
				err = fmt.Errorf("%q is not a worker that has asked for the catalog", c.Node)
			} else if !hasString(appCommands[c.Node], c.ID) {
				appCommands[c.Node] = append(appCommands[c.Node], c.ID)
			}
			appStoreMu.Unlock()
		}
		detail = c.Node + ": " + c.ID
	case "relock":
		var id string
		if id, err = svc.Relock(who, c.Name); err == nil {
			// every worker makes it again, from the new lock, when the gateway has installed it
			appStoreMu.Lock()
			for node := range appNodes {
				if !hasString(appCommands[node], id) {
					appCommands[node] = append(appCommands[node], id)
				}
			}
			appStoreMu.Unlock()
			detail = c.Name + " (new lock)"
		}
	case "pickversion":
		err = svc.InstallVersion(who, c.Name, c.Kind, c.Version)
		detail = c.Name + " " + c.Version
	case "approve":
		err = svc.Approve(who, c.Name)
	case "reject":
		err = svc.Reject(who, c.Name, c.Answer)
	case "retry", "installed", "rejectreport":
		act := c.Action
		if act == "rejectreport" {
			act = "reject"
		}
		err = svc.ResolveReport(who, c.Name, act, c.Answer)
	case "remove":
		var n int
		if n, err = svc.Remove(who, c.Name); err == nil {
			detail = fmt.Sprintf("%s (it was on for %d users)", c.Name, n)
		}
	case "apt":
		if err = svc.SetApt(c.Apt); err == nil {
			detail = strings.Join(c.Apt, ", ")
		}
	case "settings":
		s := GetSiteSettings()
		s.StoreSafeList, s.StoreLimitMB, s.StoreOff = c.Safe, c.Limit, c.Off
		s.StoreReserveMB, s.StoreRequestsPerDay, s.ScratchSoftMB, s.ScratchServerMB = c.ReserveMB, c.PerDay, c.ScratchSoftMB, c.ScratchServerMB
		if err = SaveSiteSettings(s); err == nil {
			detail = "safe list, size limit, switch"
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	server.audit(r, "app store: "+c.Action, detail)
	kickStore()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// aptRunner installs the admin's approved Debian packages on this node, except where the fake installer is in use (tests and
// development servers must never run apt-get).
func aptRunner() appstore.AptRunner {
	if os.Getenv("OPENREPL_STORE_INSTALLER") == "fake" {
		return nil
	}
	return appstore.SystemApt{}
}

// appStoreHealth is the card of the Health page: "" when the store is off or this is a worker, else a status and a line.
func appStoreHealth() (status, detail string) {
	svc := storeService()
	if svc == nil {
		if appStoreErr != "" {
			return healthFail, "the catalog could not be opened: " + appStoreErr
		}
		return "", ""
	}
	if GetSiteSettings().StoreOff {
		return healthOK, "switched off in the settings"
	}
	inst := svc.Installer().Name()
	apps := svc.Apps()
	installed, failed := 0, 0
	for _, a := range apps {
		switch a.State {
		case appstore.StateInstalled:
			installed++
		case appstore.StateFailed:
			failed++
		}
	}
	mb := float64(svc.Store().Size()) / (1 << 20)
	behind := 0
	appStoreMu.Lock()
	for _, n := range appNodes {
		if time.Since(n.Seen) < 5*time.Minute && len(n.Failed) > 0 {
			behind++
		}
	}
	appStoreMu.Unlock()
	detail = fmt.Sprintf("%d apps installed, %.0f MB of %d MB, installer: %s", installed, mb, storeLimitMB(), inst)
	status = healthOK
	if strings.HasPrefix(inst, "none") {
		return healthWarn, detail + ". Nothing can be installed until pixi is there."
	}
	if failed > 0 {
		status = healthWarn
		detail += fmt.Sprintf("; %d failed", failed)
	}
	if behind > 0 {
		status = healthWarn
		detail += fmt.Sprintf("; %d workers could not apply the catalog", behind)
	}
	if mb > float64(storeLimitMB()) {
		status = healthWarn
		detail += "; the store is over its limit, installs are refused"
	}
	return status, detail
}

// handleStorePage serves the page of the App Store (resources/store.html). The page holds no data: it asks /store/api, which
// checks who is asking.
func (server *Server) handleStorePage(w http.ResponseWriter, r *http.Request) {
	data, err := Asset("static/store.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// storePython is the Python the bundles are solved with, as the admin and the page say it.
func storePython() string {
	if v := os.Getenv("OPENREPL_STORE_PYTHON"); v != "" {
		return v
	}
	return appstore.PythonOf("python3")
}

// allowStoreSearch lets a user search once a second at most: a search is a call to conda-forge or npm.
var (
	searchMu   sync.Mutex
	searchLast = map[string]time.Time{}
)

func allowStoreSearch(uid string) bool {
	searchMu.Lock()
	defer searchMu.Unlock()
	if time.Since(searchLast[uid]) < 800*time.Millisecond {
		return false
	}
	if len(searchLast) > 5000 {
		searchLast = map[string]time.Time{}
	}
	searchLast[uid] = time.Now()
	return true
}

type storePlugin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
}

// storePlugins are the editor plugins that are on for this user, with their scripts (the page runs each in a sandboxed frame).
func storePlugins(svc *appstore.Service, home string) []storePlugin {
	out := []storePlugin{}
	on := map[string]bool{}
	for _, id := range svc.MyList(home) {
		on[id] = true
	}
	for _, a := range svc.Apps() {
		if a.Kind == appstore.KindPlugin && a.State == appstore.StateInstalled && on[a.ID] {
			if code, err := svc.Store().PluginCode(a.ID); err == nil {
				out = append(out, storePlugin{ID: a.ID, Name: a.Name, Code: code})
			}
		}
	}
	return out
}

func hasString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// runtimeConstraints are the versions the image's REPLs require of an app (appstore.Runtimes), for the entry the catalogue lists under that name.
func runtimeConstraints(name string) [][2]string {
	svc := storeService()
	if svc == nil {
		return nil
	}
	if e, ok := svc.EntryFor(name, appstore.KindTool); ok {
		return appstore.ConstraintsFor(e)
	}
	return nil
}
