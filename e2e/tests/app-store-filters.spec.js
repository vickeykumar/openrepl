// The quick App Store dialog (page/33-app-store.js): the category and language filters, a search that reads the summary, the count on My apps,
// the last tab, the usage line on a card, the question before a big app, "Suggested for this file" and "Recently switched on". The signed-in
// side runs against a stand-in for /store/api; the real server is covered by the Go tests. Each is tried on the owner's page and on a
// collaborator's page of a shared session.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

function views() {
  return [
    { name: "numpy", kind: "lib", state: "installed", version: "2.1", id: "numpy-2.1", safe: true, on: true, category: "Data science", repls: ["python", "ipython3"], summary: "Arrays and fast maths.", use: "import numpy", mod: "numpy", level: "core" },
    { name: "pandas", kind: "lib", state: "available", safe: true, category: "Data science", repls: ["python", "ipython3"], summary: "Tables and time series: read, clean, group and join data.", use: "import pandas", mod: "pandas", instMB: 158, ramMB: 64 },
    { name: "pytorch-cpu", kind: "lib", state: "available", safe: false, category: "Data science", repls: ["python"], summary: "Deep learning.", use: "import torch", mod: "torch", instMB: 747, ramMB: 226 },
    { name: "pytest", kind: "lib", state: "installed", version: "8.0", id: "pytest-8.0", safe: true, on: false, category: "Dev tools", repls: ["python"], summary: "Write and run tests.", use: "import pytest", mod: "pytest" },
    { name: "cmake", kind: "tool", state: "available", safe: true, category: "Build tools", repls: ["c", "cpp"], summary: "Build systems for C and C++.", use: "cmake --version" },
    { name: "jar-gson", kind: "jar", state: "available", safe: false, category: "Java libraries", repls: ["java"], summary: "JSON for Java objects.", use: "import com.google.gson.Gson;", mod: "com.google.gson" },
    { name: "mystery", kind: "lib", state: "installed", version: "1", id: "mystery-1", safe: true, on: true, repls: ["python"], summary: "An app with no category." },
  ];
}

function fake(page) {
  const st = { v: views(), calls: [] };
  page.route("**/store/api/**", async (route) => {
    const req = route.request();
    const what = new URL(req.url()).pathname.split("/store/api/")[1];
    const body = req.method() === "POST" ? req.postDataJSON() : {};
    if (req.method() === "POST") st.calls.push([what, body]);
    if (what === "switch") { const a = st.v.find((x) => x.id === body.id); if (a) a.on = body.on; }
    const json = (o) => route.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
    if (what === "notices") return json({ notices: [] });
    return json({ ok: true, views: st.v, on: st.v.filter((x) => x.on).map((x) => x.id), builtins: [{ name: "Go", version: "go1.23", used: ["go"], docs: "https://go.dev/doc/" }], envScript: "/store/envs/me/env.sh", installer: "fake", sandbox: true });
  });
  return st;
}

async function signedIn(page) {
  await page.waitForFunction(() => window.AppStore && window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await page.evaluate(() => { document.body.classList.add("is-signed-in"); try { localStorage.removeItem("appstore-tab"); localStorage.removeItem("appstore-recent"); } catch (e) { /* no storage */ } });
}

async function ownerPage(browser) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.AppStore && window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.editor && window.editor.env && window.editor.env.editor);
  await waitForTerminalReady(page);
  return { ctx, page };
}

async function viewerPage(browser, owner) {
  const link = await owner.page.evaluate(() => document.getElementById("share-it").value);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/" + new URL(link).hash);
  await page.waitForFunction(() => window.AppStore && window.FileBrowser && window.FileBrowser.ready());
  expect(await page.evaluate(() => isMaster())).toBe(false);
  return { ctx, page };
}

const chip = (page, name) => page.locator(`.appstore__chip[data-category="${name}"]`);
const apps = (page) => page.locator("#appstore-list .appstore__app:not(.is-builtin)"); // the built-in programs are listed apart

async function filtersWork(page) {
  fake(page);
  await signedIn(page);
  await page.evaluate(() => window.AppStore.open());
  // Browse: every category is a chip with its count; the biggest first, "Other" (an app with none) last
  await expect(page.locator('#appstore-filters [role="group"]')).toHaveAttribute("aria-label", "Category");
  await expect(chip(page, "All")).toContainText("7");
  await expect(chip(page, "Data science")).toContainText("3");
  await expect(page.locator(".appstore__chips .appstore__chip").last()).toHaveText(/Other/);
  await expect(apps(page)).toHaveCount(7);
  // one category at a time; the chosen chip says it is pressed; clicking it again goes back to All
  await chip(page, "Data science").click();
  await expect(chip(page, "Data science")).toHaveAttribute("aria-pressed", "true");
  await expect(apps(page)).toHaveCount(3);
  await expect(page.locator(".appstore__showing")).toContainText("Showing 3 apps in Data science");
  await chip(page, "Data science").click();
  await expect(apps(page)).toHaveCount(7);
  // a search reads the summary and the category, not only the name; the chip counts follow the search
  await page.locator("#appstore-search").fill("tests");
  await expect(apps(page)).toHaveCount(1);
  await expect(page.locator('#appstore-list [data-app="pytest"]')).toBeVisible();
  await page.locator("#appstore-search").fill("json java");
  await expect(page.locator('#appstore-list [data-app="jar-gson"]')).toBeVisible();
  await page.locator("#appstore-search").fill("tables");
  await expect(chip(page, "Data science")).toContainText("1");
  await expect(chip(page, "Dev tools")).toContainText("0"); // a category the search emptied stays, dimmed
  await expect(chip(page, "Dev tools")).toHaveClass(/is-empty/);
  // category and search together, and the empty result with its two ways out
  await chip(page, "Data science").click();
  await page.locator("#appstore-search").fill("tables nothing");
  await expect(page.locator("[data-empty]")).toContainText("in Data science");
  await page.getByRole("button", { name: "Search all categories" }).click();
  await expect(chip(page, "All")).toContainText("0"); // nothing in any category matches "tables nothing"
  await page.locator("#appstore-search").fill("");
  // the language
  await page.locator("#appstore-filters select[aria-label=Language]").selectOption("c");
  await expect(apps(page)).toHaveCount(1);
  await expect(page.locator('#appstore-list [data-app="cmake"]')).toBeVisible();
  await page.getByRole("button", { name: "Clear filter" }).click();
  await expect(apps(page)).toHaveCount(7);
  // the arrow keys move along the chips
  await chip(page, "All").focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.locator(".appstore__chips .appstore__chip").nth(1)).toBeFocused();
  // the filters start at All when the dialog opens again
  await chip(page, "Dev tools").click();
  await page.locator("#appstore-close").click();
  await page.evaluate(() => window.AppStore.open());
  await expect(chip(page, "All")).toHaveAttribute("aria-pressed", "true");
  await expect(apps(page)).toHaveCount(7);
}

async function myAppsWork(page) {
  fake(page);
  await signedIn(page);
  await page.evaluate(() => window.AppStore.open());
  // the count on the tab is the apps that are on (numpy and mystery); the tab is remembered
  await expect(page.locator("#appstore-tab-mine i.appstore__count")).toHaveText("2");
  await page.locator("#appstore-tab-mine").click();
  await expect(apps(page)).toHaveCount(2);
  await expect(chip(page, "All")).toContainText("3"); // two apps and the built-in Go
  await expect(chip(page, "Built in")).toContainText("1");
  await expect(chip(page, "Dev tools")).toHaveCount(0); // no empty chip: pytest is not on
  await chip(page, "Built in").click();
  await expect(page.locator('#appstore-list [data-builtin="Go"]')).toBeVisible();
  await expect(apps(page)).toHaveCount(0);
  await chip(page, "Built in").click();
  await page.locator("#appstore-close").click();
  await page.evaluate(() => window.AppStore.open());
  await expect(page.locator("#appstore-tab-mine")).toHaveAttribute("aria-selected", "true"); // the last tab
  // a name from Genie or the terminal opens Browse
  await page.locator("#appstore-close").click();
  await page.evaluate(() => window.AppStore.open("pandas"));
  await expect(page.locator("#appstore-tab-browse")).toHaveAttribute("aria-selected", "true");
}

async function cardsWork(page) {
  const st = fake(page);
  await signedIn(page);
  page.on("dialog", (d) => d.accept("for my project")); // the quick dialog asks the reason of a request with a browser prompt
  await page.evaluate(() => window.AppStore.open());
  // the usage line on a card, as on the app's page
  await expect(page.locator('[data-app="numpy"] .appstore__use')).toHaveText("import numpy");
  await expect(page.locator('[data-app="cmake"] .appstore__use')).toHaveText("cmake --version");
  // a big app asks first: the size and the memory, Cancel, and "What is smaller?" goes to its category
  await page.locator('[data-app="pytorch-cpu"] button', { hasText: "Request" }).click();
  await expect(page.locator(".appstore__confirm")).toContainText("747 MB");
  await expect(page.locator(".appstore__confirm")).toContainText("226 MB of memory");
  await expect(page.locator(".appstore__confirm")).toContainText("An admin has to approve it");
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator(".appstore__confirm")).toHaveCount(0);
  expect(st.calls.find((c) => c[0] === "request")).toBeFalsy();
  await page.locator('[data-app="pytorch-cpu"] button', { hasText: "Request" }).click();
  await page.getByRole("button", { name: "What is smaller?" }).click();
  await expect(chip(page, "Data science")).toHaveAttribute("aria-pressed", "true");
  await page.locator('[data-app="pytorch-cpu"] button', { hasText: "Request" }).click();
  await page.getByRole("button", { name: "Request it" }).click();
  await expect.poll(() => st.calls.find((c) => c[0] === "request")).toBeTruthy();
  // a small app installs at once, with no question
  await page.getByRole("button", { name: "Clear filter" }).click();
  await page.locator("#appstore-search").fill("cmake");
  await page.locator('[data-app="cmake"] button', { hasText: "Install" }).click();
  await expect.poll(() => st.calls.find((c) => c[0] === "install" && c[1].name === "cmake")).toBeTruthy();
  await expect(page.locator(".appstore__confirm")).toHaveCount(0);
}

async function fileAndRecentWork(page) {
  const st = fake(page);
  await signedIn(page);
  await page.evaluate(() => {
    const s = document.getElementById("select-lang");
    if (s) { s.value = "python"; s.dispatchEvent(new Event("change")); }
    if (window.editor && window.editor.env && window.editor.env.editor) window.editor.env.editor.setValue("import numpy\nimport pandas as pd\n", -1);
  });
  await page.evaluate(() => window.AppStore.open());
  const sugg = page.locator('[data-suggest="file"]');
  await expect(sugg).toContainText("Your file uses pandas");
  await expect(sugg).not.toContainText("numpy"); // it is on already
  // recently switched on: the apps the user turned on, newest first, one click to switch
  await page.locator('[data-app="pytest"] .appstore__switch').click();
  await expect.poll(() => st.calls.find((c) => c[0] === "switch")).toBeTruthy();
  await page.locator("#appstore-tab-mine").click();
  const rec = page.locator('[data-suggest="recent"]');
  await expect(rec).toContainText("pytest");
  await rec.getByRole("button", { name: "pytest" }).click();
  await expect.poll(() => st.calls.filter((c) => c[0] === "switch").length).toBe(2);
}

for (const [name, fn] of [["the category and language filters, and a search that reads the summary", filtersWork], ["My apps: the count, the built-in chip, the last tab", myAppsWork], ["a card's usage line and the question before a big app", cardsWork], ["Suggested for this file, and Recently switched on", fileAndRecentWork]]) {
  test(`${name}: on the owner's page`, async ({ browser }) => {
    const owner = await ownerPage(browser);
    await fn(owner.page);
    await owner.ctx.close();
  });
  test(`${name}: on a collaborator's page`, async ({ browser }) => {
    const owner = await ownerPage(browser);
    const viewer = await viewerPage(browser, owner);
    await fn(viewer.page);
    await viewer.ctx.close();
    await owner.ctx.close();
  });
}
