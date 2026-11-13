import test from "node:test";
import assert from "node:assert/strict";
import { recapOf, memoryLine, memoryOf } from "../src/agent-protocol.ts";

test("the next task is told what the last one was and how it ended", () => {
  const msgs = Array.from({ length: 8 }, () => ({ role: "assistant", content: "{}" })) as any;
  const t = { says: ["one", "two"], max: 8, messages: msgs };
  const r = recapOf("improve the search and signup", t, false, false);
  assert.match(r, /improve the search and signup/);
  assert.match(r, /ran out of steps \(8\)/);
  assert.match(r, /- one\n- two/);
  assert.match(r, /continue or go on/);
  assert.match(recapOf("x", t, true, false), /it finished/);
  assert.match(recapOf("x", { ...t, messages: [] }, false, true), /the user stopped it/);
  assert.match(recapOf("x", { ...t, messages: [] }, false, false), /ended early/);
});

test("earlier tasks are remembered as one line each, after the recap of the last", () => {
  const t = { says: ["did a", "did b"], max: 8, messages: [] as any };
  assert.equal(memoryOf("[last]", []), "[last]");
  const line = memoryLine("build a signup form", t, true, false);
  assert.match(line, /build a signup form.*finished.*last said: did b/);
  const m = memoryOf("[The task before this one ...]", [line, memoryLine("add search", t, false, true)]);
  assert.match(m, /Tasks before that, in this session \(oldest first\):\n- "build a signup form"/);
  assert.match(m, /- "add search" \(stopped by the user/);
  assert.ok(m.endsWith("]"));
});
