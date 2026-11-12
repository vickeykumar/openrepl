# Preview pane and terminal toggle: design

Status: phases 1 to 4 built on branch `preview-pane` (see docs/lld/17-preview-pane.md for what was built and what was not). Written 2026-11-11 from the discussion with the owner. Design canvas (thirteen boards):
https://claude.ai/artifact/QurmTT5RANCgpP1ZJDUEDF (private; the owner can open it).

## 1. What it is

A **Preview pane** beside the editor, where a visitor sees the web program they are working on: an HTML file, or any
program that listens on an HTTP port in their workspace (a Flask, Node, Go, Java, PHP or Ruby server, a frontend dev
server). It looks like a small browser: an address bar that reads `127.0.0.1:<port>`, a reload button, a port list, a
phone-width toggle, open in a new tab, and close.

Together with it, the terminal gets a **Hide / Show toggle** in its own menu bar, so the editor can have the whole
window. Run still shows the terminal, because that is where the result appears.

The look is that of a modern online editor (VS Code, StackBlitz): the dark OpenREPL palette, a tab strip, a status
bar, small icon buttons. See the canvas for each state.

## 2. Decisions made (owner, 2026-11-11)

| Question | Answer |
|---|---|
| Isolated origin (a wildcard domain) or a sandboxed iframe? | A sandboxed iframe only, for the first version |
| Sign-in needed? | No. The preview works for everyone, guests included |
| Where does the pane live? | Beside the editor. Opening it hides the terminal |
| Terminal toggle | A Hide / Show button in the terminal's menu bar. Run always shows the terminal |
| Languages | Any backend language and any frontend that can run as an HTTP server or static files |
| What does Run do on an HTML file? | It opens the Preview, and the terminal stays hidden (owner: yes) |
| Cookies and `localStorage` of the previewed page, which a sandboxed iframe without its own origin does not keep | Accepted for phase 1 (owner: yes). Static pages rarely need them |

Both questions left open at first are decided (the last two rows): Run on an HTML file opens the Preview, and a
previewed page keeps no cookies or `localStorage` between reloads in phase 1 (section 7).

## 3. Phases

1. **Static files.** A Preview button for the `.html` file in the editor (next to Run, Shift+Enter), an "Open preview"
   entry in the file's right-click menu, and the empty state's "Preview index.html" button. The page is served from the
   workspace with its sibling files (CSS, JS, images, other pages) by relative path. It reloads on save (a switch, on
   by default). The terminal Hide / Show toggle ships with this phase.
2. **Any HTTP server, by port.** The address bar and the port list. This is what makes every backend language work at
   once, because the preview only cares about a port (section 4).
3. **WebSockets and hot reload**, so frontend dev servers (Vite, Next, Angular, Vue) behave properly. Each needs its
   host and base-path option; a "how to start" hint per framework.
4. **Auto-detect.** A prompt "Your program is listening on port 5000. Open preview?" when a program opens a port.

5. **Its own address per preview (not planned).** Each preview on its own address under a separate domain (for example
   `ws123-5000.<preview-domain>`), so the page has an origin of its own: cookies and `localStorage` work, the app sits at
   `/` (no absolute-path problem), and the sandbox restrictions go away. This is what Replit, Codespaces and StackBlitz
   do. It needs a second registrable domain (not a subdomain of OpenREPL, so cookies cannot leak), a wildcard address
   and certificate (Cloudflare can provide them), and routing by host name in the server. Not yet checked: whether
   Render's free tier accepts wildcard custom domains. **The owner decided on 2026-11-11 not to plan this now**; it is
   kept here as the way out of the limits of section 7.

Phase 1 is language-free and small. Phase 2 is the real work (the proxy).

## 4. How it works

### 4.1 Static files (phase 1)

A route serves a file of the visitor's workspace for the preview iframe, read-only, only inside their own workspace
(the same path checks as the file browser). The response carries the headers of section 6.

### 4.2 Reaching a running program (phase 2)

The browser cannot reach a port inside a sandbox, so the server connects for it. A request to
`/preview/<workspace>/<port>/<path>` is passed to the program the visitor started:

- **Hosts with the network emulation** (no privileged containers, such as the Render free tier; LLD 16): the emulation
  already keeps each workspace's sockets under an abstract name, `@openrepl-ne/<workspace>/t/<port>`, and knows which
  ports a workspace has opened. The server, which hosts the emulation's supervisor, dials that name directly. The
  same table gives the port list and the "your program is listening" event.
- **Hosts with real network namespaces** (privileged containers): the server connects inside the workspace's namespace
  (`nsenter`, as the sandbox already does).
- **Gateway and workers** (LLD 11): the gateway proxies the request to the worker that runs the session, through the
  existing tunnel, as it does for terminals.

The proxy speaks HTTP/1.1 and passes WebSocket upgrades (phase 3). A program may listen on `127.0.0.1` or `0.0.0.0`;
both are the workspace's own address.

### 4.2.1 Two problems of phase 2 that the sandbox choice causes

- **Server cookies.** A sandboxed page (no origin of its own) does not send cookies, so an app that logs in with a
  server cookie (Flask-Login, Express sessions) would lose the login on every request. Proposal: the proxy keeps the
  `Set-Cookie` values of each preview itself (a cookie jar per workspace and port) and adds them to the later requests
  it forwards. Server-side logins then work; only the browser's own `localStorage` stays unavailable.
- **Absolute paths.** The preview is served under `/preview/<workspace>/<port>/`, but many apps link to `/static/x.css`
  or fetch `/api/...`, which would reach OpenREPL's own site. Proposal: requests that arrive for a path the site does
  not own, from a page that came from a preview (seen in the `Referer` header), are routed to that preview. This
  works for most apps but is not guaranteed; it is tested first with Flask, Express and Vite. If it proves too fragile,
  the fallback is a separate subdomain per preview (needs the wildcard domain the owner has set aside for now).
- "Open in a new tab" must stay sandboxed: the preview responses carry `Content-Security-Policy: sandbox`, which applies
  the sandbox even when the page is opened as a whole tab (section 6).

### 4.2.2 WebSockets and hot reload (phase 3)

The proxy accepts a WebSocket upgrade, opens one to the program and copies messages both ways. It sends keep-alive
pings, because Render's edge was seen to hold or drop a close frame and to mishandle quiet connections (see the
"end notice" of LLD 02). Streaming answers (server-sent events) are flushed as they come. Dev servers (Vite, Next,
Angular, Vue) use a WebSocket for hot reload and need their host and base-path options set; a "how to start" hint per
framework is shown in the pane.

### 4.3 Language support

Any program that listens on a TCP port and answers HTTP works, whatever the language: Python (Flask, FastAPI, Django,
`python -m http.server`), Node (Express, Fastify), Go (`net/http`), Java (Spring, the built-in server), PHP
(`php -S`), Ruby (Sinatra, Rails), Rust, C and C++. Frontend dev servers are ordinary programs on a port as well.
Static HTML, CSS, JS, SVG and images need no server (phase 1).

Not covered: raw TCP and UDP programs (a chat server, a game server) and desktop GUI programs. They stay in the
terminal. A language or package that the image does not have cannot be started; this has not been tested for the
frontend frameworks, and a large `npm install` may meet the disk and network limits.

## 5. The interface

Each state is drawn on the canvas.

- **Board 1, the editor with a Preview pane.** A Preview button in the app bar with a green dot when a program is
  listening. The pane: reload, address bar (`127.0.0.1:` and the port, with a lock icon), a port list, phone width,
  open in a new tab, close. A strip under it: "Live, sandboxed page, reloads when you press Run". The terminal is
  hidden; the status bar says so and has a "Show terminal" button (Ctrl+`).
- **Board 2, the terminal toggle.** Three states: shown, hidden (the editor has the whole window), and after Run.
  Hide / Show sits at the right of the terminal bar, and in the status bar when hidden. The choice is remembered.
  Hiding does not stop anything: the shell and a running server stay alive, so the Preview keeps working. Run always
  brings the terminal back; typing in the editor does not.
- **Board 3, the states of the pane.** A "your program is listening" prompt; opened with nothing running ("Nothing is
  running yet", with Run and Preview index.html); a port with nothing behind it ("Nothing is listening on port 8080",
  with the ports that are open and a "Use 5000" button); loading, with a sandbox notice.
- **Board 4, preview and terminal together.** With the Preview open, Run slides the terminal up as a drawer under the
  editor and leaves the Preview where it is. Hide closes the drawer. This satisfies both "the Preview hides the
  terminal" and "Run shows the terminal".
- **Board 5, phase 1.** The Preview button for an `.html` file, the right-click entry, the files the page can load,
  and the auto-update switch.

### 5.1 Boards for the later phases

- **Board 6, phase 2.** The request path (Preview pane, OpenREPL server, the workspace's port, the program), the
  address bar with the port list ("Open now", "Other port"), server logins kept by the proxy's cookie jar, absolute
  paths routed back to the preview, and the risk of the `Referer` routing.
- **Board 7, phase 3.** A Vite app updating after a save, a "Live" / "Reconnecting" indicator, the start line per
  framework (Vite, Next, Angular) with a copy button (the options are examples, to be checked per framework), and what
  passes through the proxy (WebSockets, streaming, keep-alive pings). To test on Render: an idle WebSocket for several
  minutes, a worker behind the gateway, many previews at once.
- **Board 8, phase 4.** The "Your program is listening on port 3000" prompt (Open preview, Not now, "Don't ask again
  for this workspace"). It shows once per port when the Preview is closed, and is driven by the workspace's list of open
  ports, not by what a framework prints.

### 5.2 Extras (not part of the five phases)

Each can be added on its own after phase 1. Boards 9 and 10.

- **Console and Requests tabs under the page** (console: phase 1; requests: phase 2). Logs and errors of the previewed
  page are passed up by a small script injected in the preview, so a visitor does not need browser dev tools. The
  Requests tab lists the calls to the server with status and time. **Recommended first extra**, because it answers "why
  is the page blank?".
- **Sizes** (phase 1). Phone (390 x 844), Tablet (768 x 1024), Laptop (1280 x 800) or fit the pane, in a simple frame.
- **The "..." menu.** Auto-update on save, reload when Run is pressed, clear this preview's cookies (phase 2), copy
  address, open in a new tab.
- **File types.** The Preview button knows the type: `.html` (with its CSS and JS), `.md` (rendered, links work),
  images `.svg` `.png` `.jpg` (with zoom), `.pdf` (the browser's own viewer). Other files show no Preview button.
- **On a phone.** There is no room for editor and pane side by side, so the Preview becomes a third tab next to Editor
  and Terminal; Run on an HTML file switches to it.
- **A server error.** When the program answers with an error, the pane shows the status and the first lines of the
  error ("500 Internal Server Error", `KeyError: 'name'`) with "Show in terminal" and "Show page anyway".
- **Shortcuts.** Ctrl+Shift+V opens or closes the Preview, Ctrl+` shows or hides the terminal, Shift+Enter previews the
  HTML file in the editor.
- **Ideas not planned:** a shareable link to a preview (needs login rules and abuse limits), a screenshot button, and
  letting Genie read what the page shows.

### 5.3 Getting around and finish (boards 12 and 13)

- **The divider** (phase 1). Drag it to give the editor or the page more room; double-click resets to half and half;
  the width is remembered; arrow keys move it when it has the focus.
- **Back, forward and a loading bar** (phase 1). A link inside the page changes the address bar and Back returns. A
  thin bar under the toolbar shows a page is loading. The pane keeps its own history; the editor's page does not move.
- **Tabs in the pane** (with phase 2). One tab per thing previewed, a file or a port, so a backend on 5000 and a
  frontend on 5173 sit side by side. Up to four.
- **Light theme** (phase 1). The pane's bars follow the site theme; the previewed page keeps its own colours.
- **First-use hint.** Once per browser, on the Preview button, the first time a visitor opens an `.html` file or a
  program opens a port: "See your web page here", with "Got it" and "Show me". Also a step of the existing page tour.
- **Keyboard and screen reader** (phase 1). F6 moves between editor, pane toolbar and page; Esc twice leaves the
  previewed page for the editor; Ctrl+L goes to the address bar; Ctrl+R in the pane reloads it. The frame is named
  ("Preview of index.html"); "Loading", "Loaded" and "Your program has stopped" are announced; the status is always in
  words next to the dot; every button has a visible focus ring. The key choices are proposals and must be checked
  against the editor's and the browser's own shortcuts before they are fixed.
- **Pop out** (later). The pane moves to a window of its own for a second screen and stays tied to the editor: it
  reloads on save and on Run and closes with the editor's tab. Its place shows "Preview is in its own window" with a
  button to bring it back.

## 6. Security

The preview shows code the visitor (or a guest) wrote, on OpenREPL's own domain, so it must never be able to act as
the OpenREPL page.

- **Sandboxed iframe**, with `allow-scripts allow-forms allow-popups allow-modals` and **without** `allow-same-origin`.
  The page then has an opaque origin: it cannot read OpenREPL cookies, `localStorage`, or call OpenREPL's APIs as
  the visitor.
- **Responses of the preview route** carry `Content-Security-Policy: sandbox ...`, `X-Content-Type-Options: nosniff`,
  and no OpenREPL cookies are sent to the route. Because the page's origin is opaque, its requests to its own server
  are cross-origin, so the route answers with the CORS headers they need (and no credentials).
- **Access.** The route checks that the visitor owns the workspace in the URL: a signed-in user's own home, a guest's
  own guest home (tied to their browser). Nobody else can open another workspace's preview.
- **Abuse** (hosting a phishing page on our domain): a visible banner "This page runs in a sandbox and is not part of
  OpenREPL", a rate limit per workspace, and a limit on how long an idle preview stays reachable. The guest case needs
  the most care; the limits in `utils` for guests apply.
- **No access beyond the workspace.** The proxy reaches only the workspace's own sockets. It does not open outbound
  connections for the page.

## 7. Limits of the sandbox decision

Without a separate domain, the previewed page has no origin of its own. It works: scripts, forms, `fetch` to its own
server, images, CSS, popups. It does not: cookies and `localStorage` of the page (a login kept there is lost on
reload), and flows that need "same origin" (some sign-in redirects). Static pages rarely need them; apps with a login
do. If this proves too limiting, the way out is phase 5 (its own address per preview); that needs a domain and DNS and
is not planned.

## 8. Test plan

- Unit: the path checks of the static route; the workspace-owner check; the header set; the port list from the
  emulation table; the proxy's handling of a refused connection and a slow program.
- Integration (Go, Linux): a program listening on an emulated port answers through the proxy; another workspace's
  request is refused; a closed port gives the "Nothing is listening" answer.
- Page: the Preview button appears only for `.html` files; the toggle hides and shows the terminal and is remembered;
  Run shows the terminal; the pane's states.
- Browser (Playwright): open a preview of an HTML file and see its content; hide and show the terminal.
- By hand on Render: two guests running a server on the same port each see only their own page; a worker machine
  behind the gateway.

## 9. Open items

- Whether a guest preview needs extra limits (section 6).
- Which frontend frameworks to document a start command for (phase 3).
- A slot for the work: none given yet; phase 1 is proposed for the next working day at 9:00 IST.

## 10. Review notes (2026-11-12): what to change before building

Found on a second reading of this document. Board 11 of the canvas. Items 1 and 2 are gaps in the design as written
above; they replace the matching parts of sections 4.2.1 and 6 once confirmed. The browser behaviour behind them is to
be confirmed in the short test of item 8, not assumed.

1. **The owner check cannot rely on the sign-in cookie.** A sandboxed page has no origin, so the browser does not send
   OpenREPL's cookies with the page's own requests (its CSS, scripts, `fetch`). Section 6 says both "the route checks
   that the visitor owns the workspace" and "no OpenREPL cookies are sent to the route", which cannot both hold.
   Change: a **preview token** in the address. The editor page (signed in, or a guest with its own workspace) asks the
   server for a short-lived, unguessable token bound to its workspace, and the pane loads `/preview/<token>/<port>/`.
   The token is the key for every request, works in a new tab, and can name the worker so the gateway routes without a
   lookup. A token that has ended or is wrong gives "This preview has ended. Open it again from the editor."
2. **Absolute paths: the `Referer` alone will not do.** By default a browser sends only the site's origin as `Referer`
   on a cross-origin request, and every request of an origin-less page is cross-origin, so the server could not tell
   which preview asked. Change: three layers. The proxy rewrites absolute links in the HTML it passes on (`src`,
   `href`, `action`); a small script added to the page prefixes the addresses given to `fetch`, `XMLHttpRequest`,
   `WebSocket` and `EventSource`; and the preview responses carry `Referrer-Policy: unsafe-url` so the full address is
   sent, as the last net. The same added script feeds the Console tab (section 5.2). Adding a script means the proxy
   must read HTML answers uncompressed (ask the program for no compression, or decompress).
3. **An off switch for the admin.** In Site settings: Preview on or off, and separately for guests, so abuse can be
   stopped in one click without a deploy.
4. **Limits, written down.** Per workspace: requests per minute, open connections, answer size, time per request,
   request body size; ports 1024 and above only. Answers carry `Cache-Control: no-store` so an edit always shows, and
   `X-Robots-Tag: noindex`.
5. **When the session ends.** A terminal ends after 60 minutes and a guest home after an hour, and the program with
   it. A new state for the pane: "Your program has stopped. Run it again." To decide: whether looking at a preview
   counts as using the workspace (keeps a guest home alive).
6. **Shortcuts inside the preview.** With the cursor in the previewed page, Ctrl+` and Ctrl+Shift+V go to that page.
   The added script passes these two keys up to the editor page.
7. **Auto-update uses what exists.** The file browser already tells the page when a file changes (the `Event`
   message, LLD 04); the pane reloads on that, a moment after the last change.
8. **A short test before phase 2.** About half a day: a Flask, an Express and a Vite app through a throwaway proxy in
   a sandboxed frame, in Chrome, Firefox and Safari, to confirm items 1 and 2. Phase 1 does not wait for it, but phase
   1's static route should already use the token of item 1.

Also to settle before phase 2: on hosts with real network namespaces there is no emulation table, so the port list
needs another source (listing the listening sockets inside the namespace); and which sandbox permissions to give
beyond scripts, forms, popups and modals (downloads, pointer lock), never `allow-same-origin` or top navigation.

## 11. Related

- LLD 16, network emulation (the sockets the proxy dials and the port table).
- LLD 11, distributed execution (the gateway and worker path).
- LLD 04, the Run button and the file browser (path checks).
- LLD 02, terminal sessions (the terminal bar the toggle goes in).
