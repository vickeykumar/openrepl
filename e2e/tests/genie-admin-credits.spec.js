// The admin's side of the Genie credits, plans and measures (resources/js/admin.js): the card in Site settings and the
// numbers of the agent in Genie usage. The admin API is a stand-in made from the real replies (tests/fixtures); the routes are
// covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves admin.js from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ADMIN_HTML = fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));

async function stub(page, measures) {
  const st = { saved: [], baselines: 0 };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/admin.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin.js")) }));

    await page.route((u) => u.pathname.endsWith("/js/admin-sections.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin-sections.js")) }));
    await page.route((u) => u.pathname.endsWith("/js/admin-site.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin-site.js")) }));
    await page.route((u) => u.pathname.endsWith("/css/admin.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "css", "admin.css")) }));
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  let settings = fixture("admin-settings.json");
  await page.route("**/admin/settings", (r) => {
    if (r.request().method() === "POST") {
      const b = r.request().postDataJSON();
      st.saved.push(b);
      settings = Object.assign({}, settings, { genie: Object.assign({}, settings.genie, b.genie) });
    }
    json(r, settings);
  });
  await page.route("**/admin/keys", (r) => json(r, fixture("admin-keys.json")));
  await page.route("**/admin/admins", (r) => json(r, fixture("admin-admins.json")));
  await page.route("**/admin/plugin-installs", (r) => json(r, { entries: [], counts: [], days: 90 }));
  await page.route("**/admin/ide-usage**", (r) => json(r, { day: "2026-11-25", total: 0, parts: {}, top: [] }));
  await page.route("**/admin/connection-requests", (r) => json(r, { requests: [], waiting: 0 }));
  await page.route("**/admin/connections", (r) => json(r, { connected: [], calls: [] }));
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route(/\/admin\/(health|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  await page.route("**/admin/genie-usage**", (r) => json(r, { range: "month", month: "2026-11", users: [], visitors: {}, total: { answers: 0, in: 0, out: 0, cost: 0 }, pricesSet: false }));
  await page.route("**/admin/genie-measures", (r) => {
    if (r.request().method() === "POST") {
      st.baselines++;
      measures = Object.assign({}, measures, { base: measures.now, baseAt: 1794700000 });
    }
    json(r, measures);
  });
  return st;
}

const zero = { tasks: 0, steps: 0, reqBytes: 0, stepLimit: 0, budgetStop: 0, unreadable: 0, tooLong: 0, stopped: 0, done: 0, failed: 0, checked: 0, notChecked: 0, carried: 0 };

// One section of Site settings is in the page at a time: go to another by its address, and wait for it.
async function toSection(page, id, cardTitle) {
  await page.evaluate((h) => { location.hash = h; }, "#site/" + id);
  await expect(page.locator(`.site-section[data-section="${id}"]`)).toBeVisible();
  if (cardTitle) await expect(page.locator(".card", { hasText: cardTitle }).first()).toBeVisible();
}

test("Site settings has the usage record and credits: today's behaviour, and the numbers are saved", async ({ page }) => {
  const st = await stub(page, { now: zero });
  await page.goto("/admin#site/genie-chats");
  const card = page.locator(".card", { hasText: "Usage record and credits" });
  await expect(card).toBeVisible();
  // as it was: the record on, credits off, the plan empty
  await expect(card.locator("#f-meter-on")).toBeChecked();
  await expect(card.locator("#f-credits-on")).not.toBeChecked();
  await expect(card.locator("#f-plan-credits")).toHaveValue("");
  await expect(card).toContainText("Credits: ");
  await expect(card).toContainText("a dollar for a million tokens is a credit for a thousand");
  await toSection(page, "genie-agent", "Agent mode");
  await expect(page.locator("#f-stream-on")).not.toBeChecked();
  await expect(page.locator("#f-agent-tools")).not.toBeChecked();
  // the models that take the tool: the OpenAI ones until the admin chooses
  await expect(page.locator("#f-tool-gpt-6-luna")).toBeChecked();
  await expect(page.locator("#f-tool-gpt-4o-mini")).toBeChecked();
  await expect(page.locator("#f-tool-google-gemma-4-31b-it")).not.toBeChecked();
  // change things in two sections, and save them together with the one Save button
  await page.locator("#f-stream-on").evaluate((e) => e.click());
  await page.locator("#f-agent-tools").evaluate((e) => e.click());
  await page.locator("#f-tool-google-gemma-4-31b-it").evaluate((e) => e.click());
  await page.locator("#f-tool-gpt-6-luna").evaluate((e) => e.click());
  await expect(page.locator('.site-dot[data-dot="genie-agent"]')).toBeVisible();
  await toSection(page, "genie-chats", "Usage record and credits");
  await card.locator("#f-credits-on").evaluate((e) => e.click());
  await card.locator("#f-plan-name").fill("Team");
  await card.locator("#f-plan-credits").fill("500");
  await card.locator("#f-plan-budget").fill("40");
  const w = card.locator('input[id^="f-weight-in-"]').first();
  await w.fill("0.2");
  // the chat summary: on by default, 6000 when empty
  await expect(page.locator("#f-sum-on")).toBeChecked();
  await expect(page.locator("#f-sum-tokens")).toHaveAttribute("placeholder", "6000");
  await page.locator("#f-sum-tokens").fill("9000");
  await expect(page.locator(".save-bar")).toContainText(/changes: .* in Agent, .* in Chats and credits|changes: .* in Chats and credits/);
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  const g = st.saved[0].genie;
  expect(g).toMatchObject({ creditsOn: true, meterOff: false, planName: "Team", planCredits: 500, planBudget: 40, streamAnswers: true, agentNativeTools: true, summaryOff: false, summaryTokens: 9000 });
  expect(Object.values(g.creditWeights)[0].in).toBe(0.2);
  expect(g.nativeToolModels).toEqual(["gpt-4o-mini", "google/gemma-4-31b-it"]);
});

test("Genie usage shows how the agent is doing, and records a baseline", async ({ page }) => {
  const now = { ...zero, tasks: 10, steps: 52, reqBytes: 520000, stepLimit: 2, unreadable: 1, tooLong: 0, stopped: 1, done: 7, failed: 2, checked: 4, notChecked: 2, toolTries: 20, toolFallbacks: 5, toolRefused: { "google/gemma-4-31b-it": 5 } };
  const st = await stub(page, { now });
  await page.goto("/admin#usage");
  const card = page.locator(".card", { hasText: "How the agent is doing" });
  await expect(card).toBeVisible();
  await expect(card).toContainText("Steps per task");
  await expect(card.locator("tr", { hasText: "Steps per task" })).toContainText("5.2");
  await expect(card.locator("tr", { hasText: "Size of a step" })).toContainText("10 KB");
  await expect(card.locator("tr", { hasText: "stopped at the step limit" })).toContainText("20%");
  await expect(card.locator("tr", { hasText: "ended with a check" })).toContainText("67%");
  await expect(card.locator("tr", { hasText: "Steps asked as a native tool call" })).toContainText("20");
  await expect(card.locator("tr", { hasText: "refused by the host and sent again" })).toContainText("25%");
  await expect(card.locator("tr", { hasText: "Refused, by model" })).toContainText("google/gemma-4-31b-it: 5");
  await card.getByRole("button", { name: "Record today as the baseline" }).click();
  await expect.poll(() => st.baselines).toBe(1);
  await expect(card.getByRole("button", { name: "Record today as the new baseline" })).toBeVisible();
  await expect(card.locator("th", { hasText: "Baseline (" })).toBeVisible();
});

test("Site settings has the chat history card: on by default, and its numbers are saved", async ({ page }) => {
  const st = await stub(page, { now: zero });
  await page.goto("/admin#site/genie-chats");
  const card = page.locator(".card", { hasText: "Genie chat history" });
  await expect(card).toBeVisible();
  await expect(card.locator("#f-chat-on")).toBeChecked();
  await expect(card.locator("#f-chat-guests")).toBeChecked();
  await expect(card).toContainText("Nobody, an admin included, can read a chat");
  await card.locator("#f-chat-guests").evaluate((e) => e.click());
  await card.locator("#f-chat-max").fill("30");
  await card.locator("#f-chat-tabs").fill("6");
  await card.locator("#f-chat-kb").fill("100");
  await card.locator("#f-chat-days").fill("90");
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].genie).toMatchObject({ chatHistoryOff: false, chatHistoryNoGuests: true, chatHistoryChats: 30, chatHistoryTabs: 6, chatHistoryKB: 100, chatHistoryDays: 90 });
});
