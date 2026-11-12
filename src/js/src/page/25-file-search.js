// "Go to file": the files of the workspace for the palette (resources/js/palette.js), and opening one.
//
// Nothing is asked of the server: the Files panel already holds the whole tree (one load of the workspace), and this
// reads it. Opening a file is choosing it in the tree, exactly as a click does, so the open-files tabs, the shared
// selection and the rules for big and binary files all apply without any code here.
import { searchFiles, dirOf, MAX_RESULTS } from "./lib-filesearch.mjs";

function tree() { return window.jQuery && $("#file-browser").jstree ? $("#file-browser").jstree(true) : null; }
function home() { return String(window.homedir || ""); }

// A file is left out when it, or a folder above it, is hidden (its name starts with a dot) or protected (disabled in the
// tree): the Files panel does not offer those for opening either.
function offered(t, node) {
  if (!node || node.type !== "file") return false;
  for (var n = node; n && n.id !== "#"; n = t.get_node(n.parent)) {
    if (String(n.text || "").charAt(0) === ".") return false;
    if (n.state && n.state.disabled) return false;
  }
  return true;
}

function files() {
  var t = tree();
  if (!t || !t.get_node(home())) return [];
  var openTabs = window.EditorTabs ? window.EditorTabs.state().tabs : [];
  var list = [];
  var nodes = t.get_json("#", { flat: true });
  nodes.forEach(function (j) {
    var node = t.get_node(j.id);
    if (!offered(t, node)) return;
    list.push({ path: String(node.id), name: String(node.text), dir: dirOf(String(node.id), home()), open: openTabs.indexOf(String(node.id)) >= 0 });
  });
  // the open files keep the order of their tabs
  list.sort(function (a, b) {
    if (a.open && b.open) return openTabs.indexOf(a.path) - openTabs.indexOf(b.path);
    return 0;
  });
  return list;
}

// Chooses the file in the tree: its folders are opened so that it shows, the Files panel is shown, and the tree's own
// handler does the rest. The file already shown is left alone.
function go(path) {
  var t = tree();
  if (!t || !path || !t.get_node(path)) return false;
  if (window.FileBrowser && window.FileBrowser.reveal) window.FileBrowser.reveal();
  var parents = t.get_path(path, false, true) || [];
  parents.slice(0, -1).forEach(function (id) { t.open_node(id); });
  if (t.get_selected()[0] !== path) {
    t.deselect_all(true);
    t.select_node(path);
  }
  var el = t.get_node(path, true);
  if (el && el.length && el[0].scrollIntoView) el[0].scrollIntoView({ block: "nearest" });
  return true;
}

window.FileSearch = {
  files: files,
  // list: the result of files(), when the caller keeps it while someone types (the tree is walked once, not per letter)
  search: function (query, list) { return searchFiles(list || files(), query, MAX_RESULTS); },
  go: go
};
