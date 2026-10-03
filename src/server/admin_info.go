package server

import (
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gateway"
	"utils"
)

var buildVersion, buildCommit string

// SetBuildInfo records the version and commit that /admin/gateway shows.
func SetBuildInfo(version, commit string) { buildVersion, buildCommit = version, commit }

var projectIDPattern = regexp.MustCompile(`["']?projectId["']?\s*:\s*"([^"]+)"`)

// What /admin/gateway tells an admin: how this server is set up. It is a
// whitelist, built field by field. A credential, a token, an API key or a
// password does not appear here, only whether one is set.
type paramsReply struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Mode      string `json:"mode"`
	Started   string `json:"started"`
	Uptime    int64  `json:"uptimeSeconds"`

	Listen struct {
		Address   string `json:"address"`
		Port      string `json:"port"`
		TLS       bool   `json:"tls"`
		BasicAuth bool   `json:"basicAuth"`
	} `json:"listen"`
	Limits struct {
		MaxConnection int  `json:"maxConnection"`
		PermitWrite   bool `json:"permitWrite"`
		Timeout       int  `json:"timeoutSeconds"`
	} `json:"limits"`
	Gateway *gatewayParams `json:"gateway,omitempty"`
	Config  configParams   `json:"config"`
	Counts  countParams    `json:"counts"`
	Runtime struct {
		Goroutines int    `json:"goroutines"`
		AllocMB    uint64 `json:"allocMB"`
		SysMB      uint64 `json:"sysMB"`
		CPUs       int    `json:"cpus"`
	} `json:"runtime"`
}

type gatewayParams struct {
	LocalWeight    int    `json:"localWeight"`
	WorkersEnabled bool   `json:"workersEnabled"`
	TunnelPath     string `json:"tunnelPath"`
	// TunnelURLPath is TunnelPath below the site's path prefix: what a worker
	// puts after the host in --worker-server.
	TunnelURLPath string `json:"tunnelUrlPath"`
	TunnelAddr    string `json:"tunnelAddr,omitempty"`
	HostKey       string `json:"hostKeyFingerprint,omitempty"`
	WorkspaceSync bool   `json:"workspaceSync"`
	RelocateAfter string `json:"relocateAfter,omitempty"`
	SyncStateDir  string `json:"syncStateDir,omitempty"`
}

type configParams struct {
	Env     string `json:"env"`
	EnvFile struct {
		Path    string   `json:"path,omitempty"`
		Loaded  int      `json:"loaded"`
		Kept    int      `json:"kept"`
		Ignored []string `json:"ignored,omitempty"`
	} `json:"envFile"`
	GitConfigFile string `json:"gitConfigFile,omitempty"`
	Firebase      struct {
		Custom    bool   `json:"custom"`
		ProjectID string `json:"projectId"`
	} `json:"firebase"`
	Admins       int    `json:"admins"`
	OpenAIKeySet bool   `json:"openaiKeySet"`
	Host         string `json:"host"`
}

type countParams struct {
	Sessions       int `json:"sessions"`
	Terminals      int `json:"terminals"`
	WorkersOnline  int `json:"workersOnline"`
	WorkersTotal   int `json:"workersTotal"`
	TerminalWeight int `json:"terminalWeightMB"`
}

// firebaseProject returns the Firebase project the page signs in with.
func firebaseProject() (projectID string, custom bool) {
	m := projectIDPattern.FindSubmatch(firebaseConfigJS())
	if m != nil {
		projectID = string(m[1])
	}
	_, custom, _ = utils.FirebaseConfigFromEnv()
	return projectID, custom
}

func (server *Server) params() paramsReply {
	var p paramsReply
	p.Version, p.Commit = buildVersion, buildCommit
	p.GoVersion, p.OS, p.Arch = runtime.Version(), runtime.GOOS, runtime.GOARCH
	p.Mode = server.options.Mode
	if p.Mode == "" {
		p.Mode = ModeStandalone
	}
	if !server.admin.started.IsZero() {
		p.Started = server.admin.started.UTC().Format(time.RFC3339)
		p.Uptime = int64(time.Since(server.admin.started).Seconds())
	}
	p.Listen.Address, p.Listen.Port = server.options.Address, server.options.Port
	p.Listen.TLS = server.options.EnableTLS
	p.Listen.BasicAuth = server.options.EnableBasicAuth
	p.Limits.MaxConnection = server.options.MaxConnection
	p.Limits.PermitWrite = server.options.PermitWrite
	p.Limits.Timeout = server.options.Timeout

	if server.options.Mode == ModeGateway {
		g := &gatewayParams{
			LocalWeight:    server.options.LocalWeight,
			WorkersEnabled: server.options.WorkerToken != "",
			TunnelPath:     server.options.TunnelPath,
			TunnelURLPath:  server.admin.prefix + strings.Trim(server.options.TunnelPath, "/"),
			TunnelAddr:     server.options.TunnelAddr,
			HostKey:        server.admin.hostKey,
			WorkspaceSync:  server.options.WorkspaceSync,
		}
		if g.WorkspaceSync {
			g.RelocateAfter = server.options.RelocateAfter
			g.SyncStateDir = server.syncStateDir()
		}
		p.Gateway = g
	}

	c := &p.Config
	c.Env = "production"
	if utils.IsDev() {
		c.Env = "dev"
	}
	if r := utils.LastEnvFile; r.Path != "" {
		c.EnvFile.Path, c.EnvFile.Loaded, c.EnvFile.Kept, c.EnvFile.Ignored = r.Path, len(r.Loaded), len(r.Kept), r.Ignored
	}
	c.GitConfigFile = utils.GitConfigPath
	c.Firebase.ProjectID, c.Firebase.Custom = firebaseProject()
	c.Admins = len(utils.AdminEmails())
	c.OpenAIKeySet = utils.OpenAIKey() != ""
	c.Host = chatHost()

	if server.admin.router != nil {
		p.Counts.Sessions = server.admin.router.Registry().Len()
		for _, b := range server.admin.router.Backends() {
			p.Counts.WorkersTotal++
			if b.State() == gateway.Online {
				p.Counts.WorkersOnline++
			}
		}
	}
	if server.admin.counter != nil {
		p.Counts.Terminals = server.admin.counter.count()
		p.Counts.TerminalWeight = server.admin.counter.weight()
	}
	// the terminals and memory the workers run count too
	if server.admin.router != nil && server.admin.tunnel != nil {
		for _, b := range server.admin.router.Backends() {
			if tw := server.admin.tunnel.Worker(b.ID()); tw != nil {
				p.Counts.Terminals += int(tw.Active())
				p.Counts.TerminalWeight += int(tw.Used())
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	p.Runtime.Goroutines = runtime.NumGoroutine()
	p.Runtime.AllocMB, p.Runtime.SysMB = m.Alloc>>20, m.Sys>>20
	p.Runtime.CPUs = runtime.NumCPU()
	return p
}

func (server *Server) handleAdminGateway(w http.ResponseWriter, r *http.Request) {
	adminJSON(w, http.StatusOK, server.params())
}
