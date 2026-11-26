# 20. IDE data usage

What the IDE sends to a user is counted, a terminal that prints too fast is stopped, and a user who reached the limit loses the heavy parts. Designed on the canvas "IDE data usage and limits" (claude.ai/artifact/RZxfb1yLa7ytxy3vn15n2U, 8 boards, approved 2026-11-25). The trigger was the hour of 24 Nov 2026 18:00 UTC, when one workspace's `print()` loop sent about 1.27 GB through WebSocket responses and nothing on the server stopped it.

Code: package `src/ideusage` (counts, limits, speed cap, frame gate), `src/server/ideusage.go` (middleware, routes, endpoints), `src/server/ideusage_settings.go` (settings), `src/cookie` (`QuietIdentity`), the pages `resources/js/ide-usage.js` (chip and notes), `resources/js/profile-usage.js` (profile section), the admin cards in `resources/js/admin-site.js`, the Users drawer and Usage view in `resources/js/admin.js`.

## 1. What is counted

Only what goes **out**, from the IDE only. Where a request is sorted into a part (`ideRoute`):

| Part | Requests | At the limit |
|---|---|---|
| Terminal | the WebSocket of every terminal (`/ws`, `/ws_c`, `/ws_<command>`) | output stops and the run is ended |
| Editor | `/ws_filebrowser?q=load` (a file opened in the editor) | only a small file, from the reserve |
| File browser | the rest of `/ws_filebrowser` and `/upload_file`; `?q=zip` is the download | the file list stays, downloads stop |
| Genie | `/chat/completions`, `/agent_bash` (a new question or step), `/agent_files`, `/agent_restore`, `/genie_chats`, `/genie_ext`, `/genie_conn` | a new question or step is refused (429), the rest stays |
| Preview | `/preview/<token>/...` (on the preview host too) except `/preview/lib/` | refused with a notice page (429) |

Everything else (pages, scripts, styles, images, FAQ, blog, store listings, sign-in, profile, admin) is not counted. What the user sends in (keystrokes, saves, uploads, prompts) is not counted. Only sizes are kept.

The count is made where the bytes leave the server that faces the user: the gateway, or a single server. A request that came over the gateway's tunnel (`isTrusted`) is skipped on a worker, so nothing is counted twice. The counter wraps the `http.ResponseWriter` (`ideWriter`, bytes written after compression), and for a terminal it also wraps the hijacked connection (`ideusage.GateConn`).

The meter is installed between `wrapPreviewHost` and `wrapControls`, in every mode, so the preview host's rewritten paths and the preview mark are already in place.

## 2. Whose count

One count for a workspace folder (the name of the folder in the session cookie: `cookie.QuietIdentity`, which logs and saves nothing). The Preview carries no cookie, so its token names the workspace (`livepreview.Viewer`). A signed-in user's count is for the day, a guest's for the session: a guest folder lasts an hour (`utils.DEADLINE_MINUTES`) and its count goes with it (forgotten after 3 hours idle). A guest who clears the cookies gets a new folder and a fresh count; this is accepted. Admins are exempt unless the setting counts them (`ideAdminsCounted`); an admin is known by the account of the cookie (`uidIsAdmin`, remembered for a minute), and the meter remembers it so a preview token can be exempt too.

The day starts at the reset minute (UTC, default 00:00). The page sends its time zone (`tz`, minutes east of UTC) with every `GET /ide_usage`; the meter keeps it for the time a terminal notice names (`ideResetText`). The pages show the reset in the browser's own time zone.

## 3. The terminal's speed cap

A terminal that sends more than 20 MB in any 10 seconds (settings) is **paused**: `ideusage.Rate.Wait` holds the frame, so the program waits and nothing more is sent. If it is still over after 5 seconds (settings) the run is **ended**: the gate writes the notice as terminal output, the end notice (`webtty.End`, reason `terminal output too fast`) and a close frame, and closes the connection, which hangs the process up like Stop. A burst that settles inside the grace carries on. "Pause only" never ends a run (the window empties by itself). Only output frames (`webtty.Output`) are held; titles, pongs and the like always go.

`GateConn` follows the WebSocket frames of what the server writes without keeping payloads: it passes the HTTP answer that opens the connection, reads each frame header and its first payload byte (the terminal message type), asks the gate, then streams the payload on. It works for gorilla's own writes and behind the gateway's `httputil.ReverseProxy` (tested). Frames that split anywhere (even inside their header) come out unchanged.

## 4. The daily total and the limit

One total across the parts: guest 50 MB for the session, signed in 200 MB a day (settings). 80% (settings) warns; 100% is the limit. At the limit (option A of the canvas, the only one built) the heavy parts stop as the table in section 1 says, and a **reserve** of 2 MB (settings) stays open: the editor opens a small file (at most 256 KB, by adding `max=` to the load; a bigger one answers 413) and the file list and saves keep working, until the reserve is spent. Bytes sent past the limit use the reserve. The user's files are never touched. The other canvas options (a slow lane, going over the limit with credits) are not built.

## 5. Where it is kept

Live counts are in memory. A signed-in account's record (`day`, the parts, the reserve, and up to 90 days of history) is kept in the **user database**, the one that holds the profile (`user.GetUserDBHandle`, key `ide-usage:u:<uid>`), so it follows the profile to the remote database and the backups. Because that database writes through to a remote one (the Firestore 429 outage of 2026-11-09), a record is saved at most every 30 minutes when it changed, and once more at shutdown: a crash can lose up to 30 minutes of counting. A record is read the first time an account's uid is seen, and merged with what was counted before the uid was known (a preview token has no uid). A guest is never saved by name: only the combined total of all guests by day (`ide-usage:guests`, for the admin card) is.

## 6. The pages

- **IDE** (`ide-usage.js`, loaded by `index.html`): asks `GET /ide_usage?days=0&tz=` every minute. At the warning a chip (amber) and a note once for each day (each session for a guest); at the limit a red chip and a note (a guest: Sign in; a signed-in user: See usage and a greyed "Add credits, coming later"); when the day starts again the chip goes green and a note says the limit is back. The terminal prints its own notice when its run is ended.
- **Profile** (`profile-usage.js`, section "Data usage"): today's number, the bar split by part with a legend, the reset time in the user's time zone and the time left, the last 7 days with the limit line, the plan (Free, daily data, terminal speed, warning) and the empty slot for paid plans and credits. It stays hidden while metering is off.
- **Admin** (Site settings, section "IDE data usage"): Metering (on, admins exempt, counted parts), Terminal speed, Daily total Free plan (guest, signed in, warning, reserve, small file size, at the limit, resets at), Plans (Free active, paid later), and a column with Today (all users) and Top today. The Users drawer has a "Data today" line and a button that resets today's count by hand (`POST /admin/ide-usage?uid=`, audited). The Usage view has the full list of the day.

## 7. Settings (`SiteSettings.IDE*`, all admin, live)

`IDEUsageOff`, `IDEAdminsCounted`, `IDEPartsOff`, `IDETermMB` (20), `IDETermWindowSecs` (10), `IDETermGraceSecs` (5), `IDETermPauseOnly`, `IDEGuestMB` (50), `IDEUserMB` (200), `IDEWarnPct` (80), `IDEReserveMB` (2), `IDESmallKB` (256), `IDEResetMinutes` (0). A 0 means the built-in number. No environment variable.

## 8. Tests

`src/ideusage/ideusage_test.go` (counts, limits, reserve, the day and the reset minute, guest sessions, exempt admins, parts off, the store and its throttle, the rate, the frame gate in odd pieces and at a frame start), `src/server/ideusage_test.go` (routes into parts, what is counted, trusted requests, the limit for each part, the small file at the limit, admins, a real WebSocket flood paused and ended with the notice, a normal terminal untouched, the limit notice, pause only, behind a reverse proxy, the endpoint and the settings), knowledge cases in `knowledge_test.go`, `e2e/tests/ide-usage.spec.js`.

## 9. Not built

Paid plans and credits (only the empty slot and a greyed button), the slow lane and credit-overage ways of handling the limit, a per-network cap for guests, metering of the website itself, any change to Genie model billing. Not verified: a real gateway with a Pi worker, the Render proxy in front, a real browser run of the profile and admin pages (the e2e spec runs in CI).

## 10. History and the accounts that need a look (2026-11-26)

Designed on the canvas "IDE data usage: daily history and top users (admin)" (claude.ai/artifact/DVZJgM69MT3STYhCNBfUop, 5 boards, built as drawn). The daily records of section 5 were already kept; this reads them for an admin, adds what was missing, and flags the accounts worth opening.

**Kept.** A signed-in account's record holds `HistoryDays` of daily counts (setting, 30 to 365, empty 90; `trimHist`) and, new, the terminal stops of each day (`Stops`: paused and ended, counted by `Meter.NoteStop` from the gate: one pause for a run of held frames, `Rate.WaitPaused` says a frame waited). An index `ide-usage:index` (uid to the last day of its record) is saved with the records, so that the history of an account that has not been seen since the server started can be read (`roster`: the records of the index are read from the store, at most 500 in a call, kept 10 minutes). Guests: one combined number for each day (bytes, as before, and new `ide-usage:guest-sessions`, how many guest sessions were counted each day), 400 days; never a list.

**Read.** `Meter.Report(period)` (the days of all signed-in accounts by part with the guests of each day, the accounts with total, average, series, days at the limit, longest run, biggest part, peak, today, stops; the totals, accounts counted at 1 MB, accounts and account-days at the limit, the busiest day), `Meter.Account(uid, period)` (one account with the parts of each day), `Meter.Flags(watch)`. Endpoints, behind the admin check: `GET /admin/ide-usage?report=<days>` (up to 200 accounts, names and emails from the profile, kept 10 minutes; with `flags`, `watch`, `thresholds`; `&format=csv`), `GET ?acct=<uid>&days=<n>` (`&format=csv`), `POST ?watch=1|0&uid=` (the watch list, audited, at most 50). A CSV cell that starts like a formula is quoted (`csvCell`).

**Flags** (`Flags`, settings `IDEFlag*`): `limit` an account at the limit `FlagLimitDays` days in a row (3; a run that ends today or yesterday), `jump` a day `FlagJump` times the average of the 14 days before (5; never under 20 MB), `near` above `FlagNearPct` percent of the limit (90), `stops` paused plus ended more than `FlagStops` times today (5), `watched` an account on the watch list (`SiteSettings.IDEWatch`, kept by `/admin/ide-usage`, not by the settings form) that has no other flag. The worst first. `IDEFlagLog` (off) writes a line for each account, kind and day to the server log every 30 minutes (`logIDEFlags`, with the name, never sizes of what was done). The Health page has a row "IDE data usage" (amber: N accounts need a look).

**Pages.** Site usage has two tabs (`viewSiteUsage` in `admin.js`; the hash `#traffic/data` is the second): Visitors (as before) and Data usage (`viewDataUsage`): the period (Today, 7, 30, 90 days), four stat cards, the stacked chart of the days with the limit line and the guests' number, the strip "Needs a look", the watch list, and the table of the top accounts (search, sort, part, a trend for each, click for the wide drawer of the account, `openAccountUsage`: stats, 30 days by part, the share of each part, Watch, Reset today's count, Download CSV). The Users drawer's "Data today" has "See the 30 days". Site settings, IDE data usage has the cards History (days kept) and Needs a look (the four thresholds, where it is told). The two fixes: the Top today card and the totals use classes of `admin.css` (the dashboard's policy, `style-src 'self'`, blocks a style attribute set by script, which had made the total small and a name touch its number), and sizes under 1 MB show in KB.

**Differences from the canvas.** The thresholds and "Where it is told" cards are in Site settings (they are settings, saved with Save), with the strip, its explanation and the watch list in the tab. The tag "new count" and its note are gone: the stops are counted now. Search covers the 200 accounts that were sent the most.

**Tests.** `ideusage/history_test.go`, `server/ideusage_test.go` (endpoints, watch list, settings), `e2e/tests/admin-data-usage.spec.js`, `admin-ide-usage.spec.js`.

Not built: email to the admins (the site cannot send one), search past the 200 most-sent accounts, a per-hour history.
