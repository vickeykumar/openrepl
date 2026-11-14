# 17. Preview pane

Status: phases 1 to 4 of `docs/preview-pane-design.md` are on `development` (commits 5286c3d5, 116cab4b and 6805fb44 after the rebase of 2026-11-13), not on `master`, not deployed.
Packages `livepreview` (src/livepreview), `server/preview.go`, page `js/src/page/27-preview.js`, tests
`e2e/tests/preview.spec.js`.

## 1. What it does

A web page of the workspace, or a program of the workspace that listens on a port, is shown next to the editor in a
sandboxed frame. The pane takes the terminal's place while it is open (the terminal and its shell stay alive); Run
closes it, so that the result appears in the terminal; an `.html` file is shown instead of run.

## 2. Why a token

The frame is `sandbox="allow-scripts allow-forms allow-popups allow-modals allow-downloads"` **without**
`allow-same-origin`. The page then has an origin of its own that is nothing: it cannot read this site's cookies or
`localStorage` or call its APIs as the visitor. The browser also sends none of the site's cookies with the frame's
requests (its CSS, scripts, `fetch`), so the cookie cannot say whose workspace is asked for. The editor page, which has
its cookies, asks `GET /preview_token` and the pane loads `/preview/<token>/...`.

`livepreview.NewToken` makes `base64url({"h": home, "e": end}) + "." + hmac-sha256(secret, "openrepl-preview\0" + payload)`,
valid 1 hour for a guest and 3 hours for a signed-in user by default, or the admin's settings `previewLinkGuestMinutes` and `previewLinkUserMinutes` (Settings, Preview card; 10 to 1440 minutes; `previewTokenLife` in `server/preview.go`); the page renews it 5 minutes before the end. A wrong or ended token gets 410 and "This preview has ended."
A token names one workspace and cannot be made without the server's secret (`utils.Secret()`).

## 3. Routes

| Route | Does |
|---|---|
| `GET /preview_token` | A token for the caller's own workspace (the identity of `requestIdentity`, so a collaborator of a shared session gets the master's). 403 when the admin switched the preview off. |
| `GET /preview_ports` | `{ports: [...], isolated: bool}`: the ports the workspace's programs listen on. |
| `/preview/<token>/f/<path>` | A file of the workspace, read-only (`livepreview.ServeFile`). |
| `/preview/<token>/p/<port>/<path>` | A request passed to the program that listens on `<port>` (`livepreview.Proxy`), WebSocket upgrades and streamed answers included. Ports from 1024 up. |

### 3.1 Files (phase 1)

`ServeFile`: GET, HEAD, OPTIONS only; the path is cleaned as a rooted path and resolved with `EvalSymlinks`, and it must
stay inside the workspace's resolved folder (not through `..`, not through a link that leads out; a sibling folder that
starts with the same name does not count); a folder shows its `index.html` (a slash is added first, so that relative links
work); 50 MB at most. Every answer carries `SandboxHeaders`: `Content-Security-Policy: sandbox ...` (so the page is
sandboxed also when opened in a tab of its own), `nosniff`, `no-store` (an edit always shows), `noindex`, CORS `*` without
credentials (the page's requests to its own server are cross-origin), `Referrer-Policy: no-referrer`.

### 3.2 Programs (phase 2)

`previewDialer` reaches the workspace's own network and nothing else, by what the REPL has:

1. a network namespace of its own (a different `/proc/<pid>/ns/net` from the server's): `livepreview.DialIn` joins it in a
   thread that is never given back to the runtime, makes the socket, and connects to `127.0.0.1:<port>`;
2. no namespace, but the network emulation is active (LLD 16): the abstract unix socket
   `@openrepl-ne/<workspace>/t/<port>` (`netemu.Name`), which is where the REPL's "TCP" socket really is;
3. neither (the machine's network is shared): refused with 501-style text. A port of the server, or of another visitor,
   would otherwise be reachable.

The REPL's process is found by `livepreview.FindPID(home)`: a process whose environment has `HOME=<home>` (the server sets
it) or whose command line has `--workspace <home>` (the landlock helper, until it becomes the REPL); the lowest id wins;
the answer is kept 5 seconds. The ports come from the same network: `/proc/<pid>/net/tcp` and `tcp6` (state 0A) in case 1,
`/proc/net/unix` (abstract names of the workspace with the listening flag) in case 2.

`Proxy` (net/http/httputil): the request goes to `127.0.0.1:<port>` with `Host` set to it; the site's cookies,
`Authorization`, `Referer` and an `Origin: null` are dropped; the answer is asked for uncompressed (the site's gzip layer
compresses it again); the body is at most 10 MB; the program has 30 seconds to start answering. In the answer, the program's
`Set-Cookie`, CSP, `X-Frame-Options` and HSTS are removed and the sandbox headers win. A `Location` that starts with `/` or
names the program gets the preview's prefix.

**Cookies.** A sandboxed page keeps none, so the server keeps the program's cookies per workspace and port (`Jar`: name and
value only, 50 per program, 256 programs) and sends them on the next request. A login by server cookie (Flask-Login,
Express sessions) then works; `localStorage` of the previewed page does not.

An unreachable program is answered in words inside the frame: "Nothing is listening on port N" (502), "Nothing is running
yet" (no process of the workspace), "The program did not answer in time" (504), "The request is too large" (413).

### 3.3 Absolute paths, WebSockets, dev servers (phase 3)

A program is served under `/preview/<token>/p/<port>/` but its pages are written for `/`. The `Referer` cannot tell which
preview a request belongs to (a page without an origin sends none that does), so three layers:

1. `RewriteHTML` puts the prefix on `src`, `href`, `action`, `poster`, `data` and `formaction` addresses that start with
   one `/` (not `//host`, not `#`, not relative, not ones that have the prefix), in HTML answers up to 8 MB;
2. a script added at the top of the head puts the prefix on what `fetch`, `XMLHttpRequest`, `WebSocket` and `EventSource`
   are given (an address that starts with `/` or names this host); it also passes Ctrl+` up to the editor;
3. nothing else: an address that gets through both reaches this site, which does not have it.

WebSocket upgrades and server-sent events pass through (`FlushInterval: -1`). The pane lists start lines for Python, Flask,
Node, Vite, Next.js and Angular ("Type it" puts the line in the terminal); they are examples and the options of each
framework must be checked. Not built: keep-alive pings on the proxied WebSocket (the proxy copies bytes and does not read
frames), so a connection that is idle for minutes may be dropped by an edge that drops quiet connections.

### 3.4 Auto-detect (phase 4)

The page asks `/preview_ports` when it opens, every 8 seconds while it is in view and the pane is closed, when it comes
back into view, and at 2, 4, 8, 15, 30 and 60 seconds after Run. A port that was not there before gets one notice
(a dark card with a green dot, "Your program is listening on port N" and a lime Open preview button, as on the "What the Preview says in each state" board, with Not now and Don't ask again; the last is remembered per workspace
in `localStorage`, `preview-noask:<home>`). While a program listens, the Preview button shows a dot and is visible for any
file. The answer is a few bytes read from the server's own tables.

## 4. Admin switch

Site settings `previewOff` and `previewGuestsOff` (Settings page, "Preview" card; change log lines "preview off" and
"preview for guests off"). Off: the button is hidden and the token and port routes answer 403. Existing tokens of a
workspace also stop (the preview route checks the setting on every request; a guest is a home named `guest-...`).

## 5. Limits that are in place

Token 1 hour for a guest and 3 hours for a signed-in user (the admin can set 10 minutes to a day for each); ports 1024 to 65535; request body 10 MB; answer headers within 30 seconds; static files 50 MB; HTML rewrite
8 MB; cookie jar bounds; no preview where the machine's network is shared. Not built: a limit of requests per minute or of
open connections per workspace.

## 6. File types besides .html (added 2026-11-12)

The Preview button shows for `.html`, Markdown (`.md`, `.markdown`), pictures (`.png .jpg .jpeg .gif .webp .bmp .ico .svg`) and
`.pdf`; `previewKind` in `27-preview.js` decides from the name. The route is the one of a file (`/preview/<token>/f/<path>`)
with a parameter: `?render=md` is the file as a page the server writes (`livepreview/render.go`), `?render=image` a page that
shows the picture. A PDF has no parameter.

- **Markdown** is rendered by the server (no library: headings, paragraphs, emphasis, code, quotes, lists, tables, rules,
  links, pictures). Every character is escaped first, so a tag in the file is shown as text; a link goes only to http, https,
  mailto, an anchor or a path of the workspace (a path to a Markdown file keeps `?render=md`), a picture only to http, https
  or a path; and the page carries `sandbox allow-popups allow-downloads; default-src 'none'; style-src 'unsafe-inline'; img-src ...`,
  so nothing runs even if the renderer had a hole. A file over 2 MB (`MaxMarkdown`) is refused with a message. Code blocks are
  plain, in a box; nested lists are shown flat.
- **Pictures** are drawn by a small page with no script (`ImagePage`): fit to the pane, a click (a checkbox and its label) for its
  own size. A vector picture is an `<img>`, so a script in it never runs.
- **PDF** is sent as `application/pdf` with `nosniff` and without the sandbox header, in a frame of its own
  (`#preview-pdf-frame`, no `sandbox` attribute): the browser's PDF viewer does not work in a sandboxed frame, and a PDF is not a
  page. The viewer itself was not tested (headless Chromium has none); the headers and the frame are.
- **The language list** (`#select-lang`) has Markdown (a real editor mode) and a group "Shown, not edited" with Image and PDF,
  disabled, which only a file sets (`changelangbyselectednode` in `07-file-browser.js`; `06-editor.js` ignores them, so nothing
  is sent to the editor or the session). Opening a picture (not an `.svg`, which is text) or a PDF reads nothing: the viewer shows a
  card (`openMedia` in `24-large-file-viewer.js`) with Open in Preview (the Preview opens on its own), View as hex and Download. The
  Preview button follows the file the card shows (`fileOf`).

## 7. What is not built, and what is not tested

- **Gateway and workers (LLD 11).** A preview request carries no cookie, so the gateway cannot find the worker that runs
  the session. The token should name the worker; it does not yet. On a gateway the preview works only for sessions the
  gateway itself runs.
- **Case 1 (a namespace of its own)** is tested by `netns_linux_test.go`, which makes a namespace with `unshare -n`, starts a server in it, reads its port from `/proc/<pid>/net/tcp` and reaches it with `DialIn` (it runs as root only and is skipped otherwise, so CI, which runs the Go tests as an ordinary user, skips it; it passed in the privileged dev container). The browser tests ran where the network emulation is active (case 2): the REPL in the privileged dev container and in the CI image uses it.
- Not built from the design's extras: Console and Requests tabs, the divider, back and forward, tabs in the pane,
  the phone layout, keyboard shortcuts (Ctrl+Shift+V is "paste as text" in browsers and
  must be chosen again), a first-use hint, a pop-out window.
- The browser checks of design item 8 (Flask, Express and Vite in Chrome, Firefox and Safari) were not done; the tests ran
  in Chromium only, with `python3 -m http.server`.
- The design's board 4 (Run slides the terminal up as a drawer under the editor, the Preview stays) is not built: Run
  closes the pane.

## 8. Feature list (2026-11-13)

What a visitor can do, in one place. Screens of each feature, and how the pieces fit together, are on two canvases (private, the
owner can open them): features and flows https://claude.ai/artifact/AD4g5DsptGo3knQRDUGbUc, screens
https://claude.ai/artifact/Rgg2nwieVn3nryv5dAaSKo.

| Area | Feature | Where |
|---|---|---|
| Files | An `.html` page shown beside the editor, with its CSS, scripts and images, reloaded on save | 3.1 |
| Files | Markdown rendered by the server (2 MB at most), pictures, PDF in the browser's viewer, and the card with Open in Preview, View as hex and Download | 6 |
| Files | Language list: Markdown, and Image and PDF under "Shown, not edited" | 6 |
| Programs | A program on a port from 1024 up (`python3 -m http.server`, Flask, Node, Vite, Next.js, Angular), with WebSockets, streaming, cookie logins and absolute links | 3.2, 3.3 |
| Programs | The notice "Your program is listening on port N" (Open preview, Not now, Don't ask again) and the dot on the button | 3.4 |
| Programs | The Ports list with start lines and "Type it" (the line goes to the terminal, Enter is not pressed) | 3.3 |
| Pane | Takes the terminal's place; the shell stays alive; Run closes it; Hide / Show of the terminal | 1 |
| Pane | Address bar, Phone / Tablet / Laptop sizes, reload, close | 1 |
| Safety | Sandboxed frame without `allow-same-origin`, signed token of 2 hours, read-only files inside the workspace, the program reached only through the workspace's own network | 2, 3 |
| Admin | "Preview" card: off for everyone, or off for guests; tokens already issued stop at once | 4 |
| Messages | Words inside the frame: "Nothing is listening on port N", "Nothing is running yet", "The program did not answer in time", "This preview has ended." | 3.2 |

### 8.1 When the Preview button is shown

`syncButton` in `27-preview.js`: hidden when the admin switched the preview off; otherwise shown when the open file is an `.html`,
Markdown, picture or PDF file (`previewKind`), or when `/preview_ports` lists a port (the button then has a dot), or while the pane is
open. With nothing to show the button is hidden and the command menu has "Open the preview" (`palette.js`, shown when `Preview.available()` and the pane is closed), which opens the pane on its Ports list. A server where the workspace has neither a network of its own nor the network emulation lists no ports (3.2, case 3), so for a
program the button never shows there.

### 8.2 On a gateway the token and port calls had to be execution-bound (found and fixed 2026-11-13)

On a gateway, only `ws`, `ws_*` and `upload_file` are "execution-bound" (`gateway.IsExecutionBound`): the gateway chooses the node and
the workspace for them. `/preview_token` and `/preview_ports` went to the site alone and named the workspace of the cookie, while the
terminal and the file browser named the workspace the gateway had chosen. On the dev container (a gateway that runs the session
itself) the terminal ran in `guest-582489444d` and the Preview asked about `guest-4268386744`, so `/preview_ports` answered
`{"isolated":true,"ports":[]}` and the button did not show for a program that was listening. Both routes are now execution-bound
(`TestTheCallsOfThePreviewAreExecutionBound`). A guest on a gateway needs the rebuilt binary. Still open on a gateway: the frame's
own route `/preview/<token>/...` carries no cookie, so a session that a worker runs is not reached (section 7).

A first idea, sending the page's `homedir` with the calls, does not work: a page that owns its session has no signature for it
(`window.homesig` is only set for a shared session), and the server ignores a workspace named without one.

## 9. Check against the design canvas (2026-11-13)

All 15 boards of "Preview pane and terminal toggle" were read and compared with the running page (dark and light editor theme, a
page and a program, with the browser tests of `preview.spec.js` and `preview-types.spec.js`: 15 passed on the dev gateway).

**As designed and working:** sandboxed frame; guests included; the pane beside the editor in the terminal's place; Hide / Show in the
terminal bar and Ctrl+` (remembered); Run always shows the terminal; Run on an `.html` file opens the Preview; right-click "Open
preview"; page reload on save; port list and "Type it"; programs on a port with absolute links, WebSockets and cookie logins; the
listening notice (now drawn as board 3, see 8); the sandbox notice line; the states of boards 3 (nothing running, nothing on the port,
ended); Markdown, picture and PDF with the card and the language list; the admin switches; both editor themes (the pane follows the
IDE colours); the splitter between the editor and the pane is the IDE's own.

**Fixed in this check:** the listening notice (dark card, green dot, lime main button; its buttons no longer squeeze the text);
the command menu entry "Open the preview" for when nothing is shown; the size menu drawn like the bar's other buttons instead of
the browser's own control; the token and port calls on a gateway (8.2).

**Built after this check (2026-11-13, same day):** every item of the "designed but not built" list except the ones named at the end
of this section. How each works:

- *The bridge* (`livepreview.BridgeScript`, added to every page the preview shows, a program's and a file of the workspace's, so a
  file's HTML now carries it): reports the page's address and title, its console lines and uncaught errors, the calls it makes with
  `fetch` and `XMLHttpRequest`, and the keys the editor uses, to the pane with `postMessage`. The pane takes messages only from its
  own frame (`e.source`).
- *Back, Forward, loading bar, address bar*: the pane keeps its own list of addresses for each tab; a link in the page adds one; Back and
  Forward load the stored address again (with the current token). The address bar is typed in (click, or Ctrl+L): a file of the
  workspace, or `5000/path`.
- *Console and Requests*: a drawer under the page, with a red count on the button; emptied when the page loads again.
- *Tabs*: up to four (a file or a port each), each with its own history and console.
- *Status line*: Loading, Live (with what reloads it), or "Your program has stopped" (a 502 or 504 of the program).
- *The "..." menu*: Auto-update on save, Reload when I press Run (both remembered), Clear this preview's cookies
  (`POST /preview_cookies`), Copy address, Pop out to its own window (the window follows reloads; "Bring it back").
- *500 banner*: after a program's page loads, the pane asks for the same address once and, on a 5xx, shows the status and the first
  lines of the answer with "Show in terminal" and "Show page anyway".
- *Run*: with the pane open, Run keeps it, brings the terminal up under it as a drawer (the terminal's Hide closes the drawer) and
  reloads the page after 1.2 and 4 seconds (switch in the menu).
- *Keyboard*: Ctrl+Alt+V opens or closes the pane (Ctrl+Shift+V is the browsers' "paste as text" and would break paste in the editor and
  the terminal, so it was not taken), F6 moves between the editor, the pane's bar and the page, Esc Esc in the page goes back to the
  editor, Ctrl+L goes to the address bar, Ctrl+` shows the terminal.
- *First use*: one hint, once for each browser (`localStorage preview-hint`), the first time the button shows.
- *Phone*: below 800 px the open pane is a third tab, Preview, beside Editor and Terminal; the Terminal tab closes the pane.
- *WebSocket keep-alive* (`livepreview/wsping.go`): a proxied WebSocket that has been quiet for 25 seconds gets a ping, sent only between two
  frames (the relay follows frame boundaries). Not tried through a real edge such as Render's.
- *Shared sessions*: each pane has a random viewer id that goes into its token; the server keeps a program's cookies for each viewer
  (workspace, viewer, port), so a login of one person is not a login of the others. Everyone previews the owner's workspace and program.
- *Genie*: agent actions `preview_open` (path or port), `preview_reload`, `preview_close` (one permission, scope "preview") and
  `preview_state` (reads the address, ports, console errors and last calls; no permission); notes `preview-pane.md`, `preview-tools.md`,
  `genie-agent-preview.md`. Tests: `agent-protocol.test.ts`, the prompt list test in Go.

**Still not done:** the Phone layout and the pop-out window were tried in Chromium at a desktop size only (the phone tab is not
covered by a browser test); the WebSocket ping through a real edge; "Pop out" cannot be tested headless; Firefox and Safari.

## 10. localStorage, sessionStorage and cookies in the previewed page (2026-11-13)

The frame has no origin, so a browser gives its page none of the three (`localStorage` and `document.cookie` throw). The preview
now gives it working ones (`livepreview/storage.go`):

- The server keeps them in memory (`Store`): `local` and `cookie` for each workspace and scope, `session` for each viewer and scope. A
  scope is the folder of a page (`f:/site/`) or the port of a program (`p:5000`). Each area is a JSON object of at most 5 MB, at most
  4096 stores are kept (oldest first). They do not outlive the server.
- A page gets its stores in a script put at the top of its head (`StorageInfo.script`, for a file in `ServeFileWith`, for a program in
  `HTMLHookWith`), so a read is synchronous as in a browser. `<` in the data is written as `\u003c`, so data cannot close the script.
- The script replaces `window.localStorage`, `window.sessionStorage` (a Proxy: `getItem`, `setItem`, `removeItem`, `clear`, `key`,
  `length`, `store.name = v`, `Object.keys`) and `document.cookie` (name and value; `max-age` and `expires` are kept, path and domain are
  ignored). A write goes back after 40 ms with `POST /preview/<token>/s` (text body, so no preflight; `keepalive`).
- The pane's own storage and the site's cookies are not touched. Pages of one folder share their storage, as pages of one site do.
- Not provided: IndexedDB, Cache storage, service workers, the `storage` event between frames, cookie `Secure`/`HttpOnly`/path rules.
- Tests: `storage_test.go`, `TestAProgramsHTMLGetsStorageForItsPort`, and the browser test "localStorage, sessionStorage and cookies work".

## 11. HTML and CSS in the language picker (2026-11-13)

The top picker has a group "Web (shown in the Preview)" with HTML and CSS (`<optgroup>` in `index.html`, options with `data-web="1"`). They are not
languages of the terminal, so the real `<select id="optionlist">`, which the terminal, the starters and the share link follow, keeps its value:

- `select-menu.js`: an item of the web group is not set in the select; the menu calls `window.WebLanguage.choose` and shows the choice on its button
  (`select.__menuOverride`, dropped by itself when the select's own value changes). `pickLanguage` (hero chips, the palette, Genie) does the same.
- `29-web-language.js` (the page) and `lib-weblang.mjs` (no page, tested): the editor's mode (`#select-lang`), a starter in an untitled editor that
  has no text of its own (else a notice offers it), and Run (`CompileandRun` asks `WebLanguage.active()` first): HTML saves the open page, or makes
  `index.html` (a free name) from an untitled editor, and opens it in the Preview; CSS saves the sheet (an untitled editor becomes `style.css`) and shows
  the `index.html` beside it, or says to make one. The terminal is not touched.
- Not in the terminal's own menu (`main.ts`) and not read by the `?repl=` loop. Genie's `set_language` calls `WebLanguage.choose` for them.
- Tests: `lib-weblang.test.mjs`, `e2e/tests/web-language.spec.js`. Not done: sharing a code link from a web language shares it under the terminal's
  language; the hero's "19 languages" counts the terminal's languages only.


## 12. A browser's address bar, the + tab, and the pane's place (2026-11-14)

Design on the canvas "Preview pane: move it and pop it out", approved the same day. Built in `page/27-preview.js`, `page/lib-address.mjs`, `index.html`, `ui-refresh.css`.

- **The top bar is gone.** "This page runs in a sandbox and is not part of OpenREPL." (`#preview-note`) is removed; the status line below ("Live · sandboxed page · ...")
  is the only notice. It is outside the frame, so the page cannot change it.
- **An address typed in the bar loads in the tab that is shown** (`navigateTo`): the tab's target is replaced, the page reports its address (`onNav`) and it is
  pushed on that tab's history, so Back returns to the page before. `goHist` takes the tab's target from the address it goes back to (a file or a port).
  The ports list ("127.0.0.1:port" and Other port) loads in place as well; the "Preview the open file" button, Run, Genie's `preview_open` and the notice for a
  program that starts to listen still use `show`, which reuses a tab of the same target or adds one.
- **`lib-address.mjs` (`parseAddress`, tested in `lib-address.test.mjs`):** a name is a file counted from the home folder (`web/index.html`, `/web/index.html`, the absolute
  path of the home folder); it cannot climb out with `..`; when the file is not under the home folder but is beside the page shown, that one is meant (`alt`, checked against
  `FileBrowser.entries()`). A port: `5000`, `5000/api`, `localhost:5000`, `127.x.x.x`, `[::1]`, the private ranges with a port, with or without `http(s)://`: all go to the
  program of this workspace on that port through the same proxy and sandbox (the pane's server never leaves the workspace's own loopback). Another site or machine,
  a scheme other than http(s), and a port outside 1024 to 65535 are refused with a message over the tab, which keeps its page and history.
- **+ is a new empty tab** (`newBlankTab`, target `{kind:"n"}`, shown by `#preview-newpage`): the address bar is focused and empty, and the page lists up to 9 suggestions: files of
  the workspace the pane can show (by name), ports that listen, and the last 5 addresses typed. Arrow keys and Enter pick, a click picks, Escape leaves the bar. A name typed there
  loads in that same tab. A second + while an empty tab exists selects it (never two). The status line says "New tab · sandboxed pane · nothing loaded". At the tab limit (4) a notice says so.
- **The pane's place** (`placePane`, `setDock`): by default it is a child of the terminal's box and covers the terminal (`is-previewing`); docked over the editor it is a child of
  `#ide` (absolute, over the whole editor box: its file tabs and toolbar, the text and its status bar; the pane's own bar has Close and Terminal), the terminal is shown beside it, and Run does not need the drawer. The grip
  (`#preview-grip`, first in the pane's bar) is dragged with the mouse or a pen: the side of the page the pointer is on decides (the rectangle of `#ide` or of `#terminal-div`,
  outlined in `.preview-drop`), Escape cancels, and a release more than 40 px outside the browser's window pops the pane out (`popOut`, as the menu's entry does). The keyboard way is
  the "..." menu: "Dock over the editor" / "Dock over the terminal". The place is remembered in this browser (`localStorage` `preview-dock`) and is never written to a shared session.
  On a phone there is no grip (the pane is its own tab). Moving the pane in the DOM reloads the frame, so the page starts again; its history stays.
- **Tests:** `lib-address.test.mjs`; `e2e/tests/preview.spec.js`: no bar, typed address in the same tab with Back, the + tab with suggestions, docking by the menu and by dragging.
  Not tested headless: the pop-out by dragging out of the window (the pop-out itself is the same call as the menu's).
