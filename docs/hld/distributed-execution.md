# OpenREPL Distributed Execution

## High-Level Design (HLD)

**Status:** Proposed  
**Version:** 2.0  
**Target:** OpenREPL Distributed / Hybrid Execution  
**Architecture Foundation:** Existing OpenREPL + `sish-lb` reverse-tunneling architecture

---

# 1\. Overview

OpenREPL currently executes REPL sessions on the OpenREPL server.

The goal of this design is to support distributed execution where:

1. The public OpenREPL server remains the single entry point.
2. Some sessions execute on the public server itself.
3. Other sessions execute on remote OpenREPL worker machines.
4. Workers may be behind NAT, firewalls, or private networks.
5. Workers establish outbound persistent connections to the OpenREPL server.
6. The browser continues using the same OpenREPL URLs and APIs.
7. The server transparently routes requests to the backend that owns the user's execution context.
8. Administrative APIs always execute locally on the gateway.
9. All non-admin APIs are execution-context-affinity controlled.

The architecture should reuse the reverse tunneling, multiplexing, and load-balancing concepts already implemented in `sish-lb` rather than introducing a completely new tunnel architecture.

---

# 2\. Core Architectural Principle

The most important rule is:

> **Admin APIs are gateway-local. Every other API is execution-context-affine.**

Therefore:

``` text
                     Browser
                        |
                        v
              +--------------------+
              |  OpenREPL Gateway   |
              +----------+---------+
                         |
             +-----------+-----------+
             |                       |
         /admin/*               Everything else
             |                       |
             v                       v
       Local Admin              Execution Manager
        Handlers                      |
                              Session/Context Registry
                                      |
                           +----------+----------+
                           |                     |
                         LOCAL                REMOTE
                           |                     |
                           v                     v
                    Existing OpenREPL       Reverse Tunnel
                       handlers                  |
                                                 v
                                              Worker
```

The gateway therefore acts as both:

- the normal OpenREPL application server
- a session-aware reverse proxy for remote execution

---

# 3\. Deployment Modes

## 3.1 Standalone Mode

No workers are configured.

``` text
Browser
   |
   v
OpenREPL Server
   |
   v
Existing OpenREPL
implementation
```

All non-admin requests execute locally.

This preserves current OpenREPL behavior.

---

## 3.2 Server / Gateway Mode

The server can execute sessions locally and also distribute sessions to workers.

``` text
                       Internet
                          |
                          v
                 OpenREPL Gateway
                  /             \
                 /               \
          Local Backend       Worker Pool
                               /   |   \
                              /    |    \
                         Worker1 Worker2 Worker3
```

---

## 3.3 Worker Mode

A worker does not need to expose a public listening port.

Instead:

``` text
Worker
   |
   | outbound connection
   v
OpenREPL Gateway
```

This allows workers to operate behind NAT/firewalls.

The worker maintains a persistent authenticated connection to the gateway.

---

# 4\. Request Routing Model

All requests first enter the OpenREPL gateway.

The gateway performs only two top-level decisions:

``` text
                 HTTP/WebSocket Request
                          |
                          v
                   Is /admin/* ?
                    /          \
                  YES           NO
                   |             |
                   v             v
             Local Admin    Resolve Execution
               Handler         Context
                                |
                         +------+------+
                         |             |
                       Local         Remote
                         |             |
                         v             v
                  Existing API     Reverse Tunnel
                    Handler            |
                                      v
                                    Worker
```

---

# 5\. Admin APIs

All administrative APIs are always handled by the gateway.

Examples:

``` text
/admin/*
/admin/workers
/admin/sessions
/admin/metrics
/admin/config
/admin/health
/admin/debug
```

They must never be forwarded to a worker.

Example:

``` go
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if isAdminAPI(r) {
        s.adminRouter.ServeHTTP(w, r)
        return
    }

    s.executionRouter.ServeHTTP(w, r)
}
```

This guarantees that worker availability cannot prevent administrators from inspecting or controlling the gateway.

---

# 6\. Non-Admin API Affinity

Every API outside `/admin/*` is associated with an execution context.

Examples include:

``` text
/login
/logout
/filebrowser
/upload
/download
/save
/delete
/rename
/repl/*
/terminal/*
/ws/*
```

The API path itself does **not** determine the worker.

The session/execution context determines the worker.

For example:

``` text
Session ABC
    |
    +-- /filebrowser  -> worker-02
    +-- /upload       -> worker-02
    +-- /download     -> worker-02
    +-- /save         -> worker-02
    +-- /terminal     -> worker-02
    +-- WebSocket     -> worker-02
```

This is critical because the terminal and filesystem must operate against the same workspace.

---

# 7\. Execution Context

The gateway maintains an execution context for each user execution session.

``` go
type ExecutionContext struct {
    SessionID   string
    WorkspaceID string

    BackendID   string
    WorkerID    string

    UserID      string

    CreatedAt   time.Time
    ExpiresAt   time.Time
}
```

The most important fields are:

``` text
SessionID
    |
    +--> WorkspaceID
    |
    +--> BackendID
             |
             +--> local
             OR
             +--> worker-01
             +--> worker-02
             +--> worker-03
```

---

# 8\. Session Affinity

Load balancing happens only when a new execution context is created.

Example:

``` text
New Session
    |
    v
Worker Pool
    |
    v
Weighted Selection
    |
    v
worker-02
    |
    v
SessionRegistry
    |
    v
session-ABC -> worker-02
```

Every subsequent request uses:

``` text
session-ABC
      |
      v
SessionRegistry
      |
      v
worker-02
```

It does not call the load balancer again.

Therefore:

> **Load balancing selects ownership. Session affinity preserves ownership.**

---

# 9\. Backend Abstraction

The gateway should abstract local and remote execution behind a common backend interface.

``` go
type Backend interface {
    ID() string

    Health() HealthStatus

    Capabilities() []Capability

    Load() BackendLoad

    CreateTerminal(
        ctx context.Context,
        params TerminalParams,
    ) (Slave, error)

    Workspace(
        ctx context.Context,
        workspaceID string,
    ) Workspace
}
```

Two primary implementations:

``` text
Backend
  |
  +-- LocalBackend
  |
  +-- RemoteBackend
```

---

# 10\. LocalBackend

The local backend represents the existing OpenREPL server itself.

``` text
Gateway
   |
   v
LocalBackend
   |
   v
Existing OpenREPL implementation
```

The goal is to avoid rewriting existing handlers.

For example:

``` go
if context.BackendID == localBackend.ID() {
    s.router.ServeHTTP(w, r)
    return
}
```

Therefore existing APIs can continue to use:

``` text
Gorilla handlers
WebTTY
PTY
filesystem
REPL factory
existing session logic
```

without becoming distributed-aware.

---

# 11\. RemoteBackend

A remote backend represents an OpenREPL worker.

``` text
Gateway
   |
   v
RemoteBackend
   |
   v
WorkerConnection
   |
   v
Reverse Tunnel
   |
   v
Worker
```

The remote backend does not need to implement the actual REPL.

It forwards the request/stream to the worker, where the existing OpenREPL implementation handles it.

---

# 12\. Worker Architecture

A worker runs OpenREPL in worker mode.

Conceptually:

``` text
+-----------------------------+
|       OpenREPL Worker       |
|                             |
|  Worker Controller          |
|       |                     |
|       +-- Registration      |
|       +-- Heartbeat         |
|       +-- Stream Manager    |
|       +-- Existing Server   |
|              |              |
|              +-- REPL       |
|              +-- Workspace  |
|              +-- File APIs  |
+--------------+--------------+
               |
               | persistent outbound
               v
        OpenREPL Gateway
```

---

# 13\. Worker Registration

The worker initiates the connection.

``` text
Worker
   |
   | Connect
   v
Gateway
   |
   | Authenticate
   v
Worker
   |
   | REGISTER
   v
Gateway
   |
   | REGISTER_ACK
   v
Worker
```

Registration message:

``` text
REGISTER {
    worker_id
    version
    platform
    architecture
    capabilities
    max_sessions
    weight
}
```

Gateway response:

``` text
REGISTER_ACK {
    worker_id
    connection_id
    protocol_version
    heartbeat_interval
}
```

After registration, the connection becomes the worker's reverse tunnel.

---

# 14\. Worker Registry

The gateway maintains:

``` go
type Worker struct {
    ID string

    Connection *WorkerConnection

    Capabilities map[string]bool

    Weight     int
    MaxSessions int
    Active     int

    State WorkerState

    LastSeen time.Time
}
```

Worker states:

``` text
ONLINE
DRAINING
OFFLINE
```

### ONLINE

Accept new sessions.

### DRAINING

Do not assign new sessions.

Existing sessions continue.

### OFFLINE

Do not assign new sessions.

Existing sessions are considered unavailable.

---

# 15\. Worker Heartbeat

Workers periodically send load information.

``` text
HEARTBEAT {
    worker_id
    active_sessions
    cpu
    memory
    timestamp
}
```

The gateway updates the worker registry.

Example:

``` text
worker-01
    active = 4
    CPU = 35%

worker-02
    active = 1
    CPU = 15%

worker-03
    active = 8
    CPU = 72%
```

The load balancer can use these values together with configured weight and capacity.

---

# 16\. Worker Disconnect

If the persistent connection disappears:

``` text
Worker
   X
Gateway
```

the gateway immediately marks:

``` text
worker-02 -> OFFLINE
```

It is removed from new-session selection.

Existing sessions assigned to that worker are not automatically migrated.

Reason:

``` text
PTY state
process state
filesystem state
WebSocket state
REPL memory state
```

cannot safely be recreated by simply selecting another worker.

Therefore:

> **No automatic live-session migration in v1.**

---

# 17\. Reconnection

A disconnected worker reconnects:

``` text
Worker
   |
   | Connect
   v
Gateway
   |
   | Authenticate
   |
   | REGISTER
   v
Worker Registry
   |
   v
ONLINE
```

The worker can then receive new sessions.

---

# 18\. Tunnel Architecture

The tunnel should be based on the existing `sish-lb` architecture.

The repository already provides SSH-based tunneling, HTTP/HTTPS multiplexing, WebSocket support, TCP forwarding, and weighted load balancing.

The OpenREPL adaptation changes the routing key.

### Existing sish-style model

``` text
hostname/subdomain
       |
       v
ServerPool
       |
       v
backend
```

### OpenREPL model

``` text
session/execution context
       |
       v
SessionRegistry
       |
       v
backend
```

The underlying reverse connection and multiplexing concepts can therefore be reused while replacing hostname affinity with execution affinity.

---

# 19\. Stream Multiplexing

A single worker should maintain one persistent connection to the gateway rather than one connection per browser session.

Conceptually:

``` text
Worker Connection
       |
       +-- Stream 1 -> Session A
       |
       +-- Stream 2 -> Session B
       |
       +-- Stream 3 -> Session C
       |
       +-- Stream 4 -> Session D
```

The connection carries:

``` text
CONTROL
REGISTER
HEARTBEAT
DRAIN
UNREGISTER

DATA STREAMS
OPEN_STREAM
DATA
CLOSE_STREAM
```

A conceptual interface:

``` go
type Tunnel interface {
    OpenStream(ctx context.Context) (net.Conn, error)
}
```

The implementation should reuse the existing multiplexing mechanisms from `sish-lb` wherever practical rather than inventing an entirely separate transport.

---

# 20\. Transparent HTTP Forwarding

For a remote request, the gateway should preserve the original HTTP request semantics.

Example browser request:

``` http
POST /upload HTTP/1.1
Host: openrepl.example.com
Cookie: session=abc123
Content-Type: application/octet-stream
Content-Length: ...
```

The worker should receive the equivalent request.

Important properties:

``` text
HTTP method       preserved
URL/path          preserved
query parameters  preserved
headers           preserved
cookies           preserved
body              preserved
WebSocket upgrade preserved
```

This allows existing Gorilla handlers on the worker to continue operating normally.

---

# 21\. Cookie and Session Handling

The browser remains unaware of worker assignment.

``` text
Browser
  |
  | Cookie: session=abc123
  v
Gateway
  |
  v
SessionRegistry
  |
  v
worker-02
```

The gateway remains authoritative for session-to-backend mapping.

For backward compatibility, the original `Cookie` header can be forwarded to the worker.

Long term, the worker should additionally receive a trusted internal execution context from the gateway.

Example:

``` text
X-OpenREPL-Session
X-OpenREPL-Workspace
X-OpenREPL-Worker
```

These headers must never be trusted when received directly from the public internet.

They are only meaningful when injected by the authenticated gateway-to-worker channel.

---

# 22\. WebSocket Routing

WebSocket requests follow exactly the same affinity rules.

``` text
Browser
   |
   | WebSocket /ws
   v
Gateway
   |
   v
SessionRegistry
   |
   v
worker-02
   |
   v
WebSocket stream
```

The gateway bridges the bidirectional stream.

``` text
Browser <=====> Gateway <=====> Worker
                  |
             Session affinity
```

A new load-balancing decision must not occur after WebSocket establishment.

---

# 23\. File Browser and Workspace Affinity

Filesystem APIs must use the same execution context as the terminal.

Example:

``` text
Session ABC
     |
     +--> Terminal
     |
     +--> File Browser
     |
     +--> Upload
     |
     +--> Download
     |
     +--> Save
     |
     +--> Delete
```

All operate against:

``` text
Workspace ABC
        |
        v
worker-02
```

This prevents the common distributed-system failure where:

``` text
Terminal -> worker-02
File Browser -> worker-03
```

and the user sees inconsistent files.

---

# 24\. Workspace Creation

If a request requires a workspace but no execution context exists:

``` text
Request
   |
   v
Create ExecutionContext
   |
   v
Select Backend
   |
   v
Create Workspace
   |
   v
Persist Mapping
```

Example:

``` text
Session ABC
Workspace ABC
Backend worker-02
```

All subsequent APIs reuse this mapping.

---

# 25\. New Session Flow

``` text
                    Browser
                       |
                       | New session request
                       v
               OpenREPL Gateway
                       |
                       v
              ExecutionManager
                       |
                       v
               SessionRegistry
                       |
                 no context
                       |
                       v
                 ServerPool
                       |
          +------------+------------+
          |            |            |
        Local       Worker-01     Worker-02
                                   selected
                                      |
                                      v
                              ExecutionContext
                                      |
                                      v
                                  worker-02
                                      |
                                      v
                                Create session
```

---

# 26\. Existing Session Flow

``` text
Browser
   |
   | Cookie / Session
   v
Gateway
   |
   v
SessionRegistry
   |
   v
Session ABC
   |
   v
Backend = worker-02
   |
   v
Reverse Tunnel
   |
   v
Worker-02
```

The load balancer is not consulted.

---

# 27\. Local Session Flow

A session may also be assigned to the local backend.

``` text
Browser
   |
   v
Gateway
   |
   v
SessionRegistry
   |
   v
Backend = local
   |
   v
Existing OpenREPL Handler
```

There is no tunnel overhead.

---

# 28\. Backend Selection

The selection algorithm should reuse/adapt the existing `ServerPool` logic from `sish-lb`.

Selection should consider:

``` text
Capability
Health
ONLINE state
DRAINING state
Maximum sessions
Current active sessions
Configured weight
Optional resource load
```

Example:

``` text
                Candidate Backends
                       |
          +------------+------------+
          |            |            |
        Local       Worker-01     Worker-02
        healthy       full          healthy
          |            |               |
          +------------+---------------+
                       |
                       v
                 Selection
                       |
                       v
                  Worker-02
```

---

# 29\. Backend Interface

Recommended conceptual design:

``` go
type Backend interface {
    ID() string

    State() BackendState

    Capabilities() []Capability

    Load() BackendLoad

    CreateTerminal(
        ctx context.Context,
        params TerminalParams,
    ) (Slave, error)

    Proxy(
        ctx context.Context,
        w http.ResponseWriter,
        r *http.Request,
    ) error
}
```

Implementations:

``` text
LocalBackend
RemoteBackend
```

---

# 30\. ExecutionManager

The `ExecutionManager` becomes the central orchestration component.

Responsibilities:

``` text
Create execution contexts
Resolve execution contexts
Select backend
Maintain session affinity
Manage workspace affinity
Release execution contexts
Handle worker failures
```

Conceptually:

``` go
type ExecutionManager interface {
    Create(ctx context.Context, params CreateParams) (*ExecutionContext, error)

    Resolve(sessionID string) (*ExecutionContext, error)

    Release(sessionID string) error

    Backend(ctx *ExecutionContext) Backend
}
```

---

# 31\. Component Architecture

``` text
+-------------------------------------------------------+
|                    OpenREPL Gateway                   |
|                                                       |
|  +-------------+     +-----------------------------+ |
|  | HTTP Router | --> | Execution Router             | |
|  +-------------+     +-------------+---------------+ |
|                                  |                    |
|                    +-------------+-------------+      |
|                    |                           |      |
|             Admin API                    Execution     |
|             Local Only                   Manager      |
|                                                |      |
|                                     +----------+-----+|
|                                     |                ||
|                              SessionRegistry   BackendRegistry
|                                     |                |
|                                     +--------+-------+
|                                              |
|                                  +-----------+-----------+
|                                  |                       |
|                            LocalBackend           RemoteBackend
|                                  |                       |
|                                  v                       v
|                         Existing OpenREPL        WorkerConnection
|                                                          |
+----------------------------------------------------------+
                                                           |
                                                    Reverse Tunnel
                                                           |
                           +-------------------------------+ 
                           |
                           v
                    +-------------+
                    |   Worker    |
                    |             |
                    | OpenREPL    |
                    | Handlers    |
                    | REPL/PTY    |
                    | Workspace   |
                    +-------------+
```

---

# 32\. Suggested Package Structure

Adapt this to the existing OpenREPL repository rather than creating a parallel architecture.

``` text
server/
    router.go
    execution_router.go

    execution/
        manager.go
        context.go
        session_registry.go

    backend/
        backend.go
        local.go
        remote.go
        registry.go
        load_balancer.go

    worker/
        registry.go
        connection.go
        protocol.go
        registration.go
        heartbeat.go
        stream.go

    proxy/
        http.go
        websocket.go
        stream.go
```

Worker-specific code:

``` text
worker/
    worker.go
    connection.go
    registration.go
    heartbeat.go
    stream.go
```

Existing OpenREPL code should remain where practical.

---

# 33\. Configuration

Example gateway configuration:

``` yaml
distributed:
  enabled: true

  worker:
    auth_token: "..."

  load_balancer:
    enabled: true

  heartbeat:
    interval: 10s
    timeout: 30s
```

Worker:

``` yaml
worker:
  enabled: true

  id: worker-01

  server:
    address: openrepl.example.com

  auth:
    token: "..."

  capabilities:
    - python
    - go
    - cpp
```

---

# 34\. Compatibility Requirement

When distributed mode is disabled:

``` text
distributed.enabled = false
```

OpenREPL must behave exactly as before.

``` text
Browser
   |
   v
Existing OpenREPL
```

No worker registry should be required.

No reverse tunnel should be required.

No distributed dependencies should be required.

This provides a safe migration path.

---

# 35\. Security

The worker connection must be authenticated.

Recommended:

``` text
TLS
+
Worker credential
+
Worker identity
```

Higher-security deployments can use mutual TLS.

The worker should never expose its execution APIs publicly.

Preferred topology:

``` text
Internet
   |
   v
Gateway
   |
   | authenticated outbound tunnel
   v
Worker
```

Not:

``` text
Internet
   |
   +----> Worker public HTTP API
```

---

# 36\. Trust Boundary

``` text
                 UNTRUSTED
                    |
                    v
              Browser/Internet
                    |
                    v
            +---------------+
            |    Gateway    |
            |               |
            | Auth/session  |
            | authority     |
            +-------+-------+
                    |
             TRUSTED CHANNEL
                    |
                    v
              +-----------+
              |  Worker   |
              +-----------+
```

The gateway is responsible for:

``` text
Authentication
Session validation
Execution ownership
Worker selection
Worker authorization
```

---

# 37\. Failure Handling

## Gateway -\> Worker connection failure

``` text
Worker connection lost
        |
        v
Mark OFFLINE
        |
        v
Remove from new-session pool
        |
        v
Existing sessions -> unavailable
```

No migration in v1.

## Worker process failure

Same behavior.

## Worker overloaded

Move worker to:

``` text
DRAINING
```

Existing sessions continue.

New sessions are assigned elsewhere.

---

# 38\. Worker Drain Flow

``` text
Admin
  |
  v
/admin/workers/worker-02/drain
  |
  v
Worker State = DRAINING
  |
  +--> Existing sessions continue
  |
  +--> New sessions rejected
  |
  v
Active sessions = 0
  |
  v
Worker can disconnect safely
```

---

# 39\. Observability

Gateway should expose local admin information such as:

``` text
Worker ID
State
Connection status
Last heartbeat
Active sessions
Maximum sessions
CPU
Memory
Capabilities
Weight
```

Example:

``` text
worker-01   ONLINE     4/10
worker-02   ONLINE     2/10
worker-03   DRAINING  7/10
```

These are admin APIs and therefore remain gateway-local.

---

# 40\. Important Routing Invariant

The system must enforce:

``` text
ADMIN API
    -> Gateway only

NON-ADMIN API
    -> ExecutionContext
    -> Assigned Backend
```

Never:

``` text
NON-ADMIN API
    -> Random Worker
```

after a session has already been assigned.

---

# 41\. Example End-to-End Scenario

Assume:

``` text
Gateway
Worker-01
Worker-02
```

User opens OpenREPL.

### Step 1 — Session creation

``` text
Browser
   |
   v
Gateway
   |
   v
LoadBalancer
   |
   v
Worker-02 selected
```

Registry:

``` text
session-123
    backend = worker-02
    workspace = workspace-123
```

### Step 2 — Terminal

``` text
/ws/terminal
      |
      v
session-123
      |
      v
worker-02
```

### Step 3 — File browser

``` text
/filebrowser
      |
      v
session-123
      |
      v
worker-02
```

### Step 4 — Upload

``` text
/upload
      |
      v
session-123
      |
      v
worker-02
```

### Step 5 — Admin

``` text
/admin/workers
      |
      v
Gateway
      |
      v
Local Admin Handler
```

No worker is involved.

---

# 42\. Why This Architecture

This design provides:

### Transparent API behavior

The browser does not need to know whether execution is local or remote.

### Strong session affinity

All session APIs reach the same execution environment.

### Filesystem consistency

Terminal and file operations use the same workspace.

### NAT/firewall compatibility

Workers establish outbound connections.

### Minimal OpenREPL changes

Existing handlers remain largely unchanged.

### Reuse of sish-lb

Reverse connectivity, multiplexing, HTTP/WS forwarding, and load-balancing concepts can be reused.

### Simple deployment

No Kubernetes, Redis, Kafka, or external service discovery is required for v1.

---

# 43\. What Should Be Reused From sish-lb

The existing repository already describes itself as an SSH-based serveo/ngrok-style tunnel with HTTP(S), WS(S), TCP support and a randomized weighted load-balancing feature.

Recommended reuse:

| sish-lb concept | OpenREPL adaptation |
| --- | --- |
| SSH/reverse connection | Worker persistent connection |
| Connection multiplexing | Worker stream multiplexing |
| ServerPool | Backend pool |
| Weighted selection | New execution selection |
| Proxy connection | RemoteBackend connection |
| HTTP forwarding | OpenREPL API forwarding |
| WebSocket forwarding | Terminal forwarding |
| TCP forwarding | Transparent stream transport |
| Connection lifecycle | Worker lifecycle |
| Ping/health | Worker heartbeat |

Do **not** reuse the original hostname/subdomain as the primary routing key.

OpenREPL routing key:

``` text
SessionID -> ExecutionContext -> Backend
```

---

# 44\. What Should NOT Be Added in V1

Avoid introducing:

``` text
Kubernetes
Redis
Kafka
Service mesh
External service discovery
Cross-worker session migration
Distributed database
Multiple gateway coordination
```

unless a real requirement emerges.

The first implementation should remain:

``` text
Gateway
   +
LocalBackend
   +
WorkerRegistry
   +
ServerPool
   +
SessionRegistry
   +
Reverse Tunnel
   +
RemoteBackend
```

---

# 45\. Final Architecture

``` text
                         INTERNET
                            |
                            v
                  +---------------------+
                  |   OpenREPL Gateway  |
                  |                     |
                  |   HTTP/WebSocket    |
                  +----------+----------+
                             |
                +------------+-------------+
                |                          |
             /admin/*                everything else
                |                          |
                v                          v
          Local Admin API          Execution Manager
                                           |
                                  Session Registry
                                           |
                              +------------+------------+
                              |                         |
                           LOCAL                      REMOTE
                              |                         |
                              v                         v
                     LocalBackend               RemoteBackend
                              |                         |
                              v                         v
                    Existing OpenREPL          WorkerConnection
                       implementation                |
                                                      |
                                              Multiplexed Tunnel
                                                      |
                         +----------------------------+----------------+
                         |                             |               |
                         v                             v               v
                    Worker-01                     Worker-02        Worker-03
                         |                             |               |
                    OpenREPL                        OpenREPL        OpenREPL
                    handlers                        handlers        handlers
                    REPL/PTY                        REPL/PTY        REPL/PTY
                    workspace                       workspace       workspace
```

## Core invariant

``` text
                     SESSION
                        |
                        v
                ExecutionContext
                        |
                        v
                     Backend
                        |
              +---------+---------+
              |                   |
            Local               Remote
              |                   |
       Existing handlers     Reverse tunnel
                                  |
                                  v
                                Worker
```

**The gateway owns routing. The worker owns execution. The browser remains unaware of where execution happens.**
---

# 46\. Implementation Decisions

Settled after inspecting the OpenREPL codebase and `sish-lb`:

- **Transport:** SSH (`golang.org/x/crypto/ssh`), carried over a WebSocket on the gateway's public port (`wss://<gateway>/api/tunnel`, recommended) or, optionally, over raw TCP (`ssh://<gateway>:2222`). Both feed the same SSH server code. The worker dials out once; the gateway opens one SSH channel per proxied request or WebSocket. SSH keepalives serve as the heartbeat and connection state as ONLINE/OFFLINE.
- **sish-lb reuse:** `sish-lb` is a `package main` driven by global flags, so it cannot be imported. The `ServerPool` weighted selection, the reverse-proxy-over-channel pattern and `copyBoth`/`IdleTimeoutConn` are extracted and adapted into `src/` (GOPATH layout, `GO111MODULE=off`). The hostname routing key is replaced by the session key.
- **Cookie secret:** the gateway syncs the session-cookie secret to each worker in the registration reply on every connect and reconnect (memory only, never persisted or logged), and forwards the `Cookie` header, so workers can read the signed session cookie. Session ids and the user DB stay on the gateway. The gateway still validates the session and injects trusted identity headers (uid, privilege, home-ID); workers honour them only on the tunnel listener.
- **Session key:** the gateway keeps its own `uid -> backend` registry, because OpenREPL sessions are signed cookies with no server-side store. The workspace is the user's `homedir`.
- **Hardening required:** the gateway must strip client-supplied `homedir` and `jid` parameters, and route `jid` forks to the worker that owns the parent process.
- **`jid` forks:** a fork request carries a `jid` that `encodePID` maps to a process-local PID (`containers.GetWorkingDir`). The gateway records `pid -> worker` when the window-title message (which contains the `jid`) passes through it, and routes any request with that `jid` to the owning worker. If the record is missing or the worker is `OFFLINE`, the fork fails; it is never re-balanced.
- **Logged-in users are pinned:** a signed-in user's saved files live on one worker's disk, so the gateway stores a durable `uid -> worker` and always places that user's new sessions there. If that worker is `DRAINING` or `OFFLINE`, new sessions get a clear "your workspace node is unavailable" error rather than landing on an empty disk. Guests have disposable homedirs and are placed freely by the pool.
- **Capacity = cgroup memory weight:** each worker reports its total weight capacity based on its RAM, using the existing per-command weights (`containers.GetCommandWieght`, the same weights behind `--max-connection` admission control). The gateway's `max_sessions`, `Active` and selection use that weight budget rather than a plain session count.
- **Only execution-bound routes use affinity:** `ws`, `ws_*`, `ws_filebrowser` and `upload_file` follow the session's backend. Account and website routes (login, profile, blog, snippets, practice, chat proxy, static assets, index) always run on the gateway. This narrows the "every non-admin API" wording in section 6.
- **Run is carried in the WebSocket init payload** and saved by `SaveIdeContentToFile` on the executing node; no separate upload or file sync.
- **Homedir is created on the executing node.** The gateway's index handler must not create a homedir in gateway mode. Workers resolve the homedir from trusted headers (a gateway-computed home-ID for signed-in users, a guest id for guests) because they have no user-profile DB.
- **`jid` is reported by the worker** over the control channel (`jid-open`/`jid-close`) rather than sniffed from the title frame.
- **Cookies (v1):** workers may read cookies (secret synced at register/reconnect) but trusted headers remain authoritative for identity and admin; workers never write cookies (the gateway drops `Set-Cookie` from worker responses); no user DB is synced to workers.
- **Shared DB is a later phase:** replication of the user/session data through Firebase or MongoDB is deferred (LLD 11, section 19).
- **Cross-session links are routed by key:** shared-session viewers pass `homedir=` and fork links pass `jid=`; the gateway keeps a `key -> worker` map (workers report keys over the control channel) and routes on it before normal affinity. `jid` is not checked against the caller's uid, because fork links are meant to be opened by others.
- **Assignment at first page load:** the gateway issues a guest id and picks the backend when it serves the IDE page, so the parallel first requests share one workspace. Signed-in users with an existing workspace on the gateway disk are pinned to `local`.
- **Worker details:** a worker serves only the tunnel listener, ignores basic auth, and receives the gateway's WebSocket `AuthToken` at registration.

The code-level design is in [LLD 11](../lld/11-distributed-execution.md).

## Running the binary

The same `gotty` binary runs every role; `--mode` selects it. Full flag and config reference: [LLD 11, Usage](../lld/11-distributed-execution.md#13-usage).

```bash
# standalone (default, unchanged)
gotty -w -p 8080

# gateway
GOTTY_WORKER_TOKEN=... gotty -w --mode=gateway --port 80

# worker (outbound only, no public port)
GOTTY_WORKER_TOKEN=... gotty -w --mode=worker \
    --worker-server wss://gateway.example.com/api/tunnel --worker-id worker-01

# worker over raw SSH (optional; needs the gateway's --tunnel-addr and a pinned host key)
GOTTY_WORKER_TOKEN=... gotty -w --mode=worker \
    --worker-server ssh://gateway.example.com:2222 \
    --worker-hostkey 'SHA256:<fingerprint from gateway log>' --worker-id worker-01
```
