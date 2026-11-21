// The Integrations card of Site settings, under Genie (src/resources/js/admin-integrations.js, server/genie_integrations*.go): add a server with a name,
// an address and a logo, test it, switch tools on with a level, publish it, answer a request for a server, and keep a secret write-only.
// The admin API is a stand-in; the routes are covered by the Go tests. GENIE_LOCAL_ASSETS=1 serves the admin's files from this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ROOT = path.join(__dirname, "..", "..", "src", "resources");
const ADMIN_HTML = fs.readFileSync(path.join(ROOT, "admin.html"), "utf8");

function newState() {
  return {
    posts: [],
    reqPosts: [],
    list: [{ id: "github", title: "GitHub", url: "https://api.githubcopilot.com/mcp/", status: "published", auth: { mode: "oauth" }, audience: { everyone: true }, limits: {}, tools: [], secrets: {}, usage: { enabled: 2, connected: 1 }, toolsOn: 0, publishable: "" }],
    reqs: [{ id: "r1", kind: "server", user: "uid-ann", email: "ann@example.com", title: "Acme wiki", url: "https://wiki.acme.dev/mcp", note: "Search our wiki while I write docs.", signin: "oauth", docs: "https://wiki.acme.dev/docs", status: "waiting", at: "2026-11-21T08:12:00Z" }],
  };
}

async function stub(page, st) {
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, type, file] of [["/js/admin.js", "application/javascript", "js/admin.js"], ["/js/admin-integrations.js", "application/javascript", "js/admin-integrations.js"], ["/js/admin-sections.js", "application/javascript", "js/admin-sections.js"], ["/js/admin-site.js", "application/javascript", "js/admin-site.js"], ["/css/admin.css", "text/css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: type, body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o, status = 200) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(o) });
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route("**/admin/settings", (r) => json(r, JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "admin-settings.json"), "utf8"))));
  await page.route("**/admin/keys", (r) => json(r, JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "admin-keys.json"), "utf8"))));
  await page.route("**/admin/admins", (r) => json(r, JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "admin-admins.json"), "utf8"))));
  await page.route("**/admin/plugin-installs", (r) => json(r, { entries: [], counts: [], days: 90 }));
  await page.route(/\/admin\/(health|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
  await page.route("**/admin/integrations", (r) => {
    if (r.request().method() === "GET") return json(r, { integrations: st.list, canStore: true, redirect: "https://openrepl.com/genie_conn_oauth", requests: 1 });
    const b = JSON.parse(r.request().postData() || "{}");
    st.posts.push(b);
    const find = (id) => st.list.filter((i) => i.id === id)[0];
    if (b.op === "save") {
      const i = b.integration;
      let cur = i.id ? find(i.id) : null;
      if (!cur) {
        cur = { id: "notion", tools: [], secrets: {}, usage: {}, status: "draft", toolsOn: 0 };
        st.list.push(cur);
      }
      Object.assign(cur, { title: i.title, url: i.url, logo: i.logo, keywords: i.keywords, description: i.description, auth: i.auth, audience: i.audience, limits: i.limits, publishable: "Test it first." });
      return json(r, { integration: cur });
    }
    const cur = find(b.id);
    if (b.op === "test") {
      cur.lastTest = { at: "2026-11-21T09:00:00Z", ok: true, protocol: "2025-06-18", tools: 3, signIn: "none", skipped: 1 };
      cur.status = "tested";
      cur.auth = { mode: "none", detected: true };
      cur.tools = [{ name: "search_pages", enabled: false, new: true }, { name: "create_page", enabled: false, new: true }, { name: "delete_page", enabled: false, new: true }];
      cur.publishable = "No tool is switched on: test the server and switch some on.";
      return json(r, { integration: cur });
    }
    if (b.op === "tools") {
      b.tools.forEach((t) => Object.assign(cur.tools.filter((x) => x.name === t.name)[0], { enabled: t.enabled, level: t.level, new: false }));
      cur.toolsOn = b.tools.filter((t) => t.enabled).length;
      cur.publishable = cur.toolsOn ? "" : cur.publishable;
      return json(r, { integration: cur });
    }
    if (b.op === "publish") {
      cur.status = "published";
      return json(r, { integration: cur });
    }
    if (b.op === "secret") {
      cur.secrets = Object.assign({}, cur.secrets, { [b.kind]: !!b.value });
      return json(r, { integration: cur });
    }
    return json(r, { error: { message: "no" } }, 400);
  });
  await page.route("**/admin/connections", (r) => json(r, { connected: [{ user: "uid-ann", conn: "github", at: "2026-11-20T21:31:10Z" }], calls: [{ t: "2026-11-21T09:00:00Z", user: "uid-ann", conn: "github", tool: "get_issue", level: "read", op: "call", ok: true }] }));
  await page.route("**/admin/connection-requests", (r) => {
    if (r.request().method() === "GET") return json(r, { requests: st.reqs, waiting: st.reqs.filter((q) => q.status === "waiting").length });
    const b = JSON.parse(r.request().postData() || "{}");
    st.reqPosts.push(b);
    st.reqs[0].status = b.action === "decline" ? "declined" : "approved";
    return json(r, { request: st.reqs[0], integration: st.list[0], published: b.action !== "customize", why: b.action === "customize" ? "A draft: finish it and publish it." : "" });
  });
}

const GO = "/admin#site/genie-integrations";
const card = (page) => page.locator('[data-card="Integrations"]');

test("an admin adds a server with a name, an address and a logo, tests it, switches a tool on with a level and publishes it", async ({ page }) => {
  const st = newState();
  await stub(page, st);
  await page.goto(GO);
  await expect(card(page).locator("table").first().locator("tbody tr")).toHaveCount(1);
  await expect(card(page).locator("tbody tr").first()).toContainText("GitHub");
  await expect(card(page).locator("tbody tr").first()).toContainText("Published");
  await page.getByRole("button", { name: "+ Add a server" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Notion");
  await page.getByLabel("Server address").fill("https://mcp.notion.com/mcp");
  await page.getByLabel("Keywords").fill("notion, pages");
  await page.getByLabel("Logo", { exact: true }).fill("https://cdn.simpleicons.org/notion");
  await expect(card(page).locator(".int-logo img").first()).toHaveAttribute("src", "https://cdn.simpleicons.org/notion");
  await page.getByRole("button", { name: "Test the server" }).click();
  await expect(page.getByRole("heading", { name: "Notion · Discover and test" })).toBeVisible();
  expect(st.posts.map((p) => p.op)).toEqual(["save", "test"]);
  expect(st.posts[0].integration).toMatchObject({ title: "Notion", url: "https://mcp.notion.com/mcp", keywords: ["notion", "pages"], logo: "https://cdn.simpleicons.org/notion" });
  await page.getByRole("button", { name: "Next: tools" }).click();
  const tools = card(page).locator(".int-tool");
  await expect(tools).toHaveCount(3);
  await expect(tools.first()).toContainText("search_pages");
  await expect(tools.first()).toContainText("NEW");
  // a tool has no level yet: it is chosen, then the tool is switched on; the other two stay off
  await tools.nth(0).locator(".int-seg button.read").click();
  await tools.nth(0).getByRole("switch").click();
  await tools.nth(1).locator(".int-seg button.write").click();
  await tools.nth(1).getByRole("switch").click();
  await page.getByRole("button", { name: "Publish to users" }).click();
  await expect.poll(() => st.posts.filter((p) => p.op === "publish").length).toBe(1);
  expect(st.posts.filter((p) => p.op === "tools")[0].tools.map((t) => [t.name, t.enabled, t.level])).toEqual([["search_pages", true, "read"], ["create_page", true, "write"], ["delete_page", false, ""]]);
  await expect(card(page).locator(".int-hint", { hasText: "Status: Published" })).toBeVisible();
});

test("a request for a server is plain text, and an admin answers it in one click", async ({ page }) => {
  const st = newState();
  await stub(page, st);
  await page.goto(GO);
  await card(page).getByRole("tab", { name: /^Requests/ }).click();
  const q = card(page).locator(".int-panel", { hasText: "Acme wiki" });
  await expect(q).toContainText("https://wiki.acme.dev/mcp");
  await expect(q).toContainText("Search our wiki while I write docs.");
  await expect(q).toContainText("they think: OAuth");
  await expect(q).toContainText("ann@example.com");
  await q.getByRole("button", { name: "Approve for Ann only" }).click();
  await expect.poll(() => st.reqPosts.length).toBe(1);
  expect(st.reqPosts[0]).toMatchObject({ id: "r1", action: "approve_user" });
  await expect(page.getByText("Approved and published for Ann.")).toBeVisible();
});

test("a secret is write-only: the page says whether one is set and never holds it", async ({ page }) => {
  const st = newState();
  st.list[0].secrets = { oauthClientSecret: true };
  await stub(page, st);
  await page.goto(GO);
  await card(page).locator("table").first().getByRole("button", { name: "Edit" }).click();
  // GitHub's sign-in is an OAuth app of the admin's: its secret is saved, and the field asks to type a new one to replace it
  const secret = page.getByLabel("Client secret");
  await expect(secret).toHaveAttribute("placeholder", /saved/);
  await expect(secret).toHaveValue("");
  await secret.fill("a-new-secret-value");
  await secret.locator("xpath=following-sibling::button[1]").click();
  await expect.poll(() => st.posts.filter((p) => p.op === "secret").length).toBe(1);
  expect(st.posts.filter((p) => p.op === "secret")[0]).toMatchObject({ id: "github", kind: "oauthClientSecret", value: "a-new-secret-value" });
  await expect(page.getByLabel("Client secret")).toHaveValue("");
  expect(await page.evaluate(() => document.body.innerHTML.includes("a-new-secret-value"))).toBe(false);
});

test("the list says which server has tools that changed or are new, Review opens its tools, the Usage tab shows who is connected, and Switches goes to the Agent section", async ({ page }) => {
  const st = newState();
  st.list.push({ id: "notion", title: "Notion", url: "https://mcp.notion.com/mcp", status: "published", auth: { mode: "oauth", auto: true }, audience: { everyone: true }, limits: {}, secrets: {}, usage: {}, toolsOn: 1,
    tools: [{ name: "search_pages", enabled: true, level: "read" }, { name: "share_page", enabled: false, changed: true }, { name: "make_page", enabled: false, new: true }], publishable: "" });
  await stub(page, st);
  await page.goto(GO);
  const banner = card(page).locator(".int-banner");
  await expect(banner).toContainText("Notion: 1 tool changed since you approved it. It is off until you look.");
  await expect(banner).toContainText("1 new tool was found");
  await expect(card(page).getByRole("link", { name: "Switches" })).toHaveAttribute("href", "#site/genie-agent");
  // a server without a logo shows the first letter of its name
  await expect(card(page).locator(".int-logo", { hasText: "N" })).toHaveCount(1);
  await banner.getByRole("button", { name: "Review" }).click();
  await expect(card(page).locator(".int-tool")).toHaveCount(3);
  await expect(page.getByRole("button", { name: "3 Tools" })).toBeVisible();
  await page.getByRole("button", { name: "← All servers" }).click();
  await card(page).getByRole("tab", { name: "Usage" }).click();
  await expect(card(page).locator("table").first()).toContainText("uid-ann");
  await expect(card(page).locator("table").nth(1)).toContainText("get_issue");
  await expect(card(page).locator("table").nth(1)).not.toContainText("args");
});

test("the old address #integrations leads to the section under Genie", async ({ page }) => {
  await stub(page, newState());
  await page.goto("/admin#integrations");
  await expect(page).toHaveURL(/#site\/genie-integrations$/);
  await expect(card(page)).toBeVisible();
});
