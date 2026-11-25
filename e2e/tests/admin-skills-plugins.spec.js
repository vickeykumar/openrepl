// The admin's side of skills and plugins (resources/js/admin-site.js): the card with the switches and the plugins that are on for everyone, and
// the list of who installed what. The admin API is a stand-in made from the real replies (tests/fixtures); the routes are
// covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves admin.js from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ADMIN_HTML = fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));

async function stub(page, measures, installs, conn) {
  const st = { saved: [], baselines: 0 };
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/admin.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin.js")) }));

    await page.route((u) => u.pathname.endsWith("/js/admin-sections.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin-sections.js")) }));
    await page.route((u) => u.pathname.endsWith("/js/admin-site.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "admin-site.js")) }));
    await page.route((u) => u.pathname.endsWith("/css/admin.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "css", "admin.css")) }));
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  let settings = Object.assign(fixture("admin-settings.json"), { pluginCatalogue: [{ id: "code-review-kit", title: "Code review kit" }, { id: "git-helper", title: "Git helper" }] });
  await page.route("**/admin/plugin-installs", (r) => json(r, installs));
  await page.route("**/admin/ide-usage**", (r) => json(r, { day: "2026-11-25", total: 0, parts: {}, top: [] }));
  const cst = { answers: [], requests: (conn && conn.requests) || [] };
  st.conn = cst;
  await page.route("**/admin/connection-requests", (r) => {
    if (r.request().method() === "POST") {
      const b = r.request().postDataJSON();
      cst.answers.push(b);
      cst.requests = cst.requests.map((q) => (q.id === b.id ? Object.assign({}, q, { status: b.action === "decline" ? "declined" : "approved", scope: b.action === "approve_user" ? "user" : b.action === "approve" ? "site" : "", reason: b.reason || "" }) : q));
      return json(r, { request: {} });
    }
    json(r, { requests: cst.requests, waiting: cst.requests.filter((q) => q.status === "waiting").length });
  });
  await page.route("**/admin/connections", (r) => json(r, { connected: (conn && conn.connected) || [], calls: (conn && conn.calls) || [] }));
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

const INSTALLS = {
  days: 90, counts: [{ plugin: "acme-review", users: 2 }, { plugin: "git-helper", users: 0 }],
  entries: [
    { t: "2026-11-20T11:00:00Z", user: "uid-ann", op: "install", plugin: "acme-review", version: "0.3", source: "link:https://github.com/acme/genie-review-kit", skills: 2 },
    { t: "2026-11-20T10:00:00Z", user: "uid-bob", op: "remove", plugin: "git-helper", version: "1.0", source: "catalogue:git-helper" },
  ],
};

async function toSection(page, id, cardTitle) {
  await page.evaluate((h) => { location.hash = h; }, "#site/" + id);
  await expect(page.locator(`.site-section[data-section="${id}"]`)).toBeVisible();
  if (cardTitle) await expect(page.locator(".card", { hasText: cardTitle }).first()).toBeVisible();
}

test("Site settings has skills and plugins: on by default, links off, and the choices are saved", async ({ page }) => {
  const st = await stub(page, {}, INSTALLS);
  await page.goto("/admin#site/genie-agent");
  const card = page.locator(".card", { hasText: "Skills and plugins" }).first();
  await expect(card).toBeVisible();
  await expect(card.locator("#f-ext-on")).toBeChecked();
  await expect(card.locator("#f-plg-links")).not.toBeChecked();
  await expect(card.locator("#f-site-plugin-code-review-kit")).not.toBeChecked();
  await expect(card).toContainText("a skill can only narrow the actions of a task");
  await card.locator("#f-plg-links").evaluate((e) => e.click());
  await card.locator("#f-site-plugin-git-helper").evaluate((e) => e.click());
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].genie).toMatchObject({ extensionsOff: false, pluginsFromLinks: true, sitePlugins: ["git-helper"] });
});

test("Plugin installs lists who installed what and from where, newest first, with a link in full", async ({ page }) => {
  await stub(page, {}, INSTALLS);
  await page.goto("/admin#site/genie-agent");
  const card = page.locator(".card", { hasText: "Plugin installs" }).first();
  await expect(card).toBeVisible();
  await expect(card).toContainText("Now installed: acme-review (2 people)");
  const rows = card.locator("tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("uid-ann");
  await expect(rows.nth(0)).toContainText("install");
  await expect(rows.nth(0)).toContainText("acme-review v0.3");
  await expect(rows.nth(0)).toContainText("link https://github.com/acme/genie-review-kit");
  await expect(rows.nth(1)).toContainText("remove");
  await expect(rows.nth(1)).toContainText("catalogue");
});

const REQ = {
  id: "r1", user: "uid-ann", plugin: "acme-review", version: "0.3", source: "link:https://github.com/acme/genie-review-kit", status: "waiting", at: "2026-11-20T12:00:00Z",
  connections: [{ id: "github", title: "GitHub", url: "https://api.githubcopilot.com/mcp/" }], skills: ["review-it", "team-style"], note: "for my team",
};

test("Site settings has the connections card: on, no approval off, and the switches and the list of servers are saved", async ({ page }) => {
  const st = await stub(page, {}, INSTALLS, {});
  await page.goto("/admin#site/genie-agent");
  const card = page.locator(".card", { hasText: "Connections to other services" }).first();
  await expect(card).toBeVisible();
  await expect(card.locator("#f-conn-on")).toBeChecked();
  await expect(card.locator("#f-conn-noapproval")).not.toBeChecked();
  await expect(card.locator("#f-conn-hosts")).toHaveValue("");
  await expect(card).toContainText("20 calls");
  await card.locator("#f-conn-noapproval").evaluate((e) => e.click());
  await card.locator("#f-conn-hosts").fill("API.githubcopilot.com\nmcp.acme.example, api.githubcopilot.com");
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect.poll(() => st.saved.length).toBe(1);
  expect(st.saved[0].genie).toMatchObject({ connectionsOff: false, connectionsNoApproval: true, connectionHosts: ["api.githubcopilot.com", "mcp.acme.example"] });
});

test("a request for a connection waits in the admin's list with what the server fetched, and is approved for one user or declined with a reason", async ({ page }) => {
  const st = await stub(page, {}, INSTALLS, { requests: [REQ, Object.assign({}, REQ, { id: "r2", plugin: "other", status: "approved", scope: "site" })], connected: [{ user: "uid-bob", conn: "github", at: "2026-11-20T11:00:00Z" }], calls: [{ conn: "github", tool: "get_issue", ok: true }] });
  await page.goto("/admin#site/genie-agent");
  const card = page.locator('[data-card="Connection requests"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText("acme-review v0.3 · uid-ann");
  await expect(card).toContainText("link:https://github.com/acme/genie-review-kit");
  await expect(card).toContainText("GitHub (api.githubcopilot.com/mcp/)");
  await expect(card).toContainText("review-it, team-style");
  await expect(card).toContainText("for my team");
  await expect(card).toContainText("1 waiting");
  await expect(card).toContainText("uid-bob");
  await expect(card).toContainText("github.get_issue");
  // approve for this user
  await card.getByRole("button", { name: "Approve for this user" }).click();
  await page.locator("dialog[open]").getByRole("button", { name: "Approve" }).click();
  await expect.poll(() => st.conn.answers.length).toBe(1);
  expect(st.conn.answers[0]).toMatchObject({ id: "r1", action: "approve_user" });
  await expect(card).toContainText("approved (user)");
  await expect(card).toContainText("0 waiting".replace("0", "No"));
});

test("a request can be declined with a reason the user sees", async ({ page }) => {
  const st = await stub(page, {}, INSTALLS, { requests: [REQ] });
  await page.goto("/admin#site/genie-agent");
  const card = page.locator('[data-card="Connection requests"]');
  await card.getByRole("button", { name: "Decline…" }).click();
  await page.locator("dialog[open] input").fill("Server not on our list");
  await page.locator("dialog[open]").getByRole("button", { name: "Decline" }).click();
  await expect.poll(() => st.conn.answers.length).toBe(1);
  expect(st.conn.answers[0]).toMatchObject({ id: "r1", action: "decline", reason: "Server not on our list" });
  await expect(card).toContainText("declined: Server not on our list");
});
