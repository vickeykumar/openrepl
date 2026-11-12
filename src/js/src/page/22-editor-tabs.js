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
import { MAX_TABS, UNTITLED, emptyState, openTab, closeTab, renamePath, removePath, restoreState, tabLabels } from "./lib-tabs.mjs";

var state = emptyState(); // the owner's list: { tabs, active }
var sharedList = [];       // a viewer's copy of it (Firebase)
var stash = {};            // path -> { text, row, col, top }: what the editor held when the tab was left
var baseline = {};         // path -> the text of the file as loaded or last saved
var lastSaved = {};        // path -> when this page last saved it (its own write comes back as a server event)
var sharedRef = null;      // firebase: file-browser/<id>/tabs
var warned = {};           // path -> true once told "changed outside"
var shown = null;          // the file whose text the editor really holds (null: none yet, or a load is under way)
var ignoreStash = null;    // a tab being closed: its text is not kept again on the switch it causes
var lane = null;
var lastSig = "";          // what the strip last drew, to skip a redraw that would look the same
var lastRevealed = null;  // the file whose tab was last brought into view
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
function isUntitled(path) { return path === UNTITLED; }
function nameOf(path) { return isUntitled(path) ? "untitled" : String(path).split("/").pop(); }
function say(text, type) { if (window.notify) window.notify(text, { type: type || "info" }); }

// ---- what the editor holds -----------------------------------------------------------------------------------------

function currentText() { var e = aceEditor(); return e ? e.getValue() : ""; }

// Does the tab hold text that is not in the file? The shown tab is compared with what the editor has now, the others
// with what was kept. A tab whose text came back from a draft has a baseline of null: it differs from any text.
// The text a tab holds: the editor's, only while the editor really holds that file (a tab that was just clicked still
// shows the previous file's text until its own text has come: that is not its text), else the kept copy.
function textOf(path) {
  if (path === shown) return currentText();
  return stash[path] ? stash[path].text : undefined;
}

function isDirty(path) {
  if (!path) return false;
  var base = baseline[path];
  if (base === undefined) return false; // never read or saved here: not known to differ
  var text = textOf(path);
  return text !== undefined && text !== base;
}

function stashCurrent(path) {
  var e = aceEditor();
  if (!e || !path) return;
  var pos = e.getCursorPosition();
  var session = e.getSession();
  var um = session.getUndoManager();
  stash[path] = { text: e.getValue(), row: pos.row, col: pos.column, top: session.getScrollTop(), undoManager: um };
  // The undo history of this file stays with it (the manager is kept in the stash), and the editor is given a blank one for
  // the file that comes next: the text that replaces this one is not an edit of this file, and must not become a step in
  // its history. (Ace's toJSON and fromJSON of the undo manager are empty in the version in use, so the manager itself is
  // what is kept; a manager only holds steps that fit the text the file had when it was left, which is what comes back.)
  try { session.setUndoManager(new (Object.getPrototypeOf(um).constructor)()); } catch (err) { /* no undo manager */ }
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
    // the file's own undo history comes back: Ctrl+Z takes back what was typed in this file, and nothing of another
    if (held.undoManager) { try { e.getSession().setUndoManager(held.undoManager); } catch (err) { /* keep the blank one */ } }
  }
  return true;
}

// ---- the untitled tab ------------------------------------------------------------------------------------------------
// A buffer that is not a file of the workspace: one at a time, shown by its tab or by the + above the editor. It is saved
// with a name (saveAsNew) and then becomes that file's tab. Its text is kept like any tab's (stash, and a draft in
// localStorage), and nothing is written to the server until it is saved.

// the text the editor shows came from the page itself (a starter, a question), not from the visitor
function ownText(text) { return text !== undefined && text !== "" && text !== window[window.CONTENT_KEY || "editorContent"]; }

function hasUntitled() { return state.tabs.indexOf(UNTITLED) >= 0; }

function showUntitled() {
  if (!master()) return;
  var e = aceEditor();
  if (!e) return;
  if (shown === UNTITLED) { e.focus(); return; }
  var t = tree();
  var fileOpen = shown && state.tabs.indexOf(shown) >= 0;
  // the text of the file that is shown goes back into its tab
  if (fileOpen && shown !== ignoreStash) stashCurrent(shown);
  var implicit = !fileOpen && !hasUntitled() && !(t && t.get_selected().length);
  if (implicit) {
    // the editor already is an untitled buffer (nothing is open): the tab is that buffer
    stash[UNTITLED] = { text: e.getValue(), row: 0, col: 0, top: 0 };
    baseline[UNTITLED] = ownText(e.getValue()) ? "" : e.getValue();
  } else if (!hasUntitled()) {
    stash[UNTITLED] = { text: "", row: 0, col: 0, top: 0 };
    baseline[UNTITLED] = "";
  }
  var r = openTab(state, UNTITLED, MAX_TABS, isDirty);
  state = { tabs: r.tabs, active: r.active };
  dropTabs(r.dropped);
  // not silent: the tree's change tracking must see the file leave, or choosing the same file again later is not a change
  if (t) t.deselect_all();
  restoreFromStash(UNTITLED);
  shown = UNTITLED;
  if (window["editor"] && window["editor"].env) window["editor"].env.filename = "";
  if (window.updateEditorFileChip) window.updateEditorFileChip();
  persist();
  render();
  publish();
  e.focus();
}

// the default name for a new file: main.<extension of the language in the list>, main-1.<ext> when that exists
var EXT = { assembly_x86: "asm", c_cpp: "c", css: "css", golang: "go", html: "html", java: "java", javascript: "js", json: "json",
  markdown: "md", perl: "pl", python: "py", ruby: "rb", rust: "rs", sh: "sh", sql: "sql", tcl: "tcl", typescript: "ts", xml: "xml",
  yaml: "yaml", toml: "toml", text: "txt", makefile: "mk", ini: "ini", protobuf: "proto", diff: "diff", batchfile: "bat" };
function defaultName() {
  var sel = document.getElementById("select-lang");
  var ext = (sel && EXT[sel.value]) || "txt";
  var base = ext === "java" ? "Main" : "main";
  var t = tree();
  var name = base + "." + ext;
  var n = 1;
  while (t && t.get_node(home() + "/" + name)) name = base + "-" + (n++) + "." + ext;
  return name;
}

// what is wrong with a name for a new file of the workspace folder, or ""
function badName(name) {
  var t = tree();
  if (!name) return "Type a name.";
  if (/[\/\\\u0000]/.test(name)) return "A name has no slash in it: the file goes in your workspace folder.";
  if (name.charAt(0) === ".") return "A name that starts with a dot is a hidden file. Pick another.";
  if (name.length > 100) return "That name is too long.";
  if (t && t.get_node(home() + "/" + name)) return name + " exists already. Pick another name.";
  return "";
}

// asks for a name, makes the file and writes `text` to it: then(true, path), or then(false) when it was cancelled or failed
function saveAsNew(text, then) {
  function go(value, note) {
    if (!window.Unsaved || !window.Unsaved.askName || !window.FileBrowser || !window.FileBrowser.create) { say("An untitled file cannot be saved here.", "error"); then(false); return; }
    window.Unsaved.askName(value, note).then(function (name) {
      if (name === null) { then(false); return; }
      var bad = badName(name);
      if (bad) { go(name, bad); return; }
      Promise.resolve(window.FileBrowser.create(name, "file")).then(function (made) {
        if (!made || !made.ok) { go(name, (made && made.error) || "The file could not be made."); return; }
        var path = home() + "/" + name;
        saveText(path, text, function (ok) { then(ok, ok ? path : null); });
      });
    });
  }
  go(defaultName(), "");
}

// the untitled tab was saved as `path`: its tab is that file's now (and shows it, when it was shown)
function becomeFile(path, text) {
  var wasShown = shown === UNTITLED;
  var r = renamePath(state, UNTITLED, path);
  state = { tabs: r.tabs, active: r.active };
  var held = stash[UNTITLED] || { text: text, row: 0, col: 0, top: 0 };
  delete stash[UNTITLED];
  delete baseline[UNTITLED];
  baseline[path] = text;
  held.text = text;
  stash[path] = held;
  if (wasShown) {
    shown = null;
    var t = tree();
    if (t) { t.deselect_all(true); t.select_node(path); } // the tree's handler puts the kept text back: the file is not read again
  }
  persist();
  render();
  publish();
}

function saveUntitled(then) {
  var text = textOf(UNTITLED);
  if (text === undefined) { if (then) then(true); return; }
  saveAsNew(text, function (ok, path) {
    if (ok) becomeFile(path, text);
    if (then) then(ok, path);
  });
}

// ---- saving ----------------------------------------------------------------------------------------------------------

// writes what a tab being closed or dropped holds: a file to its path, the untitled text to a file the visitor names
function writeOut(path, text, then) {
  if (isUntitled(path)) saveAsNew(text, function (ok) { then(ok); });
  else saveText(path, text, then);
}

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
    if (window.FileMarks) window.FileMarks.event("write", path);
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
  persistList(); // the list is small: at once, so that a reload right after opening a file still finds its tab
  clearTimeout(persistTimer);
  persistTimer = setTimeout(persistNow, PERSIST_WAIT);
}
function persistList() {
  if (!master() || !home() || !restored) return;
  try {
    var list = JSON.stringify({ tabs: state.tabs, active: state.active });
    if (list !== lastListJson) { localStorage.setItem(key("editor-tabs"), list); lastListJson = list; }
  } catch (e) { /* storage may be full or blocked */ }
}
// localStorage is written on the page's own thread, so only what changed is written: the list when it changed, and the
// drafts only of tabs whose text differs from the last time (a draft can be 500 KB, and typing changes it all the time,
// so it waits for a pause of PERSIST_WAIT).
var PERSIST_WAIT = 1500;
var lastListJson = "";
// Nothing is written to localStorage before the saved list has been read back (treeReady). The tree can select a file
// while it loads, before that; writing then would replace the saved list with that one file.
var restored = false;
var lastDraftsJson = "";
function persistNow() {
  if (!master() || !home() || !restored) return;
  try {
    persistList();
    var drafts = {};
    state.tabs.forEach(function (p) {
      var text = textOf(p);
      if (text !== undefined && baseline[p] !== undefined && text !== baseline[p] && text.length <= MAX_DRAFT) drafts[p] = text;
    });
    var json = JSON.stringify(drafts);
    if (json !== lastDraftsJson) { localStorage.setItem(key("editor-drafts"), json); lastDraftsJson = json; }
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
  var plus = document.getElementById("new-untitled");
  if (plus) plus.hidden = !canClose; // the owner's editor only: a viewer sees the owner's
  // whether each tab has unsaved text, worked out once (it reads the whole editor text for the shown tab)
  var dirty = view.tabs.map(function (path) { return canClose && isDirty(path); });
  // the mark of each file (changed or new in this session), for the color of its name
  var marked = view.tabs.map(function (path) { return window.FileMarks ? window.FileMarks.status(path) : ""; });
  // typing redraws this often: when nothing it shows has changed, the strip is left alone (no new elements, no layout)
  var sig = JSON.stringify([view.tabs, view.active, dirty, marked, canClose]);
  var redraw = sig !== lastSig || !lane.firstChild !== !view.tabs.length;
  lastSig = sig;
  if (redraw) lane.textContent = "";
  if (redraw) view.tabs.forEach(function (path, i) {
    var active = path === view.active;
    var tab = document.createElement("div");
    tab.className = "etab" + (active ? " active" : "") + (marked[i] ? " etab--" + marked[i] : "") + (isUntitled(path) ? " etab--untitled" : "");
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-selected", active ? "true" : "false");
    tab.setAttribute("aria-label", labels[i] + (marked[i] ? ", " + marked[i] : "") + (dirty[i] ? ", unsaved changes" : ""));
    tab.tabIndex = active ? 0 : -1;
    tab.dataset.path = path;
    tab.title = isUntitled(path) ? "Untitled: not saved in the workspace yet" : path;
    var name = document.createElement("span");
    name.className = "etab__name";
    name.textContent = labels[i];
    tab.appendChild(name);
    if (dirty[i]) {
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
  // the shown tab is brought into view when the shown file changes, not on every redraw (a redraw happens while the
  // user types, and must not pull a lane they scrolled away)
  var on = lane.querySelector(".etab.active");
  if (on && view.active !== lastRevealed && window.TabLane) {
    lastRevealed = view.active;
    window.TabLane.reveal(lane, on);
  }
  // the Files panel shows the same unsaved dots: it is told that the strip changed
  if (window.FileMarks) window.FileMarks.refresh();
  if (window.Unsaved) window.Unsaved.refresh(); // and the bar, and the dot in the title of the browser tab
}

function laneEvents() {
  lane = document.getElementById("editor-tabs");
  if (!lane || lane.dataset.ready) return;
  lane.dataset.ready = "1";
  var plus = document.getElementById("new-untitled");
  if (plus) plus.addEventListener("click", function () { showUntitled(); });
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
  if (isUntitled(path)) { showUntitled(); return; }
  var t = tree();
  if (!t || !path || !t.get_node(path)) return;
  if (t.get_selected()[0] === path) return;
  t.deselect_all(true);
  t.select_node(path);
}

// Takes a tab out without writing anything: the neighbour is shown (or the empty buffer). keepSelection: with no
// neighbour the file stays chosen in the tree (a file that opens in the viewer is still the one the person chose; letting
// go of it made the shared selection come back with it, which read it again, in a loop).
function closeNow(path, keepSelection) {
    var wasActive = state.active === path;
    ignoreStash = wasActive ? path : null;
    var r = closeTab(state, path);
    state = { tabs: r.tabs, active: r.active };
    delete stash[path];
    delete baseline[path];
    delete warned[path];
    if (wasActive) {
      if (state.active) activate(state.active);
      else clearEditor(keepSelection);
    }
    ignoreStash = null;
    persist();
    render();
    publish();
}

// Tabs that left the list because a new one came (past the maximum): one with unsaved text is asked about first
function dropTabs(dropped) {
  dropped.forEach(function (p) {
    if (isDirty(p)) {
      var text = stash[p] ? stash[p].text : "";
      if (text && window.Unsaved) {
        window.Unsaved.askDropped(p).then(function (choice) {
          if (choice === "save") writeOut(p, text, function (ok) { if (ok) say("Closed " + nameOf(p) + " (saved)", "info"); });
        });
      } else if (text) {
        writeOut(p, text, function (ok) { if (ok) say("Closed " + nameOf(p) + " (saved)", "info"); });
      }
    }
    delete stash[p];
    delete baseline[p];
    delete warned[p];
  });
}

// Only a tab with changes is written, and only when the user closes it; an unchanged tab never touches the server.
function closeByUser(path) {
  if (!master()) return;
  var text = textOf(path);
  if (isDirty(path) && text !== undefined) {
    // a tab with unsaved changes asks (28-unsaved.js): Save, Don't save, or Cancel (the tab stays)
    if (!window.Unsaved) { if (!isUntitled(path)) saveText(path, text, function (ok) { if (ok) closeNow(path); }); return; }
    window.Unsaved.askClose(path).then(function (choice) {
      if (choice === "save") {
        if (isUntitled(path)) saveUntitled(function (ok, saved) { if (ok) closeNow(saved); });
        else saveText(path, textOf(path), function (ok) { if (ok) closeNow(path); });
      } else if (choice === "discard") closeNow(path);
    });
  } else {
    closeNow(path);
  }
}

// no tab is left: the editor is an empty "untitled" buffer
function clearEditor(keepSelection) {
  var t = tree();
  if (t && !keepSelection) t.deselect_all(true);
  var e = aceEditor();
  if (e) e.setValue("", -1);
  if (window["editor"] && window["editor"].env) window["editor"].env.filename = "";
  shown = null;
  if (window.updateEditorFileChip) window.updateEditorFileChip();
}

function publish() {
  if (!sharedRef || !master()) return;
  try { sharedRef.set({ tabs: state.tabs.filter(function (p) { return !isUntitled(p); }), ts: Date.now() }); } catch (e) { /* Firebase is optional here */ }
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
    // the untitled tab comes back only with text (its draft); a file's tab only while the file is there
    state = restoreState(saved.list, function (p) { return isUntitled(p) ? typeof saved.drafts[UNTITLED] === "string" : isFile(p); });
    var recovered = [];
    var recoveredPaths = [];
    state.tabs.forEach(function (p) {
      if (typeof saved.drafts[p] === "string") {
        stash[p] = { text: saved.drafts[p], row: 0, col: 0, top: 0 };
        baseline[p] = null;
        recovered.push(nameOf(p));
        recoveredPaths.push(p);
      }
    });
    // the bar of 28-unsaved.js offers Save all, Review and Discard all for what came back
    if (recoveredPaths.length && window.Unsaved) window.Unsaved.recovered(recoveredPaths);
    else if (recovered.length) say("Recovered unsaved changes in " + recovered.join(", ") + ". They are marked with a dot; Save keeps them.", "info");
    // what the tree shows selected right now is what the editor shows
    var sel = t ? t.get_selected()[0] : null;
    if (sel && isFile(sel)) state = openTab(state, sel);
    restored = true;
    if (state.active === UNTITLED && !(sel && isFile(sel))) setTimeout(showUntitled, 0); // it was the tab that was shown
    persist(); // what was chosen before the tree was ready was not written (the guard above): write it now
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
    // the file the editor really holds, not the one named by env.filename (which can already be the next one)
    var old = shown;
    if (old && old !== next && old !== ignoreStash && state.tabs.indexOf(old) >= 0) stashCurrent(old);
    if (old && old !== next) shown = null; // the editor holds nothing of the next file until its text is put in
  },

  // A file was selected: it becomes a tab. Returns true when its text was put back from memory (the caller then does
  // not read the file from the server).
  selected: function (path, alreadyShown) {
    if (!isFile(path)) return false;
    if (master()) {
      // the oldest tab without unsaved changes leaves first; one with changes only when every tab has (then it is asked about)
      var r = openTab(state, path, MAX_TABS, isDirty);
      state = { tabs: r.tabs, active: r.active };
      dropTabs(r.dropped);
      var restored = alreadyShown ? true : restoreFromStash(path);
      if (restored && !alreadyShown) shown = path;
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
    shown = path;
    // what the editor holds, not what was sent: the editor may write the text a little differently (line ends, odd
    // bytes), and a file that was only opened must not look changed
    // text that came back from the browser copy after a reload (baseline null) must not be thrown away when the file's
    // own reading finishes after it: the server text is the baseline, and the recovered text goes back into the editor
    var recoveredHere = baseline[path] === null && stash[path] ? stash[path] : null;
    baseline[path] = currentText();
    if (recoveredHere && recoveredHere.text !== baseline[path]) restoreFromStash(path);
    delete stash[path];
    render();
  },

  // The file was not opened (binary, or too large for the shared editor): its tab goes, nothing is written.
  refused: function (path) {
    if (!master() || state.tabs.indexOf(path) < 0) return;
    if (shown === path) shown = null;
    closeNow(path, true);
  },

  saved: function (path, text) {
    baseline[path] = text;
    lastSaved[path] = Date.now();
    if (window.FileMarks) window.FileMarks.event("write", path);
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
    if (shown === from || (shown && shown.indexOf(from + "/") === 0)) shown = to + shown.slice(from.length);
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
      if (shown && (shown === path || shown.indexOf(path + "/") === 0)) shown = null;
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
  shown: function () { return shown; },
  // write one file with the text the tab holds; then(true) when it was written (28-unsaved.js: Save all)
  saveFile: function (path, then) {
    if (isUntitled(path)) { saveUntitled(then); return; }
    var text = textOf(path);
    if (text === undefined || !isDirty(path)) { if (then) then(true); return; }
    saveText(path, text, then);
  },
  // give up the unsaved text of a tab: the file is read from the server again when it is shown (now, if it is shown)
  discard: function (path) {
    if (isUntitled(path)) { closeNow(path); return; }
    delete stash[path];
    delete baseline[path];
    delete warned[path];
    if (path === shown && window.ReloadFileIntoEditor) window.ReloadFileIntoEditor(path);
    persist();
    render();
  },
  // for the colors of the Files panel (26-file-marks.js): has this file unsaved text, and which files have
  dirty: function (path) { return master() && state.tabs.indexOf(path) >= 0 && isDirty(path); },
  dirtyPaths: function () { return master() ? state.tabs.filter(function (p) { return isDirty(p); }) : []; },
  redraw: function () { render(); },
  // the untitled tab (a buffer that is saved with a name): shown, saved, and whether it is what the editor holds
  showUntitled: showUntitled,
  saveUntitled: saveUntitled,
  untitledShown: function () { return master() && shown === UNTITLED; },
  // the file whose text the editor holds ("" for an untitled buffer or nothing)
  fileShown: function () { return shown && !isUntitled(shown) ? shown : ""; },
  // the page put text into the untitled editor itself (a starter, a question): that is not a change of the visitor's
  programmatic: function (text) {
    if (!master() || shown !== UNTITLED) return;
    baseline[UNTITLED] = text;
    persist();
    render();
  },
  max: MAX_TABS
};
window.EditorTabs = api;

document.addEventListener("DOMContentLoaded", laneEvents);
