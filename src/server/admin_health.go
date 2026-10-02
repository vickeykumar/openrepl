package server

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"syscall"
	"time"

	"containers"
	"gateway"
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
		add("Databases", healthOK, "feedback, blog and snippet databases are open")
	}

	if ready, total := containers.Status(); total == 0 {
		add("Sandbox", healthWarn, "no sandbox is configured")
	} else if ready == total {
		add("Sandbox", healthOK, fmt.Sprintf("%d of %d REPL sandboxes (cgroups) are ready", ready, total))
	} else {
		add("Sandbox", healthWarn, fmt.Sprintf("%d of %d REPL sandboxes are ready; the others run without namespaces or a memory limit", ready, total))
	}

	if n := len(utils.AdminEmails()); n == 0 {
		add("Admin accounts", healthFail, "nobody is configured as an admin (OPENREPL_ADMIN_EMAILS)")
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
	if utils.OpenAIKey() == "" {
		add("OpenAI key", healthWarn, "not set: Genie and the practice question generator do not work")
	} else {
		add("OpenAI key", healthOK, "set")
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
