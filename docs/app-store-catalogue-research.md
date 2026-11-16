# App Store catalogue: step 1 research report (2026-11-15)

Status: **research only, no code.** It waits for your approval before step 2.
The numbers come from runs in a throwaway container on 2026-11-15 (the raw log is not kept); the sizes the store uses now are measured by `scripts/measure-catalogue.sh` and are in `catalogue_default.json`. This
session added only what the notes did not have: how each REPL finds packages offline (tested with the image's own interpreters),
what each REPL writes into the workspace (measured), licences and publishers of the plugin and browser-library candidates, and the scratch
folder design. Everything ran in the image `openrepl:pixi-app-store` (Ubuntu 22.04, aarch64; Python 3.10.12, Node 12.22.9, Go 1.18.1,
JDK 11.0.33, Ruby 3.0.2, Perl 5.34, Tcl 8.6.12, Rust 1.74.1 with evcxr 0.17, IPython 7.31.1, Python 2.7.18).
linux-64 was only SOLVED (never installed: the Mac is arm64); every "installed" number and every "works with the REPL" test is linux-aarch64.

## 0. How to read the tables

- **dl x86 / arm**: compressed download in MB added over the baseline, solved for both CPU types (notes, measurement A). "no" = does not solve on that CPU type.
- **inst**: installed size on linux-aarch64 in MB. A value without "~" was measured (`du`, notes B and D); "~" is an estimate (download x 4.5, the ratio measured for Python libraries).
- **RAM**: peak resident memory to import the library in one process (notes C), or the REPL weight of `containers/container.go`. Classes for a 1 GB worker: **S** under 32 MB, **M** 32 to 100 MB, **L** over 100 MB (an L app is not offered on a worker that has less than 2 GB).
- **Verdict**: **now** = installable with the wiring that exists today; **wiring** = works, needs new wiring (said in the cell); **no** = not feasible offline, on 1 GB or inside the workspace limit (the reason is given); **4th** = would need a fourth install manager (needs your approval).
- Built into the image today (not store apps): gcc/g++, cling, Go 1.18.1 + gomacro + yaegi, JDK 11 + jshell, Rust 1.74 + evcxr, nasm, Python 3.10 + IPython, Python 2.7, Node 12.22.9 + ts-node + TypeScript 4.9.5, Ruby 3.0.2, Perl 5.34, Tcl 8.6.12, sqlite3, jq 1.6 + jq-repl, gdb, make, curl, wget. **Not** built in (checked): git, vim, nano, ripgrep, tree, zip, unzip, file, htop, tmux.

## A. Apps per REPL

### A1. python and ipython3 (one catalogue: both use the image's Python 3.10)

How the REPL finds them: `PYTHONPATH=<bundle>/.pixi/envs/default/lib/python3.10/site-packages` (set by the helper from the user's list; verified: numpy 2.2.6, pandas 2.3.3, scipy, matplotlib, scikit-learn import with the system `python3`). Packages must be solved with `python=3.10`.
Writes into the workspace (measured): **bytecode 31 MB** for one session that imports numpy, pandas, scipy, matplotlib and scikit-learn (the helper sets `PYTHONPYCACHEPREFIX=<workspace>/.pycache` today: that alone is 60 percent of the 50 MB limit); matplotlib `~/.cache/matplotlib` + `~/.config/matplotlib` 25 KB (more with more fonts); IPython `~/.ipython` 72 KB (history; small, kept). Variables to move: `PYTHONPYCACHEPREFIX`, `MPLCONFIGDIR`, `XDG_CACHE_HOME`; see D.

| App | For | dl x86 / arm | inst | RAM | Verdict |
|---|---|---|---|---|---|
| numpy | arrays | 22.9 / 19.3 | 88 | 26 S | now (on the safe list) |
| pandas | tables | 35.3 / 31.5 | 158 | 64 M | now |
| scipy | science | 38.5 / 34.8 | 164 | 24 S | now |
| matplotlib-base | plots (Agg backend, no Qt) | 39.1 / 35.8 | 164 | 66 M | now; offer THIS one |
| matplotlib (full) | pulls Qt | 236.3 / 235.8 | 1135 | 66 M | no: 1.1 GB for a GUI nobody can show |
| pillow | images | 10.8 / 10.7 | ~48 | 7 S | now |
| requests, httpx | HTTP clients (terminals have no internet: useful for tests and parsing) | 7.7 / 7.1, 0.7 / 0.7 | ~33, ~3 | 27, 29 S | now |
| flask, fastapi + uvicorn | web servers for the preview | 0.6 / 0.6, 7.6 + 0.2 | ~3, ~34 | 32, 39 + 24 M | now |
| sqlalchemy | SQL toolkit | 9.6 / 9.2 | ~43 | 35 M | now |
| beautifulsoup4, lxml, pyyaml, jinja2, click, rich, tqdm | parsing, CLI, text | 0.1 to 3.0 | 0.5 to 14 | 7 to 20 S | now |
| pydantic | validation | 2.3 / 2.2 | ~10 | 16 S | now |
| pytest, pytest-cov | tests | 1.5, 1.8 | ~7, ~8 | 31 M | now; `.pytest_cache` goes to scratch (`-o cache_dir`) |
| black, ruff, mypy | format, lint, types | 0.8, 8.5 / 8.2, 22.4 / 21.5 | ~4, ~37, ~100 | 28, none, 18 | now; `MYPY_CACHE_DIR`, `RUFF_CACHE_DIR` to scratch (mypy cache is 10 to 60 MB) |
| scikit-learn | machine learning | 46.8 / 43.0 | 210 | **122 L** | now, label L (not on a 1 GB worker) |
| sympy, networkx | symbolic maths, graphs | 12.8 / 13.6, 1.2 | ~61, ~5 | 51 M, 33 M | now |
| polars | fast tables | 46.5 / 43.7 | 221 | 42 M | now (optional) |
| seaborn-base | statistical plots | 67.1 / 63.4 | 310 | M | now (optional; brings pandas, scipy) |
| statsmodels, pyarrow, numba | statistics, columnar data, JIT | 63.4 / 59.5, 55.8 / 54.0, 66.3 / 68.3 | ~285, ~250, ~300 | M to L | now (optional); numba needs `NUMBA_CACHE_DIR` in scratch |
| cryptography, paramiko, psutil | crypto, ssh client, processes | 2.1 to 4.2 | 10 to 19 | S | now |
| pytorch-cpu | deep learning | 267.3 / 121.1 | 747 | **226 L** | no on 1 GB (import alone is 226 MB); optional on a 4 GB worker, label L |
| py-opencv | computer vision | 291.5 / 250.4 | ~1100 | L | no: 1.1 GB; `opencv-python-headless` does not exist on conda-forge |
| ipywidgets | notebook widgets | 6.2 / 6.2 | ~28 | S | no: needs a Jupyter frontend, the REPL is a terminal |
| poetry, pipenv | resolve and install from PyPI | 43.7 / 39.0, 7.5 | ~200, ~34 | | no: need the internet to do their job |

### A2. python2.7 (legacy)

Verified: conda-forge `python=2.7` bundles import with the image's system `python2.7` through `PYTHONPATH=<bundle>/lib/python2.7/site-packages`: `six`, `requests 2.25.1` and `numpy 1.16.4` all work. The env is 178 MB because each bundle brings its own Python 2.7 (29 MB download).
Writes: `.pyc` next to the sources (read-only store, so nothing) and `~/.python_history`; set `PYTHONDONTWRITEBYTECODE=1`.

| App | dl x86 / arm | inst | RAM | Verdict |
|---|---|---|---|---|
| six | 0.0 | ~0 | S | wiring (new `python2.7` runtime row) |
| requests 2.25 | 1.9 / 1.7 | ~8 | S | wiring |
| numpy 1.16 | 13.3 / 11.1 | ~55 | S | wiring |
Label every one "legacy (Python 2.7)". Nothing else was verified, so nothing else is listed.

### A3. node and ts-node (Node 12.22.9)

How found: `NODE_PATH=<bundle>/node_modules` (exists today). The installer picks the newest version whose `engines` fit Node 12; packages that declare no engines break anyway, so each row has a **pin** (notes, section 5, rounds 1 and 2). Writes: none at `require`; `~/.node_repl_history` KB; installs are done by the server, so no npm cache in the workspace.
**ts-node needs new wiring (verified):** with `NODE_PATH` only, `import _ from "lodash"` fails with TSError (TypeScript does not read `NODE_PATH`, and the types are not found). It works with
`TS_NODE_COMPILER_OPTIONS={"esModuleInterop":true,"baseUrl":"<bundle>/node_modules","paths":{"*":["*","@types/*"]},"typeRoots":["<bundle>/node_modules/@types"]}` (built per user from the user's list), or with `TS_NODE_TRANSPILE_ONLY=true` (no type check).

| App (pin) | For | inst MB | Verdict |
|---|---|---|---|
| axios 1.21.1, node-fetch@2, got@11, ws, socket.io 4.8 | HTTP, sockets (no internet: test servers only) | 4.4, 0.6, 4.4, 0.2, 6.1 | now |
| lodash 4.18, ramda, rxjs 7.8, immutable, dayjs, moment 2.31, date-fns@2 | utilities, dates | 4.9, 3.4, 11.6, 0.7, 2.1, 5.4, 35 | now |
| express@4 (4.22.3) | web server for the preview | 4.6 | now (on the safe list) |
| uuid@8, chalk@4, commander@9, dotenv, yaml@1, papaparse, marked@4, minimist, bcryptjs, jsonwebtoken | small helpers | 0.1 to 0.8 | now |
| cheerio@1.0.0-rc.12, jsdom@16 | HTML parsing | 5.1, 18.6 | now |
| mathjs@11 | maths | 20 | now |
| mocha@9, jest@27, sinon@9, chai@4 | tests | 8.9, 53.5, 7.6, 1.2 | now (jest is the big one) |
| eslint@8, prettier@2, nodemon@2, esbuild@0.14 | tools | 15.3, 10.8, 1.9, 7.7 | now |
| sharp@0.30, better-sqlite3@7 | native modules (prebuilt) | 28, 12.6 | now (verified: both `require` on Node 12 aarch64; the prebuilt binary is fetched at install time by the server) |
| typescript@4.9 | | 64 | built in already; do not duplicate |
| zod (both 4 and 3), winston, inquirer, lodash-es, @types/node (as a library) | | | no: need newer syntax or ESM than Node 12 (zod 3.25 fails on `??`); `@types/*` are types, they only help ts-node (see wiring) |
Newest versions of express 5, chalk 6, nanoid 6, got 16, commander 15 and many others are **no** on Node 12 (they need Node 18 or 22). The catalogue pins the version.

### A4. c and cpp (gcc for Run, cling for the REPL)

Verified (aarch64, image's gcc 11 and cling 1.2) with a conda environment of eigen, fmt, nlohmann_json:
- **Headers**: `CPLUS_INCLUDE_PATH=<prefix>/include[:<prefix>/include/eigen3]` and `C_INCLUDE_PATH` work for **both** g++ and cling (cling printed `{"a":1}` from `nlohmann::json`).
- **Libraries at link time**: `LIBRARY_PATH=<prefix>/lib` works for g++ (no `-L`).
- **Libraries at run time**: a program linked this way fails with `libfmt.so.12: cannot open shared object file`. `LD_RUN_PATH=<prefix>/lib` at link time fixes it (verified: prints `{"a":1} -2`) without a global `LD_LIBRARY_PATH` (which would break the system's own programs). Header-only libraries need none of this.
- **cling with a shared library**: not verified; needs `#pragma cling load("fmt")` or `LD_LIBRARY_PATH` for that process only (new wiring, test in step 7).

Writes (measured): `gcc test.c` leaves `a.out` 20 KB; cling `.cling_history` 8 KB, nothing else in HOME; a g++ build of a medium program leaves objects and binaries of 1 to 20 MB. ccache (`CCACHE_DIR`) and cmake/ninja build trees are the risk: **cmake, ninja and clang-tools are labelled "build in scratch"** (`cmake -B $OPENREPL_SCRATCH/build`).

| App | For | dl x86 / arm | inst | RAM | Verdict |
|---|---|---|---|---|---|
| eigen, nlohmann_json, glm, cli11 | header-only libraries | 1.3, 0.1, 8.3, 8.1 | ~6, ~1, ~35, ~35 | S | now (headers: `CPLUS_INCLUDE_PATH`) |
| fmt, spdlog, catch2, gtest, doctest, benchmark | libraries (static or shared) | 8.2, 8.3, 8.6, 8.4, 8.0, 8.2 | 20 to 40 | S | wiring (`LIBRARY_PATH` + `LD_RUN_PATH`) |
| libboost-devel | Boost | 40.4 / 40.0 | ~180 | S | wiring (mostly headers) |
| openssl, zlib, libpng, sqlite (dev) | system libraries | 4.9, 0.2, 2.0, 4.1 | 5 to 20 | S | wiring |
| libcurl | HTTP client library | 30.0 / 30.4 | ~130 | S | no: terminals have no internet, so it cannot be used |
| cmake, ninja, pkg-config | build tools | 57.0 / 56.8, 8.1, 1.8 | 229, ~35, ~7 | M | now (PATH); label "build in scratch" |
| cppcheck, clang-format | analysis, format | 50.9 / 49.2, 91.1 / 77.7 | ~220, ~330 | M | now |
| clang-tools (clang-tidy) | linter | 117.6 / 105.0 | ~500 | M | optional |
| clang (full compiler) | second compiler | 185.1 / 156.3 | 954 | L | no as default: 950 MB for a compiler the REPL does not use |
| valgrind | memory errors | 64.6 / 59.2 | 240 | M | optional; needs ptrace-like access, test per host |
| ccache | compiler cache | 9.7 / 8.9 | ~40 | S | wiring (`CCACHE_DIR` in scratch) |
The whole libs set measured as one environment is 372 MB (fmt, eigen, nlohmann_json, catch2, gtest, boost, spdlog, cli11, doctest, glm, curl, openssl, zlib).

### A5. go: gomacro and yaegi (separately)

Installed Go is 1.18.1. `GOPATH=/opt/gotty/` and `GOCACHE=/tmp/go_cache/.cache/go-build/` are already set by the server (`backend/localcommand/local_command.go`), so the Go build cache is already outside the workspace, but it is one shared folder for all users, uncapped.
Writes (measured): a 10-line program that uses `net/http`, `encoding/json`: `go build` writes the **binary 3.7 MB** into the workspace (user output; each Go build is 2 to 15 MB, so a few builds fill 50 MB) and 1 MB of build cache (now in `/tmp/go_cache`); with `GOCACHE` moved, only the binary remains. **A Go app that adds MB to the workspace in normal use is the binary itself: the catalogue labels Go "binaries are 2 to 15 MB each".**
- **gomacro** (the Go REPL) and **yaegi**: both import third-party packages from source (`GOPATH/src` or the module cache). **No Go libraries exist on conda-forge** (checked: only tools). **Go libraries (gin, cobra, testify, uuid...) are therefore `4th`**: they need a Go module mirror in `/store` (`GOPROXY=file:///store/gomod`, `GOFLAGS=-mod=mod`, `GONOSUMDB`), which is a fourth manager. Not proposed unless you approve it (reason: Go is a quarter of the REPLs and a REPL without libraries is a calculator).
- yaegi writes 12 KB (nothing). gomacro history 4 KB.

| App | For | dl x86 / arm | inst | Verdict |
|---|---|---|---|---|
| gopls | language server | 10.3 / 9.3 | ~45 | now (tool for bash; no editor client in the IDE yet) |
| golangci-lint, staticcheck | linters | 14.1 / 12.8, 4.4 / 4.0 | ~60, ~18 | now |
| delve | debugger | 6.1 / 5.1 | ~25 | optional; needs ptrace (not on Pi workers) |
| go-shfmt, go-yq | shell formatter, YAML tool | 2.1 / 2.0, 7.7 / 6.7 | 8, 30 | now |
| go (conda Go 1.2x) | newer compiler | 67.7 / 53.9 | ~300 | no: PATH order keeps the image's Go first |
| Go libraries | | | | **4th** |

### A6. java (jshell, JDK 11)

jshell takes `--class-path` or `JDK_JAVA_OPTIONS=-cp ...`. **No Java libraries are on conda-forge** (searched: `commons-*`, `guava*`, `jackson*`, `slf4j*` do not exist; `junit5` exists as a test runner only). Java libraries (jars) would come from Maven Central: **`4th`** (a mirror folder + classpath wiring).
Writes (measured): `javac` leaves `.class` files (4 KB for HelloWorld); jshell writes nothing in HOME with the tuned flags (`-J-Djava.util.logging.config.file=/dev/null`); the JVM temp dir and `~/.java` prefs go to scratch (`JAVA_TOOL_OPTIONS=-Djava.io.tmpdir=... -Djava.util.prefs.userRoot=...`).
maven (177 / 163 dl, 484 MB), gradle (296 / 282, 617 MB): **no**, they exist to download jars. kotlin, scala, sbt, leiningen are other JVM languages, not REPLs of ours. Nothing is installable for java without a fourth manager.

### A7. evcxr (Rust)

Crates need the registry: **offline crates are `4th`** (`cargo vendor` folder + `[source]` replacement, `CARGO_NET_OFFLINE=true`). Tools: rust-analyzer (12.6 / 11.7 dl, ~50 MB) and the `cargo-*` tools (cargo-audit, cargo-binutils...) exist on conda-forge; they do not need the registry (verdict: now, optional).
Writes (measured): HOME only 12 KB (`.config/evcxr`); one evcxr session that evaluates two lines writes **4 MB under `$TMPDIR`** (`/tmp/.tmpXXXX`; every further cell adds an object) and `rustc test.rs` leaves a **4.7 MB binary** in the workspace. **When the workspace is under `/tmp` the helper sets `TMPDIR=<workspace>/.tmp`, and the evcxr build then lands inside the 50 MB workspace.** Variables: `TMPDIR`, `CARGO_TARGET_DIR`, `EVCXR_TARGET_DIR`, `CARGO_HOME` to scratch; `RUSTUP_HOME` (set today) stays (found: with `HOME` = a workspace and no `RUSTUP_HOME`, rustup finds no toolchain).
Memory: REPL weight 50 MB; a cell compile peaks higher (not measured): label **M**.

### A8. bash (tools on the PATH)

Tools are on the PATH after the system's (the system's program wins, so a store `jq` or `bash` never replaces the image's). Verified: tcllib, spatialite, ruby/perl rows below use the same mechanism.

| App | For | dl x86 / arm | inst | Verdict |
|---|---|---|---|---|
| git | version control | 53.3 / 59.6 | ~290 | now (`.git` of a user's repo lives in the workspace: label) |
| ripgrep, fd-find, tree, bat, fzf, hyperfine, file, zip, unzip | search, listing, benchmark | 1.4 to 4.5 | 4 to 20 | now |
| go-yq, jq (store jq 1.8) | YAML/JSON | 7.7, 2.2 | | now (jq 1.8 never wins over the system's 1.6: pointless, do not list) |
| shellcheck, go-shfmt | shell lint, format | 2.8 / 8.3, 2.1 / 2.0 | ~25, 8 | now (shfmt is `go-shfmt`, `shfmt` does not exist) |
| htop, tmux, vim, neovim | terminal tools | 3.4, 6.7, 80.5 / 79.0, 47.1 / 45.6 | 15 to 330 | now; tmux needs `TMUX_TMPDIR`, vim/neovim `XDG_STATE_HOME` in scratch (undo and swap files) |
| duckdb | SQL on files | 81.0 / 74.6 | ~330 | now (optional) |
| litecli, sqlite-utils | SQLite CLIs | 48.8, 41.8 | ~200 | now (optional) |
| ccache, strace | | 9.7, 2.2 | | ccache see A4; strace no on hosts without ptrace |
| gh | GitHub CLI | 13.1 / 11.8 | ~60 | no: terminals have no internet |
| rustc/zig/nim/deno/php/elixir | other languages | 40 to 387 | 200 to 1500 | no: not REPLs of ours |

### A9. jq-repl, sqlite3, perli, irb, tclsh, rappel

| REPL | Packages | Verified result | Verdict |
|---|---|---|---|
| jq-repl (jq 1.6) | jq modules (`-L`) | none exist on conda-forge | **no**: nothing to install; the store jq 1.8 would never run (PATH order) |
| sqlite3 | extensions, tools | `libspatialite` (32.4 / 32.6 dl, 145 MB env): `select load_extension('<prefix>/lib/mod_spatialite.so'); select spatialite_version();` printed 5.1.0 | **wiring**: the extension path is printed on the app page (no variable needed); history `~/.sqlite_history` KB |
| perli (Perl 5.34) | conda `perl-*` | `PERL5LIB=<prefix>/lib/perl5/site_perl:<prefix>/lib/perl5/vendor_perl`: **Text::CSV, Mojolicious, Try::Tiny, Path::Tiny, JSON::MaybeXS load** with the system perl. **XS modules fail** (`YAML::XS`: "loadable library and perl binaries are mismatched" because conda's perl is 5.32). Each bundle drags conda's perl: 93 MB env. | **wiring** for pure-Perl modules only (perl-text-csv 0.2, perl-mojolicious 0.5 dl); many names do not exist (perl-lwp*, perl-libwww*, perl-http-tiny, perl-datetime, perl-xml-libxml, perl-moo on arm64, perl-getopt-long, perl-time-hires: not found or no on one CPU); XS: **no** |
| irb (Ruby 3.0.2) | conda `rb-*` gems | every gem pins its own Ruby (rack: 2.7.2, bundler: 2.5.5) and several together **do not solve** (conflict); the gem folder did not load with the system Ruby (`cannot load such file -- rack`); rb-nokogiri, rb-json no on arm64 | **no**: stdlib only; a conda Ruby would replace the interpreter |
| tclsh (8.6.12) | tcllib | `TCLLIBPATH=<prefix>/lib`: `package require json; json::json2dict` and `md5` work (tcllib 6.9 / 6.6 dl, 30 MB env) | **wiring** (`TCLLIBPATH`); tk (9.5 / 5.4): no, there is no display; tclx, tcl-tls: not on conda-forge |
| rappel (x86 assembly) | nasm and binutils are built in | nothing to add | **no** apps |

### A10. javascript (browser console), html, css

These three have no interpreter on the node: their "apps" are browser libraries and plugins: see C and B.

### A11. What needs your decision: the 4th manager

Go libraries, Java jars and Rust crates have no conda-forge or npm packages, so with pixi, npm and apt they have **no libraries at all** (only tools). A fourth manager would be a mirror folder per ecosystem populated by the server at install time (`go mod download`, Maven Central, `cargo vendor`) and used offline by the REPL (`GOPROXY=file://`, `-cp`, `[source]` replacement). It is the largest single gap in the catalogue (3 of 21 REPLs) and the largest single risk (each ecosystem's supply chain). I am **not** proposing it; I need your yes or no.

## B. Browser plugins

Constraints from the repo: the editor is Ace (`window.editor.env.editor`), today's plugins run in a sandboxed frame (`34-store-plugins.js`) with `text()` and `status()` only; the preview frame has no site origin and is served at `/preview/<token>/...` (LLD 17), so a preview tool is injected by the server into the served HTML; the terminal is hterm (gotty). The interface the notes propose (`setText`, `insertText`, `terminalText`, `panel`, `save`) is not built.
Publisher, licence and version are read from the npm registry (2026-11-15). "Fits" is the adapter type of step 6.

### B1. Ours (written in this repository, reviewed with the code)

| Place | Plugin | What it does | Needs from the page |
|---|---|---|---|
| preview browser | Viewport switcher | toolbar button: phone, tablet, desktop widths | the preview frame size only |
| preview browser | Outline | panel: headings and landmarks of the page | read the previewed DOM (injected script) |
| preview browser | Console | panel: `console.*` and errors of the page | injected script, postMessage out |
| editor | Format on request | command: re-indent and trim | `text()` and `setText()` |
| editor | Trim and final newline | on save | `text()`, `setText()` |
| editor | Word count (exists) | status bar | `text()`, `status()` |
| terminal | Copy last output, clear, font size | toolbar buttons | hterm selection and options |
| IDE panel | Regex tester, Base64 / URL / JWT decoder | panel in a sandboxed frame | nothing |
| IDE panel | Clock (exists) | status bar | `status()` |

### B2. Official third-party (admin adds; npm package; files copied into `/store`; served from our origin)

| Place | Plugin (npm) | Publisher, licence | Version | Size (package / file we serve) | Fits (adapter) | Needs from the page |
|---|---|---|---|---|---|---|
| preview browser | **eruda** | liriliri, MIT | 3.4.3 | 1.9 MB / `eruda.js` 488 KB | preview-page tool (injected, `eruda.init()`) | the previewed page's `document`; no workspace, no token |
| preview browser | **vconsole** | Tencent, MIT | 3.15.1 | 382 KB / `dist/vconsole.min.js` 279 KB | preview-page tool | same |
| preview browser | **axe-core** (accessibility audit) | Deque, **MPL-2.0** | 4.14.0 | 3.4 MB / `axe.min.js` 636 KB | preview-page tool (runs `axe.run`, posts the result to a panel) | the previewed page's `document`. MPL-2.0 allows hosting an unmodified file: keep the licence file next to it |
| editor | **ace-builds extensions**: `ext-language_tools` (autocomplete), `ext-emmet`, `ext-beautify`, `keybinding-vim`, `keybinding-emacs` | Ajax.org (the Ace project), BSD-3-Clause | 1.45.2 | 57 MB package / 4 to 56 KB each | **editor extension, runs in the page**: second confirmation; these are Ace's own extension files | the Ace instance; `ext-emmet` also needs `emmet` (MIT, 909 KB, only `dist/emmet.es.js`, an ES module) |
| editor | **prettier@2** standalone (formatter for js, ts, css, html, json, md, yaml) | Prettier, MIT | 2.8.8 | 11 MB / standalone 436 KB + parsers 154 to 317 KB each | editor command in a **sandboxed frame** (pure text in, text out): no page access | `text()`, `setText()` |
| editor | **js-beautify**, **sql-formatter** | Beautifier, MIT; sql-formatter-org, MIT | 2.0.3; 15.9.0 | 959 KB / `beautify*.js` 53 to 146 KB; 3 MB / `sql-formatter.min.js` 312 KB | same sandboxed formatter adapter | same |
| IDE panel | **marked** + **dompurify** + **highlight.js** (markdown preview) | markedjs, MIT; cure53, MPL-2.0 OR Apache-2.0; highlightjs, BSD-3-Clause | 18.2.0 (`lib/marked.umd.js`), 3.4.18 (`purify.min.js` 28 KB), 11.12.0 | 555 KB, 1.5 MB, 5.4 MB | panel in a sandboxed frame | the open file's text; no network |
| IDE panel | **mermaid** | Knut Sveidqvist and contributors, MIT | 12.3.0 | **120 MB package** / `dist/mermaid.min.js` 5.4 MB | panel in a sandboxed frame | the diagram text. Copy only the dist file, not the package (111 dependencies) |
| IDE panel | **diff2html** | Rodrigo Fernandes, MIT | 3.4.56 | 2 MB / js 75 KB + css 16 KB | panel in a sandboxed frame | two texts |
| IDE panel | **json-formatter-js** | Mohsen Azimi, MIT | 2.5.23 | 179 KB / `json-formatter.umd.js` 13 KB | panel in a sandboxed frame | a JSON text |
| terminal | none | | | | no third-party package fits hterm | |

Excluded and why: **qrcode** (no prebuilt browser file in the package: needs a bundler), **jsonpath-plus** (the browser file path in the package is not as documented; needs checking before listing), **stats.js** (a 1 KB file with no licence field worth hosting; not needed). **A plugin that needs a licence we cannot host** (gsap: "Standard no-charge" licence, not open source) is never added.
Notes on the licences: MIT and BSD need the notice kept; MPL-2.0 (axe-core, dompurify) allows hosting unmodified files with the licence text; the store page shows publisher, version and licence for each (step 6).

## C. Browser libraries for html, css and javascript

Installed by npm into `/store`, served from a fixed local path (`/preview/lib/<name>/<file>`), no CDN. The size is the whole package (npm installs all of it, counted in the store limit); the file is what the page loads. 24 libraries come to **65 MB** in total.

| Library (npm) | Version | Licence | Package / file the page loads | Note |
|---|---|---|---|---|
| bootstrap | 5.3.9 | MIT | 9.4 MB / `dist/css/bootstrap.min.css` 226 KB, `dist/js/bootstrap.bundle.min.js` 78 KB | |
| @picocss/pico | 2.1.1 | MIT | 19 MB / `css/pico.min.css` 81 KB | |
| water.css, normalize.css, animate.css | 2.1.1, 8.0.1, 4.1.1 | MIT | 116 KB / `out/water.min.css` 22 KB; 16 KB / `normalize.css`; 272 KB / `animate.min.css` 70 KB | |
| @tailwindcss/browser | 4.3.5 | MIT | 284 KB / `dist/index.global.js` 280 KB | Tailwind v4 in the browser: no build step |
| alpinejs, htmx.org | 3.17.5, 2.0.11 | MIT, 0BSD | 675 KB / `dist/cdn.min.js` 54 KB; 879 KB / `dist/htmx.min.js` 50 KB | |
| jquery | 4.0.0 | MIT | 2.8 MB / `dist/jquery.min.js` 76 KB | |
| react + react-dom | **18.3.1** | MIT | 310 KB + 4.4 MB / `umd/*.production.min.js` 10 + 128 KB | React 19 has no UMD build: pin 18 |
| preact | 10.29.8 | MIT | 1.5 MB / `dist/preact.min.js` 11 KB | |
| vue | 3.5.45 | MIT | 2.6 MB / `dist/vue.global.prod.js` 168 KB | |
| lodash, dayjs, axios | 4.18.1, 1.11.23, 1.21.1 | MIT | 1.4 MB / `lodash.min.js` 71 KB; 665 KB / `dayjs.min.js` 6 KB; 2.2 MB / `dist/axios.min.js` 75 KB | |
| d3 | 7.9.2 | ISC | 860 KB / `dist/d3.min.js` 274 KB | |
| chart.js | 4.6.1 | MIT | 6.1 MB / `dist/chart.umd.js` 205 KB | |
| katex | 0.19.1 | MIT | 4 MB / `dist/katex.min.js` 266 KB + `katex.min.css` 24 KB + fonts folder | the css needs the `fonts/` folder next to it |
| leaflet | 1.9.4 | BSD-2-Clause | 3.7 MB / `dist/leaflet.js` 144 KB + `leaflet.css` 14 KB | tiles need the internet: no map until the user supplies local data |
| matter-js, sortablejs, swiper | 0.20.0, 1.15.7, 14.4.0 | MIT | 905 KB / `build/matter.min.js` 81 KB; 625 KB / `Sortable.min.js` 44 KB; 3.7 MB / `swiper-bundle.min.js` 151 KB + css 14 KB | |
| marked | 18.2.0 | MIT | 555 KB / `lib/marked.umd.js` | |
| mathjs | 15.2.0 | Apache-2.0 | 9.2 MB / `lib/browser/math.js` 634 KB | |
Optional (large or a licence note): **three** 0.187 (MIT, 20.5 MB; module only: `<script type="module">` with an import map, no UMD), **p5** 2.4 (LGPL-2.1, 16 MB, `lib/p5.min.js` 1 MB: serve unmodified, label), **mermaid** (120 MB, see B2), **@fortawesome/fontawesome-free** 7.3.1 (CC-BY-4.0 and OFL, 24.7 MB: needs its `webfonts/` folder and the attribution line), **tone** 15.1 (MIT, 5.3 MB). **Excluded:** gsap (not open source).

The store shows, for each one, the one line to paste, for example `<link rel="stylesheet" href="/preview/lib/bootstrap/dist/css/bootstrap.min.css">`.

## D. The scratch folder

Goal: nothing a program caches or builds fills the 50 MB workspace (read the limit from `utils.WorkspaceQuotaMB`, never hard-code it).

**Where:** `$OPENREPL_SCRATCH_DIR/<home>/<terminal id>` on each node (default `/scratch`, outside `/opt` for the same reason as `/store`: the sandbox lets every REPL read the other folders of `/opt`), made when the terminal starts, exported to it as `$OPENREPL_SCRATCH`.
**Sandbox:** the terminal gets read and write on its own folder only (a new `ExtraRW` option next to `ExtraRO` in `landlock.Options`); the parent folder and the other terminals' folders stay denied.
**Cap:** default 256 MB per terminal and 2 GB per node, both admin settings. There is no way to make a hard quota without privileges (Render's free tier has no `CAP_SYS_ADMIN`; a `tmpfs` would also take RAM from 1 to 4 GB workers). So: (1) the existing per-file limit (`--max-file-mb`) stays; (2) the server checks the folder size every 2 seconds, at 100 percent removes the cache folders first (they are disposable: Go build cache, bytecode, mypy cache), then stops the terminal's process group with a message; (3) a new terminal is refused when the node's scratch is above its cap. Worst case overshoot is one 2 second burst. If you want a hard cap on privileged hosts, a size-limited tmpfs can be added later as an option.
**Ends:** removed when the terminal ends; orphans removed at server start and hourly (the same sweeper that cleans `/tmp`, `utils/tmpclean.go`).
**Never synced or snapshotted:** workspace sync and the workspace snapshots read only `HOME_DIR/<home>`; the scratch folder is not under it. It is not copied to another node when a session moves.

Variables per runtime (all point below `$OPENREPL_SCRATCH`; a user's own `export` still wins):

| Runtime | Variables |
|---|---|
| all | `TMPDIR=$S/tmp`, `XDG_CACHE_HOME=$S/cache`, `XDG_STATE_HOME=$S/state`, `CCACHE_DIR=$S/ccache`, `TMUX_TMPDIR=$S/tmux` |
| python, ipython3 | `PYTHONPYCACHEPREFIX=$S/pyc` (today `<workspace>/.pycache`, 31 MB measured), `MPLCONFIGDIR=$S/mpl`, `NUMBA_CACHE_DIR=$S/numba`, `MYPY_CACHE_DIR=$S/mypy`, `RUFF_CACHE_DIR=$S/ruff`, `BLACK_CACHE_DIR=$S/black`, `PYTEST_ADDOPTS=-o cache_dir=$S/pytest`, `JOBLIB_TEMP_FOLDER=$S/joblib`; IPython history stays in `~/.ipython` (72 KB) |
| python2.7 | `PYTHONDONTWRITEBYTECODE=1` |
| node, ts-node | `npm_config_cache=$S/npm`; TypeScript caches none |
| go (gomacro, yaegi) | `GOCACHE=$S/go-build` (today the shared `/tmp/go_cache`), `GOTMPDIR=$S/tmp`, `GOMODCACHE=$S/gomod` |
| evcxr (rust) | `CARGO_TARGET_DIR=$S/target`, `EVCXR_TARGET_DIR=$S/evcxr`, `CARGO_HOME=$S/cargo`; `RUSTUP_HOME` stays as set |
| java | `JAVA_TOOL_OPTIONS=-Djava.io.tmpdir=$S/tmp -Djava.util.prefs.userRoot=$S/prefs` |
| c, cpp (g++, cling) | `CCACHE_DIR`; build trees by hand: `cmake -B $S/build` |
| ruby, perl, tcl, sqlite3, bash | `TMPDIR` only; the history files (KB) stay in the workspace |
Include variables for installed apps (`CPLUS_INCLUDE_PATH`, `LIBRARY_PATH`, `LD_RUN_PATH`, `PERL5LIB`, `TCLLIBPATH`, `TS_NODE_COMPILER_OPTIONS`, `PYTHONPATH`, `NODE_PATH`) are separate: they point at the read-only store, not scratch.

## Disk: the proposed default set against the 15 GB limit

Physical size, hard links counted once (the cache file and every bundle's link are one file). The measured second Python bundle costs +28 MB because files that contain the install path cannot be hard-linked (benchmark of 2026-11-15): that is the per-bundle overhead used below (about 30 MB per Python bundle, about 5 MB per tool bundle).

| Group (default = core + popular) | MB |
|---|---|
| Python libraries, 25 apps measured together as one environment (python base 107 MB once) | 735 |
| Tools for bash and c/cpp (22 tools measured together: git, ripgrep, fd, tree, go-yq, file, zip, unzip, cmake, ninja, pkg-config, clang-format, cppcheck, shellcheck, go-shfmt, fzf, bat, htop, tmux, hyperfine, ccache, strace) | 970 |
| C++ libraries (13) | 380 |
| Go and Rust developer tools (gopls, golangci-lint, delve, staticcheck, rust-analyzer) | 138 |
| Node libraries (32 pinned) | 268 |
| Browser libraries (24) | 65 |
| Plugin packages (11, without mermaid) | 30 |
| Per-bundle overhead (about 27 Python bundles x 30 + 40 tool and library bundles x 5) | ~1000 |
| **Default set total** | **about 3.6 GB** |
| Optional, not in the default: polars 221, seaborn 310, statsmodels 285, pyarrow 250, numba 300, torch 747, duckdb 330, clang 954, clang-tools 500, valgrind 240, litecli + sqlite-utils 200, mermaid 120, three 20, p5 16, fontawesome 25, python2.7 set 150 | about 4.7 GB |
| **Everything feasible installed** | **about 8.3 GB** |

Against the proposed limit of 15 GB with a 3 GB reserve, **12 GB can be used by apps**: the default set takes 30 percent and everything feasible 69 percent. **The limit applies to each node**: a worker installs every app the catalogue has installed, so a Raspberry Pi worker with a 16 GB card should not run the full set; the "Add app" form shows the disk left on every node before an install. The shared packages (Python, libraries of the same family) are counted once, which is why the combined environments in section 3 of the notes are smaller than the sum of the rows.

## What I will ask for in the next steps (not decided here)

1. The 4th manager (A11): yes or no, and for which of Go, Java, Rust.
2. The default set above, or smaller (for example drop clang-format, cmake and scikit-learn from "popular").
3. The scratch design (D): a 2 second watcher and a soft cap, or you want to require a privileged mount.
4. Plugins: the first set is the preview browser (viewport, outline, console; eruda and vconsole as third-party); the editor and panel plugins follow.

## What was not verified

linux-64 installs (only solved); cling with a shared library; evcxr peak memory per cell; valgrind and delve on a host without ptrace; ts-node with `paths` for every library at once; macOS or other hosts; the real size of the per-bundle overhead for a full 60-bundle store (extrapolated from one measurement); the `jsonpath-plus` and `qrcode` browser files.
