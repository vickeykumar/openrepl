// Which files changed in this session (src/page/lib-filemarks.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { applyEvent, renameMarks, folderStatus, badgeFor, wordFor, rowBadge, rowWords, emptyMarks, count, NEW, MODIFIED } from "../src/page/lib-filemarks.mjs";

const run = (events) => events.reduce((m, [op, p]) => applyEvent(m, op, p), emptyMarks());

test("a created file is new, a written one is modified", () => {
  const m = run([["create", "/h/a.c"], ["write", "/h/b.c"]]);
  assert.deepEqual(m, { "/h/a.c": NEW, "/h/b.c": MODIFIED });
});

test("a new file stays new when it is written again, and a modified one stays modified", () => {
  const m = run([["create", "/h/a.c"], ["write", "/h/a.c"], ["write", "/h/a.c"], ["write", "/h/b.c"], ["write", "/h/b.c"]]);
  assert.deepEqual(m, { "/h/a.c": NEW, "/h/b.c": MODIFIED });
});

test("a file created again after it was written is new (it was removed and made anew)", () => {
  assert.deepEqual(run([["write", "/h/a.c"], ["create", "/h/a.c"]]), { "/h/a.c": NEW });
});

test("removing a file removes its mark; removing a folder removes the marks under it and nothing else", () => {
  const m = run([["create", "/h/src/a.c"], ["write", "/h/src/b.c"], ["write", "/h/srcother/c.c"], ["write", "/h/d.c"]]);
  assert.deepEqual(applyEvent(m, "remove", "/h/src"), { "/h/srcother/c.c": MODIFIED, "/h/d.c": MODIFIED });
  assert.deepEqual(applyEvent(m, "remove", "/h/d.c"), { "/h/src/a.c": NEW, "/h/src/b.c": MODIFIED, "/h/srcother/c.c": MODIFIED });
});

test("an event does not change the marks it was given", () => {
  const before = { "/h/a.c": NEW };
  applyEvent(before, "write", "/h/b.c");
  assert.deepEqual(before, { "/h/a.c": NEW });
  assert.deepEqual(applyEvent(before, "write", ""), before, "no path, no change");
});

test("renaming a file or a folder moves the marks, not names that only start the same", () => {
  const m = { "/h/a.c": NEW, "/h/src/b.c": MODIFIED, "/h/srcother/c.c": MODIFIED };
  assert.deepEqual(renameMarks(m, "/h/a.c", "/h/z.c"), { "/h/z.c": NEW, "/h/src/b.c": MODIFIED, "/h/srcother/c.c": MODIFIED });
  assert.deepEqual(renameMarks(m, "/h/src", "/h/lib"), { "/h/a.c": NEW, "/h/lib/b.c": MODIFIED, "/h/srcother/c.c": MODIFIED });
});

test("a folder shows modified when anything under it is modified, else new, up to the workspace", () => {
  const m = { "/h/src/deep/a.c": NEW, "/h/src/b.c": MODIFIED, "/h/docs/n.md": NEW };
  const f = folderStatus(m, "/h");
  assert.deepEqual(f, { "/h/src/deep": NEW, "/h/src": MODIFIED, "/h": MODIFIED, "/h/docs": NEW });
  assert.equal(folderStatus({ "/h/docs/n.md": NEW }, "/h")["/h"], NEW);
  assert.deepEqual(folderStatus({ "/other/a.c": NEW }, "/h"), {}, "nothing outside the workspace");
  assert.deepEqual(folderStatus({}, "/h"), {});
});

test("badges and words", () => {
  assert.equal(badgeFor(MODIFIED), "M");
  assert.equal(badgeFor(NEW), "U");
  assert.equal(badgeFor(undefined), "");
  assert.equal(wordFor(MODIFIED), "modified");
  assert.equal(wordFor(NEW), "new");
  assert.equal(rowBadge(MODIFIED, true, false), "M");
  assert.equal(rowBadge(NEW, true, true), "● U");
  assert.equal(rowBadge(undefined, true, true), "●", "unsaved text in a file with no mark");
  assert.equal(rowBadge(MODIFIED, false, false), "●", "a folder: a dot only");
  assert.equal(rowBadge(undefined, false, false), "");
  assert.equal(rowWords(MODIFIED, true), "modified, unsaved changes");
  assert.equal(rowWords(undefined, false), "");
  assert.equal(count({ a: NEW, b: MODIFIED }), 2);
});
