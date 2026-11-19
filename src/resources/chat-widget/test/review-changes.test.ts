import { test } from "node:test";
import assert from "node:assert/strict";
import { buildChanges, diffLines, kindText, reviewTitle, undoOf } from "../src/review-changes.ts";

test("a changed line shows as one removed and one added, with its neighbours", () => {
  const d = diffLines("a\nb\nc\nd\ne\nf\ng\n", "a\nb\nc\nD\ne\nf\ng\n");
  assert.equal(d.added, 1);
  assert.equal(d.removed, 1);
  assert.deepEqual(d.rows.map((r) => r.t + r.s), [" b", " c", "-d", "+D", " e", " f"]);
  assert.equal(d.cut, false);
});

test("lines far from a change are left out, with a mark where", () => {
  const long = Array.from({ length: 30 }, (_, i) => "line " + i);
  const edited = long.slice();
  edited[3] = "changed 3";
  edited[25] = "changed 25";
  const d = diffLines(long.join("\n") + "\n", edited.join("\n") + "\n");
  assert.equal(d.added, 2);
  assert.equal(d.removed, 2);
  assert.ok(d.rows.some((r) => r.t === "…"), "a gap is marked");
  assert.ok(d.rows.length < 20);
  assert.ok(!d.rows.some((r) => r.s === "line 12"), "a line in the middle is not shown");
});

test("lines added at the start, in the middle and at the end, and a file made from nothing", () => {
  const d = diffLines("b\n", "a\nb\nc\n");
  assert.equal(d.added, 2);
  assert.equal(d.removed, 0);
  assert.deepEqual(d.rows.map((r) => r.t + r.s), ["+a", " b", "+c"]);
  const fresh = diffLines("", "x\ny\n");
  assert.equal(fresh.added, 2);
  assert.equal(fresh.removed, 0);
  const gone = diffLines("x\ny\n", "");
  assert.equal(gone.removed, 2);
  assert.equal(diffLines("same\n", "same\n").rows.length, 0);
});

test("a long different pair of files is shown as removed and added without working out the match, and a long diff is cut", () => {
  const a = Array.from({ length: 3000 }, (_, i) => "a" + i).join("\n");
  const b = Array.from({ length: 3000 }, (_, i) => "b" + i).join("\n");
  const d = diffLines(a, b);
  assert.equal(d.removed, 3000);
  assert.equal(d.added, 3000);
  assert.equal(d.cut, true);
  assert.ok(d.rows.length <= 160);
});

test("the list: a changed file, a new file with text, a new empty file and what was done in the Files panel", () => {
  const before = new Map([["a.py", "x = 1\n"], ["new.py", ""], ["same.py", "k\n"], ["rejected.py", "r\n"]]);
  const after = new Map([["a.py", "x = 2\ny = 3\n"], ["new.py", "print(1)\n"], ["same.py", "k\n"]]);
  const c = buildChanges(before, after, [
    { kind: "new", path: "new.py" },
    { kind: "new", path: "empty.py" },
    { kind: "new", path: "pkg", folder: true },
    { kind: "renamed", path: "old.py", to: "renamed.py" },
    { kind: "moved", path: "m.py", to: "src/m.py" },
    { kind: "trashed", path: "gone.py", to: "Trash/gone.py" },
    { kind: "deleted", path: "lost.py" },
    { kind: "pasted", path: "p.py", to: "src" },
  ]);
  assert.deepEqual(c.map((x) => x.kind + ":" + x.label), [
    "changed:a.py",
    "new:new.py",
    "new:empty.py",
    "new:pkg/",
    "renamed:old.py → renamed.py",
    "moved:m.py → src/m.py",
    "trashed:gone.py (in the Trash)",
    "deleted:lost.py",
    "pasted:p.py → src",
  ]);
  assert.deepEqual([c[0].added, c[0].removed], [2, 1]);
  assert.deepEqual([c[1].added, c[1].removed], [1, 0]);
  assert.deepEqual(c.map((x) => x.undoable), [true, true, true, true, true, true, true, false, false]);
  assert.match(c[7].why, /no copy to put back/);
  assert.match(c[8].why, /not undone here/);
  assert.equal(c.some((x) => /same\.py|rejected\.py/.test(x.label)), false, "a file with no change, or a change that was rejected, is not listed");
});

test("the editor with no file is listed as the editor, and nothing changed gives an empty list", () => {
  const c = buildChanges(new Map([["", "a\n"]]), new Map([["", "b\n"]]), []);
  assert.equal(c.length, 1);
  assert.equal(c[0].label, "the editor");
  assert.equal(c[0].id, "");
  assert.deepEqual(buildChanges(new Map(), new Map(), []), []);
});

test("what an Undo does: new goes to the Trash, a rename and a move go back, a trashed file comes out, the rest has no Undo", () => {
  assert.deepEqual(undoOf({ kind: "new", path: "a.py" }), { do: "trash", path: "a.py", arg: "" });
  assert.deepEqual(undoOf({ kind: "renamed", path: "src/a.py", to: "src/b.py" }), { do: "rename", path: "src/b.py", arg: "a.py" });
  assert.deepEqual(undoOf({ kind: "moved", path: "a.py", to: "src/a.py" }), { do: "move", path: "src/a.py", arg: "" });
  assert.deepEqual(undoOf({ kind: "trashed", path: "x/a.py", to: "Trash/a.py" }), { do: "move", path: "Trash/a.py", arg: "x" });
  assert.equal(undoOf({ kind: "deleted", path: "a" }), null);
  assert.equal(undoOf({ kind: "pasted", path: "a", to: "b" }), null);
});

test("the words of the list", () => {
  assert.equal(reviewTitle(1), "Review 1 change");
  assert.equal(reviewTitle(4), "Review 4 changes");
  assert.equal(kindText("trashed"), "Deleted");
  assert.equal(kindText("new"), "New");
});

// ---- what shell commands did ----

import { mergeShell } from "../src/review-changes.ts";
const sc = (path: string, kind: "new" | "changed" | "deleted", before: string | null, after: string | null) => ({ path, kind, before, after });

test("commands that touch the same file are one change: the first before, the last after", () => {
  let l = mergeShell([], sc("a.py", "changed", "v0", "v1"));
  l = mergeShell(l, sc("a.py", "changed", "v1", "v2"));
  assert.deepEqual(l, [sc("a.py", "changed", "v0", "v2")]);
  l = mergeShell(l, sc("b.txt", "new", null, "x"));
  l = mergeShell(l, sc("b.txt", "changed", "x", "y"));
  assert.deepEqual(l[1], sc("b.txt", "new", null, "y"), "a file made, then changed, is still new");
  l = mergeShell(l, sc("b.txt", "deleted", "y", null));
  assert.deepEqual(l.map((x) => x.path), ["a.py"], "made and deleted again is nothing");
  l = mergeShell(l, sc("a.py", "deleted", "v2", null));
  assert.deepEqual(l, [sc("a.py", "deleted", "v0", null)]);
  l = mergeShell(l, sc("a.py", "new", null, "v0"));
  assert.deepEqual(l, [], "deleted and made again as it was is nothing");
  assert.deepEqual(mergeShell([sc("a", "deleted", "1", null)], sc("a", "new", null, "2")), [sc("a", "changed", "1", "2")]);
});

test("the list has what commands did, with the lines, and says why a file cannot be put back", () => {
  const c = buildChanges(new Map(), new Map(), [], [sc("a.py", "changed", "x = 1\n", "x = 2\ny = 3\n"), sc("n.txt", "new", null, "hello\n"), sc("d.txt", "deleted", "bye\n", null), sc("big.txt", "changed", null, null), sc("big2.txt", "deleted", null, null)]);
  assert.deepEqual(c.map((x) => x.kind + ":" + x.label), ["changed:a.py (by a command)", "new:n.txt (by a command)", "deleted:d.txt (by a command)", "changed:big.txt (by a command)", "deleted:big2.txt (by a command)"]);
  assert.deepEqual([c[0].added, c[0].removed], [2, 1]);
  assert.deepEqual([c[1].added, c[2].removed], [1, 1]);
  assert.deepEqual(c.map((x) => x.undoable), [true, true, true, false, false]);
  assert.match(c[3].why, /too large or not text/);
  assert.deepEqual(c.map((x) => x.shell), [0, 1, 2, 3, 4]);
  assert.ok(c.every((x) => x.op === -1));
});
