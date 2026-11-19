// The Genie health view of the admin dashboard (src/resources/js/admin.js viewGenieHealth, server/genie_health.go).
// The admin API is a stand-in; the routes are covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves the admin's files from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");

const REPLY = {
  hours: 24, errPctLimit: 20, slowMsLimit: 8000, alerting: 1, since: 1795080000,
  auto: { small: { model: "gpt-6-luna", name: "GPT-6 Luna", calls: 412, errPct: 3 }, strong: { model: "gpt-4o-mini", name: "GPT-4o mini", calls: 96, errPct: 1 }, stronger: 38, askedPct: 9.2 },
  rows: [
    { model: "meta/llama-x", name: "Llama X", calls: 40, errors: 12, retries: 7, errPct: 30, reasons: { limit: 9, host_error: 3 }, p50Ms: 1000, p95Ms: 16000, avgMs: 4200, lastError: 1795086000, alerts: ["many calls fail", "slow to start answering"] },
    { model: "gpt-4o-mini", name: "GPT-4o mini", calls: 120, errors: 0, errPct: 0, reasons: {}, p50Ms: 500, p95Ms: 2000, avgMs: 1800, lastError: 0, alerts: [] },
  ],
};

async function stub(page) {
  const st = { calls: [] };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, type, file] of [["/js/admin.js", "application/javascript", "js/admin.js"], ["/css/admin.css", "text/css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: type, body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route(/\/admin\/(health|settings|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  await page.route("**/admin/logs**", (r) => json(r, { lines: ["2026/11/19 11:00:00 x.go:1: a line", "2026/11/19 11:00:01 x.go:2: Error: boom"], truncated: false }));
  await page.route("**/admin/genie-health**", (r) => {
    st.calls.push(Object.fromEntries(new URL(r.request().url()).searchParams.entries()));
    json(r, REPLY);
  });
  await page.addInitScript(() => {
    window.__copied = "";
    Object.defineProperty(navigator, "clipboard", { value: { writeText: (t) => ((window.__copied = t), Promise.resolve()) }, configurable: true });
  });
  return st;
}

test("the Genie health view shows a row for each model, why calls failed, the times, and which model needs a look", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#geniehealth");
  await expect(page.getByRole("heading", { name: "Genie health" })).toBeVisible();
  const tiles = page.locator(".grid.four .card");
  await expect(tiles).toHaveCount(4);
  await expect(tiles.nth(0)).toContainText("2");
  await expect(tiles.nth(1)).toContainText("1");
  await expect(tiles.nth(2)).toContainText("160");
  await expect(tiles.nth(3)).toContainText("7.5%");
  const rows = page.locator("table").first().locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("Llama X");
  await expect(rows.nth(0)).toContainText("30% (12)");
  await expect(rows.nth(0)).toContainText("7"); // retried
  await expect(rows.nth(0)).toContainText("limit (429) \u00d79");
  await expect(rows.nth(0)).toContainText("host error (5xx) \u00d73");
  await expect(rows.nth(0)).toContainText("under 1 s");
  await expect(rows.nth(0)).toContainText("under 16 s");
  await expect(rows.nth(0)).toContainText("many calls fail");
  await expect(rows.nth(0)).toContainText("slow to start answering");
  await expect(rows.nth(1)).toContainText("GPT-4o mini");
  await expect(rows.nth(1)).toContainText("fine");
  await expect(page.getByText(/at least 20% of its calls failed/)).toBeVisible();
  // how Auto did
  await expect(page.getByRole("heading", { name: "Auto model choice" })).toBeVisible();
  const auto = page.locator("table").nth(1).locator("tbody tr");
  await expect(auto).toHaveCount(2);
  await expect(auto.nth(0)).toContainText("Small");
  await expect(auto.nth(0)).toContainText("412");
  await expect(auto.nth(0)).toContainText("38 (9%)");
  await expect(auto.nth(1)).toContainText("Strong");
  expect(st.calls[0].hours).toBe("24");
  // another period is asked for
  await page.getByLabel("Period").selectOption("1");
  await expect.poll(() => st.calls.some((c) => c.hours === "1")).toBe(true);
});

test("the Genie health view is in the System group of the menu", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#overview");
  await expect(page.getByRole("link", { name: "Genie health" })).toBeVisible();
});
