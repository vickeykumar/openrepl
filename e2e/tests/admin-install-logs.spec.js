// The install log of an app in the admin's App Store view (resources/js/admin.js, LLD 18 section 13). The admin API is a stand-in; the routes are covered by the
// Go tests. It opens the log from the Log button and from a server label, reads a running install as a live tail, filters, searches, switches server and copies
// the text for support.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ADMIN_HTML = fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "admin.html"), "utf8");

const L = (t, s, l, x) => ({ t: 1794880000000 + t * 1000, s, l, x });
const FIRST = [
  L(0, "start", "info", "install eslint on gateway, asked by the admin"),
  L(0, "env", "info", "gateway | linux-64 | node v20.1.0 | go go1.23.12 | disk free 18.0 GB | memory free 1.9 GB"),
  L(1, "order", "info", "managers: npm"),
  L(2, "look up", "warn", "npm: eslint@10.0.3 could not be looked up (npm could not find it): trying the next older one"),
  L(5, "look up", "info", "npm: 9.2.0, 98 packages, 1 MB to fetch (4.2 s)"),
  L(6, "install", "info", "npm: installing eslint 9.2.0"),
  L(9, "output", "output", "npm http fetch GET 200 https://registry.npmjs.org/ajv 380ms"),
];
const MORE = [
  L(30, "output", "output", "added 98 packages in 24s"),
  L(31, "trim", "info", "trimmed 22.0 MB to 14.1 MB (410 files removed)"),
  L(32, "check", "info", "it works (0.4 s)"),
  L(32, "result", "info", "result: installed"),
];

function stub(page) {
  const st = { logCalls: [], statusDone: false };
  page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  page.route("**/admin/gateway", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ mode: "single" }) }));
  page.route("**/admin/store/catalogue**", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ entries: [], packs: [], repls: [], places: [], checks: [], disk: [], settings: {}, now: 1794880100 }) }));
  page.route("**/admin/store/log**", (r) => {
    const u = new URL(r.request().url());
    const node = u.searchParams.get("node"), from = Number(u.searchParams.get("from"));
    st.logCalls.push({ node, from, attempt: u.searchParams.get("attempt") });
    const attempts = [
      { n: 2, app: "eslint", label: "install", start: 1794880000, result: st.statusDone ? "installed" : "running", version: "9.2.0", manager: "npm" },
      { n: 1, app: "eslint", label: "install", start: 1794800000, end: 1794800052, result: "installed", version: "8.57.1", manager: "npm", bytes: 4096 },
    ];
    const json = (o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
    if (node === "openrepl-pi-1") return json({ node, chunk: { attempts: [], info: { n: 1, label: "replay", result: "failed", start: 1794880000, err: "no candidates" }, from: 0, next: 10, found: true, running: false, lines: [L(0, "start", "info", "replay: make eslint on openrepl-pi-1"), L(1, "error", "error", "Cannot solve the request because of: No candidates were found")] } });
    if (from === 0) return json({ node, chunk: { attempts, info: attempts[0], from: 0, next: 100, found: true, running: true, lines: FIRST } });
    st.statusDone = true;
    return json({ node, chunk: { attempts: [{ ...attempts[0], result: "installed", end: 1794880032 }, attempts[1]], info: { ...attempts[0], result: "installed", end: 1794880032 }, from, next: 200, found: true, running: false, lines: MORE } });
  });
  page.route("**/admin/store", (r) => {
    if (r.request().method() === "POST") return r.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) });
    return r.fulfill({ contentType: "application/json", body: JSON.stringify({ enabled: true, installer: "npm", dir: "/store", python: "3.10", channel: "conda-forge", sizeMB: 100, limitMB: 15000, now: 1794880100,
      apps: [{ name: "eslint", kind: "node", state: "installing", version: "9.2.0", id: "eslint-9.2.0", at: 1 }, { name: "git", kind: "tool", state: "installed", version: "2.34.1", id: "git-2.34.1", size: 13e6, at: 1 }],
      defaults: [], users: {}, apt: [], safeList: [], requests: [], reports: [],
      nodes: [{ node: "gateway", have: ["git-2.34.1"], doing: "eslint", since: 1794880000, sizeMB: 100 }, { node: "openrepl-pi-1", have: [], queued: ["eslint"], sizeMB: 10, seen: new Date(1794880090000).toISOString() }] }) });
  });
  page.route(/\/admin\/(health|settings|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => r.fulfill({ contentType: "application/json", body: "{}" }));
  return st;
}

async function openInstalled(page) {
  await page.goto("/admin#store");
  await page.getByRole("button", { name: /^Installed/ }).click();
}

test("the Log button of a running install opens its log, reads it live and ends with the result", async ({ page }) => {
  const st = stub(page);
  await openInstalled(page);
  await page.locator("tr", { hasText: "eslint" }).getByRole("button", { name: "Log (live)" }).click();
  const dlg = page.locator("dialog[open]");
  await expect(dlg).toContainText("Install log: eslint");
  await expect(dlg).toContainText("install eslint on gateway, asked by the admin");
  await expect(dlg).toContainText("[env]");
  await expect(dlg).toContainText("linux-64");
  await expect(dlg.locator(".logline.lv-warn")).toContainText("could not be looked up");
  // the attempts and the steps
  await expect(dlg).toContainText("#2");
  await expect(dlg).toContainText("#1");
  await expect(dlg.locator(".logstep", { hasText: "look up" })).toBeVisible();
  // the live tail asks again from where it stopped, and the log ends with its result
  await expect(dlg).toContainText("result: installed", { timeout: 8000 });
  expect(st.logCalls.some((c) => c.from === 100)).toBe(true);
  await expect(dlg).toContainText("trimmed 22.0 MB to 14.1 MB");
});

test("the filters, the search, the server tabs and Copy for support work", async ({ page, context }) => {
  stub(page);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openInstalled(page);
  await page.locator("tr", { hasText: "eslint" }).getByRole("button", { name: "Log (live)" }).click();
  const dlg = page.locator("dialog[open]");
  await expect(dlg).toContainText("npm http fetch GET 200");
  await dlg.getByRole("button", { name: "Errors" }).click();
  await expect(dlg.locator(".logline")).toHaveCount(1);
  await dlg.getByRole("button", { name: "Output" }).click();
  await expect(dlg.locator(".logline", { hasText: "npm http fetch" })).toHaveCount(1);
  await expect(dlg.locator(".logline", { hasText: "managers: npm" })).toHaveCount(0);
  await dlg.getByRole("button", { name: "All", exact: true }).click();
  await dlg.getByLabel("Search the log").fill("ajv");
  await expect(dlg.locator(".logline")).toHaveCount(1);
  await dlg.getByLabel("Search the log").fill("");
  // Copy for support: the app, the server, the environment, the steps and the last lines
  await dlg.getByRole("button", { name: "Copy for support" }).click();
  const text = await page.evaluate(() => navigator.clipboard.readText());
  expect(text).toContain("App: eslint 9.2.0 (npm) on gateway");
  expect(text).toContain("Environment: gateway | linux-64");
  expect(text).toContain("Steps:");
  expect(text).toContain("Last 40 lines:");
  // another server: its own log, with its own attempt
  await dlg.getByRole("button", { name: "openrepl-pi-1" }).click();
  await expect(dlg).toContainText("No candidates were found");
  await expect(dlg.locator(".logline.lv-error")).toHaveCount(1);
});

test("a server label opens the log of the app on that server", async ({ page }) => {
  const st = stub(page);
  await openInstalled(page);
  const label = page.locator("tr", { hasText: "eslint" }).locator(".loglink").first();
  await label.click();
  await expect(page.locator("dialog[open]")).toContainText("Install log: eslint");
  expect(st.logCalls.length).toBeGreaterThan(0);
});
