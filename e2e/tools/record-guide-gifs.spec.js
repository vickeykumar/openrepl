// Frames of the animated pictures of the Genie guide (src/resources/genie.html). Each test plays one feature in the real panel, with the model made up at
// the network edge, and saves a picture of the panel after each step (/tmp/guide-gifs/<name>/NN.png with the time each is held in frames.json).
// tools/make-gif.py puts them together as src/resources/images/genie/guide-<name>.gif.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const { waitForTerminalReady } = require("../tests/helpers");

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
    const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true, genieAuto: { small: 'gpt-6-luna', strong: 'gpt-4o-mini', default: 'new' }, chatHistory: true, chatHistoryGuests: true, chatHistoryTabs: ${opts.tabs || 8} });` +
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


const OUT = process.env.GIF_FRAMES || "/tmp/guide-gifs";

class Recorder {
  constructor(page, name) {
    this.page = page;
    this.dir = path.join(OUT, name);
    this.frames = [];
    this.clip = null;
    fs.rmSync(this.dir, { recursive: true, force: true });
    fs.mkdirSync(this.dir, { recursive: true });
  }
  // a picture of the panel (and what hangs off it), held for `hold` ms
  async snap(hold = 900, quick = false) {
    await this.page.waitForTimeout(quick ? 90 : 260); // fades end
    if (!this.clip) {
      // Genie is pinned beside the IDE (the dock): the picture shows the panel and a part of the editor and the terminal next to it.
      // The IDE row is brought to the top of the window, and the terminal's size badge has gone before the first picture.
      await this.page.evaluate(() => document.getElementById("genie-dock").parentElement.scrollIntoView({ block: "start" }));
      await this.page.waitForTimeout(1700);
      const b = await this.page.locator("#chat-widget__container").boundingBox();
      const x = Math.max(0, Math.floor(b.x - 560)), y = Math.max(0, Math.floor(b.y));
      this.clip = { x, y, width: Math.min(1280 - x, Math.ceil(b.x + b.width - x)), height: Math.min(860 - y, Math.ceil(b.height)) };
    }
    const file = String(this.frames.length).padStart(2, "0") + ".png";
    await this.page.screenshot({ path: path.join(this.dir, file), clip: this.clip });
    this.frames.push({ file, ms: hold });
  }
  // types the text and takes a picture every `every` characters, so that the picture shows the typing; the last one is held `last` ms
  async typed(text, { every = 3, hold = 140, last = 900, delay = 25 } = {}) {
    const box = this.page.locator("#chat-widget__input");
    for (let i = 0; i < text.length; i += every) {
      await box.pressSequentially(text.slice(i, i + every), { delay });
      await this.snap(i + every >= text.length ? last : hold, true);
    }
  }
  done() {
    fs.writeFileSync(path.join(this.dir, "frames.json"), JSON.stringify(this.frames));
  }
}

const rec = (page, name) => new Recorder(page, name);
const box = (page) => page.locator("#chat-widget__input");
const type = async (page, text) => { await box(page).pressSequentially(text, { delay: 20 }); };
const pin = (page) => page.addInitScript(() => { try { localStorage.setItem("genie-pinned", "1"); } catch (e) {} }); // Genie is pinned beside the IDE
const quiet = async (page) => { await pin(page); await page.addInitScript(() => { try { localStorage.setItem("genie-tour-v1", "1"); } catch (e) {} }); };
const CHATS = () => [
  [{ t: "meta", id: "k1aa", created: "2026-11-19T05:00:00Z", context: "chat" }, { t: "title", text: "make a web page" }, { t: "state", mode: "chat" }, { t: "user", at: "10:00", ts: Date.now() - 3600000, text: "why does the click say a stale ref?" }, { t: "answer", at: "10:01", ts: Date.now() - 3590000, text: "The numbers start again after a reload." }],
  [{ t: "meta", id: "k2bb", created: "2026-11-19T05:00:00Z", context: "chat" }, { t: "title", text: "Starfall Salvager preview" }, { t: "state", mode: "agent" }, { t: "user", at: "09:00", ts: Date.now() - 7200000, text: "play the game" }, { t: "answer", at: "09:01", ts: Date.now() - 7190000, text: "e2 is not on the page now, so I read the page again." }],
  [{ t: "meta", id: "k3cc", created: "2026-11-19T05:00:00Z", context: "chat" }, { t: "title", text: "solve it" }, { t: "state", mode: "agent" }, { t: "user", at: "08:00", ts: Date.now() - 86400000, text: "solve the puzzle" }, { t: "task", text: "the numbers start again at e1", outcome: "done" }],
];


test("context", async ({ page }) => {
  await quiet(page);
  await openPanel(page, chatServer(), ["A closure is a function that keeps the variables around it alive."]);
  await open(page);
  const r = rec(page, "context");
  await r.snap(900);
  await r.typed("why does my loop print twice?", { every: 4, hold: 130, last: 900 });
  await page.locator("#chat-widget__ctx-btn").hover();
  await r.snap(700, true);
  await page.locator("#chat-widget__ctx-btn").click();
  await expect(page.locator("#chat-widget__ctx")).toBeVisible();
  await r.snap(3200);
  await page.keyboard.press("Escape");
  await r.snap(1200);
  r.done();
});

test("slash", async ({ page }) => {
  await quiet(page);
  await openPanel(page, chatServer(), ["x"]);
  await open(page);
  const r = rec(page, "slash");
  await box(page).click();
  await r.snap(900);
  await box(page).press("/");
  await expect(page.locator("#chat-widget__picker")).toBeVisible();
  await r.snap(2200);
  await box(page).press("ArrowDown");
  await r.snap(700, true);
  await box(page).press("ArrowDown");
  await r.snap(900, true);
  await box(page).press("ArrowUp");
  await box(page).press("ArrowUp");
  await box(page).press("ArrowUp");
  await box(page).press("Escape");
  await box(page).fill("");
  await r.snap(700, true);
  await box(page).press("/");
  await r.typed("re", { every: 1, hold: 450, last: 900 });
  await box(page).press("Enter");
  await r.snap(800, true);
  await r.typed("indent with tabs", { every: 3, hold: 140, last: 900 });
  await page.locator("#chat-widget__submit").click();
  await r.snap(500, true);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Remembered in GENIE.md", { timeout: 15000 });
  await r.snap(3000);
  r.done();
});

test("search-star", async ({ page }) => {
  await quiet(page);
  const srv = chatServer();
  CHATS().forEach((l) => srv.chats.set(l[0].id, l));
  srv.open = ["k1aa"];
  await openPanel(page, srv, ["x"]);
  await open(page);
  await expect(page.locator("#chat-widget__tabs")).toBeVisible({ timeout: 15000 });
  const r = rec(page, "search-star");
  await r.snap(900);
  await page.getByRole("button", { name: /All chats/ }).click();
  await r.snap(1600);
  const h = page.locator(".cw-tabmenu--list");
  const sbox = h.getByLabel("Search all chats");
  for (const ch of "stale") {
    await sbox.pressSequentially(ch, { delay: 30 });
    await page.waitForTimeout(260);
    await r.snap(300, true);
  }
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(1);
  await r.snap(2400);
  await sbox.fill("");
  await expect(h.locator(".cw-tabmenu__row")).toHaveCount(3);
  await r.snap(1000);
  await h.getByRole("button", { name: "Star Starfall Salvager preview" }).hover();
  await r.snap(600, true);
  await h.getByRole("button", { name: "Star Starfall Salvager preview" }).click();
  await r.snap(1900);
  await h.locator('[data-filter="starred"]').hover();
  await r.snap(500, true);
  await h.locator('[data-filter="starred"]').click();
  await r.snap(2400);
  r.done();
});

test("auto", async ({ page }) => {
  await quiet(page);
  const srv = chatServer();
  const seen = await openPanel(page, srv, ["A closure is a function that keeps the variables around it alive.", "A closure captures the variables of the function it was made in, so they stay available after that function returns."], {});
  await open(page);
  const r = rec(page, "auto");
  await r.snap(900);
  await page.locator("#chat-widget__model-btn").click();
  await expect(page.locator("#chat-widget__models .cw-model--auto")).toBeVisible();
  await r.snap(2800);
  await page.keyboard.press("Escape");
  await r.snap(600, true);
  await r.typed("what does a closure do?", { every: 4, hold: 130, last: 800 });
  await page.locator("#chat-widget__submit").click();
  await r.snap(500, true);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Auto chose a small model", { timeout: 15000 });
  await r.snap(3000);
  await page.getByRole("button", { name: "Use a stronger one" }).hover();
  await r.snap(700, true);
  await page.getByRole("button", { name: "Use a stronger one" }).click();
  await r.snap(500, true);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("you asked for a stronger one", { timeout: 15000 });
  await r.snap(3200);
  r.done();
});

test("cards", async ({ page }) => {
  await quiet(page);
  await openPanel(page, chatServer(), ["x"], { agent: [{ json: { say: "Looking at your files.", actions: [{ type: "files_list" }], done: false } }, { json: { say: "There are a few files. Nothing needed changing.", actions: [], done: true } }, { json: { say: "Looking again.", actions: [{ type: "files_list" }], done: false } }, { json: { say: "Done.", actions: [], done: true } }] });
  await open(page);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  const r = rec(page, "cards");
  await r.snap(800);
  await r.typed("what files are there?", { every: 4, hold: 130, last: 700 });
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator(".cw-agent__ask")).toBeVisible({ timeout: 30000 });
  await r.snap(1900);
  await page.locator(".cw-agent__ask").getByRole("button", { name: "Allow once" }).click();
  await r.snap(500, true);
  await expect(page.locator(".cw-agent__sum")).toBeVisible({ timeout: 30000 });
  await r.snap(2800);
  await r.typed("and once more", { every: 4, hold: 130, last: 700 });
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator(".cw-agent__fold")).toBeVisible({ timeout: 30000 });
  await r.snap(2800);
  await page.locator(".cw-agent__fold").first().hover();
  await r.snap(500, true);
  await page.locator(".cw-agent__fold").first().click();
  await r.snap(2600);
  await page.locator(".cw-agent__fold").first().click();
  await r.snap(1400);
  r.done();
});

test("tour", async ({ page }) => {
  await pin(page);
  await page.addInitScript(() => Object.defineProperty(navigator, "webdriver", { get: () => false }));
  await openPanel(page, chatServer(), ["x"]);
  await open(page);
  const r = rec(page, "tour");
  const tour = page.locator("#chat-widget__tour");
  await expect(tour).toBeVisible({ timeout: 8000 });
  await r.snap(2600);
  for (let i = 0; i < 3; i++) {
    await tour.getByRole("button", { name: "Next" }).hover();
    await r.snap(400, true);
    await tour.getByRole("button", { name: "Next" }).click();
    await r.snap(2600);
  }
  await tour.getByRole("button", { name: "Done" }).click();
  await r.snap(1400);
  r.done();
});
