// The App Store page (/store; resources/store.html, js/store.js). A browser test cannot sign in, so the signed-in side runs against a
// small stand-in for /store/api; the real server is covered by the Go tests.
const { test, expect } = require("@playwright/test");

function fake(page, opts = {}) {
  const st = {
    admin: !!opts.admin,
    apps: {
      flask: { name: "flask", kind: "lib", state: "available", safe: true },
      numpy: { name: "numpy", version: "2.1.0", id: "numpy-2.1.0", kind: "lib", state: "installed", safe: true, sizeMB: 96 },
      pillow: { name: "pillow", kind: "lib", state: "failed", safe: true, err: "Resolving the dependencies: not compatible", log: "Found conflicts!", node: "gateway" },
      ffmpeg: { name: "ffmpeg", kind: "tool", state: "requested", safe: false, mine: true, at: 1794600000 },
      jq: { name: "jq", kind: "tool", state: "installed", version: "1.7", id: "jq-1.7", safe: true, sizeMB: 1 },
      express: { name: "express", kind: "node", state: "available", safe: true },
      lua: { name: "lua", kind: "lang", state: "available", safe: false, summary: "The Lua language and its interpreter." },
      wordcount: { name: "wordcount", kind: "plugin", state: "available", safe: false, version: "1.0.0", summary: "Shows the words, lines and characters of the editor in the status bar." },
      shared: { name: "shared", kind: "lib", state: "installed", version: "1.0.0", id: "shared-1.0.0", safe: true, sizeMB: 5, othersOn: 2 },
    },
    on: ["jq-1.7"],
    searches: [],
    uninstalled: [],
    calls: [],
    polls: 0,
    notices: opts.notices || [],
  };
  const views = () => Object.values(st.apps).map((v) => ({ ...v, on: st.on.includes(v.id) }));
  page.route("**/store/api/**", async (route) => {
    const req = route.request();
    const what = new URL(req.url()).pathname.split("/store/api/")[1];
    const body = req.method() === "POST" ? req.postDataJSON() : {};
    if (req.method() === "POST") st.calls.push([what, body]);
    const json = (o, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(o) });
    if (what === "list") {
      if (st.apps.flask.state === "installing") {
        st.polls++;
        const idx = Math.min(3, st.polls);
        if (st.polls >= 4) Object.assign(st.apps.flask, { state: "installed", version: "3.0.2", id: "flask-3.0.2", sizeMB: 14, steps: undefined });
        else Object.assign(st.apps.flask, { pct: 10 + idx * 25, detail: "14 packages, 41 MB", steps: ["Checked that it is allowed", "Resolved the dependencies", "Downloading and linking", "Checking that it works"].map((t, i) => ({ text: t, state: i < idx ? "done" : i === idx ? "current" : "todo" })) });
      }
      return json({ views: views(), on: st.on, home: "me", envScript: "/store/envs/me/env.sh", admin: st.admin, python: "3.10", installer: "fake" });
    }
    if (what.startsWith("info")) return json({ name: "x", summary: "A summary of the package.", version: "9.9", versions: ["9.9", "9.8"] });
    if (what.startsWith("search")) {
      const q = new URL(req.url()).searchParams.get("q");
      st.searches.push(q);
      return json({ hits: [{ name: q + "-extra", version: "2.0", kind: "lib", summary: "Found on conda-forge.", state: "available", safe: false }, { name: q + "js", version: "1.1", kind: "node", summary: "Found on npm.", state: "available", safe: false }], error: "" });
    }
    if (what.startsWith("size")) return json({ version: "3.0.2", packages: 14, downloadMB: 41.2, diskMB: 98.7 });
    if (what === "uninstall") { st.uninstalled.push(body.name); delete st.apps[body.name]; }
    if (what === "notices") { const n = st.notices; st.notices = []; return json({ notices: n }); }
    if (what === "install") { st.apps[body.name].state = "installing"; st.polls = 0; }
    if (what === "switch") { const i = st.on.indexOf(body.id); if (body.on && i < 0) st.on.push(body.id); if (!body.on && i >= 0) st.on.splice(i, 1); }
    if (what === "report") Object.assign(st.apps[body.name], { reported: true, mine: true });
    if (what === "request") st.apps[body.name] = { name: body.name, kind: body.kind, state: "requested", safe: false, mine: true, at: 1794700000 };
    return json({ ok: true, views: views(), on: st.on });
  });
  page.route("**/admin/store", async (route) => {
    const req = route.request();
    if (req.method() === "POST") { st.calls.push(["admin:" + req.postDataJSON().action, req.postDataJSON()]); return route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) }); }
    const installed = Object.values(st.apps).filter((a) => a.state === "installed").map((a) => ({ ...a, state: "installed" }));
    return route.fulfill({ contentType: "application/json", body: JSON.stringify({
      enabled: true, apt: ["postgresql-client"], limitMB: 2048, now: 1794700000, users: { "shared-1.0.0": 3, "jq-1.7": 1 },
      apps: installed,
      requests: [{ name: "ffmpeg", kind: "tool", askers: ["a", "b"], reason: "convert my videos", status: "waiting", at: 1794600000 }],
      reports: [{ name: "pillow", version: "11.0", reporters: ["a"], node: "gateway", step: "Resolving the dependencies: conflict", note: "images", log: "Found conflicts!", status: "open" }],
      nodes: [
        { node: "gateway", have: ["jq-1.7", "shared-1.0.0", "numpy-2.1.0"], sizeMB: 300, sizes: { "jq-1.7": 1, "shared-1.0.0": 5, "numpy-2.1.0": 96 } },
        { node: "worker-1", have: ["jq-1.7"], sizeMB: 120, seen: "2026-11-14T10:00:00Z", sizes: { "jq-1.7": 1 }, fetching: { "numpy-2.1.0": 40 }, pending: ["jq-1.7"] },
      ],
    }) });
  });
  return st;
}

test("a guest sees the sign-in card on the page, and the server refuses its calls", async ({ page }) => {
  const res = await page.goto("/store");
  expect(res.status()).toBe(200);
  await expect(page.locator(".store__guest")).toContainText("signed-in users");
  await expect(page.locator("#store-side a").first()).toHaveText("Browse");
  const api = await page.evaluate(() => fetch("/store/api/list", { credentials: "same-origin" }).then((r) => r.status));
  expect([401, 503]).toContain(api);
});

test("the page has the menu, the chips and a grid of cards for every state; an app has a page of its own", async ({ page }) => {
  fake(page);
  await page.goto("/store");
  await expect(page.locator("#store-side")).toContainText("Python libraries");
  await expect(page.locator("#store-side")).toContainText("Node libraries");
  await expect(page.locator("#store-side")).toContainText("Languages (admin installs)");
  await expect(page.locator("#store-side")).toContainText("Editor plugins (samples)");
  await expect(page.locator("#store-side")).not.toContainText("apt packages"); // for admins only
  await expect(page.locator(".store__grid .sapp")).toHaveCount(9);
  await expect(page.locator('[data-app="flask"] button')).toHaveText("Install");
  await expect(page.locator('[data-app="numpy"] .sswitch')).toHaveAttribute("aria-checked", "false");
  await expect(page.locator('[data-app="jq"] .sswitch')).toHaveAttribute("aria-checked", "true");
  await expect(page.locator('[data-app="pillow"]')).toContainText("Failed");
  await expect(page.locator('[data-app="ffmpeg"]')).toContainText("Needs approval");
  await expect(page.locator('[data-app="flask"] .sapp__sum')).toContainText("A summary of the package.");
  // the chips
  await page.locator(".store__chip", { hasText: "Installed" }).click();
  await expect(page.locator(".store__grid .sapp")).toHaveCount(3);
  await page.locator(".store__chip", { hasText: "Needs approval" }).click();
  await expect(page.locator('.store__grid [data-app="ffmpeg"]')).toBeVisible();
  await page.locator(".store__chip", { hasText: /^All$/ }).click();
  // search: a name the store lists is found at once; the whole of conda-forge and npm is searched too, and what is found can be asked for
  await page.locator("#store-search").fill("zzz-new");
  await expect(page.locator('[data-app="zzz-new-extra"]')).toContainText("Found on conda-forge.");
  await expect(page.locator('[data-app="zzz-newjs"]')).toContainText("Found on npm.");
  await expect(page.locator('[data-app="zzz-newjs"] button')).toHaveText("Request");
  await page.locator("#store-search").fill("");
  // the page of an app: its three situations
  await page.locator('[data-app="flask"] .sapp__name').click();
  await expect(page.locator(".store__detail h2")).toHaveText("flask");
  await expect(page.locator(".store__kv")).toContainText("You, right away (it is on the safe list)");
  await expect(page.locator(".store__detail .store__code")).toContainText("import flask");
  await page.goto("/store#app/numpy");
  await expect(page.locator(".store__detail")).toContainText("Use it in my terminals");
  await expect(page.locator(".store__detail .store__code")).toContainText('python3 -c "import numpy"');
  await page.goto("/store#app/ffmpeg");
  await expect(page.locator(".store__detail")).toContainText("waiting for an admin");
  // kinds
  await page.goto("/store#kind/tool");
  await expect(page.locator(".store__grid .sapp")).toHaveCount(2);
  await page.goto("/store#kind/node");
  await expect(page.locator('.store__grid [data-app="express"] button')).toHaveText("Install");
  await page.goto("/store#kind/lang");
  await expect(page.locator(".store__list")).toContainText("an admin installs them");
  await expect(page.locator('.store__grid [data-app="lua"] button')).toHaveText("Request"); // a user asks; an admin installs
  await page.goto("/store#kind/plugin");
  await expect(page.locator(".store__list")).toContainText("sandboxed frame");
  await expect(page.locator('.store__grid [data-app="wordcount"]')).toContainText("status bar");
});

test("installing shows the steps and the progress, and ends with a switch; the switch and My apps work", async ({ page }) => {
  const st = fake(page);
  await page.goto("/store");
  await page.locator('[data-app="flask"] button', { hasText: "Install" }).click();
  expect(st.calls.at(-1)).toEqual(["install", { name: "flask", kind: "lib" }]);
  await expect(page.locator("#store-dialog")).toBeVisible();
  await expect(page.locator("#store-dialog h3")).toContainText("Installing flask");
  await expect(page.locator(".store-dialog__steps li.is-done").first()).toContainText("Checked that it is allowed");
  await expect(page.locator(".store-dialog__bar i")).toHaveAttribute("style", /width/);
  await expect(page.locator("#store-dialog h3")).toHaveText("flask is installed", { timeout: 15000 });
  await page.locator("#store-dialog button", { hasText: "Turn it on" }).click();
  expect(st.calls.at(-1)).toEqual(["switch", { id: "flask-3.0.2", on: true }]);
  await expect(page.locator('[data-app="flask"] .sswitch')).toHaveAttribute("aria-checked", "true");
  await page.goto("/store#mine");
  await expect(page.locator(".store__row")).toHaveCount(3); // flask and jq are on, ffmpeg was asked for
  await expect(page.locator(".store__note")).toContainText(". '/store/envs/me/env.sh'");
});

test("a failure is explained, can be reported with the dialog that says what is sent, or tried again", async ({ page }) => {
  const st = fake(page);
  await page.goto("/store");
  await page.locator('[data-app="pillow"] button', { hasText: "Details" }).click();
  await expect(page.locator("#store-dialog h3")).toHaveText("pillow could not be installed");
  await expect(page.locator("#store-dialog")).toContainText("Found conflicts!");
  await page.locator("#store-dialog button", { hasText: "Report to admin" }).click();
  await expect(page.locator("#store-dialog")).toContainText("Not sent: your files");
  await expect(page.locator("#store-dialog")).toContainText("gateway");
  await page.locator("#store-dialog textarea").fill("I need it for images");
  await page.locator("#store-dialog button", { hasText: "Send report" }).click();
  expect(st.calls.at(-1)).toEqual(["report", { name: "pillow", note: "I need it for images" }]);
  await expect(page.locator(".store__note")).toContainText("Sent. The admin will look at it");
});

test("a request asks for a reason; My requests lists what was asked; the answers are shown once", async ({ page }) => {
  const st = fake(page, { notices: [{ kind: "request", name: "numpy", status: "approved", text: "numpy is installed. Turn it on in the App Store to use it." }] });
  await page.goto("/store");
  // the answer to an earlier request
  await expect(page.locator("#store-dialog h3")).toHaveText("numpy is installed");
  await page.locator("#store-dialog button", { hasText: "Turn it on" }).click();
  expect(st.calls.some((c) => c[0] === "ack" && c[1].name === "numpy")).toBe(true);
  expect(st.calls.some((c) => c[0] === "switch" && c[1].id === "numpy-2.1.0" && c[1].on)).toBe(true);
  await expect(page.locator("#store-dialog")).toBeHidden();
  await page.locator("#store-search").fill("zzz-new");
  await page.locator('[data-app="zzz-new-extra"] button', { hasText: "Request" }).click();
  await expect(page.locator("#store-dialog h3")).toHaveText("Request zzz-new-extra");
  await page.locator("#store-dialog textarea").fill("for my project");
  await page.locator("#store-dialog button", { hasText: "Send request" }).click();
  expect(st.calls.at(-1)).toEqual(["request", { name: "zzz-new-extra", kind: "lib", reason: "for my project" }]);
  await page.goto("/store#requests");
  await expect(page.locator(".store__row")).toHaveCount(2); // ffmpeg and zzz-new (pillow failed but is not mine)
  await expect(page.locator('[data-app="ffmpeg"]')).toContainText("waiting for an admin");
});

test("an admin sees the apt packages and can install another version", async ({ page }) => {
  fake(page, { admin: true });
  await page.goto("/store#apt");
  await expect(page.locator("#store-side")).toContainText("apt packages");
  await expect(page.locator(".store__detail")).toContainText("postgresql-client");
  await page.goto("/store#app/pillow");
  await expect(page.locator(".store__pick")).toContainText("Admin: install another version");
  await expect(page.locator(".store__pick option")).toHaveCount(2);
});

test("the size is shown before an app is installed, and for a Node library, a language and a plugin the page says what each is", async ({ page }) => {
  fake(page);
  await page.goto("/store#app/flask");
  await expect(page.locator(".store__kv")).toContainText("download about 41 MB, about 99 MB once installed (14 packages, version 3.0.2)");
  await page.goto("/store#app/express");
  await expect(page.locator(".store__kv")).toContainText("Node.js (the server's), from npm");
  await expect(page.locator(".store__detail .store__code")).toContainText("require('express')");
  await page.goto("/store#app/lua");
  await expect(page.locator(".store__kv")).toContainText("Nothing else: it brings what it needs, from conda-forge");
  await expect(page.locator(".store__kv")).toContainText("An admin approves it first");
  await page.goto("/store#app/wordcount");
  await expect(page.locator(".store__kv")).toContainText("Nothing: it comes with the server");
  await expect(page.locator(".store__kv")).toContainText("a few KB");
});

test("a user may uninstall an app only when nobody else has it on; an admin always may", async ({ page }) => {
  const st = fake(page);
  await page.goto("/store#app/shared");
  await expect(page.locator(".store__uninstall button")).toBeDisabled();
  await expect(page.locator(".store__uninstall")).toContainText("2 other users have it on, so it stays");
  // numpy has nobody else: the user may remove it
  await page.goto("/store#app/numpy");
  await expect(page.locator(".store__uninstall button")).toBeEnabled();
  await page.locator(".store__uninstall button").click();
  await expect(page.locator("#store-dialog h3")).toHaveText("Uninstall numpy?");
  await expect(page.locator("#store-dialog")).toContainText("nobody else has it on");
  await page.locator("#store-dialog button", { hasText: "Uninstall" }).click();
  expect(st.uninstalled).toEqual(["numpy"]);
  await expect(page.locator('.store__grid [data-app="numpy"]')).toHaveCount(0);
});

test("an admin uninstalls an app that others use, from the app's page", async ({ page }) => {
  const st = fake(page, { admin: true });
  await page.goto("/store#app/shared");
  await expect(page.locator(".store__uninstall button")).toHaveText("Uninstall for everyone");
  await expect(page.locator(".store__uninstall button")).toBeEnabled();
  await page.locator(".store__uninstall button").click();
  await page.locator("#store-dialog button", { hasText: "Uninstall" }).click();
  await expect.poll(() => st.calls.some((c) => c[0] === "admin:remove" && c[1].name === "shared")).toBe(true);
});

test("the admin has sections in the store: requests to approve, failures, the servers and what they fetch", async ({ page }) => {
  const st = fake(page, { admin: true });
  await page.goto("/store#admin/requests");
  await expect(page.locator("#store-side")).toContainText("Requests");
  await expect(page.locator('[data-request="ffmpeg"]')).toContainText("2 users");
  await expect(page.locator('[data-request="ffmpeg"]')).toContainText("convert my videos");
  await page.locator('[data-request="ffmpeg"] button', { hasText: "Approve and install" }).click();
  await expect.poll(() => st.calls.some((c) => c[0] === "admin:approve" && c[1].name === "ffmpeg")).toBe(true);
  await page.locator('[data-request="ffmpeg"] button', { hasText: "Reject" }).click();
  await page.locator("#store-dialog textarea").fill("too big");
  await page.locator("#store-dialog button", { hasText: "Reject" }).click();
  await expect.poll(() => st.calls.some((c) => c[0] === "admin:reject" && c[1].answer === "too big")).toBe(true);
  // failures
  await page.goto("/store#admin/failures");
  await expect(page.locator('[data-report="pillow"]')).toContainText("on gateway");
  await expect(page.locator(".store__code")).toContainText("Found conflicts!");
  await page.locator('[data-report="pillow"] button', { hasText: "Try again" }).click();
  await expect.poll(() => st.calls.some((c) => c[0] === "admin:retry" && c[1].name === "pillow")).toBe(true);
  // servers: what each has, a fetch in percent, a command waiting, and a reinstall
  await page.goto("/store#admin/servers");
  await expect(page.locator('[data-node="gateway"]')).toContainText("300 MB of 2048 MB");
  await expect(page.locator('[data-node="worker-1"] [data-bundle="numpy-2.1.0"]')).toContainText("fetching 40%");
  await expect(page.locator('[data-node="worker-1"]')).toContainText("Waiting for worker-1 to ask: jq-1.7");
  await page.locator('[data-node="worker-1"] [data-bundle="jq-1.7"] button', { hasText: "Reinstall here" }).click();
  await expect.poll(() => st.calls.some((c) => c[0] === "admin:reinstall" && c[1].node === "worker-1" && c[1].id === "jq-1.7")).toBe(true);
});

test("a plugin that is on runs in a sandboxed frame and shows its line in the editor's status bar", async ({ page }) => {
  await page.route("**/store/api/plugins", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ plugins: [{ id: "wordcount-1.0.0", name: "wordcount", code: "function openreplPlugin(api){ api.every(1000, function(){ api.text().then(function(t){ api.status((t.trim().match(/\\S+/g)||[]).length + ' words'); }); }); }" }] }) }));
  await page.goto("/");
  await page.waitForFunction(() => window.AppStorePlugins && window.editor && window.editor.env && window.editor.env.editor);
  await page.evaluate(() => { document.body.classList.add("is-signed-in"); window.editor.env.editor.setValue("one two three four", -1); return window.AppStorePlugins.refresh(); });
  await expect(page.locator(".appstore__plugin")).toHaveText("4 words", { timeout: 10000 });
  // the frame has no origin of its own: it cannot reach this page
  const sandbox = await page.evaluate(() => document.querySelector("iframe[title='Plugin wordcount']").getAttribute("sandbox"));
  expect(sandbox).toBe("allow-scripts");
  const reach = await page.evaluate(() => { try { return typeof document.querySelector("iframe[title='Plugin wordcount']").contentWindow.document; } catch (e) { return "blocked"; } });
  expect(reach).toBe("blocked");
});

test("an admin's store page loads when the server has nothing yet (empty lists, even null ones)", async ({ page }) => {
  fake(page, { admin: true });
  await page.route("**/admin/store", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ enabled: true, apt: null, apps: null, requests: null, reports: null, nodes: null, users: null, limitMB: 2048 }) }));
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/store#admin/requests");
  await expect(page.locator("#store-side")).toContainText("Servers and installs");
  await expect(page.locator(".store__rows")).toContainText("Nobody is waiting for an answer.");
  await page.goto("/store#admin/failures");
  await expect(page.locator(".store__rows")).toContainText("No failures are waiting for you.");
  await page.goto("/store#admin/servers");
  await expect(page.locator(".store__rows")).toContainText("Servers and installs");
  expect(errors).toEqual([]);
});

// Text must be readable in both themes: the colour of the subtitle, a card's name and a heading against the colour behind them.
for (const scheme of ["light", "dark"]) {
  test(`the store page is readable in the ${scheme} theme`, async ({ page }) => {
    fake(page, { admin: true });
    await page.emulateMedia({ colorScheme: scheme });
    await page.addInitScript((s) => { try { localStorage.setItem("theme", s); } catch (e) {} }, scheme);
    await page.goto("/store");
    await page.evaluate((s) => document.documentElement.setAttribute("data-theme", s), scheme);
    await expect(page.locator(".sapp").first()).toBeVisible();
    const ratios = await page.evaluate(() => {
      const parse = (c) => c.match(/[\d.]+/g).map(Number);
      const lum = ([r, g, b]) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); };
      const bgOf = (el) => { for (; el; el = el.parentElement) { const c = parse(getComputedStyle(el).backgroundColor); if (c.length < 4 || c[3] > 0.5) return c; } return [255, 255, 255]; };
      const ratio = (sel) => { const el = document.querySelector(sel); const a = lum(parse(getComputedStyle(el).color)), b = lum(bgOf(el)); return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05); };
      return { sub: ratio(".store__head p"), h1: ratio(".store__head h1"), name: ratio(".sapp__name"), sum: ratio(".sapp__sum"), side: ratio(".store__side a") };
    });
    for (const [k, v] of Object.entries(ratios)) expect(v, `${k} in ${scheme}`).toBeGreaterThan(4.5);
    await page.screenshot({ path: `test-results/store-${scheme}.png` });
  });
}

test("the admin's servers list loads when a worker has only reported a fetch (no list of what it has)", async ({ page }) => {
  fake(page, { admin: true });
  await page.route("**/admin/store", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ enabled: true, apt: [], apps: [{ id: "jq-1.7", name: "jq", version: "1.7", kind: "tool" }], requests: [], reports: [], users: {}, limitMB: 2048, nodes: [{ node: "w9", have: null, failed: null, fetching: { "jq-1.7": 30 } }] }) }));
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/store#admin/servers");
  await expect(page.locator(".store__node").first()).toContainText("w9");
  expect(errors).toEqual([]);
});
