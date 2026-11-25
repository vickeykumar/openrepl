// The plugins of the preview browser (App Store, LLD 18): the Viewport switcher, the Outline and the Console (ours), and Eruda and vConsole
// (third party, served by this site and added to the previewed page by the server). Each one is tried on the owner's page and on a
// collaborator's page of a shared session: each person has their own pane. Also the workspace and scratch numbers in the status bar.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady, unfoldPreviewBar } = require("./helpers");

const stamp = Date.now().toString(36);
const dir = `plug${stamp}`;
const PAGE = '<!doctype html><html><head><title>t</title></head><body><nav aria-label="menu">n</nav><h1>First heading</h1><h2>Second heading</h2><p style="height:2000px">x</p><h2 id="last">Last heading</h2><script>console.log("hello from the page")</script></body></html>';

async function ownerPage(browser) {
  const ctx = await browser.newContext();
  await unfoldPreviewBar(ctx);
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.EditorTabs && window.PreviewPlugins);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.evaluate(async ([d, html]) => {
    await window.FileBrowser.create(d, "folder");
    await window.FileBrowser.create(d + "/index.html", "file");
    await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/index.html"), method: "POST", processData: false, data: btoa(html), contentType: "application/octet-stream" });
  }, [dir, PAGE]);
  return { ctx, page };
}

async function viewerPage(browser, owner) {
  const link = await owner.page.evaluate(() => document.getElementById("share-it").value);
  const ctx = await browser.newContext();
  await unfoldPreviewBar(ctx);
  const page = await ctx.newPage();
  await page.goto("/" + new URL(link).hash);
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.PreviewPlugins);
  expect(await page.evaluate(() => isMaster())).toBe(false);
  return { ctx, page };
}

async function openPreview(page) {
  await page.evaluate((d) => window.Preview.open(window.homedir + "/" + d + "/index.html"), dir);
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.frameLocator("#preview-frame").locator("h1")).toHaveText("First heading");
}

async function tryPlugins(page) {
  await openPreview(page);
  // Viewport switcher: the page is shown at a phone width
  await expect(page.locator("#preview-size")).toBeVisible();
  await page.locator("#preview-size").selectOption("phone");
  await expect.poll(() => page.locator("#preview-device").evaluate((n) => Math.round(n.getBoundingClientRect().width))).toBeLessThan(420);
  await page.locator("#preview-size").selectOption("fit");
  // Console: what the page wrote
  await page.locator("#preview-console-button").click();
  await expect(page.locator("#preview-console-body")).toContainText("hello from the page");
  // Outline: the headings and landmarks; a click scrolls the page to one
  await page.locator("#preview-tab-outline").click();
  const items = page.locator("#preview-console-body .preview-outline__item");
  await expect(items).toHaveCount(4); // nav, h1, h2, h2
  await expect(items.nth(1)).toContainText("First heading");
  await items.nth(3).click();
  await expect.poll(() => page.frameLocator("#preview-frame").locator("#last").evaluate((n) => Math.round(n.getBoundingClientRect().top))).toBeLessThan(400);
}

test("the Viewport switcher, the Console and the Outline work on the owner's page", async ({ browser }) => {
  const owner = await ownerPage(browser);
  await tryPlugins(owner.page);
  await owner.ctx.close();
});

test("the Viewport switcher, the Console and the Outline work on a collaborator's page of a shared session", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  await tryPlugins(viewer.page);
  await viewer.ctx.close();
  await owner.ctx.close();
});

test("a plugin that a user turned off has no control in the pane", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const page = owner.page;
  await page.route("**/store/api/preview-plugins", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ plugins: [
    { id: "plugin:viewport", ours: true, on: false, ready: true }, { id: "plugin:outline", ours: true, on: false, ready: true }, { id: "plugin:console", ours: true, on: true, ready: true } ] }) }));
  await page.evaluate(() => { document.body.classList.add("is-signed-in"); window.PreviewPlugins.reload(); }); // the pane asks only for a signed-in user
  await openPreview(page);
  await page.waitForFunction(() => !window.PreviewPlugins.state().viewport);
  await expect(page.locator("#preview-size")).toBeHidden();
  await page.locator("#preview-console-button").click();
  await expect(page.locator("#preview-tab-outline")).toBeHidden();
  await expect(page.locator("#preview-tab-console")).toBeVisible();
  await owner.ctx.close();
});

// Eruda and vConsole: the server adds a script of this site and the call that starts it. The server of this test has no npm bundle installed,
// so the page and the script are answered by the test, with the headers the real server sends (the policy of the page is not loosened).
async function thirdParty(page, name, file, init) {
  await page.route("**/preview/lib/" + name + "/" + file, (route) => route.fulfill({ contentType: "application/javascript", headers: { "Access-Control-Allow-Origin": "*", "X-Content-Type-Options": "nosniff" },
    body: "window." + name + "Loaded=true;window.eruda={init:function(){var d=document.createElement('div');d.id='tool-" + name + "';d.textContent='" + name + " started';document.body.appendChild(d)}};window.VConsole=function(){var d=document.createElement('div');d.id='tool-" + name + "';d.textContent='" + name + " started';document.body.appendChild(d)};" }));
  await page.route(/\/preview\/[^/]+\/f\/.*index\.html(\?.*)?$/, async (route) => {
    const res = await route.fetch();
    const starter = "(function(){function s(){try{" + init + "}catch(e){}}if(document.readyState===\"loading\"){document.addEventListener(\"DOMContentLoaded\",s)}else{s()}})()";
    const body = (await res.text()).replace("<head>", '<head><script src="/preview/lib/' + name + "/" + file + '"></script><script>' + starter + "</script>");
    await route.fulfill({ response: res, body });
  });
}

for (const [name, file, init] of [["eruda", "eruda.js", "eruda.init()"], ["vconsole", "dist/vconsole.min.js", "new VConsole()"]]) {
  test(`${name} starts in the previewed page: on the owner's page`, async ({ browser }) => {
    const owner = await ownerPage(browser);
    await thirdParty(owner.page, name, file, init);
    await openPreview(owner.page);
    await expect(owner.page.frameLocator("#preview-frame").locator("#tool-" + name)).toHaveText(name + " started");
    await owner.ctx.close();
  });
  test(`${name} starts in the previewed page: on a collaborator's page`, async ({ browser }) => {
    const owner = await ownerPage(browser);
    const viewer = await viewerPage(browser, owner);
    await thirdParty(viewer.page, name, file, init);
    await openPreview(viewer.page);
    await expect(viewer.page.frameLocator("#preview-frame").locator("#tool-" + name)).toHaveText(name + " started");
    await viewer.ctx.close();
    await owner.ctx.close();
  });
}

test("the status bar shows the workspace and the scratch folder beside Connected, and turns amber and red", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const page = owner.page;
  await expect(page.locator("#term-footer-space")).toBeVisible({ timeout: 20000 });
  await expect(page.locator("#term-footer-ws")).toContainText(/Workspace \d+ \/ 50 MB/);
  await expect(page.locator("#term-footer-scratch")).toContainText(/Scratch \d+ \/ \d+ MB/);
  await expect(page.locator("#term-footer-scratch")).toHaveAttribute("data-level", "ok");
  for (const [used, level] of [[210, "warn"], [256, "bad"]]) {
    await page.route("**/store/api/space", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ workspaceMB: 3, workspaceLimitMB: 50, scratchMB: used, scratchLimitMB: 256, scratch: 1 }) }));
    await page.evaluate(() => window.__updateSpace && window.__updateSpace());
    await expect.poll(() => page.locator("#term-footer-scratch").getAttribute("data-level"), { timeout: 15000 }).toBe(level);
    await page.unroute("**/store/api/space");
  }
  await owner.ctx.close();
});
