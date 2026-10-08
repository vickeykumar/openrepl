# 13. Admin dashboard

`/admin` is a single page for running a site: the fleet, the visitors' sessions, the switches that change what every visitor sees, the content visitors leave behind, and the health of the server. The page is static (`resources/admin.html`, `css/admin.css`, `js/admin.js`). It holds no data; it reads and changes everything through a JSON API under `/admin/...`, which is the only thing the server checks.

Files: `server/admin_core.go` (access rules, audit log), `admin_routes.go` (routes, page, site controls), `admin_info.go` (parameters), `admin_health.go`, `admin_stats.go`, `admin_logs.go`, `admin_content.go` (feedback, shared code, accounts), `settings.go`, and in `gateway/` `admin.go` and `terminals.go`.

## 1. Who gets in

An admin is a signed-in account whose email address is in `OPENREPL_ADMIN_EMAILS` (an *owner*) or was added in the dashboard (`IsUserAdmin` and `utils.IsAdminEmail`, LLD 05). A session that was signed out or that belongs to a blocked account is not an admin any more at the next request. `Server.isAdmin` is that check; tests replace it with `Server.admin.check`.

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
| `/admin/admins` | GET, POST | The owners (from the environment) and the admins added in the dashboard, and whether the caller is an owner. `{action: "add"|"remove", email}` changes the added ones, for owners only (section 4). |
| `/admin/keys` | GET, POST | The OpenAI and OpenRouter keys: whether each is set and where from (never the key), and `{provider, key}` to replace or, with an empty key, remove the one saved in the dashboard (section 3). |
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
| `/admin/workers/<id>/languages` | GET, POST | Gateway: the Languages card of a node (`<id>` is a worker id, or `local` for the gateway). GET lists the languages with what the node declares and what an admin decided; POST `{"language": "rappel", "rule": "default" \| "off" \| "on"}` sets one. Saved in `SiteSettings.NodeLanguages`, audited. See below. |
| `/admin/sessions` | GET | Gateway: the execution contexts with user, node, terminals, home, last activity and expiry. |
| `/admin/sessions/<key>/end` | POST | Gateway: close the session's terminals and forget its placement. |
| `/admin/sessions/<key>/move` | POST | Gateway: body `{"to": "<node>"}`. See section 4. |

Every change that is not just a flag on a message (settings, deleting feedback or a snippet, signing out or blocking a user, ending or moving a session, draining, reconnecting) is written to the audit log with the admin's address, the action and a short detail. The detail never holds a secret, and for the announcement it says that the text changed, not what it says.

## 3. Site settings

`SiteSettings` (`settings.go`) is stored in `settings.json` (LLD 05), or in MongoDB (below), and applied without a restart.

| Field | Effect |
|---|---|
| `colorOfTheDay` | `preprocessing.js` picks the day's accent colour (LLD 06). The dashboard shows today's colour from a copy of the same list; a test fails if the two lists differ. |
| `announcement` {`text`, `level`} | A banner on every page while `text` is not empty; `level` is `info` or `warning`; at most 280 characters. A visitor can dismiss it; the dismissal is remembered in the browser for that text. |
| `maintenance` {`enabled`, `message`} | New terminals are refused for everybody but admins, with the message (or a default). Pages, files and the rest of the site keep working. The banner shows on every page. |
| `disabledLanguages` | Values of the language picker (`python`, `cpp`, ...). The picker greys them out and their new terminals are refused. Open terminals are not touched. Admins can still start them. |
| `genie` {`disabled`, `guestPerMinute`, `userPerMinute`, `disabledModels`, `defaultModel`} | `disabledModels` are model ids (`chatModels` in `server/chatmodels.go`) that are switched off: the picker and the New question dialog hide them and `handleChatProxy` answers 503 `model_unavailable` with code `model_disabled` before charging anything or calling a provider. At least one model stays on, and `defaultModel` (empty: built in) must be on; `normalize` checks both. `defaultModel` is what visitors start with and what answers a request naming no model (`defaultModelID`: the admin's choice, else GPT-4o mini, else the first model that is on). The dashboard lists the models with a note when the server has no key for one (`modelInfos`), asks for confirmation before switching one off, and puts each change in the audit log. The limits of Genie (the numbers in the next paragraph) are in the same object. Genie off: the Genie buttons are hidden and `handleChatProxy` answers 503 `GenieDisabled` (admins excepted). The rates are requests per minute, 0 meaning the built-in `GUEST_FACTOR` and `USER_FACTOR`; `utils.SetGenieRates` applies them to the cookie package's balance arithmetic. Between 0 and 60. |

**API keys** (`settings_keys.go`). The OpenAI and OpenRouter keys can be saved in the dashboard (Site settings, "API keys"): `POST /admin/keys` with `{"provider": "openai"|"openrouter", "key": "..."}`, an empty key removes the saved one, `GET /admin/keys` lists them.

- *Storage:* in the settings document (`settings.json` or MongoDB) as `secrets`, each key AES-256-GCM encrypted under a key derived from `utils.Secret()` (the server's secret, LLD 05 section 2), with the provider bound into the ciphertext so one field cannot be moved into the other. Every instance decrypts them into memory when it reads the settings, and `utils.OpenAIKey` and `utils.OpenRouterKey` prefer them to the environment's, so a change applies at once, and on other instances within the poll interval. Removing the saved key brings the environment's back.
- *Write only:* no reply of any endpoint carries a key, a part of one or the ciphertext (`reply()` drops `secrets`; the form cannot set them, a `secrets` field in a settings POST is ignored and the stored ones kept); the audit log says "OpenAI key replaced" or "removed" and nothing else. Tests (`settings_keys_test.go`) look for the key and its ciphertext in every admin reply.
- *Checking:* before saving, the server asks the provider (`GET /v1/models`, `GET /api/v1/key`, no cost). A 401 refuses the key ("Nothing was changed"); a 403 or any other answer, or no answer, saves it with a warning, because a restricted key can be refused by a check and still work for chat. A key must be 8 to 512 plain ASCII characters without spaces.
- *The secret:* `utils.Secret()`: `OPENREPL_SECRET` if set, else the one the server generated and saved in its database as `SESSION_KEY` (LLD 05 section 2). With `OPENREPL_SECRET` the secret is only in the environment, so a copy of the database or of `settings.json` cannot be decrypted. With the generated one, the ciphertext and the secret sit in the same database, so a copy of the whole database can be read; the encryption then protects against casual exposure only. Health says which case it is. If the secret later changes, the saved keys cannot be decrypted: they are ignored, the environment's keys are used, and health warns ("Dashboard keys") until they are entered again.
- *Effect on visitors:* the chat access token a page holds is derived from the OpenAI key, so a page opened before the key changed asks the visitor to reload once.
- *Workers:* the gateway sends its `OPENREPL_SECRET` in the register reply and the worker keeps it in memory (LLD 11); a worker does not read the settings, so it holds no keys.

**Genie limits.** Empty means the built-in value, which the form shows as a placeholder (`defaults` in the settings reply); every number is checked against a range by `normalize`:

| Field (`genie`) | Built in | Range | Effect |
|---|---|---|---|
| `contextEditorChars` | 12,000 | 1,000 to 50,000 | Characters of the editor's code sent with each question |
| `contextTerminalChars` | 4,000 | 200 to 20,000 | Characters of the terminal's output |
| `contextTerminalLines` | 20 | 1 to 200 | Lines read from the terminal first |
| `historyMessages` | 20 | 6 to 50 | Messages Genie keeps |
| `answerCaps` (by model id) | Luna 7,000, 4o mini 3,000, Gemma 4,000 | 500 to 16,000 | The most tokens an answer may have (`answerCap`; a visitor's own request is clamped to it) |
| `openRouterTimeoutSec` | 60 | 10 to 90 | How long OpenRouter may take before "isn't available right now" (the site's proxy gives up at about 100) |
| `openRouterHosts` | ModelRun, then CoreWeave | one or two of those two | The hosts Gemma may use, in order, and no others; `provider.only` and `order` are built from it |

**Agent mode** (the Agent mode card; LLD 07 section 3b). `agentDisabled` (the Agent mode switch is on unless an admin turns it off) and `agentOnPractice` (off) are switches; the numbers: `agentTasksPerHour` (built in 20, 1 to 200: how many tasks a signed-in user may start in an hour, admins none) and `agentMaxSteps` (built in 8, 1 to 8: the steps of a task). The audit log says "agent mode on" or "agent mode off", "agent mode on the practice page on" and "agent limits: N tasks an hour, M steps a task". `settings.js` carries `agentEnabled` (only while Genie itself is on), `agentOnPractice` and `agentMaxSteps`, never the hourly count.

The first four reach the page through `settings.js` as `genieContext` (the effective numbers), and the widget reads them when it loads; the rest are used by the proxy.

**Where the settings are kept** (the same choice as the databases, LLD 05 section 4: MongoDB, else Firestore, else the file; with Firestore the document is `settings/site` with the fields `v`, `data` and `updated`, and a save is a commit that requires the document not to have changed since it was read, or not to exist). The rest of this paragraph is written for MongoDB and holds for Firestore too, where "database" means the project's Firestore. (`settings_store.go`, `settings_sync.go`). By default the file `settings.json`. With `OPENREPL_MONGODB_URI` set (an Atlas `mongodb+srv://` URI; `OPENREPL_MONGODB_DB`, default `openrepl`) a gateway or a standalone server keeps them in MongoDB and does not write the file, so they survive a host whose disk is wiped on deploy. A worker never reads either.

- *The document:* collection `settings`, one document `{_id: "site", v: <version>, data: <the settings as JSON>, updated}`. Every other part of the site (accounts, sessions, audit log, stats) stays where it was.
- *First start:* if the collection holds nothing, this server's own `settings.json` (if it has one and it is valid) is uploaded once, with version 1; if it holds a document, that is used and the file is ignored. After that the store wins and the file is never read again.
- *Staying in step:* the server keeps a copy in memory and reads the document every 10 seconds (`settingsPollInterval`); when the version moved, the copy is replaced. So a change made through one instance reaches another within seconds.
- *Saving:* a compare-and-set on the version (`UpdateOne` with `{_id, v: base}`), so two admins, or two instances, cannot overwrite each other unseen. The dashboard sends back the version its form was filled from (`version` in the POST; the GET reply carries `store` and `version`); a stale one gets 409 "Someone else changed the settings...". An absent `version` skips that check (only the server's own copy is compared).
- *Store unreachable:* the store is the one `persist.Init` chose (LLD 05 section 4), after its 3 tries and fallbacks. If the choice was MongoDB or Firestore and it stops answering later, or the settings document cannot be read at start-up, the server runs on the built-in defaults and keeps trying (the REPLs must not go down with the database), and a save answers 503 "not reachable, nothing was saved" until a read has worked. Later outages keep the last settings. A stored document that fails `normalize` is not applied (logged); an admin can save over it. The URI's password is removed from every message (`redactURI`).
- *Health:* a "Settings store" line: file (ok), MongoDB with its version and how long ago it was read (ok), MongoDB that was reachable and is not (warning), MongoDB never reached (failure). The settings page says where the settings are stored.
- *Other databases:* with the same URI the site's own databases (sessions, feedback, blog, snippets, practice) are collections of the same MongoDB database too (LLD 05 section 4); the settings are the document `settings`/`site`.
- *Dependency:* the official `go.mongodb.org/mongo-driver` v1.12.1 and its few dependencies are copied into `src/` (GOPATH mode, tests and test data left out). Tests: `settings_sync_test.go` with an in-memory store; `settings_mongo_test.go` against a real MongoDB when `OPENREPL_TEST_MONGODB_URI` is set (for example `docker run --network container:<devbox> mongo:7` and `mongodb://127.0.0.1:27017`).

`normalize` checks a POST and says what is wrong (400); a settings file that fails the same check is ignored with a log line. Visitors read the public part through `/settings.js` (`publicSettings`): never the Genie rates.

**Where the switches are applied.** `wrapControls` sits in front of the whole handler tree, so in gateway mode it is in front of the router and covers the terminals of workers too. For a WebSocket upgrade on a terminal route (`Server.terminals`) it checks maintenance and the language; a refusal completes the handshake and closes at once with the reason `site notice: <text>`. `webtty.ts` turns that reason into the close kind `notice`, and the page shows the text in the terminal banner (`11-terminal-state.js`). The same prefix is used when an admin ends a session (`gateway.EndedReason`). A worker applies the same two rules to the people who open its own port (LLD 11 section 6.2b), from the config the gateway hands it, and never exempts an admin there.

## 4. People and sessions

**Admin accounts** (`admin_accounts.go`). There are two kinds. *Owners* are the addresses in `OPENREPL_ADMIN_EMAILS` (or the settings file's `user.email`): fixed by whoever runs the server, never changed from the dashboard. *Added admins* are addresses an owner added in the dashboard (Site settings, "Admin accounts"); they are kept in the settings document (`admins`, lower case, at most 50, checked by `normalize`) and applied on every instance as it reads the settings (`utils.SetExtraAdmins`). An added admin can use all of the dashboard except the list itself: `POST /admin/admins` answers 403 unless the caller is an owner (`Server.isOwner`; tests replace it with `Server.admin.owner`), so nobody can promote anybody, or remove itself or an owner. Adding an owner or an existing admin, removing an owner, a bad address or an unknown action are refused; the settings form cannot touch the list (a POST's `admins` is ignored). Removing an admin takes effect on their next request, because `IsUserAdmin` reads the lists every time. Both kinds are protected like admins elsewhere: the accounts list marks them and refuses to block them ("Remove it from the admin list first"). Every change is in the audit log with the address. Whether an account really owns the address it signs in with is decided by the sign-in provider, as it was for owners before.

**Accounts.** `user.ListAccounts` reads the user database with a cursor (`cachedb.Database.Each`), skipping records that are not profiles (the cookie secret, worker pins, block flags). *Sign out everywhere* empties the account's session map: the browsers keep their cookies, but the sessions are no longer known, so every check treats them as signed out. *Block* sets a flag (`blocked:<uid>` in the same database, mirrored in memory) and signs the account out; `IsSessionExpired` then returns true for it, `UpdateAndStoreSessionData` refuses to store a new session (`ErrBlocked`) and `/login` answers 403. An admin account, or your own, cannot be blocked.

**Feedback.** The record gains `Read`. Deleting uses the existing `deleteFeedbackData`. The CSV export puts a quote in front of any cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return, because visitors write every field.

**Ending a session** (`Router.EndSession`). The router keeps a cancel function for every open terminal of a session (`trackTerminal`, from `execute`). Ending cancels them and releases the session's execution context. For a worker, cancelling the request makes the reverse proxy close the connection to the worker, and `closeGuard` ends the browser's WebSocket with `EndedReason`. A local terminal watches its request's context in `processWSConn` and returns `errSessionEnded`. Files are not touched, and the visitor is placed again by the next request, on the same node if they are signed in (their pin is kept).

**Moving a session** (`Router.MoveSession`) needs workspace sync (LLD 12), because the new node is sent the gateway's copy of the home (`PrepareHome`) and the old node's copy is dropped (`OnMoved`). It refuses a target that is the current node, unknown, or not `ONLINE`. The terminals close, the context is recreated on the target, and a signed-in user's pin follows. It is not counted as a pick. A file changed in the last moments may not have reached the gateway yet; the dashboard says so before it moves.

## 5. Looking at the server

**Parameters** (`/admin/gateway`, `paramsReply`) are built field by field. A credential, token, API key or password is never in it, only whether one is set; `TestGatewayParametersNeverHoldSecrets` checks that. It also says where the settings came from (environment, env file, git-config file), which Firebase project signs people in, how many admins there are, the gateway options and the tunnel host key fingerprint a worker needs, and runtime figures. The dashboard's "add a worker" box builds the worker's command line from these (the token is left as a placeholder).

**Health** (`health()`) checks the data directory and the disk space of data and homes (warning below 15% free, failure below 5%), the log directory, the databases, the sandbox (`containers.Status`: how many REPLs have a cgroup), that someone is an admin, which Firebase project is in use (a warning for a dev server on the production project), the OpenAI key, the OpenRouter key (optional), `dev` mode, maintenance mode, and in gateway mode each worker's state, workspace sync and the clock difference to each worker (a warning above 2 seconds). It returns the worst status and a list.

**Log** (`/admin/logs`) reads at most the last megabyte of `gotty.log` and masks, in each line, headers (`Authorization`, `Cookie`, `X-Openrepl-*`), bearer and basic credentials, `key=value` pairs that name a secret, JSON web tokens, any other long run of token characters, and email addresses (kept as `v***@example.com`). A search matches the masked line.

The dashboard's own reads (GET under `/admin/` that succeed) are left out of the request log, so a dashboard left open does not fill it. Refused reads and every change are logged.

## 6. Usage numbers

`statsStore` counts, where `wrapControls` lets a terminal through, one terminal per language per day, and the visitors of the day. A visitor is a keyed hash of the account or, for a guest, the address, different on each day, so one visitor who opens several terminals counts once. Only today keeps these hashes (to count across a restart); older days keep numbers only, 60 days in all. The file is `admin-stats.json`, saved at most every 30 seconds. A gateway counts the terminals of its workers; a worker's own port is not counted.

### OpenRouter models

The three built-in models (GPT-6 Luna, GPT-4o mini, Gemma 4 31B) are fixed: they can be switched off, not removed. Under Site settings, *OpenRouter models* lists the models an admin added, each with a switch, a name, an answer size, room for thinking, "its host takes JSON mode", the providers allowed to answer and a "free tier" mark; *Add model* takes an OpenRouter id (`author/model-name`, lower case, optionally `:free`) and adds it off, *Remove* takes it out after a question. Nothing else in the code has to change to use a new model.

The list is `GenieSettings.CustomModels`, part of the site settings document, so it is stored wherever the settings are: `settings.json`, or MongoDB or Firestore when one is configured (LLD 05), and every instance reads the same list. `nil` (never saved) shows the two free models that were tried (Nemotron 3 Super and North mini code), off; an admin who removes all of them keeps an empty list. `normalize` checks the ids (the OpenRouter pattern, not a built-in id, no duplicates), the name, the sizes and the providers, at most 20 models, and that at least one model stays on; the default model may be any model that is on. A page from before the list existed does not send it, and then the stored list is kept. Changes go to the audit log ("OpenRouter model qwen/qwen3-coder:free added (off)", "... switched on", "... removed").

Visitors: `settings.js` carries the added models that are on (`customModels`), and `model-choice.js` adds them to the picker under OpenRouter, with a "Free" tag. A model that is off is not listed and `handleChatProxy` refuses it like any switched-off model. A free model's provider may keep what is sent, so the dashboard says so and the privacy page does too; a model with no providers named is routed by OpenRouter.

### Languages per node

The site-wide switch turns a language off for everybody. The Languages card in a node's drawer (*Workers*, then the node) decides per node, for every language that has a terminal (JavaScript runs in the browser and is not listed): **Default** follows what the node declares (`--worker-languages`; a node that did not list its languages runs everything), **Off** refuses new terminals of it although the node can run it, **On** takes it although the node did not declare it, for a language installed after the worker started. Open terminals keep running. The choices are saved with the site settings (`SiteSettings.NodeLanguages`, node id to `{off, on}`), by `/admin/workers/<id>/languages` only: the Settings page does not send them and keeps what is stored. They are audited ("worker pi-1: rappel switched off").

How a choice takes effect (`server/node_languages.go`, `admin_node_languages.go`):

- *New terminals:* the gateway's router asks `Config.NodeLanguage` for the rule of the language on the session's node (`gateway/router.go`). Off closes the terminal's WebSocket with a notice the page shows ("This language is switched off on your execution node for now"), as the site-wide switch does; an admin is let through. On skips the check against the node's declared languages. Otherwise the declared list decides, as before (the same text, "this language is not available on your execution node", now as a notice).
- *The picker:* `/settings.js` is served by the gateway, so it asks `Router.NodeOf` (a lookup of the visitor's session, nothing is created) which node the visitor is on, and lists as off what that node refuses: the site-wide languages, the node's Off, and what it did not declare unless On. A visitor whose node is not known yet gets the site's list.
- *The worker itself:* its `WorkerConfig` carries the same list (`tunnel.ServerConfig.WorkerConfig`, per worker, with its own revision), for the people who open the worker's own port.
- *The gateway's own node* (`local`) has the same card.

The card also shows what the node's host says about tracing programs. Each node asks once, when it starts, with `openrepl-ptrace-probe` (LLD 04 section 2); a worker reports it in its registration (`RegisterRequest.Ptrace`), the gateway for itself. A host that cannot trace shows a "no ptrace" pill in the table and in the drawer, a warning in the card, and "needs ptrace" next to the assembly REPL (`rappel`), which cannot work there. It is only information: an admin decides whether to switch the language off.

The drawer is redrawn on every refresh; the Languages card is created once per opened drawer and not redrawn with it, so an open menu is not closed.

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
