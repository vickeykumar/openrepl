// The App Store page (resources/store.html; designed in the canvas "App Store: all screens and actions"; docs/lld/18-app-store.md).
// The page holds no data: everything comes from /store/api, which checks who is asking and refuses guests. Sections are the
// address hash: #browse, #mine, #requests, #kind/lib, #kind/tool, #kind/languages, #kind/plugins, #apt (admins), #app/<name>.
(function () {
  "use strict";

  var S = { views: [], on: [], home: "", envScript: "", admin: false, python: "", loaded: false, error: null, q: "", chip: "all", info: {}, note: null, watch: null };
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
        if (!res.ok) { var e = new Error(j.message || j.error || "The App Store did not answer."); e.code = j.error; e.status = res.status; throw e; }
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
    ["home", "envScript", "admin", "python"].forEach(function (k) { if (j[k] !== undefined) S[k] = j[k]; });
    S.loaded = true; S.error = null;
  }
  function load() {
    return store("list").then(function (j) { take(j); }, function (e) { S.error = e; S.loaded = true; });
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
    link("My requests", "#requests", counts.req, r.name === "requests");
    side.appendChild(el("h2", "", "Kinds"));
    link("Python libraries", "#kind/lib", 0, r.name === "kind" && r.arg === "lib");
    link("Tools", "#kind/tool", 0, r.name === "kind" && r.arg === "tool");
    link("Languages (admin)", "#kind/languages", 0, r.name === "kind" && r.arg === "languages");
    link("Editor plugins (later)", "#kind/plugins", 0, r.name === "kind" && r.arg === "plugins");
    if (S.admin) {
      side.appendChild(el("h2", "", "System (admin)"));
      link("apt packages", "#apt", 0, r.name === "apt");
    }
  }

  function noteBox() {
    if (!S.note) return null;
    return el("div", "store__note is-" + S.note.kind, S.note.text);
  }

  function guestCard() {
    var g = el("div", "store__guest");
    g.appendChild(el("b", "", "The App Store is for signed-in users"));
    g.appendChild(el("span", "", "Sign in to add libraries and tools to your terminals."));
    var a = el("a", "sbtn is-primary", "Go to the editor to sign in");
    a.href = "./";
    g.appendChild(a);
    return g;
  }

  function stateTag(v) {
    var t = { installed: "Installed", installing: "Installing" + (v.pct ? " " + v.pct + "%" : ""), failed: "Failed", requested: "Needs approval", rejected: "Not available" }[v.state];
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
    if (v.state === "available") btn("Install", function () { install(v); }, "is-primary");
    else if (v.state === "installing") btn("Details", function () { progress(v.name); });
    else if (v.state === "installed") {
      var sw = el("button", "sswitch" + (v.on ? " is-on" : ""), v.on ? "On" : "Off");
      sw.type = "button"; sw.setAttribute("role", "switch"); sw.setAttribute("aria-checked", v.on ? "true" : "false");
      sw.setAttribute("aria-label", "Use " + v.name + " in my terminals");
      sw.addEventListener("click", function (e) { e.stopPropagation(); switchApp(v, !v.on); });
      row.appendChild(sw);
    } else if (v.state === "failed") btn("Details", function () { failure(v.name); });
    else if (v.state === "rejected") btn("Ask again", function () { requestDialog(v); });
    else if (v.state === "requested") btn("Details", function () { go("#app/" + encodeURIComponent(v.name)); });
  }

  function card(v) {
    var c = el("article", "sapp is-" + v.state);
    c.setAttribute("data-app", v.name);
    var name = el("a", "sapp__name", v.name);
    name.href = "#app/" + encodeURIComponent(v.name);
    c.appendChild(name);
    var info = S.info[v.name];
    if (!info) loadInfo(v.name);
    c.appendChild(el("p", "sapp__sum", info && info.summary ? info.summary : v.state === "failed" ? v.err || "The install stopped." : v.state === "rejected" && v.answer ? v.answer : v.kind === "tool" ? "A tool." : ""));
    var row = el("div", "sapp__row");
    if (v.version) row.appendChild(el("span", "sapp__ver", v.version));
    var tag = stateTag(v);
    if (tag) row.appendChild(tag);
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
    c.appendChild(el("p", "sapp__sum", !info || info.loading ? "Looking it up on conda-forge…" : info.error ? info.error : (info.summary || "No description.")));
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

  function viewBrowse(kind) {
    var wrap = el("div", "store__list");
    wrap.style.cssText = "display:flex;flex-direction:column;gap:12px";
    var tools = el("div", "store__tools");
    var input = el("input"); input.type = "search"; input.id = "store-search"; input.value = S.q;
    input.placeholder = "Search the store, or type the name of a conda-forge package";
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
    var nb = noteBox(); if (nb) wrap.appendChild(nb);
    var q = S.q.trim().toLowerCase();
    var list = S.views.filter(function (v) { return matches(v, q) && chipOk(v) && (!kind || (v.kind || "lib") === kind); });
    var grid = el("div", "store__grid");
    list.forEach(function (v) { grid.appendChild(card(v)); });
    if (!list.length && q && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(q) && !kind) grid.appendChild(lookupCard(q));
    wrap.appendChild(grid);
    if (!list.length && !(q && !kind)) wrap.appendChild(el("p", "store__legend", "Nothing matches here."));
    wrap.appendChild(legend());
    input.addEventListener("input", function () {
      S.q = input.value;
      var pos = input.selectionStart;
      render();
      var again = document.getElementById("store-search");
      if (again) { again.focus(); try { again.setSelectionRange(pos, pos); } catch (e) { /* not every input has a caret */ } }
    });
    return wrap;
  }

  function legend() {
    var l = el("p", "store__legend");
    var b = el("b", "", "States: ");
    l.appendChild(b);
    l.appendChild(document.createTextNode("not installed (Install, or Request when it needs an admin) · installing (progress) · installed (a switch, off until you turn it on) · needs approval (waiting for an admin) · failed (see why, report it). Installed means installed for everyone; the switch is yours alone."));
    return l;
  }

  function viewMine() {
    var wrap = el("div", "store__rows");
    var nb = noteBox(); if (nb) wrap.appendChild(nb);
    var mineList = S.views.filter(function (v) { return v.on || (v.mine && v.state !== "available"); });
    mineList.forEach(function (v) {
      var r = el("div", "store__row");
      r.setAttribute("data-app", v.name);
      var d = el("div");
      var a = el("a", "sapp__name", v.name + (v.version ? " " + v.version : "")); a.href = "#app/" + encodeURIComponent(v.name);
      d.appendChild(a);
      d.appendChild(el("small", "", (v.kind === "tool" ? "Tool" : "Python library") + (v.state === "requested" ? ", asked for " + day(v.at) : v.state === "failed" ? ", failed" : "")));
      r.appendChild(d);
      var tag = stateTag(v); if (tag && v.state !== "installed") r.appendChild(tag);
      control(v, r);
      wrap.appendChild(r);
    });
    if (!mineList.length) wrap.appendChild(el("p", "store__legend", "Nothing is on for you yet. Browse the store and switch an app on."));
    // what the switches do to terminals that are open
    var apply = el("div", "store__note is-info");
    apply.appendChild(el("p", "", "A terminal that is open keeps what it had when it started; a new one uses what is on now. To use it in an open terminal, run this line in it:"));
    apply.appendChild(el("pre", "store__code", ". '" + S.envScript + "'"));
    var cp = el("button", "sbtn", "Copy the line"); cp.type = "button";
    cp.addEventListener("click", function () { copy(". '" + S.envScript + "'"); });
    apply.appendChild(cp);
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
    if (arg === "lib" || arg === "tool") return viewBrowse(arg);
    var w = el("div", "store__detail");
    if (arg === "languages") {
      w.appendChild(el("h2", "", "Languages (admin)"));
      w.appendChild(el("p", "store__legend", "A new language (a REPL with its compiler or interpreter) is large, and it belongs in the server's image, so an admin adds it there. It is not part of the store yet."));
    } else {
      w.appendChild(el("h2", "", "Editor plugins (later)"));
      w.appendChild(el("p", "store__legend", "Plugins would run inside your own page, so they need a security design of their own first. They are not part of the store yet."));
    }
    return w;
  }

  function viewApt() {
    var w = el("div", "store__detail");
    w.appendChild(el("h2", "", "apt packages"));
    w.appendChild(el("p", "store__legend", "Debian packages an admin approved. They are installed on every server for every user, with no switch."));
    var box = el("div", "store__rows");
    box.appendChild(el("p", "store__legend", "Loading…"));
    w.appendChild(box);
    api("/admin/store", null, true).then(function (d) {
      box.textContent = "";
      (d.apt || []).forEach(function (p) { var r = el("div", "store__row"); var dd = el("div"); dd.appendChild(el("b", "", p)); r.appendChild(dd); box.appendChild(r); });
      if (!(d.apt || []).length) box.appendChild(el("p", "store__legend", "No apt package has been approved."));
      var a = el("a", "sbtn", "Edit in the admin dashboard"); a.href = "./admin#store"; box.appendChild(a);
    }, function (e) { box.textContent = ""; box.appendChild(el("p", "store__legend", e.message)); });
    return w;
  }

  // ---- an app's page --------------------------------------------------------------------------------------------------------

  function viewApp(name) {
    var v = find(name) || { name: name, kind: "lib", state: "unknown", safe: false };
    var w = el("div", "store__detail");
    w.setAttribute("data-app", name);
    var back = el("a", "store__back", "← Back to the store"); back.href = "#browse"; w.appendChild(back);
    var head = el("div", "store__dhead");
    head.appendChild(el("span", "store__logo", name.charAt(0)));
    var hd = el("div");
    hd.appendChild(el("h2", "", name));
    hd.appendChild(el("small", "", (v.version ? v.version + " · " : "") + "conda-forge"));
    head.appendChild(hd);
    var tag = stateTag(v); if (tag) head.appendChild(tag);
    w.appendChild(head);
    var nb = noteBox(); if (nb) w.appendChild(nb);
    var info = S.info[name];
    if (!info) loadInfo(name);
    w.appendChild(el("p", "store__legend", !info || info.loading ? "Looking it up…" : info.error ? info.error : (info.summary || "No description.")));
    var kv = el("div", "store__kv");
    function kvRow(k, val) { kv.appendChild(el("span", "", k)); kv.appendChild(el("b", "", val)); }
    kvRow("Size", v.state === "installed" && v.sizeMB ? "about " + v.sizeMB.toFixed(v.sizeMB < 10 ? 1 : 0) + " MB on the server" + (v.packages ? " (" + v.packages + " packages)" : "") : "shown when it is installed");
    kvRow("Needs", S.python ? "Python " + S.python + " (the server's)" : "the server's Python");
    kvRow("Who can", v.state === "installed" ? "Everyone can switch it on for themselves" : v.safe ? "You, right away (it is on the safe list)" : "An admin approves it first");
    w.appendChild(kv);
    var actions = el("div", "sapp__row");
    var mod = moduleOf(name);
    if (v.state === "available" || (v.state === "unknown" && v.safe)) {
      var i = el("button", "sbtn is-primary", "Install " + name); i.type = "button"; i.addEventListener("click", function () { install(v); }); actions.appendChild(i);
    } else if (v.state === "unknown" || v.state === "rejected") {
      var r = el("button", "sbtn is-primary", "Request " + name); r.type = "button"; r.addEventListener("click", function () { requestDialog(v); }); actions.appendChild(r);
    }
    if (v.state === "requested") actions.appendChild(el("span", "stag stag--requested", "Requested " + day(v.at) + ", waiting for an admin"));
    if (v.state === "rejected" && v.answer) actions.appendChild(el("span", "stag stag--rejected", "Not available: " + v.answer));
    if (v.state === "installing") { var d = el("button", "sbtn", "See the progress"); d.type = "button"; d.addEventListener("click", function () { progress(name); }); actions.appendChild(d); }
    if (v.state === "failed") {
      var d2 = el("button", "sbtn", "See why it failed"); d2.type = "button"; d2.addEventListener("click", function () { failure(name); }); actions.appendChild(d2);
    }
    w.appendChild(actions);
    if (v.state === "installed") {
      var row = el("div", "store__switchrow");
      var p = el("p"); p.appendChild(el("b", "", "Use it in my terminals")); p.appendChild(document.createElement("br"));
      p.appendChild(document.createTextNode("Off for you until you turn it on. It takes effect in terminals you start next."));
      row.appendChild(p);
      control(v, row);
      w.appendChild(row);
      w.appendChild(el("pre", "store__code", v.kind === "tool" ? "$ " + name + " --help" : "$ python3 -c \"import " + mod + "\""));
      if (v.on) {
        var ap = el("button", "sbtn", "Copy the line for an open terminal"); ap.type = "button"; ap.addEventListener("click", function () { copy(". '" + S.envScript + "'"); });
        w.appendChild(ap);
        w.appendChild(el("p", "store__legend", "Turning it off removes it from your terminals only. Other users keep their own switch."));
      }
    } else if (v.state === "available" || v.state === "unknown") {
      w.appendChild(el("pre", "store__code", "after it is installed and on:\n" + (v.kind === "tool" ? name + " --help" : "import " + mod)));
    }
    if (v.state === "failed" && v.reported && !v.mine) {
      var me = el("button", "sbtn", "I have this problem too"); me.type = "button";
      me.addEventListener("click", function () { act("report", { name: name, note: "" }, "Added: the admin will see that more than one person is waiting."); });
      w.appendChild(me);
    }
    if (S.admin) {
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

  // board 4: installing, with its steps; a failure and a success end it
  function progress(name) {
    var timer = 0;
    function stop() { clearTimeout(timer); }
    onClose = stop;
    function draw() {
      var v = find(name);
      if (!v) { closeModal(); return; }
      if (v.state === "failed") { stop(); onClose = null; failure(name); return; }
      modal(function (d) {
        if (v.state === "installed") {
          d.appendChild(heading(name + " is installed"));
          d.appendChild(el("p", "", "It is installed for everyone. Turn it on to use it in your terminals."));
          d.appendChild(dialogButtons([
            { label: "Turn it on", primary: true, run: function () { stop(); onClose = null; closeModal(); switchApp(v, true); } },
            { label: "Later", run: function () { closeModal(); } }
          ]));
          return;
        }
        d.appendChild(heading("Installing " + name + (v.version ? " " + v.version : "")));
        var bar = el("div", "store-dialog__bar"); var i = el("i"); i.style.width = (v.pct || 5) + "%"; bar.appendChild(i); d.appendChild(bar);
        var ul = el("ul", "store-dialog__steps");
        (v.steps && v.steps.length ? v.steps : [{ text: v.step || "Starting", state: "current" }]).forEach(function (s) {
          var li = el("li", "is-" + s.state, (s.state === "done" ? "✓ " : s.state === "current" ? "→ " : "   ") + s.text);
          if (s.state !== "todo" && /Resolved|Downloading/.test(s.text) && v.detail) li.appendChild(el("small", "", v.detail));
          ul.appendChild(li);
        });
        d.appendChild(ul);
        d.appendChild(dialogButtons([{ label: "Hide, keep going", run: function () { stop(); onClose = null; closeModal(); } }]));
      });
      onClose = stop;
      timer = setTimeout(function () { load().then(function () { render(); draw(); }); }, 1200);
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
      var items = [];
      if (v.reported) items.push({ label: v.mine ? "You told the admin" : "Reported, I have this too", run: function () { if (!v.mine) { closeModal(); act("report", { name: name, note: "" }, "Added: the admin will see that more than one person is waiting."); } } });
      else items.push({ label: "Report to admin", primary: true, run: function () { reportDialog(v); } });
      items.push({ label: "Try again", run: function () { closeModal(); install(v); } });
      items.push({ label: "Close", run: closeModal });
      d.appendChild(dialogButtons(items));
    });
  }

  function reportDialog(v) {
    modal(function (d) {
      d.appendChild(heading("Tell the admin about " + v.name));
      d.appendChild(el("p", "", "What did you expect? (optional)"));
      var t = el("textarea"); t.maxLength = 300; t.setAttribute("aria-label", "What did you expect?"); d.appendChild(t);
      d.appendChild(el("p", "", "Sent with it: the app and its version, the step that failed (" + (v.err ? v.err.split(":")[0] : "the install") + "), the log lines above, and the server it ran on" + (v.node ? " (" + v.node + ")" : "") + ". Not sent: your files, or anything about you beyond your account."));
      d.appendChild(dialogButtons([
        { label: "Send report", primary: true, run: function () { closeModal(); act("report", { name: v.name, note: t.value }, "Sent. The admin will look at it, and you will see the answer here."); } },
        { label: "Cancel", run: function () { failure(v.name); } }
      ]));
      t.focus();
    });
  }

  function requestDialog(v) {
    modal(function (d) {
      d.appendChild(heading("Request " + v.name));
      d.appendChild(el("p", "", "An admin decides, and you will see the answer here. Why do you need it? (optional)"));
      var t = el("textarea"); t.maxLength = 200; t.setAttribute("aria-label", "Why do you need it?"); d.appendChild(t);
      d.appendChild(dialogButtons([
        { label: "Send request", primary: true, run: function () { closeModal(); act("request", { name: v.name, kind: v.kind || "lib", reason: t.value }, "Asked. An admin will decide; you will see the answer here."); } },
        { label: "Cancel", run: closeModal }
      ]));
      t.focus();
    });
  }

  function switchApp(v, on) {
    return act("switch", { id: v.id, on: on }, on ? v.name + " is on for your terminals." : v.name + " is off for your terminals.").catch(function () {});
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
    if (!S.loaded) { main.appendChild(el("p", "store__loading", "Loading…")); return; }
    var node;
    switch (r.name) {
      case "mine": node = viewMine(); break;
      case "requests": node = viewRequests(); break;
      case "kind": node = viewKind(r.arg); break;
      case "apt": node = S.admin ? viewApt() : viewBrowse(); break;
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
