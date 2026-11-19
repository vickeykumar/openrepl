// The App Store page (resources/store.html; designed in the canvas "App Store: all screens and actions"; docs/lld/18-app-store.md).
// The page holds no data: everything comes from /store/api, which checks who is asking and refuses guests. Sections are the
// address hash: #browse, #mine, #requests, #kind/lib, #kind/tool, #kind/languages, #kind/plugins, #apt (admins), #app/<name>.
(function () {
  "use strict";

  var S = { views: [], on: [], home: "", envScript: "", admin: false, python: "", loaded: false, error: null, q: "", chip: "all", info: {}, note: null, watch: null, hits: {}, sizes: {}, adm: null, builtins: [], cat: null, repl: "", category: "", level: "" };
  var side = document.getElementById("store-side");
  var main = document.getElementById("store-main");
  var dlg = document.getElementById("store-dialog");

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }
  function url(path) { return typeof window.preprocessurl === "function" ? window.preprocessurl(path) : path; }
  function api(path, body, admin) {
    var opts = { credentials: "same-origin", cache: "no-store" };
    if (body) {
      opts.method = "POST";
      opts.headers = { "Content-Type": "application/json" };
      if (admin) opts.headers["X-Requested-With"] = "openrepl-admin";
      opts.body = JSON.stringify(body);
    } else if (admin) {
      opts.headers = { "X-Requested-With": "openrepl-admin" };
    }
    return fetch(url(path), opts).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (j) {
        if (!res.ok) { var e = new Error(j.message || j.error || "The App Store did not answer."); e.code = j.error; e.status = res.status; e.body = j; throw e; }
        return j;
      });
    });
  }
  function store(path, body) { return api("/store/api/" + path, body); }

  function day(unix) {
    if (!unix) return "";
    var d = new Date(unix * 1000);
    return d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
  }
  function moduleOf(name) { return name.replace(/[-.]/g, "_"); }
  function find(name) { for (var i = 0; i < S.views.length; i++) if (S.views[i].name === name) return S.views[i]; return null; }
  function mine(v) { return v.on || (v.mine && (v.state === "requested" || v.state === "rejected" || v.state === "failed")); }

  function take(j) {
    if (j.views) S.views = j.views;
    if (j.on) S.on = j.on;
    if (j.builtins) S.builtins = j.builtins;
    if (j.requestsPerDay) { S.perDay = j.requestsPerDay; S.today = j.requestsToday || 0; }
    ["home", "envScript", "admin", "python", "sandbox"].forEach(function (k) { if (j[k] !== undefined) S[k] = j[k]; });
    S.loaded = true; S.error = null;
  }
  function load() {
    return store("list").then(function (j) { take(j); }, function (e) { S.error = e; S.loaded = true; }).then(function () { return S.admin ? loadAdmin() : null; });
  }
  // the catalogue entries the store lists apart from the installable ones: browser libraries and the plugins of the preview
  function loadCat() {
    if (S.cat) return;
    S.cat = { loading: true };
    store("catalogue?browser=1").then(function (j) { S.cat = j; renderSoon(); }, function (e) { S.cat = { entries: [], error: e.message }; renderSoon(); });
  }
  // what an admin sees besides the store: requests, failures, the servers (GET /admin/store)
  function loadAdmin() {
    return api("/admin/store", null, true).then(function (d) {
      // a list that is missing is an empty one: the side menu and the admin sections must never break on it
      ["requests", "reports", "apps", "apt", "nodes", "defaults"].forEach(function (k) { if (!Array.isArray(d[k])) d[k] = []; });
      if (!d.users) d.users = {};
      S.adm = d;
    }, function () { S.adm = null; });
  }
  function adminPost(body, ok) {
    return api("/admin/store", body, true).then(function () { if (ok) say(ok, "info"); return load(); }).then(render, function (e) { say(e.message, "bad"); render(); });
  }
  function say(text, kind) { S.note = text ? { text: text, kind: kind || "" } : null; }
  function act(path, body, ok) {
    say("");
    return store(path, body).then(function (j) { take(j); if (ok) say(ok, "info"); render(); return j; }, function (e) {
      if (e.code === "signin") { S.error = e; } else { say(e.message, "bad"); }
      render();
      throw e;
    });
  }

  // ---- the sections ---------------------------------------------------------------------------------------------------------

  function route() {
    var h = (location.hash || "#browse").replace(/^#/, "");
    var p = h.split("/");
    return { name: p[0] || "browse", arg: decodeURIComponent(p.slice(1).join("/")) };
  }
  function go(h) { if (location.hash === h) render(); else location.hash = h; }

  function renderSide(r) {
    side.textContent = "";
    function link(label, hash, count, on) {
      var a = el("a", on ? "is-on" : "", label);
      a.href = hash;
      if (on) a.setAttribute("aria-current", "page");
      if (count) a.appendChild(el("i", "", String(count)));
      side.appendChild(a);
    }
    var counts = { mine: S.views.filter(function (v) { return v.on; }).length, req: S.views.filter(function (v) { return v.mine && (v.state === "requested" || v.state === "rejected" || v.state === "failed"); }).length };
    link("Browse", "#browse", 0, r.name === "browse" || r.name === "app");
    link("My apps", "#mine", counts.mine, r.name === "mine");
    if (S.builtins.length) { var mine = side.lastChild; mine.appendChild(el("small", "store__builtincount", "+ " + S.builtins.length + " built in")); }
    link("My requests", "#requests", counts.req, r.name === "requests");
    side.appendChild(el("h2", "", "Kinds"));
    [["lib", "Python libraries"], ["py27", "Python 2.7 libraries"], ["tool", "Tools"], ["node", "Node libraries"], ["gomod", "Go modules"], ["jar", "Java libraries"], ["crate", "Rust crates"], ["browser", "Browser libraries and plugins"], ["lang", "Languages (admin installs)"], ["plugin", "Editor plugins (samples)"]].forEach(function (k) {
      link(k[1], "#kind/" + k[0], 0, r.name === "kind" && r.arg === k[0]);
    });
    if (S.admin) {
      side.appendChild(el("h2", "", "Admin"));
      var a = S.adm;
      link("Requests", "#admin/requests", a ? a.requests.filter(function (x) { return x.status === "waiting"; }).length : 0, r.name === "admin" && r.arg === "requests");
      link("Failures", "#admin/failures", a ? a.reports.filter(function (x) { return x.status === "open"; }).length : 0, r.name === "admin" && r.arg === "failures");
      link("Servers and installs", "#admin/servers", 0, r.name === "admin" && r.arg === "servers");
      link("apt packages", "#apt", 0, r.name === "apt");
    }
  }

  function noteBox() {
    if (!S.note) return null;
    return el("div", "store__note is-" + S.note.kind, S.note.text);
  }

  function guestCard() {
    var g = el("div", "store__guest");
    g.appendChild(el("b", "", "Sign in to use the App Store"));
    g.appendChild(el("span", "", "Signed-in users add libraries and tools to their terminals here."));
    var a = el("a", "sbtn is-primary", "Go to the editor to sign in");
    a.href = "./";
    g.appendChild(a);
    return g;
  }

  function stateTag(v) {
    if (v.state === "builtin") return el("span", "stag stag--builtin", "Built in" + (v.version ? " (" + v.version + ")" : ""));
    var t = { installed: "Installed", installing: v.waiting ? "Waiting for another install" : "Installing" + (v.pct ? " " + v.pct + "%" : ""), failed: "Failed", requested: "Needs approval", rejected: "Not available" }[v.state];
    return t ? el("span", "stag stag--" + v.state, t) : null;
  }

  function loadInfo(name) {
    if (S.info[name]) return;
    S.info[name] = { loading: true };
    store("info?name=" + encodeURIComponent(name)).then(function (i) { S.info[name] = i; renderSoon(); }, function (e) { S.info[name] = { error: e.message }; renderSoon(); });
  }
  var soon = 0;
  function renderSoon() { clearTimeout(soon); soon = setTimeout(render, 80); }

  function control(v, row) {
    function btn(text, fn, cls) { var b = el("button", "sbtn" + (cls ? " " + cls : ""), text); b.type = "button"; b.addEventListener("click", function (e) { e.stopPropagation(); fn(); }); row.appendChild(b); return b; }
    if (v.state === "builtin") return;
    if (v.soon) { row.appendChild(el("span", "stag", "Listed, not installable yet")); return; }
    if (v.state === "available") {
      if (v.safe) btn("Install", function () { install(v); }, "is-primary");
      else if (S.admin) btn("Install (admin)", function () { adminInstall(v); }, "is-primary");
      else btn("Request", function () { requestDialog(v); }, "is-primary");
    } else if (v.state === "installing") btn("Details", function () { progress(v.name); });
    else if (v.state === "installed") {
      var checking = S.checking === v.id;
      var sw = el("button", "sswitch" + (v.on ? " is-on" : ""), checking ? "" : v.on ? "Enabled" : "Disabled");
      if (checking) { sw.appendChild(spin(14)); sw.appendChild(document.createTextNode(" Checking…")); }
      sw.type = "button"; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", v.on ? "true" : "false");
      if (checking) { sw.disabled = true; sw.title = "Checking that it works in your terminal environment"; }
      sw.setAttribute("aria-label", "Use " + v.name + " in my terminals");
      sw.addEventListener("click", function (e) { e.stopPropagation(); switchApp(v, !v.on); });
      row.appendChild(sw);
    } else if (v.state === "failed") {
      // a user can try again at once (the server asks them to wait a minute after the last try); an app that needs an admin is asked for again
      btn("Try again", function () { if (v.safe) install(v); else if (S.admin) adminInstall(v); else requestDialog(v); }, "is-primary");
      btn("Details", function () { failure(v.name); });
    }
    else if (v.state === "rejected") btn("Ask again", function () { requestDialog(v); });
    else if (v.state === "requested") btn("Details", function () { go("#app/" + encodeURIComponent(v.name)); });
  }

  // an admin installs what is not on the safe list (a language, a plugin, anything), for everyone
  function adminInstall(v) {
    api("/admin/store", { action: "install", name: v.name, kind: v.kind || "lib" }, true).then(function () { return load(); }).then(function () { render(); progress(v.name); }, function (e) { say(e.message, "bad"); render(); });
  }

  function card(v) {
    var c = el("article", "sapp is-" + v.state);
    c.setAttribute("data-app", v.name);
    var name = el("a", "sapp__name", v.name);
    name.href = "#app/" + encodeURIComponent(v.name);
    c.appendChild(name);
    var info = S.info[v.name];
    if (!info && !v.summary && (v.kind === "lib" || v.kind === "tool")) loadInfo(v.name);
    c.appendChild(el("p", "sapp__sum", v.summary ? v.summary : info && info.summary ? info.summary : v.state === "failed" ? v.err || "The install stopped." : v.state === "rejected" && v.answer ? v.answer : ""));
    var row = el("div", "sapp__row");
    row.appendChild(el("span", "stag", { lib: "python", tool: "tool", node: "node", lang: "language", plugin: "plugin", weblib: "browser", clib: "C/C++", lib27: "python 2.7", py27: "python 2.7", gomod: "go", jar: "java", crate: "rust" }[v.kind || "lib"] || v.kind));
    if (v.level && v.level !== "optional") row.appendChild(el("span", "stag stag--level", v.level));
    if (v.ramMB >= 100) row.appendChild(el("span", "stag stag--warn", v.ramMB + " MB when used"));
    if (v.state !== "installed" && v.instMB >= 100) row.appendChild(el("span", "sapp__size", v.instMB + " MB"));
    if (v.version) row.appendChild(el("span", "sapp__ver", v.version));
    var tag = stateTag(v);
    if (tag) row.appendChild(tag);
    if (v.default && v.state === "installed") row.appendChild(el("span", "stag stag--installed", "on for everyone"));
    if (v.state === "installed" && v.sizeMB) row.appendChild(el("span", "sapp__size", v.sizeMB.toFixed(v.sizeMB < 10 ? 1 : 0) + " MB"));
    row.appendChild(el("span", "sapp__sp"));
    control(v, row);
    c.appendChild(row);
    return c;
  }

  function lookupCard(name) {
    var c = el("article", "sapp is-lookup");
    c.setAttribute("data-app", name);
    var a = el("a", "sapp__name", name);
    a.href = "#app/" + encodeURIComponent(name);
    c.appendChild(a);
    var info = S.info[name];
    if (!info) loadInfo(name);
    c.appendChild(!info || info.loading ? loadingLine("sapp__sum", "Looking it up on conda-forge…") : el("p", "sapp__sum", info.error ? info.error : (info.summary || "No description.")));
    var row = el("div", "sapp__row");
    row.appendChild(el("span", "stag", "conda-forge"));
    if (info && info.version) row.appendChild(el("span", "sapp__ver", info.version));
    row.appendChild(el("span", "sapp__sp"));
    if (info && !info.error && !info.loading) { var b = el("button", "sbtn is-primary", "Request"); b.type = "button"; b.addEventListener("click", function () { requestDialog({ name: name, kind: "lib" }); }); row.appendChild(b); }
    c.appendChild(row);
    return c;
  }

  function matches(v, q) { return !q || v.name.indexOf(q) >= 0; }
  var CHIPS = [["all", "All"], ["installed", "Installed"], ["safe", "Safe to install"], ["approval", "Needs approval"]];
  function chipOk(v) {
    switch (S.chip) {
      case "installed": return v.state === "installed";
      case "safe": return v.safe && v.state !== "installed";
      case "approval": return v.state === "requested" || v.state === "rejected" || !v.safe;
      default: return true;
    }
  }

  var INTRO = {
    py27: "Libraries for the Python 2.7 REPL, from conda-forge, in an environment of their own. Only a Python 2.7 terminal sees them: in bash, run PYTHONPATH=$OPENREPL_PY27PATH python2.7.",
    gomod: "Go modules, fetched by the server from the Go module proxy with their checksums checked, and used with no internet. The version is the newest that builds with this server's Go. Use go run; a Go REPL cannot import them.",
    jar: "Java libraries (jars and what they need) from Maven Central, checksums checked, on the class path of javac, java and jshell. The version is the newest this server's Java can load.",
    crate: "Rust crates, fetched by the server from crates.io with their checksums checked. In evcxr use :dep name = \"version\". The version is the newest that builds with this server's Rust.",
    lib: "Python libraries from conda-forge. Installed once on the server; your terminals import them when you enable them.",
    tool: "Programs from conda-forge (jq and the like), on the PATH of your terminals when enabled.",
    node: "Node libraries from npm (scripts of the packages are not run). Your terminals can require() them when enabled.",
    lang: "Languages (Lua, Ruby, Julia, R...): a program a terminal can run. They are large, so an admin installs them; ask for the one you need. These are samples.",
    browser: "Libraries for the pages you preview (html, css, javascript) and the plugins of the preview browser. They are served by this site, with their checksums checked.",
    plugin: "Editor plugins that come with the server (samples). An admin installs one; once it is enabled for you it shows in the editor's status bar. A plugin runs in a sandboxed frame: it can read the text of your editor and show one line of text, nothing else."
  };
  var searchTimer = 0;

  function searchKey(q, kind) { return (kind || "") + "|" + q; }
  function runSearch(q, kind) {
    var key = searchKey(q, kind);
    if (S.hits[key]) return;
    S.hits[key] = { loading: true };
    store("search?q=" + encodeURIComponent(q) + "&kind=" + encodeURIComponent(kind || "")).then(function (j) { S.hits[key] = { list: j.hits || [], error: j.error || "" }; renderSoon(); },
      function (e) { S.hits[key] = { list: [], error: e.message }; renderSoon(); });
  }

  // ---- paging: a long list shows one page at a time, with a pager at its foot ------------------------------------------------
  var PAGE_SIZES = [12, 24, 48];
  var pg = {};
  function pgState(key, sig) {
    var st = pg[key];
    if (!st) {
      var size = 24;
      try { var v = parseInt(localStorage.getItem("openrepl.store.pagesize"), 10); if (PAGE_SIZES.indexOf(v) >= 0) size = v; } catch (e) { /* storage may be blocked */ }
      st = pg[key] = { page: 1, size: size, sig: sig };
    }
    if (st.sig !== sig) { st.sig = sig; st.page = 1; }   // a new search or filter starts at page 1
    return st;
  }
  // pagedList adds the items of the current page (each built by draw) to box and the pager after it. key names the list; sig is what
  // narrows it (search words, filters): a change of sig goes back to page 1.
  function pagedList(box, items, key, sig, draw) {
    var st = pgState(key, sig), n = items.length, pages = Math.max(1, Math.ceil(n / st.size));
    if (st.page > pages) st.page = pages;
    items.slice((st.page - 1) * st.size, st.page * st.size).forEach(function (x) { box.appendChild(draw(x)); });
    if (n <= PAGE_SIZES[0]) return null;
    return pager(n, st, function () { render(); var t = document.getElementById("store-top"); if (t && t.scrollIntoView) t.scrollIntoView({ block: "start" }); });
  }
  function pager(n, st, change) {
    var pages = Math.max(1, Math.ceil(n / st.size));
    var bar = el("nav", "store__pager"); bar.setAttribute("aria-label", "Pages");
    var a = n ? (st.page - 1) * st.size + 1 : 0, b = Math.min(n, st.page * st.size);
    bar.appendChild(el("span", "store__pager-count", "Showing " + a + " to " + b + " of " + n));
    var sel = el("select"); sel.setAttribute("aria-label", "Per page");
    PAGE_SIZES.forEach(function (z) { var o = el("option", "", z + " per page"); o.value = String(z); if (z === st.size) o.selected = true; sel.appendChild(o); });
    sel.addEventListener("change", function () {
      st.size = parseInt(sel.value, 10); st.page = 1;
      try { localStorage.setItem("openrepl.store.pagesize", String(st.size)); } catch (e) { /* ignore */ }
      change();
    });
    bar.appendChild(sel);
    bar.appendChild(el("span", "store__pager-sp"));
    function btn(text, p, on, off, label) {
      var x = el("button", "sbtn" + (on ? " is-primary" : ""), text); x.type = "button"; x.disabled = !!off;
      if (label) x.setAttribute("aria-label", label);
      if (on) x.setAttribute("aria-current", "page");
      x.addEventListener("click", function () { st.page = p; change(); });
      bar.appendChild(x);
    }
    btn("\u2190 Previous", st.page - 1, false, st.page <= 1);
    var lo = Math.max(1, Math.min(st.page - 3, pages - 6)), hi = Math.min(pages, lo + 6);
    for (var i = lo; i <= hi; i++) btn(String(i), i, i === st.page, false, "Page " + i);
    btn("Next \u2192", st.page + 1, false, st.page >= pages);
    bar.appendChild(el("span", "store__pager-count", "Page " + st.page + " of " + pages));
    return bar;
  }

  function viewBrowse(kind) {
    var wrap = el("div", "store__list");
    wrap.style.cssText = "display:flex;flex-direction:column;gap:12px";
    if (kind && INTRO[kind]) wrap.appendChild(el("p", "store__legend", INTRO[kind]));
    var tools = el("div", "store__tools");
    var input = el("input"); input.type = "search"; input.id = "store-search"; input.value = S.q;
    input.placeholder = kind === "plugin" || kind === "lang" ? "Search" : "Search the store, conda-forge and npm by name";
    input.setAttribute("aria-label", "Search the apps"); input.autocomplete = "off"; input.spellcheck = false;
    tools.appendChild(input);
    wrap.appendChild(tools);
    var chips = el("div", "store__chips");
    CHIPS.forEach(function (c) {
      var b = el("button", "store__chip" + (S.chip === c[0] ? " is-on" : ""), c[1]); b.type = "button";
      b.addEventListener("click", function () { S.chip = c[0]; render(); });
      chips.appendChild(b);
    });
    wrap.appendChild(chips);
    if (kind !== "plugin" && kind !== "lang") wrap.appendChild(filters(kind));
    var nb = noteBox(); if (nb) wrap.appendChild(nb);
    var q = S.q.trim().toLowerCase();
    var list = S.views.filter(function (v) { return matches(v, q) && chipOk(v) && filterOk(v) && (!kind || (v.kind || "lib") === kind); });
    if (kind === "browser") return viewBrowser(wrap, q);
    var grid = el("div", "store__grid");
    wrap.appendChild(el("span", "store__top")).id = "store-top";
    var pgr = pagedList(grid, list, "browse:" + (kind || ""), [kind, q, S.chip, S.repl, S.category, S.level].join("|"), card);
    wrap.appendChild(grid);
    if (pgr) wrap.appendChild(pgr);
    if (!list.length && !q) wrap.appendChild(el("p", "store__legend", "Nothing matches here."));
    // what the whole of conda-forge and npm have under that name (the plugins and languages of the store are searched in the store)
    if (q.length >= 2 && /^[a-z0-9][a-z0-9._-]{0,40}$/.test(q) && S.chip === "all") {
      var key = searchKey(q, kind), h = S.hits[key];
      var have = {};
      list.forEach(function (v) { have[v.name + "/" + (v.kind || "lib")] = true; });
      var box = el("div", "store__hits");
      box.appendChild(el("h3", "store__h3", "Found for \u201c" + q + "\u201d" + (kind === "node" ? " on npm" : kind === "plugin" ? "" : kind ? " on conda-forge" : " on conda-forge and npm")));
      if (!h) { box.appendChild(loadingLine("store__legend", "Searching\u2026")); searchTimer = 0; }
      else if (h.loading) box.appendChild(loadingLine("store__legend", "Searching\u2026 (the first search of conda-forge can take a minute)"));
      else {
        if (h.error) box.appendChild(el("p", "store__legend", h.error));
        var g2 = el("div", "store__grid"), n = 0;
        (h.list || []).forEach(function (x) {
          if (have[x.name + "/" + (x.kind || "lib")]) return;
          n++;
          g2.appendChild(card({ name: x.name, version: x.version, kind: x.kind, state: x.state || "available", safe: x.safe, summary: x.summary }));
        });
        if (n) box.appendChild(g2); else if (!h.error) box.appendChild(el("p", "store__legend", "Nothing else was found."));
      }
      wrap.appendChild(box);
    }
    wrap.appendChild(legend());
    input.addEventListener("input", function () {
      S.q = input.value;
      var pos = input.selectionStart;
      render();
      var again = document.getElementById("store-search");
      if (again) { again.focus(); try { again.setSelectionRange(pos, pos); } catch (e) { /* not every input has a caret */ } }
      clearTimeout(searchTimer);
      var qq = S.q.trim().toLowerCase();
      if (qq.length >= 2 && /^[a-z0-9][a-z0-9._-]{0,40}$/.test(qq) && S.chip === "all") searchTimer = setTimeout(function () { runSearch(qq, kind); render(); var a2 = document.getElementById("store-search"); if (a2) { a2.focus(); try { a2.setSelectionRange(pos, pos); } catch (e) { /* no caret */ } } }, 450);
    });
    // a search box that holds text when the page opens (or the section changes): look for it
    if (q.length >= 2 && /^[a-z0-9][a-z0-9._-]{0,40}$/.test(q) && S.chip === "all" && !S.hits[searchKey(q, kind)]) setTimeout(function () { runSearch(q, kind); render(); }, 0);
    return wrap;
  }

  // language (REPL), category and level: they narrow the list the page already holds
  function filterOk(v) {
    if (S.repl && !(v.repls && v.repls.indexOf(S.repl) >= 0)) return false;
    if (S.category && v.category !== S.category) return false;
    if (S.level && v.level !== S.level) return false;
    return true;
  }
  function filters(kind) {
    var row = el("div", "store__filters");
    var repls = {}, cats = {};
    S.views.forEach(function (v) { (v.repls || []).forEach(function (r) { repls[r] = true; }); if (v.category) cats[v.category] = true; });
    function sel(label, key, values) {
      var l = el("label", "store__filter", label + " ");
      var s = el("select"); s.setAttribute("aria-label", label); s.setAttribute("data-filter", key);
      var o0 = el("option", "", "all"); o0.value = ""; s.appendChild(o0);
      values.forEach(function (x) { var o = el("option", "", x); o.value = x; s.appendChild(o); });
      s.value = S[key]; if (s.value !== S[key]) S[key] = "";
      s.addEventListener("change", function () { S[key] = s.value; render(); });
      l.appendChild(s); row.appendChild(l);
    }
    sel("Language", "repl", Object.keys(repls).sort());
    sel("Category", "category", Object.keys(cats).sort());
    sel("Level", "level", ["core", "popular", "optional"]);
    return row;
  }

  // browser libraries and the plugins of the preview (from the catalogue: they are not installed by the user's switch of a terminal)
  function loadPP() {
    if (S.pp) return;
    S.pp = { loading: true };
    store("preview-plugins").then(function (j) { S.pp = { list: j.plugins || [] }; renderSoon(); }, function (e) { S.pp = { list: [], error: e.code }; renderSoon(); });
  }
  function ppOf(id) { var l = (S.pp && S.pp.list) || []; for (var i = 0; i < l.length; i++) if (l[i].id === id) return l[i]; return null; }
  function viewBrowser(wrap, q) {
    loadCat(); loadPP();
    var grid = el("div", "store__grid");
    var entries = (S.cat && S.cat.entries) || [];
    if (S.cat && S.cat.loading) wrap.appendChild(loadingLine("store__legend", "Loading…"));
    var shown = entries.filter(function (e) { return !q || e.name.toLowerCase().indexOf(q) >= 0 || (e.summary || "").toLowerCase().indexOf(q) >= 0; })
      .filter(function (e) { return (!S.level || e.level === S.level) && (!S.repl || (e.repls || []).indexOf(S.repl) >= 0 || e.kind === "plugin"); });
    var pgr = pagedList(grid, shown, "browser", [q, S.level, S.repl].join("|"), function (e) {
        var c = el("article", "sapp is-" + e.state);
        c.setAttribute("data-app", e.id);
        var a = el("a", "sapp__name", e.name); a.href = "#app/" + encodeURIComponent(e.id); c.appendChild(a);
        c.appendChild(el("p", "sapp__sum", e.summary || ""));
        var row = el("div", "sapp__row");
        row.appendChild(el("span", "stag", e.kind === "plugin" ? "plugin: " + e.place : "browser library"));
        if (e.internal) row.appendChild(el("span", "stag stag--installed", "ours"));
        else if (e.publisher) row.appendChild(el("span", "stag", "third party"));
        row.appendChild(el("span", "stag stag--level", e.level));
        row.appendChild(el("span", "sapp__sp"));
        if (e.kind === "plugin" && e.place === "preview") {
          var pp = ppOf(e.id);
          if (pp && pp.ready) {
            var sw = el("button", "sswitch" + (pp.on ? " is-on" : ""), pp.on ? "Enabled" : "Disabled"); sw.type = "button"; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", pp.on ? "true" : "false");
            sw.setAttribute("aria-label", "Use " + e.name + " in my preview");
            sw.addEventListener("click", function () {
              store("plugin-switch", { id: e.id, on: !pp.on }).then(function () { S.pp = null; if (window.PreviewPlugins) window.PreviewPlugins.reload(); render(); }, function (er) { say(er.message, "bad"); render(); });
            });
            row.appendChild(sw);
          } else if (pp) row.appendChild(el("span", "stag", S.admin ? "Not installed: install it in the admin Catalogue" : "An admin installs it first"));
        }
        if (e.docs) { var d = el("a", "sbtn", "Documentation"); d.href = e.docs; if (e.docs.indexOf("https://") === 0) { d.target = "_blank"; d.rel = "noopener noreferrer"; } row.appendChild(d); }
        c.appendChild(row);
        return c;
      });
    wrap.appendChild(grid);
    if (pgr) wrap.appendChild(pgr);
    if (S.cat && !S.cat.loading && !entries.length) wrap.appendChild(el("p", "store__legend", "Nothing matches here."));
    wrap.appendChild(el("p", "store__legend", "Browser libraries are served from this site to your preview at /preview/lib/<name>/<file>: nothing is loaded from another site. A plugin of the preview shows in the preview toolbar once it is on for you."));
    return wrap;
  }

  function legend() {
    var l = el("p", "store__legend");
    var b = el("b", "", "States: ");
    l.appendChild(b);
    l.appendChild(document.createTextNode("not installed (Install, or Request when it needs an admin) · installing (progress) · installed (a switch, disabled until you enable it) · needs approval (waiting for an admin) · failed (see why, report it). Installed means installed for everyone; the switch is yours alone."));
    return l;
  }

  function viewMine() {
    var wrap = el("div", "store__rows");
    var nb = noteBox(); if (nb) wrap.appendChild(nb);
    var mineList = S.views.filter(function (v) { return v.on || (v.mine && v.state !== "available"); });
    var minePager = pagedList(wrap, mineList, "mine", "", function (v) {
      var r = el("div", "store__row");
      r.setAttribute("data-app", v.name);
      var d = el("div");
      var a = el("a", "sapp__name", v.name + (v.version ? " " + v.version : "")); a.href = "#app/" + encodeURIComponent(v.name);
      d.appendChild(a);
      d.appendChild(el("small", "", kindLabel(v.kind) + (v.state === "requested" ? ", asked for " + day(v.at) : v.state === "failed" ? ", failed" : "")));
      r.appendChild(d);
      var tag = stateTag(v); if (tag && v.state !== "installed") r.appendChild(tag);
      control(v, r);
      return r;
    });
    if (minePager) wrap.appendChild(minePager);
    if (!mineList.length) wrap.appendChild(el("p", "store__legend", "Nothing is enabled for you yet. Browse the store and enable an app."));
    // what the image has built in: listed with the apps, always on, nothing to install or remove
    if (S.builtins.length) wrap.appendChild(el("h3", "store__h3", "Built in"));
    S.builtins.forEach(function (b) {
      var r = el("div", "store__row is-builtin");
      r.setAttribute("data-builtin", b.name);
      var d = el("div");
      var a = el("span", "sapp__name", b.name + (b.version ? " " + b.version : ""));
      d.appendChild(a);
      d.appendChild(el("small", "", "Built in" + (b.used && b.used.length ? " · used by " + b.used.join(", ") : "")));
      r.appendChild(d);
      r.appendChild(el("span", "stag stag--builtin", "Built in"));
      var sw = el("button", "sswitch is-on", "Enabled"); sw.type = "button"; sw.disabled = true; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", "true");
      sw.title = "Built in: always on"; sw.setAttribute("aria-label", b.name + " is built in and always on");
      r.appendChild(sw);
      var u = el("button", "sbtn", "Uninstall"); u.type = "button"; u.disabled = true; u.title = "Built in: it comes with the server"; r.appendChild(u);
      if (b.docs) { var dl = el("a", "sbtn", "Documentation"); dl.href = b.docs; dl.target = "_blank"; dl.rel = "noopener noreferrer"; r.appendChild(dl); }
      wrap.appendChild(r);
    });
    // what the switches do to terminals that are open
    var apply = el("div", "store__note is-info");
    if (S.sandbox && !S.admin) {
      // the sandbox of a terminal is fixed when it starts: what was switched on later cannot be read in it, so there is no line to run
      apply.appendChild(el("p", "", "A terminal that is open keeps what it had when it started. Open a new terminal (the + beside the terminal tabs) to use what is on now."));
    } else {
      apply.appendChild(el("p", "", "A terminal that is open keeps what it had when it started; a new one uses what is on now. To use it in an open terminal, run this line in it:"));
      apply.appendChild(el("pre", "store__code", ". '" + S.envScript + "'"));
      var cp = el("button", "sbtn", "Copy the line"); cp.type = "button";
      cp.addEventListener("click", function () { copy(". '" + S.envScript + "'"); });
      apply.appendChild(cp);
    }
    var back = el("a", "sbtn", "Back to the editor"); back.href = "./"; back.style.marginLeft = "8px";
    apply.appendChild(back);
    wrap.appendChild(apply);
    if (S.admin) wrap.appendChild(el("p", "store__legend", "You are an admin: your terminal has no sandbox, so it sees every bundle in the store whether it is on or not."));
    return wrap;
  }

  function copy(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(function () { say("Copied.", "info"); render(); }, function () {});
  }

  function viewRequests() {
    var wrap = el("div", "store__rows");
    var nb = noteBox(); if (nb) wrap.appendChild(nb);
    var list = S.views.filter(function (v) { return v.mine && (v.state === "requested" || v.state === "rejected" || v.state === "failed"); });
    list.forEach(function (v) {
      var r = el("div", "store__row");
      r.setAttribute("data-app", v.name);
      var d = el("div");
      var a = el("a", "sapp__name", v.name); a.href = "#app/" + encodeURIComponent(v.name);
      d.appendChild(a);
      d.appendChild(el("small", "", v.state === "requested" ? "Asked for " + day(v.at) + ", waiting for an admin" : v.state === "rejected" ? "Not available" + (v.answer ? ": " + v.answer : "") : (v.reported ? "Reported to the admin" : "Install failed") + (v.err ? ": " + v.err : "")));
      r.appendChild(d);
      var tag = stateTag(v); if (tag) r.appendChild(tag);
      control(v, r);
      wrap.appendChild(r);
    });
    if (!list.length) wrap.appendChild(el("p", "store__legend", "You have not asked for anything, and nothing of yours has failed."));
    return wrap;
  }

  function viewKind(arg) {
    return viewBrowse(INTRO[arg] ? arg : "lib");
  }

  function viewApt() {
    var w = el("div", "store__detail");
    w.appendChild(el("h2", "", "apt packages"));
    w.appendChild(el("p", "store__legend", "Debian packages an admin approved. They are installed on every server for every user, with no switch."));
    var box = el("div", "store__rows");
    box.appendChild(loadingLine("store__legend", "Loading…"));
    w.appendChild(box);
    api("/admin/store", null, true).then(function (d) {
      box.textContent = "";
      var aptPager = pagedList(box, d.apt || [], "apt", "", function (p) { var r = el("div", "store__row"); var dd = el("div"); dd.appendChild(el("b", "", p)); r.appendChild(dd); return r; });
      if (aptPager) box.appendChild(aptPager);
      if (!(d.apt || []).length) box.appendChild(el("p", "store__legend", "No apt package has been approved."));
      var a = el("a", "sbtn", "Edit in the admin dashboard"); a.href = "./admin#store"; box.appendChild(a);
    }, function (e) { box.textContent = ""; box.appendChild(el("p", "store__legend", e.message)); });
    return w;
  }

  // ---- the admin's part: requests, failures, the servers ---------------------------------------------------------------------

  function reasonModal(title, confirm, then) {
    modal(function (d) {
      d.appendChild(heading(title));
      d.appendChild(el("p", "", "A reason the people who asked will see (optional)"));
      var t = el("textarea"); t.maxLength = 200; t.setAttribute("aria-label", "Reason"); d.appendChild(t);
      d.appendChild(dialogButtons([
        { label: confirm, primary: true, run: function () { closeModal(); then(t.value); } },
        { label: "Cancel", run: closeModal }
      ]));
      t.focus();
    });
  }

  function admRow(title, sub, tagText, tagCls) {
    var r = el("div", "store__row");
    var d = el("div");
    d.appendChild(el("b", "", title));
    d.appendChild(el("small", "", sub));
    r.appendChild(d);
    if (tagText) r.appendChild(el("span", "stag " + (tagCls || ""), tagText));
    return r;
  }
  function rbtn(row, text, cls, fn) { var b = el("button", "sbtn" + (cls ? " " + cls : ""), text); b.type = "button"; b.addEventListener("click", fn); row.appendChild(b); }

  function viewAdmin(arg) {
    var w = el("div", "store__rows");
    var nb = noteBox(); if (nb) w.appendChild(nb);
    var d = S.adm;
    if (!d) { w.appendChild(loadingLine("store__legend", "Loading…")); return w; }
    var dash = el("a", "sbtn", "Open the full admin dashboard"); dash.href = "./admin#store";
    if (arg === "requests") {
      w.appendChild(el("h2", "", "Requests"));
      w.appendChild(el("p", "store__legend", "Apps that users asked for and that are not on the safe list. Approve installs it for everyone and tells the people who asked."));
      var waiting = d.requests.filter(function (r) { return r.status === "waiting"; });
      var rqPager = pagedList(w, waiting, "adm-requests", "", function (rq) {
        var row = admRow(rq.name, kindLabel(rq.kind) + " · " + rq.askers.length + (rq.askers.length === 1 ? " user" : " users") + " · asked " + day(rq.at) + (rq.reason ? " · “" + rq.reason + "”" : ""), "Waiting", "stag--requested");
        row.setAttribute("data-request", rq.name);
        rbtn(row, "Approve and install", "is-primary", function () { adminPost({ action: "approve", name: rq.name }, rq.name + " is being installed."); });
        rbtn(row, "Reject", "is-danger", function () { reasonModal("Reject " + rq.name + "?", "Reject", function (why) { adminPost({ action: "reject", name: rq.name, answer: why }, rq.name + " was rejected."); }); });
        return row;
      });
      if (rqPager) w.appendChild(rqPager);
      if (!waiting.length) w.appendChild(el("p", "store__legend", "Nobody is waiting for an answer."));
    } else if (arg === "failures") {
      w.appendChild(el("h2", "", "Failures reported"));
      w.appendChild(el("p", "store__legend", "Installs that stopped, and that a user told you about. Every answer is sent to everyone who reported it."));
      var open = d.reports.filter(function (r) { return r.status === "open"; });
      var fPager = pagedList(w, open, "adm-failures", "", function (rp) {
        var item = el("div");
        var row = admRow(rp.name + (rp.version ? " " + rp.version : ""), rp.reporters.length + (rp.reporters.length === 1 ? " user" : " users") + (rp.node ? " · on " + rp.node : "") + " · stopped at: " + (rp.step || "").split(":")[0] + (rp.note ? " · “" + rp.note + "”" : ""), "Failed", "stag--failed");
        row.setAttribute("data-report", rp.name);
        rbtn(row, "Try again", "is-primary", function () { adminPost({ action: "retry", name: rp.name }, "Trying " + rp.name + " again."); });
        rbtn(row, "Pick another version", "", function () { go("#app/" + encodeURIComponent(rp.name)); });
        rbtn(row, "Mark installed", "", function () { adminPost({ action: "installed", name: rp.name }, rp.name + " is marked installed."); });
        rbtn(row, "Reject", "is-danger", function () { reasonModal("Reject the report about " + rp.name + "?", "Reject", function (why) { adminPost({ action: "rejectreport", name: rp.name, answer: why }, "Rejected."); }); });
        item.appendChild(row);
        if (rp.log) item.appendChild(el("pre", "store__code", rp.log));
        return item;
      });
      if (fPager) w.appendChild(fPager);
      if (!open.length) w.appendChild(el("p", "store__legend", "No failures are waiting for you."));
    } else {
      w.appendChild(el("h2", "", "Servers and installs"));
      w.appendChild(el("p", "store__legend", "Every server (the gateway and each worker) with what it has, how big it is and whether it has caught up. The gateway sends a worker the catalog over the tunnel when it changes; the worker installs what is missing itself, from the internet, and tries a failed app again after 1, 2, 5, 15, then 60 minutes."));
      d.nodes.forEach(function (n) {
        var pct = Math.min(100, Math.round(100 * n.sizeMB / d.limitMB));
        var box = el("div", "store__node");
        box.setAttribute("data-node", n.node);
        var head = el("div", "store__nodehead");
        head.appendChild(el("b", "", n.node));
        head.appendChild(el("span", "store__legend", Math.round(n.sizeMB) + " MB of " + d.limitMB + " MB (" + pct + "%)" + (n.seen ? " · last heard " + new Date(n.seen).toLocaleTimeString() : "")));
        box.appendChild(head);
        var bar = el("div", "store-dialog__bar"); var fill = el("i"); fill.style.width = pct + "%"; bar.appendChild(fill); box.appendChild(bar);
        var ids = d.apps.filter(function (a) { return a.state === "installed"; });
        ids.forEach(function (a) {
          var have = (n.have || []).indexOf(a.id) >= 0, bad = n.failed && n.failed[a.id];
          var left = bad && n.retry && n.retry[a.id] ? Math.max(1, Math.ceil((n.retry[a.id] - (d.now || Math.floor(Date.now() / 1000))) / 60)) : 0;
          var status = have ? "ready" : bad ? (left ? "failed, next try in " + left + " min" : "failed") : "missing";
          var row = admRow(a.name + " " + a.version, kindLabel(a.kind) + (have && n.sizes && n.sizes[a.id] ? " · " + Math.round(n.sizes[a.id]) + " MB" : "") + (bad ? " · " + bad : ""), status, have ? "stag--installed" : bad ? "stag--failed" : "stag--requested");
          row.setAttribute("data-bundle", a.id);
          if (have || bad) rbtn(row, "Reinstall here", "", function () { adminPost({ action: "reinstall", node: n.node, id: a.id }, "Asked " + n.node + " to make " + a.name + " again" + (n.node === "gateway" ? "." : " (within a few seconds).")); });
          box.appendChild(row);
        });
        if (!ids.length) box.appendChild(el("p", "store__legend", "Nothing is installed yet."));
        Object.keys(n.failed || {}).filter(function (k) { return k.indexOf("apt:") === 0 || k.indexOf("user:") === 0 || k === "store"; }).forEach(function (k) { box.appendChild(el("p", "store__legend", k + ": " + n.failed[k])); });
        if (n.pending && n.pending.length) box.appendChild(el("p", "store__legend", "Waiting for " + n.node + " to ask: " + n.pending.join(", ")));
        w.appendChild(box);
      });
      // installed apps and who uses them: remove for everyone
      w.appendChild(el("h3", "store__h3", "Installed apps"));
      d.apps.forEach(function (a) {
        var users = d.users[a.id] || 0;
        var row = admRow(a.name + " " + (a.version || ""), kindLabel(a.kind) + " · " + a.state + " · on for " + users + (users === 1 ? " user" : " users") + (a.size ? " · " + Math.round(a.size / 1048576) + " MB" : ""), a.state === "installed" ? "Installed" : a.state, a.state === "installed" ? "stag--installed" : "stag--failed");
        rbtn(row, "Open", "", function () { go("#app/" + encodeURIComponent(a.name)); });
        if (a.state === "installed") {
          var everyone = (d.defaults || []).indexOf(a.name) >= 0;
          rbtn(row, everyone ? "Enabled for everyone: disable" : "Enable for everyone", "", function () { adminPost({ action: "default", name: a.name, on: !everyone }, a.name + (everyone ? " is no longer enabled for everyone." : " is enabled for every signed-in user now (each may disable it for themselves).")); });
        }
        if (a.state === "installed" && a.kind !== "node" && a.kind !== "plugin") {
          rbtn(row, "New lock", "", function () {
            modal(function (dd) {
              dd.appendChild(heading("Make a new lock of " + a.name + "?"));
              dd.appendChild(el("p", "", "It is solved again for every CPU type and installed again on every server. The people who have it on keep their switch; it is missing for a short while."));
              dd.appendChild(dialogButtons([{ label: "New lock", primary: true, run: function () { closeModal(); adminPost({ action: "relock", name: a.name }, "Making a new lock of " + a.name + "."); } }, { label: "Cancel", run: closeModal }]));
            });
          });
        }
        rbtn(row, "Uninstall for everyone", "is-danger", function () {
          modal(function (dd) {
            dd.appendChild(heading("Uninstall " + a.name + "?"));
            dd.appendChild(el("p", "", users ? users + " users have it enabled. It is disabled for them and deleted on every server." : "Nobody has it enabled. It is deleted on every server."));
            dd.appendChild(dialogButtons([{ label: "Uninstall", primary: true, run: function () { closeModal(); adminPost({ action: "remove", name: a.name }, a.name + " was uninstalled."); } }, { label: "Cancel", run: closeModal }]));
          });
        });
        w.appendChild(row);
      });
    }
    w.appendChild(dash);
    return w;
  }

  // ---- an app's page --------------------------------------------------------------------------------------------------------

  function useLine(v) {
    var mod = moduleOf(v.name);
    // a library of C and C++ (headers and a library), of Perl, Tcl or SQLite: there is no program to run
    if (v.category === "C and C++ libraries") return "#include <" + v.name + "/...>   (headers)\n$ g++ file.cc -l" + v.name.replace(/^lib/, "") + "   (when it has a library to link)";
    if (v.category === "Perl modules") return "$ perl -e 'use " + v.name.replace(/^perl-/, "").split("-").map(function (w) { return w.charAt(0).toUpperCase() + w.slice(1); }).join("::") + "'";
    if (v.category === "Tcl") return "% package require " + v.name;
    if (v.category === "SQLite extensions") return "sqlite> .load <the path shown on the app page>";
    switch (v.kind) {
      case "gomod": return "$ go mod init demo && go get " + (v.source || v.name) + "   (from the mirror, no internet needed)\n$ go run .";
      case "jar": return "$ javac Main.java && java Main   (the jars are on the CLASSPATH of this terminal)";
      case "crate": return "evcxr> :dep " + (v.source || v.name) + " = \"" + (v.version || "*") + "\"";
      case "py27": return "$ python2.7 -c \"import " + moduleOf(v.source || v.name.replace(/^py27-/, "")) + "\"";
      case "tool": return "$ " + v.name + " --version";
      case "node": return "$ node -e \"console.log(require('" + v.name + "'))\"";
      case "lang": return "$ " + (v.name === "r-base" ? "R" : v.name) + " --version";
      case "plugin": return "It shows in the status bar of the editor once it is enabled for you.";
      default: return "$ python3 -c \"import " + mod + "\"";
    }
  }
  function kindLabel(k) { return { lib: "Python library", tool: "Tool", node: "Node library", lang: "Language", plugin: "Editor plugin", gomod: "Go module", jar: "Java library", crate: "Rust crate", py27: "Python 2.7 library" }[k] || "Python library"; }
  function needs(v) {
    switch (v.kind) {
      case "node": return "Node.js (the server's), from npm";
      case "plugin": return "Nothing: it comes with the server";
      case "gomod": return "Go (the server's), from the Go module proxy";
      case "jar": return "Java (the server's), from Maven Central";
      case "crate": return "Rust (the server's), from crates.io";
      case "py27": return "Python 2.7, from conda-forge";
      case "tool": case "lang": return "Nothing else: it brings what it needs, from conda-forge";
      default: return S.python ? "Python " + S.python + " (the server's), from conda-forge" : "the server's Python, from conda-forge";
    }
  }

  function loadSize(v) {
    var key = (v.kind || "lib") + ":" + v.name;
    if (S.sizes[key] || v.kind === "plugin" || v.state === "installed" || v.kind === "gomod" || v.kind === "jar" || v.kind === "crate") return; // a mirror's size is known after the install
    S.sizes[key] = { loading: true };
    store("size?name=" + encodeURIComponent(v.name) + "&kind=" + encodeURIComponent(v.kind || "lib")).then(function (j) { S.sizes[key] = j; renderSoon(); }, function (e) { S.sizes[key] = { error: e.message }; renderSoon(); });
  }

  // uninstall: a user may when nobody else has it on; an admin always
  function uninstallDialog(v) {
    modal(function (d) {
      d.appendChild(heading((S.admin ? "Uninstall " : "Uninstall ") + v.name + "?"));
      d.appendChild(el("p", "", S.admin
        ? "It is deleted from every server, and disabled for the users that have it enabled. They can install it again from the store."
        : "It is deleted from the server (nobody else has it on). You can install it again from the store."));
      d.appendChild(dialogButtons([
        { label: "Uninstall", primary: true, run: function () {
          closeModal();
          var done = function () { say(v.name + " was uninstalled.", "info"); return load().then(function () { go("#browse"); }); };
          var fail = function (e) { say(e.message, "bad"); load().then(render); };
          if (S.admin) api("/admin/store", { action: "remove", name: v.name }, true).then(done, fail);
          else store("uninstall", { name: v.name }).then(done, fail);
        } },
        { label: "Cancel", run: closeModal }
      ]));
    });
  }

  function viewApp(name) {
    var v = find(name) || { name: name, kind: "lib", state: "unknown", safe: false };
    for (var bi = 0; bi < S.builtins.length; bi++) if (S.builtins[bi].name === name && v.state === "unknown") v = { name: name, kind: "tool", state: "builtin", version: S.builtins[bi].version, docs: S.builtins[bi].docs, summary: "Built in: the server already has it.", safe: false };
    var w = el("div", "store__detail");
    w.setAttribute("data-app", name);
    var back = el("a", "store__back", "← Back to the store"); back.href = "#browse"; w.appendChild(back);
    var head = el("div", "store__dhead");
    head.appendChild(el("span", "store__logo", name.charAt(0)));
    var hd = el("div");
    hd.appendChild(el("h2", "", name));
    hd.appendChild(el("small", "", (v.version ? v.version + " · " : "") + kindLabel(v.kind) + " · " + ({ node: "npm", plugin: "this server", gomod: "Go module proxy", jar: "Maven Central", crate: "crates.io" }[v.kind] || "conda-forge")));
    head.appendChild(hd);
    var tag = stateTag(v); if (tag) head.appendChild(tag);
    w.appendChild(head);
    var nb = noteBox(); if (nb) w.appendChild(nb);
    var info = S.info[name];
    if (!info && !v.summary && (v.kind === "lib" || v.kind === "tool" || !v.kind)) loadInfo(name);
    var legendText = v.summary ? v.summary : !info || info.loading ? (v.kind === "node" || v.kind === "lang" ? "" : "Looking it up…") : info.error ? info.error : (info.summary || "No description.");
    w.appendChild(legendText === "Looking it up…" ? loadingLine("store__legend", legendText) : el("p", "store__legend", legendText));
    loadSize(v);
    var sz = S.sizes[(v.kind || "lib") + ":" + name];
    var kv = el("div", "store__kv");
    function kvRow(k, val) { kv.appendChild(el("span", "", k)); kv.appendChild(el("b", "", val)); }
    var mirror = v.kind === "gomod" || v.kind === "jar" || v.kind === "crate";
    kvRow("Size", v.smallest && v.state !== "installed" && v.instMB ? "about " + v.instMB + " MB once installed" + (v.estimate ? " (an estimate)" : "") + (v.partial ? " (the version can differ on a server with another CPU type)" : "") + (v.smallest === "apt" ? " (from the Ubuntu archive)" : "") + (v.includes ? ", " + v.includes : "") : mirror && v.state !== "installed" ? (v.instMB ? "about " + v.instMB + " MB, with what it needs" : "worked out when it is installed") : v.state === "installed" && v.sizeMB ? "about " + v.sizeMB.toFixed(v.sizeMB < 10 ? 1 : 0) + " MB on the server" + (v.packages ? " (" + v.packages + " packages)" : "")
      : v.kind === "plugin" ? "a few KB"
      : !sz || sz.loading ? "working it out…"
      : sz.error ? "not known (" + sz.error.split("\n")[0].slice(0, 80) + ")"
      : "download about " + (sz.downloadMB < 10 ? sz.downloadMB.toFixed(1) : Math.round(sz.downloadMB)) + " MB, about " + (sz.diskMB < 10 ? sz.diskMB.toFixed(1) : Math.round(sz.diskMB)) + " MB once installed (" + sz.packages + " packages, version " + sz.version + ")");
    kvRow("Needs", needs(v));
    if (v.repls && v.repls.length) kvRow("Works with", v.repls.join(", "));
    if (v.category) kvRow("Category", v.category);
    if (v.licence) kvRow("Licence", v.licence);
    if (v.ramMB) kvRow("Memory when used", "about " + v.ramMB + " MB");
    if (v.docs) {
      kv.appendChild(el("span", "", "Documentation"));
      var db = el("b"); var da = el("a", "", v.docs.indexOf("/docs/") === 0 ? "How to use it (on this site)" : v.docs); da.href = v.docs;
      if (v.docs.indexOf("https://") === 0) { da.target = "_blank"; da.rel = "noopener noreferrer"; }
      db.appendChild(da); kv.appendChild(db);
    }
    kvRow("Who can", v.state === "installed" ? "Everyone can enable it for themselves" : v.safe ? "You, right away (it is on the safe list)" : S.admin ? "You, as an admin" : "An admin approves it first");
    w.appendChild(kv);
    var actions = el("div", "sapp__row");
    if (v.state === "available" || (v.state === "unknown" && v.safe)) {
      if (v.safe) { var i = el("button", "sbtn is-primary", "Install " + name); i.type = "button"; i.addEventListener("click", function () { install(v); }); actions.appendChild(i); }
      else if (S.admin) { var ia = el("button", "sbtn is-primary", "Install " + name + " (admin)"); ia.type = "button"; ia.addEventListener("click", function () { adminInstall(v); }); actions.appendChild(ia); }
      else { var r0 = el("button", "sbtn is-primary", "Request " + name); r0.type = "button"; r0.addEventListener("click", function () { requestDialog(v); }); actions.appendChild(r0); }
    } else if (v.state === "unknown" || v.state === "rejected") {
      var r = el("button", "sbtn is-primary", "Request " + name); r.type = "button"; r.addEventListener("click", function () { requestDialog(v); }); actions.appendChild(r);
    }
    if (v.state === "requested") actions.appendChild(el("span", "stag stag--requested", "Requested " + day(v.at) + ", waiting for an admin"));
    if (v.state === "rejected" && v.answer) actions.appendChild(el("span", "stag stag--rejected", "Not available: " + v.answer));
    if (v.state === "installing") { var d = el("button", "sbtn", "See the progress"); d.type = "button"; d.addEventListener("click", function () { progress(name); }); actions.appendChild(d); }
    if (v.state === "failed") {
      var t2 = el("button", "sbtn is-primary", "Try again"); t2.type = "button"; t2.addEventListener("click", function () { if (v.safe) install(v); else if (S.admin) adminInstall(v); else requestDialog(v); }); actions.appendChild(t2);
      var d2 = el("button", "sbtn", "See why it failed"); d2.type = "button"; d2.addEventListener("click", function () { pollDetails(name, true).then(function () { failure(name); }); }); actions.appendChild(d2);
    }
    w.appendChild(actions);
    if (v.state === "installed") {
      var row = el("div", "store__switchrow");
      var p = el("p"); p.appendChild(el("b", "", v.kind === "plugin" ? "Use it in my editor" : "Use it in my terminals")); p.appendChild(document.createElement("br"));
      p.appendChild(document.createTextNode(v.default ? "An admin enabled this for everyone. You can disable it for yourself." : v.kind === "plugin" ? "Disabled for you until you enable it. It shows in the status bar of your editor." : "Disabled for you until you enable it. It takes effect in terminals you start next."));
      row.appendChild(p);
      control(v, row);
      w.appendChild(row);
      w.appendChild(el("pre", "store__code", useLine(v)));
      if (S.admin) {
        var ev = el("button", "sbtn", v.default ? "Admin: enabled for everyone, disable that" : "Admin: enable it for every signed-in user"); ev.type = "button";
        ev.addEventListener("click", function () { adminPost({ action: "default", name: name, on: !v.default }, v.default ? name + " is no longer enabled for everyone." : name + " is enabled for every signed-in user now (each may disable it for themselves)."); });
        w.appendChild(ev);
      }
      if (v.on && v.kind !== "plugin" && S.sandbox && !S.admin) {
        w.appendChild(el("p", "store__legend", "It is enabled for terminals you open from now on. A terminal that is already open keeps what it had: open a new one (the + beside the terminal tabs)."));
        w.appendChild(el("p", "store__legend", "Turning it off removes it from your terminals only. Other users keep their own switch."));
      } else if (v.on && v.kind !== "plugin") {
        var ap = el("button", "sbtn", "Copy the line for an open terminal"); ap.type = "button"; ap.addEventListener("click", function () { copy(". '" + S.envScript + "'"); });
        w.appendChild(ap);
        w.appendChild(el("p", "store__legend", "Turning it off removes it from your terminals only. Other users keep their own switch."));
      }
      // uninstall
      var un = el("div", "store__uninstall");
      var ub = el("button", "sbtn is-danger", S.admin ? "Uninstall for everyone" : "Uninstall"); ub.type = "button";
      if (!S.admin && v.othersOn > 0) {
        ub.disabled = true;
        un.appendChild(ub);
        un.appendChild(el("p", "store__legend", "Other users have it enabled, so it stays. You can disable it for yourself, or ask an admin to remove it."));
      } else {
        ub.addEventListener("click", function () { uninstallDialog(v); });
        un.appendChild(ub);
        un.appendChild(el("p", "store__legend", S.admin ? "As an admin you can remove it even when others use it." : "Nobody else has it on, so you can remove it from the server."));
      }
      w.appendChild(un);
    } else if (v.state === "available" || v.state === "unknown") {
      w.appendChild(el("pre", "store__code", "after it is installed and on:\n" + useLine(v).replace(/^\$ /, "")));
    }
    if (v.state === "failed" && v.reported && !v.mine) {
      var me = el("button", "sbtn", "I have this problem too"); me.type = "button";
      me.addEventListener("click", function () { act("report", { name: name, note: "" }, "Added: the admin will see that more than one person is waiting."); });
      w.appendChild(me);
    }
    if (S.admin && v.kind !== "plugin") {
      var pick = el("div", "store__pick");
      pick.appendChild(el("span", "store__legend", "Admin: install another version"));
      var sel = el("select"); sel.setAttribute("aria-label", "Version");
      ((info && info.versions) || []).forEach(function (x) { var o = el("option", "", x); o.value = x; sel.appendChild(o); });
      var go2 = el("button", "sbtn", "Install it for everyone"); go2.type = "button";
      go2.addEventListener("click", function () {
        if (!sel.value) return;
        api("/admin/store", { action: "pickversion", name: name, kind: v.kind || "lib", version: sel.value }, true).then(function () { say("Installing " + name + " " + sel.value + ".", "info"); return load(); }).then(function () { render(); progress(name); }, function (e) { say(e.message, "bad"); render(); });
      });
      pick.appendChild(sel); pick.appendChild(go2);
      w.appendChild(pick);
    }
    return w;
  }

  // ---- the dialogs ----------------------------------------------------------------------------------------------------------

  var onClose = null;
  function modal(build) {
    dlg.textContent = "";
    build(dlg);
    dlg.onclose = function () { dlg.onclose = null; if (onClose) { var f = onClose; onClose = null; f(); } };
    if (!dlg.open) { if (dlg.showModal) dlg.showModal(); else dlg.setAttribute("open", ""); }
  }
  function closeModal() { if (dlg.open) dlg.close(); }
  function dialogButtons(items) {
    var d = el("div", "store-dialog__btns");
    items.forEach(function (it) {
      var b = el("button", "sbtn" + (it.primary ? " is-primary" : ""), it.label); b.type = "button";
      b.addEventListener("click", it.run);
      d.appendChild(b);
    });
    return d;
  }
  function heading(text) { var h = el("h3", "", text); h.id = "store-dialog-title"; return h; }

  function install(v) {
    act("install", { name: v.name, kind: v.kind || "lib" }).then(function () { progress(v.name); }, function () {});
  }

  // The Apps box icon, turning: the loading mark of the store (CSS only, still for reduced motion). The words stay beside it.
  function spin(px) {
    var b = el("span", "boxspin");
    b.style.setProperty("--s", px + "px");
    b.setAttribute("aria-hidden", "true");
    b.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 8l-9-5-9 5v8l9 5 9-5V8z M3 8l9 5 9-5 M12 13v8"/></svg>';
    return b;
  }
  function loadingLine(cls, text, px) {
    var p = el("p", cls + " is-loading");
    p.setAttribute("role", "status");
    p.appendChild(spin(px || 18));
    p.appendChild(el("span", "", text));
    return p;
  }

  // Details: the clean lines of an install that runs (GET /store/api/log). Closed unless the user opens them; nothing is fetched while closed.
  var det = { open: false, app: "", next: 0, lines: [], servers: null };
  function clock(ms) { var d = new Date(ms); return ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2) + ":" + ("0" + d.getSeconds()).slice(-2); }
  function pollDetails(name, all) {
    if (det.app !== name) { det.app = name; det.next = 0; det.lines = []; det.servers = null; }
    if (!det.open && !all) return Promise.resolve();
    return store("log?name=" + encodeURIComponent(name) + "&from=" + det.next).then(function (r) {
      det.lines = det.lines.concat(r.lines || []).slice(-200);
      det.next = r.next || det.next;
      det.servers = r.servers || null;
    }, function () {});
  }
  function serversLine() {
    var sv = det.servers;
    if (!sv || !sv.total) return "";
    return sv.failed ? "Other servers: " + sv.ready + " of " + sv.total + " ready, " + sv.failed + (sv.failed === 1 ? " server" : " servers") + " could not copy it" : "Copying to the other servers: " + sv.ready + " of " + sv.total + " ready";
  }
  function detailsText(last) {
    var rows = det.lines.slice(last ? -last : 0).map(function (l) { return clock(l.t) + " " + l.x; });
    var sl = serversLine();
    if (sl) rows.push(sl);
    return rows.join("\n");
  }
  function detailsBox(d, name, last) {
    var dtext = detailsText(last);
    var pre = el("pre", "store__code store-dialog__log", dtext);
    if (!dtext) { pre.appendChild(spin(14)); pre.appendChild(document.createTextNode(" Waiting for the first lines…")); }
    d.appendChild(pre);
    pre.scrollTop = pre.scrollHeight;
    var cp = el("button", "sbtn", "Copy"); cp.type = "button";
    cp.addEventListener("click", function () { var t = detailsText(0); if (navigator.clipboard) navigator.clipboard.writeText(name + "\n" + t).then(function () { cp.textContent = "Copied"; }, function () {}); });
    return cp;
  }

  // board 4: installing, with its steps; a failure and a success end it
  function progress(name) {
    var timer = 0;
    function stop() { clearTimeout(timer); }
    onClose = stop;
    function draw() {
      var v = find(name);
      if (!v) { closeModal(); return; }
      if (v.state === "failed") { stop(); onClose = null; pollDetails(name, true).then(function () { failure(name); }); return; }
      var finished = false;
      modal(function (d) {
        if (v.state === "installed") {
          finished = true;
          d.appendChild(heading(name + " is installed"));
          d.appendChild(el("p", "", "It is installed for everyone. Turn it on to use it in your terminals."));
          d.appendChild(dialogButtons([
            { label: "Turn it on", primary: true, run: function () { stop(); onClose = null; closeModal(); switchApp(v, true); } },
            { label: "Later", run: function () { closeModal(); } }
          ]));
          return;
        }
        var ih = heading("Installing " + name + (v.version ? " " + v.version : ""));
        ih.insertBefore(spin(24), ih.firstChild);
        d.appendChild(ih);
        var bar = el("div", "store-dialog__bar"); var i = el("i"); i.style.width = (v.pct || 5) + "%"; bar.appendChild(i); d.appendChild(bar);
        var ul = el("ul", "store-dialog__steps");
        (v.steps && v.steps.length ? v.steps : [{ text: v.step || "Starting", state: "current" }]).forEach(function (s) {
          var li = el("li", "is-" + s.state, (s.state === "done" ? "✓ " : s.state === "current" ? "→ " : "   ") + s.text);
          if (s.state !== "todo" && /Resolved|Downloading/.test(s.text) && v.detail) li.appendChild(el("small", "", v.detail));
          ul.appendChild(li);
        });
        d.appendChild(ul);
        var cp = null;
        if (det.open && det.app === name) cp = detailsBox(d, name, 0);
        var btns = dialogButtons([{ label: "Hide, keep going", run: function () { stop(); onClose = null; closeModal(); } },
          { label: det.open ? "Hide details" : "Show details", run: function () { det.open = !det.open; pollDetails(name).then(draw); } }]);
        if (cp) btns.appendChild(cp);
        d.appendChild(btns);
      });
      if (finished) { onClose = null; return; } // "is installed" is shown once: the dialog is not drawn again, whatever the user chose
      onClose = stop;
      timer = setTimeout(function () { Promise.all([load(), pollDetails(name)]).then(function () { render(); draw(); }); }, 1200);
    }
    draw();
  }

  function failure(name) {
    var v = find(name);
    if (!v) return;
    modal(function (d) {
      d.appendChild(heading(name + " could not be installed"));
      d.appendChild(el("p", "", (v.err ? v.err + ". " : "") + "Nothing was changed for anyone."));
      if (v.log) d.appendChild(el("pre", "store__code", v.log));
      var cpf = det.app === name && det.lines.length ? detailsBox(d, name, 12) : null;
      var items = [];
      if (v.reported) items.push({ label: v.mine ? "You told the admin" : "Reported, I have this too", run: function () { if (!v.mine) { closeModal(); act("report", { name: name, note: "" }, "Added: the admin will see that more than one person is waiting."); } } });
      else items.push({ label: "Report to admin", primary: true, run: function () { reportDialog(v); } });
      items.push({ label: "Try again", run: function () { closeModal(); install(v); } });
      items.push({ label: "Close", run: closeModal });
      var fb = dialogButtons(items);
      if (cpf) fb.appendChild(cpf);
      d.appendChild(fb);
    });
  }

  function reportDialog(v, use) {
    modal(function (d) {
      d.appendChild(heading("Tell the admin about " + v.name));
      d.appendChild(el("p", "", "What did you expect? (optional)"));
      var t = el("textarea"); t.maxLength = 300; t.setAttribute("aria-label", "What did you expect?"); d.appendChild(t);
      d.appendChild(el("p", "", "Sent with it: the app and its version, the step that failed (" + (use ? "turning it on" : v.err ? v.err.split(":")[0] : "the install") + "), the log lines above, and the server it ran on" + (v.node ? " (" + v.node + ")" : "") + ". Not sent: your files, or anything about you beyond your account."));
      d.appendChild(dialogButtons([
        { label: "Send report", primary: true, run: function () { closeModal(); act("report", { name: v.name, note: t.value }, "Sent. The admin will look at it, and you will see the answer here."); } },
        { label: "Cancel", run: function () { if (use) closeModal(); else failure(v.name); } }
      ]));
      t.focus();
    });
  }

  function requestDialog(v) {
    modal(function (d) {
      d.appendChild(heading("Request " + v.name));
      d.appendChild(el("p", "", "An admin decides, and you will see the answer here. Why do you need it? (optional)"));
      var t = el("textarea"); t.maxLength = 200; t.setAttribute("aria-label", "Why do you need it?"); d.appendChild(t);
      var rl = el("label", "store__filter", "For which language "); var rs = el("select"); rs.setAttribute("aria-label", "For which language"); rs.setAttribute("data-request-repl", "1");
      var r0 = el("option", "", "any"); r0.value = ""; rs.appendChild(r0);
      var known = {}; S.views.forEach(function (x) { (x.repls || []).forEach(function (r) { known[r] = true; }); });
      Object.keys(known).sort().forEach(function (r) { var o = el("option", "", r); o.value = r; rs.appendChild(o); });
      if (v.repls && v.repls[0]) rs.value = v.repls[0];
      rl.appendChild(rs); d.appendChild(rl);
      if (S.perDay) d.appendChild(el("p", "store__legend", "One request for each app: if someone asked already, you are added to it. You can send " + S.perDay + " requests a day (" + (S.today || 0) + " used). An admin sees the name, the language, the kind and your reason."));
      d.appendChild(dialogButtons([
        { label: "Send request", primary: true, run: function () { closeModal(); act("request", { name: v.name, kind: v.kind || "lib", repl: rs.value, reason: t.value }, "Asked. An admin will decide; you will see the answer here."); } },
        { label: "Cancel", run: closeModal }
      ]));
      t.focus();
    });
  }

  // turning it on is checked first: the server runs the app's own check for this user, on the server of their terminals, and only then says it is on
  function switchApp(v, on) {
    if (!on) return act("switch", { id: v.id, on: false }, v.name + " is off for your terminals.").catch(function () {});
    if (S.checking) return Promise.resolve();
    S.checking = v.id; say("Checking that " + v.name + " works in your terminals…", "info"); render();
    return store("switch", { id: v.id, on: true }).then(function (j) {
      S.checking = null; take(j); say(v.name + " is enabled for your terminals. It was checked and works.", "info"); render();
    }, function (e) {
      S.checking = null;
      if (e.code === "verify" && e.body) { take(e.body); say(""); render(); useFailure(v, e.body); return; }
      if (e.code === "signin") { S.error = e; } else { say(e.message, "bad"); }
      render();
    });
  }
  // it did not work for this user: it is off again; what the check said is shown, and the user can report it
  function useFailure(v, b) {
    modal(function (d) {
      if (b.wait) {
        d.appendChild(heading(v.name + " is not ready on your server yet"));
        d.appendChild(el("p", "", "Your server is still getting it. Try again in a minute. Nothing is wrong."));
        d.appendChild(dialogButtons([{ label: "Try again", primary: true, run: function () { closeModal(); switchApp(v, true); } }, { label: "Close", run: closeModal }]));
        return;
      }
      d.appendChild(heading(v.name + " did not work for your terminals"));
      d.appendChild(el("p", "", "It was checked the way your terminals start it" + (b.node ? ", on " + b.node : "") + ", and the check failed, so it is off. Nothing changed for anyone else."));
      if (b.log) d.appendChild(el("pre", "store__code", b.log));
      d.appendChild(dialogButtons([
        { label: "Report to admin", primary: true, run: function () { reportDialog({ name: v.name, err: "Turning it on", node: b.node }, true); } },
        { label: "Try again", run: function () { closeModal(); switchApp(v, true); } },
        { label: "Close", run: closeModal }
      ]));
    });
  }

  // board 4D: the answers to what the user asked, one at a time
  function showNotices() {
    store("notices").then(function (j) {
      var n = (j.notices || [])[0];
      if (!n) return;
      var v = find(n.name);
      modal(function (d) {
        d.appendChild(heading(n.status === "rejected" ? n.name + " is not available" : n.name + " is installed"));
        d.appendChild(el("p", "", n.text));
        var items = [];
        if (n.status !== "rejected" && v && v.state === "installed" && !v.on) items.push({ label: "Turn it on", primary: true, run: function () { closeModal(); store("ack", { name: n.name }); switchApp(v, true); } });
        items.push({ label: n.status === "rejected" ? "Close" : "Later", run: closeModal });
        d.appendChild(dialogButtons(items));
      });
      onClose = function () { store("ack", { name: n.name }).then(showNotices, function () {}); };
    }, function () {});
  }

  // ---- drawing --------------------------------------------------------------------------------------------------------------

  var polling = 0;
  function render() {
    var r = route();
    renderSide(r);
    main.textContent = "";
    if (S.error && S.error.code === "signin") { main.appendChild(guestCard()); return; }
    if (S.error) { main.appendChild(el("p", "store__loading", S.error.message)); return; }
    if (!S.loaded) { main.appendChild(loadingLine("store__loading", "Loading the store…", 40)); return; }
    var node;
    switch (r.name) {
      case "mine": node = viewMine(); break;
      case "requests": node = viewRequests(); break;
      case "kind": node = viewKind(r.arg); break;
      case "apt": node = S.admin ? viewApt() : viewBrowse(); break;
      case "admin": node = S.admin ? viewAdmin(r.arg) : viewBrowse(); break;
      case "app": node = viewApp(r.arg); break;
      default: node = viewBrowse();
    }
    main.appendChild(node);
    // an install that is going on: the cards follow it
    clearTimeout(polling);
    if (S.views.some(function (v) { return v.state === "installing"; }) && !dlg.open) polling = setTimeout(function () { load().then(render); }, 2000);
  }

  window.addEventListener("hashchange", function () { say(""); render(); });
  load().then(function () { render(); if (S.loaded && !S.error) showNotices(); });
  window.AppStorePage = { state: function () { return S; }, render: render };
})();
