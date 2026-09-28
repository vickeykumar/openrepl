# LLD 10: Known limitations and tech debt

These are quirks and debt found during the code walkthrough, ordered by impact within each group. Each item names the code involved and suggests a direction for a fix.

## Backend: correctness and stability

| # | Issue | Where | Suggested fix |
|---|---|---|---|
| B1 | **Shared mutable state per WebSocket route.** Every `/ws_<cmd>` handler calls `server.SetNewCommand(cmd)`, which writes `factory.command` and `options.TitleVariables["command"]` on the *single* shared factory. It then calls `factory.New()` later. Concurrent connects to different REPLs can start the wrong command, and concurrent writes to the Go map can crash the process with `fatal error: concurrent map writes`. | `server/handlers.go` `generateHandleWS`, `server/server.go` `SetNewCommand`, `localcommand/factory.go` | Pass the command into `factory.New(params)` (or keep one factory per route), and build title variables per request. |
| B2 | **The 60-minute session cap is implicit.** `conn.SetWriteDeadline(now + 60 min)` is set once, so every session dies at 60 min even when active. The comment says 15 min. | `server/handlers.go` `processWSConn` | Make it an explicit option. Warn the user before the cut-off, or refresh the deadline on each write. |
| B3 | **The cursor loop can spin forever.** In `FetchBlogDataMap` and `FetchFeedbackDataMap`, `cursor.Next()` is deferred only after a successful decode. One corrupt record leaves the cursor in place, so the loop never ends. | `server/blog_db.go`, `server/db.go` | Advance the cursor unconditionally at the end of each iteration. |
| B4 | **Global gitconfig path is not expanded.** `filepath.Join("~/", ".gitconfig")` is a literal relative path, so `~/.gitconfig` is never read. | `utils/utils.go` `GetGitConfig` | Use `homedir.Expand` (already vendored in `src/pkg/homedir`). |
| B5 | **Fork links break on restart.** The `jid` encoding secret (`encoder.smallsecret`) is regenerated on every start. | `encoder/encode.go` | Acceptable for ephemeral REPLs. Document it, or persist the secret if links should survive deploys. |
| B6 | **The job file is loaded twice.** `utils.init` loads and deletes `jobfile`, and `common_setup` then tries again with a nil callback. This is harmless but confusing. | `utils/jobscheduler.go`, `gotty/main.go` | Remove the second call. |
| B7 | **Unsafe error handling in `/login` and `/logout`.** They `panic` on malformed bodies (recovered per request by `net/http`, but noisy). | `server/db.go` | Return 400 instead. |

## Platform and operations

| # | Issue | Suggested fix |
|---|---|---|
| P1 | **cgroup v1 only** (`cgroups.New(cgroups.V1, …)`). On cgroup v2 hosts, containers are silently skipped and REPLs run without namespaces or limits. The only signal is a log line. | Port to cgroup v2 (`containerd/cgroups/v3` `cgroup2` manager). Until then, fail fast or show a banner when `InitContainers` creates nothing. |
| P2 | **Workspaces live under `/tmp/home`**, including signed-in users' files. They are lost when the container is recreated unless a volume is mounted. | Move them under a configurable data dir (for example `/opt/gotty/home`) and document the volume. |
| P3 | **REPLs share the host filesystem view** (new mount namespace, no chroot). Workspaces are separated only by cwd and `$HOME`. | Consider a read-only base plus a per-user bind mount (pivot_root), or an OCI runtime. |
| P4 | **Only memory is limited.** CPU shares are commented out, and there is no pids or disk-I/O limit. The disk quota is checked only on session start, create and upload. | Enable the `cpu` and `pids` controllers, and consider per-workspace quotas (XFS project quotas or loopback images). |
| P5 | **Minimal automated tests.** Only `webtty/webtty_test.go` exists. `make test` checks only `go fmt`, and CI only builds the image. | Add handler tests (routing, file-browser path checks, rate limiter) and run `go test` in CI. |
| P6 | **Legacy build stack.** Go 1.19 with GOPATH and vendored copies (some patched in place), TypeScript 2.3, webpack 2, xterm.js 2.7, firebase 3.x in the bundle. | Migrate to Go modules (keeping the pty patch as a fork), then upgrade xterm.js (5.x) and the bundler. |

## Frontend

| # | Issue | Suggested fix |
|---|---|---|
| F1 | **Two Firebase SDKs.** The bundle uses firebase 3.x, and the page loads the 9.x compat SDK (with `firebase-auth-compat` included twice). | Standardise on the page's modular SDK and drop the bundle copy. |
| F2 | **Every terminal output chunk is pushed to Firebase**, even when nobody is viewing the share link. This adds latency and RTDB cost. | Mirror only after a viewer joins (presence node), or batch writes. |
| F3 | **`scribbler.js` is a 2,100-line global script**, and many head scripts block rendering. | Split it into modules, `defer` third-party scripts, and lazy-load reCAPTCHA, intro.js and the chat widget. |
| F4 | **The chat rate limit lives in the session cookie**, so it is per browser and resets when cookies are cleared. | Keep counters server-side, keyed by uid or IP. |
| F5 | **Upload inconsistencies.** The alert says 10 MB but the limit is 20 MB. The checksum is computed from a text read, and a mismatch is ignored on the server. | Hash the ArrayBuffer, and either enforce the checksum or drop it. |
| F6 | **One URL for every language** (`/?python`). The `index.html` template variables are unused, several pages have empty meta descriptions, and `practice.html` has the title "About OpenREPL…". | Add per-language routes that render the index template with their own title, description and H1, plus `sitemap.xml` and canonical tags. |
| F7 | **Leftover files.** `resources/NewFile.html` (an Eclipse "Insert title here" stub) is still bundled, and `index_backup.html`/`.css` are unused. | Delete them. |
