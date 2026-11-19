import { test } from "node:test";
import assert from "node:assert/strict";
import { ESCALATE_STEPS, PROMPT_VERSION, STUCK_ERROR, STUCK_SAME, StuckDetector, eventBody, extOf, isCheckFailure, isLimit, newTaskId, reasonCode, safeReason, sameWords, stuckText, targetOf } from "../src/genie-events.ts";

test("a target is short and names the thing the action works on", () => {
  assert.equal(targetOf({ type: "files_read", path: "src/a.py" }), "src/a.py");
  assert.equal(targetOf({ type: "preview_click", ref: "e3" }), "e3");
  assert.equal(targetOf({ type: "editor_replace", old: "x".repeat(100) }).length, 40);
  assert.equal(targetOf({ type: "run" }), "");
});

test("an extension is lower case, short, and empty when there is none", () => {
  assert.equal(extOf("shop/Index.HTML"), "html");
  assert.equal(extOf("Makefile"), "");
  assert.equal(extOf("a.verylongextension"), "");
  assert.equal(extOf(""), "");
});

test("reasons with other numbers are the same words", () => {
  assert.equal(sameWords("the editor has only 35 lines"), sameWords("The editor has only 36 lines"));
  assert.notEqual(sameWords("not found"), sameWords("not allowed"));
});

test("the same step failing three times is stuck, and the counts start again after", () => {
  const d = new StuckDetector();
  const a = { type: "editor_replace", old: "foo" };
  assert.equal(d.note(a, "failed", "not found"), null);
  assert.equal(d.note(a, "failed", "not found"), null);
  const s = d.note(a, "failed", "not found");
  assert.ok(s);
  assert.equal(s!.rule, "same-step");
  assert.equal(s!.count, STUCK_SAME);
  assert.equal(s!.action, "editor_replace");
  assert.equal(d.note(a, "failed", "not found"), null, "after the user chose, it takes three more");
});

test("a step that worked, whatever it was, ends the run of failures: it takes three in a row", () => {
  const d = new StuckDetector();
  const a = { type: "preview_click", ref: "e1" };
  d.note(a, "failed", "e1 is not on the page");
  d.note(a, "failed", "e1 is not on the page");
  d.note({ type: "preview_read" }, "done", ""); // read the page again: another way
  assert.equal(d.note(a, "failed", "e1 is not on the page"), null);
  assert.equal(d.note(a, "failed", "e1 is not on the page"), null);
  d.note({ type: "preview_read" }, "done", "");
  assert.equal(d.note(a, "failed", "e1 is not on the page"), null, "failures with something that worked between are not in a row");
  assert.equal(d.note(a, "failed", "e1 is not on the page"), null);
  assert.ok(d.note(a, "failed", "e1 is not on the page"), "the third in a row is stuck");
});

test("three steps in a row with the same words are stuck, whatever the steps are", () => {
  const d = new StuckDetector();
  assert.equal(d.note({ type: "files_read", path: "a.py" }, "failed", "the server refused (403)"), null);
  assert.equal(d.note({ type: "files_open", path: "b.py" }, "failed", "the server refused (403)"), null);
  const s = d.note({ type: "files_list", path: "c" }, "failed", "the server refused (403)");
  assert.ok(s);
  assert.equal(s!.rule, "same-error");
  assert.equal(s!.count, STUCK_ERROR);
});

test("different words, or a success in between, are not stuck", () => {
  const d = new StuckDetector();
  assert.equal(d.note({ type: "files_read", path: "a" }, "failed", "one thing"), null);
  assert.equal(d.note({ type: "files_open", path: "b" }, "failed", "another thing"), null);
  assert.equal(d.note({ type: "files_read", path: "c" }, "failed", "yet another"), null);
  assert.equal(d.note({ type: "files_read", path: "d" }, "failed", "yet another"), null, "two are not three");
  const e = new StuckDetector();
  e.note({ type: "files_read", path: "a" }, "failed", "same");
  e.note({ type: "files_read", path: "b" }, "failed", "same");
  e.note({ type: "run" }, "done", "");
  assert.equal(e.note({ type: "files_open", path: "c" }, "failed", "same"), null, "a success in between starts again");
});

test("what the user declines is not a failure", () => {
  const d = new StuckDetector();
  const a = { type: "files_delete", path: "x" };
  for (let i = 0; i < 6; i++) assert.equal(d.note(a, "denied", "the user said no"), null);
  for (let i = 0; i < 6; i++) assert.equal(d.note(a, "rejected", ""), null);
});

test("the same words with no text are not taken as the same", () => {
  const d = new StuckDetector();
  assert.equal(d.note({ type: "a" }, "failed", ""), null);
  assert.equal(d.note({ type: "b" }, "failed", ""), null);
});

test("an event holds only the fields of the log", () => {
  const b = eventBody({ event: "step_failed", model: "m", step: 4, action: "run", ext: "py", reason: "r".repeat(500), failed: 2, ...({ task: "secret task", text: "code" } as any) });
  assert.deepEqual(Object.keys(b).sort(), ["action", "choice", "code", "event", "ext", "failed", "groups", "lang", "mode", "model", "prompt", "reason", "rule", "sid", "step", "tid", "trail"]);
  assert.equal(b.reason.length, 300);
  assert.equal(b.mode, "agent");
  assert.equal(b.prompt, PROMPT_VERSION);
  assert.match(b.sid, /^s[a-z0-9]{6,}$/);
  assert.equal(eventBody({ event: "stuck", model: "m" }).sid, b.sid, "one id for the page");
  assert.equal(JSON.stringify(b).includes("secret task"), false);
});

test("the stuck card says what happened and that nothing more is used", () => {
  const t = stuckText({ rule: "same-step", action: "editor_replace", target: "foo", count: 3, reason: "the editor has only 35 lines" });
  assert.match(t, /failed 3 times in a row: editor_replace foo/);
  assert.match(t, /35 lines/);
  assert.match(t, /no more steps are used/);
  assert.match(stuckText({ rule: "same-error", action: "run", target: "", count: 3, reason: "" }), /3 steps in a row/);
  assert.equal(ESCALATE_STEPS, 5);
});

test("a command that exited with an error is a check that failed, not a failed step", () => {
  assert.equal(isCheckFailure({ type: "bash" }, "failed", "exit code 1: tests failed"), true);
  assert.equal(isCheckFailure({ type: "bash" }, "failed", "timed out after 30 s"), true);
  assert.equal(isCheckFailure({ type: "bash" }, "done", "exit code 0"), false);
  assert.equal(isCheckFailure({ type: "bash" }, "failed", "the shell is not available"), false);
  assert.equal(isCheckFailure({ type: "editor_replace" }, "failed", "exit code 1"), false);
});

test("a reason for the log holds no output of the user's program and no lines of their code", () => {
  assert.equal(safeReason("bash", "the shell is not available here"), "not available");
  assert.equal(safeReason("bash", "exit code 2: Traceback (most recent call last): password = hunter2"), "exited with an error");
  assert.equal(safeReason("preview_eval", "ReferenceError: secretThing is not defined"), "failed");
  const editor = "the editor has only 35 lines (the numbers move after every change); the last lines are:\nconst apiKey = 'abc'\nreturn 1\nUse editor_replace with old";
  assert.equal(safeReason("editor_replace", editor), "the editor has only 35 lines (the numbers move after every change);");
  assert.equal(safeReason("files_read", "line one\nline two").length <= 160, true);
  assert.equal(safeReason("files_read", "line one\nline two"), "line one");
  assert.equal(safeReason("files_read", "x".repeat(400)).length, 160);
});

test("a limit the model was told of is not a failed step", () => {
  assert.equal(isLimit("failed", "a task may use the Preview page 30 times: say in \"say\" what is done"), true);
  assert.equal(isLimit("failed", "an admin switched off Genie acting in the Preview on this site"), true);
  assert.equal(isLimit("failed", "two fix attempts were already made and the check still fails"), true);
  assert.equal(isLimit("failed", "the editor is not available"), false);
  assert.equal(isLimit("done", "a task may use the Preview page 30 times"), false);
});

test("the reason on the stuck card has no full stop before its bracket", () => {
  assert.match(stuckText({ rule: "same-step", action: "files_read", target: "a.py", count: 3, reason: "the files are for signed-in users only." }), /a\.py \(the files are for signed-in users only\)\. It is paused/);
});

// ---- steps that change nothing, and most of the steps used ----

import { STUCK_IDLE } from "../src/genie-events.ts";

test("steps in a row that changed nothing and printed nothing new are stuck, and a step that did something starts the count again", () => {
  const d = new StuckDetector();
  const idle = { progress: false, step: 1, max: 30, changedAnything: true };
  for (let i = 0; i < STUCK_IDLE - 1; i++) assert.equal(d.noteStep(idle), null);
  assert.equal(d.noteStep({ progress: true, step: 7, max: 30, changedAnything: true }), null);
  for (let i = 0; i < STUCK_IDLE - 1; i++) assert.equal(d.noteStep(idle), null, "six more are needed after a step that did something");
  const s = d.noteStep(idle);
  assert.ok(s);
  assert.equal(s!.rule, "no-progress");
  assert.equal(s!.count, STUCK_IDLE);
  assert.match(stuckText(s!), /6 steps in a row changed nothing and printed nothing new/);
  assert.equal(d.noteStep(idle), null, "after the user chose, the count starts again");
});

test("when most of the steps of a long stretch are used and nothing has changed it asks once, and again after a new stretch", () => {
  const d = new StuckDetector();
  assert.equal(d.noteStep({ progress: true, step: 10, max: 20, changedAnything: false }), null, "not yet 80%");
  const b = d.noteStep({ progress: true, step: 16, max: 20, changedAnything: false });
  assert.ok(b);
  assert.equal(b!.rule, "budget");
  assert.match(stuckText(b!), /Most of the steps for this stretch are used \(step 16\) and no file has changed\. If the task only runs code or looks around, choose Keep going/);
  assert.equal(d.noteStep({ progress: true, step: 17, max: 20, changedAnything: false }), null, "asked once");
  d.newStretch();
  assert.ok(d.noteStep({ progress: true, step: 16, max: 20, changedAnything: false }), "a stretch the user carried on asks again");
});

test("a task that has changed something is not asked about its budget, and a small budget is left alone", () => {
  const d = new StuckDetector();
  assert.equal(d.noteStep({ progress: true, step: 17, max: 20, changedAnything: true }), null);
  assert.equal(new StuckDetector().noteStep({ progress: true, step: 7, max: 8, changedAnything: false }), null, "the usual 8 steps: the pause at the step limit is enough");
});

test("a failure has a short code the log can count, from the words of the page's own messages", () => {
  const code = reasonCode;
  assert.equal(code("e3 is not on the page now: the numbers (e1, e2, ...) belong to your last preview_read"), "stale_ref");
  assert.equal(code("no file is open: open one with files_open first"), "no_file_open");
  assert.equal(code("the answer could not be read"), "unreadable");
  assert.equal(code("e2 is covered by div.modal: inspect it"), "covered");
  assert.equal(code("e4 is disabled"), "disabled");
  assert.equal(code("a task may use the Preview page 30 times"), "limit");
  assert.equal(code("the old text was not found in the editor"), "edit_mismatch");
  assert.equal(code("the editor has only 35 lines"), "edit_mismatch");
  assert.equal(code("src/a.py is not in the Files panel; files_list shows what is"), "no_such_path");
  assert.equal(code("the server refused (403)"), "refused");
  assert.equal(code("not available"), "not_available");
  assert.equal(code("something nobody expected"), "other");
  assert.equal(code(""), "");
});

test("a task id is short and differs", () => {
  const a = newTaskId();
  assert.match(a, /^t[a-z0-9]{6}$/);
  assert.notEqual(a, newTaskId());
});
