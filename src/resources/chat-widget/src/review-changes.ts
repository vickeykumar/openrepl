// "Review all changes" (canvas "Genie: top five improvements", board 6): what a task changed, as a list of files with the lines that
// changed, and what each choice (Keep, Undo) does. This file is the part that needs no page: the diff, and the list. The card that
// shows them and the actions that undo are in agent.ts.

export type RowKind = "+" | "-" | " " | "…";
export interface DiffRow {
  t: RowKind;
  s: string;
}
export interface Diff {
  added: number;
  removed: number;
  rows: DiffRow[]; // changed lines with a little around each, "…" where unchanged lines are left out
  cut: boolean; // more rows than are shown
}

const CONTEXT = 2; // unchanged lines shown around a change
const MAX_ROWS = 160;
const MAX_CELLS = 1_500_000; // a very different pair of long files is shown as all removed and all added, not worked out

function linesOf(text: string): string[] {
  if (text === "") return [];
  const l = text.split("\n");
  if (l[l.length - 1] === "") l.pop(); // the newline that ends the file is not a line
  return l;
}

// The lines that changed between two texts, with their neighbours: the common start and end are cut off, and what is between is
// matched by the longest common subsequence of lines.
export function diffLines(before: string, after: string): Diff {
  const a = linesOf(before);
  const b = linesOf(after);
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  const script: DiffRow[] = []; // the middle: "-", "+" and " " rows
  if (midA.length * midB.length > MAX_CELLS) {
    midA.forEach((s) => script.push({ t: "-", s }));
    midB.forEach((s) => script.push({ t: "+", s }));
  } else {
    const n = midA.length;
    const m = midB.length;
    const w = m + 1;
    const lcs = new Uint32Array((n + 1) * w);
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * w + j] = midA[i] === midB[j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && midA[i] === midB[j]) {
        script.push({ t: " ", s: midA[i] });
        i++;
        j++;
      } else if (i < n && (j >= m || lcs[(i + 1) * w + j] >= lcs[i * w + j + 1])) {
        script.push({ t: "-", s: midA[i] }); // what went comes before what came in
        i++;
      } else {
        script.push({ t: "+", s: midB[j] });
        j++;
      }
    }
  }
  const all: DiffRow[] = [];
  for (let k = Math.max(0, start - CONTEXT); k < start; k++) all.push({ t: " ", s: a[k] });
  script.forEach((r) => all.push(r));
  for (let k = endA; k < Math.min(a.length, endA + CONTEXT); k++) all.push({ t: " ", s: a[k] });
  const added = script.filter((r) => r.t === "+").length;
  const removed = script.filter((r) => r.t === "-").length;
  // unchanged lines far from any change are left out of the middle
  const keep = new Array<boolean>(all.length).fill(false);
  all.forEach((r, k) => {
    if (r.t !== " ") for (let d = -CONTEXT; d <= CONTEXT; d++) if (k + d >= 0 && k + d < all.length) keep[k + d] = true;
  });
  const rows: DiffRow[] = [];
  all.forEach((r, k) => {
    if (keep[k]) rows.push(r);
    else if (rows.length && rows[rows.length - 1].t !== "…") rows.push({ t: "…", s: "" });
  });
  const cut = rows.length > MAX_ROWS;
  return { added, removed, rows: cut ? rows.slice(0, MAX_ROWS) : rows, cut };
}

// ---- the list ----

export type ChangeKind = "new" | "changed" | "renamed" | "moved" | "trashed" | "deleted" | "pasted";

// A thing the task did to the Files panel, as it was done: the paths are the ones the action named.
export interface FileOp {
  kind: "new" | "renamed" | "moved" | "trashed" | "deleted" | "pasted";
  path: string; // the file or folder it was done to (the old path)
  to?: string; // renamed and moved: the new path; trashed: where it is in the Trash; pasted: the folder
  folder?: boolean;
}

// A file a shell command of the agent made, changed or deleted, as the server tells it (server/agent_shell_changes.go): the text of the small
// ones before and after, which is what Undo needs. Several commands that touch the same file are one change (mergeShell).
export interface ShellChange {
  path: string;
  kind: "new" | "changed" | "deleted";
  before: string | null; // the text before the first command that touched it (null: new, or too large or not text to keep)
  after: string | null; // the text now (null: deleted, or too large or not text)
}

// What a command's change makes of the file's changes so far: the first "before" stays, the last "after" wins, a file made and deleted again
// is nothing, a file deleted and made again is changed (or nothing when it is as it was).
export function mergeShell(list: ShellChange[], c: ShellChange): ShellChange[] {
  const i = list.findIndex((x) => x.path === c.path);
  if (i < 0) return [...list, c];
  const was = list[i];
  const rest = list.filter((_, k) => k !== i);
  if (was.kind === "new") return c.kind === "deleted" ? rest : [...rest.slice(0, i), { ...was, after: c.after }, ...rest.slice(i)];
  const kind = c.kind === "deleted" ? "deleted" : "changed";
  if (c.kind !== "deleted" && was.before !== null && c.after !== null && was.before === c.after) return rest; // back as it was
  return [...rest.slice(0, i), { path: c.path, kind, before: was.before, after: c.kind === "deleted" ? null : c.after }, ...rest.slice(i)];
}

export interface Change {
  id: string; // what Keep and Undo name: the file's path, "" for the editor with no file; an op is "op:" and its number
  kind: ChangeKind;
  label: string; // the path, or "a.py → b.py"
  added: number;
  removed: number;
  undoable: boolean;
  why: string; // when it cannot be undone: why
  op: number; // the index of the FileOp, or -1 for a change of text
  shell: number; // the index of the ShellChange, or -1
}

const parentOf = (p: string) => (p.lastIndexOf("/") < 0 ? "" : p.slice(0, p.lastIndexOf("/")));
const nameOf = (p: string) => p.slice(p.lastIndexOf("/") + 1);

// The files a task changed. `before` is each file the task changed with its text before the first change, `after` with its text
// after the last accepted change (a file in `before` and not in `after` was never changed); `ops` is what it did in the Files panel.
export function buildChanges(before: Map<string, string>, after: Map<string, string>, ops: FileOp[], shell: ShellChange[] = []): Change[] {
  const out: Change[] = [];
  const created = new Set(ops.filter((o) => o.kind === "new" && !o.folder).map((o) => o.path));
  const texted = new Set<string>();
  before.forEach((was, key) => {
    if (!after.has(key)) return;
    const now = after.get(key) as string;
    if (now === was && !created.has(key)) return;
    texted.add(key);
    const d = diffLines(was, now);
    out.push({
      id: key,
      kind: created.has(key) ? "new" : "changed",
      label: key || "the editor",
      added: d.added,
      removed: d.removed,
      undoable: true,
      why: "",
      op: -1,
      shell: -1,
    });
  });
  ops.forEach((o, i) => {
    const why = (w: string) => ({ undoable: false, why: w });
    if (o.kind === "new") {
      if (texted.has(o.path)) return; // shown with its text
      out.push({ id: "op:" + i, kind: "new", label: o.path + (o.folder ? "/" : ""), added: 0, removed: 0, undoable: true, why: "", op: i, shell: -1 });
    } else if (o.kind === "renamed" || o.kind === "moved") {
      out.push({ id: "op:" + i, kind: o.kind, label: o.path + " → " + (o.to || ""), added: 0, removed: 0, undoable: true, why: "", op: i, shell: -1 });
    } else if (o.kind === "trashed") {
      out.push({ id: "op:" + i, kind: "trashed", label: o.path + " (in the Trash)", added: 0, removed: 0, undoable: true, why: "", op: i, shell: -1 });
    } else if (o.kind === "deleted") {
      out.push({ id: "op:" + i, kind: "deleted", label: o.path, added: 0, removed: 0, ...why("deleted for good: there is no copy to put back"), op: i, shell: -1 });
    } else {
      out.push({ id: "op:" + i, kind: "pasted", label: o.path + " → " + (o.to || "the home folder"), added: 0, removed: 0, ...why("a paste is not undone here: delete the copy in the Files panel"), op: i, shell: -1 });
    }
  });
  // what shell commands did: the text is known for the small files; a file made is put in the Trash, a file changed or deleted is written back
  // when its text before (and, for a changed one, now) is known
  shell.forEach((c, i) => {
    const d = c.kind === "deleted" ? diffLines(c.before || "", "") : c.kind === "new" ? diffLines("", c.after || "") : c.before !== null && c.after !== null ? diffLines(c.before, c.after) : { added: 0, removed: 0 };
    const known = c.kind === "new" || (c.kind === "deleted" ? c.before !== null : c.before !== null && c.after !== null);
    out.push({
      id: "sh:" + c.path,
      kind: c.kind,
      label: c.path + " (by a command)",
      added: d.added,
      removed: d.removed,
      undoable: known,
      why: known ? "" : c.kind === "deleted" ? "deleted by a command, and it was too large or not text to keep a copy" : "changed by a command, and it is too large or not text to keep a copy",
      op: -1,
      shell: i,
    });
  });
  return out;
}

// What an Undo of an op does, as the Files panel names it: [action, path, argument]. Pure, so that the card only carries it out.
export function undoOf(o: FileOp): { do: "trash" | "rename" | "move"; path: string; arg: string } | null {
  if (o.kind === "new") return { do: "trash", path: o.path, arg: "" };
  if (o.kind === "renamed") return o.to ? { do: "rename", path: o.to, arg: nameOf(o.path) } : null;
  if (o.kind === "moved") return o.to ? { do: "move", path: o.to, arg: parentOf(o.path) } : null;
  if (o.kind === "trashed") return o.to ? { do: "move", path: o.to, arg: parentOf(o.path) } : null;
  return null;
}

export function kindText(k: ChangeKind): string {
  return { new: "New", changed: "Changed", renamed: "Renamed", moved: "Moved", trashed: "Deleted", deleted: "Deleted", pasted: "Pasted" }[k];
}

export function reviewTitle(n: number): string {
  return n === 1 ? "Review 1 change" : "Review " + n + " changes";
}
