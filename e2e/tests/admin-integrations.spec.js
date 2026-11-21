// The Integrations page of the admin dashboard (src/resources/js/admin-integrations.js, server/genie_integrations*.go): add a server with a name and an address,
// test it, switch tools on with a level, publish it, answer a request for a server, and keep a secret write-only.
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
    reqs: [{ id: "r1", kind: "server", user: "uid-ann", title: "Acme wiki", url: "https://wiki.acme.dev/mcp", note: "Search our wiki while I write docs.", signin: "oauth", docs: "https://wiki.acme.dev/docs", status: "waiting", at: "2026-11-21T08:12:00Z" }],
  };
}

async function stub(page, st) {
  await page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  if (process.env.GENIE_LOCAL_ASSETS) {
    for (const [u, type, file] of [["/js/admin.js", "application/javascript", "js/admin.js"], ["/js/admin-integrations.js", "application/javascript", "js/admin-integrations.js"], ["/js/admin-site.js", "application/javascript", "js/admin-site.js"], ["/css/admin.css", "text/css", "css/admin.css"]]) {
      await page.route((x) => x.pathname.endsWith(u), (r) => r.fulfill({ contentType: type, body: fs.readFileSync(path.join(ROOT, file)) }));
    }
  }
  const json = (r, o, status = 200) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(o) });
  await page.route("**/admin/gateway", (r) => json(r, { mode: "single" }));
  await page.route(/\/admin\/(health|settings|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => json(r, {}));
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
      Object.assign(cur, { title: i.title, url: i.url, keywords: i.keywords, description: i.description, auth: i.auth, audience: i.audience, limits: i.limits, publishable: "Test it first." });
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

test("an admin adds a server with a name and an address, tests it, switches tools on with a level and publishes it", async ({ page }) => {
  const st = newState();
  await stub(page, st);
  await page.goto("/admin#integrations");
  await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
  const table = page.locator("table").first();
  await expect(table.locator("tbody tr")).toHaveCount(1);
  await expect(table.locator("tbody tr").first()).toContainText("GitHub");
  await expect(table.locator("tbody tr").first()).toContainText("Published");
  await page.getByRole("button", { name: "+ Add a server" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Notion");
  await page.getByLabel("Server address").fill("https://mcp.notion.com/mcp");
  await page.getByLabel("Keywords").fill("notion, pages");
  await page.getByRole("button", { name: "Save and test" }).click();
  await expect(page.getByText("Last test 2026-11-21 09:00:00: ok")).toBeVisible();
  expect(st.posts.map((p) => p.op)).toEqual(["save", "test"]);
  expect(st.posts[0].integration.keywords).toEqual(["notion", "pages"]);
  expect(st.posts[0].integration.url).toBe("https://mcp.notion.com/mcp");
  // the sign-in found by the test is the one that is chosen, and the tools are off and new
  await expect(page.locator('input[name="signin"][value="none"]')).toBeChecked();
  const tools = page.locator("table").nth(0).locator("tbody tr");
  await expect(tools).toHaveCount(3);
  await expect(tools.first()).toContainText("search_pages");
  await expect(tools.first()).toContainText("new");
  // a tool on with a level; the other two stay off
  await tools.nth(0).getByRole("checkbox").check();
  await tools.nth(0).getByLabel("Level of search_pages").selectOption("read");
  await tools.nth(1).getByRole("checkbox").check();
  await tools.nth(1).getByLabel("Level of create_page").selectOption("write");
  await page.getByRole("button", { name: "Save tools" }).click();
  await expect.poll(() => st.posts.filter((p) => p.op === "tools").length).toBe(1);
  expect(st.posts.filter((p) => p.op === "tools")[0].tools.map((t) => [t.name, t.enabled, t.level])).toEqual([["search_pages", true, "read"], ["create_page", true, "write"], ["delete_page", false, ""]]);
  // who sees it, then publish
  await page.getByLabel("Everyone signed in").check();
  await page.getByRole("button", { name: "Publish", exact: true }).click();
  await expect(page.locator(".pill", { hasText: "Published" }).first()).toBeVisible();
  expect(st.posts[st.posts.length - 1].op).toBe("publish");
});

test("a request for a server is plain text, and an admin answers it in one click for one person, for everyone, as a draft or with a reason", async ({ page }) => {
  const st = newState();
  await stub(page, st);
  await page.goto("/admin#integrations");
  await page.getByRole("tab", { name: /^Requests/ }).click();
  const q = page.locator(".form-row", { hasText: "Acme wiki" });
  await expect(q).toContainText("https://wiki.acme.dev/mcp");
  await expect(q).toContainText("Search our wiki while I write docs.");
  await expect(q).toContainText("they think the sign-in is oauth");
  await expect(q).toContainText("uid-ann");
  await q.getByRole("button", { name: "Approve for this person" }).click();
  await expect.poll(() => st.reqPosts.length).toBe(1);
  expect(st.reqPosts[0]).toMatchObject({ id: "r1", action: "approve_user" });
  await expect(page.getByText("Approved and published.")).toBeVisible();
});

test("a secret is write-only: the page says whether one is set and never holds it", async ({ page }) => {
  const st = newState();
  st.list[0].secrets = { oauthClientSecret: true };
  await stub(page, st);
  await page.goto("/admin#integrations");
  await page.locator("table").first().getByRole("button", { name: "Edit" }).click();
  // GitHub's sign-in is OAuth: the secret of the app is saved, and the field asks to type a new one to replace it
  await page.locator('input[name="signin"][value="oauth"]').check();
  const secret = page.getByLabel("Client secret");
  await expect(secret).toHaveAttribute("placeholder", /Saved/);
  await expect(secret).toHaveValue("");
  await secret.fill("a-new-secret-value");
  await secret.locator("xpath=following-sibling::button[1]").click();
  await expect.poll(() => st.posts.filter((p) => p.op === "secret").length).toBe(1);
  expect(st.posts.filter((p) => p.op === "secret")[0]).toMatchObject({ id: "github", kind: "oauthClientSecret", value: "a-new-secret-value" });
  // after the save the field is empty again and the secret is not anywhere in the page
  await expect(page.getByLabel("Client secret")).toHaveValue("");
  expect(await page.evaluate(() => document.body.innerHTML.includes("a-new-secret-value"))).toBe(false);
});

test("the list says which server has tools that changed or are new, the Usage tab shows who is connected and the last calls, and Switches goes to the site settings", async ({ page }) => {
  const st = newState();
  st.list.push({ id: "notion", title: "Notion", url: "https://mcp.notion.com/mcp", status: "published", auth: { mode: "oauth", auto: true }, audience: { everyone: true }, limits: {}, secrets: {}, usage: {}, toolsOn: 1,
    tools: [{ name: "search_pages", enabled: true, level: "read" }, { name: "share_page", enabled: false, changed: true }, { name: "make_page", enabled: false, new: true }], publishable: "" });
  await stub(page, st);
  await page.goto("/admin#integrations");
  await expect(page.locator(".warn")).toContainText("Notion: 1 tool changed since you approved it. They are off until you look.");
  await expect(page.locator(".warn")).toContainText("1 new tool was found");
  await expect(page.getByRole("link", { name: "Switches" })).toHaveAttribute("href", "#site");
  await page.locator(".warn").getByRole("button", { name: "Review" }).click();
  await expect(page.locator("table").first().locator("tbody tr")).toHaveCount(3);
  await expect(page.getByText("2 Test and sign-in")).toBeVisible();
  await page.getByRole("button", { name: "← All servers" }).click();
  await page.getByRole("tab", { name: "Usage" }).click();
  await expect(page.locator("table").first()).toContainText("uid-ann");
  await expect(page.locator("table").nth(1)).toContainText("get_issue");
  await expect(page.locator("table").nth(1)).not.toContainText("args");
});
