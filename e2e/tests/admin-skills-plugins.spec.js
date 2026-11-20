// The admin's side of skills and plugins (resources/js/admin-site.js): the card with the switches and the plugins that are on for everyone, and
// the list of who installed what. The admin API is a stand-in made from the real replies (tests/fixtures); the routes are
// covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves admin.js from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ADMIN_HTML = fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));

async function stub(page, measures, installs) {
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
