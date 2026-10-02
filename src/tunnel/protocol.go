// Package tunnel carries gateway-to-worker traffic over one outbound SSH
// connection per worker. The worker dials the gateway (over a WebSocket on
// the gateway's public port, or raw TCP), registers, and then serves the
// channels the gateway opens, one per proxied HTTP request or WebSocket.
// See docs/lld/11-distributed-execution.md.
package tunnel

// ProtocolVersion is bumped when the messages below change incompatibly.
const ProtocolVersion = 1

// ChannelHTTP is the SSH channel type the gateway opens for each proxied
// connection. The worker serves HTTP on it.
const ChannelHTTP = "openrepl-http"

// Subprotocol is the WebSocket subprotocol of the tunnel endpoint.
const Subprotocol = "openrepl-tunnel"

// Global SSH request names sent by the worker.
const (
	ReqRegister   = "register@openrepl"
	ReqHeartbeat  = "heartbeat@openrepl"
	ReqRouteOpen  = "route-open@openrepl"
	ReqRouteClose = "route-close@openrepl"
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
}

// RegisterReply is the gateway's answer. It carries what the worker needs to
// act for the gateway: the cookie secret (so it can read session cookies) and
// the WebSocket auth token. Both are kept in memory only.
type RegisterReply struct {
	ConnectionID    string `json:"connection_id"`
	HeartbeatMillis int    `json:"heartbeat_ms"`
	CookieSecret    []byte `json:"cookie_secret"`
	AuthToken       string `json:"auth_token"`
}

// Heartbeat reports the worker's load.
type Heartbeat struct {
	Used   int64 `json:"used"`   // memory-weight units in use
	Active int   `json:"active"` // open terminal sessions
}

// RouteEvent announces or withdraws a key the worker owns.
type RouteEvent struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}
