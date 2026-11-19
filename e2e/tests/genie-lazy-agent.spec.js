// The agent's code is a second file (chat-widget/src/lazy-agent.ts, docs/lld/19-genie-agent.md section 20): a visitor who only chats never fetches it, Agent mode
// fetches it once, and a load that fails is told in the chat. GENIE_LOCAL_ASSETS=1 serves the widget and the agent file from this checkout's build.
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



const watch = (page) => {
  const asked = [];
  page.on("request", (r) => { if (/chat-widget-agent\.js/.test(r.url())) asked.push(r.url()); });
  return asked;
};

test("a visitor who only chats never fetches the agent's code", async ({ page }) => {
  const asked = watch(page);
  await openPanel(page, chatServer(), ["A chat answer."]);
  await open(page);
  await page.locator("#chat-widget__input").fill("what is a closure?");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("A chat answer.", { timeout: 15000 });
  await page.waitForTimeout(800);
  expect(asked).toEqual([]);
  expect(await page.evaluate(() => !!window.GenieAgentCode)).toBe(false);
});

test("switching to Agent fetches it once, and a task then runs with it", async ({ page }) => {
  const asked = watch(page);
  const seen = await openPanel(page, chatServer(), ["x"], { agent: [{ json: { say: "Done.", actions: [], done: true } }] });
  await open(page);
  expect(asked).toEqual([]);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await expect.poll(() => page.evaluate(() => !!window.GenieAgentCode)).toBe(true);
  expect(asked.length).toBe(1);
  expect(asked[0]).toMatch(/\/js\/chat-widget-agent\.js$/);
  await page.locator("#chat-widget__input").fill("say hello");
  await page.locator("#chat-widget__submit").click();
  // the task asks the model for its step: the runner is the real one, made from the file that was fetched (a task that only answers draws no card)
  await expect.poll(() => seen.requests.filter((r) => r.context === "agent").length, { timeout: 30000 }).toBeGreaterThan(0);
  await page.locator('#chat-widget__mode button[data-mode="chat"]').click();
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  expect(asked.length).toBe(1);
});

test("when the agent's file cannot be fetched the chat says so, and the task can be sent again", async ({ page }) => {
  await openPanel(page, chatServer(), ["x"]);
  // (routes are tried newest first: this one comes after the one that serves the build)
  await page.route((u) => u.pathname.endsWith("/js/chat-widget-agent.js"), (r) => r.abort());
  await open(page);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await page.locator("#chat-widget__input").fill("make a page");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("The agent could not be loaded", { timeout: 15000 });
  await expect(page.locator("#chat-widget__submit")).toBeEnabled();
});
