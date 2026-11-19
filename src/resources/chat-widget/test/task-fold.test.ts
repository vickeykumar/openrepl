import { test } from "node:test";
import assert from "node:assert/strict";
import { THINKING, foldLine, foldWord, thinkingLabel, whatChanged } from "../src/task-fold.ts";

test("the line says what changed first, then steps and seconds", () => {
  assert.equal(foldLine({ outcome: "done", changed: 3, checks: 0, steps: 7, secs: 14 }), "Changed 3 files · 7 steps · 14 s");
  assert.equal(foldLine({ outcome: "done", changed: 1, checks: 2, steps: 1, secs: 0 }), "1 file changed · 1 step · 1 s");
  assert.equal(foldLine({ outcome: "done", changed: 0, checks: 2, steps: 3, secs: 6 }), "Ran 2 checks · no file changed · 3 steps · 6 s");
  assert.equal(foldLine({ outcome: "done", changed: 0, checks: 0, steps: 2, secs: 5 }), "no file changed · 2 steps · 5 s");
});

test("a stopped task gives the first words of why, and a done one does not", () => {
  assert.equal(foldLine({ outcome: "stopped", changed: 1, checks: 0, steps: 5, secs: 9, reason: "same step failed 3 times" }), "1 file changed · 5 steps · 9 s · same step failed 3 times");
  assert.equal(foldLine({ outcome: "done", changed: 1, checks: 0, steps: 5, secs: 9, reason: "ignored" }), "1 file changed · 5 steps · 9 s");
  assert.equal(foldLine({ outcome: "stopped", changed: 0, checks: 0, steps: 1, secs: 1, reason: "x".repeat(200) }).split(" · ")[3].length, 60);
});

test("the words are Done and Stopped, and a check is one or many", () => {
  assert.equal(foldWord({ outcome: "done", changed: 0, checks: 0, steps: 1, secs: 1 }), "Done");
  assert.equal(foldWord({ outcome: "stopped", changed: 0, checks: 0, steps: 1, secs: 1 }), "Stopped");
  assert.equal(whatChanged(0, 1), "Ran 1 check · no file changed");
});

test("thinking steps are found and counted in words", () => {
  assert.ok(THINKING.test("Thought about the next step"));
  assert.ok(!THINKING.test("Read your editor and terminal"));
  assert.equal(thinkingLabel(1), "1 thinking step");
  assert.equal(thinkingLabel(2), "2 thinking steps");
});
