// The Genie agent and chat as a visitor sees them (docs/lld/19-genie-agent.md): the plan, reading without asking, editing one
// part with the real review, the check and what changed, Carry on, the project notes, Undo this task, the credits line,
// the one-click requests, Regenerate, Apply as diff, and a streamed answer. The model and the file server are made up at the
// network edge (what they do is tested in Go and in the widget tests); the page, the widget, the editor and the review are real.
//
// GENIE_LOCAL_ASSETS=1 serves the widget and the page script from this checkout's builds (src/resources/chat-widget/dist and
// src/js/dist) instead of the ones inside the server under test, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const { waitForTerminalReady } = require("./helpers");

const root = path.join(__dirname, "..", "..", "src");

async function localAssets(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  const serve = (file) => (route) => route.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, file)) });
  await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), serve("resources/chat-widget/dist/index.umd.js"));
  await page.route((u) => u.pathname.endsWith("/js/chat-widget-agent.js"), serve("resources/chat-widget/dist/agent.js"));
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), serve("js/dist/scribbler.js"));
  await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (route) => route.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(root, "resources/css/ui-refresh.css")) }));
  await page.route((u) => u.pathname.endsWith("/js/model-choice.js"), serve("resources/js/model-choice.js"));
}

const step = (o) => JSON.stringify(o);

// A page with a signed-in user, a model that answers from `script` (each entry is the JSON of a step, a string for a chat answer,
// {status, error} for a refusal, or {sse: [...]} for a stream), and a workspace of `files`.
async function openAgent(page, script, opts = {}) {
  await localAssets(page);
  const seen = { requests: [], files: [], outcomes: [] };
  const usage = opts.usage || { left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 } };
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify(opts.usageFor ? opts.usageFor(route.request().url()) : usage) })
  );
  await page.route((u) => u.pathname.endsWith("/chat/agent-outcome"), (route) => {
    seen.outcomes.push(JSON.parse(route.request().postData() || "{}"));
    route.fulfill({ status: 204 });
  });
  await page.route((u) => u.pathname.endsWith("/agent_files"), (route) => {
    const req = JSON.parse(route.request().postData() || "{}");
    seen.files.push(req);
    const r = (opts.files || (() => ({ status: 404 })))(req);
    route.fulfill({ status: r.status || 200, contentType: "application/json", body: r.status && r.status !== 200 ? r.message || "no" : JSON.stringify(r.body || {}) });
  });
  await page.route((u) => u.pathname.endsWith("/chat/completions"), (route) => {
    const body = JSON.parse(route.request().postData() || "{}");
    seen.requests.push(body);
    const next = script.shift();
    if (next === undefined) return route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { message: "the script is over" } }) });
    if (next.status) return route.fulfill({ status: next.status, contentType: "application/json", body: JSON.stringify({ error: { message: next.error, code: next.code } }) });
    const headers = { "X-OpenREPL-Agent-Task": "task-1", "X-OpenREPL-Agent-Step": seen.requests.length + "/8", "Access-Control-Expose-Headers": "X-OpenREPL-Agent-Task, X-OpenREPL-Agent-Step, X-OpenREPL-Usage" };
    if (next.sse) return route.fulfill({ status: 200, headers: { ...headers, "Content-Type": "text/event-stream" }, body: next.sse.join("") });
    const content = typeof next === "string" ? next : step(next);
    route.fulfill({ status: 200, headers, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 1000, completion_tokens: 200 } }) });
  });
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && window.FileBrowser && window.FileBrowser.ready() && window.GenieReview && window.GenieAgent && window.editor && window.editor.env.editor);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  // the starter code of the language arrives a moment after the terminal: wait for it, so that it does not replace what a test puts in the editor
  await page.waitForFunction(() => window.editor.env.editor.getValue().length > 0);
  await page.waitForTimeout(800);
  await page.evaluate((streams) => {
    window.site_settings = Object.assign(window.site_settings || {}, { agentEnabled: true, genieStream: !!streams });
    document.body.classList.add("is-signed-in");
    window.CompileandRun = () => {};
  }, !!opts.stream);
  await page.evaluate(() => window.ChatWidget.open());
  return seen;
}

const setCode = (page, t) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), t);
const code = (page) => page.evaluate(() => window.editor.env.editor.getValue());
const agentMode = async (page) => {
  const btn = page.locator('#chat-widget__mode button[data-mode="agent"]');
  await expect(btn).toBeVisible();
  await btn.click();
};
const ask = async (page, text) => {
  await page.locator("#chat-widget__input").fill(text);
  await page.locator("#chat-widget__submit").click();
};
const allowAll = async (page) => {
  // the questions of the card are answered as they come: allow for the session
  await page.evaluate(() => {
    window.__answerer = setInterval(() => {
      document.querySelectorAll(".cw-agent__ask button").forEach((b) => {
        if (/Allow for this session|Allow once/.test(b.textContent)) b.click();
      });
    }, 100);
  });
};
const accept = async (page) => {
  await page.evaluate(() => {
    window.__acceptor = setInterval(() => {
      const b = Array.from(document.querySelectorAll(".genie-review__head button")).find((x) => /Accept all/.test(x.textContent));
      if (b) b.click();
    }, 100);
  });
};

test("a task shows its plan, reads without asking, edits one part with the review, checks it and says what changed", async ({ page }) => {
  const seen = await openAgent(
    page,
    [
      { say: "I will look first.", plan: ["Find the function", "Change the result", "Run it"], actions: [{ type: "files_search", query: "total" }, { type: "files_read", path: "calc.py", start_line: 1, end_line: 4 }], done: false },
      { say: "Changing it.", plan_done: [1], actions: [{ type: "editor_replace", old: "return a + b", new: "return a + b + 1" }], done: false },
      { say: "Running.", plan_done: [1, 2], actions: [{ type: "run" }], done: false },
      { say: "Done: it adds one more.", plan_done: [1, 2, 3], actions: [], done: true },
    ],
    {
      files: (req) =>
        req.path === "GENIE.md"
          ? { status: 404 }
          : req.op === "search"
          ? { body: { matches: [{ file: "calc.py", line: 1, text: "def total(a, b):" }], files_scanned: 3 } }
          : { body: { path: "calc.py", start_line: 1, end_line: 2, total_lines: 2, text: "    1  def total(a, b):\n    2      return a + b\n" } },
    }
  );
  await setCode(page, "def total(a, b):\n    return a + b\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await ask(page, "make total add one more");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  // the plan, all ticked
  const plan = page.locator(".cw-agent__plan-list .cw-agent__step");
  await expect(plan).toHaveCount(3);
  await expect(plan.locator(".cw-agent__mark")).toHaveText(["✓", "✓", "✓"]);
  // reading asked nothing, and its result is on the line
  await expect(page.locator(".cw-agent__step", { hasText: 'Searching the files for "total"' })).toContainText("1 match in 1 file");
  expect(seen.files.map((f) => f.op)).toEqual(["read", "search", "read"]); // GENIE.md first
  // the change is the one part
  expect(await code(page)).toBe("def total(a, b):\n    return a + b + 1\n");
  // the end of the task says what was checked and what changed
  const chips = page.locator(".cw-agent__chip");
  await expect(chips.filter({ hasText: "Checked" })).toContainText("Run");
  await expect(chips.filter({ hasText: "Changed" })).toContainText("+1");
  await expect(page.locator(".cw-agent__meta")).toContainText("4 steps");
  expect(seen.outcomes.map((o) => [o.outcome, o.changed, o.checked])).toEqual([["done", true, true]]);
});

test("Undo this task puts the file back, and does not touch what was typed after the task", async ({ page }) => {
  await openAgent(
    page,
    [
      { say: "Changing.", actions: [{ type: "editor_write", text: "x = 2\n" }], done: false },
      { say: "Run.", actions: [{ type: "run" }], done: true },
    ]
  );
  await setCode(page, "x = 1\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await ask(page, "set x to two");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  expect(await code(page)).toBe("x = 2\n");
  await page.locator(".cw-agent__undo").click();
  await expect(page.locator(".cw-agent__undo")).toHaveText("Undone");
  expect(await code(page)).toBe("x = 1\n");
});

test("the Review bar above the box lists what the task changed; Keep and Undo make a row vanish and the bar goes with the last", async ({ page }) => {
  await openAgent(
    page,
    [
      { say: "Changing.", actions: [{ type: "editor_write", text: "x = 2\ny = 5\n" }], done: false },
      { say: "Run.", actions: [{ type: "run" }], done: true },
    ]
  );
  await setCode(page, "x = 1\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await expect(page.locator("#chat-widget__review")).toBeHidden();
  await ask(page, "set x to two and add y");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  const bar = page.locator("#chat-widget__review");
  await expect(bar).toBeVisible();
  await expect(bar.locator(".cw-reviewbar__title")).toHaveText("Genie changed 1 file");
  const rows = bar.locator(".cw-reviewbar__row");
  await expect(rows).toHaveCount(1);
  await expect(rows.locator(".cw-reviewbar__count")).toHaveText("+2 \u22121");
  // it is above the chat box, and the task card has no list
  const box = await bar.boundingBox();
  const input = await page.locator("#chat-widget__input").boundingBox();
  expect(box.y + box.height).toBeLessThanOrEqual(input.y + 1);
  await expect(page.locator(".cw-agent .cw-reviewbar__row")).toHaveCount(0);
  // Keep: the row is gone, the bar is gone, the text stays, and the chat says what was done
  await rows.getByRole("button", { name: "Keep" }).click();
  await expect(bar).toBeHidden();
  expect(await code(page)).toBe("x = 2\ny = 5\n");
  await expect(page.locator(".chat-widget__message--system", { hasText: "Reviewed 1: 1 kept." })).toBeVisible();
});

test("Undo in the Review bar puts the text back and the row vanishes; Undo all asks first", async ({ page }) => {
  await openAgent(
    page,
    [
      { say: "Changing.", actions: [{ type: "editor_write", text: "x = 2\n" }], done: false },
      { say: "Run.", actions: [{ type: "run" }], done: true },
    ]
  );
  await setCode(page, "x = 1\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await ask(page, "set x to two");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  const bar = page.locator("#chat-widget__review");
  await bar.getByRole("button", { name: "Undo all" }).click();
  const card = page.locator(".cw-agent__ask", { hasText: "Undo all changes of this task?" });
  await expect(card).toBeVisible();
  expect(await code(page)).toBe("x = 2\n"); // nothing before the user says yes
  await card.getByRole("button", { name: "Cancel" }).click();
  expect(await code(page)).toBe("x = 2\n");
  await bar.locator(".cw-reviewbar__row").getByRole("button", { name: "Undo" }).click();
  await expect(bar).toBeHidden();
  expect(await code(page)).toBe("x = 1\n");
});

test("a row of the Review bar shows the file in the editor itself: green band for what came, red lines for what went, a bar with Keep and Undo", async ({ page }) => {
  await openAgent(
    page,
    [
      { say: "Changing.", actions: [{ type: "editor_write", text: "a\nB\nc\nd\n" }], done: false },
      { say: "Run.", actions: [{ type: "run" }], done: true },
    ]
  );
  await setCode(page, "a\nb\nc\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await ask(page, "change b to B and add d");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  await page.locator("#chat-widget__review .cw-reviewbar__path").click();
  const bar = page.locator(".genie-changes__bar");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("Genie changed this file");
  await expect(bar.locator(".genie-changes__count")).toContainText("+2");
  await expect(bar.locator(".genie-changes__count")).toContainText("\u22121");
  // the added lines are marked in the editor's own layer, the removed line is a widget that cannot be edited
  await expect(page.locator(".ace_editor .genie-changes__add")).toHaveCount(2);
  await expect(page.locator(".ace_editor .genie-changes__del .genie-changes__line")).toHaveText(["b"]);
  // the bands follow what is typed: another line added
  await page.evaluate(() => { const ed = window.editor.env.editor; ed.session.insert({ row: 0, column: 0 }, "top\n"); });
  await expect(page.locator(".ace_editor .genie-changes__add")).toHaveCount(3);
  // Keep there settles the row in the chat's bar and takes the editor's bar away
  await bar.getByRole("button", { name: "Keep" }).click();
  await expect(bar).toBeHidden();
  await expect(page.locator(".ace_editor .genie-changes__add")).toHaveCount(0);
  await expect(page.locator("#chat-widget__review")).toBeHidden();
});

test("the lines Genie took out are shown where they were, also at the top and at the end of the file", async ({ page }) => {
  await openAgent(page, [{ say: "Trimming.", actions: [{ type: "editor_write", text: "b\nc\n" }], done: false }, { say: "Run.", actions: [{ type: "run" }], done: true }]);
  await setCode(page, "a\nb\nc\nz\n");
  await agentMode(page);
  await allowAll(page);
  await accept(page);
  await ask(page, "drop the first and the last line");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  await page.locator("#chat-widget__review .cw-reviewbar__path").click();
  await expect(page.locator(".genie-changes__bar")).toBeVisible();
  const dels = page.locator(".ace_editor .genie-changes__del .genie-changes__line");
  await expect(dels).toHaveText(["a", "z"]);
  await page.waitForTimeout(500);
  // each removed line sits at the right height: "a" above "b", "z" below "c"
  const tops = await page.evaluate(() => {
    const y = (e) => Math.round(e.getBoundingClientRect().top);
    const lines = [...document.querySelectorAll(".ace_text-layer .ace_line")].map((e) => [e.textContent, y(e)]);
    const del = [...document.querySelectorAll(".ace_editor .genie-changes__del .genie-changes__line")].map((e) => [e.textContent, y(e)]);
    return { lines, del };
  });
  const top = (arr, t) => arr.find((x) => x[0] === t)[1];
  expect(top(tops.del, "a")).toBeLessThan(top(tops.lines, "b"));
  expect(top(tops.del, "z")).toBeGreaterThan(top(tops.lines, "c"));
});

test("the end of a stretch asks whether to carry on, and carrying on goes on with the same task", async ({ page }) => {
  const loop = { say: "Working.", actions: [{ type: "files_list" }], done: false };
  const seen = await openAgent(page, [loop, loop, loop, { say: "All done.", actions: [], done: true }], {
    usage: { left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 2 } },
  });
  await page.evaluate(() => { window.site_settings.agentMaxSteps = 2; });
  await agentMode(page);
  await allowAll(page);
  await ask(page, "keep going");
  const card = page.locator(".cw-agent__ask", { hasText: "Paused at the step limit" });
  await expect(card).toBeVisible({ timeout: 30000 });
  await expect(page.locator(".cw-agent__count")).toContainText("paused");
  await card.getByRole("button", { name: "Carry on" }).click();
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  expect(seen.requests.filter((r) => r.agent_carry === true).length).toBe(1);
  expect(new Set(seen.requests.map((r) => r.agent_task).filter(Boolean))).toEqual(new Set(["task-1"]));
});

test("GENIE.md is read at the start, shown as a step and sent with the task", async ({ page }) => {
  const seen = await openAgent(page, [{ say: "Looking.", actions: [{ type: "files_list", path: "" }], done: false }, { say: "Noted.", actions: [], done: true }], {
    files: (req) => (req.path === "GENIE.md" ? { body: { text: "    1  Run it with python3 app.py\n    2  Never touch data/\n", total_lines: 2 } } : { status: 404 }),
  });
  await agentMode(page);
  await ask(page, "explain the project");
  await expect(page.locator(".cw-agent__step", { hasText: "Read GENIE.md, the project notes" })).toBeVisible({ timeout: 30000 });
  const first = JSON.stringify(seen.requests[0].messages[0]);
  expect(first).toContain("Notes of this project, from its GENIE.md");
  expect(first).toContain("Run it with python3 app.py");
});

test("a task over its credit budget asks, and the usage line leads with credits", async ({ page }) => {
  const usage = { left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 }, credits: { plan: "Free", of: 500, used: 88, left: 412, resets: "2026-12-01", taskBudget: 40 } };
  const go = { say: "Working.", actions: [{ type: "files_list" }], done: false };
  const seen = await openAgent(page, [go, { status: 429, code: "agent_budget", error: "This task has used 42 of the 40 credits one task may use. The next step would pass that. Carry on to use up to 40 more, or stop here and keep what is done." }, { say: "Done.", actions: [], done: true }], {
    usage,
    usageFor: (url) => (url.includes("task=") ? { ...usage, task: { credits: 42, steps: 2 } } : usage),
  });
  await expect(page.locator("#chat-widget__usage-text")).toHaveText("412 of 500 credits left");
  await page.locator("#chat-widget__usage-line").click();
  await expect(page.locator("#chat-widget__usage-more")).toContainText("Most one task may use");
  await expect(page.locator("#chat-widget__usage-more")).toContainText("about 40 credits");
  await agentMode(page);
  await allowAll(page);
  await ask(page, "work");
  const card = page.locator(".cw-agent__ask", { hasText: "This task has used its budget" });
  await expect(card).toBeVisible({ timeout: 30000 });
  await card.getByRole("button", { name: "Carry on" }).click();
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  expect(seen.requests[2].agent_carry).toBe(true);
  await expect(page.locator(".cw-agent__meta")).toContainText("42 credits");
});

test("a used-up month shows no credits left and the box says so", async ({ page }) => {
  await openAgent(page, [], { usage: { left: 18, cap: 20, perMinute: 0.33, signedIn: true, credits: { plan: "Free", of: 500, used: 500, left: 0, resets: "2026-12-01" } } });
  await expect(page.locator("#chat-widget__usage-text")).toHaveText("No credits left · back on 1 Dec");
  await expect(page.locator("#chat-widget__input")).toHaveAttribute("placeholder", "No credits left this month");
});

test("while credits are not on, the usage line is as it was", async ({ page }) => {
  await openAgent(page, []);
  await agentMode(page);
  await expect(page.locator("#chat-widget__usage-text")).toContainText("tasks left this hour");
  await page.locator("#chat-widget__usage-line").click();
  await expect(page.locator("#chat-widget__usage-more")).not.toContainText("Credits");
});

test("in Chat, the buttons for the selection and the last error appear when they apply, and send a request", async ({ page }) => {
  const seen = await openAgent(page, ["It reverses a list."]);
  await setCode(page, "def rev(xs):\n    return xs[::-1]\n");
  const explain = page.locator('#chat-widget__quick button[data-quick="selection"]');
  await expect(explain).toBeHidden(); // nothing is selected
  await page.evaluate(() => window.editor.env.editor.selectAll());
  await expect(explain).toBeVisible({ timeout: 5000 });
  await explain.click();
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("It reverses a list.");
  const sent = JSON.stringify(seen.requests[0].messages);
  expect(sent).toContain("Explain this");
  expect(sent).toContain("xs[::-1]");
});

test("an answer can be asked for again, and the message edited and sent again", async ({ page }) => {
  const seen = await openAgent(page, ["First answer.", "Second answer."]);
  const before = await page.locator(".chat-widget__message--system").count(); // Genie's own greeting, if there is one
  await ask(page, "what is a list");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("First answer.");
  await page.locator(".cw-turn button", { hasText: "Regenerate" }).click();
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Second answer.");
  await expect(page.locator(".chat-widget__message--system")).toHaveCount(before + 1);
  expect(seen.requests.length).toBe(2);
  // the answer that was asked for again is not in the conversation of the second request
  expect(JSON.stringify(seen.requests[1].messages)).not.toContain("First answer.");
  await page.locator(".cw-turn button", { hasText: "Edit my message" }).click();
  await expect(page.locator("#chat-widget__input")).toHaveValue("what is a list");
  await expect(page.locator(".chat-widget__message--user")).toHaveCount(0);
});

test("a long code block is not sent again in full, and the editor is not sent twice", async ({ page }) => {
  const long = "```python\n" + Array.from({ length: 80 }, (_, i) => "line_" + i + " = " + i).join("\n") + "\n```";
  const seen = await openAgent(page, ["Here:\n" + long, "ok", "ok again", "last"]);
  for (const q of ["one", "two", "three", "four"]) {
    await ask(page, q);
    await expect(page.locator("#chat-widget__submit")).toBeEnabled({ timeout: 15000 });
  }
  // the fourth request: the first answer is older than the last two
  const third = JSON.stringify(seen.requests[3].messages);
  expect(third).toContain("more lines left out of this older answer");
  const copies = (third.match(/Openrepl IDE real-time context|Openrepl IDE\/EditorCodeContent/g) || []).length;
  expect(copies).toBeLessThanOrEqual(2);
});

test("Apply as diff offers only the one function that the code replaces", async ({ page }) => {
  await openAgent(page, ["Use this:\n```python\ndef area(r):\n    return 3.14 * r * r\n```"]);
  await setCode(page, "import math\n\ndef area(r):\n    return r * r\n\ndef main():\n    print(area(2))\n");
  await ask(page, "better area");
  const apply = page.locator(".chat-widget__code-btn", { hasText: "Apply as diff" });
  await expect(apply).toBeVisible({ timeout: 15000 });
  await apply.click();
  await expect(page.locator(".genie-review")).toContainText("area");
  await page.locator(".genie-review__head button", { hasText: "Accept all" }).click();
  expect(await code(page)).toBe("import math\n\ndef area(r):\n    return 3.14 * r * r\n\ndef main():\n    print(area(2))\n");
});

test("a streamed chat answer grows in one message and ends whole", async ({ page }) => {
  const ev = (c) => "data: " + JSON.stringify({ choices: [{ delta: { content: c } }] }) + "\n\n";
  await openAgent(page, [{ sse: [ev("Streaming "), ev("works."), "data: " + JSON.stringify({ choices: [], usage: { prompt_tokens: 10, completion_tokens: 3 } }) + "\n\n", "data: [DONE]\n\n"] }], { stream: true });
  const before = await page.locator(".chat-widget__message--system").count();
  await ask(page, "say it");
  await expect(page.locator(".chat-widget__message--system")).toHaveCount(before + 1);
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Streaming works.");
  await expect(page.locator(".chat-widget__message-meta").first()).toContainText("13 tokens");
});

test("a streamed agent step shows its words and is carried out", async ({ page }) => {
  const text = JSON.stringify({ say: "I will list the files.", actions: [{ type: "files_list" }], done: false });
  const ev = (c) => "data: " + JSON.stringify({ choices: [{ delta: { content: c } }] }) + "\n\n";
  await openAgent(page, [{ sse: [ev(text.slice(0, 20)), ev(text.slice(20)), "data: [DONE]\n\n"] }, { say: "Listed.", actions: [], done: true }], { stream: true });
  await agentMode(page);
  await allowAll(page);
  await ask(page, "list");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  await expect(page.locator(".cw-agent__live")).toHaveCount(0);
  await expect(page.locator(".chat-widget__message--system", { hasText: "I will list the files." })).toBeVisible();
});

test("a viewer of a shared session has no agent of the owner's, and nothing here is the owner's only", async ({ page }) => {
  await localAssets(page);
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: true }) }));
  await page.goto("/#-Mabcdefghijklmnop12");
  await page.waitForFunction(() => window.ChatWidget);
  await page.evaluate(() => {
    window.site_settings = Object.assign(window.site_settings || {}, { agentEnabled: true });
    document.body.classList.add("is-signed-in");
    window.ChatWidget.open();
  });
  // a viewer's page has no Agent switch (the owner's workspace is not theirs), and the one-click buttons are not offered
  await expect(page.locator("#chat-widget__mode")).toBeHidden();
  await expect(page.locator("#chat-widget__quick")).toBeHidden();
});

test("an answer to a job offers Do this with Agent, and it starts the same words as a task", async ({ page }) => {
  const seen = await openAgent(page, ["Here is one way:\n```python\nprint('hi')\n```", { say: "On it.", actions: [{ type: "files_search", query: "hi" }], done: false }, { say: "Done.", actions: [], done: true }]);
  await ask(page, "create a script that prints hi and run it");
  const hand = page.locator(".cw-turn button", { hasText: "Do this with Agent" });
  await expect(hand).toBeVisible({ timeout: 15000 });
  await hand.click();
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 30000 });
  expect(seen.requests[1].context).toBe("agent");
  expect(JSON.stringify(seen.requests[1].messages[0])).toContain("create a script that prints hi and run it");
});

test("an empty chat offers starter questions; Agent mode with a small model says so and offers the admin's model", async ({ page }) => {
  await openAgent(page, []);
  const chips = page.locator("#chat-widget__starters");
  await expect(chips).toBeVisible();
  await expect(chips.getByRole("button", { name: "Explain the error in my terminal" })).toBeVisible();
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await expect(chips.getByRole("button", { name: "Run this" })).toBeVisible();
  await chips.getByRole("button", { name: "Run this" }).click();
  await expect(page.locator("#chat-widget__input")).toHaveValue("Run this");
  // a small model
  const small = await page.evaluate(() => {
    const m = window.ModelChoice.models.filter((x) => /qwen3-30b|gemma-4-26b|mini|flash/.test(x.id))[0];
    if (!m) return "";
    window.site_settings.genieAgentModel = "gpt-6-luna";
    window.ModelChoice.set(m.id);
    return m.id;
  });
  test.skip(!small, "no small model in the list of this server");
  const hint = page.locator("#chat-widget__modehint");
  await expect(hint).toBeVisible();
  await expect(hint).toContainText("Agent works best with a stronger one");
  await hint.getByRole("button", { name: /Switch to/ }).click();
  await expect(hint).toBeHidden();
  expect(await page.evaluate(() => window.ModelChoice.get().model)).toBe("gpt-6-luna");
});

test("a long chat is summarized by the model in use, and the next question is sent the summary and the last turns", async ({ page }) => {
  const filler = (n) => "Answer " + n + ". " + "word ".repeat(520);
  const summary = "The user is learning lists in Python; they asked four questions about slicing and reversing.";
  const seen = await openAgent(page, [filler(1), filler(2), filler(3), filler(4), summary, "Fifth answer."]);
  await page.evaluate(() => { window.site_settings.genieSummaryTokens = 1000; });
  for (const q of ["one", "two", "three", "four"]) {
    await ask(page, q);
    await expect(page.locator("#chat-widget__submit")).toBeEnabled({ timeout: 15000 });
  }
  expect(seen.requests.length).toBe(4);
  await ask(page, "five");
  await expect(page.locator(".chat-widget__message--system", { hasText: "Fifth answer." })).toBeVisible({ timeout: 20000 });
  // one more request: the summary, made with the model in use and no site context
  expect(seen.requests.length).toBe(6);
  const s = seen.requests[4];
  expect(JSON.stringify(s.messages)).toContain("Write a summary of the chat so far");
  expect(s.context).toBeUndefined();
  // the question after it carries the summary, the last turns, and not the answers that went into it
  const q5 = JSON.stringify(seen.requests[5].messages);
  expect(q5).toContain("Summary of the earlier chat: " + summary);
  expect(q5).toContain("five");
  expect(q5).not.toContain("Answer 1.");
});

test("in a narrow panel the Auto badge is one line and the name Genie is not cut", async ({ page }) => {
  await openAgent(page, []);
  const m = await page.evaluate(() => {
    const box = document.getElementById("chat-widget__container");
    box.style.width = "340px";
    const holder = document.getElementById("chat-widget__auto");
    holder.innerHTML = "";
    const b = document.createElement("button");
    b.className = "cw-auto";
    b.textContent = "Auto: 1 of 7 ▾";
    holder.appendChild(b);
    const ctx = document.getElementById("chat-widget__context");
    if (ctx) ctx.textContent = "Reads script.js and the terminal";
    const t = document.getElementById("chat-widget__title_text");
    return { badgeH: b.getBoundingClientRect().height, cut: t.scrollWidth > t.clientWidth, title: t.textContent };
  });
  expect(m.badgeH).toBeLessThan(30);
  expect(m.cut).toBe(false);
});

test("Genie is told the path of the open file in the workspace, not only its name", async ({ page }) => {
  const seen = await openAgent(page, ["Noted."]);
  // the Files panel says the open file is shop/index.html; the tab and the chip say index.html
  await page.evaluate(() => {
    const fb = window.FileBrowser;
    window.FileBrowser = Object.assign(Object.create(fb), { current: () => "shop/index.html", ready: () => true });
    document.getElementById("editor-filename").textContent = "index.html";
  });
  await ask(page, "what is this file");
  await expect(page.locator(".chat-widget__message--system", { hasText: "Noted." })).toBeVisible({ timeout: 20000 });
  const sent = JSON.stringify(seen.requests[0].messages);
  expect(sent).toContain("File: shop/index.html");
  expect(sent).not.toMatch(/File: index\.html/);
});

// A tall block at the top of the conversation (the oldest end), so that it scrolls and the newest lines can be out of view.
const makeScrollable = (page) =>
  page.evaluate(() => {
    const box = document.getElementById("chat-widget__messages_history");
    const tall = document.createElement("div");
    tall.className = "chat-widget__message";
    tall.style.minHeight = "1500px";
    tall.textContent = "older messages";
    box.appendChild(tall);
  });
const scrollUp = (page) =>
  page.evaluate(() => {
    const box = document.getElementById("chat-widget__messages_history");
    box.scrollTop = box.scrollTop < 0 || box.scrollHeight ? -(box.scrollHeight - box.clientHeight) : 0;
    box.dispatchEvent(new Event("scroll"));
  });

test("a down arrow leads to the last message, counts what came while reading, and does not move what is read", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await openAgent(page, ["First answer."]);
  await ask(page, "hello");
  await expect(page.locator(".chat-widget__message--system", { hasText: "First answer." })).toBeVisible({ timeout: 20000 });
  const jump = page.locator("#chat-widget__jump");
  await expect(jump).toBeHidden();
  await makeScrollable(page);
  await scrollUp(page);
  await expect(jump).toBeVisible();
  await expect(jump).toHaveText("↓");
  // it floats at the bottom of the conversation, in the middle, and not over the input or the usage line
  const place = await page.evaluate(() => {
    const j = document.getElementById("chat-widget__jump").getBoundingClientRect();
    const c = document.getElementById("chat-widget__messages_history").getBoundingClientRect();
    const f = document.getElementById("chat-widget__form").getBoundingClientRect();
    return { jumpBottom: j.bottom, convBottom: c.bottom, formTop: f.top, jumpMid: (j.left + j.right) / 2, convMid: (c.left + c.right) / 2 };
  });
  expect(place.jumpBottom).toBeLessThanOrEqual(place.convBottom);
  expect(place.jumpBottom).toBeLessThanOrEqual(place.formTop + 1);
  expect(Math.abs(place.jumpMid - place.convMid)).toBeLessThan(4);
  // a new message while reading further up: the arrow counts it, and what is on screen does not move
  const before = await page.evaluate(() => {
    const box = document.getElementById("chat-widget__messages_history");
    const first = Array.from(box.children).find((c) => c.getBoundingClientRect().bottom > box.getBoundingClientRect().top);
    window.__ref = first;
    return first.getBoundingClientRect().top;
  });
  await page.evaluate(() => {
    const box = document.getElementById("chat-widget__messages_history");
    const m = document.createElement("div");
    m.className = "chat-widget__message";
    m.style.minHeight = "120px";
    m.textContent = "a new one";
    box.prepend(m);
  });
  await expect(jump).toHaveText("↓ Latest · 1 new");
  const after = await page.evaluate(() => window.__ref.getBoundingClientRect().top);
  expect(Math.abs(after - before)).toBeLessThan(3);
  // one click: the last message, and the arrow goes
  await jump.click();
  await expect(jump).toBeHidden();
  expect(await page.evaluate(() => Math.abs(document.getElementById("chat-widget__messages_history").scrollTop))).toBeLessThan(2);
});

test("a task that waits shows a bar above the input while its card is out of view; the bar leads to the card and goes when it is answered", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  const seen = await openAgent(page, [{ say: "Looking.", actions: [{ type: "files_list" }], done: false }, { say: "Done.", actions: [], done: true }]);
  await agentMode(page);
  await ask(page, "what files are there");
  const card = page.locator(".cw-agent__ask");
  await expect(card).toBeVisible({ timeout: 30000 });
  const bar = page.locator("#chat-widget__wait");
  // the card is in view: no bar
  await expect(bar).toBeHidden();
  // scroll it out of view: the bar says what is asked, and which way the card is
  await makeScrollable(page);
  await scrollUp(page);
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("Waiting for you: allow Genie to use your files?");
  await expect(page.locator("#chat-widget__wait-label")).toHaveText("Show ↓");
  // a click brings the card into view, rings it and puts the focus on its first button
  await page.locator("#chat-widget__wait-go").click();
  await expect(card).toBeInViewport();
  await expect(card).toHaveClass(/cw-ring/);
  await expect(bar).toBeHidden();
  expect(await page.evaluate(() => document.activeElement && document.activeElement.textContent)).toMatch(/Allow once/);
  // answered: nothing is waiting
  await card.getByRole("button", { name: "Allow once" }).click();
  await expect(card).toHaveCount(0);
  await expect(bar).toBeHidden();
  expect(seen.requests.length).toBeGreaterThan(0);
});

test("a card of a task that is done folds to one line when the user writes again, and opens on a click with the thinking steps in one line", async ({ page }) => {
  await openAgent(page, [
    { say: "Looking.", actions: [{ type: "files_list" }], done: false },
    { say: "More.", actions: [{ type: "files_list" }], done: false },
    { say: "Done.", actions: [], done: true },
    { say: "Looking again.", actions: [{ type: "files_list" }], done: false },
    { say: "Done too.", actions: [], done: true },
  ]);
  await allowAll(page);
  await agentMode(page);
  await ask(page, "what files are there");
  await expect(page.locator(".cw-agent .cw-agent__sum")).toBeVisible({ timeout: 30000 });
  await page.evaluate(() => document.querySelector(".cw-agent").setAttribute("data-first", "1")); // the newest card is put on top: this one is tagged
  const first = page.locator('.cw-agent[data-first="1"]');
  // the newest card stays open until the user writes again
  await expect(first.locator(".cw-agent__fold")).toHaveCount(0);
  await expect(first.locator(".cw-agent__steps")).toBeVisible();
  await ask(page, "and once more");
  const fold = first.locator(".cw-agent__fold");
  await expect(fold).toBeVisible();
  await expect(fold).toContainText("Done");
  await expect(fold).toContainText("no file changed");
  await expect(fold).toContainText("3 steps");
  await expect(fold).toHaveAttribute("aria-expanded", "false");
  await expect(first.locator(".cw-agent__steps")).toBeHidden();
  await expect(first.locator(".cw-agent__sum")).toBeHidden();
  // a click opens it in place, with the thinking steps folded into one line
  await fold.click();
  await expect(fold).toHaveAttribute("aria-expanded", "true");
  await expect(first.locator(".cw-agent__steps")).toBeVisible();
  const think = first.locator(".cw-agent__think-btn");
  await expect(think).toHaveText(/\d thinking steps?/);
  await expect(first.locator('.cw-agent__steps li:not([hidden]) .cw-agent__what', { hasText: "Thought about the next step" })).toHaveCount(0);
  await think.click();
  await expect(first.locator('.cw-agent__steps li:not([hidden]) .cw-agent__what', { hasText: "Thought about the next step" }).first()).toBeVisible();
  // and closes again
  await fold.click();
  await expect(first.locator(".cw-agent__steps")).toBeHidden();
});

test("a card of a task that failed stays open when the user writes again", async ({ page }) => {
  await openAgent(page, ["this is not json at all", "still not json", { say: "Looking.", actions: [{ type: "files_list" }], done: false }, { say: "Done.", actions: [], done: true }]);
  await allowAll(page);
  await agentMode(page);
  await ask(page, "do something");
  await expect(page.locator(".cw-agent--ended").first()).toBeVisible({ timeout: 30000 });
  await expect(page.locator("#chat-widget__submit")).toBeEnabled({ timeout: 30000 });
  await ask(page, "try the next thing");
  await expect(page.locator(".cw-agent--ended")).toHaveCount(2, { timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(500);
  await expect(page.locator(".cw-agent__fold")).toHaveCount(0);
});
