// Site settings in sections (src/resources/js/admin.js: viewSite, admin-sections.js, css/admin.css): the list of sections, one open at a time, the
// address of a section and of a setting, the search, the dots and the line under Save, and the keys and admins that come later. The admin API is a
// stand-in made from the real replies (tests/fixtures); the routes are covered by the Go tests.
//
// GENIE_LOCAL_ASSETS=1 serves the admin page's files from this checkout, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));

async function stub(page, opts = {}) {
  const st = { saved: [], keysAt: 0 };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, type, file] of [["/js/admin.js", "application/javascript", "js/admin.js"], ["/js/admin-sections.js", "application/javascript", "js/admin-sections.js"], ["/js/admin-site.js", "application/javascript", "js/admin-site.js"], ["/css/admin.css", "text/css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: type, body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  let settings = fixture("admin-settings.json");
  await page.route("**/admin/settings", (r) => {
    if (r.request().method() === "POST") {
      const b = r.request().postDataJSON();
      st.saved.push(b);
      settings = Object.assign({}, settings, b, { genie: Object.assign({}, settings.genie, b.genie) });
    }
    json(r, settings);
  });
  await page.route("**/admin/keys", async (r) => {
    st.keysAt = Date.now();
    if (opts.keysDelay) await new Promise((x) => setTimeout(x, opts.keysDelay));
    json(r, fixture("admin-keys.json"));
  });
  await page.route("**/admin/admins", (r) => json(r, fixture("admin-admins.json")));
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route(/\/admin\/(health|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  return st;
}

const section = (page, id) => page.locator(`.site-section[data-section="${id}"]`);

test("Site settings opens on a section, shows only that one, and the list leads to the others", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site");
  await expect(section(page, "general")).toBeVisible();
  await expect(page).toHaveURL(/#site\/general$/);
  // one section in the page: the cards of the others are not
  await expect(page.locator(".card", { hasText: "Genie limits" })).toHaveCount(0);
  await expect(page.locator(".site-section")).toHaveCount(1);
  const nav = page.locator(".site-nav");
  for (const name of ["General", "Workspaces", "Models and prices", "Limits and context", "Agent", "Chats and credits", "API keys and admins"]) await expect(nav.getByRole("link", { name })).toBeVisible();
  await nav.getByRole("link", { name: "Limits and context" }).click();
  await expect(section(page, "genie-limits")).toBeVisible();
  await expect(page).toHaveURL(/#site\/genie-limits$/);
  await expect(page.locator(".card", { hasText: "Genie limits" })).toBeVisible();
  await expect(page.locator(".card", { hasText: "Announcement" })).toHaveCount(0);
  // the one Save bar stays in view in every section
  await expect(page.locator(".save-bar")).toBeVisible();
  // the back button goes back to the section before
  await page.goBack();
  await expect(section(page, "general")).toBeVisible();
});

test("an address opens the section of a setting, and the last section used opens next time", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/genie-chats/sum-tokens");
  await expect(section(page, "genie-chats")).toBeVisible();
  const row = page.locator('[data-anchor="sum-tokens"]');
  await expect(row).toBeVisible();
  await expect(row).toBeInViewport();
  await expect(row).toHaveClass(/site-ring/);
  await expect(page.locator("#f-sum-tokens")).toBeFocused();
  // leave and come back by the plain address: the section that was last open
  await page.goto("/admin#overview");
  await page.goto("/admin#site");
  await expect(section(page, "genie-chats")).toBeVisible();
});

test("the models section has tabs, and a setting on another tab is reached by its address", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/genie-models");
  await expect(page.getByRole("tab", { name: "Models", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".card", { hasText: "Genie prices and cost" })).toBeHidden();
  await page.getByRole("tab", { name: "Prices and cost" }).click();
  await expect(page.locator(".card", { hasText: "Genie prices and cost" })).toBeVisible();
  await expect(page.locator(".card", { hasText: "The AI helper" })).toBeHidden();
  // a setting of the third tab, from the address
  await page.goto("/admin#overview");
  await page.goto("/admin#site/genie-models/show-cost");
  await expect(page.locator('[data-anchor="show-cost"]')).toBeVisible();
});

test("search finds a setting by a word of its name or its help, and goes to it", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/general");
  await page.keyboard.press("/");
  await expect(page.locator("#site-search")).toBeFocused();
  await page.keyboard.type("summar");
  const hits = page.locator(".site-hit");
  await expect(hits.first()).toContainText("Chats and credits");
  await expect(hits.first()).toContainText("ummar");
  await page.keyboard.press("Enter");
  await expect(section(page, "genie-chats")).toBeVisible();
  await expect(page.locator('[data-anchor="sum-tokens"]')).toBeInViewport();
  // nothing found: it says so
  await page.locator("#site-search").fill("zzzz");
  await expect(page.locator(".site-none")).toBeVisible();
  // Escape clears it
  await page.keyboard.press("Escape");
  await expect(page.locator("#site-search")).toHaveValue("");
  await expect(page.locator(".site-results")).toBeHidden();
});

test("a change puts a dot on its section and a line under Save, and Save saves the sections together", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/general");
  await expect(page.locator(".save-bar")).toContainText("All changes are saved.");
  await expect(page.locator(".site-dot:visible")).toHaveCount(0);
  await page.locator("#f-colour").evaluate((e) => e.click());
  await expect(page.locator('.site-dot[data-dot="general"]')).toBeVisible();
  await page.locator(".site-nav").getByRole("link", { name: "Limits and context" }).click();
  await page.locator("#f-ctx-editor").fill("9000");
  await expect(page.locator('.site-dot[data-dot="genie-limits"]')).toBeVisible();
  await expect(page.locator(".save-bar")).toContainText("2 changes: 1 in General, 1 in Limits and context");
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].colorOfTheDay).toBe(true);
  expect(st.saved[0].genie.contextEditorChars).toBe(9000);
  // after the save the form is drawn again, on the same section, with no dots
  await expect(section(page, "genie-limits")).toBeVisible();
  await expect(page.locator(".site-dot:visible")).toHaveCount(0);
});

test("Only settings with a value hides the ones that use the built-in value", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/genie-limits");
  const rows = page.locator(".site-section .form-row");
  const all = await rows.count();
  await page.getByLabel("Show only the settings that have a value of their own").check();
  const shown = await page.locator(".site-section .form-row:visible").count();
  expect(shown).toBeLessThan(all);
  await page.getByLabel("Show only the settings that have a value of their own").uncheck();
  expect(await page.locator(".site-section .form-row:visible").count()).toBe(all);
});

test("the keys and the admins come when they come: the settings are drawn without waiting for them", async ({ page }) => {
  await stub(page, { keysDelay: 2500 });
  const t0 = Date.now();
  await page.goto("/admin#site/general");
  await expect(section(page, "general")).toBeVisible();
  expect(Date.now() - t0).toBeLessThan(2400); // the keys take 2.5 s: the page did not wait for them
  await page.locator(".site-nav").getByRole("link", { name: "API keys and admins" }).click();
  await expect(page.locator(".card", { hasText: "API keys" })).toBeVisible({ timeout: 8000 });
  await expect(page.locator(".card", { hasText: "OpenAI" }).first()).toBeVisible({ timeout: 8000 });
});

test("leaving Site settings with a change asks, moving between its sections does not", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/general");
  await page.locator("#f-colour").evaluate((e) => e.click());
  await page.locator(".site-nav").getByRole("link", { name: "Agent" }).click();
  await expect(section(page, "genie-agent")).toBeVisible();
  await expect(page.locator("dialog[open]")).toHaveCount(0);
  await page.locator("#nav").getByRole("link", { name: "Feedback" }).click();
  await expect(page.locator("dialog[open]")).toContainText("Discard your unsaved changes?");
});

test("the code of Site settings is loaded when the page is opened, not with the dashboard", async ({ page }) => {
  await stub(page);
  const loaded = [];
  page.on("request", (r) => { const u = new URL(r.url()); if (/\/js\/admin(-site|-sections)?\.js$/.test(u.pathname)) loaded.push(u.pathname.split("/").pop()); });
  await page.goto("/admin#overview");
  await expect(page.locator("#nav")).toContainText("Site settings");
  expect(loaded).toEqual(["admin.js"]);
  await page.locator("#nav").getByRole("link", { name: "Site settings" }).click();
  await expect(section(page, "general")).toBeVisible();
  expect(loaded).toEqual(["admin.js", "admin-sections.js", "admin-site.js"]);
  // opened again: not loaded again
  await page.locator("#nav").getByRole("link", { name: "Overview" }).click();
  await page.locator("#nav").getByRole("link", { name: "Site settings" }).click();
  await expect(section(page, "general")).toBeVisible();
  expect(loaded.length).toBe(3);
});

test("the models for Agent mode: the ones that are not small start ticked, an unrelated save sends no list, and the Agent model is one of the ticked", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/genie-models");
  const tick = (name) => page.getByLabel("Agent mode: " + name, { exact: true });
  await expect(page.locator(".card", { hasText: "Models for Agent mode" })).toHaveCount(0);
  await expect(page.locator(".card", { hasText: "Default models" })).toBeVisible();
  // not small: ticked; small: not
  await expect(tick("GPT-6 Luna")).toBeChecked();
  await expect(tick("GPT-4o mini")).not.toBeChecked();
  await expect(tick("Gemma 4 31B")).not.toBeChecked();
  // the Agent model can be a ticked one only
  const agentModel = page.locator("#f-agentmodel");
  await expect(agentModel.locator("option")).toHaveText(["Built in (the first model that is not small)", "GPT-6 Luna"]);
  // an unrelated change: no list is sent (every model that is on keeps working)
  await page.locator(".site-nav").getByRole("link", { name: "General" }).click();
  await page.locator("#f-colour").evaluate((e) => e.click());
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].genie.agentModels).toBeNull();
  // tick a small model, choose the Agent model and an effort, save: the list is sent
  await page.locator(".site-nav").getByRole("link", { name: "Models and prices" }).click();
  await tick("GPT-4o mini").check();
  await expect(agentModel.locator("option")).toHaveText(["Built in (the first model that is not small)", "GPT-6 Luna", "GPT-4o mini"]);
  await agentModel.selectOption("gpt-4o-mini");
  await page.locator("#f-agenteffort").selectOption("high");
  await expect(page.locator('.site-dot[data-dot="genie-models"]')).toBeVisible();
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(2);
  expect(st.saved[1].genie.agentModels).toEqual(expect.arrayContaining(["gpt-6-luna", "gpt-4o-mini"]));
  expect(st.saved[1].genie.agentModels).not.toContain("google/gemma-4-31b-it");
  expect(st.saved[1].genie.agentModel).toBe("gpt-4o-mini");
  expect(st.saved[1].genie.agentEffort).toBe("high");
});

test("unticking every model says so, and a model that is taken off the list is not the Agent model any more", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/genie-models");
  const tick = (name) => page.getByLabel("Agent mode: " + name, { exact: true });
  await tick("GPT-4o mini").check();
  await page.locator("#f-agentmodel").selectOption("gpt-4o-mini");
  await tick("GPT-4o mini").uncheck();
  await expect(page.locator("#f-agentmodel")).toHaveValue("");
  for (const name of ["GPT-6 Luna"]) await tick(name).uncheck();
  await expect(page.getByText("Tick Agent mode on at least one model", { exact: false })).toBeVisible();
});

test("an added model has its own ticks: small, Agent mode and available for guest users, and they are saved", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/genie-models");
  await page.getByRole("tab", { name: "Added models" }).click();
  const nemo = page.locator(".custom-model", { hasText: "nvidia/nemotron-3-super-120b-a12b:free" });
  // 120B: not small; guests may use it; ticked for Agent mode (it is not small)
  await expect(nemo.getByLabel("nvidia/nemotron-3-super-120b-a12b:free is a small model")).not.toBeChecked();
  await expect(nemo.getByLabel("nvidia/nemotron-3-super-120b-a12b:free is available for guest users")).toBeChecked();
  await expect(nemo.getByLabel("Agent mode: Nemotron 3 Super (free)")).toBeChecked();
  await nemo.getByLabel("nvidia/nemotron-3-super-120b-a12b:free is available for guest users").uncheck();
  await nemo.getByLabel("nvidia/nemotron-3-super-120b-a12b:free is a small model").check();
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  const saved = st.saved[0].genie.customModels.find((m) => m.id === "nvidia/nemotron-3-super-120b-a12b:free");
  expect(saved.noGuests).toBe(true);
  expect(saved.small).toBe(true);
  // the other added model was not touched: its small tick stays as its id says (null)
  const other = st.saved[0].genie.customModels.find((m) => m.id === "cohere/north-mini-code:free");
  expect(other.small).toBeNull();
  expect(other.noGuests).toBe(false);
});

test("every built-in model has the ticks too: Agent mode and available for guest users, saved as the list of models kept for signed-in users", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/genie-models");
  const luna = page.locator(".row, .setting, div", { has: page.getByLabel("Agent mode: GPT-6 Luna", { exact: true }) }).last();
  await expect(page.getByLabel("GPT-6 Luna is available for guest users")).toBeChecked();
  await expect(page.getByLabel("GPT-4o mini is available for guest users")).toBeChecked();
  await page.getByLabel("GPT-6 Luna is available for guest users").uncheck();
  await expect(page.locator('.site-dot[data-dot="genie-models"]')).toBeVisible();
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].genie.guestBlockedModels).toEqual(["gpt-6-luna"]);
  void luna;
});

test("Practice saved in the workspace is a switch on the Workspaces section, and off is saved", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/workspaces");
  await expect(page.locator(".card", { hasText: "Practice is saved in the workspace" })).toBeVisible();
  await page.locator("#f-practice-ws").evaluate((e) => e.click());
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].practiceWorkspaceOff).toBe(true);
});
