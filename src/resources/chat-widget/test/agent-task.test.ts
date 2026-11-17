// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { CHECK_BEFORE_DONE, PlanState, SAME_CONTEXT, WorkTrack, changeLine, compactHistory, contextToSend, countLines, shrinkAnswer, shrinkResults } from "../src/agent-task.ts";
import { extractJSON, parsePlan, parsePlanDone, parseStep } from "../src/agent-protocol.ts";

const big = "x".repeat(2000);
const answer = (n: number) => JSON.stringify({ say: "step " + n, actions: [{ type: "editor_write", text: big + n }], done: false });
const results = (n: number, out = "") => JSON.stringify({ step_results: [{ type: "bash", status: "done", detail: "ran", ...(out ? { output: out } : {}) }] });

test("older results shrink to a line and the last two steps stay whole", () => {
  const msgs = [{ role: "user", content: "[user-a] fix it" }];
  for (let n = 1; n <= 4; n++) {
    msgs.push({ role: "assistant", content: answer(n) }, { role: "user", content: results(n, "o".repeat(3000)) });
  }
  const out = compactHistory(msgs);
  assert.equal(out.length, msgs.length);
  assert.equal(out[0].content, "[user-a] fix it"); // the task is never touched
  // results 1 and 2 are old, 3 and 4 are whole
  assert.match(out[2].content, /step_results_older/);
  assert.match(out[2].content, /output of 3000 characters left out/);
  assert.equal(out[6].content, msgs[6].content);
  assert.equal(out[8].content, msgs[8].content);
  // answers 1 and 2 lose their code, 3 and 4 keep it
  assert.match(out[1].content, /characters of code, left out/);
  assert.ok(!out[1].content.includes(big));
  assert.equal(out[5].content, msgs[5].content);
  assert.equal(out[7].content, msgs[7].content);
  // the page's own history is not changed
  assert.ok(msgs[1].content.includes(big));
  assert.ok(JSON.stringify(out).length < JSON.stringify(msgs).length * 0.6);
});

test("a short history, a first message that looks like a result and text that is not JSON are left as they are", () => {
  const two = [{ role: "user", content: "task" }, { role: "assistant", content: answer(1) }, { role: "user", content: results(1) }];
  assert.deepEqual(compactHistory(two), two);
  assert.equal(shrinkResults("not json"), "not json");
  assert.equal(shrinkAnswer("not json"), "not json");
  assert.equal(shrinkResults('{"other":1}'), '{"other":1}');
  const small = JSON.stringify({ say: "hi", actions: [{ type: "editor_write", text: "short" }] });
  assert.equal(shrinkAnswer(small), small);
  const first = [{ role: "user", content: results(0) }];
  assert.deepEqual(compactHistory(first), first);
});

test("an older result keeps what each action did and why one was left out", () => {
  const r = JSON.stringify({ step_results: [{ type: "run", status: "done" }, { type: "files_delete", status: "denied", detail: "the user did not allow files actions" }], not_done: ["action 3 left out"] });
  const s = JSON.parse(shrinkResults(r)).step_results_older as string;
  assert.match(s, /run: done/);
  assert.match(s, /files_delete: denied \(the user did not allow/);
  assert.match(s, /not done: action 3 left out/);
});

test("the editor and the terminal are sent again only when they changed", () => {
  const ctx = { role: "system", content: "Openrepl IDE real-time context\nprint(1)" };
  assert.deepEqual(contextToSend(ctx, null), ctx);
  assert.deepEqual(contextToSend(ctx, "something else"), ctx);
  assert.deepEqual(contextToSend(ctx, ctx.content), { role: "system", content: SAME_CONTEXT });
});

test("a plan is a short list of lines, and what is done is within it", () => {
  assert.equal(parsePlan(undefined), null);
  assert.equal(parsePlan("a plan"), null);
  assert.equal(parsePlan([]), null);
  assert.equal(parsePlan([1, null, "  "]), null);
  assert.deepEqual(parsePlan(["  Look   at it ", "Fix it", 3]), ["Look at it", "Fix it"]);
  assert.equal(parsePlan(Array.from({ length: 20 }, (_, i) => "item " + i))!.length, 8);
  assert.equal(parsePlan(["x".repeat(300)])![0].length, 100);
  assert.deepEqual(parsePlanDone([3, 1, 1, 9, 0, "2", 2.7], 4), [1, 2, 3]);
  assert.deepEqual(parsePlanDone("1", 4), []);
});

test("a step may carry a plan and say what is done", () => {
  const p = parseStep(JSON.stringify({ say: "ok", plan: ["Read", "Edit", "Run"], plan_done: [1], actions: [{ type: "run" }], done: false }));
  assert.ok(p.ok);
  if (p.ok) {
    assert.deepEqual(p.step.plan, ["Read", "Edit", "Run"]);
    assert.deepEqual(p.step.planDone, [1]);
  }
  const none = parseStep('{"say":"ok","actions":[{"type":"run"}]}');
  assert.ok(none.ok && none.step.plan === undefined && none.step.planDone === undefined);
});

test("the plan state ticks items off and keeps what is done when the plan changes", () => {
  const p = new PlanState();
  assert.equal(p.active(), 0);
  assert.ok(p.update(["Read", "Edit", "Run"], []));
  assert.equal(p.active(), 1);
  assert.ok(p.update(null, [1]));
  assert.equal(p.active(), 2);
  assert.ok(!p.update(null, [1]), "the same news is not a change");
  assert.ok(!p.update(["Read", "Edit", "Run"], []), "the same plan is not a change");
  // a changed plan: Read is the same and was done, so it stays done
  assert.ok(p.update(["Read", "Edit the file", "Run", "Report"], []));
  assert.ok(p.isDone(1));
  assert.equal(p.active(), 2);
  assert.equal(p.doneCount(), 1);
  assert.equal(p.line(), "1 of 4 plan items done");
  p.update(null, [2, 3, 4]);
  assert.equal(p.active(), 0);
  // a number beyond the plan is ignored
  assert.ok(!p.update(null, [9]));
});

test("work is checked when something runs after the last change", () => {
  const w = new WorkTrack();
  assert.ok(!w.mustCheck(), "nothing was changed");
  w.noteAction({ type: "editor_write", text: "x" }, { type: "editor_write", status: "accepted" }, "auth.py");
  assert.ok(w.changed && w.mustCheck());
  w.noteAction({ type: "editor_write", text: "y" }, { type: "editor_write", status: "rejected" }, "auth.py");
  assert.equal(w.changes, 1, "a rejected change is not a change");
  w.noteAction({ type: "bash", text: "pytest -q" }, { type: "bash", status: "failed", detail: "exit code 1, 0 s" }, "auth.py");
  assert.equal(w.checked, "pytest -q");
  assert.ok(w.checkFailed);
  assert.ok(!w.mustCheck());
  // another change makes the check old
  w.noteAction({ type: "editor_insert", text: "z" }, { type: "editor_insert", status: "partly_accepted" }, "auth.py");
  assert.equal(w.checked, null);
  assert.ok(w.mustCheck());
  w.pushedBack = true;
  assert.ok(!w.mustCheck(), "told once only");
  w.noteAction({ type: "run" }, { type: "run", status: "done" }, "auth.py");
  assert.equal(w.checked, "Run");
  assert.ok(!w.checkFailed);
  assert.deepEqual(w.files, ["auth.py"]);
  // a command that could not run checks nothing either
  const n = new WorkTrack();
  n.noteAction({ type: "editor_write", text: "x" }, { type: "editor_write", status: "accepted" }, "");
  n.noteAction({ type: "bash", text: "pytest" }, { type: "bash", status: "failed", detail: "the shell is not available" }, "");
  assert.equal(n.checked, null);
  // a run that was denied checks nothing
  const d = new WorkTrack();
  d.noteAction({ type: "editor_write", text: "x" }, { type: "editor_write", status: "accepted" }, "");
  d.noteAction({ type: "run" }, { type: "run", status: "denied" }, "");
  assert.equal(d.checked, null);
  assert.deepEqual(d.files, ["the editor"]);
  assert.match(CHECK_BEFORE_DONE, /Run the program or the tests/);
});

test("lines added and removed are counted between two texts", () => {
  assert.deepEqual(countLines("a\nb\nc", "a\nb\nc"), { added: 0, removed: 0 });
  assert.deepEqual(countLines("", "a\nb"), { added: 2, removed: 0 });
  assert.deepEqual(countLines("a\nb", ""), { added: 0, removed: 2 });
  assert.deepEqual(countLines("a\nb\nc\nd", "a\nB\nc\nd\ne"), { added: 2, removed: 1 });
  const before = "def f():\n    return 1\n";
  const after = "def f(x):\n    if x < 0:\n        raise ValueError\n    return 1\n";
  assert.deepEqual(countLines(before, after), { added: 3, removed: 1 });
  assert.equal(changeLine("auth.py", { added: 9, removed: 2 }), "auth.py +9 −2");
  // a very long text is not compared line by line
  const lots = Array.from({ length: 4000 }, (_, i) => "l" + i).join("\n");
  assert.deepEqual(countLines(lots, lots + "\nmore\nmore2"), { added: 2, removed: 0 });
});

test("the common slips of a model are mended when the answer cannot be read as it is", () => {
  assert.deepEqual(extractJSON('{"say":"ok","actions":[],}'), { say: "ok", actions: [] });
  assert.deepEqual(extractJSON('Here it is:\n```json\n{"say":"ok","actions":[{"type":"run"},],"done":true,}\n```'), { say: "ok", actions: [{ type: "run" }], done: true });
  assert.deepEqual(extractJSON('{"say":"ok", // a comment\n"actions":[]}'), { say: "ok", actions: [] });
  // text that is fine as it is is not touched, commas in strings included
  assert.deepEqual(extractJSON('{"say":"a, ] b,}"}'), { say: "a, ] b,}" });
  assert.equal(extractJSON("no json here"), null);
  assert.equal(extractJSON('{"say": '), null);
});

import { chatTurnsText } from "../src/agent-task.ts";

test("the chat before a task is the last turns, cut, newest last", () => {
  const h = [
    { role: "system", content: "prompt" },
    { role: "system", content: "editor snapshot" },
    { role: "user", content: "[user-abc] make a shop site" },
    { role: "assistant", content: "I'll create the files." },
    { role: "user", content: "[user-abc] " + "x".repeat(2000) },
  ];
  const t = chatTurnsText(h, 2);
  assert.match(t, /^Earlier in this chat/);
  assert.match(t, /User: make a shop site\nGenie: I'll create the files\.\nUser: x+ \.\.\./);
  assert.ok(!t.includes("[user-abc]") && !t.includes("prompt"));
  assert.equal(chatTurnsText(h.slice(0, 2), 2), "");
  const many = Array.from({ length: 20 }, (_, i) => ({ role: i % 2 ? "assistant" : "user", content: "m" + i }));
  assert.equal(chatTurnsText(many, 0).split("\n").length, 1 + 6);
});

import { ASK_FIRST, factsText, isVagueTask, lastErrorLines } from "../src/agent-task.ts";
import { SUMMARY_PREFIX as CHAT_SUMMARY_PREFIX } from "../src/chat-tabs.ts";

test("the summary mark is the same in both files, and a task sent after a summary has it first", () => {
  const h = [{ role: "system", content: "p" }, { role: "system", content: CHAT_SUMMARY_PREFIX + "S." }, { role: "user", content: "[user-a] hello" }];
  assert.match(chatTurnsText(h, 1), /\nSummary of the earlier chat: S\.\nUser: hello/);
});

test("the facts of the workspace, the last error and a vague task", () => {
  assert.equal(lastErrorLines("$ node a.js\nok\nReferenceError: x is not defined\n$ "), "ReferenceError: x is not defined");
  assert.equal(lastErrorLines("all fine"), "");
  const t = factsText({ language: "JavaScript", file: "script.js", files: ["index.html", "script.js"], error: "" });
  assert.match(t, /language JavaScript; open file script\.js; files: index\.html, script\.js\./);
  assert.match(factsText({ language: "C", file: "", files: null, error: "boom error" }), /open file none \(an unnamed file\); last error in the terminal: boom error/);
  for (const v of ["try again", "Continue.", "fix it", "ok", ""]) assert.equal(isVagueTask(v), true, v);
  for (const v of ["make a html css js website for a shop", "add a delete button to the cart"]) assert.equal(isVagueTask(v), false, v);
  assert.match(ASK_FIRST, /ONE short question/);
});
