// The Preview pane (docs/preview-pane-design.md): a web page of the workspace, shown next to the editor in a sandboxed
// frame, in the terminal's place while it is open. Phase 1: an .html file of the workspace, served with its sibling files.
//
// The frame has no origin of its own (sandbox without allow-same-origin), so the browser sends none of this site's
// cookies with its requests: what names the workspace is a token in the address (/preview/<token>/f/<path>), asked
// for with this page's own cookies (/preview_token) and renewed before it ends.

var TOKEN_MARGIN = 5 * 60 * 1000; // renew this long before the end
var SIZES = { fit: null, phone: [390, 844], tablet: [768, 1024], laptop: [1280, 800] };
var SIZE_KEY = "preview-size";

var target = null;       // { kind: "f", path: "/home/.../index.html" }
var token = null;        // { value, expires (ms) }
var tokenPending = null;
var open = false;
var reloadTimer = 0;
var size = "fit";
var knownPorts = [];      // the ports the workspace's programs listen on, as far as the page has asked
var isolated = true;
var seenPorts = {};       // ports the page has told the visitor about (or that the visitor said "not now" to)
var watchTimers = [];
var MAX_TABS = 4;
var tabs = [];            // one for each thing previewed: { t: target, hist: [addresses], idx, logs, reqs, errs }
var cur = -1;
var navTo = "";           // an address the pane itself set (Back, Forward): the page reporting it is not a new page
var popout = null;        // the window of a popped-out preview
var consoleOn = false, consoleTab = "console";
var viewer = Math.random().toString(36).slice(2, 12) + Date.now().toString(36).slice(-4); // this pane's id: its own cookies in a shared session
function readOpt(k, d) { try { var v = localStorage.getItem(k); return v === null ? d : v === "1"; } catch (e) { return d; } }
function saveOpt(k, v) { try { localStorage.setItem(k, v ? "1" : "0"); } catch (e) {} }
var optAuto = readOpt("preview-opt-auto", true);   // reload on save
var optRun = readOpt("preview-opt-run", true);     // reload when Run is pressed
function tab() { return cur >= 0 ? tabs[cur] : null; }

function el(id) { return document.getElementById(id); }
function home() { return String(window.homedir || ""); }
function fileOf() {
  // a picture or a PDF is not in the editor: the card of the viewer holds it (24-large-file-viewer.js)
  var m = window.LargeFileViewer && window.LargeFileViewer.mediaPath ? window.LargeFileViewer.mediaPath() : "";
  if (m) return m;
  var e = window["editor"];
  return e && e.env && e.env.filename ? String(e.env.filename) : "";
}
function isHtml(path) { return /\.html?$/i.test(String(path || "")); }
// What a file of the workspace is shown as: a page (.html), Markdown rendered by the server, a picture, or a PDF
// (the browser's own viewer). Anything else has no Preview.
function previewKind(path) {
  var p = String(path || "");
  if (isHtml(p)) return "html";
  if (/\.(md|markdown)$/i.test(p)) return "md";
  if (/\.(png|jpe?g|gif|webp|bmp|ico|svg)$/i.test(p)) return "image";
  if (/\.pdf$/i.test(p)) return "pdf";
  return "";
}
function canShow(path) { return !!previewKind(path); }
function disabled() { return !!(window.site_settings && window.site_settings.previewOff); }

// ---- the address of the frame -----------------------------------------------------------------------------------------

function relative(path) {
  var h = home();
  return h && path.indexOf(h + "/") === 0 ? path.slice(h.length + 1) : path.replace(/^\/+/, "");
}

function encodePath(rel) { return rel.split("/").map(encodeURIComponent).join("/"); }

function getToken() {
  if (token && token.expires - Date.now() > TOKEN_MARGIN) return Promise.resolve(token.value);
  if (tokenPending) return tokenPending;
  tokenPending = fetch(preprocessurl("/preview_token?v=" + viewer), { credentials: "same-origin" })
    .then(function (res) {
      if (!res.ok) return res.text().then(function (t) { throw new Error(t.trim() || "The preview is not available."); });
      return res.json();
    })
    .then(function (j) {
      token = { value: j.token, expires: j.expires * 1000 };
      return token.value;
    })
    .then(function (v) { tokenPending = null; return v; }, function (e) { tokenPending = null; throw e; });
  return tokenPending;
}

function urlFor(t, tok) {
  if (t.kind === "f") {
    var how = previewKind(t.path);
    return "/preview/" + tok + "/f/" + encodePath(relative(t.path)) + (how === "md" || how === "image" ? "?render=" + how : "");
  }
  if (t.kind === "p") return "/preview/" + tok + "/p/" + t.port + (t.sub || "/");
  return "";
}

// ---- the ports of the workspace -----------------------------------------------------------------------------------------

function askPorts() {
  return fetch(preprocessurl("/preview_ports"), { credentials: "same-origin" })
    .then(function (res) { return res.ok ? res.json() : { ports: [], isolated: true }; })
    .then(function (j) {
      knownPorts = (j.ports || []).slice();
      isolated = j.isolated !== false;
      syncButton();
      return knownPorts;
    }, function () { return knownPorts; });
}

function noAskKey() { return "preview-noask:" + home(); }
function noAsk() { try { return localStorage.getItem(noAskKey()) === "1"; } catch (e) { return false; } }

// A program started listening while the pane was closed: ask whether to open it (once for each port)
function offer(ports) {
  if (open || noAsk() || disabled() || !window.notify) return;
  var fresh = ports.filter(function (p) { return !seenPorts[p]; });
  if (!fresh.length) return;
  var port = fresh[0];
  fresh.forEach(function (p) { seenPorts[p] = true; });
  // as drawn on the "What the Preview says in each state" board: a dark card with a green dot and a lime main button
  window.notify("Open it in the Preview pane beside your editor.", {
    type: "info", variant: "program", title: "Your program is listening on port " + port, timeout: 0, // stays until it is answered
    actions: [
      { label: "Open preview", primary: true, onClick: function () { openPort(port); } },
      { label: "Not now", onClick: function () {} },
      { label: "Don't ask again", quiet: true, onClick: function () { try { localStorage.setItem(noAskKey(), "1"); } catch (e) {} } }
    ]
  });
}

// After Run, a program may start listening within seconds: the ports are asked for a few times, then no more
function watchAfterRun() {
  watchTimers.forEach(clearTimeout);
  watchTimers = [2000, 4000, 8000, 15000, 30000, 60000].map(function (ms) {
    return setTimeout(function () { askPorts().then(offer); }, ms);
  });
}

// examples of how to start a program that the pane can show (to be checked per framework: the options differ)
var HOWTO = [
  ["Python", "python3 -m http.server 8000"],
  ["Flask", "flask run --host 127.0.0.1 --port 5000"],
  ["Node", "node server.js   # listen on a port such as 3000"],
  ["Vite", "npm run dev -- --host 127.0.0.1 --port 5173"],
  ["Next.js", "npx next dev -H 127.0.0.1 -p 3000"],
  ["Angular", "ng serve --host 127.0.0.1 --port 4200"]
];

function renderHowto() {
  var box = el("preview-howto");
  if (!box || box.firstChild) return;
  HOWTO.forEach(function (h) {
    var row = document.createElement("div");
    row.className = "preview-howto__row";
    var name = document.createElement("span");
    name.textContent = h[0];
    var code = document.createElement("code");
    code.textContent = h[1];
    code.title = h[1];
    var b = document.createElement("button");
    b.type = "button";
    b.className = "ide-btn";
    b.textContent = "Type it";
    b.setAttribute("aria-label", "Type the " + h[0] + " command in the terminal");
    b.addEventListener("click", function () {
      closePane(); // the terminal comes back
      if (window.LargeFileViewer && window.LargeFileViewer.typeInTerminal) window.LargeFileViewer.typeInTerminal(h[1].split("   #")[0]);
    });
    row.appendChild(name); row.appendChild(code); row.appendChild(b);
    box.appendChild(row);
  });
}

function renderPorts() {
  renderHowto();
  var list = el("preview-ports-list");
  var fileBtn = el("preview-file-button");
  if (!list) return;
  list.textContent = "";
  if (!knownPorts.length) {
    var none = document.createElement("span");
    none.className = "preview-ports__empty";
    none.textContent = isolated ? "Nothing is listening yet. Run your program in the terminal." : "A program cannot be previewed on this server: its network is not private to your workspace.";
    list.appendChild(none);
  }
  knownPorts.forEach(function (p) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "ide-btn";
    b.textContent = "127.0.0.1:" + p;
    b.addEventListener("click", function () { openPort(p); togglePorts(false); });
    list.appendChild(b);
  });
  if (fileBtn) fileBtn.hidden = !canShow(fileOf());
}

function togglePorts(force) {
  var box = el("preview-ports");
  var btn = el("preview-ports-button");
  if (!box) return;
  var show = typeof force === "boolean" ? force : box.hidden;
  box.hidden = !show;
  if (btn) btn.setAttribute("aria-expanded", show ? "true" : "false");
  if (show) { renderPorts(); askPorts().then(renderPorts); }
}

// ---- the pane ---------------------------------------------------------------------------------------------------------

function message(html, action) {
  var box = el("preview-message");
  if (!box) return;
  if (!html) { box.hidden = true; box.textContent = ""; return; }
  box.textContent = "";
  var p = document.createElement("div");
  p.innerHTML = html;
  box.appendChild(p);
  if (action) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "ide-btn";
    b.textContent = action.label;
    b.addEventListener("click", action.run);
    box.appendChild(b);
  }
  box.hidden = false;
}

function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }

function setAddress(text) {
  var a = el("preview-address-text");
  if (!a) return;
  a.textContent = text || (target ? (target.kind === "f" ? relative(target.path) : "127.0.0.1:" + target.port) : "Preview");
}

// what the frame's address means to a person: a file of the workspace, or a port and a path
function describe(p) {
  var m = /^\/preview\/[^/]+\/f\/([^?#]*)/.exec(p);
  if (m) { try { return decodeURIComponent(m[1]); } catch (e) { return m[1]; } }
  m = /^\/preview\/[^/]+\/p\/(\d+)(\/[^?#]*)?/.exec(p);
  if (m) return "127.0.0.1:" + m[1] + (m[2] && m[2] !== "/" ? m[2] : "");
  return p;
}
function pathOf(href) { try { var u = new URL(href, location.href); return u.pathname + u.search + u.hash; } catch (e) { return href; } }
function renew(p) { return token ? p.replace(/^\/preview\/[^/]+\//, "/preview/" + token.value + "/") : p; }

// ---- tabs, history, the status line ---------------------------------------------------------------------------------

function sameTarget(a, b) { return a && b && a.kind === b.kind && (a.kind === "f" ? a.path === b.path : a.port === b.port); }

function useTab(t) {
  if (!t) { cur = -1; return; }
  var i = -1;
  tabs.forEach(function (x, k) { if (i < 0 && sameTarget(x.t, t)) i = k; });
  if (i < 0) {
    var n = { t: t, hist: [], idx: -1, logs: [], reqs: [], errs: 0 };
    if (tabs.length >= MAX_TABS) { i = cur >= 0 ? cur : 0; tabs[i] = n; } else { tabs.push(n); i = tabs.length - 1; }
  } else if (t.sub) { tabs[i].t = t; }
  cur = i;
}

function tabName(x) { return x.t.kind === "f" ? x.t.path.split("/").pop() : ":" + x.t.port; }

function renderTabs() {
  var box = el("preview-tabs");
  if (!box) return;
  box.hidden = tabs.length < 2 && !(tabs.length === 1 && open && false);
  box.textContent = "";
  tabs.forEach(function (x, k) {
    var b = document.createElement("div");
    b.className = "preview-tab" + (k === cur ? " is-on" : "");
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", k === cur ? "true" : "false");
    b.tabIndex = 0;
    var name = document.createElement("span");
    name.textContent = tabName(x);
    b.appendChild(name);
    var close = document.createElement("button");
    close.type = "button"; close.className = "preview-tab__x"; close.textContent = "\u2715";
    close.setAttribute("aria-label", "Close " + tabName(x));
    close.addEventListener("click", function (e) { e.stopPropagation(); closeTab(k); });
    b.appendChild(close);
    b.addEventListener("click", function () { selectTab(k); });
    b.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); selectTab(k); } });
    box.appendChild(b);
  });
  if (tabs.length && tabs.length < MAX_TABS) {
    var plus = document.createElement("button");
    plus.type = "button"; plus.className = "preview-tab preview-tab--new"; plus.textContent = "+";
    plus.setAttribute("aria-label", "Preview another port or file");
    plus.addEventListener("click", function () { togglePorts(true); });
    box.appendChild(plus);
  }
}

function selectTab(k) {
  if (k < 0 || k >= tabs.length || k === cur) return;
  cur = k; target = tabs[k].t;
  var tb = tabs[k];
  setAddress(tb.hist.length ? describe(tb.hist[tb.idx]) : "");
  var frame = frameFor(target);
  frame.setAttribute("data-url", "");
  if (tb.hist.length) { navTo = renew(tb.hist[tb.idx]); frame.setAttribute("data-url", navTo); setStatus("loading"); frame.src = navTo; } else load();
  hideError(); render();
}

function closeTab(k) {
  tabs.splice(k, 1);
  if (!tabs.length) { cur = -1; target = null; closePane(); return; }
  var was = k === cur;
  cur = Math.min(cur > k ? cur - 1 : cur, tabs.length - 1);
  target = tabs[cur].t;
  if (was) { var tb = tabs[cur]; if (tb.hist.length) { navTo = renew(tb.hist[tb.idx]); var f = frameFor(target); f.setAttribute("data-url", navTo); f.src = navTo; } else load(); }
  render();
}

function render() {
  var tb = tab();
  var back = el("preview-back"), fwd = el("preview-forward");
  if (back) back.disabled = !tb || tb.idx <= 0;
  if (fwd) fwd.disabled = !tb || tb.idx >= tb.hist.length - 1;
  renderTabs();
  renderConsole();
}

// a page that loads again starts with an empty console, as in a browser
function clearLogs() { var tb = tab(); if (tb) { tb.logs = []; tb.reqs = []; tb.errs = 0; renderConsole(); } }

function setStatus(state) {
  var box = el("preview-status"), text = el("preview-status-text");
  var bar = el("preview-loading");
  if (bar) bar.hidden = state !== "loading";
  if (!box || !text) return;
  box.classList.toggle("is-loading", state === "loading");
  box.classList.toggle("is-bad", state === "stopped");
  var program = target && target.kind === "p";
  text.textContent = state === "loading" ? "Loading\u2026"
    : state === "stopped" ? "Your program has stopped. Run it again."
    : state === "message" ? "Nothing to show"
    : "Live \u00b7 sandboxed page \u00b7 " + (program ? (optRun ? "reloads when you press Run" : "reload with the button") : (optAuto ? "reloads when you save" : "reload with the button"));
}

function hideError() { var b = el("preview-error"); if (b) { b.hidden = true; b.textContent = ""; } }

// A program that answers with a server error: say so above the page, with the first lines of its answer
function checkProgram(url) {
  if (!target || target.kind !== "p") return;
  var want = tab();
  fetch(url, { cache: "no-store", credentials: "omit" }).then(function (res) {
    if (tab() !== want) return;
    if (res.status === 502 || res.status === 504) { setStatus("stopped"); return; }
    if (res.status < 500) { hideError(); return; }
    res.text().then(function (txt) {
      var box = el("preview-error");
      if (!box || tab() !== want) return;
      var lines = txt.replace(/<(script|style)[\s\S]*?<\/\1>/gi, "").replace(/<[^>]+>/g, "\n").split("\n").map(function (l) { return l.trim(); }).filter(Boolean).slice(0, 3).join("\n");
      box.textContent = "";
      var head = document.createElement("div"); head.className = "preview-error__head";
      head.textContent = res.status + " " + (res.statusText || "Server Error") + " \u00b7 " + describe(pathOf(url));
      var pre = document.createElement("pre"); pre.textContent = lines;
      var acts = document.createElement("div"); acts.className = "preview-error__actions";
      [["Show in terminal", showDrawer], ["Show page anyway", hideError]].forEach(function (a) {
        var b = document.createElement("button"); b.type = "button"; b.className = "ide-btn"; b.textContent = a[0];
        b.addEventListener("click", a[1]); acts.appendChild(b);
      });
      box.appendChild(head); box.appendChild(pre); box.appendChild(acts);
      box.hidden = false;
    });
  }, function () {});
}

// The terminal as a drawer under the pane (Run, and "Show in terminal"): the pane stays
function showDrawer() {
  var term = el("terminal-div");
  if (!term || !open) return;
  if (typeof window.ShowTerminal === "function") window.ShowTerminal();
  term.classList.add("with-drawer");
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 60);
}
function hideDrawer() {
  var term = el("terminal-div");
  if (term && term.classList.contains("with-drawer")) { term.classList.remove("with-drawer"); setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 60); }
}

// ---- what the page reports (BridgeScript in package livepreview) ------------------------------------------------------

function onNav(tb, href, title) {
  var p = pathOf(href);
  if (navTo && p === navTo) { navTo = ""; tb.hist[tb.idx] = p; }
  else if (tb.hist[tb.idx] !== p) { tb.hist = tb.hist.slice(0, tb.idx + 1); tb.hist.push(p); tb.idx = tb.hist.length - 1; }
  tb.title = title;
  if (tb === tab()) { setAddress(describe(p)); render(); }
}

function addLog(tb, l) {
  tb.logs.push(l); if (tb.logs.length > 200) tb.logs.shift();
  if (l.level === "error") tb.errs++;
  if (tb === tab()) renderConsole();
}
function addReq(tb, r) { tb.reqs.push(r); if (tb.reqs.length > 200) tb.reqs.shift(); if (tb === tab()) renderConsole(); }

function renderConsole() {
  var tb = tab();
  var badge = el("preview-console-count");
  var n = tb ? tb.errs : 0;
  if (badge) { badge.hidden = !n; badge.textContent = n > 99 ? "99+" : String(n); }
  var c1 = el("preview-tab-console-n"), c2 = el("preview-tab-requests-n");
  if (c1) c1.textContent = tb && tb.logs.length ? "(" + tb.logs.length + ")" : "";
  if (c2) c2.textContent = tb && tb.reqs.length ? "(" + tb.reqs.length + ")" : "";
  var body = el("preview-console-body");
  if (!body || !consoleOn) return;
  body.textContent = "";
  var rows = tb ? (consoleTab === "console" ? tb.logs : tb.reqs) : [];
  if (!rows.length) {
    var e = document.createElement("div"); e.className = "preview-console__empty";
    e.textContent = consoleTab === "console" ? "Nothing written to the console yet." : "No calls made by the page yet.";
    body.appendChild(e); return;
  }
  rows.forEach(function (r) {
    var d = document.createElement("div");
    if (consoleTab === "console") { d.className = "preview-console__row preview-console__row--" + r.level; d.textContent = r.level + "  " + r.text; }
    else {
      d.className = "preview-console__row" + (r.status >= 400 || !r.status ? " preview-console__row--error" : "");
      var st = document.createElement("span"); st.className = "st"; st.textContent = r.status || "ERR";
      d.appendChild(st); d.appendChild(document.createTextNode(" " + r.method + " " + describe(pathOf(r.url)) + "  " + r.ms + " ms"));
    }
    body.appendChild(d);
  });
  body.scrollTop = body.scrollHeight;
}

function toggleConsole(force) {
  consoleOn = typeof force === "boolean" ? force : !consoleOn;
  var box = el("preview-console"), btn = el("preview-console-button");
  if (box) box.hidden = !consoleOn;
  if (btn) btn.setAttribute("aria-expanded", consoleOn ? "true" : "false");
  renderConsole();
}

function onKey(key) {
  if (key === "terminal") { if (typeof window.ToggleTerminal === "function") { if (open) showDrawer(); else window.ToggleTerminal(); } }
  else if (key === "preview") closePane();
  else if (key === "address") focusAddress();
  else if (key === "focus") focusNext();
  else if (key === "escape") { try { el("preview-pane").focus(); } catch (e) {} focusEditor(); }
}
function focusEditor() { var e = window["editor"]; if (e && e.env && e.env.editor) e.env.editor.focus(); }
// F6 moves between the editor, the pane's toolbar and the page
function focusNext() {
  var bar = el("preview-reload"), frame = el("preview-frame");
  var a = document.activeElement;
  if (a === frame) focusEditor();
  else if (a && el("preview-pane") && el("preview-pane").contains(a)) { try { frame.focus(); } catch (e) {} }
  else if (bar) bar.focus();
}

// ---- the address bar --------------------------------------------------------------------------------------------------

function focusAddress() {
  var input = el("preview-address-input"), text = el("preview-address-text");
  if (!input || !text) return;
  input.value = text.textContent === "Preview" ? "" : text.textContent;
  text.hidden = true; input.hidden = false;
  input.focus(); input.select();
}
function leaveAddress() {
  var input = el("preview-address-input"), text = el("preview-address-text");
  if (input) input.hidden = true;
  if (text) text.hidden = false;
}
function goTyped(raw) {
  var text = String(raw || "").trim();
  if (!text) return;
  var m = /^(?:https?:\/\/)?(?:(?:127\.0\.0\.1|localhost):)?(\d{4,5})(\/.*)?$/.exec(text);
  if (m && +m[1] >= 1024 && +m[1] <= 65535) { show({ kind: "p", port: +m[1], sub: m[2] || "/" }); return; }
  var path = text.charAt(0) === "/" && text.indexOf(home()) === 0 ? text : home() + "/" + text.replace(/^\/+/, "");
  if (canShow(path)) show({ kind: "f", path: path });
  else message("<strong>Nothing to show at that address.</strong><br>Type a file of your workspace, or a port such as 5000 or 5000/api.", null);
}

function goBack() { var tb = tab(); if (!tb || tb.idx <= 0) return; tb.idx--; goHist(tb); }
function goForward() { var tb = tab(); if (!tb || tb.idx >= tb.hist.length - 1) return; tb.idx++; goHist(tb); }
function goHist(tb) {
  var frame = frameFor(target);
  navTo = renew(tb.hist[tb.idx]);
  frame.setAttribute("data-url", navTo);
  setStatus("loading"); hideError(); clearLogs();
  frame.src = navTo;
  setAddress(describe(navTo)); render();
}

// ---- the menu, the pop-out window ---------------------------------------------------------------------------------------

function toggleMenu(force) {
  var m = el("preview-menu"), b = el("preview-menu-button");
  if (!m) return;
  var show = typeof force === "boolean" ? force : m.hidden;
  m.hidden = !show;
  if (b) b.setAttribute("aria-expanded", show ? "true" : "false");
  if (show) {
    el("preview-opt-auto").checked = optAuto; el("preview-opt-run").checked = optRun;
    var first = m.querySelector("input,button"); if (first) first.focus();
  }
}

function copyAddress() {
  var tb = tab();
  var url = tb && tb.hist.length ? location.origin + renew(tb.hist[tb.idx]) : "";
  var done = function () { if (window.notify) window.notify("Address copied.", { type: "success" }); };
  if (!url) return;
  if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(url).then(done, function () {});
}

function popOut() {
  if (!target) return;
  getToken().then(function (tok) {
    var tb = tab();
    var url = location.origin + (tb && tb.hist.length ? renew(tb.hist[tb.idx]) : urlFor(target, tok));
    popout = window.open(url, "openrepl-preview", "popup=yes,width=1000,height=720");
    if (!popout) { message("<strong>The window was blocked.</strong><br>Allow pop-ups for this site, then try again.", null); return; }
    var frame = frameFor(target); frame.removeAttribute("src");
    message("<strong>The preview is in its own window.</strong><br>It reloads there when you save or press Run.", { label: "Bring it back", run: bringBack });
    var watch = setInterval(function () { if (!popout || popout.closed) { clearInterval(watch); if (popout) { popout = null; bringBack(); } } }, 1000);
  });
}
function bringBack() {
  if (popout && !popout.closed) { try { popout.close(); } catch (e) {} }
  popout = null; message(""); var tb = tab();
  if (tb && tb.hist.length) goHist(tb); else load();
}

// Run: the pane stays and the terminal comes up under it; the page reloads (its program may need a moment to restart)
function onRun() {
  if (!open) return;
  showDrawer();
  if (optRun) { [1200, 4000].forEach(function (ms) { setTimeout(function () { if (open) reload(); }, ms); }); }
}

function applySize() {
  var device = el("preview-device");
  var stage = el("preview-stage");
  if (!device || !stage) return;
  var dims = SIZES[size];
  device.classList.toggle("is-sized", !!dims);
  if (!dims) {
    device.style.width = "100%"; device.style.height = "100%"; device.style.transform = "";
    return;
  }
  // a page of a phone or a tablet is drawn at its own size and scaled down to fit the pane
  var scale = Math.min(1, (stage.clientWidth - 16) / dims[0], (stage.clientHeight - 16) / dims[1]);
  device.style.width = dims[0] + "px";
  device.style.height = dims[1] + "px";
  device.style.transform = scale < 1 ? "scale(" + scale + ")" : "";
  device.style.marginTop = "8px";
}

function load() {
  if (!target) return;
  var frame = frameFor(target);
  message(""); hideError(); clearLogs(); setStatus("loading");
  getToken().then(function (tok) {
    var url = urlFor(target, tok);
    navTo = "";
    frame.setAttribute("data-url", url);
    frame.src = url;
    if (popout && !popout.closed) { try { popout.location.href = location.origin + url; } catch (e) {} }
  }, function (e) {
    message("<strong>The preview is not available.</strong><br>" + esc(e && e.message ? e.message : e), null);
  });
}

// A PDF is shown by the browser's own viewer, which does not work in a sandboxed frame: it has a frame of its own
// (a PDF is sent as a PDF and nothing else, so nothing in it can run as a page).
function frameFor(t) {
  var pdf = !!t && t.kind === "f" && previewKind(t.path) === "pdf";
  var page = el("preview-frame");
  var viewer = el("preview-pdf-frame");
  if (viewer) viewer.hidden = !pdf;
  if (page) page.hidden = pdf;
  var frame = pdf ? viewer : page;
  var other = pdf ? page : viewer;
  if (other) { other.removeAttribute("src"); other.removeAttribute("data-url"); }
  return frame;
}

function reload() {
  if (!open || !target) return;
  var frame = frameFor(target);
  var url = frame.getAttribute("data-url");
  if (!url) { load(); return; }
  // a token that has ended gets a new address
  if (token && token.expires - Date.now() <= TOKEN_MARGIN) { load(); return; }
  var tb = tab();
  if (tb && tb.hist.length) { url = renew(tb.hist[tb.idx]); navTo = url; frame.setAttribute("data-url", url); } // the page now shown, not the first one
  setStatus("loading"); hideError(); clearLogs();
  if (popout && !popout.closed) { try { popout.location.href = location.origin + url; } catch (e) {} }
  frame.src = url; // the same address again reloads the page
}

function syncButton() {
  var b = el("preview-button");
  if (!b) return;
  // for an .html file, or while a program of the workspace listens on a port
  b.hidden = disabled() || !(canShow(fileOf()) || knownPorts.length > 0 || open);
  if (!b.hidden) firstUse();
  b.setAttribute("aria-pressed", open ? "true" : "false");
  var dot = el("preview-dot");
  if (dot) dot.hidden = knownPorts.length === 0;
  b.title = knownPorts.length ? "Preview (a program listens on port " + knownPorts[0] + ")" : "Preview this page";
}

// On a phone the pane is a third tab beside Editor and Terminal (board 10)
function mobileTab(on) {
  var b = el("mobile-view-preview");
  if (!b) return;
  b.hidden = !on;
  if (!window.mobileActive || typeof window.setMobileView !== "function") return;
  if (on) window.setMobileView("preview");
  else if (b.getAttribute("aria-selected") === "true") window.setMobileView("terminal");
}

// The first time the button shows: one hint, once for each browser
function firstUse() {
  var seen = false;
  try { seen = localStorage.getItem("preview-hint") === "1"; localStorage.setItem("preview-hint", "1"); } catch (e) { seen = true; }
  if (seen || open || !window.notify) return;
  window.notify("Open an HTML file, or run a web server, and Preview shows it beside your code.", {
    type: "info", variant: "program", title: "See your web page here", timeout: 0,
    actions: [
      { label: "Show me", primary: true, onClick: function () { openPane(); } },
      { label: "Got it", onClick: function () {} }
    ]
  });
}

function openPane(path) {
  if (disabled()) return;
  var file = path || fileOf();
  if (canShow(file)) return show({ kind: "f", path: String(file) });
  // no page to show: a program that listens is, else the list of ports
  if (knownPorts.length) return show({ kind: "p", port: knownPorts[0] });
  show(null);
  togglePorts(true);
}

function openPort(port) {
  if (disabled()) return;
  show({ kind: "p", port: Number(port) });
}

function show(t) {
  // the pane takes the terminal's place: the terminal is shown first if it was hidden, so that there is a place
  if (typeof window.ShowTerminal === "function") window.ShowTerminal();
  useTab(t);
  target = t;
  open = true;
  var term = el("terminal-div");
  var pane = el("preview-pane");
  if (!term || !pane) return;
  pane.hidden = false;
  term.classList.add("is-previewing");
  setAddress();
  applySize();
  if (target) { var tb = tab(); if (tb && tb.hist.length && !t.sub) { var f = frameFor(target); navTo = renew(tb.hist[tb.idx]); f.setAttribute("data-url", navTo); setStatus("loading"); f.src = navTo; setAddress(describe(navTo)); } else load(); }
  else { message("<strong>Nothing to show yet.</strong><br>Run a program that listens on a port, or open an .html or Markdown file, a picture or a PDF.", null); setStatus("message"); }
  render();
  mobileTab(true);
  syncButton();
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 50);
}

function closePane() {
  if (!open) return;
  open = false;
  var term = el("terminal-div");
  var pane = el("preview-pane");
  if (pane) pane.hidden = true;
  togglePorts(false);
  if (term) { term.classList.remove("is-previewing"); term.classList.remove("with-drawer"); }
  mobileTab(false);
  toggleMenu(false); hideError();
  if (popout && !popout.closed) { try { popout.close(); } catch (e) {} }
  popout = null;
  ["preview-frame", "preview-pdf-frame"].forEach(function (id) {
    var frame = el(id);
    if (frame) { frame.removeAttribute("src"); frame.removeAttribute("data-url"); }
  });
  syncButton();
  // the terminal draws itself again at its size
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 50);
}

// A file of the workspace was saved or written: the page reloads, a moment after the last change
function changed(path) {
  if (!open || !target || target.kind !== "f" || !optAuto) return;
  var dir = target.path.slice(0, target.path.lastIndexOf("/") + 1);
  if (String(path).indexOf(dir) !== 0) return;
  clearTimeout(reloadTimer);
  reloadTimer = setTimeout(reload, 400);
}

function init() {
  var b = el("preview-button");
  if (!b || b.dataset.ready) return;
  b.dataset.ready = "1";
  try { var saved = localStorage.getItem(SIZE_KEY); if (saved && SIZES.hasOwnProperty(saved)) size = saved; } catch (e) {}
  var sel = el("preview-size");
  if (sel) {
    sel.value = size;
    sel.addEventListener("change", function () {
      size = sel.value;
      try { localStorage.setItem(SIZE_KEY, size); } catch (e) {}
      applySize();
    });
  }
  b.addEventListener("click", function () { if (open) closePane(); else openPane(); });
  el("preview-ports-button").addEventListener("click", function () { togglePorts(); });
  el("preview-file-button").addEventListener("click", function () { var f = fileOf(); if (canShow(f)) { show({ kind: "f", path: f }); togglePorts(false); } });
  el("preview-ports-form").addEventListener("submit", function (e) {
    e.preventDefault();
    var n = parseInt(el("preview-port-input").value, 10);
    if (n >= 1024 && n <= 65535) { openPort(n); togglePorts(false); }
  });
  el("preview-reload").addEventListener("click", function () { reload(); });
  el("preview-close").addEventListener("click", closePane);
  el("preview-terminal").addEventListener("click", closePane);
  el("preview-newtab").addEventListener("click", function () {
    if (!target) return;
    getToken().then(function (tok) { window.open(urlFor(target, tok), "_blank", "noopener"); });
  });
  window.addEventListener("resize", function () { if (open) applySize(); });
  var mt = el("mobile-view-terminal");
  if (mt) mt.addEventListener("click", function () { if (open && window.mobileActive) closePane(); }, true);
  el("preview-back").addEventListener("click", goBack);
  el("preview-forward").addEventListener("click", goForward);
  var fr = el("preview-frame");
  if (fr) fr.addEventListener("load", function () {
    if (!fr.getAttribute("src")) return;
    setStatus("live");
    var u = fr.getAttribute("data-url");
    var tb = tab();
    if (tb && target && target.kind === "p") checkProgram(tb.hist.length ? renew(tb.hist[tb.idx]) : u);
  });
  window.addEventListener("message", function (e) {
    var d = e.data;
    var frame = el("preview-frame");
    if (!d || typeof d !== "object" || !d.openrepl || !frame || e.source !== frame.contentWindow) return;
    var tb = tab();
    if (!tb) return;
    if (d.openrepl === "nav") onNav(tb, String(d.href || ""), String(d.title || ""));
    else if (d.openrepl === "console") addLog(tb, { level: ["log", "info", "warn", "error", "debug"].indexOf(d.level) >= 0 ? d.level : "log", text: String(d.text || "").slice(0, 2000) });
    else if (d.openrepl === "request") addReq(tb, { method: String(d.method || "GET").slice(0, 8), url: String(d.url || "").slice(0, 500), status: +d.status || 0, ms: +d.ms || 0 });
    else if (d.openrepl === "key") onKey(String(d.key));
  });
  var addr = el("preview-address"), input = el("preview-address-input");
  if (addr && input) {
    addr.addEventListener("click", function () { if (input.hidden) focusAddress(); });
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); var v = input.value; leaveAddress(); goTyped(v); }
      else if (e.key === "Escape") { e.preventDefault(); leaveAddress(); }
    });
    input.addEventListener("blur", leaveAddress);
  }
  el("preview-console-button").addEventListener("click", function () { toggleConsole(); });
  el("preview-console-close").addEventListener("click", function () { toggleConsole(false); });
  el("preview-console-clear").addEventListener("click", function () { var tb = tab(); if (!tb) return; if (consoleTab === "console") { tb.logs = []; tb.errs = 0; } else tb.reqs = []; renderConsole(); });
  [["console", "preview-tab-console"], ["requests", "preview-tab-requests"]].forEach(function (x) {
    el(x[1]).addEventListener("click", function () {
      consoleTab = x[0];
      el("preview-tab-console").classList.toggle("is-on", consoleTab === "console"); el("preview-tab-console").setAttribute("aria-selected", consoleTab === "console" ? "true" : "false");
      el("preview-tab-requests").classList.toggle("is-on", consoleTab === "requests"); el("preview-tab-requests").setAttribute("aria-selected", consoleTab === "requests" ? "true" : "false");
      renderConsole();
    });
  });
  el("preview-menu-button").addEventListener("click", function (e) { e.stopPropagation(); toggleMenu(); });
  document.addEventListener("click", function (e) { var m = el("preview-menu"); if (m && !m.hidden && !m.contains(e.target)) toggleMenu(false); });
  el("preview-menu").addEventListener("keydown", function (e) { if (e.key === "Escape") { toggleMenu(false); el("preview-menu-button").focus(); } });
  el("preview-opt-auto").addEventListener("change", function (e) { optAuto = e.target.checked; saveOpt("preview-opt-auto", optAuto); setStatus("live"); });
  el("preview-opt-run").addEventListener("change", function (e) { optRun = e.target.checked; saveOpt("preview-opt-run", optRun); setStatus("live"); });
  el("preview-menu-copy").addEventListener("click", function () { toggleMenu(false); copyAddress(); });
  el("preview-menu-popout").addEventListener("click", function () { toggleMenu(false); popOut(); });
  el("preview-menu-cookies").addEventListener("click", function () {
    toggleMenu(false);
    fetch(preprocessurl("/preview_cookies?v=" + viewer + (target && target.kind === "p" ? "&port=" + target.port : "")), { method: "POST", credentials: "same-origin" })
      .then(function () { if (window.notify) window.notify("The cookies of this preview are cleared.", { type: "success" }); reload(); }, function () {});
  });
  // with the terminal as a drawer, its Hide closes the drawer and keeps the pane
  var hideBtn = el("hide-terminal-button");
  if (hideBtn) hideBtn.addEventListener("click", function (e) {
    var term = el("terminal-div");
    if (open && term && term.classList.contains("with-drawer")) { e.stopImmediatePropagation(); e.preventDefault(); hideDrawer(); }
  }, true);
  // the keyboard: Ctrl+Alt+V opens or closes the pane (Ctrl+Shift+V is the browsers' "paste as text"), F6 moves between the
  // editor and the pane, and Ctrl+L goes to the address bar while the pane has the focus
  document.addEventListener("keydown", function (e) {
    var k = e.key;
    if (e.ctrlKey && e.altKey && !e.shiftKey && !e.metaKey && (k === "v" || k === "V" || e.code === "KeyV")) { e.preventDefault(); if (open) closePane(); else openPane(); return; }
    if (k === "F6" && open) { e.preventDefault(); focusNext(); return; }
    if (open && e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey && (k === "l" || k === "L")) { var p = el("preview-pane"); if (p && p.contains(document.activeElement)) { e.preventDefault(); focusAddress(); } }
  });
  // a program that already listens when the page opens (the pane's button shows a dot), and one that starts later: the
  // workspace is asked every 20 seconds while the page is in view and the pane is closed (a short answer, read from the
  // server's own tables; nothing else is asked of the server for this)
  askPorts();
  setInterval(function () { if (!open && !document.hidden && !disabled()) askPorts().then(offer); }, 8000);
  // the terminal printed where a program listens: ask at once, and again after a moment (it may still be starting)
  var hintTimers = [];
  window.addEventListener("terminal-port-hint", function () {
    if (open || disabled() || hintTimers.length) return;
    hintTimers = [300, 2500].map(function (ms) {
      return setTimeout(function () { hintTimers = []; if (!open && !disabled()) askPorts().then(offer); }, ms);
    });
  });
  document.addEventListener("visibilitychange", function () { if (!document.hidden && !open && !disabled()) askPorts().then(offer); });
  // the button follows the file in the editor
  var timer = setInterval(function () {
    var e = window["editor"];
    if (!(e && e.env && e.env.editor)) return;
    clearInterval(timer);
    var last = "";
    setInterval(function () { var f = fileOf(); if (f !== last) { last = f; syncButton(); } }, 400);
    syncButton();
  }, 300);
}

window.Preview = {
  open: openPane,
  close: closePane,
  isOpen: function () { return open; },
  available: function () { return !disabled(); },
  changed: changed,
  // Run on an .html file shows the page instead of starting a program
  runs: function () { return !disabled() && isHtml(fileOf()); },
  canPreview: function (path) { return !disabled() && canShow(path); },
  openPort: openPort,
  onRun: onRun,
  hasDrawer: function () { var t = el("terminal-div"); return !!t && t.classList.contains("with-drawer"); },
  state: function () { var tb = tab(); return { open: open, target: target, tabs: tabs.length, address: tb && tb.hist.length ? describe(tb.hist[tb.idx]) : "", errors: tb ? tb.errs : 0, console: tb ? tb.logs.slice(-20) : [], requests: tb ? tb.reqs.slice(-20) : [], ports: knownPorts.slice() }; },
  openPath: function (path) { if (canShow(path)) show({ kind: "f", path: String(path) }); return canShow(path); },
  reload: reload,
  console: function (on) { toggleConsole(on); },
  watchAfterRun: watchAfterRun,
  refresh: function () { return askPorts().then(function (p) { offer(p); return p; }); },
  ports: function () { return knownPorts.slice(); }
};

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
