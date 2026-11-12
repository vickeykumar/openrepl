// Finding a file by a few letters of its name (src/page/lib-filesearch.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { searchFiles, dirOf, MAX_RESULTS } from "../src/page/lib-filesearch.mjs";

const f = (path, open = false) => ({ path, name: path.split("/").pop(), dir: path.split("/").slice(0, -1).join("/"), open });
const names = (r) => r.map((x) => x.file.name);

test("nothing typed: the open files first, in their order, then the rest by name", () => {
  const files = [f("/w/b.c"), f("/w/a.c"), f("/w/z.c", true), f("/w/m.c", true)];
  assert.deepEqual(names(searchFiles(files, "")), ["z.c", "m.c", "a.c", "b.c"]);
  assert.deepEqual(names(searchFiles(files, "   ")), ["z.c", "m.c", "a.c", "b.c"]);
});

test("the letters must come in order; the others are left out", () => {
  const files = [f("/w/server.go"), f("/w/main.c"), f("/w/test.rb")];
  assert.deepEqual(names(searchFiles(files, "srv")), ["server.go"]);
  assert.deepEqual(names(searchFiles(files, "vrs")), []);
});

test("capitals and spaces in the query do not matter", () => {
  const files = [f("/w/HelloWorld.java")];
  assert.deepEqual(names(searchFiles(files, "HELLO")), ["HelloWorld.java"]);
  assert.deepEqual(names(searchFiles(files, "hello w")), ["HelloWorld.java"]);
});

test("the matched letters are given as positions in the name", () => {
  const [hit] = searchFiles([f("/w/server.go")], "srv");
  assert.deepEqual(hit.indices, [0, 2, 3]);
  const [whole] = searchFiles([f("/w/server.go")], "erv");
  assert.deepEqual(whole.indices, [1, 2, 3], "a run in the name is taken whole");
});

test("a name that is the query, or starts with it, comes before one that only contains it", () => {
  const files = [f("/w/my_main.c"), f("/w/main.c.o"), f("/w/main.c"), f("/w/domain.c")];
  assert.deepEqual(names(searchFiles(files, "main.c")), ["main.c", "main.c.o", "my_main.c", "domain.c"]);
});

test("a match on the start of words beats the same letters scattered", () => {
  const files = [f("/w/portdemo.c"), f("/w/pd_test.c"), f("/w/pxxxxxxxxdxxx.c")];
  assert.equal(searchFiles(files, "pd")[0].file.name, "pd_test.c");
  assert.equal(names(searchFiles(files, "pd")).at(-1), "pxxxxxxxxdxxx.c");
});

test("an open file comes before a closed one with the same match", () => {
  const files = [f("/w/a/util.go"), f("/w/b/util.go", true)];
  assert.equal(searchFiles(files, "util")[0].file.path, "/w/b/util.go");
});

test("a much better match in a closed file still beats a weak one in an open file", () => {
  const files = [f("/w/server.go"), f("/w/sxxxxxxxxxrxxxxxxxxxvxx.txt", true)];
  assert.equal(searchFiles(files, "server")[0].file.name, "server.go");
});

test("letters that are in the folder and the name together still find the file, with no marks in the name", () => {
  const files = [f("/w/tests/run.py"), f("/w/other/x.py")];
  const r = searchFiles(files, "testsrun");
  assert.deepEqual(names(r), ["run.py"]);
  assert.deepEqual(r[0].indices, []);
});

test("a name match comes before a path-only match", () => {
  const files = [f("/w/server/readme.md"), f("/w/server.go")];
  assert.equal(searchFiles(files, "server")[0].file.name, "server.go");
});

test("at most 50 results by default, and a smaller number when asked", () => {
  const files = Array.from({ length: 200 }, (_, i) => f(`/w/file${i}.txt`));
  assert.equal(MAX_RESULTS, 50);
  assert.equal(searchFiles(files, "file").length, 50);
  assert.equal(searchFiles(files, "").length, 50);
  assert.equal(searchFiles(files, "file", 5).length, 5);
});

test("the folder is shown under the workspace, not the whole path", () => {
  assert.equal(dirOf("/home/u/src/server/a.go", "/home/u"), "src/server");
  assert.equal(dirOf("/home/u/a.go", "/home/u"), "");
  assert.equal(dirOf("/other/a.go", "/home/u"), "/other");
  assert.equal(dirOf("/home/user2/a.go", "/home/u"), "/home/user2", "a folder that only starts the same is not the workspace");
});
