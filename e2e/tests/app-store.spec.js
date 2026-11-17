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

test("a guest sees the App Store button, which says to sign in, and the server refuses", async ({ page }) => {
  await ready(page);
  await expect(page.locator("#appstore-button")).toBeVisible();
  expect(await page.evaluate(() => window.AppStore.available())).toBe(false);
  const status = await page.evaluate(() => fetch("/store/api/list", { credentials: "same-origin" }).then((r) => r.status));
  expect([401, 503]).toContain(status);
  const post = await page.evaluate(() => fetch("/store/api/install", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: "flask" }) }).then((r) => r.status));
  expect([401, 503]).toContain(post);
  await page.locator("#appstore-button").click();
  await expect(page.locator("#appstore-dialog")).toBeVisible();
  await expect(page.locator(".appstore__guest")).toContainText("Sign in to use the App Store");
  await expect(page.locator(".appstore__guest button")).toHaveText("Sign in");
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
  // every app says its kind, its name opens its page in the store, and the link to the store says where it goes
  await expect(page.locator('[data-app="flask"] .appstore__tag').first()).toHaveText("python");
  const link = page.locator('[data-app="numpy"] a.appstore__name');
  await expect(link).toHaveAttribute("href", "./store#app/numpy");
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(page.locator(".appstore__full")).toHaveText("Go to app store page");
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

// The quick dialog is painted by the editor theme like the other overlays of the IDE: a light editor theme gives a light dialog.
for (const [theme, light] of [["ace/theme/github", true], ["ace/theme/monokai", false]]) {
  test(`the quick App Store follows the editor theme (${theme.split("/").pop()})`, async ({ page }) => {
    await page.addInitScript((t) => { try { localStorage.setItem("editor-theme", t); localStorage.removeItem("ide-palette"); } catch (e) {} }, theme);
    await ready(page);
    await page.waitForFunction(() => document.documentElement.hasAttribute("data-ide-theme"), null, { timeout: 15000 });
    await page.route("**/store/api/**", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, views: [{ name: "flask", kind: "lib", state: "installed", version: "3.0", id: "flask-3.0", sizeMB: 9, safe: true }, { name: "jq", kind: "tool", state: "failed", err: "no", safe: true }], on: [], envScript: "/store/envs/me/env.sh", installer: "fake" }) }));
    await page.evaluate(() => document.body.classList.add("is-signed-in"));
    await page.locator("#appstore-button").click();
    await expect(page.locator('[data-app="flask"]')).toBeVisible();
    const lum = await page.locator(".appstore__card").evaluate((e) => {
      const c = getComputedStyle(e).backgroundColor.match(/[\d.]+/g).map(Number);
      return (0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]) / 255;
    });
    if (light) expect(lum).toBeGreaterThan(0.6); else expect(lum).toBeLessThan(0.3);
    // the text is readable on it: the name and the tags
    const ratio = await page.evaluate(() => {
      const p = (c) => c.match(/[\d.]+/g).map(Number);
      const L = ([r, g, b]) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); };
      const bg = L(p(getComputedStyle(document.querySelector(".appstore__app")).backgroundColor));
      const out = {};
      for (const sel of [".appstore__name", ".appstore__ver", ".appstore__tag"]) { const f = L(p(getComputedStyle(document.querySelector('[data-app="flask"] ' + sel)).color)); out[sel] = (Math.max(f, bg) + 0.05) / (Math.min(f, bg) + 0.05); }
      return out;
    });
    for (const [k, v] of Object.entries(ratio)) expect(v, k).toBeGreaterThan(3.5);
  });
}

// Turning an app on is checked first: the answer of the server says whether it worked for this user; when it did not, it is off, the
// check's own words are shown, and the user can report it to the admin.
test("switching an app on is checked first, and a failure is shown and can be reported", async ({ page }) => {
  await ready(page);
  const calls = [];
  let fail = true;
  const views = (on) => [{ name: "numpy", kind: "lib", state: "installed", version: "1.0.0", id: "numpy-1.0.0", sizeMB: 9, safe: true, on }];
  await page.route("**/store/api/**", async (route) => {
    const req = route.request();
    const what = new URL(req.url()).pathname.split("/store/api/")[1];
    if (req.method() === "POST") calls.push([what, req.postDataJSON()]);
    if (what === "switch" && fail) {
      await new Promise((r) => setTimeout(r, 300)); // the check takes a moment
      return route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "verify", message: "numpy did not work for your terminals, so it is off.", name: "numpy", log: "python3 -c import numpy: exit status 1\nImportError: libopenblas.so.0", node: "worker-1", views: views(false), on: [] }) });
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, views: views(what === "switch"), on: what === "switch" ? ["numpy-1.0.0"] : [], envScript: "/store/envs/me/env.sh", installer: "fake" }) });
  });
  await page.evaluate(() => { document.body.classList.add("is-signed-in"); window.prompt = () => "it does not start"; });
  await page.locator("#appstore-button").click();
  const sw = page.locator('[data-app="numpy"] .appstore__switch');
  await sw.click();
  await expect(sw).toHaveText("Checking…");
  await expect(page.locator("#appstore-note")).toContainText("did not work for your terminals");
  await expect(page.locator('[data-app="numpy"] .appstore__log')).toContainText("libopenblas.so.0");
  await expect(sw).toHaveAttribute("aria-checked", "false");
  await page.locator('[data-app="numpy"] button', { hasText: "Report to admin" }).click();
  await expect.poll(() => calls.some((c) => c[0] === "report" && c[1].name === "numpy" && c[1].note === "it does not start")).toBe(true);
  // when the check passes it is on, and it says it was checked
  fail = false;
  await sw.click();
  await expect(sw).toHaveAttribute("aria-checked", "true");
  await expect(page.locator("#appstore-note")).toContainText("checked and works");
});

test("the quick dialog lists the built-in apps in My apps, tagged, always on, with nothing to install or remove, and a Docs link on the cards", async ({ page }) => {
  await ready(page);
  await page.route("**/store/api/**", async (route) => {
    const what = new URL(route.request().url()).pathname.split("/store/api/")[1];
    const views = [{ name: "numpy", version: "2.1", id: "numpy-2.1", kind: "lib", state: "installed", safe: true, on: true, docs: "https://numpy.org/doc/stable/" }, { name: "polars", kind: "lib", state: "available", safe: false, docs: "https://docs.pola.rs/", summary: "Fast tables." }];
    const builtins = [{ name: "Go", version: "go1.18", used: ["go", "yaegi"], docs: "https://go.dev/doc/" }, { name: "Perl", version: "5.34", used: ["perli"], docs: "https://perldoc.perl.org/" }];
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, views, on: ["numpy-2.1"], builtins, envScript: "/store/envs/me/env.sh", installer: "fake", sandbox: true }) });
  });
  await page.evaluate(() => document.body.classList.add("is-signed-in"));
  await page.locator("#appstore-button").click();
  await expect(page.locator('[data-app="polars"] a', { hasText: "Docs" })).toHaveAttribute("href", "https://docs.pola.rs/");
  await expect(page.locator('[data-app="polars"]')).toContainText("Fast tables.");
  await page.locator("#appstore-tab-mine").click();
  const go = page.locator('[data-builtin="Go"]');
  await expect(go).toContainText("Built in");
  await expect(go).toContainText("go1.18");
  await expect(go).toContainText("Used by go, yaegi");
  await expect(go.locator(".appstore__switch")).toBeDisabled();
  await expect(go.locator(".appstore__switch")).toHaveAttribute("aria-checked", "true");
  await expect(go.getByRole("button", { name: "Uninstall" })).toBeDisabled();
  await expect(go.getByRole("link", { name: "Docs" })).toHaveAttribute("href", "https://go.dev/doc/");
  await expect(page.locator('[data-builtin="Perl"]')).toBeVisible();
  await expect(page.locator('[data-app="numpy"]')).toBeVisible();
});

test("the Apps button is on the terminal's header; the floating Genie button is not drawn while maximized", async ({ page }) => {
  await ready(page);
  await expect(page.locator("#terminal-tabs #appstore-button")).toBeVisible();
  await expect(page.locator(".genie-fab")).toBeVisible();
  await page.evaluate(() => window.ToggleFunction());
  await expect(page.locator("body")).toHaveClass(/ide-maximized/);
  await expect(page.locator(".genie-fab")).toBeHidden();
  await page.evaluate(() => window.ToggleFunction()); // restore (the Escape key needs the focus in the page)
  await expect(page.locator("body")).not.toHaveClass(/ide-maximized/);
  await expect(page.locator(".genie-fab")).toBeVisible();
});

test("Details of an install that runs show the clean lines and how far the other servers are", async ({ page }) => {
  await ready(page);
  let asked = 0;
  await page.route("**/store/api/**", async (route) => {
    const what = new URL(route.request().url()).pathname.split("/store/api/")[1];
    let body = { ok: true, views: [{ name: "cobra", kind: "gomod", state: "installing", step: "Downloading and linking", safe: true }], on: [], installer: "fake" };
    if (what === "log") {
      asked++;
      body = { lines: [{ t: 1700000000000, x: "go: fetched github.com/spf13/pflag v1.0.5" }], next: 10, running: true, servers: { total: 3, ready: 2, failed: 0 } };
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await page.evaluate(() => document.body.classList.add("is-signed-in"));
  await page.locator("#appstore-button").click();
  const card = page.locator('[data-app="cobra"]');
  await expect(card).toContainText("Downloading and linking");
  expect(asked).toBe(0); // closed: nothing is fetched
  await card.getByRole("button", { name: "Show details" }).click();
  await expect(card.locator(".appstore__details")).toContainText("fetched github.com/spf13/pflag v1.0.5");
  await expect(card.locator(".appstore__details")).toContainText("Copying to the other servers: 2 of 3 ready");
  await card.getByRole("button", { name: "Hide details" }).click();
  await expect(card.locator(".appstore__details")).toHaveCount(0);
});
