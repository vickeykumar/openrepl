// The colors of changed and new files, in the Files panel and on the tabs (rules: lib-filemarks.mjs).
//
// Nothing is asked of the server for this. The page already hears what happens to the files (the server tells it of
// every file created, written or removed, by a save, a program or the terminal), and keeps the Files panel up to date
// from the same messages; the marks are made from those messages and from this page's own saves. A mark lasts until the
// page is reloaded or "Clear change marks" is chosen: there is no commit to compare with.
import { applyEvent, renameMarks, folderStatus, rowBadge, rowWords, emptyMarks, count } from "./lib-filemarks.mjs";

var marks = emptyMarks();
var scheduled = 0;
var painted = {}; // id -> true: the rows that carry a mark now, so that a row that lost it can be cleaned

function tree() { return window.jQuery && $("#file-browser").jstree ? $("#file-browser").jstree(true) : null; }
function home() { return String(window.homedir || ""); }
function unsaved(path) { return !!(window.EditorTabs && window.EditorTabs.dirty && window.EditorTabs.dirty(path)); }

function schedule() {
  if (scheduled) return;
  // a hidden pane gives no animation frames: a short timer is the fallback
  scheduled = setTimeout(function () { scheduled = 0; paint(); }, 30);
}

function clearRow(t, id) {
  var li = t.get_node(id, true);
  var a = li && li.length ? li.children("a.jstree-anchor") : null;
  if (!a || !a.length) return;
  a.removeClass("fm-new fm-modified fm-unsaved").removeAttr("data-fm");
  var node = t.get_node(id);
  if (node) a.attr("aria-label", String(node.text || ""));
}

function paint() {
  var t = tree();
  if (!t || !home() || !t.get_node(home())) return;
  var folders = folderStatus(marks, home());
  var dirty = window.EditorTabs && window.EditorTabs.dirtyPaths ? window.EditorTabs.dirtyPaths() : [];
  var rows = {};
  Object.keys(marks).forEach(function (p) { rows[p] = { status: marks[p], file: true, unsaved: false }; });
  Object.keys(folders).forEach(function (p) { rows[p] = { status: folders[p], file: false, unsaved: false }; });
  dirty.forEach(function (p) {
    rows[p] = rows[p] || { status: "", file: true, unsaved: false };
    rows[p].unsaved = true;
  });
  Object.keys(painted).forEach(function (id) { if (!rows[id]) clearRow(t, id); });
  painted = {};
  Object.keys(rows).forEach(function (id) {
    var node = t.get_node(id);
    var li = t.get_node(id, true); // false or empty while the row is not drawn (its folder is closed)
    painted[id] = true;
    if (!node || !li || !li.length) return;
    var r = rows[id];
    var a = li.children("a.jstree-anchor");
    a.toggleClass("fm-new", r.status === "new").toggleClass("fm-modified", r.status === "modified").toggleClass("fm-unsaved", r.unsaved);
    var badge = rowBadge(r.status, r.file, r.unsaved);
    if (badge) a.attr("data-fm", badge); else a.removeAttr("data-fm");
    var words = rowWords(r.status, r.unsaved);
    a.attr("aria-label", String(node.text || "") + (words ? ", " + words : ""));
  });
}

function event(op, path) {
  if (!path) return;
  var before = JSON.stringify(marks);
  marks = applyEvent(marks, op, String(path));
  if (JSON.stringify(marks) !== before) { schedule(); if (window.EditorTabs) window.EditorTabs.redraw(); }
}

window.FileMarks = {
  // the server said a file was created, written or removed (or this page saved it)
  event: event,
  renamed: function (from, to) { marks = renameMarks(marks, String(from), String(to)); schedule(); },
  status: function (path) { return marks[path] || ""; },
  count: function () { return count(marks); },
  clear: function () {
    marks = emptyMarks();
    schedule();
    if (window.EditorTabs) window.EditorTabs.redraw();
    if (window.notify) window.notify("Change marks cleared.", { type: "success", timeout: 2500 });
  },
  refresh: schedule,
  // the Files panel's own events: redraw the marks when rows are drawn again
  attach: function () {
    var el = $("#file-browser");
    if (!el.length || el.data("fmAttached")) return;
    el.data("fmAttached", true);
    el.on("redraw.jstree open_node.jstree after_open.jstree create_node.jstree refresh.jstree after_close.jstree", schedule);
    schedule();
  }
};
