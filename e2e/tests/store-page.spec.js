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
    },
    on: ["jq-1.7"],
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
    if (what === "notices") { const n = st.notices; st.notices = []; return json({ notices: n }); }
    if (what === "install") { st.apps[body.name].state = "installing"; st.polls = 0; }
    if (what === "switch") { const i = st.on.indexOf(body.id); if (body.on && i < 0) st.on.push(body.id); if (!body.on && i >= 0) st.on.splice(i, 1); }
    if (what === "report") Object.assign(st.apps[body.name], { reported: true, mine: true });
    if (what === "request") st.apps[body.name] = { name: body.name, kind: body.kind, state: "requested", safe: false, mine: true, at: 1794700000 };
    return json({ ok: true, views: views(), on: st.on });
  });
  page.route("**/admin/store", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ apt: ["postgresql-client"], apps: [], requests: [], reports: [] }) }));
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
  await expect(page.locator("#store-side")).toContainText("Languages (admin)");
  await expect(page.locator("#store-side")).toContainText("Editor plugins (later)");
  await expect(page.locator("#store-side")).not.toContainText("apt packages"); // for admins only
  await expect(page.locator(".store__grid .sapp")).toHaveCount(5);
  await expect(page.locator('[data-app="flask"] button')).toHaveText("Install");
  await expect(page.locator('[data-app="numpy"] .sswitch')).toHaveAttribute("aria-checked", "false");
  await expect(page.locator('[data-app="jq"] .sswitch')).toHaveAttribute("aria-checked", "true");
  await expect(page.locator('[data-app="pillow"]')).toContainText("Failed");
  await expect(page.locator('[data-app="ffmpeg"]')).toContainText("Needs approval");
  await expect(page.locator('[data-app="flask"] .sapp__sum')).toContainText("A summary of the package.");
  // the chips
  await page.locator(".store__chip", { hasText: "Installed" }).click();
  await expect(page.locator(".store__grid .sapp")).toHaveCount(2);
  await page.locator(".store__chip", { hasText: "Needs approval" }).click();
  await expect(page.locator('.store__grid [data-app="ffmpeg"]')).toBeVisible();
  await page.locator(".store__chip", { hasText: /^All$/ }).click();
  // search, and a name the store does not know can be looked up and asked for
  await page.locator("#store-search").fill("zzz-new");
  await expect(page.locator('[data-app="zzz-new"]')).toContainText("A summary of the package.");
  await expect(page.locator('[data-app="zzz-new"] button')).toHaveText("Request");
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
  await page.goto("/store#kind/languages");
  await expect(page.locator(".store__detail")).toContainText("belongs in the server's image");
  await page.goto("/store#kind/plugins");
  await expect(page.locator(".store__detail")).toContainText("security design");
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
  await page.locator('[data-app="zzz-new"] button', { hasText: "Request" }).click();
  await expect(page.locator("#store-dialog h3")).toHaveText("Request zzz-new");
  await page.locator("#store-dialog textarea").fill("for my project");
  await page.locator("#store-dialog button", { hasText: "Send request" }).click();
  expect(st.calls.at(-1)).toEqual(["request", { name: "zzz-new", kind: "lib", reason: "for my project" }]);
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
