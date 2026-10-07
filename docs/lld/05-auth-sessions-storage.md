# LLD 05: Auth, sessions and storage

Scope: `src/user/{user,util}.go`, `src/cookie/cookie.go`, `src/cachedb/cachedb.go`, `src/server/{db,blog_db}.go`, sign-in code in `src/js/src/page/05-auth.js` (bundled into `scribbler.js`).

## 1. Sign-in flow

Identity comes from **Firebase Authentication**, which runs in the browser through FirebaseUI (Google, GitHub and email, shown in the sign-in dialog; LLD 06). The Go server keeps its own session, keyed by the Firebase `uid`.

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser (scribbler.js)
    participant FA as Firebase Auth
    participant S as gotty /login
    participant DB as user_sessions.db
    B->>S: GET /login (on every page load)
    S->>S: GetOrUpdateHomeDir (guest dir + cleanup job if not logged in)
    S-->>B: UserSession JSON {uid, sessionID, expirationTime, loggedIn}
    alt not logged in and user clicks Login
        B->>FA: FirebaseUI in the sign-in dialog (Google, GitHub or email)
        FA-->>B: authResult (user, stsTokenManager)
        alt email not verified
            B->>FA: sendEmailVerification() (new users) and show the "verify your email" panel
        else verified
            B->>S: POST /login  body = authResult JSON
            S->>S: session.Update(user): uid, sessionID = accessToken, expirationTime
            S->>DB: UpdateAndStoreSessionData(uid, sessionID, session)
            S-->>B: Set-Cookie user-session (MaxAge = token expiry)
            B->>B: location.reload()
        end
    end
    B->>S: POST /logout → Delete_SessionCookie + delete sessionID from profile
```

- A user can hold several sessions (browsers or devices), stored in `UserProfile.SessionMap[sessionID]`.
- `IsSessionExpired(uid, sessionID)` treats a missing or expired entry as logged out, and starts `PurgeExpiredSessionData` in the background.
- The session lifetime follows the Firebase ID token expiry (`stsTokenManager.expirationTime`), which is usually about 1 hour. There is no server-side refresh. The browser signs in again when the session ends.
- `POST /login` does not verify the Firebase ID token: it trusts the `uid` in the body (launch blocker B2 in the UI tracker). Everything keyed by `uid` inherits that gap: the workspace, the admin check (LLD 01) and practice progress (section 4).

## 2. The `user-session` cookie

This is a gorilla `sessions.CookieStore`. It is HMAC-signed with the cookie key (`user.CookieKey`, set by `user.ensureServerSecret`). When `OPENREPL_SECRET` is set the key is derived from it, `HMAC-SHA256(secret, "openrepl/cookie/v1")`: the same after every restart and on every instance that has the variable, and nothing about it is saved in the database (a copy left there by an earlier version is removed). When it is not set, the key is `SESSION_KEY` in `user_sessions.db`: a random 128-bit prime made and saved once, and used from then on; it is also the secret `utils.Secret()` returns. The dashboard's saved API keys are encrypted under a key derived from `utils.Secret()` with another label (LLD 13 section 3), so the two uses share a secret but not key material. If the key changes, everybody is signed out once; if the secret changes, the dashboard's saved keys have to be entered again. It is not encrypted, and its path is `/`.

| Key | Set by | Meaning |
|---|---|---|
| `uid`, `sessionID`, `loggedIn`, `expirationTime` | `Set_SessionCookie` (`/login`) | Session identity. |
| `homedir` | `Set_SessionCookie`, `GetOrUpdateHomeDir` | The workspace path for this browser. |
| `OPENAI_REQUEST_COUNT`, `OPENAI_REQUEST_LAST_ACCESS` | chat proxy | Token-bucket balance and time of last refill (LLD 07). |
| `ACCESS_TOKEN`, `ACCESS_SECRET` | `/config.js` | Per-session chat-proxy access token material (LLD 07). |

`MaxAge` is the remaining token lifetime for signed-in users. For guests it is refreshed to `DEADLINE_MINUTES × 60` (3600 s) on each homedir lookup (`UpdateGuestSessionCookieAge`).

## 3. Home directory resolution

`cookie.GetOrUpdateHomeDir(rw, req, uid)` is called by most handlers and by every WebSocket connect:

```mermaid
flowchart TD
    A["request"] --> J{"form has jid<br/>that decodes to a PID?"}
    J -- yes --> JW["return cwd of that PID<br/>(/proc/pid/cwd)"]
    J -- no --> H{"form has homedir=?"}
    H -- yes --> HW["return that value<br/>(used by shared-session viewers)"]
    H -- no --> L{"cookie loggedIn<br/>and not expired?"}
    L -- no --> G["uid := '' (guest)"]
    L -- yes --> K
    G --> K{"cookie homedir set<br/>and cookie uid == uid?"}
    K -- yes --> R["reuse cookie homedir"]
    K -- no --> N["user.GetHomeDir(uid)"]
    N --> NG["guest: ioutil.TempDir('/tmp/home/', 'guest-')"]
    N --> NU["user: /tmp/home/ID<br/>ID = lower(first 8 chars of<br/>4-char first-name prefix, 0-padded, + uid)"]
    R --> S["store in cookie, refresh guest MaxAge"]
    NG --> S
    NU --> S
```

A signed-in user's directory name is derived from their profile name and `uid`, so it is the same on every device.

## 4. Persistent stores

All server-side stores are key-value databases behind one small interface, `persist.Store` (`src/persist`: `Store`, `Fetch`, `Delete`, `Commit`, `Rollback`, `Each`, `Close`). By default each is an embedded **UnQLite** file (`github.com/nobonobo/unqlitego`) under `utils.GOTTY_PATH = /opt/gotty`.

**Which store (`persist.Init`).** `gotty` calls `persist.Init` once, in `app.Action`, when the mode is known and before any database is opened. It is the only place where the choice is tested: (1) if `OPENREPL_MONGODB_URI` is set, MongoDB is connected and pinged, up to 3 tries with a second between them (5 seconds each); (2) if that fails, or the URI is not set, and Firestore is configured (`OPENREPL_FIRESTORE_CREDENTIALS`, or the emulator), one read of the project's Firestore, again up to 3 tries; (3) if that fails or Firestore is not configured, the UnQLite files. A worker tests nothing and keeps files. The choice is final for the life of the process and is written to the log (`persist: the databases are stored in MongoDB, database X` / `... Firestore, project X` / `... files under /opt/gotty (unqlite)`), each failed try is logged with its reason (the URI's password removed), and so is what was given up (`persist: MongoDB did not answer after 3 tries (...)`). The health page shows a warning "Database choice" when MongoDB or Firestore was asked for and the test fell back. The settings (LLD 13) follow the same choice. After `Init` the rest of the start-up runs as it always did; a database that stops answering later returns errors from its calls, and the settings keep their last values (LLD 13).

**Firestore** (`persist/firestore.go`). The server talks to Firestore's REST API with the standard library only (the official client would bring gRPC and a long list of modules into this GOPATH build) and signs in as a service account: a JSON key, given as the JSON, its base64, or a file path in `OPENREPL_FIRESTORE_CREDENTIALS`, exchanged for an OAuth token (a signed JWT with the scope `datastore`, renewed a minute before it ends). The web config of a Firebase project cannot be used for this: it carries no secret. `FIRESTORE_EMULATOR_HOST=host:port` talks to the emulator without a key (tests, local runs); `OPENREPL_FIRESTORE_PROJECT` names the project when the key does not. Each database is a collection `kv_<name>` in the default database, one document per key (`v` bytes, `t` time). A key that is not a valid document id (empty, with `/`, `.`/`..`, `__x__`, longer than 700 bytes, not UTF-8, or starting with `~`) is stored as `~` plus its base64. Listing pages with a query that starts after the last id. The first start copies an existing UnQLite file into an empty collection, once, as for MongoDB. A document is at most 1 MiB. **Keep the security rules of these collections closed** (`kv_*` and `settings`: `allow read, write: if false`): the service account bypasses rules, and the browser's Firestore access (the practice page) must not be able to read sessions or the dashboard's encrypted keys.

**MongoDB instead of the files.** With `OPENREPL_MONGODB_URI` set (and `OPENREPL_MONGODB_DB`, default `openrepl`), a gateway or a standalone server keeps every one of the databases below in MongoDB: each file is a collection named after it (`user_sessions.db` is `kv_user_sessions`), with a record `{_id: <key>, v: <value>, t: <time>}` (the key is text, binary only when it is not valid UTF-8). `gotty` decides this in `app.Action`, once the mode is known (`persist.Configure(mode != worker)`), so a worker always keeps files, whatever its environment holds. The cache (`cachedb`) sits on top of either, unchanged. Differences:

- *Commit and Rollback* do nothing in MongoDB: a write is permanent when `Store` returns.
- *The first start* copies the file's records into a collection that is empty, once (the session secret included, so nobody is signed out); afterwards MongoDB wins and the file is not read again. The file is left where it is.
- *Reaching MongoDB* is tested once, by `persist.Init` (above), not when a database is opened. Every later call has an 8-second limit and returns its error.
- *A missing key* gives `persist.ErrNotFound` from `Fetch` and `Delete`.
- *Several instances* sharing one database each have their own cache; a record written by one is seen by the others when their cached copy is evicted or the key is not cached yet.
- Tests: `persist/persist_test.go` (the same checks on both backends, the copy, a worker, an unreachable server) run against a real MongoDB when `OPENREPL_TEST_MONGODB_URI` is set.

| File | Wrapper | Key → value | Used by |
|---|---|---|---|
| `user_sessions.db` | `cachedb.Database` (UnQLite, or MongoDB, + 15 MB `freecache`, write-through, read-through) | `SESSION_KEY` → cookie HMAC key; `<uid>` → `UserProfile` JSON; `worker-pin:<uid>` → the execution node that holds the user's workspace (written by a gateway, `user/pin.go`, LLD 11); `blocked:<uid>` → `1` or `0`, set by an admin (`user/admin.go`, LLD 13) | `user`, `cookie` |
| `feedback.db` | raw UnQLite (or MongoDB) | `<UnixNano timestamp>` → `{Name, Email, Message, Read}` | `/feedback` |
| `blog.db` | raw UnQLite (or MongoDB) | `<blog name>` → `BlogPost` JSON | `/blog` |
| `snippets.db` | raw UnQLite (or MongoDB), guarded by a mutex | `<8-character id>` → `snippet` JSON | `/snippet`, `/s/<id>` (LLD 01) |
| `practice.db` | raw UnQLite (or MongoDB), guarded by a mutex | `u:<uid>` → `practiceDoc` JSON | `/practice/progress` |
| `jobfile` | `encoding/gob` | `map[name]*Job{Name, ExpirationTime}` | `utils.GottyJobs` (written on shutdown, read and deleted on start) |
| `.gitconfig` | text (read-only at runtime), the fallback for the `OPENREPL_*` environment variables | admin email, OpenAI key, allowed host | LLD 01, 07 |
| `settings.json` (or MongoDB when `OPENREPL_MONGODB_URI` is set, LLD 13 section 3) | JSON (`server/settings.go`), read once and cached, written atomically (tmp file + rename) | `SiteSettings`: colour of the day, announcement, maintenance, switched-off languages, Genie switch and rates (LLD 13); a missing file means every switch is off | `/settings.js`, `/admin` |
| `admin-audit.jsonl` | one JSON object per line, appended, mode 0600; the newest 500 are kept in memory | `{time, admin, action, detail, from}` for every change an admin made | `/admin/audit` (LLD 13) |
| `admin-stats.json` | JSON, rewritten at most every 30 seconds, mode 0600 | per day: visitors, terminals by language, and for today only the keyed hashes of the visitors seen; 60 days | `/admin/stats` (LLD 13) |

Every write calls `Commit()` right away. Listing uses UnQLite cursors (`FetchFeedbackDataMap`, `FetchBlogDataMap`).

### Data models

```go
// user/user.go
type User        struct { Uid, Name(displayName), Email, PhotoURL string; StsTokenManager{AccessToken, RefreshToken string; ExpirationTime int64} }
type UserSession struct { Uid, SessionID string; ExpirationTime int64; LoggedIn bool; User *User /* only on the wire */ }
type UserProfile struct { Uid, Name, Email, PhotoURL string; SessionMap map[string]UserSession }

// server/db.go, server/blog_db.go
type feedback struct { Name, Email, Message string }
type BlogPost struct { Name, Title, Desc, Content string; Lastupdated time.Time }

// server/snippets.go: ids use 57 unambiguous letters and digits (no 0/O, 1/l/I)
type snippet struct { Lang, Code string; Created int64 /* unix seconds */ }

// server/practice.go
type practiceState struct { Done, Del bool; T int64 /* ms of the change */ }
type practiceDoc   struct { Questions map[string]json.RawMessage; State map[string]practiceState; Updated int64 }
```

**Practice merge.** The browser sends its whole list; `mergePractice` combines it with the stored copy. For each id the newer mark wins (larger `T`; on a tie a deletion wins), and the newer copy of a question wins (larger of its `updated` and `added`). A question whose mark is `Del` is dropped, and the tombstone stays so another browser can't bring it back. Past 200 questions the oldest are dropped and marked deleted; past 1,000 marks the oldest tombstones and marks of missing questions go first. Question bodies are stored as the browser sent them (each under 64 KB).

`/profile?q=json` returns `UserProfile` with `SessionMap` set to nil.

## 5. Browser-side state

| Store | Key | Purpose |
|---|---|---|
| localStorage | `theme` | `light` or `dark` once someone uses the theme switch; absent means follow the system setting (`js/theme.js`). |
| localStorage | `files-panel` | `open` or `closed` for the Files panel in the workspace. |
| localStorage | `editor-theme` | The Ace theme picked in the status bar; absent means OpenREPL Dark (`ace/theme/openrepl_dark`). |
| localStorage | `questions`, `bookmarkedRows`, `topic`, `difficultyLevel`, `temperature` | Practice mode: generated questions (latest 100) with per-language code, the ids marked done (kept for older code), the last topic and level, LLM temperature. |
| localStorage | `practiceState` | `{id: {done, t}}` or `{id: {del: true, t}}`: done marks and deletions with the time of each change, for merging (`js/practice-store.js`). Created from `bookmarkedRows` on first use. |
| localStorage | `practiceFilters` | Status, topic, level and sort chosen on the practice list. |
| localStorage | theme key (`LS_THEME_KEY`) | Selected editor theme. |
| cookie | `Session` | Count of open terminal connections in this browser, capped at 3 (LLD 02). |
| Firebase RTDB | `openrepl/<dbpath>/<termId>/…` | Live-sharing event log (LLD 06). |
| Firebase RTDB | `chat-list/<dbpath>/…` | Genie chat history for the session (LLD 07). |

Practice questions and done marks live in the browser and, for a signed-in user, in `practice.db` too. `PracticeStore.init()` (practice list and practice editor) reads `/practice/progress`, merges it in, then sends the browser's copy to be merged on the server. Later changes are sent 1.5 s after they happen; a change left when the page closes is sent with `keepalive` when under 60 KB, otherwise on the next visit. Guests keep everything in the browser, as before.
