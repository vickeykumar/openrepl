// The App Store (page/33-app-store.js, server/appstore.go, LLD 18). A browser test cannot sign in, so the signed-in side is
// checked against the page's own calls, answered here by a small stand-in for the server; what the real server does is in the
// Go tests (appstore, server) and in the sandbox test of the helper.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.AppStore && window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
}

test("a guest cannot use the App Store: no button, a sign-in card, and the server refuses", async ({ page }) => {
  await ready(page);
  await expect(page.locator("#appstore-button")).toBeHidden();
  expect(await page.evaluate(() => window.AppStore.available())).toBe(false);
  const status = await page.evaluate(() => fetch("/store/api/list", { credentials: "same-origin" }).then((r) => r.status));
  expect([401, 503]).toContain(status);
  const post = await page.evaluate(() => fetch("/store/api/install", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: "flask" }) }).then((r) => r.status));
  expect([401, 503]).toContain(post);
  await page.evaluate(() => window.AppStore.open());
  await expect(page.locator("#appstore-dialog")).toBeVisible();
  await expect(page.locator(".appstore__guest")).toContainText("signed-in users");
  await page.locator("#appstore-close").click();
  await expect(page.locator("#appstore-dialog")).toBeHidden();
});

test("a signed-in user installs, switches on and applies an app; a failure can be reported", async ({ page }) => {
  await ready(page);
  const apps = {
    flask: { name: "flask", kind: "lib", state: "available", safe: true },
    numpy: { name: "numpy", version: "1.0.0", id: "numpy-1.0.0", kind: "lib", state: "installed", safe: true, on: false, sizeMB: 96 },
    pillow: { name: "pillow", kind: "lib", state: "failed", safe: true, err: "Resolving the dependencies: not compatible", log: "Found conflicts!" },
  };
  const on = [];
  const calls = [];
  const views = () => Object.values(apps).map((v) => ({ ...v, on: on.includes(v.id) }));
  await page.route("**/store/api/**", async (route) => {
    const req = route.request();
    const what = new URL(req.url()).pathname.split("/store/api/")[1];
    const body = req.method() === "POST" ? req.postDataJSON() : {};
    if (req.method() === "POST") calls.push([what, body]);
    if (what === "install") Object.assign(apps[body.name], { state: "installed", version: "1.0.0", id: body.name + "-1.0.0", sizeMB: 14, err: undefined });
    if (what === "switch") { const i = on.indexOf(body.id); if (body.on && i < 0) on.push(body.id); if (!body.on && i >= 0) on.splice(i, 1); }
    if (what === "report") Object.assign(apps[body.name], { reported: true, mine: true });
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, views: views(), on, envScript: "/store/envs/me/env.sh", installer: "fake" }) });
  });
  await page.evaluate(() => document.body.classList.add("is-signed-in"));
  await expect(page.locator("#appstore-button")).toBeVisible();
  await page.locator("#appstore-button").click();
  await expect(page.locator('[data-app="flask"]')).toContainText("Install");
  await expect(page.locator('[data-app="numpy"] .appstore__switch')).toHaveAttribute("aria-checked", "false");
  await expect(page.locator('[data-app="pillow"]')).toContainText("Failed");
  // search narrows the list
  await page.locator("#appstore-search").fill("num");
  await expect(page.locator(".appstore__app")).toHaveCount(1);
  // a name the store does not know is looked up on conda-forge and can be asked for
  await page.route("**/store/api/info?name=*", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ name: "ffmpeg", summary: "Convert audio and video.", version: "7.1" }) }));
  await page.locator("#appstore-search").fill("ffmpeg");
  await expect(page.locator('[data-app="ffmpeg"]')).toContainText("Convert audio and video");
  await expect(page.locator('[data-app="ffmpeg"] button')).toHaveText("Request ffmpeg");
  await page.locator("#appstore-search").fill("");
  // install an app on the safe list
  await page.locator('[data-app="flask"] button', { hasText: "Install" }).click();
  expect(calls.at(-1)).toEqual(["install", { name: "flask", kind: "lib" }]);
  await expect(page.locator('[data-app="flask"]')).toContainText("Installed");
  // switch it on, then off
  await page.locator('[data-app="flask"] .appstore__switch').click();
  expect(calls.at(-1)).toEqual(["switch", { id: "flask-1.0.0", on: true }]);
  await expect(page.locator('[data-app="flask"] .appstore__switch')).toHaveAttribute("aria-checked", "true");
  // my apps lists what is on, and offers to apply it to the open terminal
  await page.locator("#appstore-tab-mine").click();
  await expect(page.locator(".appstore__app")).toHaveCount(1);
  await expect(page.locator(".appstore__apply")).toContainText("Terminals that are open keep what they had");
  await page.evaluate(() => { window.__sent = []; window.sendToActiveTerminal = (t) => window.__sent.push(t); window.confirm = () => true; });
  await page.locator(".appstore__apply button").click();
  expect(await page.evaluate(() => window.__sent)).toEqual([". '/store/envs/me/env.sh'\r"]);
  // a failure is reported to the admin
  await page.locator("#appstore-tab-browse").click();
  await page.evaluate(() => { window.prompt = () => "I need it for images"; });
  await page.locator('[data-app="pillow"] button', { hasText: "Report to admin" }).click();
  expect(calls.at(-1)).toEqual(["report", { name: "pillow", note: "I need it for images" }]);
  await expect(page.locator('[data-app="pillow"]')).toContainText("You told the admin");
});

test("Genie and the terminal open the store on an app the user needs", async ({ page }) => {
  await ready(page);
  await page.route("**/store/api/list", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ views: [{ name: "flask", kind: "lib", state: "available", safe: true }], on: [], envScript: "/store/envs/me/env.sh" }) }));
  await page.evaluate(() => document.body.classList.add("is-signed-in"));
  await page.evaluate(() => window.AppStore.suggest("flask"));
  await expect(page.locator("#appstore-dialog")).toBeVisible();
  await expect(page.locator("#appstore-search")).toHaveValue("flask");
  await expect(page.locator('[data-app="flask"]')).toBeVisible();
});
