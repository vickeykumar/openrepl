package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"appstore"
)

// The catalogue routes (LLD 18): /store/api/catalogue for a signed-in user, /admin/store/catalogue for an admin. A user's answer holds
// only what the page of an app shows: no node names, no disk of a node, no paths, no lock, no other user. Listing or importing an
// entry never installs it.

// catalogueItem is one entry as a user sees it.
type catalogueItem struct {
	appstore.Entry
	State     string `json:"state"`             // available, installing, installed, failed, requested, rejected, builtin
	Installed bool   `json:"installed"`         // an admin installed it: a user can switch it on
	Soon      bool   `json:"soon,omitempty"`    // the kind cannot be installed on this server yet: it is listed with its page
	On        bool   `json:"on,omitempty"`      // on for me
	Safe      bool   `json:"safe"`              // I may install it myself
	Version   string `json:"version,omitempty"` // the version that is installed, when it is
	Builtin   bool   `json:"builtin,omitempty"`
}

type catalogueQuery struct{ appstore.Filter }

func queryFilter(r *http.Request) appstore.Filter {
	q := r.URL.Query()
	return appstore.Filter{Q: q.Get("q"), Repl: q.Get("repl"), Kind: q.Get("kind"), Category: q.Get("category"), Level: q.Get("level"),
		Place: q.Get("place"), Browser: q.Get("browser") == "1"}
}

// catalogueFor answers a user's /store/api/catalogue.
func catalogueFor(svc *appstore.Service, uid, home string, f appstore.Filter) map[string]interface{} {
	views := map[string]appstore.View{}
	for _, v := range svc.Views(uid, home) {
		views[v.Name+"|"+v.Kind] = v
	}
	all := svc.Catalogue()
	cats := map[string]bool{}
	for _, e := range all {
		if e.Category != "" {
			cats[e.Category] = true
		}
	}
	items := []catalogueItem{}
	for _, e := range f.Apply(all) {
		e.Options, e.Minimal = nil, nil // the sizes by CPU type and the options are the admin's: a user is never told what CPUs the servers have
		it := catalogueItem{Entry: e, State: "available", Safe: e.Install == "anyone"}
		k := e.InstallKind()
		if k == "" || e.Python == "2.7" {
			it.Soon = true
		} else if v, ok := views[e.PackageName()+"|"+k]; ok {
			it.State, it.On, it.Version = v.State, v.On, v.Version
			it.Installed = v.State == appstore.StateInstalled
		}
		if b, ok := svc.BuiltinNamed(e.PackageName()); ok && (e.Kind == appstore.CKLib || e.Kind == appstore.CKTool) {
			it.State, it.Builtin, it.Version, it.Safe = "builtin", true, b.Version, false
		}
		items = append(items, it)
	}
	catList := []string{}
	for c := range cats {
		catList = append(catList, c)
	}
	sort.Strings(catList)
	return map[string]interface{}{"entries": items, "repls": appstore.CatalogueRepls, "categories": catList, "levels": []string{"core", "popular", "optional"}}
}

// adminDisk is the disk of the store on every server: used, limit, reserve.
func adminDisk() []map[string]interface{} {
	limit, reserve := storeLimitMB(), storeReserveMB()
	svc := storeService()
	rows := []map[string]interface{}{}
	if svc == nil {
		return rows
	}
	add := func(node string, used float64) {
		rows = append(rows, map[string]interface{}{"node": node, "usedMB": int(used), "limitMB": limit, "reserveMB": reserve,
			"belowReserve": float64(limit)-used < float64(reserve)})
	}
	add("gateway", float64(svc.Store().Size())/(1<<20))
	appStoreMu.Lock()
	ids := make([]string, 0, len(appNodes))
	for id := range appNodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		add(id, appNodes[id].SizeMB)
	}
	appStoreMu.Unlock()
	return rows
}

// handleAdminCatalogue is the Catalogue tab of the admin: GET the list, packs and disk; POST one action.
func (server *Server) handleAdminCatalogue(w http.ResponseWriter, r *http.Request) {
	svc := storeService()
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "error": appStoreErr})
		return
	}
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("export") == "1" {
			raw, err := svc.Export()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", `attachment; filename="openrepl-catalogue.json"`)
			w.Write(raw)
			server.audit(r, "app store: catalogue export", "")
			return
		}
		s := GetSiteSettings()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"entries": svc.Catalogue(), "packs": svc.Packs(), "disk": adminDisk(), "repls": appstore.CatalogueRepls, "places": appstore.CataloguePlaces,
			"checks": appstore.CheckTemplates, "now": time.Now().Unix(), "retired": svc.Retired(),
			"settings": map[string]int{"limitMB": storeLimitMB(), "reserveMB": storeReserveMB(), "perDay": storeRequestsPerDay(),
				"scratchSoftMB": scratchSoftMB(), "scratchServerMB": scratchServerMB(), "limitSet": s.StoreLimitMB},
		})
		return
	}
	if r.Method != http.MethodPost || !sameOrigin(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	var c struct {
		Action  string          `json:"action"`
		Entry   appstore.Entry  `json:"entry"`
		Pack    appstore.Pack   `json:"pack"`
		ID      string          `json:"id"`
		Name    string          `json:"name"`
		Kind    string          `json:"kind"`
		Names   []string        `json:"names"`
		Repls   []string        `json:"repls"`
		Level   string          `json:"level"`
		Data    json.RawMessage `json:"data"`
		Dry     bool            `json:"dry"`
		Install bool            `json:"install"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&c) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	who := adminActor(r)
	out := map[string]interface{}{"ok": true}
	var err error
	detail := c.ID
	switch c.Action {
	case "add":
		detail = c.Entry.ID
		err = svc.AddEntry(who, c.Entry)
		if err == nil && c.Install {
			if c.Entry.InstallKind() != "" {
				err = svc.InstallEntry(who, c.Entry)
			}
		}
	case "remove":
		err = svc.RemoveEntry(who, c.ID)
	case "restore-removed":
		out["restored"] = svc.RestoreRemoved(c.Names)
		detail = strings.Join(c.Names, ",")
	case "purge-retired":
		var n int
		n, err = svc.PurgeRetired(c.Names)
		out["removed"] = n
		detail = strings.Join(c.Names, ",")
	case "install":
		e, ok := svc.Entry(c.ID)
		if !ok {
			err = appstore.ErrUnknown
		} else {
			err = svc.InstallEntry(who, e)
		}
		detail = c.ID
	case "compare":
		// the dry runs only (the apt plan and the pixi solve): seconds, nothing is installed; the sizes are marked as estimates
		e, ok := svc.Entry(c.ID)
		if !ok {
			err = appstore.ErrUnknown
			break
		}
		ce, rows := svc.Compare(r.Context(), e)
		if ce.Minimal != nil {
			if err = svc.AddEntry(who, ce); err != nil {
				break
			}
		}
		out["rows"], out["summary"] = rows, appstore.SummaryOf(rows)
		detail = c.ID
	case "measure":
		e, ok := svc.Entry(c.ID)
		if !ok {
			err = appstore.ErrUnknown
		} else if !startMeasure(svc, who, e) {
			err = fmt.Errorf("%s is being measured already", e.Name)
		}
		detail = c.ID
	case "plugin-default":
		err = svc.SetPluginDefault(who, c.ID, c.Install)
	case "lookup":
		kind := c.Kind
		res, lerr := svc.Estimate(r.Context(), c.Name, kind)
		if lerr != nil {
			out["solves"], out["error"] = false, lerr.Error()
		} else {
			out["solves"], out["version"], out["packages"], out["downloadMB"], out["diskMB"] = true, res.Version, res.Packages, res.DownloadMB, int(res.DiskMB)
			left := []map[string]interface{}{}
			for _, d := range adminDisk() {
				l := d["limitMB"].(int) - d["usedMB"].(int) - int(res.DiskMB)
				left = append(left, map[string]interface{}{"node": d["node"], "leftMB": l, "belowReserve": l < d["reserveMB"].(int)})
			}
			out["left"] = left
			if e, ok := svc.EntryFor(strings.ToLower(c.Name), kind); ok {
				cons := []string{}
				for _, k := range appstore.ConstraintsFor(e) {
					cons = append(cons, k[0]+" "+k[1])
				}
				out["constraints"] = cons // what the image's runtimes required of the solve
			}
		}
		detail = c.Name
	case "bulkdry":
		out["rows"] = svc.DryRun(r.Context(), c.Kind, c.Names)
		detail = fmt.Sprintf("%d names", len(c.Names))
	case "bulkadd":
		rows := svc.DryRun(r.Context(), c.Kind, c.Names) // the server asks again: the page's table is never trusted
		var n int
		mgr := appstore.MgrPixi
		if c.Kind == appstore.KindNode {
			mgr = appstore.MgrNPM
		}
		if n, err = svc.BulkAdd(who, c.Repls, c.Kind, c.Level, mgr, rows); err == nil {
			out["added"] = n
			if c.Install {
				for _, row := range rows {
					if row.Solves {
						svc.Install(who, row.Name, c.Kind, true)
					}
				}
			}
		}
		detail = fmt.Sprintf("%d added", n)
	case "packsave":
		detail = c.Pack.ID
		err = svc.SetPack(who, c.Pack)
	case "packremove":
		err = svc.RemovePack(who, c.ID)
	case "packinstall":
		var started []string
		var skipped map[string]string
		if started, skipped, err = svc.PackInstall(who, c.ID); err == nil {
			out["started"], out["skipped"] = started, skipped
		}
	case "import":
		var rep appstore.ImportReport
		if rep, err = svc.Import(who, c.Data, c.Dry); err == nil {
			out["report"] = rep
		}
		detail = fmt.Sprintf("dry=%v", c.Dry)
	case "verify":
		out["results"] = verifyAllHere(r, svc)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	server.audit(r, "app store catalogue: "+c.Action, detail)
	kickStore()
	writeJSON(w, http.StatusOK, out)
}

// verifyAllHere checks every installed app on this node (the gateway) and says which work. A worker checks its own apps when the
// gateway asks it for a user's switch (verify.go); "Verify now" is the gateway's own and the node rows tell the rest.
func verifyAllHere(r *http.Request, svc *appstore.Service) []map[string]string {
	res := []map[string]string{}
	for _, a := range svc.Apps() {
		if a.State != appstore.StateInstalled {
			continue
		}
		row := map[string]string{"name": a.Name, "state": "ok"}
		if err := appstore.RunVerify(r.Context(), svc.Store(), "verify-probe", a.ID, a.Name, a.Kind, "", nil); err != nil {
			row["state"], row["error"] = "failed", strings.TrimSpace(err.Error())
			svc.NoteCheckFailure(a.Name, "gateway", err.Error())
		}
		res = append(res, row)
	}
	return res
}

func scratchSoftMB() int {
	if n := GetSiteSettings().ScratchSoftMB; n > 0 {
		return n
	}
	return 256
}

func scratchServerMB() int {
	if n := GetSiteSettings().ScratchServerMB; n > 0 {
		return n
	}
	return 2048
}

// measuring is the entries that are being measured (one run for each at a time).
var measuring sync.Map

// startMeasure measures an entry on this server, in the background (it installs the entry with every manager that can, which takes minutes): the
// same code as scripts/measure-catalogue.sh, for the CPU type of this server. The result (the options with their sizes, the smallest chosen, the size
// shown) is saved as the admin's copy of the entry; the sizes of the other CPU type stay as they were. It returns false when it is already running.
func startMeasure(svc *appstore.Service, who string, e appstore.Entry) bool {
	if _, busy := measuring.LoadOrStore(e.ID, true); busy {
		return false
	}
	arch := appstore.ArchAMD64
	if runtime.GOARCH == "arm64" {
		arch = appstore.ArchARM64
	}
	go func() {
		defer measuring.Delete(e.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		work := filepath.Join(svc.Store().Dir, "measure")
		m := appstore.MeasureEnv{Svc: svc, Work: work, Arch: arch, AptPackage: appstore.AptHas}
		r := m.Measure(ctx, e)
		if sz := r.ShownMB(); sz > 0 {
			r.InstMB = int(sz + 0.999)
		}
		if err := svc.AddEntry(who, r); err != nil {
			log.Printf("app store: the measurement of %s could not be saved: %v", e.ID, err)
		}
	}()
	return true
}
