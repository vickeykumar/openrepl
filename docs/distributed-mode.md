# Distributed mode: operator guide

The short version is in the [project README](../README.md#distributed-mode-gateway-and-workers). This page has every option, the key and certificate details, deployment and troubleshooting.

By default OpenREPL runs everything on one machine (`--mode=standalone`). In distributed mode one public server, the **gateway**, keeps serving the website and can hand the REPL sessions to other machines, the **workers**. The design is in [docs/hld/distributed-execution.md](hld/distributed-execution.md) and [docs/lld/11-distributed-execution.md](lld/11-distributed-execution.md).

- The **gateway** is the only public address. It serves the pages, sign-in, blog, snippets, practice, the AI proxy and `/admin`. For each visitor it picks one execution node and sends that visitor's terminals, file browser and uploads there every time.
- A **worker** needs no public port. It connects *out* to the gateway, so it can sit behind NAT or a firewall. It runs the REPLs and holds the users' files. If you want, it can also open a port of its own for people on the same network (see [the worker's own port](#the-workers-own-port)).
- The browser does not change: same URLs, same pages. It never talks to a worker directly.
- The same `gotty` binary does all three jobs. `--mode` selects one.

```
                 browsers
                    |
                    v
        +-----------------------+        outbound tunnel (WebSocket or SSH)
        |  gateway  (--mode=    | <------------------------------+-----------------+
        |  gateway, public)     |                                |                 |
        +-----------------------+                        +-------+------+   +------+-------+
          pages, sign-in, /admin                         |   worker-1   |   |   worker-2   |
          + its own REPL sessions                        | (--mode=     |   | (--mode=     |
            (unless --local-weight 0)                    |   worker)    |   |   worker)    |
                                                         +--------------+   +--------------+
                                                           REPLs + files      REPLs + files
```

## What you need

| Item | Where | What it is |
|---|---|---|
| Worker token | gateway and every worker | One shared secret. A worker that does not know it cannot connect. |
| Tunnel host key | gateway only | An SSH key that identifies the gateway to workers. Created automatically. |
| Host key fingerprint | workers | Lets a worker check it reached the real gateway. Required for `ssh://`, optional for `wss://`. |
| TLS certificate | gateway (or the proxy in front of it) | Needed for `https://` and therefore for `wss://`. |
| The same runtime on every worker | workers | The same image as a normal OpenREPL server: the REPLs, the compile scripts, `nsenter` and cgroup access. |

## Step 1: create the worker token

Any long random string works. Generate one and keep it out of shell history and `ps` output by passing it in the environment or a config file, not on the command line.

```bash
openssl rand -hex 32
```

Put it in a file that only the service user can read, on the gateway and on every worker:

```bash
umask 077
echo "GOTTY_WORKER_TOKEN=$(openssl rand -hex 32)" > /etc/openrepl/tunnel.env   # run once, then copy the file to the workers
```

The gateway and all workers must have the same value. To change it, replace it everywhere and restart the gateway and the workers; there is no live rotation.

## Step 2: the tunnel host key (gateway)

You normally do nothing. The first time a gateway starts with a worker token it creates an ed25519 key at `~/.gotty.tunnel_key` (mode 0600; change the path with `--tunnel-hostkey`) and logs its fingerprint:

```
Gateway mode: workers connect to /api/tunnel (tunnel host key SHA256:hgOdwhaazhJt+e/X1IaRZM0ddHoAtFRbo38oqjIWbWY)
```

The log is `/gottyTraces/gotty.log`. `~` is the home directory of the user the service runs as.

To create the key yourself, for example to know the fingerprint before the first start, use `ssh-keygen`. The key must have no passphrase:

```bash
ssh-keygen -t ed25519 -N "" -C "openrepl tunnel" -f ~/.gotty.tunnel_key
chmod 600 ~/.gotty.tunnel_key
```

Print the fingerprint of an existing key (generated either way) at any time:

```bash
ssh-keygen -lf ~/.gotty.tunnel_key
```

The second field of the output, starting with `SHA256:`, is the value workers pin with `--worker-hostkey`. Keep the key file: if it is deleted the gateway creates a new key with a new fingerprint, and workers that pinned the old one refuse to connect until they are given the new fingerprint.

## Step 3: TLS for the public port

Workers should reach the gateway over `wss://`, which needs HTTPS on the gateway. Two ways:

**a) TLS in the gateway itself**, with a certificate from your CA (for example Let's Encrypt):

```bash
gotty -w --mode=gateway --port 443 --tls --tls-crt /etc/openrepl/fullchain.pem --tls-key /etc/openrepl/privkey.pem
```

**b) TLS in a reverse proxy** in front of the gateway. The proxy must pass WebSocket upgrades, which the site already needs for terminals, and must not time out an idle tunnel in under a minute (the tunnel sends a ping every 20 seconds). For nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 3600s;
    client_max_body_size 50m;
}
```

Do not turn on client-certificate authentication (`--tls-ca-crt`) on the port workers use; a worker does not present a client certificate.

**A private CA or a self-signed certificate** works too, as long as each worker trusts it. Create one for testing (the name after `DNS:` must be the host name workers use in `--worker-server`):

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout gw.key -out gw.crt \
  -subj "/CN=gateway.example.com" -addext "subjectAltName=DNS:gateway.example.com"
```

Then either add `gw.crt` to the worker machine's trust store, or point the worker process at it:

```bash
SSL_CERT_FILE=/etc/openrepl/gw.crt gotty -w --mode=worker --worker-server wss://gateway.example.com/api/tunnel
```

A worker that does not trust the certificate logs `x509: certificate signed by unknown authority` and keeps retrying.

**No TLS at all?** Use the `ssh://` transport (below). It is encrypted by SSH and the worker checks the pinned host key before it sends the token. `ws://` (no TLS) sends the token in clear text; use it only on a test machine.

## Step 4: start the gateway

```bash
set -a; . /etc/openrepl/tunnel.env; set +a      # exports GOTTY_WORKER_TOKEN
gotty -w --mode=gateway --port 80 --max-connection 2564
```

The equivalent config file (`~/.gotty`, or any file given with `--config`):

```hcl
mode           = "gateway"
port           = "80"
permit_write   = true
max_connection = 2564
local_weight   = 10                       // 0 = the gateway runs no sessions itself
worker_token   = "<the token from step 1>" // or leave it out and set GOTTY_WORKER_TOKEN
// enable_tls   = true
// tls_crt_file = "/etc/openrepl/fullchain.pem"
// tls_key_file = "/etc/openrepl/privkey.pem"
```

| Gateway option | Default | Meaning |
|---|---|---|
| `--mode=gateway` | `standalone` | Turns on routing. |
| `--worker-token` / `$GOTTY_WORKER_TOKEN` | empty | The shared secret. Without it the gateway accepts no workers and runs every session itself. |
| `--local-weight` | `10` | The gateway's own share of new sessions next to the workers. `0` makes it routing-only. |
| `--max-connection` | `0` (no limit) | The gateway's own memory budget in MB for REPLs, as in standalone mode. |
| `--tunnel-path` | `/api/tunnel` | The WebSocket endpoint workers connect to, on the public port. |
| `--tunnel-addr` | empty (off) | Also accept workers over raw SSH, e.g. `0.0.0.0:2222`. |
| `--tunnel-hostkey` | `~/.gotty.tunnel_key` | The host key file from step 2. |

Nothing else has to be opened in the firewall: workers use the public HTTP(S) port. Only if you set `--tunnel-addr` do you open that port, and only to the workers.

## Step 5: start a worker

```bash
set -a; . /etc/openrepl/tunnel.env; set +a
gotty -w --mode=worker \
  --worker-server wss://gateway.example.com/api/tunnel \
  --worker-id worker-01
```

Config file:

```hcl
mode             = "worker"
permit_write     = true
worker_server    = "wss://gateway.example.com/api/tunnel"
worker_id        = "worker-01"
worker_weight    = 10
worker_token     = "<the token from step 1>"  // or GOTTY_WORKER_TOKEN
// worker_hostkey   = "SHA256:<fingerprint from step 2>"
// worker_capacity  = 4096
// worker_languages = "python,bash,cling"
```

| Worker option | Default | Meaning |
|---|---|---|
| `--mode=worker` | | No port is opened, unless you give `--port` or `--address` (see below). |
| `--worker-server` | required | `wss://host/api/tunnel` (recommended), `ssh://host:port`, or `ws://host/api/tunnel` for tests. |
| `--worker-token` / `$GOTTY_WORKER_TOKEN` | required | The shared secret. |
| `--worker-hostkey` | empty | The gateway's `SHA256:` fingerprint. Required for `ssh://`. With `wss://` it is an extra check on top of the TLS certificate. |
| `--worker-id` | the host name | Must be unique: letters, digits, `.`, `_`, `-`, at most 64 characters, and not `local`. A second worker with the same id replaces the first. |
| `--worker-weight` | `10` | Relative share of new sessions. A worker with 30 gets three times as many as one with 10. |
| `--worker-capacity` | `0` = the machine's RAM in MB | Memory budget for REPLs, in the same MB units as `--max-connection`. When it is used up the worker gets no new sessions and refuses further terminals. |
| `--worker-languages` | empty = all | Comma-separated REPL commands this worker has, e.g. `python,bash,cling,gointerpreter`. Set it only if the worker lacks some REPLs. |

A worker reconnects by itself, with a delay that grows from 1 to 30 seconds, when the gateway restarts or the network drops.

### The worker's own port

A worker opens no port by default. Give it `--port` (or `--address`) and it also serves that address, like a standalone server, so people on the same network can use the worker directly at `http://<worker-address>:<port>/`:

```bash
GOTTY_WORKER_TOKEN=<token> gotty -w --mode=worker --worker-server wss://gateway.example.com/api/tunnel --port 9090
```

`--port` and `--address` count as given when they come from the command line, from `GOTTY_PORT` / `GOTTY_ADDRESS`, or from the config file. The worker keeps working through the gateway as before.

- **A second door, not the gateway's.** Visitors of that port have their own cookies, sign-in, files and sessions, as on a standalone server. Their sessions are not visible to the gateway, cannot be reached through it, and are not part of workspace sync. A guest's files there expire on the worker's own one-hour timer.
- **It shares the worker's capacity.** Those sessions count against `--worker-capacity`, so the gateway sees the real load.
- **Nothing is trusted from outside.** The identity headers the gateway adds are believed only on the tunnel, never on this port.
- **No gateway in front of it.** The gateway's `--credential` does not apply here. To ask visitors for a password, start the worker with `--credential user:pass`; the worker asks only visitors of its own port, never the gateway. Use `--address 127.0.0.1` to keep the port to the machine itself, and do not expose it to the internet.
- **The port must be free.** If it is taken, the worker stops with `failed to listen` before it connects to the gateway.

**Raw SSH instead of WebSocket.** For a network where the worker cannot use HTTPS to the gateway but can reach a TCP port:

```bash
# gateway
gotty -w --mode=gateway --port 80 --tunnel-addr 0.0.0.0:2222
# worker; the fingerprint is required
gotty -w --mode=worker --worker-server ssh://gateway.example.com:2222 \
  --worker-hostkey 'SHA256:<fingerprint from step 2>' --worker-id worker-01
```

## Running it as a service or in Docker

**systemd.** Copy `src/services/gotty.service` and change `ExecStart` to the gateway or worker command. Load the token from the file made in step 1:

```ini
[Service]
EnvironmentFile=/etc/openrepl/tunnel.env
ExecStart=/usr/local/bin/gotty -w --mode=worker --worker-server wss://gateway.example.com/api/tunnel --worker-id worker-01
```

**Docker.** The image's entrypoint passes its arguments to `gotty`, so add the mode flags after the image name (use an image built from a version that has distributed mode):

```bash
# gateway
docker run -itd --name openrepl-gateway \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw --privileged \
  -v openrepl-gateway-keys:/keys \
  --env-file /etc/openrepl/tunnel.env \
  -p 80:80 <image> -p 80 --mode=gateway --tunnel-hostkey /keys/tunnel_key

# worker: no -p, it only connects out
docker run -itd --name openrepl-worker-01 --hostname worker-01 \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw --privileged \
  --env-file /etc/openrepl/tunnel.env \
  <image> --mode=worker --worker-server wss://gateway.example.com/api/tunnel
```

The `/keys` volume keeps the gateway's tunnel host key across container replacements, so its fingerprint stays the same. User files live in `/tmp/home` on whichever node runs the session; mount a volume there on workers (and on the gateway if it runs sessions) if they must survive the container.

## How sessions are placed

- **Once per visitor.** When someone opens the site the gateway picks a node and remembers it: a guest by a signed cookie (`or-aff`), a signed-in user by their account. Every later terminal, file and upload request of that visitor goes to the same node.
- **Randomized and weighted.** Among the nodes that are online, have a weight above 0 and have free capacity, one is drawn at random in proportion to its weight.
- **Signed-in users keep their node.** It is saved in the gateway's user database, so they return to the same files after a gateway restart. A user who already has files on the gateway stays on the gateway.
- **Nothing is moved.** If a worker goes away, its sessions get an error until it is back; they are not restarted elsewhere, because the files and running programs are on that worker.
- **Guests' files** are deleted after an hour without use, on the node that holds them, as on a single server.

## Keeping the users' files safe: workspace sync

By default a user's files exist only on the worker that runs their session. If that worker is lost, the files are lost, and while it is away the user cannot even browse or download them. Workspace sync keeps a second copy of every home on the gateway, in step with the worker, in both directions. The design is in [LLD 12](lld/12-workspace-sync.md).

Turn it on at the gateway only; workers follow it:

```bash
gotty -w --mode=gateway --port 80 --workspace-sync --sync-state-dir /var/lib/openrepl/wsync
```

`--relocate-after` (default `2m`, any duration of at least `1s` such as `30s`) is how long a worker may be away before its sessions are placed again. A shorter time gets users back sooner, but a worker that is only restarting then loses its sessions, and edits it had not yet sent are dropped when it returns.

| What changes | Effect |
|---|---|
| Programs and the file API on a worker | Their file changes (create, edit, delete, rename, directories, links) reach the gateway about a second later, and changes made on the gateway reach the worker. |
| A worker goes away | The file browser, downloads, saves and uploads keep working from the gateway's copy. Terminals still report "execution node unavailable", because the running programs are gone. |
| A worker (re)connects | It shows as `SYNCING` in `/admin/workers` and takes no new sessions until its homes are reconciled with the gateway's copy, then `ONLINE`. Changes made on both sides while it was away are merged; where both changed the same file, the later modification wins. |
| A worker is lost for good | For the first 2 minutes (`--relocate-after`) the terminal cannot start, because the worker may only be restarting. The page shows "Your execution node is away. Reconnect in 1:20." with a countdown from the moment the worker drops (or the first time a terminal is started in that period), and Reconnect, Run and Debug stay disabled until it reaches zero. After that its sessions are placed again, on another worker (which is sent the files first) or on the gateway itself, which already has them. The old worker's copy is dropped if it ever returns, so changes it had not yet sent are lost. |
| A worker is drained | Its signed-in users are placed on other workers at their next session; sessions it already has carry on. |
| A guest is idle for an hour | The gateway deletes the home on both machines. Workers no longer delete homes on their own timers. A guest with a terminal open never expires. |

Things to do:

- **Give the gateway disk for every user's files**, as a single server would need, and keep `/tmp/home` on durable storage if signed-in users' files must survive a gateway restart. Put `--sync-state-dir` on durable storage too (on a worker as well); losing it deletes nothing, but files deleted while the two sides were apart come back.
- **Keep the clocks of all machines in step** (NTP). The gateway measures each worker's clock offset at connect and corrects for it, and logs a warning above 2 seconds, but a badly wrong clock can still make the wrong edit win.
- **Do not enable it half-way**: turn it on at the gateway, restart the gateway, then restart the workers so they reconnect and receive the setting.

Limits: files over 50 MB are not synchronized; sockets, pipes and device files are skipped; names that start with `.wsync-` are reserved; ownership and set-user-id bits are not copied; Linux only.

## Operating a fleet

These routes run on the gateway and need the same admin sign-in as `/admin` (open them in the browser you are signed in with, or pass that browser's session cookie to `curl`):

| Route | What it does |
|---|---|
| `GET /admin/workers` | Every node with its state, weight, used and maximum MB, sessions, last heartbeat, address and languages, and how many sessions the weighted random choice has given it (see below). |
| `POST /admin/workers/<id>/drain` | The worker gets no new sessions. Its current ones carry on. |
| `POST /admin/workers/<id>/undrain` | Back to normal. |
| `GET /admin/sessions` | Which session is on which node. |

```bash
curl -b "user-session=<your admin session cookie>" https://gateway.example.com/admin/workers
curl -b "user-session=<your admin session cookie>" -X POST https://gateway.example.com/admin/workers/worker-01/drain
```

**Seeing the weighted random choice at work.** Each row of `/admin/workers` has three numbers:

| Field | Meaning |
|---|---|
| `picked` | How many sessions the weighted random choice has given this node since the gateway started. |
| `pickedPercent` | Its share of all such sessions. |
| `weightPercent` | The share it should get by weight, among the nodes that take new sessions now (`ONLINE` and weight above 0; 0 for any other). |

Over many sessions `pickedPercent` approaches `weightPercent`. For example, a worker with weight 30 and a gateway with weight 10 should each get about 75% and 25%:

```json
{"workers": [
  {"id": "local",    "state": "ONLINE", "weight": 10, "picked": 27, "pickedPercent": 27.0, "weightPercent": 25.0},
  {"id": "worker-1", "state": "ONLINE", "weight": 30, "picked": 73, "pickedPercent": 73.0, "weightPercent": 75.0}],
 "pickedTotal": 100, "pickedSince": "2026-10-02T09:00:00Z"}
```

A few things to know when reading it:
- It counts new sessions, not requests. A visitor stays on the node they were given, so one browser adds 1, however often it reloads. To see the spread, use new visitors (another browser, a private window, cleared cookies, or a guest whose home has expired).
- Only the weighted choice is counted. A signed-in user who goes back to the worker that holds their files was not picked.
- The counts start again from zero when the gateway restarts (`pickedSince` says when it started). A worker that has left is no longer listed, but its sessions stay in `pickedTotal`, so the percentages of the rest add up to less than 100.
- With few sessions the numbers are lumpy: 10 sessions say little, a few hundred say a lot.

**Taking a worker out for maintenance:** drain it, watch `terminals` for it in `/admin/workers` fall to 0, then stop it. A drain is forgotten when the gateway restarts. Signed-in users whose files are on that worker cannot start a new session until it is back.

**Adding a worker:** start it with the token; it appears in `/admin/workers` and starts receiving new sessions. Existing sessions stay where they are.

## Troubleshooting

| What you see | Cause and fix |
|---|---|
| Gateway log: `no --worker-token set, workers are disabled` | Set `GOTTY_WORKER_TOKEN` (or `worker_token`) on the gateway. |
| Worker exits: `worker mode needs --worker-token` or `needs --worker-server` | A required option is missing. |
| Gateway log: `the sync channel ... (the worker stays out of rotation)` | The worker is an older version without workspace sync, or its sync handler is off. Update the worker, or run the gateway without `--workspace-sync`. |
| A worker stays `SYNCING` | It is reconciling a large home, or cannot reach the gateway's sync channel. If the conversation with the gateway ends, the gateway disconnects the worker and it reconnects at once (the log says `asking worker ... to reconnect`); otherwise after 5 minutes the gateway drops it and it tries again. The logs of both sides name the home that is not finishing. |
| Browser: `workspace is synchronizing, please try again` | The session's home on the worker is not yet in step with the gateway (it just reconnected, or the home is large). It resolves by itself; see `/admin/workers`. |
| Worker exits: `failed to listen at ...: address already in use` | The port given with `--port` is taken. Choose another, or drop `--port` so the worker opens no port. |
| Worker log: `connect: connection refused` | Nothing listens at that address and port. Check the port the gateway really uses (its log prints `HTTP server is listening at`). Between containers, `localhost` is the worker itself: use the gateway container's address. |
| Worker log: `tls: first record does not look like a TLS handshake` | `wss://` was used against a gateway that serves plain HTTP. Use `ws://` for a test setup, or enable HTTPS on the gateway. |
| Worker log: `bad handshake (HTTP 401)` | The token differs from the gateway's. |
| Worker log: `(HTTP 429)` | Ten failed attempts from that address within a minute. Fix the token; it clears after a minute. |
| Worker log: `(HTTP 403)` | Something added an `Origin` header to the tunnel request. The endpoint refuses browser-style requests. |
| Worker log: `(HTTP 404)` | The gateway is not in gateway mode, has no token, or `--tunnel-path` differs. |
| Worker log: `x509: certificate signed by unknown authority` | The worker does not trust the gateway's certificate. See step 3. |
| Worker log: `gateway host key SHA256:... does not match the pinned key` | `--worker-hostkey` is wrong, or the gateway's key file was recreated. Update the fingerprint. |
| Worker exits: `invalid worker id` | See the id rules in step 5. |
| Worker log: `registration refused: ...` | The gateway did not accept the worker; the text after the colon says why (for example a protocol version it does not support). |
| Browser: `execution node unavailable` (503) | The worker that holds this session is offline. It works again when the worker reconnects. |
| Browser: `workspace node unavailable` (503) | A signed-in user's worker is offline or draining. |
| Browser: `no execution node available` (503) | No online node has a weight: no worker is connected and the gateway runs with `--local-weight 0`. |
| Browser: `this language is not available on your execution node` | The session's worker was started with `--worker-languages` and lacks that REPL. |
| Terminal: `exceeding max number of connections` | That node's memory budget is used up. |

All messages go to `/gottyTraces/gotty.log` on the machine concerned.

## Good to know

- The cookie-signing secret and the WebSocket auth token are sent to each worker when it connects, over the tunnel, and kept in memory only. User accounts, sessions and all other databases stay on the gateway. Treat workers as trusted machines all the same: they run users' code and see their session cookies.
- `--credential` (basic auth) on the gateway does not apply to the tunnel endpoint; workers authenticate with the token.
- Turning distributed mode off again is just `--mode=standalone` (or removing the option). Files that were created on workers stay on the workers.
