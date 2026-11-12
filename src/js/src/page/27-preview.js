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
  tokenPending = fetch(preprocessurl("/preview_token"), { credentials: "same-origin" })
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
  if (t.kind === "p") return "/preview/" + tok + "/p/" + t.port + "/";
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
  window.notify("Your program is listening on port " + port + ".", {
    type: "info", title: "Preview", timeout: 0, // stays until it is answered
    actions: [
      { label: "Open preview", onClick: function () { openPort(port); } },
      { label: "Not now", onClick: function () {} },
      { label: "Don't ask again", onClick: function () { try { localStorage.setItem(noAskKey(), "1"); } catch (e) {} } }
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

function setAddress() {
  var a = el("preview-address-text");
  if (!a) return;
  a.textContent = target ? (target.kind === "f" ? relative(target.path) : "127.0.0.1:" + target.port) : "Preview";
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
  message("");
  getToken().then(function (tok) {
    var url = urlFor(target, tok);
    frame.setAttribute("data-url", url);
    frame.src = url;
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
  frame.src = url; // the same address again reloads the page
}

function syncButton() {
  var b = el("preview-button");
  if (!b) return;
  // for an .html file, or while a program of the workspace listens on a port
  b.hidden = disabled() || !(canShow(fileOf()) || knownPorts.length > 0 || open);
  b.setAttribute("aria-pressed", open ? "true" : "false");
  var dot = el("preview-dot");
  if (dot) dot.hidden = knownPorts.length === 0;
  b.title = knownPorts.length ? "Preview (a program listens on port " + knownPorts[0] + ")" : "Preview this page";
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
  target = t;
  open = true;
  var term = el("terminal-div");
  var pane = el("preview-pane");
  if (!term || !pane) return;
  pane.hidden = false;
  term.classList.add("is-previewing");
  setAddress();
  applySize();
  if (target) load(); else message("<strong>Nothing to show yet.</strong><br>Run a program that listens on a port, or open an .html or Markdown file, a picture or a PDF.", null);
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
  if (term) term.classList.remove("is-previewing");
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
  if (!open || !target || target.kind !== "f") return;
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
  changed: changed,
  // Run on an .html file shows the page instead of starting a program
  runs: function () { return !disabled() && isHtml(fileOf()); },
  canPreview: function (path) { return !disabled() && canShow(path); },
  openPort: openPort,
  watchAfterRun: watchAfterRun,
  refresh: function () { return askPorts().then(function (p) { offer(p); return p; }); },
  ports: function () { return knownPorts.slice(); }
};

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
