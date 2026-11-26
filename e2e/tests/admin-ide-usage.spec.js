// The "IDE data usage" section of Site settings (canvas "IDE data usage and limits", board 7): Metering, Terminal speed, Daily total Free plan, Plans, and the
// column of today's numbers. The admin API is a stand-in made from the real replies (tests/fixtures); the routes and the counting are covered by the Go tests.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));
const MB = 1048576;

async function stub(page, ideSettings = {}) {
  const st = { saved: [], resets: 0 };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, file] of [["/js/admin.js", "js/admin.js"], ["/js/admin-sections.js", "js/admin-sections.js"], ["/js/admin-site.js", "js/admin-site.js"], ["/css/admin.css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: u.endsWith("css") ? "text/css" : "application/javascript", body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  let settings = Object.assign(fixture("admin-settings.json"), ideSettings);
  await page.route("**/admin/settings", (r) => {
    if (r.request().method() === "POST") {
      const b = r.request().postDataJSON();
      st.saved.push(b);
      settings = Object.assign({}, settings, b, { genie: Object.assign({}, settings.genie, b.genie) });
    }
    json(r, settings);
  });
  await page.route("**/admin/keys", (r) => json(r, fixture("admin-keys.json")));
  await page.route("**/admin/admins", (r) => json(r, fixture("admin-admins.json")));
  await page.route("**/admin/plugin-installs", (r) => json(r, { entries: [], counts: [], days: 90 }));
  await page.route("**/admin/connection-requests", (r) => json(r, { requests: [], waiting: 0 }));
  await page.route("**/admin/connections", (r) => json(r, { connected: [], calls: [] }));
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route("**/admin/ide-usage**", (r) => {
    const parts = (t) => ({ terminal: t * 0.6 * MB, genie: t * 0.2 * MB, files: t * 0.1 * MB, editor: t * 0.08 * MB, preview: t * 0.02 * MB });
    if (r.request().method() === "POST") { st.resets++; return json(r, { ok: true }); }
    json(r, { day: "2026-11-25", total: 3.4 * 1024 * MB, parts: {}, guestBytes: 900 * MB, guestsNow: 31, signedCount: 86, atLimit: 9,
      top: [["Asha", 200], ["Ben", 196], ["Chen", 142], ["Dee", 120], ["Eli", 96]].map((u) => ({ key: u[0].toLowerCase(), uid: "uid-" + u[0], name: u[0], total: u[1] * MB, limit: 200 * MB, parts: parts(u[1]) })) });
  });
  await page.route(/\/admin\/(health|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  return st;
}

const section = (page) => page.locator('.site-section[data-section="ide-usage"]');

test("the section has the four cards and the column of today's numbers, with the built-in numbers as hints", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#site/ide-usage");
  await expect(section(page)).toBeVisible();
  await expect(page.locator(".site-nav").getByRole("link", { name: "IDE data usage" })).toBeVisible();
  for (const title of ["Metering", "Terminal speed", "Daily total, Free plan", "History", "Needs a look", "Plans", "Today, all users", "Top today", "Per user"]) {
    await expect(section(page).locator(".card h2", { hasText: new RegExp("^" + title + "$") })).toBeVisible();
  }
  // metering and exempt admins are on; all five parts are counted
  await expect(page.locator("#f-ide-on")).toBeChecked();
  await expect(page.locator("#f-ide-admins")).toBeChecked();
  for (const p of ["terminal", "editor", "files", "genie", "preview"]) await expect(page.locator("#f-ide-part-" + p)).toBeChecked();
  // the numbers of the canvas are the hints
  const hints = { "f-ide-term-mb": "20", "f-ide-term-window": "10", "f-ide-term-grace": "5", "f-ide-guest-mb": "50", "f-ide-user-mb": "200", "f-ide-warn-pct": "80", "f-ide-reserve-mb": "2", "f-ide-small-kb": "256" };
  for (const [id, ph] of Object.entries(hints)) await expect(page.locator("#" + id)).toHaveAttribute("placeholder", ph);
  await expect(page.locator("#f-ide-term-action")).toHaveValue("end");
  await expect(page.locator("#f-ide-at-limit option")).toHaveText("Stop heavy parts, keep reserve");
  await expect(page.locator("#f-ide-reset")).toHaveValue("00:00");
  await expect(section(page)).toContainText("Guest, per session (1 hour) (MB)");
  await expect(section(page)).toContainText("Free (active)");
  await expect(section(page)).toContainText("defined later");
  // today, all users, and the top accounts, from the server
  await expect(section(page).locator('[data-ide="today"]')).toContainText("3.40 GB");
  await expect(section(page).locator('[data-ide="today"]')).toContainText("31 sessions now");
  await expect(section(page).locator('[data-ide="today"]')).toContainText("86 accounts");
  await expect(section(page).locator('[data-ide="today"]')).toContainText("At the limit now: 9");
  await expect(section(page).locator('[data-ide="top"]')).toContainText("Asha");
  await expect(section(page).locator('[data-ide="top"]')).toContainText("200 MB");
  await expect(section(page).locator('[data-ide="top"]')).toContainText("Open the Data usage tab for the days");
  await expect(section(page).locator('[data-ide="top"] a')).toHaveAttribute("href", "#traffic/data");
  // the numbers are classes of the page's own style sheet (its policy blocks style attributes set by script), so the big total is big and a row keeps its layout
  await expect(section(page).locator('[data-ide="today"] .ide-big')).toBeVisible();
  expect(await section(page).locator('[data-ide="today"] .ide-big').evaluate((e) => parseFloat(getComputedStyle(e).fontSize))).toBeGreaterThan(24);
  expect(await section(page).locator('[data-ide="top"] .ide-top-row').first().evaluate((e) => getComputedStyle(e).display)).toBe("flex");
  // history and the flags: the built-in numbers are the hints
  const hints2 = { "f-ide-history-days": "90", "f-ide-flag-limit-days": "3", "f-ide-flag-jump": "5", "f-ide-flag-near": "90", "f-ide-flag-stops": "5" };
  for (const [id, ph] of Object.entries(hints2)) await expect(page.locator("#" + id)).toHaveAttribute("placeholder", ph);
  await expect(page.locator("#f-ide-flag-log")).not.toBeChecked();
  await expect(section(page).locator('[data-card="Needs a look"]')).toContainText("An email to the admins");
  await expect(section(page).locator('[data-card="Top today"]')).toContainText("Guests are only counted, never listed");
});

test("a change puts a dot on the section, and Save sends the numbers", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#site/ide-usage");
  await expect(page.locator('.site-dot[data-dot="ide-usage"]')).toBeHidden();
  await page.locator("#f-ide-term-mb").fill("30");
  await page.locator("#f-ide-guest-mb").fill("40");
  await page.locator("#f-ide-user-mb").fill("300");
  await page.locator("#f-ide-warn-pct").fill("75");
  await page.locator("#f-ide-term-action").selectOption("pause");
  await page.locator("#f-ide-reset").fill("05:30");
  await page.locator("#f-ide-history-days").fill("180");
  await page.locator("#f-ide-flag-limit-days").fill("2");
  await page.locator("#f-ide-flag-jump").fill("8");
  await page.locator("#f-ide-flag-near").fill("85");
  await page.locator("#f-ide-flag-stops").fill("9");
  await page.locator("#f-ide-flag-log").evaluate((e) => e.click());
  await page.locator("#f-ide-admins").evaluate((e) => e.click());
  await page.locator("#f-ide-part-preview").evaluate((e) => e.click());
  await expect(page.locator('.site-dot[data-dot="ide-usage"]')).toBeVisible();
  // the Free plan row follows the numbers as they are typed
  await expect(section(page).locator('[data-card="Plans"]')).toContainText("guest 40 MB per session, signed in 300 MB a day");
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  const b = st.saved[0];
  expect(b).toMatchObject({ ideTermMB: 30, ideGuestMB: 40, ideUserMB: 300, ideWarnPct: 75, ideTermPauseOnly: true, ideResetMinutes: 330, ideAdminsCounted: true, ideUsageOff: false, ideHistoryDays: 180, ideFlagLimitDays: 2, ideFlagJump: 8, ideFlagNearPct: 85, ideFlagStops: 9, ideFlagLog: true });
  expect(b.idePartsOff).toEqual(["preview"]);
  // the reset time is also shown in the admin's own time zone
  await expect(section(page).locator('[data-card="Daily total, Free plan"]')).toContainText(/UTC, in your time/);
});

test("saved settings come back into the form", async ({ page }) => {
  await stub(page, { ideUsageOff: true, idePartsOff: ["genie", "editor"], ideTermMB: 8, ideTermPauseOnly: true, ideResetMinutes: 90, ideUserMB: 500 });
  await page.goto("/admin#site/ide-usage");
  await expect(page.locator("#f-ide-on")).not.toBeChecked();
  await expect(page.locator("#f-ide-part-genie")).not.toBeChecked();
  await expect(page.locator("#f-ide-part-editor")).not.toBeChecked();
  await expect(page.locator("#f-ide-part-terminal")).toBeChecked();
  await expect(page.locator("#f-ide-term-mb")).toHaveValue("8");
  await expect(page.locator("#f-ide-term-action")).toHaveValue("pause");
  await expect(page.locator("#f-ide-reset")).toHaveValue("01:30");
  await expect(page.locator("#f-ide-user-mb")).toHaveValue("500");
});

// the Users drawer has a "Data today" line and a button that resets the count by hand; the Usage view lists the day
test("a user's drawer says what the IDE sent them today, and the button resets it", async ({ page }) => {
  const st = await stub(page);
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  let used = 150;
  await page.route("**/admin/ide-usage?uid=*", (r) => {
    if (r.request().method() === "POST") { used = 0; st.resets++; return json(r, { ok: true }); }
    json(r, { state: { used: used * MB, limit: 200 * MB, atLimit: used >= 200, parts: { terminal: used * 0.6 * MB, genie: used * 0.2 * MB, files: used * 0.1 * MB, editor: used * 0.08 * MB, preview: used * 0.02 * MB } }, history: [] });
  });
  await page.route(/\/admin\/users(\?.*)?$/, (r) => json(r, { users: [{ uid: "u1", email: "asha@example.com", name: "Asha", sessions: 0, lastSignIn: Date.now() - 3600e3, usage: { bars: [], visits: 1, activeSecs: 600, days30: 1, topLang: "python", lastVisitSecs: 60, lastActive: Date.now() - 3600e3, terminals: 2 } }] }));
  await page.route("**/admin/users/u1/usage", (r) => json(r, { usage: { visits: 1, activeSecs: 600, dayMins: new Array(30).fill(0), languages: [], devices: [], recent: [], terminals: 0, avgVisitSecs: 600, since: "2026-11-01" }, genieAnswers: 0 }));
  await page.goto("/admin#users");
  await page.getByText("asha@example.com").click();
  const drawer = page.locator(".drawer, [role=dialog], aside").filter({ hasText: "Data today" }).first();
  await expect(drawer).toContainText("150 MB of 200 MB today (terminal 90 MB, Genie 30 MB, files 15 MB, editor 12 MB, preview 3.0 MB).");
  await page.getByRole("button", { name: "Reset today's count" }).click();
  await expect(drawer).toContainText("0 KB of 200 MB today");
  expect(st.resets).toBe(1);
});

test("the Usage view lists the day's data by account, with the guests only as a total", async ({ page }) => {
  await stub(page);
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  await page.route(/\/admin\/usage(\?.*)?$/, (r) => json(r, {
    since: "2026-11-01", days: [{ date: "2026-11-24", signed: 3, guests: 9 }, { date: "2026-11-25", signed: 4, guests: 12 }], languages: [], sources: [], devices: [], countries: [], funnel: [], cohorts: [],
    avgSigned: 3, avgGuests: 10, avgVisitSecs: 300, avgSignedVisitSecs: 400, avgGuestVisitSecs: 200, guestSignupPct: 4, newAccounts: 2, back7Pct: 30 }));
  await page.goto("/admin#traffic");
  const card = page.locator(".card", { hasText: "IDE data today" });
  await expect(card).toContainText("3.40 GB in all");
  await expect(card).toContainText("guests (combined, never listed) 900 MB");
  await expect(card.locator("tr", { hasText: "Asha" })).toContainText("200 MB");
  await expect(card.locator("tr", { hasText: "Asha" })).toContainText("100%");
  await expect(card.locator("th", { hasText: "Preview" })).toBeVisible();
});
