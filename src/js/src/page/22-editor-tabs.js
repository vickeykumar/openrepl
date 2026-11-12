// The open files as tabs above the editor: up to five, the oldest leaves at six (rules: lib-tabs.mjs).
//
// It is still ONE editor. A tab is a shortcut to a file of the Files panel: choosing a tab selects the file in the
// tree, and the page's own code (07-file-browser.js) does the rest, so the selection reaches everyone in a shared
// session through Firebase (selected_node) as it always did. What is added:
//  - the list of open files (this file), kept by the OWNER's browser (the master, see isMaster) and put in Firebase
//    (file-browser/<id>/tabs) so that viewers draw the same strip, also those who join late;
//  - the owner's browser holds each tab's unsaved text in memory (it is the one that holds the document), puts it
//    back into the editor on a switch, and writes a file to the server only on Save, when a changed tab is closed,
//    or when the sixth file pushes it out. An unchanged tab is never sent;
//  - a draft copy of unsaved text in localStorage, so that a reload does not lose it.
// Designed in the "Editor file tabs" canvas (claude.ai/artifact/3Kd5gQdUvoEYLRzubdL5F3).
import { MAX_TABS, emptyState, openTab, closeTab, renamePath, removePath, restoreState, tabLabels } from "./lib-tabs.mjs";

var state = emptyState(); // the owner's list: { tabs, active }
var sharedList = [];       // a viewer's copy of it (Firebase)
var stash = {};            // path -> { text, row, col, top }: what the editor held when the tab was left
var baseline = {};         // path -> the text of the file as loaded or last saved
var lastSaved = {};        // path -> when this page last saved it (its own write comes back as a server event)
var sharedRef = null;      // firebase: file-browser/<id>/tabs
var warned = {};           // path -> true once told "changed outside"
var ignoreStash = null;    // a tab being closed: its text is not kept again on the switch it causes
var lane = null;
var MAX_DRAFT = 500 * 1024;

function master() { return typeof isMaster === "function" ? isMaster() : true; }
function aceEditor() { var e = window["editor"]; return e && e.env && e.env.editor ? e.env.editor : null; }
function tree() { return window.jQuery && $("#file-browser").jstree ? $("#file-browser").jstree(true) : null; }
function isFile(path) {
  var t = tree();
  var n = t && path ? t.get_node(path) : null;
  return !!n && n.type === "file";
}
function home() { return String(window.homedir || ""); }
function key(name) { return name + ":" + home(); }
function nameOf(path) { return String(path).split("/").pop(); }
function say(text, type) { if (window.notify) window.notify(text, { type: type || "info" }); }

// ---- what the editor holds -----------------------------------------------------------------------------------------

function currentText() { var e = aceEditor(); return e ? e.getValue() : ""; }

// Does the tab hold text that is not in the file? The shown tab is compared with what the editor has now, the others
// with what was kept. A tab whose text came back from a draft has a baseline of null: it differs from any text.
function isDirty(path) {
  if (!path) return false;
  var base = baseline[path];
  if (base === undefined) return false; // never read or saved here: not known to differ
  var text = path === state.active ? currentText() : stash[path] ? stash[path].text : undefined;
  return text !== undefined && text !== base;
}

function stashCurrent(path) {
  var e = aceEditor();
  if (!e || !path) return;
  var pos = e.getCursorPosition();
  stash[path] = { text: e.getValue(), row: pos.row, col: pos.column, top: e.getSession().getScrollTop() };
}

// Puts a tab's kept text back into the editor (owner only). False when there is none: the file is then read from the
// server as before.
function restoreFromStash(path) {
  var held = stash[path];
  if (!held || !master()) return false;
  window.updateEditorContent("", held.text, true);
  var e = aceEditor();
  if (e) {
    e.moveCursorTo(held.row, held.col);
    e.getSession().setScrollTop(held.top);
  }
  return true;
}

// ---- saving ----------------------------------------------------------------------------------------------------------

function saveText(path, text, then) {
  $.ajax({
    url: preprocessurl("/ws_filebrowser?q=save&filepath=" + path),
    method: "POST",
    processData: false,
    data: btoa(text),
    contentType: "application/octet-stream"
  }).done(function () {
    baseline[path] = text;
    lastSaved[path] = Date.now();
    delete warned[path];
    persist();
    render();
    if (then) then(true);
  }).fail(function (xhr, status, error) {
    say("Couldn't save " + nameOf(path) + ". " + (xhr.responseText || error || "Please try again."), "error");
    if (then) then(false);
  });
}

// ---- keeping it across a reload --------------------------------------------------------------------------------------

var persistTimer = 0;
function persist() {
  if (!master()) return;
  clearTimeout(persistTimer);
  persistTimer = setTimeout(persistNow, 400);
}
function persistNow() {
  if (!master() || !home()) return;
  try {
    localStorage.setItem(key("editor-tabs"), JSON.stringify({ tabs: state.tabs, active: state.active }));
    var drafts = {};
    state.tabs.forEach(function (p) {
      var held = p === state.active ? { text: currentText() } : stash[p];
      if (held && baseline[p] !== undefined && held.text !== baseline[p] && held.text.length <= MAX_DRAFT) drafts[p] = held.text;
    });
    localStorage.setItem(key("editor-drafts"), JSON.stringify(drafts));
  } catch (e) { /* storage may be full or blocked: the tabs still work */ }
}
window.addEventListener("pagehide", function () { persistNow(); });

function readSaved() {
  try {
    return {
      list: JSON.parse(localStorage.getItem(key("editor-tabs")) || "null"),
      drafts: JSON.parse(localStorage.getItem(key("editor-drafts")) || "{}") || {}
    };
  } catch (e) { return { list: null, drafts: {} }; }
}

// ---- drawing the strip -----------------------------------------------------------------------------------------------

function viewList() {
  if (master()) return { tabs: state.tabs, active: state.active };
  var t = tree();
  var sel = t ? t.get_selected()[0] : null;
  return { tabs: sharedList, active: sel || null };
}

function render() {
  lane = lane || document.getElementById("editor-tabs");
  if (!lane) return;
  var view = viewList();
  var header = lane.parentNode;
  if (header) header.classList.toggle("has-tabs", view.tabs.length > 0);
  var labels = tabLabels(view.tabs);
  var canClose = master();
  lane.textContent = "";
  view.tabs.forEach(function (path, i) {
    var active = path === view.active;
    var tab = document.createElement("div");
    tab.className = "etab" + (active ? " active" : "");
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-selected", active ? "true" : "false");
    tab.setAttribute("aria-label", labels[i] + (master() && isDirty(path) ? ", unsaved changes" : ""));
    tab.tabIndex = active ? 0 : -1;
    tab.dataset.path = path;
    tab.title = path;
    var name = document.createElement("span");
    name.className = "etab__name";
    name.textContent = labels[i];
    tab.appendChild(name);
    if (master() && isDirty(path)) {
      var dot = document.createElement("span");
      dot.className = "etab__dot";
      tab.appendChild(dot);
    }
    if (canClose) {
      var x = document.createElement("button");
      x.type = "button";
      x.className = "etab__x";
      x.tabIndex = -1;
      x.setAttribute("aria-label", "Close " + labels[i]);
      x.textContent = "×";
      tab.appendChild(x);
    }
    lane.appendChild(tab);
  });
  var on = lane.querySelector(".etab.active");
  if (on && window.TabLane) window.TabLane.reveal(lane, on);
}

function laneEvents() {
  lane = document.getElementById("editor-tabs");
  if (!lane || lane.dataset.ready) return;
  lane.dataset.ready = "1";
  lane.addEventListener("click", function (e) {
    var tab = e.target.closest && e.target.closest(".etab");
    if (!tab) return;
    if (e.target.closest(".etab__x")) closeByUser(tab.dataset.path);
    else activate(tab.dataset.path);
  });
  lane.addEventListener("auxclick", function (e) {
    var tab = e.target.closest && e.target.closest(".etab");
    if (tab && e.button === 1 && master()) { e.preventDefault(); closeByUser(tab.dataset.path); }
  });
  lane.addEventListener("keydown", function (e) {
    var tab = e.target.closest && e.target.closest(".etab");
    if (!tab) return;
    var tabs = [].slice.call(lane.querySelectorAll(".etab"));
    var i = tabs.indexOf(tab);
    var to = null;
    if (e.key === "ArrowRight") to = tabs[Math.min(i + 1, tabs.length - 1)];
    else if (e.key === "ArrowLeft") to = tabs[Math.max(i - 1, 0)];
    else if (e.key === "Home") to = tabs[0];
    else if (e.key === "End") to = tabs[tabs.length - 1];
    else if ((e.key === "Delete" || e.key === "Backspace") && master()) { e.preventDefault(); closeByUser(tab.dataset.path); return; }
    else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); activate(tab.dataset.path); return; }
    if (to) { e.preventDefault(); to.focus(); activate(to.dataset.path); }
  });
}

// ---- choosing, opening, closing ----------------------------------------------------------------------------------------

// A tab was chosen: select its file in the tree. The tree's own handler (07-file-browser.js, "changed.jstree") keeps
// the old text, shows this one, and tells everyone through Firebase.
function activate(path) {
  var t = tree();
  if (!t || !path || !t.get_node(path)) return;
  if (t.get_selected()[0] === path) return;
  t.deselect_all(true);
  t.select_node(path);
}

function closeByUser(path) {
  if (!master()) return;
  var finish = function () {
    var wasActive = state.active === path;
    ignoreStash = wasActive ? path : null;
    var r = closeTab(state, path);
    state = { tabs: r.tabs, active: r.active };
    delete stash[path];
    delete baseline[path];
    delete warned[path];
    if (wasActive) {
      if (state.active) activate(state.active);
      else clearEditor();
    }
    ignoreStash = null;
    persist();
    render();
    publish();
  };
  if (isDirty(path)) {
    if (path === state.active) stashCurrent(path);
    saveText(path, stash[path] ? stash[path].text : currentText(), function (ok) { if (ok) finish(); });
  } else {
    finish();
  }
}

// no tab is left: the editor is an empty "untitled" buffer
function clearEditor() {
  var t = tree();
  if (t) t.deselect_all(true);
  var e = aceEditor();
  if (e) e.setValue("", -1);
  if (window["editor"] && window["editor"].env) window["editor"].env.filename = "";
  if (window.updateEditorFileChip) window.updateEditorFileChip();
}

function publish() {
  if (!sharedRef || !master()) return;
  try { sharedRef.set({ tabs: state.tabs, ts: Date.now() }); } catch (e) { /* Firebase is optional here */ }
}

// ---- what 07-file-browser.js and 02-layout.js tell it --------------------------------------------------------------------

var api = {
  // The tree is ready: take the saved list and drafts of this workspace back.
  treeReady: function () {
    laneEvents();
    var e = aceEditor();
    if (!master()) { render(); return; }
    var saved = readSaved();
    var t = tree();
    state = restoreState(saved.list, function (p) { return isFile(p); });
    var recovered = [];
    state.tabs.forEach(function (p) {
      if (typeof saved.drafts[p] === "string") {
        stash[p] = { text: saved.drafts[p], row: 0, col: 0, top: 0 };
        baseline[p] = null;
        recovered.push(nameOf(p));
      }
    });
    if (recovered.length) say("Recovered unsaved changes in " + recovered.join(", ") + ". They are marked with a dot; Save keeps them.", "info");
    // what the tree shows selected right now is what the editor shows
    var sel = t ? t.get_selected()[0] : null;
    if (sel && isFile(sel)) state = openTab(state, sel);
    render();
    publish();
    if (e && !e.__tabsBound) {
      e.__tabsBound = true;
      var timer = 0;
      e.on("change", function () {
        clearTimeout(timer);
        timer = setTimeout(function () { render(); persist(); }, 300);
      });
    }
  },

  // Before the editor leaves a file (the tree's selection changed): keep its text.
  beforeSwitch: function (next) {
    if (!master()) return;
    var old = window["editor"] && window["editor"].env ? window["editor"].env.filename : "";
    if (old && old !== next && old !== ignoreStash && state.tabs.indexOf(old) >= 0) stashCurrent(old);
  },

  // A file was selected: it becomes a tab. Returns true when its text was put back from memory (the caller then does
  // not read the file from the server).
  selected: function (path, alreadyShown) {
    if (!isFile(path)) return false;
    if (master()) {
      var r = openTab(state, path);
      state = { tabs: r.tabs, active: r.active };
      r.dropped.forEach(function (p) {
        if (isDirty(p)) {
          var text = stash[p] ? stash[p].text : "";
          if (text) saveText(p, text, function (ok) { if (ok) say("Closed " + nameOf(p) + " (saved)", "info"); });
        }
        delete stash[p];
        delete baseline[p];
        delete warned[p];
      });
      var restored = alreadyShown ? true : restoreFromStash(path);
      persist();
      render();
      publish();
      return restored;
    }
    render();
    return false;
  },

  loaded: function (path, text) {
    if (!master()) return;
    baseline[path] = text;
    delete stash[path];
    render();
  },

  saved: function (path, text) {
    baseline[path] = text;
    lastSaved[path] = Date.now();
    delete stash[path];
    delete warned[path];
    persist();
    render();
  },

  // The server says a file was written (the terminal, another program). True: do not reload it into the editor.
  write: function (path) {
    if (!master()) return false;
    if (Date.now() - (lastSaved[path] || 0) < 3000) return true; // the echo of this page's own save
    if (state.tabs.indexOf(path) < 0) return false;
    if (isDirty(path)) {
      if (!warned[path]) {
        warned[path] = true;
        say(nameOf(path) + " was changed outside the editor. Your unsaved changes here are kept; saving will replace the file.", "warning");
      }
      return true;
    }
    if (path !== state.active) { delete stash[path]; } // read again from the server when it is shown
    return false;
  },

  renamed: function (from, to) {
    if (!from || !to || from === to) return;
    var move = function (map) {
      Object.keys(map).forEach(function (p) {
        if (p === from || p.indexOf(from + "/") === 0) {
          map[to + p.slice(from.length)] = map[p];
          delete map[p];
        }
      });
    };
    move(stash); move(baseline); move(lastSaved);
    if (master()) {
      var r = renamePath(state, from, to);
      state = { tabs: r.tabs, active: r.active };
      persist();
      publish();
    }
    render();
  },

  removed: function (path) {
    if (!path) return;
    if (master()) {
      var wasActive = state.active && (state.active === path || state.active.indexOf(path + "/") === 0);
      var r = removePath(state, path);
      state = { tabs: r.tabs, active: wasActive ? null : r.active };
      r.dropped.forEach(function (p) { delete stash[p]; delete baseline[p]; delete warned[p]; });
      persist();
      publish();
    }
    render();
  },

  // Firebase: file-browser/<id> (the same node as selected_node)
  attachShared: function (browserRef) {
    sharedRef = browserRef.child("tabs");
    if (!master()) {
      sharedRef.on("value", function (snap) {
        var v = snap.val();
        sharedList = v && Array.isArray(v.tabs) ? v.tabs.filter(function (p) { return typeof p === "string"; }) : [];
        render();
      });
    }
  },

  // a viewer's tree selection changed: the strip's shown tab follows
  selectionChanged: function () { if (!master()) render(); },

  // for tests and for other page code
  state: function () { return { tabs: state.tabs.slice(), active: state.active }; },
  max: MAX_TABS
};
window.EditorTabs = api;

document.addEventListener("DOMContentLoaded", laneEvents);
