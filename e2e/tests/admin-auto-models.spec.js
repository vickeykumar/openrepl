// Auto model choice (Site settings, Genie, Models): the two models may be any model that is on, built in or added (an OpenRouter model of the "Added models" list too).
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", n), "utf8"));

async function stub(page, genie) {
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, file] of [["/js/admin.js", "js/admin.js"], ["/js/admin-sections.js", "js/admin-sections.js"], ["/js/admin-site.js", "js/admin-site.js"], ["/css/admin.css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: u.endsWith("css") ? "text/css" : "application/javascript", body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  const settings = fixture("admin-settings.json");
  Object.assign(settings.genie, genie);
  await page.route("**/admin/settings", (r) => json(r, settings));
  await page.route("**/admin/keys", (r) => json(r, fixture("admin-keys.json")));
  await page.route("**/admin/admins", (r) => json(r, fixture("admin-admins.json")));
  await page.route("**/admin/plugin-installs", (r) => json(r, { entries: [], counts: [], days: 90 }));
  await page.route("**/admin/connection-requests", (r) => json(r, { requests: [], waiting: 0 }));
  await page.route("**/admin/connections", (r) => json(r, { connected: [], calls: [] }));
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route(/\/admin\/(health|stats|audit|feedback|workers|sessions|jobs|ide-usage)(\?.*)?$/, (r) => json(r, {}));
}

const NEMO = "nvidia/nemotron-3-super-120b-a12b:free";

test("an added OpenRouter model is in the lists of Auto, and the saved choice of one is kept", async ({ page }) => {
  const custom = fixture("admin-settings.json").genie.customModels.map((c) => Object.assign({}, c, { enabled: c.id === NEMO }));
  await stub(page, { customModels: custom, autoOn: true, autoSmall: NEMO, autoStrong: "gpt-6-luna" });
  await page.goto("/admin#site/genie-agent");
  for (const id of ["#f-auto-small", "#f-auto-strong"]) {
    const sel = page.locator(id);
    await expect(sel.locator("option", { hasText: "Gemma 4 31B" })).toHaveCount(1); // the built-in OpenRouter model
    await expect(sel.locator("option", { hasText: "Nemotron 3 Super (free)" })).toHaveCount(1);
    await expect(sel.locator("option", { hasText: "Nemotron 3 Super (free)" })).toBeEnabled();
    await expect(sel.locator("option", { hasText: "North mini code (free)" })).toBeDisabled(); // an added model that is off cannot be chosen
  }
  await expect(page.locator("#f-auto-small")).toHaveValue(NEMO); // the saved choice is not lost
  await expect(page.locator("#f-auto-strong")).toHaveValue("gpt-6-luna");
});
