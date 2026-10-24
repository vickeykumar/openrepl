// What the user adds to the chat from the file browser and the editor ("Add to
// Genie chat"): the chips, and the text that goes into the message. No DOM, so
// that node can test it (test/attach.test.ts). The page scripts say what was
// picked (js/src/page/22-genie-context.js); the files themselves are read here,
// when the message is sent, through the FileSource the page registers.
//
// No `break` and no `continue` in a loop that awaits (test/async-loops.test.mjs).

export type ContextKind = "file" | "folder" | "selection" | "terminal";

export interface ContextItem {
  kind: ContextKind;
  // where it is in the workspace (a selection: the file it is in, "" if unsaved or from the terminal)
  path: string;
  // what the chip says: "parse.py", "utils/", "app.py"
  name: string;
  startLine?: number; // a selection, from 1
  endLine?: number;
  text?: string; // a selection or terminal text: the text as it was when it was added
  language?: string; // a selection: the editor's language, for the code fence
}

export interface FolderEntry {
  path: string;
  size?: number;
}

// What the page gives the widget to read the workspace with.
export interface FileSource {
  readFile(path: string): Promise<string | null>;
  // every file below a folder, however deep
  listFolder(path: string): Promise<FolderEntry[] | null>;
}

export interface AttachLimits {
  items: number; // chips in one message
  perItem: number; // characters of one file or selection
  total: number; // characters of all of them
}

export const DEFAULT_LIMITS: AttachLimits = { items: 8, perItem: 8000, total: 24000 };
export const MAX_FILE_BYTES = 200 * 1024;
const MAX_TREE_LINES = 200;

export function itemKey(it: ContextItem): string {
  return [it.kind, it.path, it.startLine || 0, it.endLine || 0, it.kind === "selection" || it.kind === "terminal" ? (it.text || "").length : 0].join("|");
}

// The chip's second word: the lines of a selection.
export function metaOf(it: ContextItem): string {
  if (it.kind !== "selection" || !it.startLine) return "";
  return it.endLine && it.endLine !== it.startLine ? it.startLine + "–" + it.endLine : String(it.startLine);
}

export interface AddResult {
  list: ContextItem[];
  added: boolean;
  reason: "" | "already" | "full";
}

// The list with `it` added: the same thing twice is not added, and a full list
// takes nothing more.
export function addItem(list: ContextItem[], it: ContextItem, max: number): AddResult {
  const key = itemKey(it);
  if (list.some((x) => itemKey(x) === key)) return { list, added: false, reason: "already" };
  if (list.length >= max) return { list, added: false, reason: "full" };
  return { list: [...list, it], added: true, reason: "" };
}

export function removeItem(list: ContextItem[], key: string): ContextItem[] {
  return list.filter((x) => itemKey(x) !== key);
}

const SKIPPED_FOLDERS = ["node_modules", ".git", "__pycache__", ".venv", "venv", ".cache", ".idea", ".vscode"];
const BINARY_EXTENSIONS = [
  "png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "svgz", "pdf", "zip", "gz", "tgz", "bz2", "xz", "7z", "tar",
  "exe", "dll", "bin", "o", "a", "so", "dylib", "class", "jar", "pyc", "woff", "woff2", "ttf", "otf", "mp3", "mp4",
  "mov", "wav", "wasm", "db", "sqlite", "sqlite3",
];

// Why a file of a folder is left out, or "" if it is read.
export function skipReason(path: string, size?: number): string {
  const parts = path.split("/");
  const inSkipped = parts.slice(0, -1).some((p) => SKIPPED_FOLDERS.indexOf(p) >= 0);
  if (inSkipped) return "in a folder that is left out";
  const dot = parts[parts.length - 1].lastIndexOf(".");
  const ext = dot >= 0 ? parts[parts.length - 1].slice(dot + 1).toLowerCase() : "";
  if (BINARY_EXTENSIONS.indexOf(ext) >= 0) return "not text";
  if (typeof size === "number" && size > MAX_FILE_BYTES) return "over 200 KB";
  return "";
}

// A file that is not text: a null byte, or many characters that did not decode.
export function looksBinary(text: string): boolean {
  if (text.indexOf("\u0000") >= 0) return true;
  const sample = text.slice(0, 2000);
  let bad = 0;
  for (let i = 0; i < sample.length; i++) if (sample.charCodeAt(i) === 0xfffd) bad++;
  return sample.length > 0 && bad / sample.length > 0.1;
}

export interface Cut {
  text: string;
  cut: boolean;
  total: number; // characters before the cut
}

export function cutText(text: string, max: number): Cut {
  if (max <= 0) return { text: "", cut: text.length > 0, total: text.length };
  if (text.length <= max) return { text, cut: false, total: text.length };
  return { text: text.slice(0, max), cut: true, total: text.length };
}

// A code fence that the code cannot end early.
export function fenceOf(code: string, lang: string): string {
  let fence = "```";
  while (code.indexOf(fence) >= 0) fence += "`";
  return fence + lang + "\n" + code + "\n" + fence;
}

const LANGUAGES: Record<string, string> = {
  py: "python", js: "javascript", mjs: "javascript", ts: "typescript", go: "go", java: "java", rb: "ruby", rs: "rust",
  c: "c", h: "c", cpp: "cpp", cc: "cpp", hpp: "cpp", sh: "bash", json: "json", md: "markdown", html: "html",
  css: "css", sql: "sql", pl: "perl", tcl: "tcl", asm: "asm", xml: "xml", yml: "yaml", yaml: "yaml",
};

export function languageOf(path: string): string {
  const name = path.split("/").pop() || "";
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? LANGUAGES[name.slice(dot + 1).toLowerCase()] || "" : "";
}

export type NoteStatus = "ok" | "cut" | "partly" | "missing" | "skipped";

// What became of one chip when the message was put together.
export interface Note {
  key: string;
  name: string;
  status: NoteStatus;
  detail: string; // for the chip's tooltip: "first 8,000 of 31,400 characters"
}

export interface Gathered {
  block: string; // the text for the message, "" if nothing was read
  notes: Note[];
}

function chars(n: number): string {
  return n.toLocaleString("en-US");
}

// A running count of what is left to use.
interface Budget {
  left: number;
}

function section(title: string, body: string): string {
  return "--- " + title + " ---\n" + body;
}

async function gatherFile(it: ContextItem, src: FileSource, limits: AttachLimits, budget: Budget, out: string[]): Promise<Note> {
  const base = { key: itemKey(it), name: it.name };
  if (budget.left <= 0) return { ...base, status: "skipped", detail: "no room left in this message" };
  const raw = await src.readFile(it.path);
  if (raw === null) return { ...base, status: "missing", detail: "could not be read" };
  if (looksBinary(raw)) return { ...base, status: "skipped", detail: "not text" };
  const cut = cutText(raw, Math.min(limits.perItem, budget.left));
  budget.left -= cut.text.length;
  out.push(section(it.name, fenceOf(cut.text, languageOf(it.path))));
  if (!cut.cut) return { ...base, status: "ok", detail: "" };
  return { ...base, status: "cut", detail: "first " + chars(cut.text.length) + " of " + chars(cut.total) + " characters" };
}

function gatherSelection(it: ContextItem, limits: AttachLimits, budget: Budget, out: string[]): Note {
  const base = { key: itemKey(it), name: it.name };
  if (budget.left <= 0) return { ...base, status: "skipped", detail: "no room left in this message" };
  const cut = cutText(it.text || "", Math.min(limits.perItem, budget.left));
  budget.left -= cut.text.length;
  const lines = metaOf(it);
  const title = it.name + (lines ? ", " + (lines.indexOf("–") >= 0 ? "lines " : "line ") + lines.replace("–", "-") : "");
  out.push(section(title, fenceOf(cut.text, it.language || languageOf(it.path))));
  if (!cut.cut) return { ...base, status: "ok", detail: "" };
  return { ...base, status: "cut", detail: "first " + chars(cut.text.length) + " of " + chars(cut.total) + " characters" };
}

// The file of a folder, named as the chip is ("utils/helpers.py").
function shownPath(it: ContextItem, path: string): string {
  const root = it.path.replace(/\/+$/, "");
  return it.name.replace(/\/+$/, "") + (path.indexOf(root) === 0 ? path.slice(root.length) : "/" + path);
}

async function gatherFolder(it: ContextItem, src: FileSource, limits: AttachLimits, budget: Budget, out: string[]): Promise<Note> {
  const base = { key: itemKey(it), name: it.name };
  if (budget.left <= 0) return { ...base, status: "skipped", detail: "no room left in this message" };
  const entries = await src.listFolder(it.path);
  if (entries === null) return { ...base, status: "missing", detail: "could not be read" };
  const sorted = [...entries].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  const readable = sorted.filter((e) => skipReason(e.path, e.size) === "");
  const left: string[] = [];
  const parts: string[] = [];
  const tree = readable.slice(0, MAX_TREE_LINES).map((e) => shownPath(it, e.path));
  if (readable.length > MAX_TREE_LINES) tree.push("... and " + (readable.length - MAX_TREE_LINES) + " more files");
  parts.push(tree.length ? tree.join("\n") : "(no text files)");
  let used = 0;
  for (const e of readable) {
    if (budget.left > 0 && used < limits.total) {
      const raw = await src.readFile(e.path);
      if (raw !== null && !looksBinary(raw)) {
        const cut = cutText(raw, Math.min(limits.perItem, budget.left));
        budget.left -= cut.text.length;
        used += cut.text.length;
        parts.push(section(shownPath(it, e.path), fenceOf(cut.text, languageOf(e.path))));
        if (cut.cut) left.push(shownPath(it, e.path));
      }
    } else {
      left.push(shownPath(it, e.path));
    }
  }
  const count = readable.length + " file" + (readable.length === 1 ? "" : "s");
  out.push(section("folder " + it.name + " (" + count + ")", parts.join("\n")));
  if (left.length === 0) return { ...base, status: "ok", detail: "" };
  return { ...base, status: "partly", detail: left.length + " of " + count + " cut or left out" };
}

// The text for the message from the chips: each one read now, in order, until
// the total is used. A chip that cannot be read is told about, never dropped
// without a word.
export async function gather(items: ContextItem[], src: FileSource | null, limits: AttachLimits): Promise<Gathered> {
  const out: string[] = [];
  const notes: Note[] = [];
  const budget: Budget = { left: limits.total };
  for (const it of items) {
    let note: Note;
    if (it.kind === "selection" || it.kind === "terminal") note = gatherSelection(it, limits, budget, out);
    else if (!src) note = { key: itemKey(it), name: it.name, status: "missing", detail: "the file browser is not available" };
    else if (it.kind === "folder") note = await gatherFolder(it, src, limits, budget, out);
    else note = await gatherFile(it, src, limits, budget, out);
    notes.push(note);
  }
  return { block: out.length ? "Attached by the user:\n" + out.join("\n") : "", notes };
}

// The message the model gets: the question, then what was attached.
export function withAttached(question: string, block: string): string {
  return block ? question + "\n\n" + block : question;
}

// A line for the others of a peer chat, who get the names and not the text.
export function namesLine(items: ContextItem[]): string {
  if (items.length === 0) return "";
  return (
    "Attached: " +
    items
      .map((it) => {
        const m = metaOf(it);
        return it.name + (m ? " " + m : "");
      })
      .join(", ")
  );
}

// What the chips of the sent message say under the bubble when something did not
// go in whole; "" when everything did.
export function problemsLine(notes: Note[]): string {
  const bad = notes.filter((n) => n.status !== "ok");
  if (bad.length === 0) return "";
  return bad.map((n) => n.name + ": " + n.detail).join(" · ");
}

// ---- the # picker: type # and a few letters of a file name in the message box -------------

export interface PickerEntry {
  path: string; // as the Files panel names it
  name: string; // relative to the home folder: "utils/parse.py", "utils/" for a folder
  kind: "file" | "folder";
}

// The word being typed that starts with #, just before the caret: {start, query}, or null.
// A # that is part of a word ("c#", "a#b") and a # followed by a space (a heading) do not count.
export function hashQuery(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const m = before.match(/(^|\s)#([^\s#]*)$/);
  if (!m) return null;
  return { start: caret - m[2].length - 1, query: m[2] };
}

// The entries that match what was typed, the best first: the name itself, a name that starts with it, then a
// base name that does, then a path that has it inside; files before folders on a tie.
export function rankEntries(entries: PickerEntry[], query: string, limit: number): PickerEntry[] {
  const q = query.toLowerCase();
  const scored: { e: PickerEntry; score: number }[] = [];
  for (const e of entries) {
    const name = e.name.replace(/\/$/, "").toLowerCase();
    const base = name.slice(name.lastIndexOf("/") + 1);
    let score = -1;
    if (q === "") score = 1;
    else if (name === q || base === q) score = 5;
    else if (name.indexOf(q) === 0) score = 4;
    else if (base.indexOf(q) === 0) score = 3;
    else if (base.indexOf(q) > 0) score = 2;
    else if (name.indexOf(q) > 0) score = 1;
    if (score >= 0) scored.push({ e, score });
  }
  scored.sort((a, b) => b.score - a.score || (a.e.kind === b.e.kind ? 0 : a.e.kind === "file" ? -1 : 1) || (a.e.name < b.e.name ? -1 : 1));
  return scored.slice(0, limit).map((x) => x.e);
}

// The message with the #word taken out, and where the caret goes.
export function withoutHash(text: string, start: number, caret: number): { text: string; caret: number } {
  const after = text.slice(caret);
  const before = text.slice(0, start);
  const gap = before.endsWith(" ") && after.startsWith(" ") ? after.slice(1) : after;
  return { text: before + gap, caret: start };
}
