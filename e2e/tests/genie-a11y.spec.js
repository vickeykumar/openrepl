// Accessibility of the Genie panel (docs/lld/19-genie-agent.md section 19): axe on the open panel in its states, in the dark and the light theme, and the keyboard
// path to the new parts. GENIE_LOCAL_ASSETS=1 serves the widget from this checkout's build.
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



const AxeBuilder = require("@axe-core/playwright").default || require("@axe-core/playwright");
const META = { t: "meta", id: "k3x9", created: "2026-11-19T05:00:00Z", context: "chat" };
const running = () => [META, { t: "title", text: "Build the shop page" }, { t: "state", mode: "agent", model: "gpt-4o-mini" }, { t: "user", at: "10:00", ts: Date.now() - 600000, text: "Build the shop page" }, { t: "checkpoint", ts: Date.now() - 4 * 60000, text: "Build the shop page", steps: 7, changed: "shop/index.html", file: "shop/index.html", outcome: "running" }];

async function scan(page, label, selector = "#chat-widget__container") {
  const out = [];
  for (const theme of ["dark", "light"]) {
    await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
    await page.waitForTimeout(350); // fade-ins end
    const r = await new AxeBuilder({ page }).include(selector).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "best-practice"]).disableRules(["region", "landmark-one-main", "page-has-heading-one"]).analyze();
    r.violations.forEach((v) => v.nodes.forEach((n) => out.push(theme + " | " + label + " | " + v.id + " | " + n.target.join(" ") + " | " + ((n.any[0] && n.any[0].message) || (n.all[0] && n.all[0].message) || v.help).slice(0, 90))));
  }
  return out;
}

test("axe finds nothing on the Genie panel in its states, in both themes", async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "webdriver", { get: () => false }));
  const srv = chatServer();
  srv.chats.set("k3x9", running());
  srv.open = ["k3x9"];
  await openPanel(page, srv, ["A chat answer."], { auto: { small: "gpt-6-luna", strong: "gpt-4o-mini", default: "new" } });
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  const found = [];
  // the panel with the Resume bar and the tour
  await expect(page.locator("#chat-widget__tour")).toBeVisible({ timeout: 8000 });
  found.push(...(await scan(page, "tour and resume")));
  await page.getByRole("button", { name: "Skip" }).click();
  // the Context list
  await page.locator("#chat-widget__ctx-btn").click();
  await expect(page.locator("#chat-widget__ctx")).toBeVisible();
  found.push(...(await scan(page, "context list")));
  await page.keyboard.press("Escape");
  // an answer with its row of buttons
  await page.locator("#chat-widget__input").fill("what does a closure do?");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("A chat answer.", { timeout: 15000 });
  found.push(...(await scan(page, "answer with its buttons")));
  // the / menu of the box
  await page.locator("#chat-widget__input").fill("");
  await page.locator("#chat-widget__input").press("/");
  await expect(page.locator("#chat-widget__picker")).toBeVisible();
  found.push(...(await scan(page, "slash menu")));
  await page.keyboard.press("Escape");
  // the model list with Auto
  await page.locator("#chat-widget__model-btn").click();
  found.push(...(await scan(page, "model list", "#chat-widget__container")));
  await page.keyboard.press("Escape");
  // History with its chips, stars and search
  await page.getByRole("button", { name: /All chats/ }).click();
  await page.locator(".cw-tabmenu--list").getByLabel("Search all chats").fill("shop");
  await page.waitForTimeout(400);
  found.push(...(await scan(page, "history", "body")).filter((x) => /cw-tabmenu|chat-widget/.test(x)));
  await page.keyboard.press("Escape");
  expect(found, found.join("\n")).toEqual([]);
});

test("axe finds nothing on a task card, open and folded, and on the permission question", async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "webdriver", { get: () => false }));
  await openPanel(page, chatServer(), ["A chat answer."], { agent: [{ json: { say: "Looking.", actions: [{ type: "files_list" }], done: false } }, { json: { say: "Done.", actions: [], done: true } }, { json: { say: "Looking again.", actions: [{ type: "files_list" }], done: false } }, { json: { say: "Done too.", actions: [], done: true } }] });
  await open(page);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await page.locator("#chat-widget__input").fill("what files are there");
  await page.locator("#chat-widget__submit").click();
  const ask = page.locator(".cw-agent__ask");
  await expect(ask).toBeVisible({ timeout: 30000 });
  const found = [];
  found.push(...(await scan(page, "permission question")));
  await ask.getByRole("button", { name: "Allow once" }).click();
  await expect(page.locator(".cw-agent__sum")).toBeVisible({ timeout: 30000 });
  found.push(...(await scan(page, "finished card")));
  await page.locator("#chat-widget__input").fill("and again");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator(".cw-agent__fold")).toBeVisible({ timeout: 30000 });
  found.push(...(await scan(page, "folded card")));
  await page.locator(".cw-agent__fold").first().click();
  found.push(...(await scan(page, "opened card")));
  expect(found, found.join("\n")).toEqual([]);
});

test("the keyboard reaches the Context list, the tour and History, and puts the focus where it belongs", async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "webdriver", { get: () => false }));
  const srv = chatServer();
  srv.chats.set("k3x9", running());
  srv.open = ["k3x9"];
  await openPanel(page, srv, ["A chat answer."]);
  await open(page);
  // the tour takes the focus on its Next button; Enter goes on, Escape ends it and keeps the panel
  await expect(page.locator("#chat-widget__tour")).toBeVisible({ timeout: 8000 });
  expect(await page.evaluate(() => document.activeElement && document.activeElement.textContent)).toBe("Next");
  await page.keyboard.press("Enter");
  await expect(page.locator("#chat-widget__tour")).toContainText("2 of");
  await page.keyboard.press("Escape");
  await expect(page.locator("#chat-widget__tour")).toHaveCount(0);
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  // the Context button opens with Enter, the focus is in the list, Escape gives it back to the button
  await page.locator("#chat-widget__ctx-btn").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#chat-widget__ctx")).toBeVisible();
  expect(await page.evaluate(() => document.activeElement && document.activeElement.getAttribute("aria-label"))).toBe("Close the list");
  await page.keyboard.press("Escape");
  await expect(page.locator("#chat-widget__ctx")).toBeHidden();
  expect(await page.evaluate(() => document.activeElement && document.activeElement.id)).toBe("chat-widget__ctx-btn");
  // History with Alt+H: the search box has the focus, the chips and stars are buttons with names
  await page.keyboard.press("Alt+h");
  const h = page.locator(".cw-tabmenu--list");
  await expect(h).toBeVisible();
  expect(await page.evaluate(() => document.activeElement && document.activeElement.getAttribute("aria-label"))).toBe("Search all chats");
  await expect(h.getByRole("button", { name: /^Star / }).first()).toBeVisible();
  for (const b of await h.getByRole("button").all()) {
    const name = ((await b.getAttribute("aria-label")) || (await b.innerText()) || "").trim();
    expect(name.length, "a button of History has no name").toBeGreaterThan(0);
  }
  // the focus ring is there on the keyboard path
  await page.keyboard.press("Tab");
  const ring = await page.evaluate(() => { const s = getComputedStyle(document.activeElement); return s.outlineStyle !== "none" || s.boxShadow !== "none"; });
  expect(ring).toBe(true);
});
