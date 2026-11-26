// The Data usage tab of Site usage (canvas "IDE data usage: daily history and top users", boards 2 to 4): the days of all signed-in accounts by part, the accounts that need
// a look, the watch list, the top accounts of a period, and the wide drawer of one account. The admin API is a stand-in; the counting, the flags and the routes are covered
// by the Go tests (ideusage/history_test.go, server/ideusage_test.go).
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));
const MB = 1048576;
const PARTS = ["terminal", "genie", "files", "editor", "preview"];

function days(n) {
  const out = [];
  for (let i = 0; i < n; i++) {
    const d = new Date(Date.UTC(2026, 10, 26 - (n - 1 - i)));
    const total = (i === n - 5 ? 1300 : 300 + (i % 7) * 40) * MB;
    out.push({ day: d.toISOString().slice(0, 10), total, guests: 4 * MB, guestSessions: 3, parts: { terminal: total * 0.62, genie: total * 0.18, files: total * 0.1, editor: total * 0.06, preview: total * 0.04 } });
  }
  return out;
}
const acct = (uid, name, email, total, extra = {}) => Object.assign({
  uid, name, email, total: total * MB, avg: Math.round(total * MB / 30), series: Array.from({ length: 30 }, (_, i) => Math.round(total * MB / 30 * (1 + (i % 5) / 5))),
  limitDays: 0, run: 0, parts: { terminal: total * 0.6 * MB, genie: total * 0.2 * MB, files: total * 0.1 * MB, editor: total * 0.06 * MB, preview: total * 0.04 * MB },
  biggest: "terminal", today: Math.round(total / 30 * MB), peak: total / 10 * MB, peakDay: "2026-11-20", limit: 200 * MB, paused: 0, ended: 0, usedDays: 20,
}, extra);

async function stub(page, o = {}) {
  const st = { reports: [], accts: [], watch: [], resets: 0 };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, file] of [["/js/admin.js", "js/admin.js"], ["/js/admin-sections.js", "js/admin-sections.js"], ["/js/admin-site.js", "js/admin-site.js"], ["/css/admin.css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: u.endsWith("css") ? "text/css" : "application/javascript", body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, b) => r.fulfill({ contentType: "application/json", body: JSON.stringify(b) });
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route("**/admin/feedback", (r) => json(r, { unread: 0 }));
  await page.route("**/admin/settings", (r) => json(r, fixture("admin-settings.json")));
  await page.route(/\/admin\/usage(\?.*)?$/, (r) => json(r, {
    since: "2026-11-01", days: [{ date: "2026-11-25", signed: 4, guests: 12 }], languages: [], sources: [], devices: [], countries: [], funnel: [], cohorts: [],
    avgSigned: 3, avgGuests: 10, avgVisitSecs: 300, avgSignedVisitSecs: 400, avgGuestVisitSecs: 200, guestSignupPct: 4, newAccounts: 2, back7Pct: 30 }));
  const rows = [
    acct("uid-a", "Asha", "asha@example.com", 4100, { limitDays: 6, run: 3, today: 212 * MB, peak: 1300 * MB, peakDay: "2026-11-22", paused: 14, ended: 2, usedDays: 24 }),
    acct("uid-b", "Ben", "ben@example.com", 2400, { limitDays: 2, biggest: "genie", today: 96 * MB }),
    acct("uid-c", "Chen", "chen@example.com", 2100, { limitDays: 4, biggest: "preview", today: 188 * MB, parts: { terminal: 100 * MB, genie: 200 * MB, files: 100 * MB, editor: 100 * MB, preview: 1600 * MB } }),
    acct("uid-d", "Dee", "dee@example.com", 0.03, { today: 30 * 1024, biggest: "genie" }),
  ];
  await page.route("**/admin/ide-usage**", (r) => {
    const u = new URL(r.request().url());
    if (r.request().method() === "POST") {
      if (u.searchParams.get("watch")) { st.watch.push(u.searchParams.get("watch") + ":" + u.searchParams.get("uid")); return json(r, { ok: true }); }
      st.resets++;
      return json(r, { ok: true });
    }
    if (u.searchParams.get("report")) {
      const n = Number(u.searchParams.get("report"));
      st.reports.push(n);
      return json(r, {
        day: "2026-11-26", period: n, historyDays: 90, limit: 200 * MB, days: days(n), accounts: rows, accountsTotal: o.total || 142, sent: 18.4 * 1024 * MB, guests: 3.1 * 1024 * MB, counted: 142,
        atLimit: 9, limitAcctDays: 21, busiest: { day: "2026-11-24", total: 1300 * MB, part: "terminal" }, on: o.on !== false,
        flags: [
          { uid: "uid-a", name: "Asha", email: "asha@example.com", kind: "limit", text: "At the limit 3 days in a row. 212 MB today, mostly terminal.", today: 212 * MB },
          { uid: "uid-c", name: "Chen", kind: "jump", text: "8 times its usual day (usual 24 MB, today 188 MB), mostly preview.", today: 188 * MB },
          { uid: "uid-a", name: "Asha", kind: "stops", text: "A terminal was paused 14 times and ended 2 times for printing too fast.", today: 212 * MB },
          { uid: "uid-b", name: "Ben", kind: "watched", text: "On your watch list. 96 MB today, normal.", today: 96 * MB },
        ],
        watch: [{ uid: "uid-b", name: "Ben", email: "ben@example.com" }],
        thresholds: { limitDays: 3, jump: 5, nearPct: 90, stops: 5, log: false, historyDays: 90, minJumpMB: 20 },
      });
    }
    if (u.searchParams.get("acct")) {
      st.accts.push(u.searchParams.get("acct"));
      const dd = days(30).map((d) => ({ day: d.day, total: d.total / 6, parts: Object.fromEntries(PARTS.map((p) => [p, d.parts[p] / 6])) }));
      return json(r, { account: Object.assign(acct("uid-a", "Asha", "asha@example.com", 4100, { limitDays: 6, run: 3, today: 212 * MB, peak: 1300 * MB, peakDay: dd[25].day, usedDays: 24 }), { days: dd }),
        known: true, uid: "uid-a", name: "Asha", email: "asha@example.com", firstSignIn: Date.UTC(2026, 10, 3), watched: false, limit: 200 * MB, period: 30, historyDays: 90 });
    }
    json(r, { day: "2026-11-26", total: 0, parts: {}, guestBytes: 0, guestsNow: 0, signedCount: 0, atLimit: 0, top: [] });
  });
  await page.route(/\/admin\/(health|stats|audit|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  return st;
}

const tab = (page, name) => page.getByRole("tab", { name });

test("Site usage has two tabs, and the Data usage tab shows the stats, the chart, the strip and the table", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#traffic/data");
  await expect(tab(page, "Data usage")).toHaveAttribute("aria-selected", "true");
  await expect(tab(page, "Visitors")).toHaveAttribute("aria-selected", "false");
  await expect(page.locator(".page-head h1")).toHaveText("Data usage");
  await expect(page.getByText("Guests are only counted, never listed.").first()).toBeVisible();
  expect(st.reports).toContain(30); // 30 days is the first period
  for (const b of ["Today", "7 days", "30 days", "90 days"]) await expect(page.getByRole("button", { name: b, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "30 days", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("link", { name: "Download CSV" })).toHaveAttribute("href", /admin\/ide-usage\?report=30&format=csv/);
  // the four stat cards
  const stats = page.locator(".stat");
  await expect(stats.filter({ hasText: "Sent to signed-in users" })).toContainText("18.4 GB");
  await expect(stats.filter({ hasText: "Sent to signed-in users" })).toContainText("in 30 days");
  await expect(stats.filter({ hasText: "Accounts counted" })).toContainText("142");
  await expect(stats.filter({ hasText: "Reached the limit" })).toContainText("9");
  await expect(stats.filter({ hasText: "Reached the limit" })).toContainText("21 account-days");
  await expect(stats.filter({ hasText: "Busiest day" })).toContainText("1.3 GB");
  await expect(stats.filter({ hasText: "Busiest day" })).toContainText("mostly terminal");
  // the chart: a column for each day, stacked by part, with the limit line, and the guests as one number
  await expect(page.locator(".dchart .dcol")).toHaveCount(30);
  await expect(page.locator(".dchart .dlimit")).toHaveCount(1);
  await expect(page.locator(".dchart .dcol").first().locator(".dseg")).toHaveCount(5);
  await expect(page.locator(".dlegend").first()).toContainText("Terminal");
  await expect(page.getByText(/Guests, combined: 3\.1 GB in the period/)).toBeVisible();
  // the strip, with the reason of each flag; and the thresholds
  const strip = page.locator(".card", { has: page.getByRole("heading", { name: "Needs a look" }) });
  await expect(strip.locator(".flag-row")).toHaveCount(4);
  await expect(strip.locator(".flag-row", { hasText: "8 times its usual day" })).toContainText("Chen");
  await expect(strip.locator(".flag-row", { hasText: "too fast" })).toContainText("paused 14 times and ended 2 times");
  await expect(strip.locator(".flag-row", { hasText: "watched" })).toContainText("On your watch list");
  await expect(strip).toContainText("3 days in a row");
  await expect(strip).toContainText("Change these in Site settings");
  // the watch list
  await expect(page.locator(".chips .chip-x")).toHaveCount(1);
  await expect(page.locator(".chips .chip-name")).toHaveText("Ben");
  // the table: a trend for each account, the days at the limit, the biggest part, and a small account in KB
  const table = page.locator("table").first();
  await expect(table.locator("tbody tr")).toHaveCount(4);
  await expect(table.locator("tbody tr").first()).toContainText("Asha");
  await expect(table.locator("tbody tr").first()).toContainText("6 days");
  await expect(table.locator("tbody tr").first()).toContainText("Terminal");
  await expect(table.locator("tbody tr").first().locator("svg.dspark polyline")).toHaveCount(1);
  await expect(table.locator("tbody tr", { hasText: "Dee" })).toContainText("30 KB");
  await expect(table.locator("tbody tr", { hasText: "Dee" })).toContainText("none");
  await expect(page.getByText("Showing 4 of 142 accounts")).toBeVisible();
  await expect(page.getByText("Kept for 90 days; older days are dropped")).toBeVisible();
});

test("a period button asks for that many days, and the table can be searched, sorted and cut by part", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#traffic/data");
  await expect(page.locator(".dchart .dcol")).toHaveCount(30);
  await page.getByRole("button", { name: "7 days", exact: true }).click();
  await expect(page.locator(".dchart .dcol")).toHaveCount(7);
  expect(st.reports).toContain(7);
  await expect(page.getByRole("button", { name: "7 days", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("link", { name: "Download CSV" })).toHaveAttribute("href", /report=7&format=csv/);
  await page.getByRole("button", { name: "Today", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Top accounts, today" })).toBeVisible();
  await page.getByRole("button", { name: "30 days", exact: true }).click();
  const rows = page.locator("table").first().locator("tbody tr");
  // search by name or email
  await page.getByLabel("Search by name or email").fill("chen@");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("Chen");
  await expect(page.getByText("Showing 1 of 142 accounts")).toBeVisible();
  await page.getByLabel("Search by name or email").fill("");
  // sort by days at the limit: Asha (6), Chen (4), Ben (2)
  await page.getByLabel("Sort").selectOption("limit");
  await expect(rows.nth(0)).toContainText("Asha");
  await expect(rows.nth(1)).toContainText("Chen");
  await expect(rows.nth(2)).toContainText("Ben");
  // a part: the accounts with preview use, and the amounts are that part's
  await page.getByLabel("Sort").selectOption("total");
  await page.getByLabel("Part", { exact: true }).selectOption("preview");
  await expect(rows.first()).toContainText("Chen");
  await expect(rows.first()).toContainText("1.6 GB");
});

test("a row opens the wide drawer of the account: stats, 30 days by part, the share of each part, and the buttons", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#traffic/data");
  await page.locator("table").first().locator("tbody tr", { hasText: "Asha" }).click();
  const drawer = page.locator("aside.drawer.wide");
  await expect(drawer).toBeVisible();
  expect(st.accts).toEqual(["uid-a"]);
  await expect(drawer).toContainText("asha@example.com");
  await expect(drawer).toContainText("signed in since 3 Nov 2026");
  await expect(drawer).toContainText("Free plan, 200 MB a day");
  const stats = drawer.locator(".stat");
  await expect(stats.filter({ hasText: "Today" })).toContainText("212 MB");
  await expect(stats.filter({ hasText: "Today" })).toContainText("106% of the limit");
  await expect(stats.filter({ hasText: "Average, 30 days" })).toContainText("on the 24 days it was used");
  await expect(stats.filter({ hasText: "Highest day" })).toContainText("1.3 GB");
  await expect(stats.filter({ hasText: "Days at the limit" })).toContainText("6");
  await expect(stats.filter({ hasText: "Days at the limit" })).toContainText("up to 3 in a row");
  await expect(drawer.locator(".dchart.tall .dcol")).toHaveCount(30);
  await expect(drawer.locator(".dchart .dlimit")).toHaveCount(1);
  await expect(drawer.getByRole("heading", { name: "Share of the 30 days" })).toBeVisible();
  await expect(drawer.locator("tr", { hasText: "Terminal" })).toContainText("60%");
  await expect(drawer).toContainText("Most of the use is the terminal (60%).");
  await expect(drawer).toContainText("Nothing here names a file, a message or a program: only sizes are kept.");
  await expect(drawer).toContainText("History goes back 90 days.");
  await expect(drawer).toContainText("Resetting today's count is written to the audit log");
  await expect(drawer.getByRole("link", { name: "Download CSV" })).toHaveAttribute("href", /ide-usage\?acct=uid-a&days=30&format=csv/);
  await drawer.getByRole("button", { name: "Watch this account" }).click();
  await expect.poll(() => st.watch).toEqual(["1:uid-a"]);
  await drawer.getByRole("button", { name: "Reset today's count" }).click();
  await expect.poll(() => st.resets).toBe(1);
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
});

test("a flag opens its account, a watched account can be dropped with its cross, and 'Top today' in Site settings points here", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#traffic/data");
  await page.locator(".flag-row", { hasText: "8 times its usual day" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("aside.drawer.wide")).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Stop watching Ben" }).click();
  await expect.poll(() => st.watch).toEqual(["0:uid-b"]);
  // the tabs: Visitors is the old Site usage view, and the hash follows the tab
  await tab(page, "Visitors").click();
  await expect(page.locator(".page-head h1")).toHaveText("Site usage");
  expect(new URL(page.url()).hash).toBe("#traffic");
  await tab(page, "Data usage").click();
  expect(new URL(page.url()).hash).toBe("#traffic/data");
  await expect(page.locator(".page-head h1")).toHaveText("Data usage");
});

test("when metering is off the tab says so", async ({ page }) => {
  await stub(page, { on: false });
  await page.goto("/admin#traffic/data");
  await expect(page.getByText("Metering is off")).toBeVisible();
});

test("only the accounts that were sent the most are listed, and the footer says so", async ({ page }) => {
  await stub(page, { total: 400 });
  await page.goto("/admin#traffic/data");
  await expect(page.getByText("Showing 4 of 400 accounts (the 4 that were sent the most)")).toBeVisible();
});
