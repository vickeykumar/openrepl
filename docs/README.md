# OpenREPL design docs

These docs describe how OpenREPL is built. The high-level design (HLD) lives in the [project README](../README.md#architecture-high-level-design). The proposed distributed-execution design is in [docs/hld/distributed-execution.md](hld/distributed-execution.md). The low-level design (LLD) documents below explain each subsystem at code level, naming the files, functions and data formats involved.

To build, run and test OpenREPL on your machine, see [Run locally on macOS (Colima)](../README.md#run-locally-on-macos-colima) in the project README.

## LLD index

| # | Document | Covers |
|---|---|---|
| 01 | [Server, startup and routing](lld/01-server-and-routing.md) | `gotty/main.go`, options and config, HTTP mux, middleware, route table, admin model, logging |
| 02 | [Terminal sessions and the WebTTY protocol](lld/02-terminal-sessions.md) | WebSocket handshake, message types, `webtty`, `localcommand`, PTY, timeouts, connection limits |
| 03 | [Sandboxing and resource limits](lld/03-sandboxing-and-resources.md) | Namespaces, cgroup v1 layout, memory weights, fork/`jid`, disk quota, guest cleanup |
| 04 | [IDE: Run/Debug, demos and files](lld/04-ide-run-and-files.md) | `demos.xml`, Compiler scripts, the Run pipeline, file browser API and events, uploads |
| 05 | [Auth, sessions and storage](lld/05-auth-sessions-storage.md) | Firebase sign-in, `/login`, session cookie, UnQLite stores, home-directory resolution, data models |
| 06 | [Frontend](lld/06-frontend.md) | Pages, script load order, `gotty-bundle` modules, tabs, sharing over Firebase, chat widget, Practice, command palette, Phase 2 additions |
| 07 | [AI features](lld/07-ai-features.md) | `/chat/completions` proxy, rate limiting, Genie, Practice question generation |
| 08 | [Build, packaging and deployment](lld/08-build-and-deploy.md) | Makefile and bindata pipeline, Docker, CI, systemd, runtime file layout |
| 09 | [Adding a new REPL](lld/09-adding-a-repl.md) | Step-by-step checklist that touches every layer |
| 10 | [Known limitations and tech debt](lld/10-known-limitations.md) | Quirks found during the code walkthrough, with suggested fixes |
| 11 | [Distributed execution](lld/11-distributed-execution.md) | Proposed: gateway/worker modes, route ownership, session affinity, SSH tunnel, jid routing, capacity, failure handling |

## Source map

Go code uses a GOPATH layout: the repo root is `GOPATH`, packages live under `src/`, and `GO111MODULE=off`. Import paths such as `"server"` and `"utils"` are directories under `src/`.

| Path | Kind | Doc |
|---|---|---|
| `src/gotty/` | `main` package (the binary) | 01 |
| `src/server/` | HTTP/WS server, handlers, DB-backed features, chat proxy | 01, 02, 04, 05, 07 |
| `src/webtty/` | Protocol bridge between the browser and the PTY | 02 |
| `src/backend/localcommand/` | Process + PTY slave implementation | 02 |
| `src/github.com/kr/pty/` | Vendored **and patched** `pty.Start` (adds container attributes) | 02, 03 |
| `src/containers/` | Namespaces, cgroups, `nsenter` | 03 |
| `src/filebrowser/` | Workspace tree, quota, fsnotify | 04 |
| `src/user/`, `src/cookie/`, `src/cachedb/` | Sessions, cookies, cached UnQLite | 05 |
| `src/utils/`, `src/encoder/` | Constants, flags, job scheduler, demo types, crypto helpers | 01, 03, 05 |
| `src/resources/` | HTML, CSS, JS, images, `meta/demos.xml`, chat widget source | 04, 06 |
| `src/js/` | TypeScript terminal client (webpack → `gotty-bundle.js`, `hterm.js`) and the page script `src/js/src/page/` (→ `scribbler.js`) | 06 |
| `src/jsconsole/` | Vendored `@remy/jsconsole` (browser JavaScript REPL) | 06 |
| `src/services/gotty.service`, `scripts/run_app.sh`, `Dockerfile`, `install_prerequisite.sh` | Runtime and packaging | 08 |
| `src/github.com/`, `src/golang.org/`, `src/pkg/` | Vendored third-party Go packages | 08 |

## Glossary

- **REPL**: an interactive interpreter (Read-Eval-Print Loop), for example `cling` or `python`.
- **Master / slave (webtty)**: the *master* is the browser WebSocket; the *slave* is the PTY-backed process.
- **Master / slave (sharing)**: the *master* browser owns the real WebSocket session; a *slave* browser opened `…/#<dbpath>` and mirrors it through Firebase.
- **jid**: an encoded PID of a running REPL (`encoder.EncodePID`). Used by **Fork REPL** and by extra terminal tabs to join that REPL's namespaces.
- **optionchange / optionrun / optiondebug**: DOM events on a terminal element. They mean, respectively: switch language (start a fresh REPL), run the editor content, and run it in debug mode.
- **Demo**: an entry in `demos.xml` describing one REPL (animation, usage, docs, starter code, Compiler script).
- **homedir**: the per-user workspace under `/tmp/home/`. It is the REPL's `$HOME` and the file browser's root.

## Keeping these docs current

- When you change a route, message type, constant or data format, update the matching LLD in the same PR.
- Diagrams are Mermaid, which GitHub renders natively. Check them with `npx @mermaid-js/mermaid-cli -i file.mmd` or the Mermaid live editor.
- Line-level details (such as default values) are quoted from the code at the time of writing. The code is the source of truth.
