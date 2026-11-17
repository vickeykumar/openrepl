// The Catalogue tab of the admin's App Store view (resources/js/admin.js). The admin API is a stand-in; the real routes are covered by the
// Go tests. It tries every button of the tab: Add app, Add plugin, Bulk add, Packs, Export, Import, Verify now, the filters, Edit, Install, Retire,
// the disk of every server, and a request that is approved through the Add form.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const ADMIN_HTML = fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "admin.html"), "utf8");

function stub(page) {
  const st = { posts: [] };
  // the page of the dashboard is for a signed-in admin: the stand-in serves the file itself, and its scripts and styles come from the server
  page.route(/\/admin\/?(\?.*)?(#.*)?$/, (r) => (r.request().resourceType() === "document" ? r.fulfill({ contentType: "text/html", body: ADMIN_HTML }) : r.continue()));
  const entries = [
    { id: "numpy", name: "numpy", kind: "lib", repls: ["python", "ipython3"], level: "core", manager: "pixi", install: "anyone", docs: "https://numpy.org/doc/stable/", summary: "Arrays" },
    { id: "polars", name: "polars", kind: "lib", repls: ["python"], level: "optional", manager: "pixi", install: "approval", docs: "https://docs.pola.rs/", summary: "Tables" },
    { id: "web:bootstrap", name: "bootstrap", kind: "weblib", repls: ["html", "css"], level: "core", manager: "npm", install: "anyone", docs: "https://getbootstrap.com/docs/5.3/", summary: "Styles" },
    { id: "plugin:eruda", name: "Eruda console", kind: "plugin", place: "preview", level: "popular", manager: "npm", package: "eruda", install: "admin", docs: "https://eruda.liriliri.io/docs/", summary: "Dev tools" },
  ];
  const repls = ["python", "ipython3", "html", "css", "c", "cpp", "bash"];
  page.route("**/admin/gateway", (r) => r.fulfill({ contentType: "application/json", body: JSON.stringify({ mode: "single" }) }));
  page.route("**/admin/store/catalogue**", async (r) => {
    const req = r.request();
    const json = (o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
    if (req.method() === "POST") { const b = req.postDataJSON(); st.posts.push(b); if (b.action === "bulkdry") return json({ rows: [{ name: "seaborn-base", solves: true, sizeMB: 310, version: "0.13", result: "ready to add" }, { name: "nothere", solves: false, result: "not on conda-forge" }] });
      if (b.action === "lookup") return json({ solves: true, version: "1.7.2", packages: 38, diskMB: 210, left: [{ node: "gateway", leftMB: 8000, belowReserve: false }, { node: "worker-pi-2", leftMB: 2600, belowReserve: true }] });
      if (b.action === "verify") return json({ ok: true, results: [{ name: "numpy", state: "ok" }, { name: "polars", state: "failed" }] });
      if (b.action === "import") return json({ ok: true, report: { added: 2, packs: 1, rejected: [] } });
      return json({ ok: true }); }
    return json({ entries, packs: [{ id: "data-science", name: "Data science", repls: ["python"], entries: ["numpy", "polars"] }], repls, places: ["preview", "editor", "terminal"], checks: ["python-import", "node-require", "version-flag", "file-exists"],
      disk: [{ node: "gateway", usedMB: 3900, limitMB: 15360, reserveMB: 3072, belowReserve: false }, { node: "worker-pi-2", usedMB: 9600, limitMB: 15360, reserveMB: 3072, belowReserve: true }],
      settings: { limitMB: 15360, reserveMB: 3072, perDay: 5, scratchSoftMB: 256, scratchServerMB: 2048, limitSet: 0 }, now: 1794700000 });
  });
  page.route("**/admin/store", async (r) => {
    const req = r.request();
    if (req.method() === "POST") { st.posts.push(req.postDataJSON()); return r.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) }); }
    return r.fulfill({ contentType: "application/json", body: JSON.stringify({ enabled: true, installer: "pixi", dir: "/store", python: "3.10", channel: "conda-forge", sizeMB: 3900, limitMB: 15360, now: 1794700000,
      apps: [{ name: "numpy", kind: "lib", state: "installed", version: "2.1", id: "numpy-2.1", size: 96e6, at: 1 }], defaults: [], users: {}, apt: [], safeList: [], limitSet: 0,
      requests: [{ name: "seaborn", kind: "lib", repl: "python", askers: ["a", "b", "c"], reason: "heatmaps", status: "waiting", at: 1794600000 }], reports: [],
      nodes: [{ node: "gateway", have: ["numpy-2.1"], sizeMB: 3900 }, { node: "worker-pi-2", have: [], sizeMB: 9600, failed: { "numpy-2.1": "no internet" } }] }) });
  });
  page.route(/\/admin\/(health|settings|stats|audit|feedback|workers|sessions|jobs)(\?.*)?$/, (r) => r.fulfill({ contentType: "application/json", body: "{}" }));
  return st;
}

async function openCatalogue(page) {
  await page.goto("/admin#store");
  await page.getByRole("button", { name: /^Catalogue/ }).click();
}

test("the Catalogue tab lists the entries with their REPL, kind, level, manager and who may install, and has the filters", async ({ page }) => {
  stub(page);
  await openCatalogue(page);
  const row = page.locator("tr", { hasText: "numpy" }).first();
  await expect(row).toContainText("python, ipython3");
  await expect(row).toContainText("core");
  await expect(row).toContainText("pixi");
  await expect(row).toContainText("anyone");
  await expect(page.locator("tr", { hasText: "polars" })).toContainText("needs approval");
  await expect(page.locator("tr", { hasText: "Eruda console" })).toContainText("admin only");
  await expect(page.locator("tr", { hasText: "bootstrap" })).toContainText("browser lib");
  await page.getByLabel("Filter by REPL").selectOption("html");
  await expect(page.locator("tr", { hasText: "polars" })).toHaveCount(0);
  await expect(page.locator("tr", { hasText: "bootstrap" })).toHaveCount(1);
  await page.getByLabel("Filter by REPL").selectOption("");
  await page.getByLabel("Filter by level").selectOption("optional");
  await expect(page.locator("tr", { hasText: "numpy" })).toHaveCount(0);
  await expect(page.locator("tr", { hasText: "polars" })).toHaveCount(1);
});

test("the disk of the store is shown for every server, with the reserve", async ({ page }) => {
  stub(page);
  await openCatalogue(page);
  await expect(page.locator('[data-disk="gateway"]')).toContainText("3.8 GB used");
  await expect(page.locator('[data-disk="gateway"]')).toContainText("15 GB");
  await expect(page.locator('[data-disk="gateway"]')).toContainText("3 GB reserve");
  await expect(page.locator('[data-disk="worker-pi-2"]')).toContainText("free below reserve soon");
});

test("Add app: a lookup says whether it solves and the disk left; Add and install sends the entry and asks for the install", async ({ page }) => {
  const st = stub(page);
  await openCatalogue(page);
  await page.getByRole("button", { name: "Add app" }).click();
  await page.getByLabel("Package name").fill("scikit-learn");
  await page.getByLabel("Check argument").fill("sklearn");
  await page.getByLabel("Documentation link").fill("https://scikit-learn.org/stable/");
  await page.getByRole("button", { name: "Look it up" }).click();
  await expect(page.locator("[data-lookup]")).toContainText("1.7.2");
  await expect(page.locator("[data-lookup]")).toContainText("worker-pi-2: 2.5 GB left (below the reserve)");
  await page.getByRole("button", { name: "Add and install" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "add")).toBeTruthy();
  const add = st.posts.find((p) => p.action === "add");
  expect(add.install).toBe(true);
  expect(add.entry).toMatchObject({ id: "scikit-learn", kind: "lib", manager: "pixi", check: "python-import", checkArg: "sklearn", docs: "https://scikit-learn.org/stable/" });
});

test("Add plugin needs the exact version, the entry file and the ticks; an editor extension asks twice", async ({ page }) => {
  const st = stub(page);
  await openCatalogue(page);
  await page.getByRole("button", { name: "Add plugin" }).click();
  await page.getByLabel("npm package").fill("eruda");
  await page.getByLabel("Exact version").fill("3.4.3");
  await page.getByLabel("Entry file").fill("eruda.js");
  await page.locator("dialog").getByRole("button", { name: "Add plugin" }).click();
  expect(st.posts.find((p) => p.action === "add")).toBeFalsy(); // the publisher and licence tick is missing
  await page.getByLabel("I checked the publisher and the licence").check();
  await page.getByLabel("Adapter type").selectOption("editor-extension");
  await page.locator("dialog").getByRole("button", { name: "Add plugin" }).click();
  expect(st.posts.find((p) => p.action === "add")).toBeFalsy(); // the second tick is missing
  await page.getByLabel("I understand this plugin runs in the page").check();
  await page.locator("dialog").getByRole("button", { name: "Add plugin" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "add")).toBeTruthy();
  expect(st.posts.find((p) => p.action === "add").entry).toMatchObject({ id: "plugin:eruda", kind: "plugin", version: "3.4.3", adapter: "editor-extension", place: "preview" });
});

test("Bulk add runs a dry run first and adds nothing until it is done", async ({ page }) => {
  const st = stub(page);
  await openCatalogue(page);
  await page.getByRole("button", { name: "Bulk add" }).click();
  await page.getByLabel("Names, one for each line or separated by spaces").fill("seaborn-base nothere");
  await page.locator("dialog").getByRole("button", { name: "Add what solved" }).click();
  expect(st.posts.find((p) => p.action === "bulkadd")).toBeFalsy(); // no dry run yet
  await page.getByRole("button", { name: "Dry run" }).click();
  await expect(page.locator("[data-bulk]")).toContainText("310 MB");
  await expect(page.locator("[data-bulk]")).toContainText("not on conda-forge");
  await page.locator("dialog").getByRole("button", { name: "Add what solved" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "bulkadd")).toBeTruthy();
  expect(st.posts.find((p) => p.action === "bulkadd")).toMatchObject({ kind: "lib", repls: ["python"], install: false });
});

test("Packs, Export, Import and Verify now", async ({ page }) => {
  const st = stub(page);
  await openCatalogue(page);
  await expect(page.getByRole("link", { name: "Export" })).toHaveAttribute("href", /admin\/store\/catalogue\?export=1/);
  await page.getByRole("button", { name: "Packs" }).click();
  await expect(page.locator('[data-pack="data-science"]')).toContainText("2 apps");
  await page.locator('[data-pack="data-science"]').getByRole("button", { name: /Install/ }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "packinstall")).toBeTruthy();
  await page.getByRole("button", { name: "Import" }).click();
  await page.getByLabel("A catalogue file (JSON)").fill('{"version":1,"entries":[],"packs":[]}');
  await page.getByRole("button", { name: /Check it/ }).click();
  await expect(page.locator("dialog")).toContainText("2 apps and 1 packs are valid");
  await page.locator("dialog").getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Verify now" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "verify")).toBeTruthy();
});

test("Edit, Install and Remove of an entry; a plugin of ours or a browser library can be installed from the list", async ({ page }) => {
  const st = stub(page);
  await openCatalogue(page);
  await page.locator("tr", { hasText: "polars" }).getByRole("button", { name: "Install" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "install")).toBeTruthy();
  expect(st.posts.find((p) => p.action === "install").id).toBe("polars");
  await page.locator("tr", { hasText: "bootstrap" }).getByRole("button", { name: "Install" }).click();
  await expect.poll(() => st.posts.filter((p) => p.action === "install").length).toBe(2);
  await page.locator("tr", { hasText: "polars" }).getByRole("button", { name: "Retire" }).click();
  await page.locator("dialog").getByRole("button", { name: "Retire" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "remove")).toBeTruthy();
  await page.locator("tr", { hasText: "numpy" }).getByRole("button", { name: "Edit" }).click();
  await expect(page.getByLabel("Package name")).toHaveValue("numpy");
  await page.locator("dialog").getByRole("button", { name: "Cancel" }).click();
});

test("a request shows its language and a lookup; Approve opens the Add form filled in; Reject needs a reason", async ({ page }) => {
  const st = stub(page);
  await page.goto("/admin#store");
  const row = page.locator("tr", { hasText: "seaborn" });
  await expect(row).toContainText("python");
  await row.getByRole("button", { name: "Look it up" }).click();
  await expect(page.locator('[data-lookup="seaborn"]')).toContainText("solves: 1.7.2");
  await row.getByRole("button", { name: "Reject" }).click();
  await page.locator("dialog").getByRole("button", { name: "Reject with this reason" }).click();
  expect(st.posts.find((p) => p.action === "reject")).toBeFalsy(); // a reason is required
  await page.locator("dialog input").fill("Too big: use matplotlib-base.");
  await page.locator("dialog").getByRole("button", { name: "Reject with this reason" }).click();
  await expect.poll(() => st.posts.find((p) => p.action === "reject")).toBeTruthy();
  await row.getByRole("button", { name: "Approve" }).click();
  await expect(page.getByLabel("Package name")).toHaveValue("seaborn");
});
