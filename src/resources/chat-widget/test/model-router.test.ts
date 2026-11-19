import { test } from "node:test";
import assert from "node:assert/strict";
import { autoNote, autoStarts, routeModel } from "../src/model-router.ts";

const cfg = { small: "luna", strong: "gpt4o", default: "new" as const };
const chat = (message: string, attached = 0, usable?: (id: string) => boolean) => routeModel({ mode: "chat", message, attached, cfg, usable });

test("plain questions go to the small model", () => {
  for (const m of [
    "what does a closure do?",
    "why is my loop slow",
    "explain recursion",
    "what is the difference between a list and a tuple",
    "how does Python handle memory",
    "is this a good idea?",
    "what does line 4 mean",
    "thanks!",
    "when should I use a dictionary",
    "what is a REPL",
    "can you explain this error",
    "which is faster, a set or a list",
  ]) {
    const p = chat(m);
    assert.equal(p.size, "small", m);
    assert.equal(p.model, "luna");
    assert.equal(p.why, "a plain question");
  }
});

test("a message that asks for a change goes to the strong model, whatever the verb", () => {
  for (const m of [
    "fix the login button on shop/index.html",
    "make a web page for my shop",
    "add a loading spinner",
    "change the colour of the header",
    "build a todo list",
    "run it and tell me what happens",
    "refactor this function",
    "update the readme",
    "rename the variable total to sum",
    "write a function that sorts a list",
    "create a class for users",
    "convert this to TypeScript",
    "optimize the query",
    "delete the unused imports",
    "install numpy",
    "generate a test for it",
  ]) {
    const p = chat(m);
    assert.equal(p.size, "strong", m);
    assert.equal(p.model, "gpt4o");
    assert.equal(p.why, "it asks for a change");
  }
});

test("files attached, code in the message and a long message go to the strong model, with the reason", () => {
  assert.deepEqual(chat("what is wrong here", 2), { model: "gpt4o", size: "strong", why: "files are attached" });
  assert.equal(chat("why does this fail\n```js\nconst a = 1\n```").why, "it has code in it");
  assert.equal(chat("why does this fail\n    for i in x:\n        print(i)").why, "it has code in it");
  assert.equal(chat("tell me about this " + "word ".repeat(200)).why, "it is a long message");
});

test("Agent mode is always the strong model", () => {
  const p = routeModel({ mode: "agent", message: "what is a closure", attached: 0, cfg });
  assert.deepEqual(p, { model: "gpt4o", size: "strong", why: "Agent mode" });
});

test("a model this person cannot use is replaced by the other one, and the note says so", () => {
  const noStrong = (id: string) => id !== "gpt4o";
  const p = chat("fix the button", 0, noStrong);
  assert.equal(p.model, "luna");
  assert.equal(p.size, "small");
  assert.match(p.why, /the other model is not open to you/);
  assert.equal(chat("what is a closure", 0, (id) => id !== "luna").model, "gpt4o");
  // neither is open: the configured one stays, and the caller falls back to the person's own pick
  assert.equal(chat("fix it", 0, () => false).model, "gpt4o");
});

test("who starts on Auto: the person's own word first, then the admin's default", () => {
  assert.equal(autoStarts(null, null, false), false);
  assert.equal(autoStarts(cfg, null, false), true, "new: nobody picked a model yet");
  assert.equal(autoStarts(cfg, null, true), false, "new: this person picked one");
  assert.equal(autoStarts({ ...cfg, default: "all" }, null, true), true);
  assert.equal(autoStarts({ ...cfg, default: "none" }, null, false), false);
  assert.equal(autoStarts({ ...cfg, default: "none" }, "1", true), true, "chosen by the person");
  assert.equal(autoStarts({ ...cfg, default: "all" }, "0", false), false, "turned off by the person");
});

test("the note says which model and why", () => {
  assert.equal(autoNote({ model: "luna", size: "small", why: "a plain question" }, "Luna", false), "Auto chose a small model: a plain question");
  assert.equal(autoNote({ model: "gpt4o", size: "strong", why: "Agent mode" }, "GPT-4o mini", true), "Auto chose GPT-4o mini for this task: Agent mode");
});
