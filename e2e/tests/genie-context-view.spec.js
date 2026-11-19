// The Context view of the Genie panel (chat-widget/src/context-view.ts, docs/lld/19-genie-agent.md section 16): a list of what goes with the next message,
// with sizes, opened from the Context button or by /context.
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




test("the Context button opens a list of what goes with the next message, with sizes and why a line is not sent; Escape closes the list and not the panel", async ({ page }) => {
  const seen = await openPanel(page, chatServer(), ["x"]);
  await open(page);
  const btn = page.locator("#chat-widget__ctx-btn");
  await expect(btn).toBeVisible();
  await expect(btn).toHaveAttribute("aria-expanded", "false");
  await btn.click();
  const box = page.locator("#chat-widget__ctx");
  await expect(box).toBeVisible();
  await expect(btn).toHaveAttribute("aria-expanded", "true");
  await expect(box.getByRole("heading", { name: "What Genie sees now" })).toBeVisible();
  const rows = box.locator(".cw-ctx__row");
  await expect(rows).toHaveCount(7);
  const editor = box.locator('[data-row="editor"]');
  await expect(editor).toContainText("Editor");
  await expect(editor).toHaveClass(/cw-ctx__row--sent/);
  await expect(editor).toContainText(/\d+ lines?/);
  await expect(editor).toContainText(/KB|B/);
  await expect(box.locator('[data-row="terminal"]')).toContainText("Terminal");
  await expect(box.locator('[data-row="other"]')).toHaveClass(/cw-ctx__row--off/);
  await expect(box.locator('[data-row="other"]')).toContainText("never sent");
  await expect(box.locator(".cw-ctx__foot")).toContainText(/About [\d,]+ tokens/);
  // the text of the files is never printed here
  const text = await box.innerText();
  expect(text).not.toContain("#include <stdio.h>");
  expect(seen.requests.length).toBe(0);
  // the focus is in the list, and Escape closes the list only
  await page.keyboard.press("Escape");
  await expect(box).toBeHidden();
  await expect(btn).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  expect(await page.evaluate(() => document.activeElement && document.activeElement.id)).toBe("chat-widget__ctx-btn");
});

test("a file attached to the next message shows as yours, with its name, and /context opens the list without asking a model", async ({ page }) => {
  const seen = await openPanel(page, chatServer(), ["x"]);
  await open(page);
  await page.locator("#chat-widget__input").fill("/context");
  await page.locator("#chat-widget__submit").click();
  const box = page.locator("#chat-widget__ctx");
  await expect(box).toBeVisible();
  await expect(box.locator('[data-row="attached"]')).toHaveClass(/cw-ctx__row--empty/);
  await expect(page.locator("#chat-widget__input")).toHaveValue("");
  expect(seen.requests.length).toBe(0);
  // the Close button closes it
  await box.getByRole("button", { name: "Close the list" }).click();
  await expect(box).toBeHidden();
});
