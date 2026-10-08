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
  const log = { requests: [], said: [], notes: [], states: [], typed: [], events: [] };
  const messages = new El("div");
  const term = { text: opts.terminal || "$ ", object: {} };
  globalThis.fetch = (url, init) => {
    log.requests.push(JSON.parse(init.body));
    const next = script.shift();
    if (next === undefined) return Promise.resolve({ ok: false, status: 500, headers: { get: () => null }, json: async () => ({ error: { message: "the script is over" } }) });
    if (next && next.hang) return new Promise(() => {});
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
    ideContext: () => ({ role: "system", content: "ide context" }),
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

test("built: the steps of a task are counted, and the last one says so", async () => {
  // an action that is not in the protocol is dropped and reported, so the task is not done
  const again = { say: "", actions: [{ type: "shell", text: "ls" }], done: false };
  const t = setup([again, again, again, again, again], { max: 3 });
  await t.done(t.runner.start("go on for ever"));
  assert.equal(t.log.requests.length, 3, "the loop did not stop at the limit");
  assert.deepEqual(t.log.notes, ["I used all 3 steps of this task. You can ask me to carry on."]);
  assert.match(t.results(1).not_done[0], /"shell" is not an action/);
  assert.notEqual(t.header(), "Finished");
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
  assert.match(t.log.asked[1], /\[Allow once\/Deny\]/);
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
  const t = setup([{ say: "", actions: [{ type: "shell" }], done: false }, { say: "ok", actions: [], done: true }], { repl: "gointerpreter" });
  await t.done(t.runner.start("try a loop"));
  assert.deepEqual(t.log.requests.map((r) => r.repl), ["gointerpreter", "gointerpreter"]);
});
