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
var sharedDirty = [];      // the tabs the owner has unsaved changes in, as the owner published them (a viewer's dots)
var lastDirtyJson = "";   // the owner: the dirty tabs last published
var sharedActive = null;   // the tab the owner shows, as the owner published it (a file's path, or the untitled key)
var stash = {};            // path -> { text, row, col, top }: what the editor held when the tab was left
var baseline = {};         // path -> the text of the file as loaded or last saved
var lastSaved = {};        // path -> when this page last saved it (its own write comes back as a server event)
var sharedRef = null;      // firebase: file-browser/<id>/tabs
var untitledAt = 0;        // when the untitled tab was last shown by the visitor (or the page), 0 once a file was chosen after it
var browserRef = null;     // firebase: file-browser/<id> (the same node as selected_node)
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

// Has the visitor typed (or pasted, deleted ...) in the editor since the page last put text in it? Only commands the visitor
// runs count: text the page sets (a starter, a question, a file) is not typed.
var typed = false;

function hasUntitled() { return state.tabs.indexOf(UNTITLED) >= 0; }

// A viewer of a shared session cannot show the untitled tab itself (it is the owner's buffer): it asks, through the shared
// selection, the way it asks for a file. 07-file-browser.js hands the request to the owner's page.
function requestUntitled() {
  try { if (browserRef) browserRef.update({ selected_node: UNTITLED }); } catch (e) { /* Firebase is optional here */ }
}

function showUntitled() {
  if (!master()) { requestUntitled(); return; }
  var e = aceEditor();
  if (!e) return;
  untitledAt = Date.now();
  if (shown === UNTITLED) { e.focus(); return; }
  var t = tree();
  var fileOpen = shown && state.tabs.indexOf(shown) >= 0;
  // the text of the file that is shown goes back into its tab
  if (fileOpen && shown !== ignoreStash) stashCurrent(shown);
  var implicit = !fileOpen && !hasUntitled() && !(t && t.get_selected().length);
  if (implicit) {
    // the editor already is an untitled buffer (nothing is open): the tab is that buffer
    stash[UNTITLED] = { text: e.getValue(), row: 0, col: 0, top: 0 };
    baseline[UNTITLED] = typed ? "" : e.getValue();
  } else if (!hasUntitled()) {
    stash[UNTITLED] = { text: "", row: 0, col: 0, top: 0 };
    baseline[UNTITLED] = "";
  }
  var r = openTab(state, UNTITLED, MAX_TABS, isDirty);
  state = { tabs: r.tabs, active: r.active };
  dropTabs(r.dropped);
  // not silent: the tree's change tracking must see the file leave, or choosing the same file again later is not a change
  if (t) t.deselect_all();
  // the shared selection says no file either: a later "value" event of selected_node (the Files panel refreshes when the
  // language changes) would otherwise choose the file again and bring its text back over the untitled one
  try { if (browserRef) browserRef.update({ selected_node: "" }); } catch (e) { /* Firebase is optional here */ }
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

// A collaborator names the file on their own page (the same checks); the owner's page makes it, writes its own copy of the
// untitled text and turns the tab into that file (the close_requests handler in attachShared, action "saveas"). `close`: the
// tab is closed when it has been saved (the collaborator closed it and chose Save).
function saveUntitledRemote(close, then) {
  function go(value, note) {
    if (!window.Unsaved || !window.Unsaved.askName) { say("An untitled file cannot be saved here.", "error"); if (then) then(false); return; }
    window.Unsaved.askName(value, note).then(function (name) {
      if (name === null) { if (then) then(false); return; }
      var bad = badName(name);
      if (bad) { go(name, bad); return; }
      takeLock(UNTITLED, "collab").then(function (got) {
        if (!got) { say("The untitled file is already being saved by the other person.", "info"); if (then) then(false); return; }
        try { browserRef.child("close_requests").push({ path: UNTITLED, action: "saveas", name: name, close: !!close, ts: Date.now() }); } catch (e) { /* Firebase is optional here */ }
        if (then) then(true);
      });
    });
  }
  go(defaultName(), "");
}

function saveUntitled(then) {
  if (!master()) { saveUntitledRemote(false, then); return; }
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
  // the owner's shown tab (also the untitled one, which has no file to select); before it is known, the viewer's selection
  var active = sharedActive && sharedList.indexOf(sharedActive) >= 0 ? sharedActive : sel || null;
  return { tabs: sharedList, active: active };
}

function render() {
  lane = lane || document.getElementById("editor-tabs");
  if (!lane) return;
  var view = viewList();
  var header = lane.parentNode;
  var canClose = master();
  // With no file shown and nothing chosen in the Files panel (the page has just loaded, or the last tab was closed) the editor
  // is an untitled buffer: it is drawn as an untitled tab after the others (it becomes a real one when it is chosen), and
  // the + stands next to it.
  var t0 = tree();
  var virtual = canClose && !shown && view.tabs.indexOf(UNTITLED) < 0 && !(t0 && t0.get_selected().length) && !(window.LargeFileViewer && window.LargeFileViewer.isOpen && window.LargeFileViewer.isOpen());
  if (virtual) view = { tabs: view.tabs.concat(UNTITLED), active: UNTITLED };
  if (header) header.classList.toggle("has-tabs", view.tabs.length > 0);
  var labels = tabLabels(view.tabs);
  // whether each tab has unsaved text, worked out once (it reads the whole editor text for the shown tab)
  var dirty = view.tabs.map(function (path) { return canClose ? isDirty(path) : sharedDirty.indexOf(path) >= 0; });
  // the owner tells the collaborators which tabs have changes, when that set changes (not on every key)
  if (canClose) {
    var dj = JSON.stringify(state.tabs.filter(function (p, i) { return view.tabs.indexOf(p) >= 0 && dirty[view.tabs.indexOf(p)]; }));
    if (dj !== lastDirtyJson) { lastDirtyJson = dj; publish(); }
  }
  // the mark of each file (changed or new in this session), for the color of its name
  var marked = view.tabs.map(function (path) { return window.FileMarks ? window.FileMarks.status(path) : ""; });
  // typing redraws this often: when nothing it shows has changed, the strip is left alone (no new elements, no layout)
  var sig = JSON.stringify([view.tabs, view.active, dirty, marked, canClose, virtual]);
  var redraw = sig !== lastSig || !lane.firstChild;
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
    if (!(virtual && isUntitled(path))) {
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
  // the + after the last tab (the owner's editor only: a viewer sees the owner's)
  if (redraw) {
    var add = document.createElement("button");
    add.type = "button";
    add.id = "new-untitled";
    add.className = "etab-add";
    add.setAttribute("aria-label", "New untitled file");
    add.title = "New untitled file";
    add.innerHTML = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>';
    lane.appendChild(add);
  }
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
  lane.addEventListener("click", function (e) {
    if (e.target.closest && e.target.closest("#new-untitled")) { showUntitled(); return; }
    var tab = e.target.closest && e.target.closest(".etab");
    if (!tab) return;
    if (e.target.closest(".etab__x")) closeByUser(tab.dataset.path);
    else activate(tab.dataset.path);
  });
  lane.addEventListener("contextmenu", function (e) {
    var tab = e.target.closest && e.target.closest(".etab");
    if (!tab) return;
    e.preventDefault();
    openTabMenu(tab.dataset.path, e.clientX, e.clientY);
  });
  lane.addEventListener("auxclick", function (e) {
    var tab = e.target.closest && e.target.closest(".etab");
    if (tab && e.button === 1) { e.preventDefault(); closeByUser(tab.dataset.path); }
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
    else if (e.key === "ContextMenu" || (e.shiftKey && e.key === "F10")) {
      e.preventDefault();
      var box = tab.getBoundingClientRect();
      openTabMenu(tab.dataset.path, box.left + 8, box.bottom);
      return;
    }
    else if (e.key === "Delete" || e.key === "Backspace") { e.preventDefault(); closeByUser(tab.dataset.path); return; }
    else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); activate(tab.dataset.path); return; }
    if (to) { e.preventDefault(); to.focus(); activate(to.dataset.path); }
  });
}

// ---- choosing, opening, closing ----------------------------------------------------------------------------------------

// A tab was chosen: select its file in the tree. The tree's own handler (07-file-browser.js, "changed.jstree") keeps
// the old text, shows this one, and tells everyone through Firebase.
function activate(path) {
  if (isUntitled(path)) { showUntitled(); return; } // (a viewer asks the owner to show it)
  untitledAt = 0; // a file was chosen
  var t = tree();
  if (!t || !path || !t.get_node(path)) return;
  // A collaborator's tree can still have this file selected while the owner shows another tab (the untitled one): choosing the
  // tab must then still reach the owner, so the selection is made again (a change of selection is what is shared).
  var same = t.get_selected()[0] === path;
  if (same && (master() || !sharedActive || sharedActive === path)) return;
  // When the file is selected already, the deselect must not be silent: the tree's change tracking has to see it leave, or the
  // select that follows is not a change and nothing is shared (the same reason as in showUntitled)
  t.deselect_all(!same);
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
// Resolves true when the tab was closed, false when it stayed (Cancel, or a save that failed).
function closeAsk(path) {
  return new Promise(function (resolve) {
    if (!master()) { resolve(false); return; }
    var text = textOf(path);
    if (isDirty(path) && text !== undefined) {
      // a tab with unsaved changes asks (28-unsaved.js): Save, Don't save, or Cancel (the tab stays)
      if (!window.Unsaved) {
        if (isUntitled(path)) { resolve(false); return; }
        saveText(path, text, function (ok) { if (ok) closeNow(path); resolve(!!ok); });
        return;
      }
      // one person is asked at a time: a collaborator who is closing this tab holds the lock
      takeLock(path, "owner").then(function (got) {
        if (!got) { say("A collaborator is closing " + nameOf(path) + ".", "info"); resolve(false); return; }
        window.Unsaved.askClose(path).then(function (choice) {
          if (choice === "save") {
            if (isUntitled(path)) saveUntitled(function (ok, saved) { releaseLock(); if (ok) closeNow(saved); resolve(!!ok); });
            else saveText(path, textOf(path), function (ok) { releaseLock(); if (ok) closeNow(path); resolve(!!ok); });
          } else if (choice === "discard") { releaseLock(); closeNow(path); resolve(true); }
          else { releaseLock(); resolve(false); }
        });
      });
    } else {
      closeNow(path);
      resolve(true);
    }
  });
}
// One person at a time is asked about a tab with changes: the lock is a Firebase node (file-browser/<id>/closing) taken in a
// transaction by whoever clicked first, the owner or a collaborator, and let go when the question is answered (it also
// ends by itself after two minutes).
function takeLock(path, by) {
  return new Promise(function (resolve) {
    if (!browserRef) { resolve(true); return; }
    try {
      browserRef.child("closing").transaction(function (cur) {
        if (cur && cur.path && Date.now() - (cur.ts || 0) < 120000 && !(cur.by === by && cur.path === path)) return; // someone else holds it
        return { path: path, by: by, ts: Date.now() };
      }, function (err, committed) { resolve(!err && !!committed); }, false);
    } catch (e) { resolve(true); }
  });
}
function releaseLock() {
  try { if (browserRef) browserRef.child("closing").remove(); } catch (e) { /* Firebase is optional here */ }
}

// A collaborator cannot close the owner's tab itself: it asks the owner's page (Firebase file-browser/<id>/close_requests, one
// child for each request). "close": a tab without changes. "save": the collaborator was asked and chose Save: the owner's page
// writes the file from its own copy of the text and closes the tab, without a question of its own.
function requestClose(path, action) {
  try { if (browserRef) browserRef.child("close_requests").push({ path: path, action: action || "close", ts: Date.now() }); } catch (e) { /* Firebase is optional here */ }
}
// The collaborator who clicks is asked first, for a tab with changes (Save or Cancel: dropping the owner's changes is the
// owner's to decide). Resolves true when a request was sent.
function closeRemote(path) {
  var changed = sharedDirty.indexOf(path) >= 0;
  if (!changed) { requestClose(path, "close"); return Promise.resolve(true); }
  if (!window.Unsaved || !window.Unsaved.askShared) { requestClose(path, "close"); return Promise.resolve(true); }
  if (isUntitled(path)) {
    // an untitled tab with changes: Save asks for a name here (the owner's page makes the file and closes the tab); Cancel keeps it
    return window.Unsaved.askShared(path).then(function (choice) {
      if (choice !== "save") return false;
      return new Promise(function (resolve) { saveUntitledRemote(true, resolve); });
    });
  }
  return takeLock(path, "collab").then(function (got) {
    if (!got) { say(nameOf(path) + " is already being closed by the other person.", "info"); return false; }
    return window.Unsaved.askShared(path).then(function (choice) {
      if (choice === "save") { requestClose(path, "save"); return true; }
      releaseLock();
      return false;
    });
  });
}
function closeByUser(path) {
  if (!master()) { closeRemote(path); return; }
  closeAsk(path);
}

// Closes the tabs one after another (a tab with changes asks); the first Cancel stops the rest.
function closeMany(paths) {
  return paths.reduce(function (chain, path) {
    return chain.then(function (go) { return go ? closeAsk(path) : false; });
  }, Promise.resolve(true));
}

// ---- the right-click menu of a tab -------------------------------------------------------------------------------------
// The Files panel's own menu (07-file-browser.js builds it for a file), with what does not apply to a tab greyed out, and
// three items of the tab at the bottom. The untitled tab has no file: the same list with only Save (it asks for a name),
// Download (the text as a file) and the three Close items on. Designed in the "Practice DSA" canvas, board 5.

function tabMenuItems(path) {
  var items;
  var t = tree();
  var mod = /Mac|iP(hone|ad)/.test(navigator.platform) ? "\u2318" : "Ctrl+";
  if (isUntitled(path)) {
    var off = function (label, icon, extra) { return $.extend({ label: label, icon: "ctx-i " + icon, _disabled: true }, extra || {}); };
    items = {
      rename: off("Rename", "ctx-i-pencil"),
      cut: off("Cut", "ctx-i-cut", { shortcut_label: mod + "X" }),
      copy: off("Copy", "ctx-i-copy", { shortcut_label: mod + "C" }),
      paste: off("Paste", "ctx-i-paste", { shortcut_label: mod + "V", separator_after: true }),
      save: { label: "Save", icon: "ctx-i ctx-i-save", shortcut_label: mod + "S", action: function () { saveUntitled(); } },
      download: { label: "Download", icon: "ctx-i ctx-i-download", action: function () { if (typeof window.DownloadEditor === "function") window.DownloadEditor(); } },
      delete: off("Delete", "ctx-i-trash", { separator_after: true }),
      copypath: off("Copy path", "ctx-i-path"),
      copyrelpath: off("Copy relative path", "ctx-i-path"),
      genie: off("Add to Genie chat", "ctx-i-genie")
    };
  } else {
    var fn = t && t.settings.contextmenu.items;
    var node = t && t.get_node(path);
    items = typeof fn === "function" && node ? fn.call(t, node, function () {}) : null;
    if (!items) return null;
    if (items.paste) items.paste._disabled = true; // a tab is not a folder
  }
  // a collaborator's Close items ask the owner's page, like the close button
  var all = master() ? state.tabs : sharedList;
  var others = all.filter(function (p) { return p !== path; });
  var closeThese = function (paths) {
    if (master()) { closeMany(paths); return; }
    paths.reduce(function (chain, p) { return chain.then(function (go) { return go ? closeRemote(p) : false; }); }, Promise.resolve(true));
  };
  items.tab_close = { label: "Close", separator_before: true, action: function () { closeByUser(path); } };
  items.tab_close_others = { label: "Close others", _disabled: others.length === 0, action: function () { closeThese(others); } };
  items.tab_close_all = { label: "Close all", action: function () { closeThese(others.concat(all.indexOf(path) >= 0 ? [path] : [])); } };
  return items;
}

function showTabMenu(items, x, y) {
  var t = tree();
  // the same look as the Files panel's menu
  $(document).one("context_show.vakata", function (e, data) {
    $(data.element).addClass("jstree-contextmenu jstree-" + (t ? t.get_theme() : "default") + "-contextmenu");
  });
  $.vakata.context.show($("#file-browser"), { x: x, y: y }, items);
}

// The tab is shown first (the items act on the file in the editor: it must have its text there before a Save), then the menu.
function openTabMenu(path, x, y) {
  if (!window.jQuery || !$.vakata || !$.vakata.context) return;
  if (isUntitled(path)) showUntitled(); else activate(path);
  var tries = 0;
  (function wait() {
    // a viewer's editor is the owner's: there is nothing of its own to wait for
    var ready = !master() || (isUntitled(path) ? shown === UNTITLED : shown === path);
    if (ready || ++tries > 60) {
      var items = tabMenuItems(path);
      if (items) showTabMenu(items, x, y);
      return;
    }
    setTimeout(wait, 50);
  })();
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
  try { sharedRef.set({ tabs: state.tabs, active: state.active, dirty: state.tabs.filter(function (p) { return isDirty(p); }), ts: Date.now() }); } catch (e) { /* Firebase is optional here */ }
}

// ---- what 07-file-browser.js and 02-layout.js tell it --------------------------------------------------------------------

var api = {
  // The tree is ready: take the saved list and drafts of this workspace back.
  treeReady: function () {
    laneEvents();
    var tr = tree();
    if (tr && window.jQuery) $("#file-browser").on("activate_node.jstree", function () { untitledAt = 0; }); // a click or Enter on a file
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
      // a command of the visitor that changes the text (not the page's own setValue)
      e.commands.on("afterExec", function (ev) { if (ev && ev.command && !ev.command.readOnly) typed = true; });
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
  attachShared: function (ref) {
    browserRef = ref;
    sharedRef = ref.child("tabs");
    if (master()) {
      // a collaborator's close buttons: the owner's page closes the tab (a tab with changes asks, on this page), one request
      // after another
      var chain = Promise.resolve();
      ref.child("close_requests").on("child_added", function (snap) {
        var v = snap.val();
        try { snap.ref.remove(); } catch (e) { /* the request is handled anyway */ }
        if (!v || typeof v.path !== "string" || Date.now() - (v.ts || 0) > 60 * 1000) return;
        chain = chain.then(function () {
          if (state.tabs.indexOf(v.path) < 0) { releaseLock(); return false; }
          if (v.action === "saveas" && isUntitled(v.path)) {
            // a collaborator named the untitled file: make it, write this page's text to it, and turn (or close) the tab
            var bad = typeof v.name === "string" ? badName(v.name) : "No name.";
            if (bad) { releaseLock(); say("The untitled file could not be saved for the collaborator: " + bad, "warning"); return false; }
            say("A collaborator is saving the untitled file as " + v.name + ".", "info");
            return new Promise(function (resolve) {
              var text = textOf(UNTITLED);
              Promise.resolve(window.FileBrowser.create(v.name, "file")).then(function (made) {
                if (!made || !made.ok) { releaseLock(); say("The file " + v.name + " could not be made.", "warning"); resolve(false); return; }
                var file = home() + "/" + v.name;
                saveText(file, text === undefined ? "" : text, function (ok) {
                  releaseLock();
                  // closing: the file is written and the untitled tab goes (it is not turned into a tab that is closed at once:
                  // choosing the new file in the tree would bring its tab back); saving: the tab becomes that file
                  if (ok) { if (v.close) closeNow(UNTITLED); else becomeFile(file, text === undefined ? "" : text); }
                  resolve(!!ok);
                });
              });
            });
          }
          if (v.action === "save" && !isUntitled(v.path)) {
            // the collaborator answered the question on their page: write the file from this page's text, then close
            say("A collaborator is saving and closing " + nameOf(v.path) + ".", "info");
            return new Promise(function (resolve) {
              saveText(v.path, textOf(v.path), function (ok) { releaseLock(); if (ok) closeNow(v.path); resolve(!!ok); });
            });
          }
          return closeAsk(v.path);
        });
      });
      // a collaborator is being asked about a tab: this page says so (it shows no dialog of its own)
      var told = 0;
      ref.child("closing").on("value", function (snap) {
        var c = snap.val();
        if (c && c.by === "collab" && c.ts !== told && Date.now() - (c.ts || 0) < 120000) { told = c.ts; say("A collaborator is closing " + nameOf(c.path) + ".", "info"); }
      });
    }
    if (!master()) {
      sharedRef.on("value", function (snap) {
        var v = snap.val();
        sharedList = v && Array.isArray(v.tabs) ? v.tabs.filter(function (p) { return typeof p === "string"; }) : [];
        sharedActive = v && typeof v.active === "string" ? v.active : null;
        sharedDirty = v && Array.isArray(v.dirty) ? v.dirty.filter(function (p) { return typeof p === "string"; }) : [];
        render();
      });
    }
  },

  // The Files panel was reloaded and chose its file again (a change of language does it, and its restored selection comes
  // back after the page showed the untitled tab): within a few seconds of the untitled tab being shown, it is shown again.
  treeRefreshed: function () {
    if (!master() || shown === UNTITLED || !untitledAt || Date.now() - untitledAt > 4000) return;
    var t = tree();
    if (t && t.get_selected().length) showUntitled();
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
  untitledShown: function () { return master() ? shown === UNTITLED : sharedActive === UNTITLED; },
  // the file whose text the editor holds ("" for an untitled buffer or nothing)
  fileShown: function () { return shown && !isUntitled(shown) ? shown : ""; },
  // Does the untitled editor hold text of the visitor's own: typed, or recovered after a reload (not a starter or a question
  // that the page put there)? The untitled tab is compared with what the page put in it; an editor with no tab at all
  // (nothing open) with the last text the page put in.
  untitledHasOwnText: function () {
    if (!master()) return false;
    if (shown === UNTITLED) return isDirty(UNTITLED);
    var t = tree();
    if (!shown && !(t && t.get_selected().length)) return typed && currentText() !== "";
    return false;
  },
  // the page put text into the untitled editor itself (a starter, a question): that is not a change of the visitor's
  programmatic: function (text) {
    typed = false;
    if (!master() || shown !== UNTITLED) return;
    baseline[UNTITLED] = text;
    persist();
    render();
  },
  // Is a file chosen in the Files panel or shown in the editor (not the untitled editor)? The practice page writes a question
  // only into an untitled editor: choosing a file may change the language (by its extension) but never brings a question.
  fileChosen: function () {
    if (!master()) return false;
    var t = tree();
    var sel = t ? t.get_selected()[0] : null;
    return !!((shown && !isUntitled(shown)) || (sel && isFile(sel)));
  },
  // The practice page is about to write a question into the editor. Only an untitled editor takes one: it resolves "file"
  // when a file is chosen or shown (nothing is written). Text typed from scratch in the untitled editor is asked about
  // first (Save / Don't save / Cancel, the question of closing a changed tab); a solution written on top of an earlier
  // question is not asked about, since the page keeps it for that question and language. Resolves true when the question
  // may be written, false when the visitor cancelled.
  prepareGenerated: function () {
    if (!master()) return Promise.resolve(true);
    if (api.fileChosen()) return Promise.resolve("file");
    var own = api.untitledHasOwnText() && (baseline[UNTITLED] === undefined || baseline[UNTITLED] === "" || baseline[UNTITLED] === null);
    if (!own || !window.Unsaved) return Promise.resolve(true);
    var text = currentText();
    return window.Unsaved.askClose(UNTITLED).then(function (choice) {
      if (choice === "cancel") return false;
      if (choice !== "save") return true;
      return new Promise(function (resolve) {
        // saved, the tab is that file's now: a new untitled one takes the question
        if (shown === UNTITLED) saveUntitled(function (ok) { if (ok) showUntitled(); resolve(!!ok); });
        else saveAsNew(text, function (ok) { resolve(!!ok); });
      });
    });
  },
  max: MAX_TABS
};
window.EditorTabs = api;

document.addEventListener("DOMContentLoaded", laneEvents);
