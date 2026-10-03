# 13. Admin dashboard

`/admin` is a single page for running a site: the fleet, the visitors' sessions, the switches that change what every visitor sees, the content visitors leave behind, and the health of the server. The page is static (`resources/admin.html`, `css/admin.css`, `js/admin.js`). It holds no data; it reads and changes everything through a JSON API under `/admin/...`, which is the only thing the server checks.

Files: `server/admin_core.go` (access rules, audit log), `admin_routes.go` (routes, page, site controls), `admin_info.go` (parameters), `admin_health.go`, `admin_stats.go`, `admin_logs.go`, `admin_content.go` (feedback, shared code, accounts), `settings.go`, and in `gateway/` `admin.go` and `terminals.go`.

## 1. Who gets in

An admin is a signed-in account whose email address is in `OPENREPL_ADMIN_EMAILS` (`IsUserAdmin`, LLD 05). A session that was signed out or that belongs to a blocked account is not an admin any more at the next request. `Server.isAdmin` is that check; tests replace it with `Server.admin.check`.

Every route goes through one of two wrappers.

| Wrapper | Used for | Rules |
|---|---|---|
| `wrapAdmin` | The page, `/admin` | Not an admin: the 401 error page. |
| `adminAPI` | Everything below `/admin/` | Not an admin: 401 `{"error": ...}`. A request that is not GET, HEAD or OPTIONS must carry `X-Requested-With: openrepl-admin`, and its `Origin`, if it has one, must be this site; otherwise 403. Every reply has `Cache-Control: no-store`. |

The header is the dashboard's proof that a change came from its own page: a form or a script on another site cannot add a header without the browser asking this site first, and the site never allows that. It deliberately does not start with `X-Openrepl-`, because the gateway removes headers with that prefix (LLD 11). A command line client sends the header itself, see the [operator guide](../distributed-mode.md#operating-a-fleet).

The page is served with a Content-Security-Policy that allows only its own script and style, the site's fonts, and requests to this site (`adminCSP`), plus `X-Frame-Options: DENY`. Everything that comes from visitors or from the machine (feedback, shared code, email addresses, log lines) is put on the page with `textContent` and `setAttribute` only, never as HTML.

## 2. Routes

All of them run on the gateway or the standalone server, never on a worker. The fleet routes exist only in gateway mode.

| Route | Method | Purpose |
|---|---|---|
| `/admin` | GET | The page. |
| `/admin/gateway` | GET | How the server was started (section 5) and its counts. |
| `/admin/health` | GET | The health checks (section 5). |
| `/admin/settings` | GET, POST | The site settings and the list of languages (section 3). A POST replaces all settings. |
| `/admin/stats` | GET | Terminals started and visitors for the last 30 days, and the totals by language (section 6). |
| `/admin/audit` | GET | The last 200 admin changes, newest first. |
| `/admin/logs` | GET | The end of the log: `lines` (default 200, at most 1000), `q` (text to find), `level=error`. |
| `/admin/feedback` | GET | Messages, newest first, and the unread count. `?format=csv` downloads them. |
| `/admin/feedback/<id>/read`, `/unread`, `/delete` | POST | `<id>` is the message's key, digits only. |
| `/admin/snippets` | GET | The newest 300 shared snippets with a short preview. |
| `/admin/snippets/<id>` | GET | One snippet with its code. |
| `/admin/snippets/<id>/delete` | POST | Remove it; its `/s/<id>` link stops working. |
| `/admin/users` | GET | Accounts: email, name, live sessions, blocked, admin. |
| `/admin/users/<uid>/signout`, `/block`, `/unblock` | POST | See section 4. |
| `/admin/workers` | GET | Gateway: every node (LLD 11 section 11). |
| `/admin/workers/<id>/drain`, `/undrain`, `/reconnect` | POST | Gateway: stop or resume new sessions; drop the connection of a worker, which connects again by itself. |
| `/admin/sessions` | GET | Gateway: the execution contexts with user, node, terminals, home, last activity and expiry. |
| `/admin/sessions/<key>/end` | POST | Gateway: close the session's terminals and forget its placement. |
| `/admin/sessions/<key>/move` | POST | Gateway: body `{"to": "<node>"}`. See section 4. |

Every change that is not just a flag on a message (settings, deleting feedback or a snippet, signing out or blocking a user, ending or moving a session, draining, reconnecting) is written to the audit log with the admin's address, the action and a short detail. The detail never holds a secret, and for the announcement it says that the text changed, not what it says.

## 3. Site settings

`SiteSettings` (`settings.go`) is stored in `settings.json` (LLD 05) and applied without a restart.

| Field | Effect |
|---|---|
| `colorOfTheDay` | `preprocessing.js` picks the day's accent colour (LLD 06). The dashboard shows today's colour from a copy of the same list; a test fails if the two lists differ. |
| `announcement` {`text`, `level`} | A banner on every page while `text` is not empty; `level` is `info` or `warning`; at most 280 characters. A visitor can dismiss it; the dismissal is remembered in the browser for that text. |
| `maintenance` {`enabled`, `message`} | New terminals are refused for everybody but admins, with the message (or a default). Pages, files and the rest of the site keep working. The banner shows on every page. |
| `disabledLanguages` | Values of the language picker (`python`, `cpp`, ...). The picker greys them out and their new terminals are refused. Open terminals are not touched. Admins can still start them. |
| `genie` {`disabled`, `guestPerMinute`, `userPerMinute`} | Off: the Genie buttons are hidden and `handleChatProxy` answers 503 `GenieDisabled` (admins excepted). The rates are requests per minute, 0 meaning the built-in `GUEST_FACTOR` and `USER_FACTOR`; `utils.SetGenieRates` applies them to the cookie package's balance arithmetic. Between 0 and 60. |

`normalize` checks a POST and says what is wrong (400); a settings file that fails the same check is ignored with a log line. Visitors read the public part through `/settings.js` (`publicSettings`): never the Genie rates.

**Where the switches are applied.** `wrapControls` sits in front of the whole handler tree, so in gateway mode it is in front of the router and covers the terminals of workers too. For a WebSocket upgrade on a terminal route (`Server.terminals`) it checks maintenance and the language; a refusal completes the handshake and closes at once with the reason `site notice: <text>`. `webtty.ts` turns that reason into the close kind `notice`, and the page shows the text in the terminal banner (`11-terminal-state.js`). The same prefix is used when an admin ends a session (`gateway.EndedReason`). A worker applies none of this: a visitor of a worker's own port (LLD 11) is outside the gateway's controls.

## 4. People and sessions

**Accounts.** `user.ListAccounts` reads the user database with a cursor (`cachedb.Database.Each`), skipping records that are not profiles (the cookie secret, worker pins, block flags). *Sign out everywhere* empties the account's session map: the browsers keep their cookies, but the sessions are no longer known, so every check treats them as signed out. *Block* sets a flag (`blocked:<uid>` in the same database, mirrored in memory) and signs the account out; `IsSessionExpired` then returns true for it, `UpdateAndStoreSessionData` refuses to store a new session (`ErrBlocked`) and `/login` answers 403. An admin account, or your own, cannot be blocked.

**Feedback.** The record gains `Read`. Deleting uses the existing `deleteFeedbackData`. The CSV export puts a quote in front of any cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return, because visitors write every field.

**Ending a session** (`Router.EndSession`). The router keeps a cancel function for every open terminal of a session (`trackTerminal`, from `execute`). Ending cancels them and releases the session's execution context. For a worker, cancelling the request makes the reverse proxy close the connection to the worker, and `closeGuard` ends the browser's WebSocket with `EndedReason`. A local terminal watches its request's context in `processWSConn` and returns `errSessionEnded`. Files are not touched, and the visitor is placed again by the next request, on the same node if they are signed in (their pin is kept).

**Moving a session** (`Router.MoveSession`) needs workspace sync (LLD 12), because the new node is sent the gateway's copy of the home (`PrepareHome`) and the old node's copy is dropped (`OnMoved`). It refuses a target that is the current node, unknown, or not `ONLINE`. The terminals close, the context is recreated on the target, and a signed-in user's pin follows. It is not counted as a pick. A file changed in the last moments may not have reached the gateway yet; the dashboard says so before it moves.

## 5. Looking at the server

**Parameters** (`/admin/gateway`, `paramsReply`) are built field by field. A credential, token, API key or password is never in it, only whether one is set; `TestGatewayParametersNeverHoldSecrets` checks that. It also says where the settings came from (environment, env file, git-config file), which Firebase project signs people in, how many admins there are, the gateway options and the tunnel host key fingerprint a worker needs, and runtime figures. The dashboard's "add a worker" box builds the worker's command line from these (the token is left as a placeholder).

**Health** (`health()`) checks the data directory and the disk space of data and homes (warning below 15% free, failure below 5%), the log directory, the databases, the sandbox (`containers.Status`: how many REPLs have a cgroup), that someone is an admin, which Firebase project is in use (a warning for a dev server on the production project), the OpenAI key, `dev` mode, maintenance mode, and in gateway mode each worker's state, workspace sync and the clock difference to each worker (a warning above 2 seconds). It returns the worst status and a list.

**Log** (`/admin/logs`) reads at most the last megabyte of `gotty.log` and masks, in each line, headers (`Authorization`, `Cookie`, `X-Openrepl-*`), bearer and basic credentials, `key=value` pairs that name a secret, JSON web tokens, any other long run of token characters, and email addresses (kept as `v***@example.com`). A search matches the masked line.

The dashboard's own reads (GET under `/admin/` that succeed) are left out of the request log, so a dashboard left open does not fill it. Refused reads and every change are logged.

## 6. Usage numbers

`statsStore` counts, where `wrapControls` lets a terminal through, one terminal per language per day, and the visitors of the day. A visitor is a keyed hash of the account or, for a guest, the address, different on each day, so one visitor who opens several terminals counts once. Only today keeps these hashes (to count across a restart); older days keep numbers only, 60 days in all. The file is `admin-stats.json`, saved at most every 30 seconds. A gateway counts the terminals of its workers; a worker's own port is not counted.

## 7. Front end

`admin.js` is one file without libraries. A view is `{root, refresh, poll}`; the router shows the view of the URL hash (`#workers`, `#site`, ...) and hides the fleet views when the server is not a gateway.

- The page refreshes the open view every 5 seconds. The overview fetches the slower data (settings, usage, audit, feedback) every 30 seconds. Nothing is fetched while the tab is hidden, and the header button pauses it. A view redraws only when its data changed, and times ("3m ago") are kept current by a one-second ticker.
- The settings form is never redrawn by a refresh. It tracks unsaved changes, asks before leaving them, and asks before turning maintenance mode on.
- A dialog (`<dialog>`) confirms ending a session, moving, reconnecting, blocking, signing out and every delete. A dangerous dialog opens with *Cancel* focused.
- A 401 from any call replaces the page with a "signed out" message.

**On the site's own pages**, `preprocessing.js` (loaded by every page with `/settings.js`) shows the banners, greys out switched-off languages in `#optionlist` and hides the Genie buttons. `GET /profile?q=json` carries `isAdmin`, and the account menu gets an *Admin* link when it is true (`05-auth.js`); the dashboard checks again.

## 8. Build

`Makefile` copies `resources/admin.html` to `bindata/static/admin.html` (`asset` depends on it); `css/` and `js/` are copied as a whole, and `bindata/static/js` is rebuilt whenever a file in `resources/js` changes. `handleAdminPage` reads the page from bindata.

## 9. Tests

`server/admin_test.go` runs every route with temporary files (the real ones belong to a running server): access (visitor 401, change without the header 403, a foreign origin 403), the real admin check with signed cookies (an admin, a user, a signed-out admin, a blocked admin), no secret in the parameters, settings validation, saving and the audit text, the audit file's size and mode, maintenance and switched-off languages over real WebSockets, the Genie switch, usage numbers across a restart, log masking, the feedback inbox and CSV, shared-code moderation, sign out and block, the page's security headers, and the colour list. `gateway/adminactions_test.go` covers ending and moving sessions (including the rules for a move), reconnecting, the sync details in the worker list, and `closeguard_test.go` the notice sent for an ended terminal. `utils/genie_test.go` covers the rates.

## 10. Limits

- The numbers are for the process that serves them: they restart with it, except usage and the audit log, which are files.
- Settings are read by the gateway or standalone server. A worker started with its own port serves that port without them.
- The user list shows accounts that signed in at least once; it has no paging, which suits thousands of accounts, not millions.
- The log viewer shows the current file, not the rotated ones.
