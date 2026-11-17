// The agent's loop, run as the build tool leaves it.
//
// The other tests read the TypeScript. This one builds src/agent.ts with
// microbundle, the tool that builds the widget (it rewrites async functions into
// promise chains, and has got loops wrong: see async-loops.test.mjs), and drives
// the built AgentRunner with a scripted model, a few pretend page elements and a
// pretend page. What is checked is what a user would see go wrong: a task that
// ends where it should go on, a step that runs with what it should have skipped,
// a box that is never given back.
//
// It takes a few seconds: the build, and the short waits the runner makes so
// that a user can see where Genie is.
import test, { before, after } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.join(here, "..");
let outDir = "";
let AgentRunner = null;

// ---- a page, as much of one as the runner touches ---------------------------------

class El {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.parentElement = null;
    this.className = "";
    this.text = "";
    this.attrs = {};
    this.listeners = {};
  }
  get classList() {
    const self = this;
    const list = () => self.className.split(/\s+/).filter(Boolean);
    return {
      add: (c) => {
        if (!list().includes(c)) self.className = list().concat([c]).join(" ");
      },
      remove: (c) => {
        self.className = list().filter((x) => x !== c).join(" ");
      },
      contains: (c) => list().includes(c),
    };
  }
  set textContent(v) {
    this.text = String(v);
    this.children = [];
  }
  get textContent() {
    return this.text + this.children.map((c) => c.textContent).join("");
  }
  append(...kids) {
    kids.forEach((k) => this.appendChild(k));
  }
  appendChild(k) {
    k.parentElement = this;
    this.children.push(k);
    return k;
  }
  prepend(k) {
    k.parentElement = this;
    this.children.unshift(k);
  }
  insertBefore(k, ref) {
    k.parentElement = this;
    const i = this.children.indexOf(ref);
    if (i < 0) this.children.push(k);
    else this.children.splice(i, 0, k);
    return k;
  }
  remove() {
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((c) => c !== this);
    this.parentElement = null;
  }
  setAttribute(k, v) {
    this.attrs[k] = v;
  }
  getAttribute(k) {
    return this.attrs[k];
  }
  addEventListener(type, fn) {
    (this.listeners[type] = this.listeners[type] || []).push(fn);
  }
  click() {
    (this.listeners.click || []).forEach((fn) => fn({}));
  }
  focus() {}
  scrollIntoView() {}
  get firstChild() {
    return this.children[0] || null;
  }
  querySelectorAll(sel) {
    const out = [];
    const match = (e) => (sel.startsWith(".") ? e.classList.contains(sel.slice(1)) : e.tag === sel);
    const walk = (n) =>
      n.children.forEach((c) => {
        if (match(c)) out.push(c);
        walk(c);
      });
    walk(this);
    return out;
  }
  querySelector(sel) {
    return this.querySelectorAll(sel)[0] || null;
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// A task with a scripted model. Each entry of `script` is one answer of the
// model: an object (sent as the JSON the model would write), a string (sent as
// it is, for answers that are not JSON), {http: 500, error: "..."} for a refusal
// of the server, {hang: true} for a model that never answers, {cut: "..."} for an
// answer that was cut off at the length limit.
function setup(script, opts = {}) {
  const log = { requests: [], said: [], notes: [], states: [], typed: [], events: [], reports: [] };
  const messages = new El("div");
  const term = { text: opts.terminal || "$ ", object: {} };
  globalThis.fetch = (url, init) => {
    log.requests.push(JSON.parse(init.body));
    const next = script.shift();
    if (next === undefined) return Promise.resolve({ ok: false, status: 500, headers: { get: () => null }, json: async () => ({ error: { message: "the script is over" } }) });
    if (next && next.hang) return new Promise(() => {});
    // a streamed answer: the pieces of text the body delivers one at a time
    if (next && next.sse) {
      const pieces = next.sse.slice();
      const headers = { "X-OpenREPL-Agent-Task": "token-1", "X-OpenREPL-Agent-Step": log.requests.length + "/" + (opts.max || 8), "Content-Type": "text/event-stream" };
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: { get: (k) => headers[k] || null },
        body: { getReader: () => ({ read: async () => (pieces.length ? { done: false, value: new TextEncoder().encode(pieces.shift()) } : { done: true, value: undefined }) }) },
      });
    }
    // an answer as a call of the step function (server/agent_tools.go)
    if (next && next.toolCall) {
      const headers = { "X-OpenREPL-Agent-Task": "token-1", "X-OpenREPL-Agent-Step": log.requests.length + "/" + (opts.max || 8) };
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: { get: (k) => headers[k] || null },
        json: async () => ({ choices: [{ message: { content: null, tool_calls: [{ type: "function", function: { name: "step", arguments: JSON.stringify(next.toolCall) } }] }, finish_reason: "tool_calls" }] }),
      });
    }
    if (next && next.http) return Promise.resolve({ ok: false, status: next.http, headers: { get: () => null }, json: async () => ({ error: { message: next.error, code: next.code } }) });
    const content = typeof next === "string" ? next : next.cut !== undefined ? next.cut : JSON.stringify(next);
    const headers = { "X-OpenREPL-Agent-Task": "token-1", "X-OpenREPL-Agent-Step": log.requests.length + "/" + (opts.max || 8) };
    return Promise.resolve({
      ok: true,
      status: 200,
      headers: { get: (k) => headers[k] || null },
      json: async () => ({ choices: [{ message: { content }, finish_reason: next && next.cut !== undefined ? "length" : "stop" }] }),
    });
  };
  const host = {
    url: () => "http://test/chat/completions",
    headers: () => ({}),
    modelFields: () => ({ model: "test-model" }),
    ideContext: () => ({ role: "system", content: opts.context ? opts.context() : "ide context" }),
    editorText: () => (opts.editorText ? opts.editorText() : ""),
    stream: () => !!opts.stream,
    readFiles: opts.readFiles ? (req, signal) => opts.readFiles(req, signal) : undefined,
    taskCredits: async () => (opts.credits === undefined ? null : opts.credits),
    report: (info) => log.reports.push(info),
    terminalText: () => term.text,
    terminal: () => term.object,
    terminalType: (data) => {
      log.typed.push(data);
      if (opts.onType) opts.onType(data, term);
      return opts.terminalTakesInput !== false;
    },
    thinking: () => {},
    tabs: () => ({ count: 1, active: 1 }),
    terminalReady: () => true,
    reconnect: () => {
      term.object = {};
      term.text = "fresh $ ";
      return true;
    },
    addTab: () => false,
    selectTab: () => false,
    closeTab: () => false,
    files: {
      ready: () => true,
      reveal: () => {},
      info: (p) => (p === "" ? { exists: true, type: "folder" } : p === "a.py" ? { exists: true, type: "file" } : { exists: false }),
      current: () => "",
      buffer: () => null,
      list: () => ({ ok: true, entries: ["a.py"], truncated: false }),
      open: async () => ({ ok: true }),
      create: async () => ({ ok: true }),
      save: async () => ({ ok: true }),
      rename: async () => ({ ok: true }),
      cut: () => ({ ok: true }),
      copy: () => ({ ok: true }),
      paste: async () => ({ ok: true }),
      move: async () => ({ ok: true }),
      remove: async () => {
        log.removed = true;
        return { ok: true };
      },
    },
    userSaid: async (t) => {
      log.said.push("user: " + t);
    },
    genieSaid: async (t) => {
      log.said.push("genie: " + t);
    },
    note: async (t) => {
      log.notes.push(t);
    },
    messages: () => messages,
    maxSteps: () => opts.max || 8,
    uid: () => "u1",
    repl: () => opts.repl || "",
    // what the panel's button is told while the panel may be closed
    attention: (on) => log.events.push(on ? "needs you" : "free"),
    progress: (n, max) => log.events.push("step " + n + "/" + max),
    ended: (outcome) => log.events.push("ended " + outcome),
  };
  const runner = new AgentRunner(host, (s) => log.states.push(s));
  // answers the permission cards as they come: (title, buttons) => the label to press
  let answering = true;
  const answers = (async () => {
    while (answering) {
      const card = messages.querySelector(".cw-agent__ask");
      if (card && opts.answer) {
        const title = card.querySelector(".cw-agent__ask-title").textContent;
        const buttons = card.querySelectorAll("button");
        const label = opts.answer(title, buttons.map((b) => b.textContent));
        (log.asked = log.asked || []).push(title + " [" + buttons.map((b) => b.textContent).join("/") + "] -> " + label);
        buttons.find((b) => b.textContent === label).click();
      }
      await sleep(5);
    }
  })();
  const done = async (p) => {
    await p;
    answering = false;
    await answers;
  };
  const steps = () => messages.querySelectorAll(".cw-agent__step").map((s) => s.textContent);
  const header = () => (messages.querySelector(".cw-agent__count") || { textContent: "" }).textContent;
  // what was sent back to the model after step n (1 based): the results of its actions
  const results = (n) => {
    const req = log.requests[n];
    const last = req.messages.filter((m) => m.role === "user").pop();
    return JSON.parse(last.content);
  };
  return { runner, log, done, steps, header, results, messages };
}

before(() => {
  outDir = fs.mkdtempSync(path.join(os.tmpdir(), "agent-built-"));
  const out = path.join(outDir, "agent.js");
  execFileSync(
    process.execPath,
    [path.join(root, "node_modules", "microbundle", "dist", "cli.js"), "-i", "test/fixtures/agent-entry.ts", "-o", out, "-f", "cjs", "--no-pkg-main", "--generateTypes", "false", "--no-sourcemap", "--define", "process.env.NODE_ENV=production"],
    { cwd: root, stdio: "pipe" }
  );
  globalThis.document = {
    createElement: (tag) => new El(tag),
    querySelectorAll: () => [],
    getElementById: () => null,
  };
  globalThis.window = { location: { pathname: "/" } };
  AgentRunner = createRequire(import.meta.url)(out).AgentRunner;
});

after(() => {
  if (outDir) fs.rmSync(outDir, { recursive: true, force: true });
});

test("built: a task that is done in one step", async () => {
  const t = setup([{ say: "Nothing to do.", actions: [], done: true }]);
  await t.done(t.runner.start("hello"));
  assert.equal(t.log.requests.length, 1);
  assert.deepEqual(t.log.said, ["user: hello", "genie: Nothing to do."]);
  assert.deepEqual(t.log.notes, []);
  assert.deepEqual(t.log.states, ["running", "idle"]);
  assert.equal(t.header(), "Finished");
  assert.equal(t.runner.running, false);
  // the first request starts a task, with the user's words and the page's context
  assert.equal(t.log.requests[0].context, "agent");
  assert.equal(t.log.requests[0].agent_task, "");
  assert.deepEqual(t.log.requests[0].messages.map((m) => m.role), ["user", "system"]);
});

test("built: an answer that cannot be read is asked for again, and the task goes on", async () => {
  // the bug this file is for: the `continue` after "asking again" was dropped by
  // the build tool, and the step went on with an answer that was not there
  const t = setup(["Sure! I will help you with that.", { say: "Here it is.", actions: [], done: true }]);
  await t.done(t.runner.start("do a thing"));
  assert.equal(t.log.requests.length, 2, "the model was not asked again");
  assert.deepEqual(t.log.notes, [], "the task must not end with an error: " + t.log.notes.join(" | "));
  assert.deepEqual(t.log.said, ["user: do a thing", "genie: Here it is."]);
  assert.equal(t.header(), "Finished");
  // the second request tells the model what was wrong with the first answer, and carries the task's token
  const told = t.results(1);
  assert.deepEqual(told.step_results, []);
  assert.match(told.not_done[0], /not a JSON object/);
  assert.equal(t.log.requests[1].agent_task, "token-1");
  assert.ok(t.steps().some((s) => /could not be read; asking again/.test(s)));
});

test("built: a second unreadable answer ends the task, with words for the user", async () => {
  const t = setup(["nope", "still nope", { say: "never asked", actions: [], done: true }]);
  await t.done(t.runner.start("do a thing"));
  assert.equal(t.log.requests.length, 2, "a third request was made");
  assert.equal(t.log.notes.length, 1);
  assert.match(t.log.notes[0], /could not put together a usable step/);
  assert.deepEqual(t.log.said, ["user: do a thing"], "an error is not something Genie said");
  assert.notEqual(t.header(), "Finished");
  assert.deepEqual(t.log.states, ["running", "idle"]);
});

test("built: an answer cut off at the length limit says so, to the model and to the user", async () => {
  const t = setup([{ cut: '{"say":"x","actions":[{"type":"editor_write","text":"aaa' }, { cut: '{"say":"x","actions":[{"type":"edi' }]);
  await t.done(t.runner.start("write a lot"));
  assert.equal(t.log.requests.length, 2);
  assert.match(t.results(1).not_done[0], /cut off because it was too long/);
  assert.match(t.log.notes[0], /too long for one answer/);
});

test("built: a refusal of the server ends the task and gives the box back", async () => {
  const t = setup([{ http: 429, error: "You have started 20 tasks in the last hour, which is the limit.", code: "agent_task_limit" }]);
  await t.done(t.runner.start("anything"));
  assert.equal(t.log.requests.length, 1);
  assert.deepEqual(t.log.notes, ["You have started 20 tasks in the last hour, which is the limit."]);
  assert.deepEqual(t.log.states, ["running", "idle"]);
  assert.equal(t.runner.running, false);
});

test("built: at the last step of a stretch the user is asked whether to carry on, and Stop here ends the task", async () => {
  // an action that is not in the protocol is dropped and reported, so the task is not done
  const again = { say: "", actions: [{ type: "shell", text: "ls" }], done: false };
  const t = setup([again, again, again, again, again], { max: 3, answer: (title) => (/step limit/.test(title) ? "Stop here" : "Allow once") });
  await t.done(t.runner.start("go on for ever"));
  assert.equal(t.log.requests.length, 3, "the loop did not stop at the limit");
  assert.deepEqual(t.log.asked, ["Paused at the step limit [Carry on/Stop here] -> Stop here"]);
  assert.match(t.results(1).not_done[0], /"shell" is not an action/);
  assert.notEqual(t.header(), "Finished");
  assert.match(t.header(), /paused/);
});

test("built: carrying on goes on with the same task and its token, a stretch at a time, up to three times", async () => {
  const again = { say: "", actions: [{ type: "shell", text: "ls" }], done: false };
  let asks = 0;
  const t = setup([again, again, again, again, again, again, again, again, again, again, again, again, again, again], {
    max: 2,
    answer: (title) => (++asks <= 3 ? "Carry on" : "Stop here"),
  });
  await t.done(t.runner.start("go on for ever"));
  // 2 steps, carry, 2, carry, 2, carry, 2, then the fourth question is not asked: three carries are all there is
  assert.equal(t.log.requests.length, 8);
  assert.equal(t.log.asked.length, 3);
  assert.equal(t.log.requests[0].agent_carry, undefined);
  assert.equal(t.log.requests[2].agent_carry, true, "the first step after a carry says so");
  assert.equal(t.log.requests[3].agent_carry, undefined, "the next one does not");
  assert.equal(t.log.requests[2].agent_task, "token-1", "the same task");
  assert.match(t.log.notes[t.log.notes.length - 1], /carried on 3 times/);
});

test("built: a task that passes its budget is asked about, carries on when the user says so, and ends when not", async () => {
  const go = { say: "", actions: [{ type: "shell", text: "x" }], done: false };
  const t = setup([go, { http: 429, code: "agent_budget", error: "This task has used 42 of the 40 credits one task may use." }, { say: "Done.", actions: [], done: true }], { answer: () => "Carry on" });
  await t.done(t.runner.start("work"));
  assert.deepEqual(t.log.asked, ["This task has used its budget [Carry on/Stop here] -> Carry on"]);
  assert.equal(t.log.requests.length, 3);
  assert.equal(t.log.requests[1].agent_carry, undefined);
  assert.equal(t.log.requests[2].agent_carry, true);
  assert.equal(t.header(), "Finished");
  // the user says no: the task ends, nothing is lost, no error is shown
  const no = setup([go, { http: 429, code: "agent_budget", error: "This task has used 42 of the 40 credits one task may use." }], { answer: () => "Stop here" });
  await no.done(no.runner.start("work"));
  assert.equal(no.log.requests.length, 2);
  assert.deepEqual(no.log.notes, []);
  assert.notEqual(no.header(), "Finished");
});

test("built: the question names what is done of the plan, and Stop while it is open answers no", async () => {
  const step = { say: "", plan: ["One", "Two", "Three"], plan_done: [1], actions: [{ type: "shell", text: "ls" }], done: false };
  const t = setup([step, { say: "", actions: [{ type: "shell", text: "ls" }], done: false }], { max: 2 });
  const running = t.runner.start("work");
  for (let i = 0; i < 100 && !t.messages.querySelector(".cw-agent__ask"); i++) await sleep(10);
  const text = t.messages.querySelector(".cw-agent__ask-text").textContent;
  assert.match(text, /1 of 3 plan items are done/);
  assert.match(text, /up to 2 more steps/);
  t.runner.stop();
  await t.done(running);
  assert.equal(t.log.requests.length, 2);
  assert.equal(t.messages.querySelectorAll(".cw-agent__ask").length, 0, "the question was left open");
  assert.deepEqual(t.log.states, ["running", "stopping", "idle"]);
});

test("built: the results of the actions go back to the model, in order, and a denial is one of them", async () => {
  const t = setup(
    [
      { say: "Looking, then running.", actions: [{ type: "files_list" }, { type: "run" }], done: false },
      { say: "Done.", actions: [{ type: "finish" }], done: false },
    ],
    { answer: (title) => (/run your code/.test(title) ? "Deny" : "Allow for this session") }
  );
  await t.done(t.runner.start("list and run"));
  assert.equal(t.log.requests.length, 2);
  const r = t.results(1).step_results;
  assert.deepEqual(r.map((x) => x.type + ":" + x.status), ["files_list:done", "run:denied"]);
  assert.equal(r[0].output, "a.py");
  assert.equal(t.header(), "Finished");
  assert.deepEqual(t.log.said, ["user: list and run", "genie: Looking, then running.", "genie: Done."]);
  assert.equal(t.log.asked.length, 2, "one question for the files, one for Run: " + t.log.asked.join(" | "));
});

test("built: what is asked about every time is asked again after a session grant, and what cannot be done is not asked about", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "files_list" }, { type: "files_delete", path: "a.py" }, { type: "files_delete", path: "missing.py" }], done: false },
      { say: "", actions: [{ type: "terminal_close_tab", tab: 1 }], done: true },
    ],
    { answer: (title, buttons) => (buttons.includes("Allow for this session") ? "Allow for this session" : "Allow once") }
  );
  await t.done(t.runner.start("clean up"));
  const r = t.results(1).step_results;
  assert.deepEqual(r.map((x) => x.type + ":" + x.status), ["files_list:done", "files_delete:done", "files_delete:failed"]);
  assert.equal(t.log.removed, true);
  // the list was allowed for the session; the delete still asked, with no session button; the missing file and tab 1 asked nothing
  assert.equal(t.log.asked.length, 2, t.log.asked.join(" | "));
  assert.match(t.log.asked[0], /Allow once\/Allow for this session\/Deny/);
  assert.match(t.log.asked[1], /\[Allow once\/Deny\//);
  assert.doesNotMatch(t.log.asked[1], /Allow for this session|Allow everything/); // a risky action cannot be allowed for the session
});

test("built: Allow everything asks to confirm once, and then nothing else is asked about, except what is always asked", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "files_list" }, { type: "terminal_type", text: "echo hi" }, { type: "run" }], done: false },
      { say: "", actions: [{ type: "files_delete", path: "a.py" }], done: true },
    ],
    {
      answer: (title, buttons) =>
        buttons.includes("Allow everything") ? "Allow everything" : buttons.includes("Allow everything for this session…") ? "Allow everything for this session…" : "Allow once",
    }
  );
  await t.done(t.runner.start("do it"));
  // the first action: one card, answered in two clicks (the choice, then its confirmation); then only the delete, a risky one, asks again
  assert.equal(t.log.asked.length, 3, t.log.asked.join(" | "));
  assert.match(t.log.asked[0], /-> Allow everything for this session…$/);
  assert.match(t.log.asked[1], /-> Allow everything$/);
  assert.match(t.log.asked[2], /-> Allow once$/);
  assert.match(t.log.asked[2], /\[Allow once\/Deny\//); // a risky action has no session button
  assert.equal(t.log.removed, true);
});

test("built: when the user allowed risky deletes for a while, the delete is moved to the Trash and nothing is asked; a hidden file is still asked", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "files_delete", path: "a.py" }], done: false },
      { say: "", actions: [], done: true },
    ],
    { answer: () => "Allow once" }
  );
  t.runner.perms.allowRisky(["delete"], 30);
  t.runner.perms.grantEverything();
  await t.done(t.runner.start("clean up"));
  assert.equal(t.log.removed, undefined, "the file was deleted for good instead of moved to the Trash");
  assert.equal((t.log.asked || []).length, 0, "a card was shown: " + (t.log.asked || []).join(" | "));
  const r = t.results(1).step_results[0];
  assert.equal(r.status, "done");
  assert.match(r.detail, /Trash/);
  assert.match(t.steps().join(" "), /risky, allowed by you/);
});

test("built: the editor's changes go in without a review when everything is allowed, with an Undo on the line", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_write", text: "print(1)\n" }], done: false },
      { say: "", actions: [], done: true },
    ],
    { answer: () => "Allow once" }
  );
  let undone = 0;
  globalThis.window.GenieReview = {
    applyDirect: () => ({ total: 2, accepted: 2, rejected: 0, undo: () => (undone++, true) }),
    proposeAsync: async () => {
      throw new Error("the review was shown although changes are allowed without one");
    },
  };
  t.runner.perms.grantEverything();
  await t.done(t.runner.start("write it"));
  const r = t.results(1).step_results[0];
  assert.equal(r.status, "accepted");
  assert.equal(r.accepted, 2);
  assert.equal((t.log.asked || []).length, 0);
  const undo = t.messages.querySelectorAll(".cw-agent__step-btn");
  assert.equal(undo.length, 1);
  undo[0].click();
  assert.equal(undone, 1);
  assert.equal(t.runner.perms.undoable().length, 0); // undone on the line, so the badge menu does not list it any more
});

test("built: a language that is a mode of the editor (HTML, CSS) is set as the mode, with the terminal left alone, and an unknown one lists both", async () => {
  const sel = (id, options, value) => ({
    id,
    options,
    value,
    changed: 0,
    dispatchEvent(e) {
      if (e.type === "change") this.changed++;
      return true;
    },
  });
  const picker = sel("optionlist", [{ value: "c", text: "C" }, { value: "python", text: "Python" }], "python");
  const mode = sel("select-lang", [{ value: "css", text: "CSS" }, { value: "html", text: "HTML" }, { value: "javascript", text: "JavaScript" }, { value: "image", text: "Image", disabled: true }], "text");
  const t = setup(
    [
      { say: "", actions: [{ type: "set_language", language: "HTML" }], done: false },
      { say: "", actions: [{ type: "set_language", language: "COBOL" }], done: false },
      { say: "", actions: [], done: true },
    ],
    { answer: () => "Allow once" }
  );
  globalThis.document.getElementById = (id) => (id === "optionlist" ? picker : id === "select-lang" ? mode : null);
  try {
    await t.done(t.runner.start("make a web page"));
  } finally {
    globalThis.document.getElementById = () => null;
  }
  const r1 = t.results(1).step_results[0];
  assert.equal(r1.status, "done");
  assert.match(r1.detail, /mode is HTML/);
  assert.match(r1.detail, /preview_open/);
  assert.equal(mode.value, "html");
  assert.equal(mode.changed, 1);
  assert.equal(picker.value, "python", "the terminal's language was changed");
  assert.equal(picker.changed, 0);
  const r2 = t.results(2).step_results[0];
  assert.equal(r2.status, "failed");
  assert.match(r2.detail, /the picker has: C, Python/);
  assert.match(r2.detail, /editor's modes are: CSS, HTML, JavaScript/);
  assert.doesNotMatch(r2.detail, /Image/); // a mode that cannot be chosen is not offered
});

test("built: Stop ends a task whose model is silent, and the box comes back", async () => {
  const t = setup([{ hang: true }]);
  const running = t.runner.start("wait for ever");
  await sleep(50);
  assert.equal(t.runner.running, true);
  t.runner.stop();
  await t.done(running);
  assert.deepEqual(t.log.states, ["running", "stopping", "idle"]);
  assert.deepEqual(t.log.notes, [], "stopping is not an error");
  assert.equal(t.runner.running, false);
  // and a new task can start at once
  const again = setup([{ say: "ok", actions: [], done: true }]);
  await again.done(again.runner.start("again"));
  assert.equal(again.header(), "Finished");
});

test("built: Stop while a question is open answers it no, and nothing more is done", async () => {
  const t = setup([{ say: "", actions: [{ type: "files_delete", path: "a.py" }, { type: "files_list" }], done: false }]);
  const running = t.runner.start("delete it");
  while (!t.messages.querySelector(".cw-agent__ask")) await sleep(5);
  t.runner.stop();
  await t.done(running);
  assert.equal(t.log.removed, undefined, "the file was deleted although the task was stopped");
  assert.equal(t.log.requests.length, 1);
  assert.equal(t.messages.querySelector(".cw-agent__ask"), null, "the question is still on the screen");
  assert.deepEqual(t.log.states, ["running", "stopping", "idle"]);
});

test("built: a line typed in the terminal waits for the output to settle, and returns what is new", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "terminal_type", text: "echo hi", wait_seconds: 5 }], done: false },
      { say: "", actions: [], done: true },
    ],
    {
      terminal: "$ ",
      answer: () => "Allow once",
      onType: (data, term) => {
        setTimeout(() => {
          term.text += "echo hi\nhi\n$ ";
        }, 100);
      },
    }
  );
  await t.done(t.runner.start("say hi"));
  assert.deepEqual(t.log.typed, ["echo hi\r"]);
  const r = t.results(1).step_results[0];
  assert.equal(r.status, "done");
  assert.equal(r.output, "echo hi\nhi\n$ ");
  assert.equal(r.detail, undefined, "the terminal was quiet: " + r.detail);
});

test("built: a restarted terminal is waited for, and its first lines come back", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "terminal_reconnect" }, { type: "read_output", wait_seconds: 3 }], done: false },
      { say: "", actions: [], done: true },
    ],
    { answer: () => "Allow once" }
  );
  await t.done(t.runner.start("restart"));
  const r = t.results(1).step_results;
  assert.deepEqual(r.map((x) => x.type + ":" + x.status), ["terminal_reconnect:done", "read_output:done"]);
  assert.equal(r[0].output, "fresh $");
  assert.equal(r[1].output, "fresh $");
});

// ---- a task goes on behind a closed panel: what the panel's button is told ---------

test("built: the panel is told each step, and that the task is done", async () => {
  const t = setup([
    { say: "Looking.", actions: [{ type: "files_list" }], done: false },
    { say: "Done.", actions: [{ type: "finish" }], done: false },
  ], { answer: () => "Allow once" });
  await t.done(t.runner.start("look"));
  // (the header is set twice a step: when it begins, and with the server's count)
  const seen = t.log.events.filter((e) => /^(step|ended)/.test(e)).filter((e, i, all) => e !== all[i - 1]);
  assert.deepEqual(seen, ["step 1/8", "step 2/8", "ended done"]);
  // the end comes after the task stopped running, and before the box is given back
  assert.deepEqual(t.log.states, ["running", "idle"]);
});

test("built: a question to the user is announced while it is open and withdrawn when answered", async () => {
  const t = setup([
    { say: "", actions: [{ type: "files_list" }], done: false },
    { say: "ok", actions: [], done: true },
  ], { answer: () => "Allow once" });
  await t.done(t.runner.start("list"));
  const i = t.log.events.indexOf("needs you");
  assert.ok(i >= 0, "the panel was not told Genie was waiting: " + t.log.events.join(", "));
  assert.equal(t.log.events[i + 1], "free");
  assert.equal(t.log.events.filter((e) => e === "needs you").length, t.log.events.filter((e) => e === "free").length);
});

test("built: a question left open and then stopped is withdrawn once", async () => {
  const t = setup([{ say: "", actions: [{ type: "files_delete", path: "a.py" }], done: false }]);
  const running = t.runner.start("delete it");
  while (!t.messages.querySelector(".cw-agent__ask")) await sleep(5);
  t.runner.stop();
  await t.done(running);
  assert.equal(t.log.events.filter((e) => e === "needs you").length, 1);
  assert.equal(t.log.events.filter((e) => e === "free").length, 1);
  assert.equal(t.log.events[t.log.events.length - 1], "ended stopped");
});

test("built: a task that fails is reported as failed, and Stop as stopped", async () => {
  const failed = setup([{ http: 500, error: "down" }]);
  await failed.done(failed.runner.start("anything"));
  assert.equal(failed.log.events[failed.log.events.length - 1], "ended failed");

  const unreadable = setup(["nope", "still nope"]);
  await unreadable.done(unreadable.runner.start("anything"));
  assert.equal(unreadable.log.events[unreadable.log.events.length - 1], "ended failed");

  const stopped = setup([{ hang: true }]);
  const running = stopped.runner.start("wait");
  await sleep(50);
  stopped.runner.stop();
  await stopped.done(running);
  assert.equal(stopped.log.events[stopped.log.events.length - 1], "ended stopped");
});

test("built: every step names the REPL in use, so that the server can say how it is typed into", async () => {
  const t = setup([{ say: "", actions: [{ type: "shell" }], done: false }, { say: "ok", actions: [], done: true }], { repl: "gomacro" });
  await t.done(t.runner.start("try a loop"));
  assert.deepEqual(t.log.requests.map((r) => r.repl), ["gomacro", "gomacro"]);
});

// ---- the plan, sending less, and checking the work ---------------------------------------

test("built: the plan is shown, ticked off as the model says, and a changed plan keeps what is done", async () => {
  const t = setup(
    [
      { say: "Plan.", plan: ["Look around", "Make the change", "Run it"], actions: [{ type: "files_list" }], done: false },
      { say: "", plan_done: [1], actions: [{ type: "files_list" }], done: false },
      { say: "", plan: ["Look around", "Make the change", "Run it", "Tell you"], plan_done: [1, 2], actions: [], done: true },
    ],
    { answer: () => "Allow for this session" }
  );
  await t.done(t.runner.start("do a thing"));
  const marks = () => t.messages.querySelector(".cw-agent__plan-list").querySelectorAll(".cw-agent__step").map((s) => s.textContent);
  assert.deepEqual(marks(), ["✓Look around", "✓Make the change", "◔Run it", "○Tell you"]);
  assert.equal(t.messages.querySelectorAll(".cw-agent__plan").length, 1, "one plan box, drawn again");
  // the plan sits above the steps in the card
  const card = t.messages.querySelector(".cw-agent");
  assert.ok(card.children.findIndex((c) => c.classList.contains("cw-agent__plan")) < card.children.findIndex((c) => c.classList.contains("cw-agent__steps")));
});

test("built: older results and answers are sent short, and an unchanged editor and terminal are not sent again", async () => {
  const code = "x".repeat(1500);
  const step = (n) => ({ say: "step " + n, actions: [{ type: "editor_write", text: code + n }, { type: "files_list" }], done: false });
  const t = setup([step(1), step(2), step(3), step(4), { say: "done", actions: [{ type: "run" }], done: true }], { max: 8, answer: () => "Allow for this session" });
  globalThis.window.GenieReview = { applyDirect: () => ({ total: 1, accepted: 1, rejected: 0, undo: () => true }), proposeAsync: async () => ({ total: 1, accepted: 1 }) };
  globalThis.window.CompileandRun = () => {};
  t.runner.perms.grantEverything();
  await t.done(t.runner.start("write it"));
  assert.equal(t.log.requests.length, 5);
  const last = t.log.requests[4].messages;
  const text = JSON.stringify(last);
  // the first request carries the context, the later ones only say it is the same
  assert.ok(JSON.stringify(t.log.requests[0].messages).includes("ide context"));
  assert.ok(!text.includes("ide context"), "an unchanged context was sent again");
  assert.match(last[last.length - 1].content, /the same as in the context of your last step/);
  // the code of steps 1 and 2 is out of the history, the code of steps 3 and 4 is in
  assert.ok(!text.includes(code + "1") && !text.includes(code + "2"));
  assert.ok(text.includes(code + "3") && text.includes(code + "4"));
  assert.match(text, /step_results_older/);
  // the page's own record of the task is whole
  assert.ok(t.log.requests[1].messages.length < t.log.requests[4].messages.length);
});

test("built: a changed editor or terminal is sent again", async () => {
  let n = 0;
  const t = setup([{ say: "", actions: [{ type: "files_list" }], done: false }, { say: "", actions: [], done: true }], { context: () => "context " + ++n, answer: () => "Allow for this session" });
  await t.done(t.runner.start("go"));
  assert.ok(JSON.stringify(t.log.requests[1].messages).includes("context 2"));
});

test("built: a task that changed code and says it is done is sent back once to run it", async () => {
  const t = setup(
    [
      { say: "Written.", actions: [{ type: "editor_write", text: "print(1)\n" }], done: true },
      { say: "Ran it.", actions: [{ type: "run" }], done: false },
      { say: "It works.", actions: [], done: true },
    ],
    { answer: () => "Allow for this session" }
  );
  globalThis.window.GenieReview = { applyDirect: () => ({ total: 1, accepted: 1, rejected: 0, undo: () => true }), proposeAsync: async () => ({ total: 1, accepted: 1 }) };
  globalThis.window.CompileandRun = () => {};
  t.runner.perms.grantEverything();
  await t.done(t.runner.start("write and run"));
  assert.equal(t.log.requests.length, 3, "the model was not sent back");
  const told = t.results(1);
  assert.match(told.not_done.join(" "), /Run the program or the tests/);
  assert.equal(t.header(), "Finished");
  // the end of the task says what was checked and what changed
  const chips = t.messages.querySelectorAll(".cw-agent__chip").map((c) => c.textContent);
  assert.ok(chips.some((c) => /Checked/.test(c) && /Run/.test(c)), chips.join(" | "));
  assert.ok(chips.some((c) => /Changed/.test(c)), chips.join(" | "));
  assert.deepEqual(t.log.reports.map((r) => [r.outcome, r.changed, r.checked]), [["done", true, true]]);
});

test("built: a task is sent back to check only once, and then says it was not checked", async () => {
  const t = setup(
    [
      { say: "Written.", actions: [{ type: "editor_write", text: "print(1)\n" }], done: true },
      { say: "There is nothing here that can be run.", actions: [], done: true },
    ],
    { answer: () => "Allow for this session" }
  );
  globalThis.window.GenieReview = { applyDirect: () => ({ total: 1, accepted: 1, rejected: 0, undo: () => true }), proposeAsync: async () => ({ total: 1, accepted: 1 }) };
  t.runner.perms.grantEverything();
  await t.done(t.runner.start("write"));
  assert.equal(t.log.requests.length, 2);
  const chips = t.messages.querySelectorAll(".cw-agent__chip").map((c) => c.textContent);
  assert.ok(chips.some((c) => /Not checked/.test(c) && /nothing here that can be run/.test(c)), chips.join(" | "));
  assert.deepEqual(t.log.reports.map((r) => r.checked), [false]);
});

test("built: a task that changed nothing is not sent back, and a task at its last step is not either", async () => {
  const none = setup([{ say: "It is a loop.", actions: [], done: true }]);
  await none.done(none.runner.start("explain"));
  assert.equal(none.log.requests.length, 1);
  assert.equal(none.messages.querySelectorAll(".cw-agent__chip").length, 0);
  const last = setup([{ say: "Written.", actions: [{ type: "editor_write", text: "x\n" }], done: true }], { max: 1, answer: () => "Allow for this session" });
  globalThis.window.GenieReview = { applyDirect: () => ({ total: 1, accepted: 1, rejected: 0, undo: () => true }), proposeAsync: async () => ({ total: 1, accepted: 1 }) };
  last.runner.perms.grantEverything();
  await last.done(last.runner.start("write"));
  assert.equal(last.log.requests.length, 1, "sent back with no step left");
});

test("built: after two fix attempts the code is not changed again while the check fails", async () => {
  const edit = (n) => ({ say: "fix " + n, actions: [{ type: "editor_write", text: "v" + n + "\n" }], done: false });
  const check = { say: "run", actions: [{ type: "bash", command: "pytest -q" }], done: false };
  const t = setup([edit(1), check, edit(2), check, edit(3), check, edit(4), { say: "it still fails", actions: [], done: true }], {
    max: 8,
    answer: () => "Allow for this session",
  });
  globalThis.window.GenieReview = { applyDirect: () => ({ total: 1, accepted: 1, rejected: 0, undo: () => true }), proposeAsync: async () => ({ total: 1, accepted: 1 }) };
  t.runner.runCommand = undefined;
  t.runner.perms.grantEverything();
  // the pretend shell says the tests fail
  const host = t.runner.host;
  host.runCommand = async () => ({ status: 200, exit_code: 1, output: "1 failed", duration_ms: 5 });
  await t.done(t.runner.start("fix it"));
  const labels = t.steps();
  assert.ok(labels.some((s) => /Fix attempt 1 of 2/.test(s)), labels.join(" | "));
  assert.ok(labels.some((s) => /Fix attempt 2 of 2/.test(s)), labels.join(" | "));
  assert.ok(labels.some((s) => /2 fix attempts were made/.test(s)), labels.join(" | "));
});

test("built: how a task ended is reported with its reason, and an unreadable answer says so", async () => {
  const t = setup(["nope", "still nope"]);
  await t.done(t.runner.start("do a thing"));
  assert.deepEqual(t.log.reports.map((r) => r.outcome), ["unreadable"]);
  const long = setup([{ cut: '{"say":"x","actions":[{"type":"editor_write","text":"aaa' }, { cut: '{"say":"x","actions":[{"type":"edi' }]);
  await long.done(long.runner.start("write a lot"));
  assert.deepEqual(long.log.reports.map((r) => r.outcome), ["toolong"]);
});

test("built: the end of a task tells the steps, the tokens and, when a plan counts them, the credits", async () => {
  const t = setup([{ say: "Yes.", actions: [], done: true }], { credits: 9 });
  await t.done(t.runner.start("explain"));
  const meta = t.messages.querySelector(".cw-agent__meta").textContent;
  assert.match(meta, /1 step/);
  assert.match(meta, /9 credits/);
  const plain = setup([{ say: "Yes.", actions: [], done: true }]);
  await plain.done(plain.runner.start("explain"));
  assert.ok(!/credit/.test(plain.messages.querySelector(".cw-agent__meta").textContent));
});

// ---- editing a part, looking around ----------------------------------------------------------

function reviewing(proposed) {
  globalThis.window.GenieReview = {
    proposeAsync: async (text, o) => {
      proposed.push({ text, title: o.title });
      return { total: 1, accepted: 1 };
    },
    applyDirect: (d) => {
      proposed.push({ text: d.text, direct: true });
      return { total: 1, accepted: 1, rejected: 0, undo: () => true };
    },
  };
}

test("built: editor_replace puts the new text where the old text was, and the review is of the whole file with one change", async () => {
  const proposed = [];
  reviewing(proposed);
  const code = "def f():\n    return 1\n\nprint(f())\n";
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_replace", old: "return 1", new: "return 2" }], done: false },
      { say: "", actions: [{ type: "run" }], done: true },
    ],
    { editorText: () => code, answer: () => "Allow once" }
  );
  globalThis.window.CompileandRun = () => {};
  await t.done(t.runner.start("change it"));
  assert.equal(proposed[0].text, "def f():\n    return 2\n\nprint(f())\n");
  assert.match(proposed[0].title, /this part of the file/);
  assert.equal(t.results(1).step_results[0].type, "editor_replace");
  assert.equal(t.results(1).step_results[0].status, "accepted");
});

test("built: editor_replace says why an old text that is not there, or is there twice, was not used", async () => {
  const proposed = [];
  reviewing(proposed);
  const code = "x = 1\nx = 1\ny = 2\n";
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_replace", old: "z = 3", new: "z = 4" }, { type: "editor_replace", old: "x = 1", new: "x = 5" }], done: false },
      { say: "ok", actions: [], done: true },
    ],
    { editorText: () => code, answer: () => "Allow once" }
  );
  await t.done(t.runner.start("change it"));
  assert.deepEqual(proposed, [], "nothing was proposed");
  const r = t.results(1).step_results;
  assert.equal(r[0].status, "failed");
  assert.match(r[0].detail, /not found in the editor/);
  assert.equal(r[1].status, "failed");
  assert.match(r[1].detail, /more than once/);
});

test("built: editor_replace by line numbers replaces those lines, and an empty new text deletes them", async () => {
  const proposed = [];
  reviewing(proposed);
  const code = "a\nb\nc\nd\n";
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_replace", start_line: 2, end_line: 3, new: "B\nC\nC2" }], done: false },
      { say: "", actions: [{ type: "editor_replace", start_line: 1, end_line: 1, new: "" }, { type: "editor_replace", start_line: 99, new: "x" }, { type: "run" }], done: true },
    ],
    { editorText: () => code, answer: () => "Allow once" }
  );
  globalThis.window.CompileandRun = () => {};
  await t.done(t.runner.start("change it"));
  assert.equal(proposed[0].text, "a\nB\nC\nC2\nd\n");
  assert.equal(proposed[1].text, "b\nc\nd\n");
  assert.equal(proposed.length, 2, "a line that is not there was proposed");
  assert.match(t.log.requests[1].messages.map((m) => m.content).join("\n"), /editor_replace/);
  assert.equal(t.log.requests.length, 2);
});

test("built: searching and reading ask the user nothing, show what was found and send it back", async () => {
  const seen = [];
  const t = setup(
    [
      { say: "Looking.", actions: [{ type: "files_search", query: "login" }, { type: "files_read", path: "auth.py", start_line: 3, end_line: 5 }], done: false },
      { say: "Found it.", actions: [], done: true },
    ],
    {
      readFiles: async (req) => {
        if (req.path === "GENIE.md") return { status: 404, message: "There is no such file in the workspace." };
        seen.push(req);
        if (req.op === "search") return { status: 200, matches: [{ file: "auth.py", line: 3, text: "def login():" }, { file: "routes.py", line: 7, text: "app.add('/login', login)" }], files_scanned: 12 };
        return { status: 200, path: "auth.py", start_line: 3, end_line: 5, total_lines: 20, text: "    3  def login():\n    4      pass\n    5  \n", next_line: 6 };
      },
    }
  );
  await t.done(t.runner.start("find the login"));
  assert.deepEqual((t.log.asked || []), [], "something was asked");
  assert.deepEqual(seen.map((r) => r.op), ["search", "read"]);
  assert.equal(seen[1].start_line, 3);
  const r = t.results(1).step_results;
  assert.equal(r[0].status, "done");
  assert.match(r[0].detail, /2 matches in 2 files/);
  assert.match(r[0].output, /auth\.py:3: def login\(\):/);
  assert.match(r[1].detail, /lines 3 to 5 of 20; next_line 6/);
  assert.match(r[1].output, /    3  def login/);
  // the lines are in the card, the few matches open
  const outs = t.messages.querySelectorAll(".cw-agent__out");
  assert.equal(outs.length, 2);
  assert.equal(outs[0].open, true);
  assert.equal(outs[1].open, undefined);
  assert.ok(t.steps().some((s) => /Searching the files for "login": 2 matches in 2 files/.test(s)));
});

test("built: a search with no match, a refusal and a page that cannot look say so", async () => {
  const t = setup(
    [
      { say: "", actions: [{ type: "files_search", query: "zzz" }, { type: "files_read", path: "nope.py" }], done: false },
      { say: "", actions: [], done: true },
    ],
    {
      readFiles: async (req) => (req.op === "search" ? { status: 200, matches: [], files_scanned: 4 } : { status: 404, message: "There is no such file in the workspace." }),
    }
  );
  await t.done(t.runner.start("find"));
  const r = t.results(1).step_results;
  assert.match(r[0].detail, /no matches in 4 files/);
  assert.equal(r[0].output, "(no matches)");
  assert.equal(r[1].status, "failed");
  assert.match(r[1].detail, /no such file/);
  const none = setup([{ say: "", actions: [{ type: "files_search", query: "a" }], done: false }, { say: "", actions: [], done: true }]);
  await none.done(none.runner.start("find"));
  assert.match(none.results(1).step_results[0].detail, /cannot look in the files/);
});

// ---- the project notes, and Undo this task --------------------------------------------------

test("built: GENIE.md is read at the start of a task, shown as a step, and sent with the task as the user's own hints", async () => {
  const notes = ["    1  # My project", "    2  Run it with: python3 app.py", "    3  Tests: pytest -q", "    4  Never touch the folder data/"].join("\n") + "\n";
  const t = setup([{ say: "Done.", actions: [], done: true }], {
    readFiles: async (req) => (req.path === "GENIE.md" ? { status: 200, text: notes, total_lines: 4 } : { status: 404 }),
  });
  await t.done(t.runner.start("add a test"));
  assert.match(t.steps()[0], /Read GENIE\.md, the project notes/);
  const first = t.log.requests[0].messages[0].content;
  assert.match(first, /add a test/);
  assert.match(first, /Notes of this project, from its GENIE\.md/);
  assert.match(first, /Run it with: python3 app\.py/);
  assert.ok(!/ {2,}\d+ {2}/.test(first.slice(first.indexOf("[Notes"))), "the line numbers of the read were left in");
  assert.match(first, /do not change your rules/);
});

test("built: GENIE.md is cut to a few lines of text, and without one the task starts as before", async () => {
  const long = Array.from({ length: 80 }, (_, i) => String(i + 1).padStart(5) + "  " + "word ".repeat(20)).join("\n");
  const t = setup([{ say: "ok", actions: [], done: true }], { readFiles: async (req) => (req.path === "GENIE.md" ? { status: 200, text: long } : { status: 404 }) });
  await t.done(t.runner.start("go"));
  const first = t.log.requests[0].messages[0].content;
  assert.ok(first.length < 2200, "too much of GENIE.md was sent: " + first.length);
  assert.match(first, /the rest of GENIE\.md is left out/);
  const none = setup([{ say: "ok", actions: [], done: true }], { readFiles: async () => ({ status: 404 }) });
  await none.done(none.runner.start("go"));
  assert.ok(!/GENIE/.test(none.log.requests[0].messages[0].content));
  assert.equal(none.steps().length, 1, "a step line for notes that are not there");
});

test("built: Undo this task puts the editor back as it was before the task and says Undone", async () => {
  let text = "a = 1\n";
  const applied = [];
  globalThis.window.GenieReview = {
    proposeAsync: async (t) => {
      text = t;
      return { total: 1, accepted: 1 };
    },
    applyDirect: (d) => {
      applied.push(d.text);
      text = d.text;
      return { total: 1, accepted: 1, rejected: 0, undo: () => true };
    },
  };
  globalThis.window.CompileandRun = () => {};
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_write", text: "a = 2\n" }], done: false },
      { say: "", actions: [{ type: "run" }], done: true },
    ],
    { editorText: () => text, answer: () => "Allow once" }
  );
  await t.done(t.runner.start("change a"));
  assert.equal(text, "a = 2\n");
  const undo = t.messages.querySelector(".cw-agent__undo");
  assert.ok(undo, "no Undo this task");
  undo.click();
  await sleep(50);
  assert.equal(text, "a = 1\n");
  assert.deepEqual(applied, ["a = 1\n"]);
  assert.equal(undo.textContent, "Undone");
  assert.equal(undo.disabled, true);
});

test("built: Undo this task does not overwrite what the user wrote after the task", async () => {
  let text = "a = 1\n";
  globalThis.window.GenieReview = {
    proposeAsync: async (t) => {
      text = t;
      return { total: 1, accepted: 1 };
    },
    applyDirect: (d) => {
      text = d.text;
      return { total: 1, accepted: 1, rejected: 0, undo: () => true };
    },
  };
  globalThis.window.CompileandRun = () => {};
  const t = setup(
    [
      { say: "", actions: [{ type: "editor_write", text: "a = 2\n" }], done: false },
      { say: "", actions: [{ type: "run" }], done: true },
    ],
    { editorText: () => text, answer: () => "Allow once" }
  );
  await t.done(t.runner.start("change a"));
  text = "a = 2\nmy own line\n"; // the user typed after the task
  t.messages.querySelector(".cw-agent__undo").click();
  await sleep(50);
  assert.equal(text, "a = 2\nmy own line\n", "the user's text was overwritten");
  assert.match(t.log.notes[t.log.notes.length - 1], /changed since the task/);
  assert.equal(t.messages.querySelector(".cw-agent__undo").textContent, "Undo this task");
});

test("built: a task that changed nothing has no Undo", async () => {
  const t = setup([{ say: "It is a loop.", actions: [], done: true }]);
  await t.done(t.runner.start("explain"));
  assert.equal(t.messages.querySelector(".cw-agent__undo"), null);
});

// ---- streamed answers and native tool calls --------------------------------------------------

const sseOf = (text, usage) => {
  const json = JSON.stringify({ say: "Looking.", actions: [], done: true });
  const half = Math.floor(text.length / 2);
  const ev = (c) => "data: " + JSON.stringify({ choices: [{ delta: { content: c } }] }) + "\n\n";
  return [ev(text.slice(0, half)), ev(text.slice(half)), "data: " + JSON.stringify({ choices: [{ delta: {}, finish_reason: "stop" }] }) + "\n\n", usage ? "data: " + JSON.stringify({ choices: [], usage }) + "\n\n" : "", "data: [DONE]\n\n"].filter(Boolean);
};

test("built: a streamed step is read as it comes, its words are shown while it is written and it is carried out as usual", async () => {
  const text = JSON.stringify({ say: "I will check the file.", actions: [{ type: "files_list" }], done: false });
  const t = setup([{ sse: sseOf(text, { prompt_tokens: 100, completion_tokens: 40 }) }, { say: "Done.", actions: [], done: true }], { stream: true, answer: () => "Allow for this session" });
  await t.done(t.runner.start("look"));
  assert.equal(t.log.requests[0].stream, true);
  assert.ok(t.log.said.includes("genie: I will check the file."), t.log.said.join(" | "));
  assert.equal(t.results(1).step_results[0].type, "files_list");
  assert.equal(t.messages.querySelectorAll(".cw-agent__live").length, 0, "the live words were left in the card");
  assert.equal(t.header(), "Finished");
});

test("built: a streamed answer that is empty, or only an error, ends the step with words and no live line is left", async () => {
  const t = setup([{ sse: ["data: " + JSON.stringify({ error: { message: "the host is busy" } }) + "\n\n", "data: [DONE]\n\n"] }], { stream: true });
  await t.done(t.runner.start("look"));
  assert.deepEqual(t.log.notes, ["the host is busy"]);
  assert.equal(t.messages.querySelectorAll(".cw-agent__live").length, 0);
});

test("built: without the switch the page does not ask for a stream", async () => {
  const t = setup([{ say: "ok", actions: [], done: true }]);
  await t.done(t.runner.start("look"));
  assert.equal(t.log.requests[0].stream, undefined);
});

test("built: a step answered as a call of the function is read like JSON written as text", async () => {
  const t = setup([{ toolCall: { say: "Via a call.", actions: [{ type: "files_list" }], done: false } }, { say: "Done.", actions: [], done: true }], { answer: () => "Allow for this session" });
  await t.done(t.runner.start("look"));
  assert.ok(t.log.said.includes("genie: Via a call."), t.log.said.join(" | "));
  assert.equal(t.results(1).step_results[0].type, "files_list");
  assert.deepEqual(t.log.notes, []);
});
