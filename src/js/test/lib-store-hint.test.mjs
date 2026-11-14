// What the terminal says is missing (src/page/lib-store-hint.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { missingFrom } from "../src/page/lib-store-hint.mjs";

test("a Python import error names the package", () => {
  assert.equal(missingFrom("Traceback (most recent call last):\n  File \"app.py\", line 1, in <module>\n    import flask\nModuleNotFoundError: No module named 'flask'\n$ "), "flask");
  assert.equal(missingFrom("ModuleNotFoundError: No module named 'flask.cli'"), "flask");
  assert.equal(missingFrom("ModuleNotFoundError: No module named 'PIL'"), "pillow");
  assert.equal(missingFrom("ImportError: No module named \"yaml\""), "pyyaml");
});

test("a command that is not found names the tool", () => {
  assert.equal(missingFrom("$ ffmpeg -i a.mp4\nbash: ffmpeg: command not found"), "ffmpeg");
  assert.equal(missingFrom("sh: 1: jq: not found"), "jq");
});

test("the newest error wins, and old lines are not read", () => {
  assert.equal(missingFrom("No module named 'numpy'\nsome output\nNo module named 'flask'\n"), "flask");
  assert.equal(missingFrom("No module named 'numpy'\n" + "ok\n".repeat(40)), "");
});

test("nothing is missing, or the name is not one a package could have", () => {
  assert.equal(missingFrom(""), "");
  assert.equal(missingFrom("hello world\n$ "), "");
  assert.equal(missingFrom("No module named '../../etc'"), "");
  assert.equal(missingFrom("No module named ''"), "");
});
