// The rules of the open-file tabs (src/page/lib-tabs.mjs): up to five, the oldest leaves at six, an open file is only
// shown, closing picks a neighbour, rename and delete follow the tree, a saved list is checked against what exists.
import test from "node:test";
import assert from "node:assert/strict";
import { MAX_TABS, emptyState, openTab, closeTab, renamePath, removePath, restoreState, tabLabels } from "../src/page/lib-tabs.mjs";

const open = (paths) => paths.reduce((s, p) => openTab(s, p), emptyState());

test("a new file joins on the right and is shown", () => {
  const s = open(["/a", "/b"]);
  assert.deepEqual(s.tabs, ["/a", "/b"]);
  assert.equal(s.active, "/b");
  assert.deepEqual(s.dropped, []);
});

test("a file that is already open is only shown: no second tab, and its place does not change", () => {
  let s = open(["/a", "/b", "/c"]);
  s = openTab(s, "/a");
  assert.deepEqual(s.tabs, ["/a", "/b", "/c"]);
  assert.equal(s.active, "/a");
  assert.deepEqual(s.dropped, []);
});

test("the sixth file drops the oldest tab", () => {
  assert.equal(MAX_TABS, 5);
  let s = open(["/1", "/2", "/3", "/4", "/5"]);
  assert.equal(s.tabs.length, 5);
  s = openTab(s, "/6");
  assert.deepEqual(s.tabs, ["/2", "/3", "/4", "/5", "/6"]);
  assert.deepEqual(s.dropped, ["/1"]);
  assert.equal(s.active, "/6");
});

test("showing an old tab does not make it newer: the oldest to open is still the first to go", () => {
  let s = open(["/1", "/2", "/3", "/4", "/5"]);
  s = openTab(s, "/1"); // only shown
  s = openTab(s, "/6");
  assert.deepEqual(s.dropped, ["/1"], "it was opened first, so it leaves first");
});

test("an empty path opens nothing", () => {
  const s = openTab(emptyState(), "");
  assert.deepEqual(s.tabs, []);
});

test("closing the shown tab shows its right neighbour, else its left, else nothing", () => {
  let s = open(["/a", "/b", "/c"]);
  s = openTab(s, "/b");
  let c = closeTab(s, "/b");
  assert.deepEqual(c.tabs, ["/a", "/c"]);
  assert.equal(c.active, "/c");
  c = closeTab(openTab(c, "/c"), "/c");
  assert.equal(c.active, "/a");
  c = closeTab(c, "/a");
  assert.equal(c.active, null);
  assert.deepEqual(c.tabs, []);
});

test("closing a tab that is not shown keeps the shown one", () => {
  const s = open(["/a", "/b", "/c"]); // /c shown
  const c = closeTab(s, "/a");
  assert.equal(c.active, "/c");
  assert.deepEqual(c.tabs, ["/b", "/c"]);
  assert.deepEqual(closeTab(s, "/zzz").tabs, s.tabs);
});

test("renaming a file renames its tab; renaming a folder renames the tabs under it", () => {
  const s = open(["/h/a.c", "/h/src/b.c", "/h/srcother/c.c"]);
  let r = renamePath(s, "/h/a.c", "/h/z.c");
  assert.deepEqual(r.tabs, ["/h/z.c", "/h/src/b.c", "/h/srcother/c.c"]);
  r = renamePath(s, "/h/src", "/h/lib");
  assert.deepEqual(r.tabs, ["/h/a.c", "/h/lib/b.c", "/h/srcother/c.c"], "only paths under the folder, not names that start the same");
  assert.equal(renamePath(open(["/h/src/b.c"]), "/h/src", "/h/lib").active, "/h/lib/b.c");
});

test("deleting a file closes its tab; deleting a folder closes the tabs under it", () => {
  const s = open(["/h/a.c", "/h/src/b.c", "/h/src/c.c", "/h/d.c"]);
  const r = removePath(s, "/h/src");
  assert.deepEqual(r.tabs, ["/h/a.c", "/h/d.c"]);
  assert.deepEqual(r.dropped, ["/h/src/b.c", "/h/src/c.c"]);
  // /h/d.c was shown and is not under src: still shown
  assert.equal(r.active, "/h/d.c");
  const r2 = removePath(openTab(s, "/h/src/b.c"), "/h/src");
  assert.equal(r2.active, "/h/d.c", "the shown one went: its nearest survivor on the right");
  assert.equal(removePath(open(["/x"]), "/x").active, null);
});

test("a saved list is checked: repeats, files that are gone, and too many", () => {
  const exists = (p) => p !== "/gone";
  const r = restoreState({ tabs: ["/a", "/gone", "/a", "/b", "/c"], active: "/b" }, exists);
  assert.deepEqual(r.tabs, ["/a", "/b", "/c"]);
  assert.equal(r.active, "/b");
  const many = restoreState({ tabs: ["/1", "/2", "/3", "/4", "/5", "/6", "/7"], active: "/1" }, () => true);
  assert.deepEqual(many.tabs, ["/3", "/4", "/5", "/6", "/7"], "the newest are kept");
  assert.equal(many.active, "/7", "the saved active tab is gone, so the newest is shown");
  assert.deepEqual(restoreState(null, () => true).tabs, []);
  assert.deepEqual(restoreState({ tabs: "x" }, () => true).tabs, []);
});

test("two open files with the same name show their folder", () => {
  assert.deepEqual(tabLabels(["/h/src/main.c", "/h/lib/main.c", "/h/notes.txt"]), ["src/main.c", "lib/main.c", "notes.txt"]);
  assert.deepEqual(tabLabels(["/h/a.c"]), ["a.c"]);
});
