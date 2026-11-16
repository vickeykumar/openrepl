// The App Store (docs/lld/18-app-store.md): libraries and tools that a signed-in user switches on for their terminals. They are
// installed once on the server, not in the workspace; this page only asks the server (/store/api/...) and shows what it says.
// Part of the page script: webpack bundles src/js/src/page/*.js into js/scribbler.js.

import { missingFrom } from "./lib-store-hint.mjs";
import { matches, chipsFor, languages, narrow, pushRecent, importsOf, needed } from "./lib-store-filter.mjs";

var tab = "browse";          // browse | mine
var views = [];              // what the server says: one for each app
var builtins = [];           // what the image has built in (listed in My apps, always on)
var onList = [];             // the bundles that are on for this user
var sandbox = false;         // terminals run in a sandbox that is fixed when they start
var envScript = "";          // the file a shell can run to use what was switched on
var installer = "";
var query = "";
var lookups = {};            // name -> { info } | { error } for a name typed that the store does not list
var checking = "";           // the bundle whose check is running (turning it on is checked first)
var useFail = null;          // {name, log, node}: an app that did not work when it was switched on, until it is reported or tried again
var changed = false;         // the user switched something since the dialog opened: open terminals keep what they had
var watching = {};           // apps that are being installed: a notice says when they are done
var pollTimer = 0;
var loaded = false;
var category = "";           // the chosen category chip ("" is All; "Built in" is the built-in programs of My apps)
var repl = "";               // the chosen language ("" is all)
var confirming = null;       // { v, act }: a big app that the user is asked about before it is installed or asked for

// the browser's storage may be blocked: what is kept there is only a convenience
function lsGet(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* no storage: nothing is remembered */ } }
function recentList() { try { var a = JSON.parse(lsGet("appstore-recent") || "[]"); return Array.isArray(a) ? a.filter(function (x) { return typeof x === "string"; }).slice(0, 3) : []; } catch (e) { return []; } }
function noteRecent(name) { lsSet("appstore-recent", JSON.stringify(pushRecent(recentList(), name, 3))); }
function setTab(t) { tab = t; lsSet("appstore-tab", t); }

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
      if (!res.ok) { var e = new Error(j.message || j.error || "The App Store did not answer."); e.status = res.status; e.code = j.error; e.body = j; throw e; }
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
  if (j.builtins) builtins = j.builtins;
  if (j.envScript) envScript = j.envScript;
  if (j.sandbox !== undefined) sandbox = !!j.sandbox;
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

// what the open file imports: its text and its language (the language menu of the editor)
function editorText() {
  try { var e = window["editor"]; var ed = e && e.env && e.env.editor; return ed ? String(ed.getValue()).slice(0, 200000) : ""; } catch (x) { return ""; }
}
function editorLang() { var s = el("select-lang"); return s ? String(s.value || "") : ""; }

// a big app asks first: its size on the disk and the memory it needs when it is used
function bigApp(v) { return (v.instMB || 0) > 150 || (v.ramMB || 0) >= 100; }
function guarded(what, v) {
  if (what !== "switch" && bigApp(v)) { confirming = { v: v, act: what }; render(); return; }
  if (what === "install") install(v); else if (what === "request") request(v);
}

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
  if (window.AppStorePlugins) setTimeout(function () { window.AppStorePlugins.refresh(); }, 600); // a plugin starts or stops at once
  if (!on) return act("switch", { id: v.id, on: false }, v.name + " is off for your terminals.");
  // turning it on is checked first: the server runs the app's own check for this user, on the server of their terminals
  if (checking) return Promise.resolve();
  checking = v.id; useFail = null; say("Checking that " + v.name + " works in your terminals…"); render();
  return api("switch", { id: v.id, on: true }).then(function (j) {
    checking = ""; take(j); noteRecent(v.name); say(v.name + " is on for your terminals. It was checked and works."); render();
  }, function (e) {
    checking = "";
    if (e.code === "verify" && e.body) {
      take(e.body);
      useFail = e.body.wait ? null : { name: v.name, log: e.body.log || "", node: e.body.node || "" };
      say(e.message, !e.body.wait);
      render();
    } else if (e.code === "signin") { render(e); } else { say(e.message, true); render(); }
    throw e;
  });
}
function report(v) {
  var note = window.prompt ? window.prompt("Tell the admin what you were trying to do (optional)", "") : "";
  if (note === null) return Promise.resolve();
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

var KIND_TEXT = { lib: "python", tool: "tool", node: "node", lang: "language", plugin: "plugin", py27: "python 2.7", gomod: "go", jar: "java", crate: "rust" };

// the name of an app opens its page in the App Store (a new tab: the editor and its terminals stay as they are)
function nameLink(name) {
  var a = h("a", "appstore__name", name);
  a.href = "./store#app/" + encodeURIComponent(name);
  a.target = "_blank";
  a.rel = "noopener";
  return a;
}

function card(v) {
  var c = h("div", "appstore__app is-" + v.state);
  c.setAttribute("data-app", v.name);
  var top = h("div", "appstore__top");
  top.appendChild(nameLink(v.name));
  top.appendChild(h("span", "appstore__tag", KIND_TEXT[v.kind || "lib"] || v.kind));
  if (v.version) top.appendChild(h("span", "appstore__ver", v.version));
  if (v.default && v.state === "installed") top.appendChild(h("span", "appstore__tag appstore__tag--installed", "on for everyone"));
  if (STATE_TEXT[v.state]) top.appendChild(h("span", "appstore__tag appstore__tag--" + v.state, v.state === "installing" && v.waiting ? "Waiting for another install" : STATE_TEXT[v.state]));
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
  if (v.use && (v.state === "installed" || v.state === "available" || v.state === "installing")) c.appendChild(h("code", "appstore__use", v.use)); // how to use it, one line
  if (v.summary && !(info && info.info)) c.appendChild(h("p", "appstore__sum", v.summary));
  var row = h("div", "appstore__row");
  function button(text, fn, primary) {
    var b = h("button", "appstore__btn" + (primary ? " is-primary" : ""), text);
    b.type = "button";
    b.addEventListener("click", fn);
    row.appendChild(b);
    return b;
  }
  if (v.state === "available") button(v.safe ? "Install" : "Request", function () { guarded(v.safe ? "install" : "request", v); }, true);
  if (v.state === "installed") {
    var sw = h("button", "appstore__switch" + (v.on ? " is-on" : ""), checking === v.id ? "Checking…" : v.on ? "On" : "Off");
    sw.type = "button"; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", v.on ? "true" : "false");
    if (checking === v.id) { sw.disabled = true; sw.title = "Checking that it works in your terminal environment"; }
    sw.setAttribute("aria-label", "Use " + v.name + " in my terminals");
    sw.addEventListener("click", function () { switchApp(v, !v.on).catch(function () {}); });
    row.appendChild(sw);
  }
  if (v.state === "installed" && useFail && useFail.name === v.name) {
    c.appendChild(h("p", "appstore__sum", "It was checked the way your terminals start it" + (useFail.node ? ", on " + useFail.node : "") + ", and failed, so it is off:"));
    if (useFail.log) c.appendChild(h("pre", "appstore__log", useFail.log));
    var fr = h("div", "appstore__row");
    var rb = h("button", "appstore__btn is-primary", "Report to admin"); rb.type = "button";
    rb.addEventListener("click", function () { var f = useFail; report(v).then(function () { if (useFail === f) useFail = null; render(); }); });
    var tb = h("button", "appstore__btn", "Try again"); tb.type = "button";
    tb.addEventListener("click", function () { switchApp(v, true).catch(function () {}); });
    fr.appendChild(rb); fr.appendChild(tb); c.appendChild(fr);
  }
  if (v.state === "failed") {
    button("Try again", function () { install(v); }, true);
    if (v.reported) row.appendChild(h("span", "appstore__tag", v.mine ? "You told the admin" : "Reported, the admin is looking at it"));
    else button("Report to admin", function () { report(v); });
  }
  if (v.state === "rejected") button("Ask again", function () { guarded("request", v); });
  if (v.docs) {
    var dl = h("a", "appstore__btn", "Docs"); dl.href = v.docs; dl.target = "_blank"; dl.rel = "noopener noreferrer"; row.appendChild(dl);
  }
  c.appendChild(row);
  return c;
}

// an app the image has built in: listed with the apps, tagged, always on, nothing to install or remove
function builtinCard(b) {
  var c = h("div", "appstore__app is-builtin");
  c.setAttribute("data-builtin", b.name);
  var top = h("div", "appstore__top");
  top.appendChild(h("span", "appstore__name", b.name));
  top.appendChild(h("span", "appstore__tag appstore__tag--builtin", "Built in"));
  if (b.version) top.appendChild(h("span", "appstore__ver", b.version));
  c.appendChild(top);
  if (b.used && b.used.length) c.appendChild(h("p", "appstore__sum", "Used by " + b.used.join(", ") + ". It comes with the server."));
  var row = h("div", "appstore__row");
  var sw = h("button", "appstore__switch is-on", "On"); sw.type = "button"; sw.disabled = true; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", "true");
  sw.title = "Built in: always on"; row.appendChild(sw);
  var u = h("button", "appstore__btn", "Uninstall"); u.type = "button"; u.disabled = true; u.title = "Built in: it comes with the server"; row.appendChild(u);
  if (b.docs) { var dl = h("a", "appstore__btn", "Docs"); dl.href = b.docs; dl.target = "_blank"; dl.rel = "noopener noreferrer"; row.appendChild(dl); }
  c.appendChild(row);
  return c;
}

function lookupCard(name) {
  var l = lookups[name];
  var c = h("div", "appstore__app is-lookup");
  c.setAttribute("data-app", name);
  var top = h("div", "appstore__top");
  top.appendChild(nameLink(name));
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

// ---- the filters, the suggestions, the question about a big app --------------------------------------------------------------

function chip(label, count, on, fn) {
  var b = h("button", "appstore__chip" + (on ? " is-on" : ""), label);
  b.type = "button";
  b.setAttribute("aria-pressed", on ? "true" : "false");
  b.setAttribute("data-category", label);
  if (count !== undefined) b.appendChild(h("b", "", String(count)));
  if (count === 0) b.classList.add("is-empty");
  b.addEventListener("click", fn);
  return b;
}

function renderFilters(chips, total, builtinCount, langs, shownCount) {
  var box = el("appstore-filters");
  if (!box) return;
  box.textContent = "";
  box.hidden = false;
  var head = h("div", "appstore__filterhead");
  head.appendChild(h("span", "appstore__cap", "Category"));
  if (category || repl) {
    var clr = h("button", "appstore__link", "Clear filter"); clr.type = "button";
    clr.addEventListener("click", function () { category = ""; repl = ""; render(); });
    head.appendChild(clr);
  }
  box.appendChild(head);
  var row = h("div", "appstore__chips"); row.setAttribute("role", "group"); row.setAttribute("aria-label", "Category");
  row.appendChild(chip("All", total + (tab === "mine" ? builtinCount : 0), !category, function () { category = ""; render(); }));
  chips.forEach(function (c) { row.appendChild(chip(c.name, c.count, category === c.name, function () { category = category === c.name ? "" : c.name; render(); })); });
  if (tab === "mine" && builtinCount) row.appendChild(chip("Built in", builtinCount, category === "Built in", function () { category = category === "Built in" ? "" : "Built in"; render(); }));
  // the arrow keys move along the chips; Tab leaves the group
  row.addEventListener("keydown", function (e) {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    var all = Array.prototype.slice.call(row.querySelectorAll("button")), i = all.indexOf(document.activeElement);
    if (i < 0) return;
    e.preventDefault();
    all[(i + (e.key === "ArrowRight" ? 1 : all.length - 1)) % all.length].focus();
  });
  box.appendChild(row);
  var langRow = h("div", "appstore__langrow");
  var lab = h("label", "appstore__cap", "Language ");
  var sel = h("select", "appstore__select"); sel.setAttribute("aria-label", "Language");
  var o0 = h("option", "", "all"); o0.value = ""; sel.appendChild(o0);
  langs.forEach(function (l) { var o = h("option", "", l); o.value = l; sel.appendChild(o); });
  sel.value = repl; if (sel.value !== repl) repl = "";
  sel.addEventListener("change", function () { repl = sel.value; render(); });
  lab.appendChild(sel);
  langRow.appendChild(lab);
  if (category || repl) {
    var n = category === "Built in" ? builtinCount : shownCount;
    langRow.appendChild(h("span", "appstore__showing", "Showing " + n + (n === 1 ? " app" : " apps") + (category ? " in " + category : "") + (repl ? " for " + repl : "") + "."));
  }
  box.appendChild(langRow);
}

function emptyFilter(q) {
  var c = h("div", "appstore__empty");
  c.setAttribute("data-empty", "1");
  var what = (q ? "Nothing called \u201c" + q + "\u201d" : "Nothing") + (category ? " in " + category : "") + (repl ? " for " + repl : "") + ".";
  c.appendChild(h("p", "appstore__sum", what));
  var row = h("div", "appstore__row");
  if (category && q) { var a = h("button", "appstore__btn", "Search all categories"); a.type = "button"; a.addEventListener("click", function () { category = ""; render(); }); row.appendChild(a); }
  var b = h("button", "appstore__btn", "Clear filter"); b.type = "button"; b.addEventListener("click", function () { category = ""; repl = ""; render(); }); row.appendChild(b);
  c.appendChild(row);
  return c;
}

// "Your file uses ...": apps the open file imports and that are not on; "Recently switched on": the last three apps the user used (My apps)
function renderSuggest() {
  var box = el("appstore-suggest");
  if (!box) return;
  box.textContent = "";
  var any = false;
  var need = needed(importsOf(editorText(), editorLang()), views).slice(0, 3);
  if (need.length) {
    any = true;
    var s1 = h("div", "appstore__sugg"); s1.setAttribute("data-suggest", "file");
    s1.appendChild(h("span", "appstore__cap", "Suggested for this file"));
    need.forEach(function (v) {
      var r = h("div", "appstore__row");
      r.appendChild(h("span", "", "Your file uses "));
      var n = h("b", "", v.name.replace(/^(gomod|jar|crate|py27)-/, "")); r.appendChild(n);
      r.appendChild(h("span", "appstore__sp"));
      var b = h("button", "appstore__btn is-primary", v.state === "installed" ? "Turn it on" : v.safe ? "Install" : "Request"); b.type = "button";
      b.addEventListener("click", function () { if (v.state === "installed") switchApp(v, true).catch(function () {}); else guarded(v.safe ? "install" : "request", v); });
      r.appendChild(b);
      s1.appendChild(r);
    });
    box.appendChild(s1);
  }
  if (tab === "mine") {
    var rec = recentList().map(function (n) { return views.filter(function (x) { return x.name === n && x.state === "installed"; })[0]; }).filter(Boolean);
    if (rec.length) {
      any = true;
      var s2 = h("div", "appstore__sugg"); s2.setAttribute("data-suggest", "recent");
      s2.appendChild(h("span", "appstore__cap", "Recently switched on"));
      var row = h("div", "appstore__row");
      rec.forEach(function (v) {
        var b = h("button", "appstore__chip" + (v.on ? " is-on" : ""), v.name.replace(/^(gomod|jar|crate|py27)-/, "")); b.type = "button";
        b.setAttribute("aria-pressed", v.on ? "true" : "false");
        b.title = v.on ? "On: click to turn it off" : "Off: click to turn it on";
        b.addEventListener("click", function () { switchApp(v, !v.on).catch(function () {}); });
        row.appendChild(b);
      });
      s2.appendChild(row);
      box.appendChild(s2);
    }
  }
  box.hidden = !any;
}

function confirmPanel(c) {
  var v = c.v, p = h("div", "appstore__confirm");
  p.setAttribute("role", "alertdialog"); p.setAttribute("aria-label", (c.act === "install" ? "Install " : "Request ") + v.name);
  p.appendChild(h("b", "", (c.act === "install" ? "Install " : "Request ") + v.name.replace(/^(gomod|jar|crate|py27)-/, "") + "?"));
  var bits = [];
  if (v.instMB) bits.push("is " + v.instMB + " MB on the server");
  if (v.ramMB) bits.push("needs about " + v.ramMB + " MB of memory when used");
  p.appendChild(h("p", "appstore__sum", "It " + (bits.join(" and ") || "is big") + ". Your terminal may be stopped if it runs out of memory." + (c.act === "request" ? " An admin has to approve it." : "")));
  var row = h("div", "appstore__row");
  var go = h("button", "appstore__btn is-primary", c.act === "install" ? "Install it" : "Request it"); go.type = "button";
  go.addEventListener("click", function () { confirming = null; if (c.act === "install") install(v); else request(v); });
  var no = h("button", "appstore__btn", "Cancel"); no.type = "button"; no.addEventListener("click", function () { confirming = null; render(); });
  row.appendChild(go); row.appendChild(no);
  if (v.category) {
    var sm = h("button", "appstore__link", "What is smaller?"); sm.type = "button";
    sm.addEventListener("click", function () { confirming = null; category = v.category; render(); });
    row.appendChild(sm);
  }
  p.appendChild(row);
  return p;
}

function render(err) {
  var list = el("appstore-list"), foot = el("appstore-foot");
  if (!list) return;
  list.textContent = "";
  ["browse", "mine"].forEach(function (t) {
    var b = el("appstore-tab-" + t);
    if (b) { b.setAttribute("aria-selected", tab === t ? "true" : "false"); b.classList.toggle("is-on", tab === t); }
  });
  var mt = el("appstore-tab-mine");
  if (mt) { // how many apps are on for the user, on the tab itself
    var on = views.filter(function (v) { return v.on; }).length;
    mt.textContent = "My apps";
    if (loaded && signedIn() && on) mt.appendChild(h("i", "appstore__count", String(on)));
  }
  var tools = el("appstore-tools"); if (tools) tools.hidden = tab !== "browse";
  var fl = el("appstore-filters"), sg = el("appstore-suggest");
  if (fl) fl.hidden = true;
  if (sg) sg.hidden = true;
  if (!signedIn() || (err && err.code === "signin")) {
    var g = h("div", "appstore__guest");
    g.appendChild(h("b", "", "Sign in to use the App Store"));
    g.appendChild(h("span", "", "Signed-in users add libraries and tools to their terminals here."));
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
  var mineOk = function (v) { return v.on || v.state === "requested" || v.state === "rejected" || (v.state === "failed" && v.mine) || (v.state === "installing" && v.mine); };
  var pool = tab === "mine" ? views.filter(mineOk) : views;                 // what the tab is about
  var searched = tab === "browse" ? pool.filter(function (v) { return matches(v, q); }) : pool;
  var inLang = narrow(searched, "", repl);                                  // the counts of the chips follow the search and the language
  var biList = tab === "mine" ? builtins.filter(function (b) { return !repl || (b.used || []).indexOf(repl) >= 0; }) : [];
  // every category of the tab's list is a chip (the counts follow the search, a category the search emptied is dimmed); a chosen category that
  // the tab does not have at all (the language changed) is dropped
  var chips = chipsFor(narrow(pool, "", repl), inLang);
  if (category && category !== "Built in" && !chips.some(function (c) { return c.name === category; })) category = "";
  if (category === "Built in" && !biList.length) category = "";
  var shown = category === "Built in" ? [] : narrow(searched, category, repl);
  var biShown = tab === "mine" && (category === "" || category === "Built in") ? biList : [];
  renderSuggest();
  renderFilters(chips, inLang.length, biList.length, languages(pool), shown.length);
  if (confirming) list.appendChild(confirmPanel(confirming));
  shown.forEach(function (v) { list.appendChild(card(v)); });
  biShown.forEach(function (b) { list.appendChild(builtinCard(b)); });
  // a name that nothing in the store matches can still be looked up on conda-forge and asked for
  if (tab === "browse" && q && !shown.length && !category && !repl && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(q)) list.appendChild(lookupCard(q));
  if (!shown.length && !biShown.length && (category || repl || (tab === "browse" && q))) {
    if (tab !== "browse" || category || repl) list.appendChild(emptyFilter(q));
  } else if (!shown.length && !biShown.length) list.appendChild(h("p", "appstore__sum", tab === "mine" ? "Nothing is on for you yet. Browse the store and switch an app on." : "No apps yet."));
  if (foot) {
    foot.textContent = "";
    if (changed && tab === "mine") {
      var note = h("div", "appstore__apply");
      note.appendChild(h("span", "", "You changed what is on. Terminals that are open keep what they had; new ones use it." + (sandbox ? " Open a new terminal (the + beside the terminal tabs) to use it now." : "")));
      if (!sandbox) {
        var ab = h("button", "appstore__btn", "Use it in my active terminal");
        ab.type = "button"; ab.addEventListener("click", applyHere);
        note.appendChild(ab);
      }
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
  tab = !name && lsGet("appstore-tab") === "mine" ? "mine" : "browse"; // a user who works in My apps lands there; a name from Genie or the terminal opens Browse
  category = ""; repl = ""; confirming = null;                           // the filters start at All every time
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
  el("appstore-tab-browse").addEventListener("click", function () { setTab("browse"); say(""); render(); });
  el("appstore-tab-mine").addEventListener("click", function () { setTab("mine"); say(""); render(); });
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
