// Frames of the animated pictures of the guide to skills and plugins (src/resources/genie.html, section "Skills and plugins"). Each test plays one
// feature in the real panel, with the model and the skills server made up at the network edge, and saves a picture after each step
// (/tmp/guide-gifs/<name>/NN.png with the time each is held in frames.json). tools/make-gif.py puts them together.
//
// The rule for these pictures: the IDE is maximized and Genie is pinned beside it, and each picture is the whole window.
// Run: BASE_URL=http://localhost:8080 GENIE_LOCAL_ASSETS=1 npx playwright test -c tools/playwright.gifs.config.js record-skills-gifs
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

const SKILLS = [
  { name: "fix-merge-conflicts", description: "Resolve git conflicts in the workspace", mode: "agent", source: "workspace", path: ".genie/skills/fix-merge-conflicts.md", args: "the file or branch" },
  { name: "release-notes", description: "Write the notes for a release", mode: "chat", source: "workspace", path: ".genie/skills/release-notes.md", args: "the version" },
  { name: "security-check", description: "Look for security problems in the code", mode: "chat", source: "plugin", plugin: "code-review-kit", path: ".genie/plugins/code-review-kit/skills/security-check.md" },
  { name: "explain", description: "Explain the code or the error in plain words", mode: "chat", source: "builtin" },
  { name: "review", description: "Look over the code for problems and improvements", mode: "chat", source: "builtin" },
  { name: "test", description: "Write tests for the code in the editor", mode: "agent", source: "builtin" },
  { name: "pr", description: "Write a pull request title and description", mode: "chat", source: "builtin" },
];
const TEXTS = {
  "fix-merge-conflicts": "Find the files with conflict markers and keep the change that fits.",
  "release-notes": "Write release notes: one line for each change, newest first.",
  "security-check": "Look for secrets in the code.",
};

// a stand-in for POST genie_ext
function extServer(opts = {}) {
  const st = { calls: [], fromLinks: !!opts.fromLinks, installed: [] };
  const ok = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  const err = (r, status, code, message) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: { code, message } }) });
  const plugin = (title, name, link) => ({
    name, title, version: link ? "0.3" : "1.0", description: link ? "Review skills for a team." : "A security check, a performance look and test ideas.", author: link ? "acme" : "OpenREPL", source: link ? "link:https://github.com/acme/genie-review-kit" : "catalogue:" + name, link: !!link, digest: "d1g3st",
    skills: link
      ? [{ name: "review-it", description: "Review the code against the team rules", mode: "agent", actions: ["files_read", "files_search", "bash"] }, { name: "team-style", description: "Check the code style", mode: "chat" }]
      : [{ name: "security-check", description: "Look for security problems in the code", mode: "chat", actions: ["files_read"] }, { name: "perf-check", description: "Find the slow parts of the code", mode: "chat" }, { name: "test-ideas", description: "List the tests the code needs", mode: "chat" }],
    ignored: link ? 3 : 0, installed: false, changes: [],
    files: [{ path: "plugin.json", bytes: 120, text: '{ "name": "' + name + '", "version": "' + (link ? "0.3" : "1.0") + '" }' }, { path: "skills/review-it.md", bytes: 210, text: "---\nname: review-it\ndescription: Review the code against the team rules\nmode: agent\nactions: [files_read, files_search, bash]\n---\nRead the files that changed and check them against the team rules." }],
  });
  st.handler = (route) => {
    const b = JSON.parse(route.request().postData() || "{}");
    st.calls.push(b);
    switch (b.op) {
      case "list":
        return ok(route, { skills: SKILLS, plugins: [], problems: [], canWrite: true, fromLinks: st.fromLinks });
      case "read":
        return TEXTS[b.name] ? ok(route, { skill: { name: b.name }, text: TEXTS[b.name] }) : err(route, 404, "no_skill", "There is no such skill.");
      case "catalogue":
        return ok(route, {
          fromLinks: st.fromLinks, installed: st.installed,
          plugins: [
            { id: "code-review-kit", title: "Code review kit", version: "1.0", description: "A security check, a performance look and test ideas.", official: true, skills: ["security-check", "perf-check", "test-ideas"], installed: st.installed.length > 0, update: false, site: false },
            { id: "git-helper", title: "Git helper", version: "1.0", description: "A commit message, an explanation of a change, and help to undo a mistake.", official: true, skills: ["commit-message", "explain-diff", "undo-help"], installed: false, update: false, site: false },
            { id: "docs-writer", title: "Docs writer", version: "1.0", description: "A README, comments for functions, and a short summary of a project.", official: true, skills: ["readme", "comments"], installed: false, update: false, site: false },
          ],
        });
      case "plugin_fetch":
        return ok(route, { plugin: plugin(b.url ? "Acme review" : "Code review kit", b.url ? "acme-review" : "code-review-kit", !!b.url) });
      case "plugin_apply":
        st.installed = [{ name: b.url ? "acme-review" : "code-review-kit", title: b.url ? "Acme review" : "Code review kit", version: b.url ? "0.3" : "1.0", description: "", source: b.url ? "link:" + b.url : "catalogue:code-review-kit", link: !!b.url, skills: 3 }];
        return ok(route, { installed: "x", version: "1.0", skills: 3 });
      default:
        return err(route, 400, "bad_op", "no");
    }
  };
  return st;
}

async function openPanel(page, srv, script, opts = {}) {
  await localAssets(page);
  await page.addInitScript(() => { try { localStorage.setItem("genie-pinned", "1"); localStorage.setItem("genie-tour-v1", "1"); } catch (e) {} }); // pinned, and no tour
  await page.route((u) => u.pathname.endsWith("/genie_ext"), (r) => srv.handler(r));
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 } }) }));
  await page.route((u) => u.pathname.endsWith("/chat/completions"), async (route) => {
    const next = script.shift();
    const o = typeof next === "string" ? { text: next } : next || { text: "(the script is over)" };
    const headers = { "X-OpenREPL-Agent-Task": "task-1", "X-OpenREPL-Agent-Step": "1/8", "Access-Control-Expose-Headers": "X-OpenREPL-Agent-Task, X-OpenREPL-Agent-Step" };
    const content = o.json ? JSON.stringify(o.json) : o.text;
    route.fulfill({ status: 200, headers, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 5 } }) });
  });
  await page.route((u) => u.pathname.endsWith("/settings.js"), async (route) => {
    const res = await route.fetch();
    const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true, chatHistory: false });` +
      `document.addEventListener("DOMContentLoaded", function () { document.body.classList.add("is-signed-in"); window.CompileandRun = function () {}; });`;
    route.fulfill({ response: res, body: (await res.text()) + extra });
  });
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env.editor);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.waitForFunction(() => window.editor.env.editor.getValue().length > 0);
  await page.waitForTimeout(500);
  // the IDE maximized, then Genie pinned beside it
  await page.evaluate(() => window.ToggleFunction());
  await page.waitForFunction(() => document.body.classList.contains("ide-maximized"));
  await page.evaluate(() => window.ChatWidget.open({}, { pin: true }));
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  await page.waitForTimeout(1500);
}

const OUT = process.env.GIF_FRAMES || "/tmp/guide-gifs";

class Recorder {
  constructor(page, name) {
    this.page = page;
    this.dir = path.join(OUT, name);
    this.frames = [];
    fs.rmSync(this.dir, { recursive: true, force: true });
    fs.mkdirSync(this.dir, { recursive: true });
  }
  // a picture of the whole window, held for `hold` ms
  async snap(hold = 900, quick = false) {
    await this.page.waitForTimeout(quick ? 90 : 260);
    const file = String(this.frames.length).padStart(2, "0") + ".png";
    await this.page.screenshot({ path: path.join(this.dir, file), clip: { x: 0, y: 0, width: 1280, height: 860 } });
    this.frames.push({ file, ms: hold });
  }
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

test("skills-menu", async ({ page }) => {
  await openPanel(page, extServer(), ["x"]);
  const r = rec(page, "skills-menu");
  await box(page).click();
  await r.snap(900);
  await box(page).press("/");
  await expect(page.locator("#chat-widget__picker")).toBeVisible();
  await r.snap(2800);
  for (let i = 0; i < 12; i++) {
    await box(page).press("ArrowDown");
    await r.snap(i === 5 || i === 8 || i === 11 ? 1100 : 380, true);
  }
  await box(page).press("Escape");
  await box(page).fill("");
  await r.snap(600, true);
  await box(page).press("/");
  await r.typed("fix", { every: 1, hold: 450, last: 900 });
  await box(page).press("Enter");
  await r.snap(800, true);
  await r.typed("in the cart folder", { every: 3, hold: 140, last: 1200 });
  await box(page).fill("");
  await r.snap(500, true);
  await r.typed("please write /rel", { every: 4, hold: 160, last: 1100 });
  await box(page).press("Enter");
  await r.snap(900, true);
  await r.typed("for 2.0 and then /", { every: 4, hold: 140, last: 1800 });
  r.done();
});

test("skills-chat", async ({ page }) => {
  await openPanel(page, extServer(), ["Release 2.0\n\n- Faster start\n- The cart no longer loses items\n- New dark theme"]);
  const r = rec(page, "skills-chat");
  await box(page).click();
  await r.snap(700);
  await r.typed("/release-notes for 2.0", { every: 4, hold: 130, last: 900 });
  await page.locator("#chat-widget__submit").click();
  await r.snap(500, true);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Faster start", { timeout: 15000 });
  await r.snap(3200);
  r.done();
});

test("skills-agent", async ({ page }) => {
  await openPanel(page, extServer(), [{ json: { say: "Using the skill /fix-merge-conflicts. I look at the files first.", actions: [{ type: "files_list" }], done: false } }, { json: { say: "No file has conflict markers, so there is nothing to fix.", actions: [], done: true } }]);
  const r = rec(page, "skills-agent");
  await box(page).click();
  await r.snap(700);
  await r.typed("/fix-merge-conflicts in the cart folder", { every: 5, hold: 130, last: 900 });
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator(".cw-agent__ask")).toBeVisible({ timeout: 30000 });
  await r.snap(2200);
  await page.locator(".cw-agent__ask").getByRole("button", { name: "Allow once" }).click();
  await r.snap(500, true);
  await expect(page.locator(".cw-agent__sum")).toBeVisible({ timeout: 30000 });
  await r.snap(3200);
  r.done();
});

test("plugins", async ({ page }) => {
  await openPanel(page, extServer(), ["x"]);
  const r = rec(page, "plugins");
  await box(page).click();
  await r.snap(700);
  await r.typed("/add-plugin", { every: 3, hold: 140, last: 800 });
  await page.locator("#chat-widget__submit").click();
  const dlg = page.locator("#chat-widget__plugins");
  await expect(dlg).toBeVisible();
  await r.snap(2800);
  await dlg.getByRole("button", { name: "Install", exact: true }).first().hover();
  await r.snap(600, true);
  await dlg.getByRole("button", { name: "Install", exact: true }).first().click();
  await expect(dlg.locator("[data-review]")).toBeVisible();
  await r.snap(3200);
  await dlg.getByText("View the files").click();
  await r.snap(2400);
  await dlg.locator(".cw-plg__foot").getByRole("button", { name: "Install" }).click();
  await expect(dlg).toContainText("is installed");
  await r.snap(2400);
  await dlg.getByRole("button", { name: /^Installed/ }).click();
  await r.snap(2200);
  await page.keyboard.press("Escape");
  await r.snap(500, true);
  await box(page).press("/");
  await r.typed("sec", { every: 1, hold: 500, last: 2000 });
  r.done();
});

test("plugins-link", async ({ page }) => {
  await openPanel(page, extServer({ fromLinks: true }), ["x"]);
  const r = rec(page, "plugins-link");
  await box(page).click();
  await box(page).fill("/add-plugin");
  await page.locator("#chat-widget__submit").click();
  const dlg = page.locator("#chat-widget__plugins");
  await expect(dlg).toBeVisible();
  await r.snap(2000);
  const link = dlg.locator('input[type="url"]');
  await link.click();
  for (let i = 0; i < "https://github.com/acme/genie-review-kit".length; i += 8) {
    await link.pressSequentially("https://github.com/acme/genie-review-kit".slice(i, i + 8), { delay: 20 });
    await r.snap(i + 8 >= 40 ? 900 : 120, true);
  }
  await dlg.getByRole("button", { name: "Look" }).click();
  await expect(dlg).toContainText("Not from the catalogue");
  await r.snap(3600);
  const install = dlg.locator(".cw-plg__foot").getByRole("button", { name: "Install" });
  await install.hover();
  await r.snap(900, true);
  await dlg.getByLabel(/I read the skills/).check();
  await r.snap(1500);
  await install.click();
  await expect(dlg).toContainText("is installed");
  await r.snap(2800);
  r.done();
});
