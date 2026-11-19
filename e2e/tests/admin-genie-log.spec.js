// The Genie log of the admin dashboard (src/resources/js/admin.js viewGenieLog, server/genie_log.go) and the Download log button of the Log view.
// The admin API is a stand-in; the routes are covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves the admin's files from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");

const EVENT = (o) => Object.assign({ t: "2026-11-19T06:00:00Z", sid: "sabc123xyz", mode: "agent", plan: "free", prompt: "g1" }, o);
const REPLY = {
  days: 7, tasks: 6, done: 4, donePct: 66.7, failures: 4, stuck: 1, worstAction: "editor_replace", matching: 3, off: false, keepDays: 30, dropped: 0,
  codes: [{ code: "stale_ref", n: 4, topAction: "preview_click" }, { code: "no_file_open", n: 1, topAction: "files_save" }],
  models: [
    { model: "gpt-4o-mini", tasks: 5, done: 4, donePct: 80, failures: 1, stuck: 0, topAction: "run", topReason: "not available" },
    { model: "other", tasks: 1, done: 0, donePct: 0, failures: 3, stuck: 1, topAction: "editor_replace", topReason: "the editor has only 35 lines" },
  ],
  events: [
    EVENT({ event: "stuck", model: "other", action: "editor_replace", step: 3, failed: 3, rule: "same-step", reason: "the editor has only 35 lines", trail: "editor_replace,editor_replace,editor_replace" }),
    EVENT({ event: "step_failed", model: "other", action: "editor_replace", ext: "html", step: 2, failed: 2, code: "edit_mismatch", reason: "the editor has only 35 lines" }),
    EVENT({ event: "task_end", model: "gpt-4o-mini", outcome: "done", steps: 7, secs: 42 }),
  ],
};

async function stub(page) {
  const st = { genieCalls: [], copied: "" };
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
  await page.route("**/admin/genie-log**", (r) => {
    const u = new URL(r.request().url());
    st.genieCalls.push(Object.fromEntries(u.searchParams.entries()));
    const model = u.searchParams.get("model");
    const events = model ? REPLY.events.filter((e) => e.model === model) : REPLY.events;
    json(r, Object.assign({}, REPLY, { events, matching: events.length }));
  });
  await page.addInitScript(() => {
    window.__copied = "";
    Object.defineProperty(navigator, "clipboard", { value: { writeText: (t) => ((window.__copied = t), Promise.resolve()) }, configurable: true });
  });
  return st;
}

test("the Log view has a button that downloads the whole log file", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#logs");
  const dl = page.getByRole("link", { name: "Download log" });
  await expect(dl).toBeVisible();
  await expect(dl).toHaveAttribute("href", /admin\/logs\?download=1$/);
  await expect(dl).toHaveAttribute("download", /gotty-log/);
  await expect(page.locator("pre.log")).toContainText("Error: boom");
});

test("the Genie log shows the summary, a row for each model and the newest events, and filters by model", async ({ page }) => {
  const st = await stub(page);
  await page.goto("/admin#genielog");
  await expect(page.getByRole("heading", { name: "Genie log" })).toBeVisible();
  await expect(page.getByText(/Never the user's name, messages, files or terminal text/)).toBeVisible();
  // the summary tiles and the table by model
  const tiles = page.locator(".grid.five .card");
  await expect(tiles).toHaveCount(5);
  await expect(tiles.nth(0)).toContainText("6");
  await expect(tiles.nth(1)).toContainText("66.7%");
  await expect(tiles.nth(2)).toContainText("1");
  await expect(tiles.nth(4)).toContainText("editor_replace");
  const rows = page.locator("table").first().locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("gpt-4o-mini");
  await expect(rows.nth(0)).toContainText("80%");
  await expect(rows.nth(1)).toContainText("the editor has only 35 lines");
  // the events, newest first, with what each says
  const events = page.locator("table").nth(2).locator("tbody tr");
  await expect(events).toHaveCount(3);
  await expect(events.nth(0)).toContainText("stuck");
  await expect(events.nth(0)).toContainText("editor_replace");
  await expect(events.nth(1)).toContainText("editor_replace .html");
  await expect(page.locator("table").nth(2)).not.toContainText("[object");
  await expect(events.nth(2)).toContainText("done · 7 steps · 42 s");
  await expect(page.getByText("Kept 30 days")).toBeVisible();
  // the most common failures, by code, and the way to a stuck task
  await expect(page.getByRole("heading", { name: "Most common failures" })).toBeVisible();
  const codes = page.locator("table").nth(1).locator("tbody tr");
  await expect(codes).toHaveCount(2);
  await expect(codes.nth(0)).toContainText("stale_ref");
  await expect(codes.nth(0)).toContainText("4");
  await expect(codes.nth(0)).toContainText("preview_click");
  await expect(events.nth(0)).toContainText("same-step: the editor has only 35 lines");
  await expect(events.nth(0)).toContainText("after: editor_replace");
  await expect(events.nth(1)).toContainText("edit_mismatch: the editor has only 35 lines");
  // the model filter asks the server again
  await page.getByLabel("Model").selectOption("other");
  await expect(events).toHaveCount(2);
  expect(st.genieCalls[st.genieCalls.length - 1].model).toBe("other");
  expect(st.genieCalls[0].days).toBe("7");
  await page.getByLabel("Period").selectOption("30");
  await expect.poll(() => st.genieCalls[st.genieCalls.length - 1].days).toBe("30");
});

test("Download whole log follows the period, and Copy for support copies the newest events as lines", async ({ page }) => {
  await stub(page);
  await page.goto("/admin#genielog");
  const dl = page.getByRole("link", { name: "Download whole log" });
  await expect(dl).toHaveAttribute("href", /admin\/genie-log\?download=1&days=7$/);
  await page.getByLabel("Period").selectOption("30");
  await expect(dl).toHaveAttribute("href", /download=1&days=30$/);
  await page.getByRole("button", { name: "Copy for support" }).click();
  const copied = await page.evaluate(() => window.__copied);
  const lines = copied.split("\n");
  expect(lines).toHaveLength(3);
  expect(JSON.parse(lines[0]).event).toBe("stuck");
  expect(copied).not.toMatch(/uid|email/i);
});

test("the Genie log says so when it is switched off, and when there is nothing yet", async ({ page }) => {
  await stub(page);
  await page.route("**/admin/genie-log**", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify(Object.assign({}, REPLY, { off: true, tasks: 0, done: 0, donePct: 0, failures: 0, stuck: 0, worstAction: "", models: [], events: [], matching: 0 })) }));
  await page.goto("/admin#genielog");
  await expect(page.getByText("The Genie log is switched off in Site settings")).toBeVisible();
  await expect(page.getByText("Nothing has been reported in this period.")).toBeVisible();
  await expect(page.getByText("No events match.")).toBeVisible();
});
