// Resume and Review (docs/lld/19-genie-agent.md, section 12): a chat whose last checkpoint says a task was running shows the Resume bar when the page
// comes back; Resume starts a new task that is told it was interrupted, Discard lets it go, and a task writes a checkpoint as it goes. The workspace's
// chats are a stand-in that keeps the lines in memory (the real server is tested in Go); the page, the widget and the model's wire are real.
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
    const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true, chatHistory: true, chatHistoryGuests: true, chatHistoryTabs: ${opts.tabs || 8} });` +
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

const META = { t: "meta", id: "k3x9", created: "2026-11-19T05:00:00Z", context: "chat" };
const saved = (checkpoint) => [META, { t: "title", text: "Build the shop page" }, { t: "state", mode: "agent", model: "gpt-4o-mini" }, { t: "user", at: "10:00", ts: Date.now() - 600000, text: "Build the shop page" }, checkpoint];
const running = (extra = {}) => ({ t: "checkpoint", ts: Date.now() - 4 * 60000, text: "Build the shop page", steps: 7, changed: "shop/index.html, shop/style.css, shop/app.js", file: "shop/app.js", outcome: "running", ...extra });

async function withSavedChat(page, lines, agentScript = []) {
  const srv = chatServer();
  srv.chats.set("k3x9", lines);
  srv.open = ["k3x9"];
  const seen = await openPanel(page, srv, ["A chat answer."], { agent: agentScript });
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  return { srv, seen };
}

test("a task that was running when the page closed shows the Resume bar, with the task, its step and the files it had changed", async ({ page }) => {
  await withSavedChat(page, saved(running()));
  const bar = page.locator("#chat-widget__resume");
  await expect(bar).toBeVisible();
  await expect(bar).toContainText("A task was running when this page closed.");
  await expect(bar).toContainText("Build the shop page");
  await expect(bar).toContainText("step 7");
  await expect(bar).toContainText("3 files changed");
  await expect(bar).toContainText("4 minutes ago");
  await expect(page.getByRole("button", { name: "Resume this task" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Discard" })).toBeVisible();
});

test("a task that ended, or that is more than a day old, shows no bar", async ({ page }) => {
  await withSavedChat(page, saved(running({ outcome: "done" })));
  await expect(page.locator("#chat-widget__resume")).toBeHidden();
  const second = await page.context().newPage();
  await withSavedChat(second, saved(running({ ts: Date.now() - 26 * 3600 * 1000 })));
  await expect(second.locator("#chat-widget__resume")).toBeHidden();
});

test("Discard lets the task go: the bar goes, a stopped checkpoint is saved and a reload shows no bar", async ({ page }) => {
  const { srv } = await withSavedChat(page, saved(running()));
  await page.getByRole("button", { name: "Discard" }).click();
  await expect(page.locator("#chat-widget__resume")).toBeHidden();
  await expect.poll(() => srv.chats.get("k3x9").filter((l) => l.t === "checkpoint").pop().outcome).toBe("stopped");
});

test("Resume starts a new task that is told it was interrupted, asks for the groups it needs, and writes its checkpoints", async ({ page }) => {
  const { srv, seen } = await withSavedChat(page, saved(running()), [{ json: { say: "I looked at the files; nothing is left to do.", actions: [], done: true } }]);
  await page.getByRole("button", { name: "Resume this task" }).click();
  await expect(page.locator("#chat-widget__resume")).toBeHidden();
  await expect(page.locator(".chat-widget__message--user").first()).toContainText("Continue: Build the shop page"); // the newest is first in the list
  await expect(page.locator(".chat-widget__message--system", { hasText: "nothing is left to do" })).toBeVisible();
  const agent = seen.requests.filter((r) => r.context === "agent");
  expect(agent).toHaveLength(1);
  const first = agent[0].messages[0].content;
  expect(first).toContain("Continue: Build the shop page");
  expect(first).toContain("was interrupted before");
  expect(first).toContain("had changed: shop/index.html, shop/style.css, shop/app.js");
  expect(Array.isArray(agent[0].agent_groups)).toBe(true);
  expect(agent[0].agent_groups).toContain("editor");
  // the chat file: the user's words, and the newest checkpoint says the task ended
  await expect.poll(() => srv.chats.get("k3x9").filter((l) => l.t === "checkpoint").pop().outcome).toBe("done");
  expect(srv.chats.get("k3x9").some((l) => l.t === "user" && l.text === "Continue: Build the shop page")).toBe(true);
  await expect(page.locator("#chat-widget__resume")).toBeHidden();
});

test("a new task of this chat writes a checkpoint when it starts and when it ends", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, ["A chat answer."], { agent: [{ json: { say: "Hello.", actions: [], done: true } }] });
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await page.locator("#chat-widget__input").fill("say hello to me");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator(".chat-widget__message--system", { hasText: "Hello." })).toBeVisible();
  await expect.poll(() => [...srv.chats.values()][0].filter((l) => l.t === "checkpoint").length).toBeGreaterThan(0);
  const cps = [...srv.chats.values()][0].filter((l) => l.t === "checkpoint");
  expect(cps[cps.length - 1].outcome).toBe("done");
  expect(cps[cps.length - 1].text).toBe("say hello to me");
  expect(cps.every((c) => !("email" in c) && !("uid" in c))).toBe(true);
});
