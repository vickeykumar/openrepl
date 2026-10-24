// The rules the CI keeps on every pull request (scripts/ci/lib-repo-rules.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { forbiddenPaths, lineEndingProblems, docsReminder } from "../../../scripts/ci/lib-repo-rules.mjs";

test("build output, generated files and secrets are refused", () => {
  const bad = [
    "bin/gotty",
    "src/server/asset.go",
    "src/js/dist/scribbler.js",
    "src/resources/chat-widget/dist/index.umd.js",
    "src/bindata/static/index.html",
    "bindata/knowledge/genie.md",
    "src/js/node_modules/webpack/index.js",
    ".env",
    "deploy/.env.production",
    "keys/server.pem",
    "secrets/serviceAccount.json",
    "gcp-credentials.json",
    "docs/.DS_Store",
  ];
  const found = forbiddenPaths(bad).map((p) => p.file);
  assert.deepEqual(found, bad);
  forbiddenPaths(bad).forEach((p) => assert.ok(p.why.length > 5, p.file + " says why"));
});

test("ordinary files, the env template and the bundled helper tools are allowed", () => {
  const fine = [
    ".env.example",
    "bin/go-bindata",
    "bin/gdb",
    "src/server/feedback.go",
    "src/js/src/page/21-ide-theme.js",
    "src/js/package-lock.json",
    "docs/lld/01-server-and-routing.md",
    "src/resources/js/theme.js",
    "e2e/tests/theme.spec.js",
  ];
  assert.deepEqual(forbiddenPaths(fine), []);
});

const crlf = (s) => s.replace(/\n/g, "\r\n");

test("a CRLF file that turns into LF is refused", () => {
  const read = () => ({ base: crlf("one\ntwo\nthree\n"), head: "one\ntwo\nthree\n" });
  const problems = lineEndingProblems(["src/Makefile"], read);
  assert.equal(problems.length, 1);
  assert.equal(problems[0].file, "src/Makefile");
});

test("a CRLF file that stays CRLF, an LF file, a new file and a mixed edit are fine", () => {
  assert.deepEqual(lineEndingProblems(["a"], () => ({ base: crlf("x\ny\n"), head: crlf("x\ny\nz\n") })), []);
  assert.deepEqual(lineEndingProblems(["a"], () => ({ base: "x\ny\n", head: "x\ny\nz\n" })), []);
  assert.deepEqual(lineEndingProblems(["a"], () => ({ base: null, head: "x\n" })), []);
  // two of three lines still CRLF after the edit
  assert.deepEqual(lineEndingProblems(["a"], () => ({ base: crlf("x\ny\nz\n"), head: "x\r\ny\r\nz\n" })), []);
});

test("binary files are not looked at", () => {
  assert.deepEqual(lineEndingProblems(["a.bin"], () => ({ base: "\u0000\r\n\r\n", head: "\u0000\n\n" })), []);
});

test("a code change with no document is reminded; with a document, or only tests, it is not", () => {
  assert.match(docsReminder(["src/server/feedback.go"]), /no document did/);
  assert.match(docsReminder(["src/resources/css/ui-refresh.css", "e2e/tests/a.js"]), /ui-refresh\.css/);
  assert.equal(docsReminder(["src/server/feedback.go", "docs/lld/01-server-and-routing.md"]), null);
  assert.equal(docsReminder(["src/server/feedback.go", "README.md"]), null);
  assert.equal(docsReminder(["src/server/feedback.go", "src/resources/knowledge/theme.md"]), null);
  assert.equal(docsReminder(["src/server/feedback_test.go", "src/js/test/theme.test.mjs"]), null);
  assert.equal(docsReminder(["e2e/tests/theme.spec.js", ".github/workflows/ci.yml"]), null);
});
