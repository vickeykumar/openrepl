# 18. App Store

Status: built 2026-11-14 on `development` (design approved the same day: canvas "App Store: all screens and actions", 11 boards). Not deployed.
Packages `appstore` (`src/appstore`), `server/appstore.go`, the sandbox in `landlock` and `landlockhelper`, the page `js/src/page/33-app-store.js`, the admin
view `viewStore` in `resources/js/admin.js`, tests `appstore/appstore_test.go`, `server/appstore_test.go`, `landlock/policy_test.go`, `e2e/tests/app-store.spec.js`.

## 1. What it is

Libraries and tools (conda-forge packages) are installed **once** on a server into a shared store, never into a workspace. Each signed-in user has a list of the
ones that are on for them; their terminal can read and run exactly those and nothing else. Guests have no store.

```
/store/                      (OPENREPL_STORE_DIR, "off" switches the store off; outside /opt on purpose: the sandbox lets every REPL read the other folders of /opt)
  pkgs/                      the download cache, one copy of every package file
  apps/<name>-<ver>/         the project of one app (pixi.toml, pixi.lock, activate.json) and its conda prefix in .pixi/envs/default (the package and everything
                             it needs), hard-linked from the cache, made once; bundles made before the move to pixi have the prefix at their root;
                             .openrepl-bundle.json is written last: a bundle without it (an install that was cut) is never used
  envs/<home>/manifest.json  the bundles that are on for the user whose workspace folder is <home>  ["flask-3.0.2", ...]
  envs/<home>/env.sh         what a shell runs (. env.sh) to use them without a new terminal
```

A bundle is a whole conda prefix because conda programs find their libraries by the RPATH they were built with, relative to themselves: a package without its
dependencies beside it does not work. Python bundles are solved with the server's Python (`python=3.10` here, read from `python3`), so several bundles can be put on one
`PYTHONPATH` and the system's `python3` stays the one that runs.

## 2. Who can do what

| | Guest | Signed-in user | Admin |
|---|---|---|---|
| See the store, switch an installed app on or off for themselves | no | yes | yes |
| Install an app on the safe list | no | yes | yes |
| Ask for an app that is not on it (a request; a second asker joins it) | no | yes | |
| Report an install that failed, try it again (once a minute) | no | yes | yes |
| Approve or reject a request, install anything, resolve a report, remove an app, apt, settings | no | no | yes |

Every `/store/api` call needs a signed-in account (`requestIdentity`) and a workspace that snapshots are kept of (`homesnap.IsKept`: the account's own folder, which is also the
name of its manifest folder). POSTs must be the page's own JSON (`sameOrigin`). The admin routes are `admin/store` (GET the picture, POST one decision; audited).

## 3. The sandbox (Landlock)

`containers.wrapWithLandlock` adds `--store-dir <dir>` to the helper when the folder exists. The helper (`landlockhelper.storeFolders`), before it restricts itself and with the
server's rights, reads `<dir>/envs/<basename of the workspace>/manifest.json`, checks every name (`^[a-z0-9][a-z0-9._-]*-[0-9]...$`, no `/`, no `..`: **a bad entry stops the
terminal with status 126, it is never skipped**), leaves out a bundle this server does not have yet, then
- passes `landlock.Options.ExtraRO`: the folders of the bundles on the list and the user's own `envs/<home>` folder (read and execute, never written, and never a folder that
  holds the workspaces), and
- puts the bundles on the program's environment: their `bin` folders **after** the system's `PATH`, their `site-packages` in front of `PYTHONPATH` (and `lib/node_modules` in
  `NODE_PATH`). There is no `LD_LIBRARY_PATH`: it would put conda's libraries in front of the system's and break the system's own programs.

The terminal cannot write `/store` at all, so it cannot add itself to a list or change a bundle (a hard-linked file shares its inode with the cache). Python's bytecode cannot be
written into a bundle either: the helper sets `PYTHONPYCACHEPREFIX` to `<workspace>/.pycache`.

Checked by hand 2026-11-14 with the real helper on Landlock (arm64 image): a user with `flask` on can `import flask`, run its tool, and sees `numpy` as "No module named"; listing
`/store/apps`, another bundle or another user's folder gives "Permission denied"; writing a bundle or one's own manifest is refused; `["../../etc"]` in a manifest stops the start.

## 4. Installing (`appstore.Service`)

State lives in the site's database (`persist`, file `appstore.db` or Firestore/MongoDB like the other databases): `app:<name>` (name, pinned version, state installing / installed /
failed, size, log tail), `req:<name>`, `rep:<name>`, `user:<home>` (the list), `apt`. The version is pinned by the store: the newest conda-forge build that solves with the server's Python
(`Installer.Resolve`: `pixi lock` solves it, nothing is installed, and the lock says the version, the packages and their sizes), and every node installs that same version, from the same lock.

One install at a time (a mutex), as a goroutine with four steps the page shows (`installSteps`, `View.Steps`): *checked that it is allowed, resolve (the solve says how many packages and how many MB), download and link* (`pixi install` in the bundle folder, whose `pixi.toml` pins `python = "3.10.*"` (a library) and `<name> = "==<ver>"`), write the marker,
run a check (`checkBundle`: the bundle's own Python starts and imports the package's module, when it has one), record it, and mark the request and the open report answered (`Notices` tells their askers). An admin can install a version of their choice (`InstallVersion`, for an app whose newest version does not solve). A failure removes the half-made bundle, keeps the last lines of the log (the store's folder shortened) and the step it stopped at. The server runs the
installer outside any terminal's sandbox, so terminals need no internet. The store is refused when it is over its limit (`storeLimitMB`, default 2048). A server that stopped in the middle of
an install marks it failed at its next start (`Recover`). No pixi on the server (`OPENREPL_PIXI`, or on the PATH) gives the installer `Missing`: every install says so, and the admin page
shows it. `OPENREPL_STORE_INSTALLER=fake` makes tiny bundles (a Python module of the package's name) for tests and development.

## 5. Replay on every node

`Service.Apply(snapshot)` makes a node's store match the catalog: missing bundles are installed (the same pinned versions), every user's `manifest.json` and `env.sh` are written, and, when the
snapshot is complete (`Full`: both listings of the database worked, so an outage is never taken for an empty catalog), bundles that are no longer in it are removed.
- Gateway or standalone: at start, every 10 minutes, and after each admin decision.
- **A worker (no database) gets the catalog over its tunnel** (`server/storetunnel.go`, `tunnel` package; no HTTP, no polling). The worker's heartbeat (every 10 s, which already exists) carries the revision of the catalog it has (`Heartbeat.StoreRev`, 0 at first). The gateway answers with the whole catalog (`HeartbeatReply.Store`: `Rev` = a hash of the content, `Catalog` = the `Snapshot`, `Reinstall`) only when the revision is not its own, or an admin gave the node a command; otherwise the reply carries nothing. A catalog is a few KB, so it is sent whole, not as a delta: a lost message heals with the next heartbeat. The gateway makes the catalog at most every 15 s (`catalogTTL`), so a change reaches a worker within about 25 s. The worker applies it in one goroutine (the newest catalog wins; a command to reinstall is never lost to a newer one), installs what is missing by itself, and answers over the tunnel with `store-state@openrepl` (`NodeState`: what it has, what failed, when it tries again, its size) after every bundle, and again when it reconnects. The gateway keeps the last state of each worker for the Servers matrix.
- **Backoff**: a bundle that fails on a node is tried again by the node itself after 1, 2, 5, 15, then 60 minutes (`appstore.Backoff`, per bundle, in memory); until then `Apply` skips it and says when it will try (`NodeState.Retry`; the admin sees "failed, next try in N min"). A command to reinstall, a catalog without the app, a success or a restart of the node ends the waiting. A worker has a timer for the earliest retry, so a failure that nobody mentions again is still tried.
- **A worker installs for itself.** The gateway sends only the catalog (`Snapshot`: the installed apps and each user's list, no files). A worker installs every missing bundle itself from conda-forge / npm over its own internet, so it gets programs built for its own CPU (an arm64 worker of an amd64 gateway) and the gateway's egress bandwidth, which costs money, is not used. Earlier versions let a worker download a .tar.gz of the bundle from the gateway (`/store/bundle`, `/store/progress`); that was removed 2026-11-15 on the owner's word.
- A bundle already in the store is not made again, so a deploy that keeps `/store` costs nothing. After a wipe every node, the gateway included, installs what the catalog lists again.
- apt: the admin's list (database) is part of the snapshot; each node installs what is missing with `apt-get install -y --no-install-recommends` (`SystemApt`; it needs root and apt-get, and says so when it
  has neither) at every replay, and leaves what `dpkg -s` finds. A package that fails is said in that node's state and on the admin page (a card for `apt:<name>`) and does not stop the others. These are for all users:
  there is no switch. A server with the fake installer never runs apt-get. A node that loses its system between starts (a container) installs them again at its next start; for big packages the image is the better place.

## 6. The page, the dialog, the notices

**The dedicated page** (`/store`, `resources/store.html`, `js/store.js`, `css/store.css`; canvas boards 2 to 5): a left menu (Browse, My apps with the count of what is on, My requests, Kinds: Python libraries, Tools, Languages (admin), Editor plugins (later);
System (admin): apt packages), a search field, the chips (All, Installed, Safe to install, Needs approval) and a 3-column grid of cards (name, summary from conda-forge, version, state tag, switch or button). Sections are the address hash.
- **An app's page** (`#app/<name>`): its three situations (not installed on the safe list, installed with the switch, not on the safe list with a request), the table of Size / Needs (the server's Python) / Who can, the "how to use" line
  (`import flask`, or `<tool> --help`), the line to run in an open terminal, "I have this problem too" on a failure someone else reported, and, for an admin, "install another version" (the versions come from conda-forge).
- **Install dialog** (board 4A): a progress bar and the four steps (Checked that it is allowed / Resolved the dependencies (14 packages, 41 MB) / Downloading and linking / Checking that it works), "Hide, keep going". A failure turns it into the
  failure dialog (4B: the step, the log tail, Report to admin / Try again / Close); a success into "X is installed. Turn it on?".
- **Report dialog** (4C): a text box and what is sent (the app and version, the step, the log, the server it ran on) and what is not. **Request dialog** (3C): the reason.
- **The answers** (4D, `Service.Notices`, `GET /store/api/notices`, `POST ack`): an approved or rejected request, a fixed or rejected report: shown once when the page opens (and as a notice in the editor), then acknowledged. An approved request is
  forgotten when every asker has seen it; a rejected one stays so the card can say "Not available: reason".
- Guests see the sign-in card. The page cannot type into a terminal: My apps shows the line (`. '<env.sh>'`) with a Copy button.

**A terminal without the sandbox** (an admin's, which never has one, or any terminal on a server without Landlock) still goes through `landlock-helper --no-landlock --workspace <home> --store-dir <dir> -- <shell>` (`containers.wrapStoreEnv`), only so that the apps that are on for the user are on its PATH, PYTHONPATH and NODE_PATH; before this an admin's terminal got no App Store environment at all (found 2026-11-15: axios "Cannot find module" for an admin who had it on).

**An open terminal cannot use an app switched on later** when the sandbox is on: Landlock is fixed when the terminal starts, so the new bundle's folder is "Permission denied" there and `. env.sh` cannot help (tried 2026-11-15 with the real helper). So the list answer carries `sandbox` (Landlock active) and, for a non-admin, the pages say "open a new terminal" instead of offering the line to run; the line stays where there is no sandbox and for admins (who have none).

## The installer: pixi, and the lock

Every app of kind lib, tool or language is a small pixi project in its own bundle folder (`appstore/pixi.go`; Node libraries stay on npm, plugins on the server). Why pixi, measured 2026-11-15 on numpy: it gives one lockfile for every CPU type (the gateway is amd64, the workers arm64), was faster (10 s against 14 to 43 s), left less on the disk by default (232 MB against 299 MB, about equal once micromamba's archives are cleaned) and has the activation variables as JSON; it uses about 50 MB more memory in an install (150 against 105 MB) and its binary is 74 MB (micromamba's 22 MB).

- **pixi.toml** (`Pixi.Manifest`): `[workspace]` with `channels = ["conda-forge"]`, `platforms = ["linux-64", "linux-aarch64"]`, the server's Python for a library, the app pinned. A tool or a language brings the Python it needs and does not drag one in.
- **Solve and size**: `pixi lock` solves for every platform without installing (about a second with a warm index). `LockPackages` reads `pixi.lock` (version 7: `environments.default.packages.<platform>` lists the packages of a CPU type, the top level `packages` have their sizes): the version, the number of packages and the download size for the node's CPU type, and the disk size as 2.5 times that. A CPU type conda-forge has no build for fails the solve, and the error names the CPU (found by running pixi: `failed to solve requirements of environment 'default' for platform 'linux-aarch64'`): an app is the same version everywhere or it is not installed; there is no silent fallback on a worker.
- **The lock is part of the catalog.** The gateway reads `pixi.toml` and `pixi.lock` after a successful install and keeps them in its database (`lock:<bundle>`, `Service.captureLock`; `App.Locked` says it has one). A node installs a locked bundle by writing both files first and running `pixi install --locked`; a worker gets them from the gateway over the tunnel (`store-lock@openrepl`, `Client.StoreLock`; the catalog carries only `locked: true`, so it stays a few KB), a gateway whose disk was wiped from its own database. A node that cannot get the lock installs nothing (`the lock of <id> could not be had`), retried with the backoff. Real test: a second node installs flask from the gateway's lock and has exactly the same 29 packages.
- **A new lock** (admin, `relock`; "New lock" on the Installed list): the saved lock is dropped, the bundle is made again on the gateway (same version, solved now) and every known worker is told to make it again; the users keep their switch and their files are written again when the install is done. For when conda-forge no longer has a build the old lock names.
- **Activation variables**: after an install the node runs `pixi shell-hook --json` in a plain environment and keeps the variables that packages added (`ActivationFrom`: not PATH, PYTHONPATH, NODE_PATH, XDG_DATA_DIRS, LD_*, HOME and the like, not `CONDA_*`, `PIXI_*`), in `activate.json` of the bundle. The helper puts them in the terminal's environment (`Vars.Extra`, first bundle on the list wins a name) and they are in `env.sh`. Real test: proj sets `PROJ_DATA`, and `projinfo` finds its data through it. The hook runs the packages' activation scripts once, on the server, at install time.
- **Not built (2026-11-15):** a different version for one CPU type chosen by the admin, and "this app is amd64 only"; today an app that cannot be solved for a CPU type of the lock fails. Pixi is pinned in the image (`PIXI_VERSION`, with the checksum of the release).

**Verification** (`appstore/verify.go`, `check.go`): every app has a step that proves it works. (1) A node runs it after it installed a bundle, before it says it has it: the gateway in `runInstall`, a worker in `Apply` (a failure there is a failed install, tried again with the backoff). A Python library: its bundle's Python imports the module; a Node library: `require`; a tool or language: its program prints its version (`--version`, `-version`, `-v`, `-V`, `version`; `R` for r-base); a plugin: its two files. (2) When a user switches an app **on**, `RunVerify` runs the same step *for that user*: in the user's Landlock sandbox (`containers.WrapForVerification`, with `landlock-helper --store-extra <bundle>` so the bundle is allowed before the user's list has it), with the user's environment, starting in the user's workspace, on the node where the user's terminals run: `server.verifyOnNode` asks the worker that holds the workspace over the tunnel (`POST /store/verify` on the worker, which needs the tunnel and the worker token) or runs it on the gateway. The switch is on only when it passes. When it fails the switch is turned off again, the answer is 422 `{error: "verify", message, log, node}`, the page shows the words of the check, and "Report to admin" sends them (the failure is kept an hour: `useFailures`; `Service.ReportUse`; it shows in Failures with the step "Turning it on"). A node that does not have the bundle yet answers `wait`: "try again in a minute", not a fault. The check has 45 s.

**On for everyone**: an admin can turn an installed app on for every signed-in user, and the people who sign in later (checkbox "Also turn it on for all signed-in users" on the admin install, or "Turn on for everyone" on an installed app; admin action `default`, `Service.SetDefault`). The names are in the catalog (`defaults`); `/store/defaults.json` holds their bundle names and each user's `off.json` the ones the user turned off. A terminal's list is `Store.Effective`: the defaults less the user's off list, then the user's own (`landlock-helper` reads that, so a user who never switched anything gets them). Guests never (`Kept`: the same rule as `homesnap.IsKept`). A user's switch on such an app writes the off list, a user cannot uninstall it (an admin can), and a worker gets `Snapshot.Defaults` and `Snapshot.Off`.

**Guests** see the Apps button and the "App Store" entry of the command menu like everyone; opening either (or `/store`) shows "Sign in to use the App Store" with a Sign in button, and the server still refuses every `/store/api` call without an account.

**The quick dialog** in the editor (`33-app-store.js`, the Apps button and "App Store" in the command menu) stays, with a link "Go to app store page", the name of each app opens that app's page in the store (a new tab), each app shows its kind (python, tool, node, language, plugin), and the dialog follows the editor theme like the other IDE overlays (`.appstore` is in `THEMED_SCOPE`); it also offers "Use it in my active terminal" (asks, then types the line).
`AppStore.installAndOn(name)` installs a safe app and, when it is done, switches it on. **The terminal's own error** ("No module named flask", `lib-store-hint.mjs`) shows a notice with "Install and turn on" (safe apps),
"Turn it on" (installed), or "Open the App Store"; one notice for each name in a visit; a name the store does not know is never offered.

**Genie**: the agent action `store_suggest` (`{"type":"store_suggest","name":"flask"}`) is a permission scope of its own ("store"): the card asks "Genie wants to install an app from the App Store"; on yes it calls `installAndOn`
(an app that needs an admin is only shown in the store). It is asked every time (not in the everyday scopes). The agent prompt (`server/agent.go`) says when to use it.

## 7. Admin

`viewStore` ("App Store" in the dashboard; canvas boards 7 and 8): tabs.
- **Requests**: App, Asked by, Why, Status; Approve and install, Reject (a reason the askers see).
- **Failures**: App and version, Reported by, Where it stopped, What they wanted, the log, the **server** it ran on, Try again, **Pick another version**, Mark installed, Reject.
- **Installed**: size, how many users have it on, **"unused for N days" when nobody has had it on for 60 days** (nothing is removed by itself), ready / **behind** / failed / missing on the gateway and each worker (fetching: it is behind and answered
  within 3 minutes), Remove (asks, says how many users lose it), install for everyone.
- **Servers**: the disk of the store on each server against the limit (a bar), and what a server could not do (an app, an apt package).
- **Settings**: the safe list, the size limit, the channel (conda-forge, not editable), the switch, and the apt list.
- The **Health** page has an "App Store" card (`appStoreHealth`): the installer (a warning when pixi is missing), apps installed, size against the limit, failed installs, workers that could not apply the catalog.

## 8. More kinds, search, size, progress, uninstall (2026-11-14, the second round)

- **Kinds** (`App.Kind`): `lib` (a Python library, conda-forge, solved with the server's Python), `tool` (a program, conda-forge, **no Python pinned**: jq went from 112 MB to 7 MB), `node` (a Node library, npm), `lang` (a language: lua, ruby,
  julia, r-base as samples; conda-forge, installed by an admin), `plugin` (an editor plugin that comes with the server; installed by an admin). The safe list names the kind of an entry: `flask` (library), `tool:jq`, `npm:express`.
  A language and a plugin are never on a user's safe list: a user requests them. One name has one kind (installing it again as another kind is refused). `Installer` takes the kind; `Multi` routes: conda-forge to `Mamba`, `node` to `Npm`, `plugin` to `PluginInstaller`.
- **Node libraries** (`Npm`): `npm install --prefix <bundle> --ignore-scripts --omit=dev <name>@<version>`; the bundle is `<bundle>/node_modules` with its `.bin` (on the terminal's `PATH` after the system's) and `NODE_PATH`. The image's Node is **12.22.9**, and
  the latest of many libraries needs a newer one (express 5 needs 18), so the version is the **newest whose `engines.node` the server's Node satisfies** (`NodeSatisfies`, a small reader of semver ranges; `npm view <name> versions`, then `engines.node` of each candidate, at most 40).
  Checked for real: express 4.22.3, axios 1.21.1, lodash 4.18.1, moment 2.31.0 install and load. The check step loads the module with the server's `node`. npm is found by `OPENREPL_NPM`, the PATH, or `/root/.nvm/versions/node/*/bin/npm`.
- **Editor plugins** (`plugins.go`; samples `wordcount`, `clock`): made from text in the Go source, nothing is downloaded. A plugin script defines `openreplPlugin(api)`. `34-store-plugins.js` runs each plugin that is on for the user in a hidden `<iframe sandbox="allow-scripts">`
  (no `allow-same-origin`: the frame has no origin, no cookies, no access to the page, the account or the network of the site). The api has `status(text)` (one line, 200 characters, shown with `textContent` in the editor's status bar), `text()` (the editor's text:
  the user's own) and `every(ms, fn)`. Messages are accepted only from the frames the module made, at most 200 a second for each. `GET /store/api/plugins` sends the scripts of the plugins that are on (at most 64 KB each). A test shows the frame cannot reach the page.
- **Search** (`Service.Search`, `GET /store/api/search?q=&kind=`, one a second for each user): `pixi search "<q>*" --limit 1` (conda-forge, for this node's CPU type: the newest version of each name, the exact name first) and `npm search`; the plugins and languages of the store are searched in the store. Each hit says what the store knows of it (available, installed...)
  and whether it is on the safe list. The page searches 450 ms after typing and shows "Found for ..." under the grid. The first search of conda-forge downloads the part of its index it needs; pixi keeps it.
- **Size before installing** (`Service.Estimate`, `GET /store/api/size`): a solve without installing (conda: the download from the solver and about 2.5 times that on disk; npm: `dist.unpackedSize`), kept an hour. The app's page shows "download about 41 MB, about 99 MB once installed (14 packages, version 3.0.2)".
- **Commands for a server**: `reinstall` (admin) deletes a node's copy of a bundle and the replay makes it again, at once (it ends the waiting of a failed bundle). The gateway does it at once; a worker gets it in the heartbeat reply (`StoreUpdate.Reinstall`, within about 10 s); the admin sees the command waiting.
- **Uninstall**: a signed-in user may uninstall an app when **no other user has it on** (`Service.Uninstall`; their own switch goes with the app; `View.OthersOn` says how many others have it, the button is disabled with the reason, the server refuses with 409). An **admin uninstalls anyway**
  (`remove`, which switches it off for everyone and deletes it on every node).
- **The admin in the store page**: Admin section in the menu (admins only) with Requests (Approve and install, Reject with a reason), Failures (Try again, Pick another version, Mark installed, Reject, the log), Servers and installs (every server: size, what it has, status
  ready / behind / failed / missing, size of each bundle, "Reinstall here"; the installed apps with "Uninstall for everyone"), apt packages. The dashboard's App Store view (Requests, Failures, Installed, Servers, Settings) has the same and more.

## 9. What is not built, and what was not tested

- **The terminal does not find a plugin or a language by itself**: a language is a program on the `PATH` (`lua`, `ruby`), not an entry in the language picker; the picker's REPLs are the image's.
- **Plugins are samples**: there is no way to write or upload one (that needs a review process of its own); the two that exist are in `plugins.go`.
- **conda-forge search shows the name and the newest version, not a description** (pixi's search has none); the page looks the description up when the app is opened.
- A search is slow the first time (the index of conda-forge).
- A real **worker installing for itself from a real gateway's catalog** was not run (the pieces are tested separately); **a signed-in user doing the whole flow in a browser** was not (a browser test cannot sign in: the screens run against stand-in answers).
- The **import check runs outside the sandbox** (as the server, in an empty folder, 60 s limit).
- A user's switch reaching a worker takes up to about 25 seconds (the catalog is made at most every 15 s and rides the next 10 s heartbeat).
- **Real installs, in CI** (job "App Store installs", after the build, on amd64 and arm64, in the image): every app of the safe list (Python libraries, a tool, Node libraries), a language (lua) and a plugin, a real search and size, the sandbox.
  Run by hand in the image 2026-11-14: flask, jq, requests, express, axios, lodash, moment, lua, wordcount, the search and the size all pass.

## 10. The curated catalogue (2026-11-15; canvas "App Store catalogue: all screens", claude.ai/artifact/Xd9Tgt2bzeWxoEHAorMWKd)

Research that the catalogue stands on: `docs/app-store-catalogue-research.md` (per-REPL tables, what each toolchain can build, disk totals). The owner's own notes are `docs/app-store-research-notes.txt` (not rerun).

**What it is.** A list of what the store offers, as data: `src/appstore/catalogue_default.json` (embedded, 157 entries and 3 packs: python/ipython3 libraries, python2.7 legacy, C/C++ tools and libraries, bash tools, Go and Rust tools, Perl, Tcl, SQLite, Node libraries, browser libraries, plugins) plus the admin's changes in the catalog database (`cat:<id>` add or replace, `catdel:<id>` hide, `pack:`, `packdel:`). Only the REPL ids of the spec are accepted (`CatalogueRepls`). An entry: id, name, summary, kind, REPLs (or the place of a plugin), level (core, popular, optional), manager, package, optional pin, who may install (anyone, approval, admin), category, RAM and disk labels, documentation link, licence, check template, labels. `ValidateEntry` checks every field against a fixed list or a pattern; the check is one of four templates (python-import, node-require, version-flag, file-exists) and there is **no field that takes a shell command**.

**Rules.** Listing or importing an entry never installs it. "anyone" entries join the safe list (`isSafe`), "approval" and "admin" do not. The kinds that the installer can make today are `lib`, `tool`, `clib` (a conda prefix with headers/libraries), `node`, `weblib` and third party `plugin` (npm bundles); `lib27`, `gomod`, `jar`, `crate` are listed with their pages ("Listed, not installable yet": the pixi project for Python 2.7 and the three mirrors are not built). `checkApp` runs the entry's own check after an install on every node (a file for a browser library or a header package, an import for a Python library), else the check of the kind.

**Built-in apps** (`builtin.go`): a table of the programs the image has, found out at start (`Builtins()`); listed inside My apps, tagged "Built in", with a switch that is always on and disabled, a disabled Uninstall, the REPLs that use it and a documentation link. Their names cannot be installed or requested (`ErrBuiltin`); a search hit says "Built in (version)".

**Pages.** The store page: filters (language, category, level), cards with level, size and memory labels ("226 MB when used"), a "Browser libraries and plugins" section, a documentation link and licence on every app page, the request dialog with the language and "N requests a day (K used)". The IDE's quick dialog lists the built-ins in My apps and a Docs link on each card. The admin's App Store view has a new **Catalogue** tab: Add app (with a lookup that says whether it solves, its size, and the disk left on every server), Add plugin (exact version, adapter type, entry file; an editor extension asks twice), Bulk add (dry run first; the server asks again when it adds), Packs (install the missing, remove), Export, Import (checked entry by entry, dry run), Verify now, the filters, and the disk of the store on every server (used, limit, reserve). Requests show the language, a Lookup and Approve (opens the Add form filled in; Reject needs a reason).

**Settings** (Site settings / the Settings tab): store limit 15 GB per node (physical size, hard links once), reserve 3 GB (an install is refused when the store is over limit minus reserve), requests per user per day (5), scratch per terminal (256 MB, soft), scratch on a server (2 GB, soft). The workspace limit shown in the status bar is read from `utils.WorkspaceQuotaMB`.

**Privacy.** A user's answer from `/store/api` never has a node name or count, a CPU type, a per-node disk or state, a server path, a raw log, a lock, a setting or anything of another user (name, request, reason, switch). `server/appstore_privacy_test.go` asks **every route** (GET and POST: list, notices, job, info, search, size, plugins, preview-plugins, catalogue, space, install, retry, request, switch, report, uninstall, plugin-switch, ack) and searches the whole answer for secrets and forbidden keys. A failed check goes to the admin's failure list with the node and the log; the user gets a plain sentence.

**Requests (step 5).** `RequestFor` records the language; one request for each app (a second asker is added); a user may make `PerDay` new asks a day (`ErrDaily`, 429); the answer comes back as before.

**Plugins of the preview (step 6).** Ours: Viewport switcher (the size select of the pane), Outline (a new tab of the console: the page's bridge posts its headings and landmarks; a click scrolls the page), Console. They are on by default; a user's switch is a per-user list (`pplug:`, `pplugoff:`, an admin's `pplugdef`), and a plugin that is off has no control in the pane. Third party: Eruda and vConsole, from npm at an exact version, installed like a Node library (npm checks the integrity), served **by this site** at `/preview/lib/<package>/<file>` (public, read only, only the files the entry names, a browser library any file inside its package, a link out of the package refused, 404 for everything else) and added to the previewed page by the server for the user who has them on (`StorageInfo.Tools`, started when the body exists). Nothing is loaded from another site and the policy of the page (`sandbox ...`) is not loosened. A shared session's viewer gets the same, each with their own switches. The editor plugins (wordcount, clock) are unchanged.

**Scratch (step 7).** `src/scratch`, `landlock-helper --scratch-root`, `utils.ScratchDir` (OPENREPL_SCRATCH_DIR, default /scratch, made by the server): for each terminal a folder `/scratch/<home>/t-<random>` that the sandbox lets that terminal read and write and nothing else (`Options.ExtraRW`); not in workspace sync, snapshots, forks or shared copies. The helper sets PYTHONPYCACHEPREFIX, MPLCONFIGDIR, NUMBA_CACHE_DIR, MYPY_CACHE_DIR, RUFF_CACHE_DIR, GOCACHE, GOTMPDIR, CARGO_TARGET_DIR, EVCXR_TMPDIR (and EVCXR_TARGET_DIR), npm_config_cache, CCACHE_DIR, TMPDIR, XDG_CACHE_HOME, XDG_STATE_HOME, TMUX_TMPDIR, JAVA_TOOL_OPTIONS (+ -Djava.io.tmpdir) and OPENREPL_SCRATCH. CARGO_HOME is not moved: it holds the cargo binaries. `server/scratch.go` measures every live terminal's folder every 2 s, clears the caches when it is over the soft cap, stops the terminal (SIGTERM) when it is still over twice in a row, and sweeps the folders of ended terminals every minute (the pid and its start time are in the folder). No mount, no quota, no privilege. The status bar of the terminal shows **Workspace x / 50 MB** and **Scratch y / 256 MB** at the right of "Connected" (`GET /store/api/space`, also for guests; amber at 80 percent, red at 100).

**Wiring of the apps (step 7).** `VarsFor` adds, for a bundle that has no Python of its own, `CPLUS_INCLUDE_PATH`, `C_INCLUDE_PATH`, `LIBRARY_PATH`, `LD_RUN_PATH`, `PKG_CONFIG_PATH`, `PERL5LIB` and `TCLLIBPATH` (also in env.sh), next to PYTHONPATH, NODE_PATH and the activation variables. They point at the read-only store, never at scratch.

**Tests.** Go: `catalogue_test.go` (the default list is sound: every entry valid, a documentation link, a licence, packs consistent; the refusal of shell checks and unsafe links; add, remove, export, import, filters; packs; requests a day; dry run and bulk add; wiring; preview plugins, switches, head, files and links out of the package), `scratch_test.go`, `landlock` (the scratch rule), `server` (`preview_plugins_test.go`, the privacy test per route). Browser: `store-catalogue.spec.js` (built-ins, filters, documentation, browser section and switches, request dialog), `preview-plugins.spec.js` (Viewport, Console and Outline, the switches, Eruda and vConsole, each on the owner's page and on a collaborator's page of a shared session; the status bar numbers). CI (`TestTheCoreOfTheCatalogueInstallsAndLeavesTheWorkspaceAlone`, on amd64 and arm64): the core entries install, pass their own check, a Python library that is imported leaves nothing in the workspace.

**Not built or not proven** (updates section 9): the Go, Maven and Cargo mirrors and the Python 2.7 environment (the entries are listed only); pinning each library to a version the old toolchains build (Go 1.18, Rust 1.74, JDK 11) is data that has to be filled in when those mirrors are built; no version of the catalogue file has been installed end to end on real pixi yet (the CI test is new); the sizes in the default file come from the research report and are estimates.

**Added 2026-11-16:** the snapshot a worker gets carries the admin's catalogue entries (`Snapshot.Catalogue`, kept in memory on the worker), so it picks the same checks and constraints; the runtime table (`runtimes.go`) constrains the solve of C/C++, Perl and Tcl libraries to the image's runtimes (the lookup shows the constraints); a terminal over its scratch soft cap is told why on its own screen (written to its pty), then gets SIGHUP (an interactive shell ignores SIGTERM) and SIGKILL after 3 seconds. Not run for real yet: the stop message and the Perl/Tcl pins.

**Mirrors and Python 2.7 (2026-11-16)** (`mirror.go`, `gomod.go`, `maven.go`, `cargo.go`; kinds `gomod`, `jar`, `crate`, `py27`; apps are named `gomod-cobra`, `jar-gson`, `crate-serde`, `py27-six` so the same name in two ecosystems does not collide). The server fetches once, with the registry's checksums checked, into a bundle; a terminal uses it with no internet. Each picks, when no version is pinned, the newest version that the image's own toolchain accepts, found by asking the toolchain, not from a number in the code:
- **Go modules:** `go get` + `go mod download all` with GOTOOLCHAIN=local (a module that needs a newer Go fails and the next older version is tried), kept as a module proxy layout in `<bundle>/gomod/cache/download`; checked by building a program that imports it with only that proxy. Terminals get `GOPROXY=file://...,off`, `GOFLAGS=-mod=mod`, `GOSUMDB=off`, and `GOMODCACHE` in scratch. The Go REPL `go` (go run) uses them; yaegi cannot import modules.
- **Maven jars:** the version is the newest whose jar's class file version the image's Java can load (read from the jar, after its sha1 is checked); Apache Maven (3.9.9, sha512 checked, downloaded once to `<store>/tools`) copies the runtime dependencies with strict checksums into `<bundle>/jars`. `CLASSPATH` gets `<bundle>/jars/*` and keeps `.`; the helper gives jshell `--class-path` and the jars on its boot class path (jshell runs `--execution local`, which ignores CLASSPATH).
- **Rust crates:** `cargo generate-lockfile` and `cargo vendor --locked` with `CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS=fallback` (newest versions, of the crate and its dependencies, whose rust-version this Rust accepts) into `<bundle>/vendor`; checked with `cargo check --offline` through a source replacement. The helper makes a cargo home in scratch (links to the vendored crates, `[net] offline`), so evcxr's `:dep` works.
- **Python 2.7:** a pixi environment with `python 2.7.*` and the library; `OPENREPL_PY27PATH` holds the site-packages; a `python2*` terminal gets them as PYTHONPATH (and none of Python 3's); in bash run `PYTHONPATH=$OPENREPL_PY27PATH python2.7`.
Worker nodes install the exact version the gateway chose. Proven with real downloads in the dev container (Go 1.23, JDK 21, Rust 1.85): cobra, gson, serde_json and six install, pass their check and are used offline; CI runs `TestTheMirrorsInstallAndAreUsedOffline`. Entries: 10 Go modules, 8 jars, 8 crates (default catalogue). Not covered: yaegi and gomacro cannot import a mirrored module; a Maven library with a native or non-runtime need; the "image's Go/Java/Rust" are read at install time, so after an upgrade the saved pins are re-picked only by a new lock.
