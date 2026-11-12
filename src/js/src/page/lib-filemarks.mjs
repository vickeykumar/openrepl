// Which files changed in this session, for the colors in the Files panel and on the tabs (26-file-marks.js), without
// the page, so that node can test it (src/js/test).
//
// There is no commit to compare with, so "changed" means: since this page was opened. A file the server says was
// created is NEW; a file it says was written is MODIFIED (a save, or a program writing it); a new file stays new when
// it is written again. A marks object is { path: "new" | "modified" }; every function returns a new one.

export const NEW = "new";
export const MODIFIED = "modified";

export const emptyMarks = () => ({});

// op: "create" | "write" | "remove"; path: a file
export function applyEvent(marks, op, path) {
  const out = { ...marks };
  if (!path) return out;
  if (op === "create") out[path] = NEW;
  else if (op === "write") { if (out[path] !== NEW) out[path] = MODIFIED; }
  else if (op === "remove") {
    for (const p of Object.keys(out)) if (p === path || p.startsWith(path + "/")) delete out[p];
  }
  return out;
}

// A file or a folder was renamed or moved: its marks follow it.
export function renameMarks(marks, from, to) {
  const out = {};
  for (const [p, status] of Object.entries(marks)) {
    if (p === from) out[to] = status;
    else if (p.startsWith(from + "/")) out[to + p.slice(from.length)] = status;
    else out[p] = status;
  }
  return out;
}

// The folders above the marked files, each with the status it shows: modified when anything under it is modified, else
// new. `home` (the workspace folder) is included; nothing above it is.
export function folderStatus(marks, home) {
  const out = {};
  for (const [path, status] of Object.entries(marks)) {
    let dir = path.slice(0, path.lastIndexOf("/"));
    while (dir && (!home || dir === home || dir.startsWith(home + "/"))) {
      if (status === MODIFIED || out[dir] === undefined) out[dir] = status === MODIFIED ? MODIFIED : out[dir] || NEW;
      if (dir === home) break;
      dir = dir.slice(0, dir.lastIndexOf("/"));
    }
  }
  return out;
}

export const badgeFor = (status) => (status === MODIFIED ? "M" : status === NEW ? "U" : "");
export const wordFor = (status) => (status === MODIFIED ? "modified" : status === NEW ? "new" : "");

// What a row shows on the right: the badge letter for a file, nothing for a folder, and a dot when it has unsaved text.
export function rowBadge(status, isFile, unsaved) {
  const parts = [];
  if (unsaved) parts.push("●");
  if (isFile && status) parts.push(badgeFor(status));
  else if (!isFile && status && !unsaved) parts.push("●");
  return parts.join(" ");
}

// The words a screen reader adds to a row's name.
export function rowWords(status, unsaved) {
  const words = [];
  if (status) words.push(wordFor(status));
  if (unsaved) words.push("unsaved changes");
  return words.join(", ");
}

export const count = (marks) => Object.keys(marks).length;
