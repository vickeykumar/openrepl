// node --test test/   (from src/js)
import test from "node:test";
import assert from "node:assert/strict";
import { makeHunks, applyHunks, holds, locate, context, splitLines, MAX_CELLS } from "../src/page/lib-diff.mjs";

const all = (h) => new Set(h.hunks.map((x) => x.id));
const none = () => new Set();

test("identical texts have no changes", () => {
  assert.equal(makeHunks("a\nb\n", "a\nb\n").hunks.length, 0);
  assert.equal(makeHunks("", "").hunks.length, 0);
});

test("one changed line is one hunk", () => {
  const r = makeHunks("a\nb\nc", "a\nB\nc");
  assert.equal(r.hunks.length, 1);
  assert.deepEqual(r.hunks[0].del, ["b"]);
  assert.deepEqual(r.hunks[0].add, ["B"]);
  assert.equal(r.hunks[0].oldStart, 1);
  assert.equal(r.hunks[0].before, "a");
  assert.equal(r.hunks[0].after, "c");
});

test("an insertion and a deletion", () => {
  let r = makeHunks("a\nc", "a\nb\nc");
  assert.equal(r.hunks.length, 1);
  assert.deepEqual(r.hunks[0].del, []);
  assert.deepEqual(r.hunks[0].add, ["b"]);
  assert.equal(r.hunks[0].oldStart, 1);
  r = makeHunks("a\nb\nc", "a\nc");
  assert.deepEqual(r.hunks[0].del, ["b"]);
  assert.deepEqual(r.hunks[0].add, []);
});

test("changes apart from each other are separate hunks, in order", () => {
  const r = makeHunks("1\n2\n3\n4\n5\n6\n7", "1\nTWO\n3\n4\n5\n6\n7\n8");
  assert.equal(r.hunks.length, 2);
  assert.deepEqual(r.hunks.map((h) => h.id), [0, 1]);
  assert.ok(r.hunks[0].oldStart < r.hunks[1].oldStart);
});

test("accepting all gives the new text, none the old, one only that change", () => {
  const oldText = "a\nb\nc\nd\ne\nf";
  const newText = "a\nB\nc\nd\nE\nf\ng";
  const r = makeHunks(oldText, newText);
  assert.equal(r.hunks.length, 3);
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), newText);
  assert.equal(applyHunks(r.oldLines, r.hunks, none()).join("\n"), oldText);
  assert.equal(applyHunks(r.oldLines, r.hunks, new Set([1])).join("\n"), "a\nb\nc\nd\nE\nf");
  assert.equal(applyHunks(r.oldLines, r.hunks, new Set([0, 2])).join("\n"), "a\nB\nc\nd\ne\nf\ng");
});

test("trailing newlines are kept", () => {
  const r = makeHunks("a\nb", "a\nb\n");
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), "a\nb\n");
  assert.equal(applyHunks(r.oldLines, r.hunks, none()).join("\n"), "a\nb");
});

test("from nothing and to nothing", () => {
  let r = makeHunks("", "int main(){}\n");
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), "int main(){}\n");
  r = makeHunks("int main(){}\n", "");
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), "");
});

test("random edits: all gives new, none gives old, any subset keeps unchanged lines", () => {
  let seed = 7;
  const rnd = (n) => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed % n;
  };
  for (let round = 0; round < 300; round++) {
    const oldLines = Array.from({ length: rnd(25) }, () => "l" + rnd(6));
    const newLines = oldLines.slice();
    for (let k = rnd(6); k > 0; k--) {
      const p = rnd(newLines.length + 1);
      const op = rnd(3);
      if (op === 0) newLines.splice(p, 0, "n" + rnd(9));
      else if (op === 1 && newLines.length) newLines.splice(Math.min(p, newLines.length - 1), 1);
      else if (newLines.length) newLines[Math.min(p, newLines.length - 1)] = "m" + rnd(9);
    }
    const o = oldLines.join("\n");
    const n = newLines.join("\n");
    const r = makeHunks(o, n);
    assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), n, `round ${round}: all`);
    assert.equal(applyHunks(r.oldLines, r.hunks, none()).join("\n"), o, `round ${round}: none`);
    // each hunk alone applies cleanly to the old text
    for (const h of r.hunks) {
      const one = applyHunks(r.oldLines, r.hunks, new Set([h.id]));
      assert.equal(one.length, r.oldLines.length - h.del.length + h.add.length, `round ${round}: size`);
      assert.ok(holds(r.oldLines, h, h.oldStart), `round ${round}: holds`);
    }
  }
});

test("a change too big for the table is one hunk that still applies", () => {
  const big = Math.ceil(Math.sqrt(MAX_CELLS)) + 50;
  const a = Array.from({ length: big }, (_, i) => "x" + i).join("\n");
  const b = Array.from({ length: big }, (_, i) => (i % 2 ? "y" + i : "x" + i)).join("\n");
  const r = makeHunks(a, b);
  assert.equal(r.hunks.length, 1);
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), b);
});

test("a real-sized file is quick", () => {
  const a = Array.from({ length: 1500 }, (_, i) => "line " + i);
  const b = a.slice();
  b[10] = "changed";
  b.splice(700, 0, "new");
  b.splice(1200, 3);
  const t0 = Date.now();
  const r = makeHunks(a.join("\n"), b.join("\n"));
  assert.ok(Date.now() - t0 < 1500, "took " + (Date.now() - t0) + " ms");
  assert.equal(r.hunks.length, 3);
  assert.equal(applyHunks(r.oldLines, r.hunks, all(r)).join("\n"), b.join("\n"));
});

test("holds: the editor must still have what the hunk was made against", () => {
  const r = makeHunks("a\nb\nc", "a\nB\nc");
  const h = r.hunks[0];
  assert.ok(holds(["a", "b", "c"], h, 1));
  assert.ok(holds(["x", "a", "b", "c"], h, 2), "moved down by lines added above");
  assert.ok(!holds(["a", "edited", "c"], h, 1), "the removed line was edited");
  assert.ok(!holds(["a", "b", "other"], h, 1), "the line after changed");
  assert.ok(!holds(["a"], h, 1), "the lines are gone");
  assert.ok(!holds(["a", "b", "c"], h, -1));
  const ins = makeHunks("a\nc", "a\nb\nc").hunks[0];
  assert.ok(holds(["a", "c"], ins, 1));
  assert.ok(!holds(["z", "c"], ins, 1));
});

test("context gives the lines around a hunk", () => {
  const r = makeHunks("1\n2\n3\n4\n5\n6\n7\n8", "1\n2\n3\nFOUR\n5\n6\n7\n8");
  const c = context(r.oldLines, r.hunks[0], 2);
  assert.deepEqual(c.before, ["2", "3"]);
  assert.deepEqual(c.after, ["5", "6"]);
  assert.equal(c.firstRow, 1);
  assert.deepEqual(context(r.oldLines, makeHunks("a\nb", "A\nb").hunks[0], 2).before, []);
});

test("splitLines keeps an empty last line", () => {
  assert.deepEqual(splitLines("a\n"), ["a", ""]);
  assert.deepEqual(splitLines(""), [""]);
});

test("locate finds a change after the user typed above it, and gives up when it is gone", () => {
  const h = makeHunks("a\nb\nc", "a\nB\nc").hunks[0];
  assert.equal(locate(["a", "b", "c"], h, 1), 1);
  assert.equal(locate(["mine", "mine2", "a", "b", "c"], h, 1), 3, "two lines added above");
  assert.equal(locate(["b", "c"], h, 1), -1, "the line before is gone");
  assert.equal(locate(["a", "edited", "c"], h, 1), -1);
  const far = Array.from({ length: 500 }, () => "x").concat(["a", "b", "c"]);
  assert.equal(locate(far, h, 1), -1, "further than the radius");
  assert.equal(locate(far, h, 1, 600), 501);
  // two equal places: the nearest to where it was expected
  assert.equal(locate(["a", "b", "c", "a", "b", "c"], h, 4), 4);
  assert.equal(locate(["a", "b", "c", "a", "b", "c"], h, 0), 1);
});
