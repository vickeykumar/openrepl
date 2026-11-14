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
written into a bundle either; Python then simply does not cache it.

Checked by hand 2026-11-14 with the real helper on Landlock (arm64 image): a user with `flask` on can `import flask`, run its tool, and sees `numpy` as "No module named"; listing
`/store/apps`, another bundle or another user's folder gives "Permission denied"; writing a bundle or one's own manifest is refused; `["../../etc"]` in a manifest stops the start.

## 4. Installing (`appstore.Service`)

State lives in the site's database (`persist`, file `appstore.db` or Firestore/MongoDB like the other databases): `app:<name>` (name, pinned version, state installing / installed /
failed, size, log tail), `req:<name>`, `rep:<name>`, `user:<home>` (the list), `apt`. The version is pinned by the store: the newest conda-forge build that solves with the server's Python
(`Installer.Resolve`, `micromamba create --dry-run --json`), and every node installs that same version.

One install at a time (a mutex), as a goroutine: *resolve, download and link* (`micromamba create -p <bundle> --override-channels -c conda-forge python=3.10 <name>=<ver>`), write the marker,
record it, answer the request and the open report. A failure removes the half-made bundle, keeps the last lines of the log (the store's folder shortened) and the step it stopped at. The server runs the
installer outside any terminal's sandbox, so terminals need no internet. The store is refused when it is over its limit (`storeLimitMB`, default 2048). A server that stopped in the middle of
an install marks it failed at its next start (`Recover`). No micromamba on the server (`OPENREPL_MICROMAMBA`, or on the PATH) gives the installer `Missing`: every install says so, and the admin page
shows it. `OPENREPL_STORE_INSTALLER=fake` makes tiny bundles (a Python module of the package's name) for tests and development.

## 5. Replay on every node

`Service.Apply(snapshot)` makes a node's store match the catalog: missing bundles are installed (the same pinned versions), every user's `manifest.json` and `env.sh` are written, and, when the
snapshot is complete (`Full`: both listings of the database worked, so an outage is never taken for an empty catalog), bundles that are no longer in it are removed.
- Gateway or standalone: at start, every 10 minutes, and after each admin decision.
- A worker (no database): every minute it POSTs `/store/sync` to the gateway with the shared worker token (`Authorization: Bearer`, constant-time compare) and its state (what it has, what failed, its
  size), and applies the catalog it gets back. The gateway keeps the last state of each worker for the Servers matrix. A change a user makes is therefore on a worker within a minute.
- A bundle already in the store is not fetched again, so a deploy that keeps `/store` costs nothing; one that wipes it rebuilds every bundle from conda-forge (the object-store tarballs of the design are **not
  built**: the gateway and workers all have internet).
- apt: the admin's list (database) is part of the snapshot; each node installs what is missing with `apt-get install -y --no-install-recommends` (`SystemApt`; it needs root and apt-get, and says so when it
  has neither) at every replay, and leaves what `dpkg -s` finds. A package that fails is said in that node's state and on the admin page (a card for `apt:<name>`) and does not stop the others. These are for all users:
  there is no switch. A server with the fake installer never runs apt-get. A node that loses its system between starts (a container) installs them again at its next start; for big packages the image is the better place.

## 6. The page

`33-app-store.js`: a dialog (`#appstore-dialog`) opened by the Apps button (shown to signed-in users: `body.is-signed-in`), the command menu entry "App Store", `AppStore.open()` and `AppStore.suggest(name)`. Browse (search; a name
nothing matches is looked up on conda-forge, `GET /store/api/info`, and can be asked for), My apps (what is on, asked for, failed). Cards for every state: available (Install), installing (the step; the page polls and
tells you when it is done even if the dialog is closed), installed (a switch, off until the user turns it on), failed (the reason and a log tail, Try again, Report to admin), requested, rejected (with the admin's reason).
After a switch the dialog offers "Use it in my active terminal": it asks first, then types `. '<env.sh>'` into the active terminal (open terminals keep what they had; new ones use the list).

The terminal's own error: every 3 s, for a signed-in user with the store closed, the page reads the last lines of the active terminal (`lib-store-hint.mjs`, tested in `lib-store-hint.test.mjs`): "No module named 'flask'"
(a few module names are mapped to their package: PIL to pillow, yaml to pyyaml, ...) or "x: command not found". If the store has that app (on the safe list, installed, or asked for) and it is not on for the user, a notice
offers "Open the App Store" (or "Turn it on" for an installed one). One notice for each name in a visit; a name the store does not know is never offered; nothing is installed or typed by the notice.

Genie: the agent action `store_suggest` (`{"type":"store_suggest","name":"flask"}`) opens the store on an app; **the user decides**. It needs no permission because it installs nothing. The agent prompt
(`server/agent.go`) says when to use it.

## 7. Admin

`viewStore` ("App Store" in the dashboard): requests (Approve and install, Reject with a reason), failures reported (Try again, Mark installed, Reject), installed apps with their size, how many users have each on,
and ready / failed / missing on each server (gateway and every worker that has synced), Remove (asks, says how many users lose it), install for everyone, the apt list, and the settings: the safe list, the size
limit and the switch (`SiteSettings.StoreSafeList`, `StoreLimitMB`, `StoreOff`).

## 8. Not built, and not tested

- The tarballs of finished bundles in the object store (nodes build from conda-forge).
- A user's own switch reaching a worker takes up to a minute (the poll); a terminal started in that minute does not have it yet.
- Node libraries (npm) and other kinds (languages, editor plugins): `NODE_PATH` is set for a bundle that has `lib/node_modules`, nothing installs them.
- A real micromamba install was **not run** here (the image has micromamba only after the Dockerfile's new step); the fake installer, the Go tests, the browser tests with the page's calls answered by a stand-in, and the
  hand check of the sandbox were.
- A browser test cannot sign in: the signed-in screens are tested against stand-in answers.
