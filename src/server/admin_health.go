package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"backup"
	"containers"
	"gateway"
	"homesnap"
	"objstore"
	"persist"
	"utils"
)

// healthCheck is one line of the health panel.
type healthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn or fail
	Detail string `json:"detail"`
}

const (
	healthOK   = "ok"
	healthWarn = "warn"
	healthFail = "fail"
)

func worst(a, b string) string {
	rank := map[string]int{healthOK: 0, healthWarn: 1, healthFail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// writable reports whether files can be created in dir.
func writable(dir string) error {
	f, err := ioutil.TempFile(dir, ".health-")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

func diskFree(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
}

func gb(n uint64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

func diskCheck(name, path string) healthCheck {
	free, total, err := diskFree(path)
	if err != nil || total == 0 {
		return healthCheck{name, healthWarn, "cannot read the disk usage of " + path}
	}
	pct := float64(free) / float64(total) * 100
	detail := fmt.Sprintf("%s free of %s (%.0f%%) in %s", gb(free), gb(total), pct, path)
	switch {
	case pct < 5:
		return healthCheck{name, healthFail, detail}
	case pct < 15:
		return healthCheck{name, healthWarn, detail}
	}
	return healthCheck{name, healthOK, detail}
}

// health runs the checks. It never repeats a secret: it says whether one is set.
// replicaCheck says how the databases of a server that keeps them in MongoDB or Firestore are read: from a
// local copy filled at start (the reads cost nothing there), or, for one that is off or broken, straight
// from the remote database, which on Firestore is a read for every record and what used the allowance up.
func replicaCheck(infos []persist.ReplicaInfo) (status, detail string) {
	if len(infos) == 0 {
		return healthOK, "no database is open yet"
	}
	var full, onDemand, direct, broken []string
	records := 0
	for _, i := range infos {
		switch {
		case i.Broken && i.Mode != "off":
			broken = append(broken, i.Name)
		case i.Mode == "full":
			full = append(full, fmt.Sprintf("%s %d", i.Name, i.Records))
			records += i.Records
		case i.Mode == "on demand":
			onDemand = append(onDemand, i.Name)
		default:
			direct = append(direct, i.Name)
		}
	}
	var parts []string
	if len(full) > 0 {
		parts = append(parts, fmt.Sprintf("copied at start and read locally: %s (%d records)", strings.Join(full, ", "), records))
	}
	if len(onDemand) > 0 {
		parts = append(parts, "kept on demand: "+strings.Join(onDemand, ", "))
	}
	switch {
	case len(broken) > 0:
		parts = append(parts, "the local copy could not be written, so reads go to the remote database again: "+strings.Join(broken, ", "))
		return healthWarn, strings.Join(parts, "; ")
	case len(direct) > 0:
		parts = append(parts, "read straight from the remote database (OPENREPL_REPLICA=off, or the copy could not be made): "+strings.Join(direct, ", ")+". Every list the dashboard opens is then a read for each record")
		return healthWarn, strings.Join(parts, "; ")
	}
	return healthOK, strings.Join(parts, "; ")
}

// firestoreAllowanceCheck is how much of Firestore's free daily allowance (persist.FirestoreFree*) the
// operations counted today use: a warning from 70 percent, a failure from 90, when the database starts
// answering 429 and the site's data is out of reach until the allowance starts again (midnight Pacific time).
func firestoreAllowanceCheck(reads, writes, deletes int64) (status, detail string) {
	parts := []struct {
		name        string
		used, limit int64
	}{{"reads", reads, persist.FirestoreFreeReads}, {"writes", writes, persist.FirestoreFreeWrites}, {"deletes", deletes, persist.FirestoreFreeDeletes}}
	worst := 0.0
	var texts []string
	for _, p := range parts {
		pct := float64(p.used) * 100 / float64(p.limit)
		if pct > worst {
			worst = pct
		}
		texts = append(texts, fmt.Sprintf("%s %s of %s", p.name, thousands(p.used), thousands(p.limit)))
	}
	detail = "today, as this server counts: " + strings.Join(texts, ", ")
	switch {
	case worst >= 90:
		return healthFail, detail + ". Nearly out of the free allowance: past it Firestore answers 429 until midnight Pacific time. Keep the admin lists closed, or move to the paid plan."
	case worst >= 70:
		return healthWarn, detail + ". More than 70% of the free allowance is used."
	}
	return healthOK, detail
}

func (server *Server) health() (status string, checks []healthCheck) {
	add := func(name, st, detail string) { checks = append(checks, healthCheck{name, st, detail}) }

	if err := writable(utils.GOTTY_PATH); err != nil {
		add("Data directory", healthFail, utils.GOTTY_PATH+" is not writable: "+err.Error())
	} else {
		add("Data directory", healthOK, utils.GOTTY_PATH+" is writable")
	}
	checks = append(checks, diskCheck("Disk space for data", utils.GOTTY_PATH))
	if _, err := os.Stat(utils.HOME_DIR); err == nil {
		checks = append(checks, diskCheck("Disk space for users' files", utils.HOME_DIR))
	}
	if err := writable(utils.LOG_PATH); err != nil {
		add("Log directory", healthWarn, utils.LOG_PATH+" is not writable: "+err.Error())
	} else {
		add("Log directory", healthOK, utils.LOG_PATH+" is writable")
	}

	switch {
	case feedback_db_handle == nil || blog_db_handle == nil:
		add("Databases", healthFail, "the feedback or blog database is not open")
	case snippet_db_handle == nil:
		add("Databases", healthWarn, "the snippet database is not open: share links are off")
	default:
		where := "files in " + utils.GOTTY_PATH
		if b := feedback_db_handle.Backend(); b != "unqlite" {
			where = storeTitle(b)
		}
		add("Databases", healthOK, "feedback, blog and snippet databases are open ("+where+")")
	}
	// MongoDB or Firestore was asked for, and the start-up test sent the
	// databases somewhere else
	if backend, notes := persist.Chosen(); notes != "" && (utils.MongoURI() != "" || utils.FirestoreConfigured()) && backend != persistWanted() {
		add("Database choice", healthWarn, "the start-up test fell back to "+backend+": "+notes)
	}

	// Firestore's free daily allowance (reads, writes and deletes), as this server counts it: a database that
	// runs out of it answers 429 and, at a restart, the server could not open it
	if persist.Remote() == "firestore" {
		st := persist.CurrentStats()
		s, detail := firestoreAllowanceCheck(st.Reads, st.Writes, st.Deletes)
		add("Firestore allowance", s, detail)
	}

	// the local copy of the remote database that every read is answered from (persist/replica.go)
	if persist.Remote() != "" {
		s, detail := replicaCheck(persist.ReplicaStatus())
		add("Local replica", s, detail)
	}

	// the filesystem sandbox (Landlock) is independent of the cgroup sandbox below
	ll := containers.LandlockStatus()
	switch {
	case ll.Active:
		add("Filesystem sandbox", healthOK, ll.Reason)
	case ll.Mode == utils.LandlockRequire:
		add("Filesystem sandbox", healthFail, "required (OPENREPL_LANDLOCK=require) but not available: "+ll.Reason+"; no REPL starts")
	case ll.Mode == utils.LandlockOff:
		add("Filesystem sandbox", healthWarn, ll.Reason+": REPLs can read the files of the server and of other users")
	default:
		add("Filesystem sandbox", healthWarn, "not available: "+ll.Reason+". REPLs can read the files of the server and of other users")
	}
	// the private network of a workspace, for a REPL that has no kernel network namespace of its own
	ne := containers.NetEmuStatus()
	switch {
	case ne.Active:
		add("Network isolation", healthOK, ne.Reason)
	case ne.Mode == utils.NetIsolationRequire:
		add("Network isolation", healthFail, "required (OPENREPL_NET_ISOLATION=require) but not available: "+ne.Reason+"; no REPL starts")
	case ne.Mode == utils.NetIsolationOff:
		add("Network isolation", healthWarn, ne.Reason+": REPLs without a namespace of their own share one network, so a port can be used by one user only and users can reach each other's servers")
	default:
		add("Network isolation", healthWarn, "not available: "+ne.Reason+". REPLs without a namespace of their own share one network")
	}
	if _, err := os.Stat(os.TempDir()); err == nil {
		checks = append(checks, diskCheck("Disk space for /tmp", os.TempDir()))
	}
	if ready, total := containers.Status(); total == 0 {
		add("Sandbox", healthWarn, "no sandbox is configured")
	} else if ready == total {
		add("Sandbox", healthOK, fmt.Sprintf("%d of %d REPL sandboxes (cgroups) are ready", ready, total))
	} else {
		add("Sandbox", healthWarn, fmt.Sprintf("%d of %d REPL sandboxes are ready; the others run without namespaces or a memory limit", ready, total))
	}

	if n := len(utils.AdminEmails()); n == 0 {
		add("Admin accounts", healthFail, "nobody is configured as an owner (OPENREPL_ADMIN_EMAILS), so nobody can add admins")
	} else if extra := len(utils.ExtraAdmins()); extra > 0 {
		add("Admin accounts", healthOK, fmt.Sprintf("%d owners and %d added in the dashboard", n, extra))
	} else {
		add("Admin accounts", healthOK, fmt.Sprintf("%d configured", n))
	}
	if project, custom := firebaseProject(); custom {
		add("Firebase", healthOK, "signing in with the project "+project+" (from OPENREPL_FIREBASE_CONFIG)")
	} else if utils.IsDev() {
		add("Firebase", healthWarn, "this dev server uses the built-in project, which is the production one ("+project+")")
	} else {
		add("Firebase", healthOK, "signing in with the built-in project "+project)
	}
	switch st := currentSettingsStoreStatus(); {
	case st.Healthy:
		add("Settings store", healthOK, st.Detail)
	case st.Degraded:
		add("Settings store", healthWarn, st.Detail)
	case st.Name != "file":
		add("Settings store", healthFail, st.Detail)
	default:
		add("Settings store", healthOK, st.Detail)
	}
	if utils.OpenAIKey() == "" {
		add("OpenAI key", healthWarn, "not set: Genie and the practice question generator do not work")
	} else {
		add("OpenAI key", healthOK, "set (from the "+keySourceName(utils.KeySource("openai"))+")")
	}
	if utils.OpenRouterKey() == "" {
		add("OpenRouter key", healthOK, "not set (optional): Gemma 4 31B is not offered")
	} else {
		add("OpenRouter key", healthOK, "set (from the "+keySourceName(utils.KeySource("openrouter"))+"): Gemma 4 31B is offered")
	}
	switch {
	case utils.CloudflareEnabled():
		add("Cloudflare Workers AI", healthOK, "token (from the "+keySourceName(utils.KeySource("cloudflare"))+") and account id are set: the Workers AI models are offered")
	case utils.CloudflareToken() != "" || utils.CloudflareAccountID() != "":
		add("Cloudflare Workers AI", healthWarn, "the token and the account id are both needed: the Workers AI models are not offered")
	default:
		add("Cloudflare Workers AI", healthOK, "not set (optional): the Workers AI models are not offered")
	}
	storage := objectStorageHealth()
	add("Object storage", storage.status, storage.detail)
	if utils.S3Enabled() {
		status, detail := backupHealth()
		add("Backups", status, detail)
		if homesnap.Default.Enabled() {
			status, detail := homesHealth()
			add("Workspace snapshots", status, detail)
		}
	}
	settingsMu.Lock()
	problem := keysProblem
	settingsMu.Unlock()
	switch {
	case problem != "":
		add("Dashboard keys", healthWarn, problem+"; the environment's keys are in use. Enter them again, or restore OPENREPL_SECRET")
	case utils.Secret() == "":
		add("Dashboard keys", healthWarn, "off: the server has no secret yet, so keys cannot be kept in the dashboard")
	case utils.EnvSecretValue() == "":
		add("Dashboard keys", healthOK, "on, encrypted with a secret the server generated and saved in its database (a copy of the whole database includes it)")
	default:
		add("Dashboard keys", healthOK, "on, encrypted with OPENREPL_SECRET, which is not saved in the database")
	}
	if utils.IsDev() {
		add("Mode", healthWarn, "dev: the start-up log shows the values of the settings. Do not use dev on a public server")
	} else {
		add("Mode", healthOK, "production")
	}
	if s := GetSiteSettings(); s.Maintenance.Enabled {
		add("Maintenance mode", healthWarn, "on: visitors cannot start terminals")
	}

	if r := server.admin.router; r != nil {
		if server.options.WorkspaceSync && server.admin.sync == nil {
			add("Workspace sync", healthFail, "is on, but its manager did not start")
		}
		for _, b := range r.Backends() {
			if b.ID() == gateway.LocalID {
				continue
			}
			state := b.State()
			switch state {
			case gateway.Online:
				add("Worker "+b.ID(), healthOK, "online")
			case gateway.Syncing:
				add("Worker "+b.ID(), healthWarn, "synchronizing its files; it takes no new sessions yet")
			case gateway.Draining:
				add("Worker "+b.ID(), healthWarn, "draining: it takes no new sessions")
			default:
				add("Worker "+b.ID(), healthWarn, state.String())
			}
			if m := server.admin.sync; m != nil {
				if off, ok := m.Offset(b.ID()); ok && (off > 2*time.Second || off < -2*time.Second) {
					add("Clock of "+b.ID(), healthWarn, fmt.Sprintf("differs from the gateway's by %.1f s; the wrong edit can win when both sides change a file", off.Seconds()))
				}
			}
		}
		if server.options.WorkerToken == "" {
			add("Workers", healthWarn, "the worker tunnel is off (no worker token set)")
		}
	}

	status = healthOK
	for _, c := range checks {
		status = worst(status, c.Status)
	}
	return status, checks
}

func (server *Server) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	status, checks := server.health()
	adminJSON(w, http.StatusOK, map[string]interface{}{"status": status, "checks": checks})
}

// keySourceName is how the health page names where a key comes from.
func keySourceName(source string) string {
	switch source {
	case "dashboard":
		return "dashboard"
	case "env":
		return "environment"
	case "file":
		return "settings file"
	}
	return "server"
}

// persistWanted is the backend the settings ask for first.
func persistWanted() string {
	if utils.MongoURI() != "" {
		return "mongodb"
	}
	return "firestore"
}

// objectStorageHealth says whether the object store (S3, R2) that holds the backups and the workspaces of
// signed-in users answers. A request to the bucket is made at most once a minute, whatever asks.
type objectStorageState struct {
	status string
	detail string
	at     time.Time
	key    string // the settings it was made for: a change of them makes it stale
}

func objectStorageSettingsKey() string {
	sum := sha256.Sum256([]byte(utils.S3SecretKey()))
	return utils.S3Endpoint() + "|" + utils.S3Bucket() + "|" + utils.S3AccessKey() + "|" + hex.EncodeToString(sum[:4])
}

var (
	objectStorageMu    sync.Mutex
	objectStorageCache objectStorageState
)

func objectStorageHealth() objectStorageState {
	objectStorageMu.Lock()
	defer objectStorageMu.Unlock()
	key := objectStorageSettingsKey()
	if time.Since(objectStorageCache.at) < time.Minute && objectStorageCache.detail != "" && objectStorageCache.key == key {
		return objectStorageCache
	}
	st := objectStorageState{at: time.Now(), key: key}
	switch {
	case !utils.S3Enabled() && (utils.S3Endpoint() != "" || utils.S3Bucket() != "" || utils.S3AccessKey() != "" || utils.S3SecretKey() != ""):
		st.status, st.detail = healthWarn, "set up in part: the endpoint, the bucket and both keys are all needed, so nothing is stored"
	case !utils.S3Enabled():
		st.status, st.detail = healthOK, "not set (optional): no backups, and workspaces stay on this server's disk"
	default:
		c, err := objstore.Default()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = c.Check(ctx)
			cancel()
		}
		if err != nil {
			st.status, st.detail = healthFail, "the bucket does not answer: "+redactErr(err).Error()
		} else {
			st.status, st.detail = healthOK, "bucket "+utils.S3Bucket()+" answers (from the "+keySourceName(utils.KeySource("s3-secret"))+")"
		}
	}
	objectStorageCache = st
	return st
}

// backupHealth sums up the backups for the Health page: when the last one was made and how big it was, a
// failure, or a backup that is late; and whether this server's files came back from a backup at start.
func backupHealth() (status, detail string) {
	st := backup.Default.Status()
	state := utils.JobOK
	for _, j := range utils.Jobs.Snapshot(time.Now()) {
		if j.ID == "backup" {
			state = j.State
		}
	}
	note := ""
	if st.Restored != nil {
		note = "; this server's files were restored from the backup of " + st.Restored.CreatedAt.Format("2006-01-02 15:04") + " UTC at start"
	}
	switch {
	case st.LastErr != "" && (st.LastOK.IsZero() || st.LastErrAt.After(st.LastOK)):
		return healthFail, "the last backup failed: " + st.LastErr + note
	case st.LastOK.IsZero():
		return healthOK, "no backup yet (the first is made a few minutes after the start, and only when there is something to back up)" + note
	case state == utils.JobLate:
		return healthWarn, "the backup job has not run for a long time; the last backup was " + time.Since(st.LastOK).Round(time.Minute).String() + " ago" + note
	}
	return healthOK, fmt.Sprintf("last backup %s ago, %.1f KB, %d files, %d archives kept%s", time.Since(st.LastOK).Round(time.Minute), float64(st.LastSize)/1024, len(st.Files), st.Kept, note)
}

// homesHealth sums up the snapshots of the signed-in users' workspaces.
func homesHealth() (status, detail string) {
	st := homesnap.Default.Status()
	note := ""
	if st.Restores > 0 {
		note = fmt.Sprintf("; %d restored from the object store since the start (the last: %s)", st.Restores, st.LastRestor)
	}
	switch {
	case st.LastErr != "" && st.LastErrAt.After(st.LastRun):
		return healthWarn, "the last snapshot failed: " + st.LastErr + note
	case st.Homes == 0:
		return healthOK, "no signed-in user's workspace has been used since the start" + note
	case st.LastRun.IsZero():
		return healthOK, fmt.Sprintf("%d workspaces are followed; the first round comes a few minutes after the start%s", st.Homes, note)
	}
	why := ""
	if len(st.Notes) > 0 {
		var parts []string
		for h, n := range st.Notes {
			parts = append(parts, h+": "+n)
		}
		sort.Strings(parts)
		why = "; not snapshotted yet: " + strings.Join(parts, ", ")
	}
	return healthOK, fmt.Sprintf("%d workspaces are followed, %d snapshots made since the start, the last round %s ago%s%s", st.Homes, st.Total, time.Since(st.LastRun).Round(time.Minute), why, note)
}
