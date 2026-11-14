// The App Store (docs/lld/18-app-store.md): libraries and tools that a signed-in user switches on for their terminals. They are
// installed once on the server, not in the workspace; this page only asks the server (/store/api/...) and shows what it says.
// Part of the page script: webpack bundles src/js/src/page/*.js into js/scribbler.js.

import { missingFrom } from "./lib-store-hint.mjs";

var tab = "browse";          // browse | mine
var views = [];              // what the server says: one for each app
var onList = [];             // the bundles that are on for this user
var envScript = "";          // the file a shell can run to use what was switched on
var installer = "";
var query = "";
var lookups = {};            // name -> { info } | { error } for a name typed that the store does not list
var changed = false;         // the user switched something since the dialog opened: open terminals keep what they had
var watching = {};           // apps that are being installed: a notice says when they are done
var pollTimer = 0;
var loaded = false;

function el(id) { return document.getElementById(id); }
function signedIn() { return document.body.classList.contains("is-signed-in"); }
function h(tag, cls, text) {
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}
function url(path) { return typeof window.preprocessurl === "function" ? window.preprocessurl(path) : path; }

function api(path, body) {
  var opts = { credentials: "same-origin", cache: "no-store" };
  if (body) { opts.method = "POST"; opts.headers = { "Content-Type": "application/json" }; opts.body = JSON.stringify(body); }
  return fetch(url("/store/api/" + path), opts).then(function (res) {
    return res.json().catch(function () { return {}; }).then(function (j) {
      if (!res.ok) { var e = new Error(j.message || j.error || "The App Store did not answer."); e.status = res.status; e.code = j.error; throw e; }
      return j;
    });
  });
}

function say(text, bad) {
  var n = el("appstore-note");
  if (!n) return;
  n.hidden = !text;
  n.textContent = text || "";
  n.classList.toggle("is-bad", !!bad);
}

function take(j) {
  if (j.views) views = j.views;
  if (j.on) onList = j.on;
  if (j.envScript) envScript = j.envScript;
  if (j.installer) installer = j.installer;
  loaded = true;
  schedulePoll();
}

function refresh() {
  return api("list").then(function (j) { take(j); render(); }, function (e) { loaded = false; render(e); });
}

// ---- watching an install --------------------------------------------------------------------------------------------------

function busy() { return views.some(function (v) { return v.state === "installing"; }); }

function schedulePoll() {
  views.forEach(function (v) { if (v.state === "installing" && !watching[v.name]) watching[v.name] = true; });
  clearTimeout(pollTimer);
  if (!busy()) return;
  pollTimer = setTimeout(function () {
    api("list").then(function (j) {
      var before = views;
      take(j);
      // an app that was being installed is done (or failed): say so even if the dialog is closed
      views.forEach(function (v) {
        if (!watching[v.name] || v.state === "installing") return;
        var mode = watching[v.name];
        delete watching[v.name];
        if (v.state === "installed" && mode === "on") {
          // asked with "Install and turn on": it is switched on now
          switchApp(v, true);
          if (window.notify) window.notify(v.name + " is installed and on. New terminals have it; open ones need the line from My apps.", { type: "success", title: "App Store" });
        } else if (v.state === "installed" && window.notify) {
          window.notify(v.name + " is installed.", { type: "success", variant: "program", title: "App Store", timeout: 0, actions: [
            { label: "Turn it on", primary: true, onClick: function () { switchApp(v, true); } },
            { label: "Later", onClick: function () {} }
          ] });
        } else if (v.state === "failed" && window.notify) {
          window.notify(v.name + " could not be installed. Open the App Store to see why.", { type: "error", title: "App Store" });
        }
      });
      if (isOpen()) render();
    }, function () { schedulePoll(); });
  }, 1800);
}

// ---- actions --------------------------------------------------------------------------------------------------------------

function act(path, body, ok) {
  say("");
  return api(path, body).then(function (j) { take(j); if (ok) say(ok); render(); }, function (e) {
    if (e.code === "signin") { render(e); return; }
    say(e.message, true);
  });
}

function install(v) { return act("install", { name: v.name, kind: v.kind || "lib" }); }

// "Install and turn on": a safe app is installed and, when it is done, switched on (an installed one is only switched on)
function installAndOn(name) {
  name = String(name || "").toLowerCase();
  return api("list").then(function (j) {
    take(j);
    var v = views.filter(function (x) { return x.name === name; })[0];
    if (!v) throw new Error(name + " is not in the App Store");
    if (v.state === "installed") return switchApp(v, true).then(function () { return "on"; });
    if (v.state === "installing") { watching[name] = "on"; schedulePoll(); return "installing"; }
    if (v.state !== "available") throw new Error(name + " needs an admin: open the App Store and Request it");
    watching[name] = "on";
    return api("install", { name: name, kind: v.kind || "lib" }).then(function (r) { take(r); return "installing"; });
  });
}
function request(v) {
  var reason = window.prompt ? window.prompt("Why do you need " + v.name + "? (optional, one line)", "") : "";
  if (reason === null) return;
  return act("request", { name: v.name, kind: v.kind || "lib", reason: reason }, "Asked. An admin will decide; you will see the answer here.");
}
function switchApp(v, on) {
  changed = true;
  return act("switch", { id: v.id, on: on }, on ? v.name + " is on for your terminals." : v.name + " is off for your terminals.");
}
function report(v) {
  var note = window.prompt ? window.prompt("Tell the admin what you were trying to do (optional)", "") : "";
  if (note === null) return;
  return act("report", { name: v.name, note: note }, "Sent. The admin will look at it, and you will see the answer here.");
}
function applyHere() {
  if (!envScript || typeof window.sendToActiveTerminal !== "function") { say("Open a terminal first, then try again.", true); return; }
  if (window.confirm && !window.confirm("This types a line into your active terminal that loads your apps. Do it now? (Do not do it while a program is running in it.)")) return;
  window.sendToActiveTerminal(". '" + envScript.replace(/'/g, "'\\''") + "'\r");
  changed = false;
  say("Done. Your active terminal uses what is on now.");
  render();
}

function lookup(name) {
  if (lookups[name]) return;
  lookups[name] = { loading: true };
  api("info?name=" + encodeURIComponent(name)).then(function (info) { lookups[name] = { info: info }; render(); },
    function (e) { lookups[name] = { error: e.message }; render(); });
}

// ---- drawing --------------------------------------------------------------------------------------------------------------

var STATE_TEXT = { available: "", installing: "Installing", installed: "Installed", failed: "Failed", requested: "Waiting for an admin", rejected: "Not available" };

function card(v) {
  var c = h("div", "appstore__app is-" + v.state);
  c.setAttribute("data-app", v.name);
  var top = h("div", "appstore__top");
  top.appendChild(h("b", "appstore__name", v.name));
  if (v.version) top.appendChild(h("span", "appstore__ver", v.version));
  if (v.kind === "tool") top.appendChild(h("span", "appstore__tag", "tool"));
  if (STATE_TEXT[v.state]) top.appendChild(h("span", "appstore__tag appstore__tag--" + v.state, STATE_TEXT[v.state]));
  if (v.state === "installed" && v.sizeMB) top.appendChild(h("span", "appstore__size", v.sizeMB.toFixed(v.sizeMB < 10 ? 1 : 0) + " MB"));
  c.appendChild(top);
  var info = lookups[v.name];
  if (info && info.info && info.info.summary) c.appendChild(h("p", "appstore__sum", info.info.summary));
  if (v.state === "installing") c.appendChild(h("p", "appstore__sum", (v.step || "Starting") + "…"));
  if (v.state === "failed") {
    c.appendChild(h("p", "appstore__sum", v.err || "The install stopped."));
    if (v.log) { var pre = h("pre", "appstore__log", v.log); c.appendChild(pre); }
  }
  if (v.state === "rejected" && v.answer) c.appendChild(h("p", "appstore__sum", v.answer));
  var row = h("div", "appstore__row");
  function button(text, fn, primary) {
    var b = h("button", "appstore__btn" + (primary ? " is-primary" : ""), text);
    b.type = "button";
    b.addEventListener("click", fn);
    row.appendChild(b);
    return b;
  }
  if (v.state === "available") button("Install", function () { install(v); }, true);
  if (v.state === "installed") {
    var sw = h("button", "appstore__switch" + (v.on ? " is-on" : ""), v.on ? "On" : "Off");
    sw.type = "button"; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", v.on ? "true" : "false");
    sw.setAttribute("aria-label", "Use " + v.name + " in my terminals");
    sw.addEventListener("click", function () { switchApp(v, !v.on); });
    row.appendChild(sw);
  }
  if (v.state === "failed") {
    button("Try again", function () { install(v); }, true);
    if (v.reported) row.appendChild(h("span", "appstore__tag", v.mine ? "You told the admin" : "Reported, the admin is looking at it"));
    else button("Report to admin", function () { report(v); });
  }
  if (v.state === "rejected") button("Ask again", function () { request(v); });
  c.appendChild(row);
  return c;
}

function lookupCard(name) {
  var l = lookups[name];
  var c = h("div", "appstore__app is-lookup");
  c.setAttribute("data-app", name);
  var top = h("div", "appstore__top");
  top.appendChild(h("b", "appstore__name", name));
  top.appendChild(h("span", "appstore__tag", "conda-forge"));
  c.appendChild(top);
  if (!l || l.loading) { c.appendChild(h("p", "appstore__sum", "Looking it up…")); lookup(name); return c; }
  if (l.error) { c.appendChild(h("p", "appstore__sum", l.error)); return c; }
  c.appendChild(h("p", "appstore__sum", (l.info.summary || "No description.") + (l.info.version ? " Newest: " + l.info.version + "." : "")));
  var row = h("div", "appstore__row");
  var b = h("button", "appstore__btn is-primary", "Request " + name);
  b.type = "button";
  b.addEventListener("click", function () { request({ name: name, kind: "lib" }); });
  row.appendChild(b);
  c.appendChild(row);
  return c;
}

function render(err) {
  var list = el("appstore-list"), foot = el("appstore-foot");
  if (!list) return;
  list.textContent = "";
  ["browse", "mine"].forEach(function (t) {
    var b = el("appstore-tab-" + t);
    if (b) { b.setAttribute("aria-selected", tab === t ? "true" : "false"); b.classList.toggle("is-on", tab === t); }
  });
  var tools = el("appstore-tools"); if (tools) tools.hidden = tab !== "browse";
  if (!signedIn() || (err && err.code === "signin")) {
    var g = h("div", "appstore__guest");
    g.appendChild(h("b", "", "The App Store is for signed-in users"));
    g.appendChild(h("span", "", "Sign in to add libraries and tools to your terminals."));
    var sb = h("button", "appstore__btn is-primary", "Sign in");
    sb.type = "button";
    sb.addEventListener("click", function () { close(); if (typeof window.openSignIn === "function") window.openSignIn(); });
    g.appendChild(sb);
    list.appendChild(g);
    if (foot) foot.textContent = "";
    return;
  }
  if (err) { list.appendChild(h("p", "appstore__sum", err.message || "The App Store is not available.")); return; }
  if (!loaded) { list.appendChild(h("p", "appstore__sum", "Loading…")); return; }
  var q = query.trim().toLowerCase();
  var shown = views.filter(function (v) {
    if (tab === "mine") return v.on || v.state === "requested" || v.state === "rejected" || (v.state === "failed" && v.mine) || (v.state === "installing" && v.mine);
    return !q || v.name.indexOf(q) >= 0;
  });
  shown.forEach(function (v) { list.appendChild(card(v)); });
  // a name that nothing in the store matches can still be looked up on conda-forge and asked for
  if (tab === "browse" && q && !shown.length && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(q)) list.appendChild(lookupCard(q));
  if (!shown.length && !(tab === "browse" && q)) list.appendChild(h("p", "appstore__sum", tab === "mine" ? "Nothing is on for you yet. Browse the store and switch an app on." : "No apps yet."));
  if (foot) {
    foot.textContent = "";
    if (changed && tab === "mine") {
      var note = h("div", "appstore__apply");
      note.appendChild(h("span", "", "You changed what is on. Terminals that are open keep what they had; new ones use it."));
      var ab = h("button", "appstore__btn", "Use it in my active terminal");
      ab.type = "button"; ab.addEventListener("click", applyHere);
      note.appendChild(ab);
      foot.appendChild(note);
    }
    foot.appendChild(h("span", "appstore__help", "Apps are installed once on the server and shared. Switching one on only changes your own terminals."));
  }
}

// ---- opening and closing --------------------------------------------------------------------------------------------------

function isOpen() { var d = el("appstore-dialog"); return !!d && d.open; }
function close() { var d = el("appstore-dialog"); if (d && d.open) d.close(); }

function open(name) {
  var d = el("appstore-dialog");
  if (!d) return;
  if (!d.open) { if (d.showModal) d.showModal(); else d.setAttribute("open", ""); }
  tab = "browse";
  query = name ? String(name).toLowerCase() : "";
  var s = el("appstore-search"); if (s) s.value = query;
  say("");
  render();
  if (signedIn()) refresh();
}

// ---- the terminal's own error: "No module named flask" -----------------------------------------------------------------------

var hinted = {};   // names that were offered already in this visit: one notice for each
var hintBusy = false;

function terminalTail() {
  var rows = document.querySelector(".terminal.active .xterm-rows") || document.querySelector(".xterm-rows");
  return rows ? rows.innerText : "";
}

var noticesDone = false;

// what the admin decided about this user's requests and reports (board 4D): one notice each, until it has been seen
function checkNotices() {
  if (noticesDone || !signedIn() || !window.notify) return;
  noticesDone = true;
  api("notices").then(function (j) {
    (j.notices || []).forEach(function (n) {
      window.notify(n.text, { type: n.status === "rejected" ? "info" : "success", variant: "program", title: "App Store", timeout: 0, actions: [
        { label: "Open the App Store", primary: true, onClick: function () { open(n.name); } },
        { label: "Later", onClick: function () {} }
      ] });
      api("ack", { name: n.name }).catch(function () {});
    });
  }, function () { noticesDone = false; });
}

function checkHint() {
  checkNotices();
  if (hintBusy || !signedIn() || !window.notify || isOpen()) return;
  var name = missingFrom(terminalTail());
  if (!name || hinted[name]) return;
  hintBusy = true;
  api("list").then(function (j) {
    take(j);
    var v = views.filter(function (x) { return x.name === name; })[0];
    // only an app the store has (the safe list, or already installed or asked for); a guess is never offered
    if (!v || v.state === "installing" || (v.state === "installed" && v.on)) return;
    hinted[name] = true;
    var text = v.state === "installed" ? name + " is installed, but not on for your terminals." : name + " is in the App Store.";
    window.notify(text, { type: "info", variant: "program", title: "Missing something?", timeout: 0, actions: [
      { label: v.state === "installed" ? "Turn it on" : v.state === "available" ? "Install and turn on" : "Open the App Store", primary: true, onClick: function () {
        if (v.state === "installed") switchApp(v, true);
        else if (v.state === "available") installAndOn(name).then(function () { if (window.notify) window.notify("Installing " + name + "\u2026 it is switched on when it is done.", { type: "info", title: "App Store" }); }, function (e) { if (window.notify) window.notify(e.message, { type: "error", title: "App Store" }); });
        else open(name);
      } },
      { label: "Open the App Store", onClick: function () { open(name); } },
      { label: "Not now", onClick: function () {} }
    ] });
  }, function () {}).then(function () { hintBusy = false; });
}

function init() {
  var d = el("appstore-dialog");
  if (!d || d.dataset.ready) return;
  d.dataset.ready = "1";
  el("appstore-close").addEventListener("click", close);
  d.addEventListener("click", function (e) { if (e.target === d) close(); });
  el("appstore-tab-browse").addEventListener("click", function () { tab = "browse"; say(""); render(); });
  el("appstore-tab-mine").addEventListener("click", function () { tab = "mine"; say(""); render(); });
  el("appstore-search").addEventListener("input", function (e) { query = e.target.value; render(); });
  var b = el("appstore-button");
  if (b) b.addEventListener("click", function () { open(); });
  setInterval(checkHint, 3000);
}

window.AppStore = {
  // Genie's permission card and the terminal's notice: install a safe app and switch it on, when the user said yes
  installAndOn: installAndOn,
  open: open,
  // Genie and the terminal offer an app: the store opens on it, the user decides
  suggest: function (name) { open(name); },
  available: function () { return signedIn(); },
  isOpen: isOpen,
  state: function () { return { views: views.slice(), on: onList.slice(), tab: tab, installer: installer }; }
};

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();

export {};
