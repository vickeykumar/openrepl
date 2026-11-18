// The chat tabs against a REAL server (only the model is made up): needs a server built from this checkout, BASE_URL pointing at it.
// (see genie-chat-tabs.spec.js for the same panel with a stand-in server.) The chat tabs of the Genie panel (docs/lld/19-genie-agent.md, section 9): the strip, switching, a reload that brings the
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
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "js/dist/scribbler.js")) }));
  await page.route((u) => u.pathname.endsWith("/js/model-choice.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "resources/js/model-choice.js")) }));
}

// the model: answers from `script`, each after `delay` ms when given as {text, delay}
async function openPanel(page, srv, script, opts = {}) {
  await localAssets(page);
  const seen = { requests: [] };
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
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env && window.editor.env.editor);
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
const state = (page) => page.evaluate(async () => (await fetch("/genie_chats", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "state" }) })).json());

test("with the real server: a chat is written to the workspace, a reload brings it back, delete removes the file", async ({ page }) => {
  await openPanel(page, null, ["The real answer.", "Another."]);
  // a clean workspace for this browser
  await page.evaluate(() => fetch("/genie_chats", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "deleteAll" }) }));
  await page.reload();
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env && window.editor.env.editor);
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  await ask(page, "a question for the real workspace");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("The real answer.");
  await expect.poll(async () => (await state(page)).chats.length).toBe(1);
  let st = await state(page);
  expect(st.chats[0].title).toBe("a question for the real workspace");
  expect(st.chats[0].messages).toBe(2);
  expect(st.open.length).toBe(1);
  // a second tab, then a reload: both come back from the workspace
  await page.locator("#chat-widget__newtab").click();
  await ask(page, "the second chat");
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("Another.");
  await expect.poll(async () => (await state(page)).open.length).toBe(2);
  await page.reload();
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env && window.editor.env.editor);
  await open(page);
  await expect(page.locator("#chat-widget__tabs .cw-tab")).toHaveCount(2, { timeout: 15000 });
  await expect(page.locator(".chat-widget__message--user")).toContainText("the second chat");
  await page.locator("#chat-widget__tabs .cw-tab").first().click();
  await expect(page.locator(".chat-widget__message--system").first()).toContainText("The real answer.");
  // delete the shown chat from its menu: the file goes
  await page.locator("#chat-widget__tabs .cw-tab.is-active").click({ button: "right" });
  await page.getByRole("menuitem", { name: /Delete chat/ }).click();
  await page.locator("#chat-widget__toast button", { hasText: "Delete" }).first().click();
  await expect.poll(async () => (await state(page)).chats.length).toBe(1);
  st = await state(page);
  expect(st.chats[0].title).toBe("the second chat");
  await page.evaluate(() => fetch("/genie_chats", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "deleteAll" }) }));
});
