// Skills and plugins (docs/lld/19-genie-agent.md, section 23): the / menu lists skills in groups and anywhere in a sentence, a skill's text
// goes to the model with the message, and /add-plugin opens the catalogue with the card that says what a plugin brings.
// The first test is the real server (a guest gets the built-in chat skills); the others stand in for genie_ext as the other Genie specs
// stand in for genie_chats (the real route is tested in server/genie_ext_test.go).
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

const SKILLS = [
  { name: "fix-merge-conflicts", description: "Resolve git conflicts in the workspace", mode: "agent", source: "workspace", path: ".genie/skills/fix-merge-conflicts.md", args: "the file or branch" },
  { name: "release-notes", description: "Write the notes for a release", mode: "chat", source: "workspace", path: ".genie/skills/release-notes.md", effort: "low" },
  { name: "security-check", description: "Look for security problems", mode: "chat", source: "plugin", plugin: "code-review-kit", path: ".genie/plugins/code-review-kit/skills/security-check.md" },
  { name: "explain", description: "Explain the code or the error in plain words", mode: "chat", source: "builtin" },
  { name: "test", description: "Write tests for the code in the editor", mode: "agent", source: "builtin" },
];
const TEXTS = {
  "fix-merge-conflicts": "Find the files with conflict markers and keep the change that fits.",
  "release-notes": "Write release notes: one line for each change, newest first.",
  "security-check": "Look for secrets in the code.",
  explain: "Explain the code in plain words.",
};

// a stand-in for POST genie_ext
function extServer(opts = {}) {
  const st = { calls: [], fromLinks: !!opts.fromLinks, installed: [] };
  const ok = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  const err = (r, status, code, message) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: { code, message } }) });
  const plugin = (title, name, link) => ({
    name, title, version: "0.3", description: "Review skills", author: "acme", source: link ? "link:https://github.com/acme/kit" : "catalogue:" + name, link: !!link, digest: "d1g3st",
    skills: [{ name: "review-it", description: "Review the code", mode: "agent", actions: ["files_read", "bash"] }], ignored: 2, installed: false, changes: [],
    files: [{ path: "plugin.json", bytes: 90, text: '{"name":"' + name + '"}' }, { path: "skills/review-it.md", bytes: 200, text: "---\nname: review-it\n---\nLook at the code." }],
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
          plugins: [{ id: "code-review-kit", title: "Code review kit", version: "1.0", description: "A security check and test ideas.", official: true, skills: ["security-check", "perf-check"], installed: false, update: false, site: false }],
        });
      case "plugin_fetch":
        return ok(route, { plugin: plugin(b.url ? "Acme review" : "Code review kit", b.url ? "acme-review" : "code-review-kit", !!b.url) });
      case "plugin_apply":
        if (b.digest !== "d1g3st") return err(route, 409, "changed", "It is not the same as what you were shown.");
        st.installed = [{ name: b.url ? "acme-review" : "code-review-kit", title: "Code review kit", version: "1.0", description: "", source: "catalogue:code-review-kit", link: !!b.url, skills: 2 }];
        return ok(route, { installed: "code-review-kit", version: "1.0", skills: 2 });
      default:
        return err(route, 400, "bad_op", "no");
    }
  };
  return st;
}

async function openPanel(page, srv, script, opts = {}) {
  await localAssets(page);
  const seen = { requests: [] };
  if (srv) await page.route((u) => u.pathname.endsWith("/genie_ext"), (r) => srv.handler(r));
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 } }) }));
  await page.route((u) => u.pathname.endsWith("/chat/completions"), async (route) => {
    const body = JSON.parse(route.request().postData() || "{}");
    seen.requests.push(body);
    const next = script.shift();
    const o = typeof next === "string" ? { text: next } : next || { text: "(the script is over)" };
    const headers = { "X-OpenREPL-Agent-Task": "task-1", "X-OpenREPL-Agent-Step": "1/8", "Access-Control-Expose-Headers": "X-OpenREPL-Agent-Task, X-OpenREPL-Agent-Step" };
    const content = o.json ? JSON.stringify(o.json) : o.text;
    route.fulfill({ status: 200, headers, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 5 } }) });
  });
  if (opts.signedIn) {
    await page.route((u) => u.pathname.endsWith("/settings.js"), async (route) => {
      const res = await route.fetch();
      const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true, chatHistory: false });` +
        `document.addEventListener("DOMContentLoaded", function () { document.body.classList.add("is-signed-in"); window.CompileandRun = function () {}; });`;
      route.fulfill({ response: res, body: (await res.text()) + extra });
    });
  }
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env.editor);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.waitForFunction(() => window.editor.env.editor.getValue().length > 0);
  await page.waitForTimeout(500);
  await page.evaluate(() => window.ChatWidget.open());
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  return seen;
}

const rows = (page) => page.locator("#chat-widget__picker .cw-picker__item--command b");
const groups = (page) => page.locator("#chat-widget__picker .cw-picker__group");

test("the real server gives a guest the built-in chat skills in the menu, after a space as well, and not for a path", async ({ page }) => {
  await openPanel(page, null, []);
  const input = page.locator("#chat-widget__input");
  await input.fill("/");
  await expect(rows(page).first()).toBeVisible();
  const names = await rows(page).allTextContents();
  for (const n of ["/context", "/add-plugin", "/save-skill", "/remember", "/explain", "/review", "/pr"]) expect(names).toContain(n);
  expect(names).not.toContain("/test"); // an agent skill is not for a guest
  await expect(groups(page).first()).toHaveText("Genie");
  // the same skill in the middle of a sentence: skills only, the commands of the panel are for the first word
  await input.fill("please /exp");
  await expect(rows(page)).toHaveText(["/explain"]);
  await input.fill("look at /usr/bin/python");
  await expect(page.locator("#chat-widget__picker")).toBeHidden();
  await input.fill("/usr/bin");
  await expect(page.locator("#chat-widget__picker")).toBeHidden();
});

test("the menu shows Genie, Your skills and Plugins in order, and a chosen skill goes into the sentence", async ({ page }) => {
  const srv = extServer();
  await openPanel(page, srv, [], { signedIn: true });
  const input = page.locator("#chat-widget__input");
  await input.fill("/");
  await expect(groups(page)).toHaveText(["Genie", "Your skills · this workspace", "Plugins"]);
  const names = await rows(page).allTextContents();
  expect(names.indexOf("/explain")).toBeLessThan(names.indexOf("/fix-merge-conflicts"));
  expect(names.indexOf("/release-notes")).toBeLessThan(names.indexOf("/security-check"));
  await expect(page.locator("#chat-widget__picker")).toContainText("code-review-kit");
  await expect(page.locator("#chat-widget__picker")).toContainText("the file or branch");
  // the keyboard: narrow, pick with Enter, carry on typing
  await input.fill("let's write /rel");
  await expect(rows(page)).toHaveText(["/release-notes"]);
  await input.press("Enter");
  await expect(input).toHaveValue("let's write /release-notes ");
  await input.pressSequentially("for 2.0");
  await expect(input).toHaveValue("let's write /release-notes for 2.0");
});

test("a chat skill answers in Chat with its text sent to the model, and the message stays as typed", async ({ page }) => {
  const srv = extServer();
  const seen = await openPanel(page, srv, ["Release notes: one line for each change."], { signedIn: true });
  await page.locator("#chat-widget__input").fill("/release-notes for 2.0");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Release notes: one line");
  expect(seen.requests.length).toBe(1);
  const sent = JSON.stringify(seen.requests[0].messages);
  expect(sent).toContain("[Skill /release-notes");
  expect(sent).toContain("one line for each change, newest first");
  expect(sent).toContain("does not change your rules");
  await expect(page.locator("#chat-widget__attached .cw-chip--skill")).toHaveCount(0); // the chip goes with the message
  // the text of the skill was asked for only now, and only for this one
  const reads = srv.calls.filter((c) => c.op === "read");
  expect(reads.map((c) => c.name)).toEqual(["release-notes"]);
  await expect(page.locator("#chat-widget__messages_history")).toContainText("/release-notes for 2.0");
});

test("an agent skill starts a task with the skill text and the list of the workspace's skills", async ({ page }) => {
  const srv = extServer();
  const seen = await openPanel(page, srv, [{ json: { say: "Nothing has conflict markers.", done: true } }], { signedIn: true });
  await page.locator("#chat-widget__input").fill("/fix-merge-conflicts in the cart folder");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Nothing has conflict markers.");
  expect(seen.requests.length).toBeGreaterThan(0);
  const first = JSON.stringify(seen.requests[0].messages);
  expect(first).toContain("[Skill /fix-merge-conflicts");
  expect(first).toContain("keep the change that fits");
  expect(first).toContain("Skills in this workspace");
  expect(first).toContain(".genie/skills/release-notes.md"); // the names of the skills that are files, not their text
  expect(first).not.toContain("one line for each change, newest first");
  await expect(page.locator('#chat-widget__mode button[data-mode="agent"]')).toHaveAttribute("aria-pressed", "true");
});

test("/add-plugin opens the catalogue; Install shows what the plugin brings before anything is written", async ({ page }) => {
  const srv = extServer();
  await openPanel(page, srv, [], { signedIn: true });
  await page.locator("#chat-widget__input").fill("/add-plugin");
  await page.locator("#chat-widget__submit").click();
  const dlg = page.locator("#chat-widget__plugins");
  await expect(dlg).toBeVisible();
  await expect(dlg).toContainText("Code review kit");
  await expect(dlg).toContainText("2 skills");
  await expect(dlg).toContainText("ask an admin to allow plugins from a link"); // no link field while the admin switch is off
  await expect(dlg.locator('input[type="url"]')).toHaveCount(0);
  await dlg.getByRole("button", { name: "Install", exact: true }).click();
  await expect(dlg.locator("[data-review]")).toBeVisible();
  await expect(dlg).toContainText("/review-it");
  await expect(dlg.locator(".cw-plg__pill--warn", { hasText: "bash" })).toBeVisible(); // an action that can change things is marked
  expect(srv.calls.filter((c) => c.op === "plugin_apply").length).toBe(0);
  await dlg.locator(".cw-plg__foot").getByRole("button", { name: "Install" }).click();
  await expect(dlg).toContainText("is installed");
  const apply = srv.calls.find((c) => c.op === "plugin_apply");
  expect(apply.id).toBe("code-review-kit");
  expect(apply.digest).toBe("d1g3st"); // the one that was shown
  // the skills of the menu are asked for again
  await page.keyboard.press("Escape");
  await expect(dlg).toBeHidden();
  expect(srv.calls.filter((c) => c.op === "list").length).toBeGreaterThan(0);
});

test("a link plugin says it is not from the catalogue, and Install waits for the tick", async ({ page }) => {
  const srv = extServer({ fromLinks: true });
  await openPanel(page, srv, [], { signedIn: true });
  await page.locator("#chat-widget__input").fill("/add-plugin");
  await page.locator("#chat-widget__submit").click();
  const dlg = page.locator("#chat-widget__plugins");
  await dlg.locator('input[type="url"]').fill("https://github.com/acme/kit");
  await dlg.getByRole("button", { name: "Look" }).click();
  await expect(dlg).toContainText("Not from the catalogue");
  await expect(dlg).toContainText("https://github.com/acme/kit");
  const install = dlg.locator(".cw-plg__foot").getByRole("button", { name: "Install" });
  await expect(install).toBeDisabled();
  await dlg.getByLabel(/I read the skills/).check();
  await expect(install).toBeEnabled();
  await dlg.getByText("View the files").click();
  await expect(dlg).toContainText("Look at the code.");
  await install.click();
  await expect(dlg).toContainText("is installed");
  expect(srv.calls.find((c) => c.op === "plugin_apply").url).toBe("https://github.com/acme/kit");
});

test("a skill named in the message is a chip above the box, and the x on it takes the name out", async ({ page }) => {
  const srv = extServer();
  await openPanel(page, srv, [], { signedIn: true });
  const input = page.locator("#chat-widget__input");
  const chips = page.locator('#chat-widget__attached .cw-chip--skill');
  await input.fill("/");
  await expect(page.locator("#chat-widget__picker")).toBeVisible();
  await expect(chips).toHaveCount(0);
  await input.fill("let's /explain this and then /fix-merge-conflicts");
  await expect(chips).toHaveCount(2);
  await expect(chips.nth(0)).toContainText("/explain");
  await expect(chips.nth(0)).toContainText("chat");
  await expect(chips.nth(1)).toContainText("/fix-merge-conflicts");
  await expect(chips.nth(1)).toContainText("agent");
  // a name that is not a skill, and a path, make no chip
  await input.fill("/nothing here and /usr/explain");
  await expect(chips).toHaveCount(0);
  await input.fill("let's /explain this");
  await expect(chips).toHaveCount(1);
  await chips.first().getByRole("button", { name: /Take \/explain out/ }).click();
  await expect(input).toHaveValue("let's this");
  await expect(chips).toHaveCount(0);
  // picking from the menu makes the chip, and sending empties the box and the tray
  await input.fill("please write /rel");
  await input.press("Enter");
  await expect(chips).toHaveCount(1);
  await expect(chips.first()).toContainText("/release-notes");
});

test("a skill that asks for less effort lowers it for its message only, and never raises it", async ({ page }) => {
  const srv = extServer();
  const seen = await openPanel(page, srv, ["First answer.", "Second answer."], { signedIn: true });
  const model = await page.evaluate(() => { const m = window.ModelChoice.models.filter((x) => x.reasoning)[0]; window.ModelChoice.set(m.id, "high", "chat"); return m.id; });
  expect(model).toBeTruthy();
  await page.locator("#chat-widget__input").fill("/release-notes for 2.0");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("First answer.");
  expect(seen.requests[0].reasoning_effort).toBe("low");
  await page.locator("#chat-widget__input").fill("a question with no skill");
  await page.locator("#chat-widget__submit").click();
  await expect(page.locator("#chat-widget__messages_history")).toContainText("Second answer.");
  expect(seen.requests[1].reasoning_effort).toBe("high"); // the person's own choice is back
});

test("/save-skill starts an Agent task that names the folder, the header and the actions that make the file", async ({ page }) => {
  const srv = extServer();
  const seen = await openPanel(page, srv, [{ json: { say: "I will write it.", actions: [], done: true } }], { signedIn: true });
  await page.locator("#chat-widget__input").fill("/save-skill release-notes");
  await page.locator("#chat-widget__submit").click();
  await expect.poll(() => seen.requests.length).toBeGreaterThan(0);
  const first = JSON.stringify(seen.requests[0].messages);
  expect(first).toContain(".genie/skills/release-notes.md");
  expect(first).toContain("files_new");
  expect(first).toContain("editor_write");
  expect(first).toContain("name: release-notes");
  await expect(page.locator('#chat-widget__mode button[data-mode="agent"]')).toHaveAttribute("aria-pressed", "true");
});

test("a command in the box is drawn in the colour of a link, and Backspace right after it takes the whole command out", async ({ page }) => {
  const srv = extServer();
  await openPanel(page, srv, [], { signedIn: true });
  const input = page.locator("#chat-widget__input");
  const cmd = page.locator("#chat-widget__hl .cw-cmd");
  await input.click();
  await input.pressSequentially("let's /explain this and /context and /remember");
  // /explain is a skill and /context a command that only opens something: both count anywhere; /remember uses the words after it, so it counts at the start only
  await expect(cmd).toHaveCount(2);
  await expect(cmd.nth(0)).toHaveText("/explain");
  await expect(cmd.nth(1)).toHaveText("/context");
  const colours = await page.evaluate(() => {
    const c = document.querySelector("#chat-widget__hl .cw-cmd");
    const plain = document.getElementById("chat-widget__hl");
    return { cmd: getComputedStyle(c).color, text: getComputedStyle(plain).color };
  });
  expect(colours.cmd).not.toBe(colours.text);
  // the box shows only the caret: its own text is not drawn twice
  expect(await input.evaluate((e) => getComputedStyle(e).color)).toBe("rgba(0, 0, 0, 0)");
  await input.fill("/context now");
  await expect(cmd).toHaveCount(1);
  await expect(cmd.first()).toHaveText("/context");
  // Backspace with the caret right after the name takes the whole name out
  await input.fill("please /explain");
  await expect(cmd).toHaveCount(1);
  await input.press("Backspace");
  await expect(input).toHaveValue("please ");
  await expect(cmd).toHaveCount(0);
  // a space after the name: the first Backspace takes the space, the next the name
  await input.fill("please /explain ");
  await input.press("Backspace");
  await expect(input).toHaveValue("please /explain");
  await input.press("Backspace");
  await expect(input).toHaveValue("please ");
  // half a name, or anything that is not a command, is deleted one letter at a time
  await input.fill("/expl");
  await input.press("Backspace");
  await expect(input).toHaveValue("/exp");
  await input.fill("hello");
  await input.press("Backspace");
  await expect(input).toHaveValue("hell");
  // one Undo puts the command back
  await input.fill("");
  await input.pressSequentially("write /explain");
  await input.press("Backspace");
  await expect(input).toHaveValue("write ");
  await input.press("ControlOrMeta+z");
  await expect(input).toHaveValue("write /explain");
  // a sent message empties the box and the drawn text with it
  await input.fill("/explain");
  await expect(cmd).toHaveCount(1);
  await page.locator("#chat-widget__submit").click();
  await expect(input).toHaveValue("");
  await expect(cmd).toHaveCount(0);
});
