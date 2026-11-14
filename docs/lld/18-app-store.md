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
  apps/<name>-<ver>/         a complete conda prefix (the package and everything it needs), hard-linked from the cache, made once;
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
(`Installer.Resolve`, `micromamba create --dry-run --json`), and every node installs that same version.

One install at a time (a mutex), as a goroutine with four steps the page shows (`installSteps`, `View.Steps`): *checked that it is allowed, resolve (the solve says how many packages and how many MB), download and link* (`micromamba create -p <bundle> --override-channels -c conda-forge python=3.10 <name>=<ver>`), write the marker,
run a check (`checkBundle`: the bundle's own Python starts and imports the package's module, when it has one), record it, and mark the request and the open report answered (`Notices` tells their askers). An admin can install a version of their choice (`InstallVersion`, for an app whose newest version does not solve). A failure removes the half-made bundle, keeps the last lines of the log (the store's folder shortened) and the step it stopped at. The server runs the
installer outside any terminal's sandbox, so terminals need no internet. The store is refused when it is over its limit (`storeLimitMB`, default 2048). A server that stopped in the middle of
an install marks it failed at its next start (`Recover`). No micromamba on the server (`OPENREPL_MICROMAMBA`, or on the PATH) gives the installer `Missing`: every install says so, and the admin page
shows it. `OPENREPL_STORE_INSTALLER=fake` makes tiny bundles (a Python module of the package's name) for tests and development.

## 5. Replay on every node

`Service.Apply(snapshot)` makes a node's store match the catalog: missing bundles are installed (the same pinned versions), every user's `manifest.json` and `env.sh` are written, and, when the
snapshot is complete (`Full`: both listings of the database worked, so an outage is never taken for an empty catalog), bundles that are no longer in it are removed.
- Gateway or standalone: at start, every 10 minutes, and after each admin decision.
- A worker (no database): every minute it POSTs `/store/sync` to the gateway with the shared worker token (`Authorization: Bearer`, constant-time compare) and its state (what it has, what failed, its
  size), and applies the catalog it gets back. The gateway keeps the last state of each worker for the Servers matrix. A change a user makes is therefore on a worker within a minute.
- A bundle already in the store is not fetched again, so a deploy that keeps `/store` costs nothing. A worker takes a missing bundle as a .tar.gz from the gateway (`Store.WriteBundle` / `ReadBundle`, which refuse any entry that leaves the
  bundle; the files hold the store's path, so every node of a site uses the same `OPENREPL_STORE_DIR`) and installs it itself only if that fails. The gateway rebuilds from conda-forge after a wipe.
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

**The quick dialog** in the editor (`33-app-store.js`, the Apps button and "App Store" in the command menu) stays, with a link "Open the full App Store"; it also offers "Use it in my active terminal" (asks, then types the line).
`AppStore.installAndOn(name)` installs a safe app and, when it is done, switches it on. **The terminal's own error** ("No module named flask", `lib-store-hint.mjs`) shows a notice with "Install and turn on" (safe apps),
"Turn it on" (installed), or "Open the App Store"; one notice for each name in a visit; a name the store does not know is never offered.

**Genie**: the agent action `store_suggest` (`{"type":"store_suggest","name":"flask"}`) is a permission scope of its own ("store"): the card asks "Genie wants to install an app from the App Store"; on yes it calls `installAndOn`
(an app that needs an admin is only shown in the store). It is asked every time (not in the everyday scopes). The agent prompt (`server/agent.go`) says when to use it.

## 7. Admin

`viewStore` ("App Store" in the dashboard; canvas boards 7 and 8): tabs.
- **Requests**: App, Asked by, Why, Status; Approve and install, Reject (a reason the askers see).
- **Failures**: App and version, Reported by, Where it stopped, What they wanted, the log, the **server** it ran on, Try again, **Pick another version**, Mark installed, Reject.
- **Installed**: size, how many users have it on, **"unused for N days" when nobody has had it on for 60 days** (nothing is removed by itself), ready / **fetching** / failed / missing on the gateway and each worker (fetching: it is behind and answered
  within 3 minutes), Remove (asks, says how many users lose it), install for everyone.
- **Servers**: the disk of the store on each server against the limit (a bar), and what a server could not do (an app, an apt package).
- **Settings**: the safe list, the size limit, the channel (conda-forge, not editable), the switch, and the apt list.
- The **Health** page has an "App Store" card (`appStoreHealth`): the installer (a warning when micromamba is missing), apps installed, size against the limit, failed installs, workers that could not apply the catalog.

## 8. What is not built, and what was not tested

- **Percent of a download on a worker** ("fetching 40%" in the canvas): a worker tells the gateway what it has only after each replay, so the admin sees "fetching" and not a percentage.
- **Search of all of conda-forge**: the store searches the safe list and the apps it knows, and looks up one exact name on conda-forge (summary, newest version, the versions for an admin); it has no full index search.
- **The size of an app before it is installed** (the canvas shows "about 14 MB"): shown once it is installed.
- **Object-store tarballs**: instead a worker takes the finished bundle as a .tar.gz from the **gateway** (`GET /store/bundle`, worker token) and falls back to installing it itself; the object store is not used.
- **The import check runs outside the sandbox** (as the server, in an empty folder, 60 s limit), not inside a terminal's sandbox.
- Node libraries (npm) and other kinds (languages, editor plugins): their sections say so; `NODE_PATH` is set for a bundle that has `lib/node_modules`, nothing installs them.
- A user's own switch reaching a worker takes up to a minute (the poll).
- A real micromamba install **was run** (2026-11-14, in the new image): the owner's first try failed on `MAMBA_NO_RC=1` ("Bad conversion of configurable 'no_rc'": micromamba cannot read a switch from the environment), so only the two folders
  are passed by environment and the switches are flags (`TestMambaIsGivenNoSwitchesInTheEnvironment`). With that, `python=3.10 beautifulsoup4` solves (29 packages), installs (130 MB, 62 MB of cache shared by hard links), starts, and the system's
  `python3` imports it through `PYTHONPATH`. Not run: an install through the server's own job with a signed-in user, and a worker fetching a real bundle from a real gateway.
- A browser test cannot sign in: the signed-in screens are tested against stand-in answers.
