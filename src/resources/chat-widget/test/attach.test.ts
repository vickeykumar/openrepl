import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_LIMITS,
  addItem,
  cutText,
  fenceOf,
  gather,
  hashQuery,
  itemKey,
  languageOf,
  looksBinary,
  metaOf,
  namesLine,
  problemsLine,
  rankEntries,
  removeItem,
  skipReason,
  withAttached,
  withoutHash,
} from "../src/attach.ts";
import type { ContextItem, FileSource } from "../src/attach.ts";

const file = (path: string): ContextItem => ({ kind: "file", path, name: path.split("/").pop() as string });
const folder = (path: string, name: string): ContextItem => ({ kind: "folder", path, name });
const selection = (text: string, start: number, end: number): ContextItem => ({
  kind: "selection",
  path: "/home/app.py",
  name: "app.py",
  startLine: start,
  endLine: end,
  text,
  language: "python",
});

// a workspace in memory
function source(files: Record<string, string>, sizes: Record<string, number> = {}): FileSource {
  return {
    async readFile(path) {
      return path in files ? files[path] : null;
    },
    async listFolder(path) {
      const prefix = path.replace(/\/+$/, "") + "/";
      return Object.keys(files)
        .filter((p) => p.indexOf(prefix) === 0)
        .map((p) => ({ path: p, size: sizes[p] }));
    },
  };
}

test("a chip says the lines of a selection, and nothing for a file", () => {
  assert.equal(metaOf(selection("x", 5, 7)), "5–7");
  assert.equal(metaOf(selection("x", 5, 5)), "5");
  assert.equal(metaOf(file("/home/a.py")), "");
});

test("the same thing is added once, and a full list takes no more", () => {
  let r = addItem([], file("/home/a.py"), 2);
  assert.ok(r.added);
  r = addItem(r.list, file("/home/a.py"), 2);
  assert.deepEqual([r.added, r.reason], [false, "already"]);
  r = addItem(r.list, file("/home/b.py"), 2);
  assert.ok(r.added);
  r = addItem(r.list, file("/home/c.py"), 2);
  assert.deepEqual([r.added, r.reason, r.list.length], [false, "full", 2]);
  // two different pieces of one file are two chips
  assert.notEqual(itemKey(selection("a", 1, 2)), itemKey(selection("a", 3, 4)));
  // two different texts of the same length are two chips, and the same text is one
  assert.notEqual(itemKey(selection("line 1", 1, 1)), itemKey(selection("line 2", 1, 1)));
  assert.equal(itemKey(selection("line 1", 1, 1)), itemKey(selection("line 1", 1, 1)));
  assert.equal(removeItem(r.list, itemKey(file("/home/a.py"))).length, 1);
});

test("what is not read from a folder", () => {
  assert.equal(skipReason("/h/utils/a.py"), "");
  assert.match(skipReason("/h/node_modules/x/index.js"), /left out/);
  assert.match(skipReason("/h/.git/config"), /left out/);
  assert.equal(skipReason("/h/logo.PNG"), "not text");
  assert.equal(skipReason("/h/big.txt", 300 * 1024), "over 200 KB");
  assert.equal(skipReason("/h/ok.txt", 1000), "");
  assert.equal(looksBinary("abc\u0000def"), true);
  assert.equal(looksBinary("plain text"), false);
});

test("a cut says how much was left", () => {
  assert.deepEqual(cutText("abcdef", 4), { text: "abcd", cut: true, total: 6 });
  assert.deepEqual(cutText("abc", 4), { text: "abc", cut: false, total: 3 });
  assert.equal(cutText("abc", 0).text, "");
});

test("a fence that the code cannot end", () => {
  assert.equal(fenceOf("x", "py"), "```py\nx\n```");
  assert.equal(fenceOf("a ``` b", ""), "````\na ``` b\n````");
  assert.equal(languageOf("/h/a.py"), "python");
  assert.equal(languageOf("/h/Makefile"), "");
});

test("a selection and a file go into the message, a selection as it was added", async () => {
  const src = source({ "/home/b.py": "print('b')" });
  const got = await gather([selection("return 1", 5, 7), file("/home/b.py")], src, DEFAULT_LIMITS);
  assert.match(got.block, /^Attached by the user:/);
  assert.match(got.block, /--- app\.py, lines 5-7 ---\n```python\nreturn 1\n```/);
  assert.match(got.block, /--- b\.py ---\n```python\nprint\('b'\)\n```/);
  assert.deepEqual(got.notes.map((n) => n.status), ["ok", "ok"]);
  assert.equal(problemsLine(got.notes), "");
  assert.equal(withAttached("why?", got.block).indexOf("why?\n\nAttached"), 0);
  assert.equal(withAttached("why?", ""), "why?");
});

test("a file that is too long is cut, and the chip says so", async () => {
  const src = source({ "/home/data.json": "x".repeat(10000) });
  const got = await gather([file("/home/data.json")], src, DEFAULT_LIMITS);
  assert.equal(got.notes[0].status, "cut");
  assert.match(got.notes[0].detail, /first 8,000 of 10,000/);
  assert.match(problemsLine(got.notes), /data\.json: first 8,000/);
});

test("the total of a message is kept, and what did not fit is said", async () => {
  const src = source({ "/home/a.txt": "a".repeat(700), "/home/b.txt": "b".repeat(700), "/home/c.txt": "c".repeat(700) });
  const limits = { items: 8, perItem: 1000, total: 1000 };
  const got = await gather([file("/home/a.txt"), file("/home/b.txt"), file("/home/c.txt")], src, limits);
  assert.deepEqual(got.notes.map((n) => n.status), ["ok", "cut", "skipped"]);
  assert.ok(got.block.length < 1000 + 400);
});

test("a file that cannot be read is told about", async () => {
  const got = await gather([file("/home/gone.py")], source({}), DEFAULT_LIMITS);
  assert.equal(got.block, "");
  assert.equal(got.notes[0].status, "missing");
  const none = await gather([file("/home/a.py")], null, DEFAULT_LIMITS);
  assert.equal(none.notes[0].status, "missing");
});

test("a folder is its tree and its text files, with the rest left out", async () => {
  const src = source(
    {
      "/home/utils/helpers.py": "def clean(r): return r",
      "/home/utils/parse.py": "import json",
      "/home/utils/logo.png": "\u0000PNG",
      "/home/utils/node_modules/x.js": "x",
      "/home/other/z.py": "not in the folder",
    },
    {}
  );
  const got = await gather([folder("/home/utils", "utils/")], src, DEFAULT_LIMITS);
  assert.match(got.block, /--- folder utils\/ \(2 files\) ---/);
  assert.match(got.block, /utils\/helpers\.py\nutils\/parse\.py/);
  assert.match(got.block, /--- utils\/helpers\.py ---\n```python\ndef clean/);
  assert.doesNotMatch(got.block, /logo\.png|node_modules|not in the folder/);
  assert.equal(got.notes[0].status, "ok");
});

test("a folder that does not fit says how many files were left out", async () => {
  const files: Record<string, string> = {};
  for (let i = 0; i < 6; i++) files["/home/d/f" + i + ".txt"] = String(i).repeat(500);
  const got = await gather([folder("/home/d", "d/")], source(files), { items: 8, perItem: 500, total: 1200 });
  assert.equal(got.notes[0].status, "partly");
  assert.match(got.notes[0].detail, /of 6 files/);
});

test("the others of a peer chat get names, not text", () => {
  assert.equal(namesLine([]), "");
  assert.equal(namesLine([file("/home/a.py"), selection("x", 5, 7), folder("/home/u", "u/")]), "Attached: a.py, app.py 5–7, u/");
});

test("the text of the terminal goes in like a selection, without lines", async () => {
  const term: ContextItem = { kind: "terminal", path: "", name: "Terminal", text: "Traceback...\nNameError: x" };
  const got = await gather([term], null, DEFAULT_LIMITS);
  assert.match(got.block, /--- Terminal ---\n```\nTraceback\.\.\.\nNameError: x\n```/);
  assert.equal(got.notes[0].status, "ok");
  assert.equal(metaOf(term), "");
  assert.notEqual(itemKey(term), itemKey({ ...term, text: "other text" }));
});

test("a # starts a file name only at the start of a word, right before the caret", () => {
  assert.deepEqual(hashQuery("look at #par", 12), { start: 8, query: "par" });
  assert.deepEqual(hashQuery("#", 1), { start: 0, query: "" });
  assert.deepEqual(hashQuery("why #ut/he and more", 10), { start: 4, query: "ut/he" });
  assert.equal(hashQuery("use c#", 6), null);
  assert.equal(hashQuery("# a heading", 11), null);
  assert.equal(hashQuery("#par and then", 13), null);
  assert.equal(hashQuery("no hash here", 12), null);
});

test("the picker puts the best match first and files before folders", () => {
  const entries = [
    { path: "/h/utils", name: "utils/", kind: "folder" as const },
    { path: "/h/utils/parse.py", name: "utils/parse.py", kind: "file" as const },
    { path: "/h/app.py", name: "app.py", kind: "file" as const },
    { path: "/h/notes/parser-notes.md", name: "notes/parser-notes.md", kind: "file" as const },
    { path: "/h/README.md", name: "README.md", kind: "file" as const },
  ];
  assert.deepEqual(rankEntries(entries, "pa", 5).map((e) => e.name), ["notes/parser-notes.md", "utils/parse.py"]);
  assert.deepEqual(rankEntries(entries, "app", 5).map((e) => e.name), ["app.py"]);
  assert.deepEqual(rankEntries(entries, "UTILS", 5).map((e) => e.name), ["utils/", "utils/parse.py"]);
  assert.equal(rankEntries(entries, "", 3).length, 3);
  assert.deepEqual(rankEntries(entries, "zzz", 5), []);
});

test("choosing from the picker takes the #word out of the message", () => {
  assert.deepEqual(withoutHash("look at #par now", 8, 12), { text: "look at now", caret: 8 });
  assert.deepEqual(withoutHash("#par", 0, 4), { text: "", caret: 0 });
  assert.deepEqual(withoutHash("see #par", 4, 8), { text: "see ", caret: 4 });
});
