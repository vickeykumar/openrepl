# 17. Preview pane

Status: implemented on branch `preview-pane` (phases 1 to 4 of `docs/preview-pane-design.md`), not merged, not deployed.
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
valid 2 hours; the page renews it 5 minutes before the end. A wrong or ended token gets 410 and "This preview has ended."
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

The page asks `/preview_ports` when it opens, every 20 seconds while it is in view and the pane is closed, when it comes
back into view, and at 2, 4, 8, 15, 30 and 60 seconds after Run. A port that was not there before gets one notice
("Your program is listening on port N", with Open preview, Not now, Don't ask again; the last is remembered per workspace
in `localStorage`, `preview-noask:<home>`). While a program listens, the Preview button shows a dot and is visible for any
file. The answer is a few bytes read from the server's own tables.

## 4. Admin switch

Site settings `previewOff` and `previewGuestsOff` (Settings page, "Preview" card; change log lines "preview off" and
"preview for guests off"). Off: the button is hidden and the token and port routes answer 403. Existing tokens of a
workspace also stop (the preview route checks the setting on every request; a guest is a home named `guest-...`).

## 5. Limits that are in place

Token 2 hours; ports 1024 to 65535; request body 10 MB; answer headers within 30 seconds; static files 50 MB; HTML rewrite
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
