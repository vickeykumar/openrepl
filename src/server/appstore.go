package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"appstore"
	"homesnap"
	"persist"
	"utils"
)

// The App Store (LLD 18). The catalog (which apps are installed, who asked for what, the users' lists) lives in the site's
// database on the gateway or a standalone server; every node (the gateway, each worker) keeps the same bundles in its own store
// folder and makes them match the catalog at its start and from then on. A worker takes the catalog from the gateway
// (POST /store/sync with the worker token, which also tells the gateway what the worker has).

// APPSTORE_DB is the database of the catalog.
const APPSTORE_DB = utils.GOTTY_PATH + "/appstore.db"

// defaultSafeList is what a signed-in user may install without asking, until an admin saves a list of their own.
var defaultSafeList = appstore.DefaultSafeList

const defaultStoreLimitMB = 2048

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

// newInstaller picks the installer: the fake one for tests and development (OPENREPL_STORE_INSTALLER=fake), else micromamba
// (OPENREPL_MICROMAMBA, or the PATH), else one that says what is missing.
func newInstaller(dir string) appstore.Installer {
	if os.Getenv("OPENREPL_STORE_INSTALLER") == "fake" {
		return appstore.Fake{}
	}
	bin := os.Getenv("OPENREPL_MICROMAMBA")
	if bin == "" {
		bin, _ = exec.LookPath("micromamba")
	}
	if bin == "" {
		return appstore.Missing{Reason: "micromamba is not installed on this server (set OPENREPL_MICROMAMBA to its path)"}
	}
	py := os.Getenv("OPENREPL_STORE_PYTHON")
	if py == "" {
		py = appstore.PythonOf("python3")
	}
	return appstore.Mamba{Bin: bin, Dir: dir, Python: py}
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
	cfg := appstore.Config{Store: appstore.Store{Dir: dir}, KV: kv, Installer: newInstaller(dir), Safe: storeSafeList, LimitMB: storeLimitMB, Apt: aptRunner()}
	if worker {
		cfg.Fetch = gatewayFetch(gatewayHTTPBase(server.options.WorkerServer), server.options.WorkerToken, dir)
	}
	svc := appstore.New(cfg)
	appStoreMu.Lock()
	appStoreSvc, appStoreKV = svc, kv
	appStoreMu.Unlock()
	if err := svc.Store().Ready(); err != nil {
		log.Printf("WARNING: appstore: the folder %s cannot be made: %v (installs will fail until it can)", dir, err)
	}
	log.Printf("appstore: store %s, installer %s", dir, svc.Installer().Name())
	if worker {
		go server.workerStoreLoop(ctx, svc)
		return
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

// workerStoreLoop is a worker's replay: it asks the gateway for the catalog (saying what it has), and applies it.
func (server *Server) workerStoreLoop(ctx context.Context, svc *appstore.Service) {
	base := gatewayHTTPBase(server.options.WorkerServer)
	if base == "" {
		log.Printf("appstore: this worker cannot reach its gateway over HTTP (%q), so the App Store is not kept up to date here", server.options.WorkerServer)
		return
	}
	id := server.options.WorkerID
	if id == "" {
		id, _ = os.Hostname()
	}
	client := &http.Client{Timeout: 60 * time.Second}
	var have appstore.NodeState
	for {
		snap, err := fetchStoreSnapshot(ctx, client, base, server.options.WorkerToken, id, have)
		if err != nil {
			log.Printf("appstore: the catalog could not be fetched from the gateway: %v", err)
		} else {
			have = svc.Apply(ctx, snap)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

// gatewayHTTPBase turns the tunnel address of a worker (wss://host/api/tunnel) into the gateway's site address.
func gatewayHTTPBase(tunnel string) string {
	u, err := url.Parse(tunnel)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	default:
		return ""
	}
	u.Path, u.RawQuery = "", ""
	return strings.TrimRight(u.String(), "/")
}

func fetchStoreSnapshot(ctx context.Context, c *http.Client, base, token, node string, have appstore.NodeState) (appstore.Snapshot, error) {
	var snap appstore.Snapshot
	body, _ := json.Marshal(map[string]interface{}{"node": node, "state": have})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/store/sync", strings.NewReader(string(body)))
	if err != nil {
		return snap, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return snap, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return snap, fmt.Errorf("the gateway answered %s", res.Status)
	}
	return snap, json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&snap)
}

// handleStoreSync is what the gateway answers a worker: the catalog. The worker proves itself with the shared token.
func (server *Server) handleStoreSync(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if server.options.WorkerToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(server.options.WorkerToken)) != 1 {
		http.Error(w, "not allowed", http.StatusUnauthorized)
		return
	}
	if svc == nil || r.Method != http.MethodPost {
		http.Error(w, "the App Store is off", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Node  string             `json:"node"`
		State appstore.NodeState `json:"state"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) == nil && nodeIDPattern.MatchString(in.Node) {
		appStoreMu.Lock()
		appNodes[in.Node] = appNode{NodeState: in.State, Seen: time.Now()}
		appStoreMu.Unlock()
	}
	writeJSON(w, http.StatusOK, svc.Snapshot())
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
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// handleStoreAPI answers /store/api/<what> for a signed-in user.
func (server *Server) handleStoreAPI(w http.ResponseWriter, r *http.Request) {
	what := ""
	if i := strings.Index(r.URL.Path, "store/api/"); i >= 0 {
		what = strings.Trim(r.URL.Path[i+len("store/api/"):], "/")
	}
	uid, home, svc := server.storeUser(w, r)
	if svc == nil {
		return
	}
	if r.Method == http.MethodGet {
		switch what {
		case "list":
			dir := svc.Store().Dir
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"views": svc.Views(uid, home), "on": svc.MyList(home), "home": home, "envScript": filepath.Join(dir, "envs", home, "env.sh"),
				"installer": svc.Installer().Name(), "maxOn": appstore.MaxOn, "admin": IsUserAdmin(w, r), "python": storePython(),
			})
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
	switch what {
	case "install", "retry":
		if err = svc.Install(uid, c.Name, c.Kind, false); err == nil {
			kickStore()
		}
	case "request":
		_, err = svc.Request(uid, c.Name, c.Kind, c.Reason)
	case "switch":
		err = svc.Switch(home, c.ID, c.On)
	case "report":
		err = svc.Report(uid, c.Name, c.Note)
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
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "views": svc.Views(uid, home), "on": svc.MyList(home)})
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
	Node   string            `json:"node"`
	Have   []string          `json:"have"`
	Failed map[string]string `json:"failed,omitempty"`
	SizeMB float64           `json:"sizeMB"`
	Seen   string            `json:"seen,omitempty"`
}

// handleAdminStore is the Apps page of the dashboard: GET the whole picture, POST one decision.
func (server *Server) handleAdminStore(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "error": appStoreErr})
		return
	}
	if r.Method == http.MethodGet {
		local := appstore.NodeState{Have: svc.Store().Bundles(), SizeMB: float64(svc.Store().Size()) / (1 << 20), At: time.Now().Unix()}
		appStoreMu.Lock()
		rows := []storeNodeRow{{Node: "gateway", Have: local.Have, Failed: appLocal.Failed, SizeMB: local.SizeMB}}
		for id, n := range appNodes {
			rows = append(rows, storeNodeRow{Node: id, Have: n.Have, Failed: n.Failed, SizeMB: n.SizeMB, Seen: n.Seen.UTC().Format(time.RFC3339)})
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
			"apps": svc.Apps(), "requests": svc.Requests(), "reports": svc.Reports(), "apt": svc.Apt(), "users": users, "nodes": rows,
			"safeList": storeSafeList(), "limitMB": storeLimitMB(), "limitSet": s.StoreLimitMB, "safeSet": len(s.StoreSafeList) > 0,
			"sizeMB": local.SizeMB, "channel": "conda-forge", "python": storePython(), "now": time.Now().Unix(),
		})
		return
	}
	var c struct {
		Action  string   `json:"action"`
		Version string   `json:"version"`
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		Answer  string   `json:"answer"`
		Apt     []string `json:"apt"`
		Safe    []string `json:"safeList"`
		Limit   int      `json:"limitMB"`
		Off     bool     `json:"off"`
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
		return healthWarn, detail + ". Nothing can be installed until micromamba is there."
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

// gatewayFetch is how a worker gets a finished bundle: the gateway sends it as a .tar.gz (handleStoreBundle), so the worker needs
// no solve and no internet. It is nil when the worker cannot reach its gateway over HTTP.
func gatewayFetch(base, token, dir string) func(ctx context.Context, id, dest string) error {
	if base == "" || token == "" {
		return nil
	}
	client := &http.Client{Timeout: 20 * time.Minute}
	return func(ctx context.Context, id, dest string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/store/bundle?id="+url.QueryEscape(id), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("the gateway answered %s", res.Status)
		}
		if err := appstore.ReadBundle(res.Body, dest); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(dest, ".openrepl-bundle.json")); err != nil {
			os.RemoveAll(dest)
			return fmt.Errorf("the bundle arrived without its marker")
		}
		return nil
	}
}

// handleStoreBundle sends a finished bundle to a worker (GET /store/bundle?id=<name>-<version>, the worker token).
func (server *Server) handleStoreBundle(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if server.options.WorkerToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(server.options.WorkerToken)) != 1 {
		http.Error(w, "not allowed", http.StatusUnauthorized)
		return
	}
	id := r.URL.Query().Get("id")
	if svc == nil || !appstore.ValidBundle(id) || !svc.Store().HasBundle(id) {
		http.Error(w, "no such bundle", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	if err := svc.Store().WriteBundle(id, w); err != nil {
		log.Printf("appstore: sending %s to %s: %v", id, r.RemoteAddr, err)
	}
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
