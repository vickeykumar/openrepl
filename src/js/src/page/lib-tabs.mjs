// The list of open files above the editor (22-editor-tabs.js), without the page, so that node can test it
// (src/js/test). A state is { tabs: [path, ...] oldest first, active: path | null }; every function returns a new
// state, plus `dropped`, the paths that left the list because of the call.

export const MAX_TABS = 5;
// The key of the one untitled tab: a buffer that is not a file of the workspace until it is saved with a name. No path
// looks like it (paths are absolute).
export const UNTITLED = "untitled:";

export function emptyState() {
  return { tabs: [], active: null, dropped: [] };
}

// Opening a file: an open one is only shown (no second tab, and its place does not change); a new one joins on the
// right, and past MAX_TABS the oldest leaves. `dirty` (a function path -> boolean, optional): the oldest tab WITHOUT
// unsaved changes leaves first, so that nothing has to be asked; the oldest of all only when every one has changes.
export function openTab(state, path, max = MAX_TABS, dirty = null) {
  if (!path) return { tabs: state.tabs.slice(), active: state.active, dropped: [] };
  if (state.tabs.includes(path)) return { tabs: state.tabs.slice(), active: path, dropped: [] };
  const tabs = state.tabs.concat(path);
  const dropped = [];
  while (tabs.length > max) {
    // never the file that was just opened (the last one)
    let i = dirty ? tabs.findIndex((p, k) => k < tabs.length - 1 && !dirty(p)) : -1;
    if (i < 0) i = 0;
    dropped.push(tabs.splice(i, 1)[0]);
  }
  return { tabs, active: path, dropped };
}

// Closing a tab: the one on its right is shown, else the one on its left, else nothing (the empty "untitled" buffer).
export function closeTab(state, path) {
  const i = state.tabs.indexOf(path);
  if (i < 0) return { tabs: state.tabs.slice(), active: state.active, dropped: [] };
  const tabs = state.tabs.slice();
  tabs.splice(i, 1);
  let active = state.active;
  if (state.active === path) active = tabs[i] !== undefined ? tabs[i] : tabs[i - 1] !== undefined ? tabs[i - 1] : null;
  return { tabs, active, dropped: [path] };
}

// A file or a folder was renamed or moved: the tabs under it follow.
export function renamePath(state, from, to) {
  const map = (p) => (p === from ? to : p.startsWith(from + "/") ? to + p.slice(from.length) : p);
  const seen = new Set();
  const tabs = [];
  for (const p of state.tabs.map(map)) if (!seen.has(p)) { seen.add(p); tabs.push(p); }
  return { tabs, active: state.active === null ? null : map(state.active), dropped: [] };
}

// A file or a folder was deleted: the tabs under it close.
export function removePath(state, path) {
  const gone = (p) => p === path || p.startsWith(path + "/");
  const tabs = state.tabs.filter((p) => !gone(p));
  let active = state.active;
  if (active !== null && gone(active)) {
    const i = state.tabs.indexOf(active);
    // the nearest survivor to the right, else to the left
    active = null;
    for (let k = i + 1; k < state.tabs.length && active === null; k++) if (!gone(state.tabs[k])) active = state.tabs[k];
    for (let k = i - 1; k >= 0 && active === null; k--) if (!gone(state.tabs[k])) active = state.tabs[k];
  }
  return { tabs, active, dropped: state.tabs.filter(gone) };
}

// A saved list, checked against what exists now: no repeats, no more than the maximum (the newest are kept), and
// only files that are still there. `exists` is a function path -> boolean.
export function restoreState(saved, exists, max = MAX_TABS) {
  const list = Array.isArray(saved && saved.tabs) ? saved.tabs.filter((p) => typeof p === "string" && p) : [];
  const seen = new Set();
  let tabs = [];
  for (const p of list) if (!seen.has(p) && (!exists || exists(p))) { seen.add(p); tabs.push(p); }
  if (tabs.length > max) tabs = tabs.slice(tabs.length - max);
  const active = saved && typeof saved.active === "string" && tabs.includes(saved.active) ? saved.active : tabs.length ? tabs[tabs.length - 1] : null;
  return { tabs, active, dropped: [] };
}

// The path of a file inside the workspace ("shop/index.html"), or "" when it is not inside it.
export function relPath(path, home) {
  const h = String(home || "").replace(/\/+$/, "");
  return h && String(path).startsWith(h + "/") ? String(path).slice(h.length + 1) : "";
}

// A path cut to fit: the file name and as many folders in front of it as fit ("…/product/index.html"); the whole path is the title.
export function shortRel(rel, max = 28) {
  if (rel.length <= max) return rel;
  const parts = rel.split("/");
  let out = parts[parts.length - 1];
  for (let i = parts.length - 2; i >= 0; i--) {
    if (("…/" + parts[i] + "/" + out).length > max) break;
    out = parts[i] + "/" + out;
  }
  return out === rel ? rel : "…/" + out;
}

// What a tab says. With the workspace folder (`home`): the path of the file inside it ("shop/index.html", "notes.txt" at the
// top), so that two files of one name, or a file in a folder, are not taken for another. Without it, or for a file outside it:
// the file name, and the folder too when two open files have the same name.
export function tabLabels(tabs, home) {
  const names = tabs.map((p) => (p === UNTITLED ? "untitled" : p.split("/").pop()));
  return tabs.map((p, i) => {
    if (p === UNTITLED) return names[i];
    const rel = relPath(p, home);
    if (rel) return shortRel(rel);
    if (names.filter((n) => n === names[i]).length < 2) return names[i];
    const parts = p.split("/");
    return parts.length > 1 ? parts[parts.length - 2] + "/" + names[i] : names[i];
  });
}
