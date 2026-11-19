// Auto model choice (chat-widget/src/model-router.ts, docs/lld/19-genie-agent.md section 18): an Auto entry at the top of the model list, a small model
// for plain questions in Chat and a strong one for what asks for a change, a note under the answer that says which and why, "Use a stronger one",
// and Auto off when the person picks a model. The model's wire is a stand-in that records the model of each request.
//
// GENIE_LOCAL_ASSETS=1 serves the widget from this checkout's build.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const { waitForTerminalReady } = require("./helpers");

const root = path.join(__dirname, "..", "..", "src");

async function localAssets(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "resources/chat-widget/dist/index.umd.js")) }));
  await page.route((u) => u.pathname.endsWith("/js/chat-widget-agent.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "resources/chat-widget/dist/agent.js")) }));
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "js/dist/scribbler.js")) }));
  await page.route((u) => u.pathname.endsWith("/js/model-choice.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "resources/js/model-choice.js")) }));
}

// A workspace that keeps chats in memory: the same calls as server/genie_chats.go
function chatServer(opts = {}) {
  const st = { chats: new Map(), open: [], off: false, calls: [], fail: false, refuse: null };
  const ok = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  const err = (r, status, code) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: { code, message: code } }) });
  st.handler = (route) => {
    const b = JSON.parse(route.request().postData() || "{}");
    st.calls.push(b);
    if (st.refuse) return err(route, 403, st.refuse);
    if (st.fail && ["append", "tabs"].includes(b.op)) return err(route, 507, "write");
    switch (b.op) {
      case "state": {
        const chats = [...st.chats.entries()].map(([id, lines]) => {
          const title = [...lines].reverse().find((l) => l.t === "title");
          return { id, title: title ? title.text : "", updated: Math.floor(Date.now() / 1000), messages: lines.filter((l) => l.t === "user" || l.t === "answer").length, size: JSON.stringify(lines).length, language: (lines.find((l) => l.t === "user") || {}).language || "" };
        });
        return ok(route, { enabled: true, off: st.off, open: st.open.filter((id) => st.chats.has(id)), chats, bytes: 100, limits: { chats: 50, tabs: opts.tabs || 8, bytes: 204800 } });
      }
      case "read":
        return st.chats.has(b.id) ? ok(route, { id: b.id, lines: st.chats.get(b.id) }) : err(route, 404, "no_chat");
      case "append": {
        const lines = st.chats.get(b.id) || [];
        st.chats.set(b.id, lines.concat(b.lines));
        return ok(route, { ok: true });
      }
      case "tabs":
        st.open = b.open;
        return ok(route, { ok: true, open: b.open });
      case "delete":
        st.chats.delete(b.id);
        st.open = st.open.filter((id) => id !== b.id);
        return ok(route, { ok: true });
      case "deleteAll":
        st.chats.clear();
        st.open = [];
        return ok(route, { ok: true });
      case "export":
        return ok(route, { markdown: "# " + b.id });
      default:
        return ok(route, { ok: true });
    }
  };
  return st;
}

// the model: answers from `script`, each after `delay` ms when given as {text, delay}
async function openPanel(page, srv, script, opts = {}) {
  await localAssets(page);
  const seen = { requests: [] };
  await page.route((u) => u.pathname.endsWith("/genie_chats"), (r) => srv.handler(r));
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 } }) }));
  await page.route((u) => u.pathname.endsWith("/chat/completions"), async (route) => {
    const body = JSON.parse(route.request().postData() || "{}");
    seen.requests.push(body);
    // an agent step and a chat message may be asked at the same time: each has its own script
    const next = body.context === "agent" && opts.agent ? opts.agent.shift() : script.shift();
    const o = typeof next === "string" ? { text: next } : next || { text: "(the script is over)" };
    if (o.delay) await new Promise((r) => setTimeout(r, o.delay));
    const headers = { "X-OpenREPL-Agent-Task": "task-1", "X-OpenREPL-Agent-Step": "1/8", "Access-Control-Expose-Headers": "X-OpenREPL-Agent-Task, X-OpenREPL-Agent-Step" };
    const content = o.json ? JSON.stringify(o.json) : o.text;
    route.fulfill({ status: 200, headers, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 5 } }) });
  });
  // the settings of the site, as the server would send them with chats on, and a signed-in user
  await page.route((u) => u.pathname.endsWith("/settings.js"), async (route) => {
    const res = await route.fetch();
    const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true, genieAuto: ${JSON.stringify(opts.auto === undefined ? { small: 'gpt-6-luna', strong: 'gpt-4o-mini', default: 'new' } : opts.auto)}, chatHistory: true, chatHistoryGuests: true, chatHistoryTabs: ${opts.tabs || 8} });` +
      `document.addEventListener("DOMContentLoaded", function () { document.body.classList.add("is-signed-in"); window.CompileandRun = function () {}; });`;
    route.fulfill({ response: res, body: (await res.text()) + extra });
  });
  await page.goto(opts.url || "/");
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env.editor);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.waitForFunction(() => window.editor.env.editor.getValue().length > 0);
  await page.waitForTimeout(500);
  return seen;
}


const open = async (page) => {
  await page.evaluate(() => window.ChatWidget.open());
  await expect(page.locator("#chat-widget__input")).toBeVisible();
};


async function setup(page, opts = {}) {
  const events = [];
  await page.route((u) => u.pathname.endsWith("/genie-event"), (r) => {
    events.push(JSON.parse(r.request().postData() || "{}"));
    r.fulfill({ status: 204 });
  });
  const seen = await openPanel(page, chatServer(), ["Small answer.", "Strong answer.", "Another."], { agent: opts.agent || [], auto: opts.auto });
  await open(page);
  return { seen, events };
}
const ask = async (page, text) => {
  await page.locator("#chat-widget__input").fill(text);
  await page.locator("#chat-widget__submit").click();
};
const models = (seen) => seen.requests.map((r) => r.model);

test("Auto is the first entry of the model list and the chip says Auto, for a person who never picked a model", async ({ page }) => {
  await setup(page);
  await expect(page.locator("#chat-widget__model-label")).toHaveText(/^Auto/);
  await page.locator("#chat-widget__model-btn").click();
  const first = page.locator("#chat-widget__models .cw-model").first();
  await expect(first).toContainText("Auto");
  await expect(first).toHaveAttribute("aria-checked", "true");
  await expect(first).toContainText("Small and quick for questions");
  await expect(page.locator('#chat-widget__models .cw-model[aria-checked="true"]')).toHaveCount(1);
});

test("a plain question goes to the small model, says so, and Use a stronger one asks again with the strong one and tells the Genie log", async ({ page }) => {
  const { seen, events } = await setup(page);
  await ask(page, "what does a closure do?");
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Small answer.", { timeout: 15000 });
  expect(models(seen)).toEqual(["gpt-6-luna"]);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Auto chose a small model: a plain question");
  const stronger = page.getByRole("button", { name: "Use a stronger one" });
  await expect(stronger).toBeVisible();
  await stronger.click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Strong answer.", { timeout: 15000 });
  expect(models(seen)).toEqual(["gpt-6-luna", "gpt-4o-mini"]);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Auto chose a strong model: you asked for a stronger one");
  await expect.poll(() => events.filter((e) => e.event === "auto_stronger").length).toBe(1);
  expect(events.find((e) => e.event === "auto_stronger").model).toBe("gpt-6-luna");
  // the answer of the strong model has no such link
  await expect(page.getByRole("button", { name: "Use a stronger one" })).toHaveCount(0);
});

test("a message that asks for a change goes to the strong model at once, with its reason", async ({ page }) => {
  const { seen } = await setup(page);
  await ask(page, "fix the login button on shop/index.html");
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Small answer.", { timeout: 15000 });
  expect(models(seen)).toEqual(["gpt-4o-mini"]);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Auto chose a strong model: it asks for a change");
  await expect(page.getByRole("button", { name: "Use a stronger one" })).toHaveCount(0);
});

test("picking a model turns Auto off and the next message goes to it; Auto can be picked again; Always use leaves Auto for the model that answered", async ({ page }) => {
  const { seen } = await setup(page);
  await page.locator("#chat-widget__model-btn").click();
  await page.locator('#chat-widget__models .cw-model[data-id="gpt-4o-mini"]').click();
  await expect(page.locator("#chat-widget__models .cw-model--auto")).toHaveAttribute("aria-checked", "false");
  await expect(page.locator("#chat-widget__model-label")).not.toHaveText(/^Auto/);
  await page.keyboard.press("Escape");
  await ask(page, "what is a closure");
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Small answer.", { timeout: 15000 });
  expect(models(seen)).toEqual(["gpt-4o-mini"]);
  await expect(page.locator("#chat-widget__messages_history")).not.toContainText("Auto chose");
  // Auto again
  await page.locator("#chat-widget__model-btn").click();
  await page.locator("#chat-widget__models .cw-model--auto").click();
  await expect(page.locator("#chat-widget__model-label")).toHaveText(/^Auto/);
  await page.keyboard.press("Escape");
  await ask(page, "what is a tuple");
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Strong answer.", { timeout: 15000 });
  expect(models(seen)[1]).toBe("gpt-6-luna");
  await page.getByRole("button", { name: /^Always use / }).click();
  await expect(page.locator("#chat-widget__model-label")).not.toHaveText(/^Auto/);
});

test("with Auto off for everybody (default none) the model list has Auto but it is not on until chosen; with no Auto set up there is no entry", async ({ page }) => {
  await setup(page, { auto: { small: "gpt-6-luna", strong: "gpt-4o-mini", default: "none" } });
  await expect(page.locator("#chat-widget__model-label")).not.toHaveText(/^Auto/);
  await page.locator("#chat-widget__model-btn").click();
  await expect(page.locator("#chat-widget__models .cw-model--auto")).toHaveAttribute("aria-checked", "false");
  const second = await page.context().newPage();
  await setup(second, { auto: null });
  await second.locator("#chat-widget__model-btn").click();
  await expect(second.locator("#chat-widget__models .cw-model--auto")).toHaveCount(0);
});

test("a task in Agent mode keeps the strong model, and the chat says so before it starts", async ({ page }) => {
  const { seen } = await setup(page, { agent: [{ json: { say: "Done.", actions: [], done: true } }] });
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await ask(page, "make a hello page");
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Auto chose GPT-4o mini for this task: Agent mode", { timeout: 15000 });
  await expect.poll(() => seen.requests.filter((r) => r.context === "agent").length).toBeGreaterThan(0);
  expect(seen.requests.filter((r) => r.context === "agent")[0].model).toBe("gpt-4o-mini");
});
