// The chat tabs of the Genie panel (docs/lld/19-genie-agent.md, section 9): the strip, switching, a reload that brings the
// chats back, closing and reopening, renaming, deleting, the background answer, the one task at a time, the keys, the note when the
// editor is not what a chat was about, and what a viewer or a failing workspace sees. The server of the chats is a stand-in that
// keeps the lines in memory (what the real one does is tested in Go); the page, the widget and the editor are real.
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

const tabNames = (page) => page.locator("#chat-widget__tabs .cw-tab .cw-tab__name").allTextContents();
const ask = async (page, text) => {
  await page.locator("#chat-widget__input").fill(text);
  await page.locator("#chat-widget__submit").click();
};
const open = async (page) => {
  await page.evaluate(() => window.ChatWidget.open());
  await expect(page.locator("#chat-widget__input")).toBeVisible();
};
// the strip is shown once the chats are loaded: the page asks when it starts, so a reload of the page asks again
async function strip(page) {
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
}

// (the page asks for the chats when it starts, with the settings it was given)
async function startChats() {}

test("a chat is saved as it goes and a new tab is its own conversation", async ({ page }) => {
  const srv = chatServer();
  const seen = await openPanel(page, srv, ["It reverses a list.", "A tuple cannot be changed."]);
  await startChats(page);
  await open(page);
  await strip(page);
  await ask(page, "how do I reverse a list in python");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("It reverses a list.");
  await expect.poll(() => srv.chats.size).toBe(1);
  const lines = [...srv.chats.values()][0];
  expect(lines.map((l) => l.t)).toEqual(["meta", "title", "state", "user", "answer"]);
  expect(lines.find((l) => l.t === "title").text).toBe("how do I reverse a list in python");
  expect(lines.find((l) => l.t === "answer").tokens).toBe(15);
  expect(srv.open.length).toBe(1);
  expect(await tabNames(page)).toEqual(["how do I reverse a list in python"]);
  // + opens an empty chat, with nothing of the first one
  await page.locator("#chat-widget__newtab").click();
  await expect(page.locator(".chat-widget__message--user")).toHaveCount(0);
  await ask(page, "what is a tuple");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("A tuple cannot be changed.");
  expect(JSON.stringify(seen.requests[1].messages)).not.toContain("reverse a list");
  expect(await tabNames(page)).toEqual(["how do I reverse a list in python", "what is a tuple"]);
  // back to the first: its conversation, not the second's
  await page.locator("#chat-widget__tabs .cw-tab").first().click();
  await expect(page.locator(".chat-widget__message--user")).toHaveCount(1);
  await expect(page.locator(".chat-widget__message--user")).toContainText("reverse a list");
  await expect(page.locator(".chat-widget__message--system", { hasText: "A tuple" })).toHaveCount(0);
});

test("a reload brings the same tabs back, each with its conversation, and a half-typed message", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, ["First answer.", "Second answer."]);
  await startChats(page);
  await open(page);
  await strip(page);
  await ask(page, "first question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("First answer.");
  await page.locator("#chat-widget__newtab").click();
  await ask(page, "second question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Second answer.");
  await page.locator("#chat-widget__input").fill("half a thought");
  await page.waitForTimeout(600);
  await page.reload();
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env.editor);
  await open(page);
  await strip(page);
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(2);
  // the tab that was shown is shown, with its words and its draft
  await expect(page.locator(".chat-widget__message--user")).toContainText("second question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Second answer.");
  await expect(page.locator("#chat-widget__input")).toHaveValue("half a thought");
  await page.locator("#chat-widget__tabs .cw-tab").first().click();
  await expect(page.locator(".chat-widget__message--user")).toContainText("first question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("First answer.");
});

test("closing a tab keeps the chat, the list reopens it, rename and delete work", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, ["One.", "Two."]);
  await startChats(page);
  await open(page);
  await strip(page);
  await ask(page, "alpha question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("One.");
  await page.locator("#chat-widget__newtab").click();
  await ask(page, "beta question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Two.");
  // close the shown tab: the other one is shown, the chat is still in the workspace
  await page.locator("#chat-widget__tabs .cw-tab.is-active .cw-tab__x").click();
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(1);
  expect(srv.chats.size).toBe(2);
  await expect(page.locator(".chat-widget__message--user")).toContainText("alpha question");
  // the list shows both, the closed one can be reopened
  await page.locator("#chat-widget__allchats").click();
  await expect(page.locator(".cw-tabmenu__row")).toHaveCount(2);
  await page.locator(".cw-tabmenu__search").fill("beta");
  await expect(page.locator(".cw-tabmenu__row")).toHaveCount(1);
  await page.locator(".cw-tabmenu__row").click();
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(2);
  await expect(page.locator(".chat-widget__message--user")).toContainText("beta question");
  // rename: double-click, type, Enter
  await page.locator("#chat-widget__tabs .cw-tab.is-active").dblclick();
  await page.locator(".cw-tab__rename").fill("Betas");
  await page.keyboard.press("Enter");
  await expect(page.locator("#chat-widget__tabs .cw-tab.is-active .cw-tab__name")).toHaveText("Betas");
  await expect.poll(() => [...srv.chats.values()].some((l) => l.some((x) => x.t === "title" && x.text === "Betas"))).toBe(true);
  // delete from the menu: it asks, and then the file is gone
  await page.locator("#chat-widget__tabs .cw-tab.is-active").click({ button: "right" });
  await page.getByRole("menuitem", { name: /Delete chat/ }).click();
  await expect(page.locator("#chat-widget__toast")).toContainText("Delete “Betas” for good?");
  await page.locator("#chat-widget__toast button", { hasText: "Delete" }).first().click();
  await expect.poll(() => srv.chats.size).toBe(1);
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(1);
});

test("an answer that comes while another tab is shown goes to its own tab and leaves a dot", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, [{ text: "A slow answer.", delay: 1500 }, "A quick one."]);
  await startChats(page);
  await open(page);
  await strip(page);
  await ask(page, "slow question");
  await page.locator("#chat-widget__newtab").click(); // before the answer comes
  await ask(page, "quick question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("A quick one.");
  // the slow one lands in the first tab, which shows a dot, and not here
  const first = page.locator("#chat-widget__tabs .cw-tab").first();
  await expect(first.locator(".cw-tab__mark--dot")).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".chat-widget__message--system", { hasText: "A slow answer." })).toHaveCount(0);
  await first.click();
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("A slow answer.");
  await expect(first.locator(".cw-tab__mark--dot")).toHaveCount(0);
  const lines = [...srv.chats.values()].find((l) => l.some((x) => x.text === "slow question"));
  expect(lines.some((x) => x.t === "answer" && x.text === "A slow answer.")).toBe(true);
});

test("one task at a time: while a tab runs one, Agent is off in the others and Chat is not", async ({ page }) => {
  const srv = chatServer();
  const listing = { say: "Looking.", actions: [{ type: "files_list" }], done: false };
  await openPanel(page, srv, ["A chat answer."], { agent: [{ json: listing, delay: 2500 }, { json: { say: "Done.", actions: [], done: true } }] });
  await startChats(page);
  await open(page);
  await strip(page);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await expect(page.locator('#chat-widget__mode button[data-mode="agent"]')).toHaveAttribute("aria-pressed", "true");
  await page.evaluate(() => {
    setInterval(() => document.querySelectorAll(".cw-agent__ask button").forEach((b) => /Allow for this session|Allow once/.test(b.textContent) && b.click()), 100);
  });
  await ask(page, "list my files");
  await expect(page.locator("#chat-widget__tabs .cw-tab__mark--ring")).toBeVisible({ timeout: 15000 });
  await page.locator("#chat-widget__newtab").click();
  const agentBtn = page.locator('#chat-widget__mode button[data-mode="agent"]');
  await expect(agentBtn).toHaveAttribute("aria-disabled", "true");
  await expect(agentBtn).toHaveAttribute("title", /is running a task/);
  // chat works in this tab
  await page.locator('#chat-widget__mode button[data-mode="chat"]').click();
  await ask(page, "a plain question");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("A chat answer.", { timeout: 20000 });
  // the task finishes in its tab and leaves a dot
  await expect(page.locator("#chat-widget__tabs .cw-tab").first().locator(".cw-tab__mark--ring")).toHaveCount(0, { timeout: 30000 });
  await page.locator("#chat-widget__tabs .cw-tab").first().click();
  await expect(page.locator(".cw-agent--ended")).toBeVisible();
  await expect(agentBtn).not.toHaveAttribute("aria-disabled", "true");
  await expect.poll(() => [...srv.chats.values()].some((l) => l.some((x) => x.t === "task" && x.outcome === "done"))).toBe(true);
});

test("the keys: Alt+T opens a tab, Alt+1 and Alt+2 switch, Alt+W closes", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, ["One."]);
  await startChats(page);
  await open(page);
  await strip(page);
  await ask(page, "the first");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("One.");
  await page.locator("#chat-widget__input").focus();
  await page.keyboard.press("Alt+KeyT");
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(2);
  await expect(page.locator(".chat-widget__message--user")).toHaveCount(0);
  await page.keyboard.press("Alt+Digit1");
  await expect(page.locator(".chat-widget__message--user")).toContainText("the first");
  await page.keyboard.press("Alt+Digit2");
  await expect(page.locator(".chat-widget__message--user")).toHaveCount(0);
  await page.keyboard.press("Alt+KeyW");
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(1);
});

test("a tab opened for a file or language that is no longer the editor's says so", async ({ page }) => {
  const srv = chatServer();
  srv.chats.set("stale001", [
    { t: "meta", id: "stale001", context: "chat" },
    { t: "title", text: "about main.py" },
    { t: "user", ts: 1, text: "why", file: "elsewhere.py", language: "Rust" },
    { t: "answer", ts: 2, text: "because", model: "m" },
  ]);
  srv.open = ["stale001"];
  await openPanel(page, srv, []);
  await startChats(page);
  await open(page);
  await strip(page);
  await expect(page.locator(".cw-stale")).toContainText("This chat was about elsewhere.py (Rust).");
  await expect(page.locator(".cw-stale")).toContainText("The editor now has");
  // the note goes when the user writes
});

test("when the workspace cannot be written the strip says so, and the lines are saved when it is back", async ({ page }) => {
  const srv = chatServer();
  await openPanel(page, srv, ["Answer."]);
  await startChats(page);
  await open(page);
  await strip(page);
  srv.fail = true;
  await ask(page, "a question while it is away");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Answer.");
  await expect(page.locator(".cw-tabs__note")).toHaveText("not saved right now");
  expect(srv.chats.size).toBe(0);
  srv.fail = false;
  await page.evaluate(() => window.ChatWidget.retrySave());
  await expect.poll(() => srv.chats.size, { timeout: 20000 }).toBe(1);
  await expect(page.locator(".cw-tabs__note")).toHaveCount(0);
  const lines = [...srv.chats.values()][0];
  expect(lines.map((l) => l.t)).toContain("answer");
});

test("a viewer of a shared session, or a site with chats off, has one tab and no strip", async ({ page }) => {
  const srv = chatServer();
  srv.refuse = "not_owner";
  await openPanel(page, srv, ["Answer."]);
  await startChats(page);
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeHidden();
  await ask(page, "hello");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Answer.");
  expect(srv.chats.size).toBe(0);
});
