// Package tunnel carries gateway-to-worker traffic over one outbound SSH
// connection per worker. The worker dials the gateway (over a WebSocket on
// the gateway's public port, or raw TCP), registers, and then serves the
// channels the gateway opens, one per proxied HTTP request or WebSocket.
// See docs/lld/11-distributed-execution.md.
package tunnel

import "encoding/json"

// ProtocolVersion is bumped when the messages below change incompatibly.
const ProtocolVersion = 1

// ChannelHTTP is the SSH channel type the gateway opens for each proxied
// connection. The worker serves HTTP on it.
const ChannelHTTP = "openrepl-http"

// ChannelSync is the SSH channel type the gateway opens for workspace
// synchronization. One channel per worker connection carries every home.
const ChannelSync = "openrepl-sync"

// Subprotocol is the WebSocket subprotocol of the tunnel endpoint.
const Subprotocol = "openrepl-tunnel"

// Global SSH request names sent by the worker.
const (
	ReqRegister   = "register@openrepl"
	ReqHeartbeat  = "heartbeat@openrepl"
	ReqRouteOpen  = "route-open@openrepl"
	ReqRouteClose = "route-close@openrepl"
	// ReqSyncReady tells the gateway that the worker's homes are reconciled
	// and it can take new sessions.
	ReqSyncReady = "sync-ready@openrepl"
	// ReqStoreState is the worker's answer to a catalog of the App Store: what it has, what failed and why, and when it tries again.
	// It is sent when that changes (an install ended), not on a timer.
	ReqStoreState = "store-state@openrepl"
	// ReqStoreLock asks the gateway for the lock of one App Store bundle (the exact packages of its install), which the answer carries.
	ReqStoreLock = "store-lock@openrepl"
	// ReqStoreLog is the worker's answer to a request of the admin for a chunk of the log of an install (StoreUpdate.Logs).
	ReqStoreLog = "store-log@openrepl"
)

// Route kinds: keys that must reach the worker that owns them even when the
// request comes from another session.
const (
	RouteJID  = "jid"
	RouteHome = "home"
)

// RegisterRequest is the worker's first message.
type RegisterRequest struct {
	WorkerID        string   `json:"worker_id"`
	ProtocolVersion int      `json:"protocol_version"`
	Version         string   `json:"version"`
	OS              string   `json:"os"`
	Arch            string   `json:"arch"`
	Languages       []string `json:"languages,omitempty"`
	Capacity        int64    `json:"capacity"` // memory-weight units it can run
	Weight          int      `json:"weight"`
	// Ptrace is what the worker's host answered when asked whether programs can
	// be traced (openrepl-ptrace-probe): "ok", "ok-aslr", or why not. Empty means
	// the worker did not find out. The dashboard shows it; it decides nothing.
	Ptrace string `json:"ptrace,omitempty"`
}

// RegisterReply is the gateway's answer. It carries what the worker needs to
// act for the gateway: the cookie secret (so it can read session cookies) and
// the WebSocket auth token, and OPENREPL_SECRET when the gateway has one. They
// are kept in memory only.
type RegisterReply struct {
	ConnectionID    string `json:"connection_id"`
	HeartbeatMillis int    `json:"heartbeat_ms"`
	CookieSecret    []byte `json:"cookie_secret"`
	AuthToken       string `json:"auth_token"`
	// Secret is the gateway's OPENREPL_SECRET, for the worker's own use. It
	// replaces the worker's own for as long as the connection lasts.
	Secret string `json:"secret,omitempty"`
	// WorkspaceSync says the gateway keeps a copy of the worker's homes. The
	// worker then stays out of rotation (SYNCING) until it sends ReqSyncReady.
	WorkspaceSync bool `json:"workspace_sync,omitempty"`
	// Config is what the gateway wants the worker to follow right now; later
	// changes arrive in heartbeat replies.
	Config *WorkerConfig `json:"config,omitempty"`
}

// WorkerConfig is the part of the gateway's settings a worker follows: the site
// rules (maintenance, languages that are switched off, the announcement, the
// colour) that the gateway already applies to everybody it forwards, and that a
// worker applies to people who open the worker's own port. Nothing secret is in
// it. Revision identifies the content: a worker that has the revision the
// gateway has is up to date.
type WorkerConfig struct {
	Revision           int64    `json:"revision"`
	ColorOfTheDay      bool     `json:"color_of_the_day,omitempty"`
	AnnouncementText   string   `json:"announcement_text,omitempty"`
	AnnouncementLevel  string   `json:"announcement_level,omitempty"`
	Maintenance        bool     `json:"maintenance,omitempty"`
	MaintenanceMessage string   `json:"maintenance_message,omitempty"`
	DisabledLanguages  []string `json:"disabled_languages,omitempty"`
	Egress             bool     `json:"egress,omitempty"` // internet for signed-in users' REPLs through the proxy (LLD 16 "Egress")
	PreviewBaseURL     string   `json:"preview_base_url,omitempty"`
}

// StoreUpdate is the App Store's catalog as the gateway hands it to a worker (the tunnel does not look inside it). Rev identifies
// the content: a worker that reports the Rev the gateway has is up to date, and nothing is sent. Reinstall are the bundles an admin
// asked this worker to make again; they are given once.
type StoreUpdate struct {
	Rev       int64             `json:"rev"`
	Catalog   json.RawMessage   `json:"catalog,omitempty"`
	Reinstall []string          `json:"reinstall,omitempty"`
	Logs      []StoreLogRequest `json:"logs,omitempty"` // chunks of install logs an admin is reading; the worker answers each with ReqStoreLog
}

// StoreLogRequest asks a worker for a chunk of the log of an install of an app: the attempt (0: the newest) from a byte offset.
type StoreLogRequest struct {
	ID      string `json:"id"` // what the answer carries back
	App     string `json:"app"`
	Attempt int    `json:"attempt"`
	From    int64  `json:"from"`
}

// HeartbeatReply is the payload of the answer to a heartbeat. Config is set
// when the revision the worker reported is not the gateway's, and so is Store.
type HeartbeatReply struct {
	Config *WorkerConfig `json:"config,omitempty"`
	Store  *StoreUpdate  `json:"store,omitempty"`
}

// Heartbeat reports the worker's load.
type Heartbeat struct {
	Used   int64 `json:"used"`   // memory-weight units in use
	Active int   `json:"active"` // open terminal sessions
	// ConfigRev is the revision of the WorkerConfig the worker follows now (0:
	// none yet).
	ConfigRev int64 `json:"config_rev,omitempty"`
	// StoreRev is the revision of the App Store catalog the worker has now (0: none yet).
	StoreRev int64 `json:"store_rev,omitempty"`
}

// RouteEvent announces or withdraws a key the worker owns.
type RouteEvent struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}
