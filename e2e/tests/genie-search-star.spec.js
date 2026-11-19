// Search across chats and stars (chat-widget/src/chat-search.ts, docs/lld/19-genie-agent.md section 17): History has a search over titles and messages, chips, a star on
// each chat, and the starred chats come first and stay when everything is deleted. The workspace's chats are a stand-in (the real server is tested in Go).
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
          const stars = lines.filter((l) => l.t === "star");
          return { id, title: title ? title.text : "", starred: stars.length > 0 && stars[stars.length - 1].on === 1, updated: Math.floor(Date.now() / 1000), messages: lines.filter((l) => l.t === "user" || l.t === "answer").length, size: JSON.stringify(lines).length, language: (lines.find((l) => l.t === "user") || {}).language || "" };
        });
        return ok(route, { enabled: true, off: st.off, open: st.open.filter((id) => st.chats.has(id)), chats, bytes: 100, limits: { chats: 50, tabs: opts.tabs || 8, bytes: 204800 } });
      }
      case "read":
        return st.chats.has(b.id) ? ok(route, { id: b.id, lines: st.chats.get(b.id) }) : err(route, 404, "no_chat");
      case "append": {
        if (b.lines.some((l) => l.t === "star" && l.on === 1) && opts.starLimit !== undefined) {
          const others = [...st.chats.entries()].filter(([id, ls]) => id !== b.id && ls.filter((l) => l.t === "star").slice(-1).some((l) => l.on === 1)).length;
          if (others >= opts.starLimit) return err(route, 409, "star_limit");
        }
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
        for (const [id, ls] of [...st.chats.entries()]) {
          const starred = ls.filter((l) => l.t === "star").slice(-1).some((l) => l.on === 1);
          if (b.includeStarred || !starred) st.chats.delete(id);
        }
        st.open = st.open.filter((id) => st.chats.has(id));
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


const mk = (id, title, ago, extra = []) => [{ t: "meta", id, created: "2026-11-19T05:00:00Z", context: "chat" }, { t: "title", text: title }, ...extra];
const user = (text) => ({ t: "user", at: "10:00", ts: Date.now(), text });
const answer = (text) => ({ t: "answer", at: "10:01", ts: Date.now(), text });

async function withChats(page, chats, opts = {}) {
  const srv = chatServer(opts);
  chats.forEach((lines, i) => srv.chats.set(lines[0].id, lines));
  srv.open = [chats[0][0].id];
  const seen = await openPanel(page, srv, ["A chat answer."], opts);
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  return { srv, seen };
}

const CHATS = () => [
  mk("k1aa", "make a web page", 0, [{ t: "state", mode: "chat" }, user("why does the click say a stale ref?"), answer("The numbers start again after a reload.")]),
  mk("k2bb", "Starfall Salvager preview", 1, [{ t: "state", mode: "agent" }, user("play the game"), answer("e2 is not on the page now, so I read the page again.")]),
  mk("k3cc", "solve it", 2, [{ t: "state", mode: "agent" }, user("solve the puzzle")]),
];

const showHistory = (page) => page.getByRole("button", { name: /All chats/ }).click();
const list = (page) => page.locator(".cw-tabmenu--list");

test("History has chips and a search over titles and messages, with the words found marked, newest first; a click opens the chat at the message", async ({ page }) => {
  await withChats(page, CHATS());
  await showHistory(page);
  const h = list(page);
  await expect(h.locator('[data-filter="all"]')).toHaveAttribute("aria-pressed", "true");
  for (const name of ["Starred", "Chat", "Agent"]) await expect(h.getByRole("button", { name: new RegExp("^" + name) })).toBeVisible();
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(3);
  // a word that is in a message and not in a title
  await h.getByLabel("Search all chats").fill("stale ref");
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(1);
  await expect(h.locator(".cw-tabmenu__snippet mark").first()).toHaveText("stale");
  await expect(h.locator(".cw-tabmenu__status")).toHaveText("1 chat");
  // a word in two chats
  await h.getByLabel("Search all chats").fill("page");
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(2);
  // nothing
  await h.getByLabel("Search all chats").fill("zzzz qqqq");
  await expect(h.getByText("No chat has these words. Try fewer words.")).toBeVisible();
  // Escape clears the box first
  await h.getByLabel("Search all chats").press("Escape");
  await expect(h.getByLabel("Search all chats")).toHaveValue("");
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(3);
  // the chips: Agent shows the two agent chats
  await h.getByRole("button", { name: /^Agent/ }).click();
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(2);
  await h.getByRole("button", { name: /^Chat/ }).click();
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(1);
  // a result opens the chat and marks the message
  await h.locator('[data-filter="all"]').click();
  await h.getByLabel("Search all chats").fill("reload");
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(1); // the search has run
  await h.locator(".cw-tabmenu__row").first().click();
  await expect(page.locator(".chat-widget__message.cw-found")).toBeVisible({ timeout: 5000 });
});

test("a star on a chat in History puts it first and is kept in the chat's own file; the tab shows it and the menu can take it away", async ({ page }) => {
  const { srv } = await withChats(page, CHATS());
  await showHistory(page);
  let h = list(page);
  await h.getByRole("button", { name: "Star solve it" }).click();
  expect(srv.chats.get("k3cc").some((l) => l.t === "star" && l.on === 1)).toBe(true);
  await expect(h.getByText("Starred", { exact: true }).first()).toBeVisible();
  await expect(h.locator(".cw-tabmenu__group").first()).toHaveText("Starred");
  await expect(h.locator(".cw-tabmenu__rowwrap").first()).toContainText("solve it");
  await expect(h.getByRole("button", { name: /^Starred \u00b7 1/ })).toBeVisible();
  await h.getByRole("button", { name: /^Starred/ }).click();
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(1);
  await page.keyboard.press("Escape");
  // the open chat: the menu of its tab stars it and the tab shows the star
  const tab = page.locator('#chat-widget__tabs [role="tab"]').first();
  await tab.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Star this chat" }).click();
  await expect(tab.locator(".cw-tab__star")).toBeVisible();
  await tab.click({ button: "right" });
  await expect(page.getByRole("menuitem", { name: "Unstar this chat" })).toBeVisible();
  await page.getByRole("menuitem", { name: "Unstar this chat" }).click();
  await expect(tab.locator(".cw-tab__star")).toHaveCount(0);
});

test("only twenty can be starred: the next one says so and nothing is lost", async ({ page }) => {
  await withChats(page, CHATS(), { starLimit: 1 });
  await showHistory(page);
  const h = list(page);
  await h.getByRole("button", { name: "Star make a web page" }).click();
  await h.getByRole("button", { name: "Star solve it" }).click();
  await expect(h.locator(".cw-tabmenu__status")).toHaveText("You can star 20 chats. Unstar one first.");
  await expect(h.getByRole("button", { name: "Star solve it" })).toHaveAttribute("aria-pressed", "false");
});

test("Delete all keeps the starred chats unless Include starred is chosen", async ({ page }) => {
  const { srv } = await withChats(page, CHATS());
  await showHistory(page);
  await list(page).getByRole("button", { name: "Star solve it" }).click();
  await list(page).getByRole("button", { name: /Delete all/ }).click();
  const bar = page.locator("#chat-widget__toast");
  await expect(bar).toContainText("2 of the 3 chats");
  await expect(bar).toContainText("starred chat stays");
  await bar.getByRole("button", { name: "Delete", exact: true }).click();
  await expect.poll(() => [...srv.chats.keys()].join(",")).toBe("k3cc");
  // and with Include starred nothing is left
  await showHistory(page);
  await list(page).getByRole("button", { name: /Delete all/ }).click();
  await page.locator("#chat-widget__toast").getByRole("button", { name: "Include starred" }).click();
  await expect.poll(() => srv.chats.size).toBe(0);
});
