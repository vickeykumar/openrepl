import test from "node:test";
import assert from "node:assert/strict";
import { bandsOf, countsOf } from "../src/page/lib-changes.mjs";

test("a changed line is one removed and one added at the same place", () => {
  const b = bandsOf("a\nb\nc\n", "a\nB\nc\n");
  assert.deepEqual(b, [{ row: 1, added: 1, removed: ["b"] }]);
  assert.deepEqual(countsOf(b), { added: 1, removed: 1 });
});

test("lines added and lines taken out are placed in the current text, whatever came before them", () => {
  const before = "l1\nl2\nl3\nl4\nl5\n";
  const now = "l1\nnew a\nnew b\nl2\nl4\nl5\n"; // two lines in after l1, l3 gone
  const b = bandsOf(before, now);
  assert.deepEqual(b, [
    { row: 1, added: 2, removed: [] },
    { row: 4, added: 0, removed: ["l3"] },
  ]);
  assert.deepEqual(countsOf(b), { added: 2, removed: 1 });
});

test("lines taken out at the start and at the end, a new file and a text that is the same", () => {
  assert.deepEqual(bandsOf("x\ny\nz\n", "y\nz\n"), [{ row: 0, added: 0, removed: ["x"] }]);
  assert.deepEqual(bandsOf("x\ny\nz\n", "x\ny\n"), [{ row: 2, added: 0, removed: ["z"] }]);
  const fresh = bandsOf("", "a\nb\n");
  assert.equal(fresh[0].row, 0);
  assert.equal(countsOf(fresh).added, 2, "a new file is all added lines");
  assert.deepEqual(bandsOf("same\n", "same\n"), []);
});

test("what the user typed after the task is part of the bands, so the bands follow the edits", () => {
  const before = "a\nb\n";
  const task = "a\nb\nc\n";
  assert.deepEqual(bandsOf(before, task), [{ row: 2, added: 1, removed: [] }]);
  const typed = "a\nmine\nb\nc\n";
  assert.equal(countsOf(bandsOf(before, typed)).added, 2);
  assert.equal(bandsOf(before, typed)[0].row, 1);
});
