// The list of open files above the editor (22-editor-tabs.js), without the page, so that node can test it
// (src/js/test). A state is { tabs: [path, ...] oldest first, active: path | null }; every function returns a new
// state, plus `dropped`, the paths that left the list because of the call.

export const MAX_TABS = 5;

export function emptyState() {
  return { tabs: [], active: null, dropped: [] };
}

// Opening a file: an open one is only shown (no second tab, and its place does not change); a new one joins on the
// right, and past MAX_TABS the oldest leaves.
export function openTab(state, path, max = MAX_TABS) {
  if (!path) return { tabs: state.tabs.slice(), active: state.active, dropped: [] };
  if (state.tabs.includes(path)) return { tabs: state.tabs.slice(), active: path, dropped: [] };
  const tabs = state.tabs.concat(path);
  const dropped = [];
  while (tabs.length > max) dropped.push(tabs.shift());
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

// What a tab says: the file name, and the folder too when two open files have the same name.
export function tabLabels(tabs) {
  const names = tabs.map((p) => p.split("/").pop());
  return tabs.map((p, i) => {
    if (names.filter((n) => n === names[i]).length < 2) return names[i];
    const parts = p.split("/");
    return parts.length > 1 ? parts[parts.length - 2] + "/" + names[i] : names[i];
  });
}
