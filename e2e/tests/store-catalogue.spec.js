// The curated catalogue on the App Store page (/store; js/store.js): built-in apps inside My apps, the filters, the browser libraries and
// plugins with their switches, documentation links, and the request dialog. The signed-in side runs against a stand-in for /store/api; the
// real server is covered by the Go tests (a test for every /store/api route).
const { test, expect } = require("@playwright/test");

function fake(page) {
  const st = { calls: [], pp: [
    { id: "plugin:viewport", name: "Viewport switcher", ours: true, on: true, ready: true },
    { id: "plugin:outline", name: "Outline", ours: true, on: true, ready: true },
    { id: "plugin:console", name: "Console", ours: true, on: true, ready: true },
    { id: "plugin:eruda", name: "Eruda console", ours: false, on: false, ready: true },
    { id: "plugin:vconsole", name: "vConsole", ours: false, on: false, ready: false },
  ] };
  const views = [
    { name: "numpy", kind: "lib", state: "installed", version: "2.1.0", id: "numpy-2.1.0", safe: true, sizeMB: 96, on: true, repls: ["python", "ipython3"], category: "Data science", level: "core", docs: "https://numpy.org/doc/stable/", licence: "BSD-3-Clause" },
    { name: "polars", kind: "lib", state: "available", safe: false, repls: ["python"], category: "Data science", level: "optional", instMB: 221, docs: "https://docs.pola.rs/" },
    { name: "pytorch-cpu", kind: "lib", state: "available", safe: false, repls: ["python"], category: "Data science", level: "optional", instMB: 747, ramMB: 226 },
    { name: "cmake", kind: "tool", state: "available", safe: true, repls: ["c", "cpp"], category: "Build tools", level: "popular", docs: "https://cmake.org/cmake/help/latest/" },
  ];
  const entries = [
    { id: "web:bootstrap", name: "bootstrap", kind: "weblib", repls: ["html", "css", "javascript"], level: "core", state: "available", docs: "https://getbootstrap.com/docs/5.3/", summary: "Ready-made styles and components.", installed: false },
    { id: "plugin:viewport", name: "Viewport switcher", kind: "plugin", place: "preview", level: "core", internal: true, docs: "/docs/plugins/viewport-switcher", summary: "Phone, tablet and desktop widths." },
    { id: "plugin:outline", name: "Outline", kind: "plugin", place: "preview", level: "core", internal: true, docs: "/docs/plugins/outline", summary: "Headings of the page." },
    { id: "plugin:console", name: "Console", kind: "plugin", place: "preview", level: "core", internal: true, docs: "/docs/plugins/console", summary: "Console messages." },
    { id: "plugin:eruda", name: "Eruda console", kind: "plugin", place: "preview", level: "popular", publisher: "liriliri", docs: "https://eruda.liriliri.io/docs/", summary: "Developer tools in the preview page." },
    { id: "plugin:vconsole", name: "vConsole", kind: "plugin", place: "preview", level: "popular", publisher: "Tencent", docs: "https://github.com/Tencent/vConsole#readme", summary: "A small console." },
  ];
  page.route("**/store/api/**", async (route) => {
    const req = route.request();
    const what = new URL(req.url()).pathname.split("/store/api/")[1];
    const body = req.method() === "POST" ? req.postDataJSON() : {};
    if (req.method() === "POST") st.calls.push([what, body]);
    const json = (o, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(o) });
    if (what === "list") return json({ views, on: ["numpy-2.1.0"], home: "me", admin: false, python: "3.10", sandbox: true, requestsPerDay: 5, requestsToday: 2,
      builtins: [{ name: "gcc", version: "11.4", used: ["c", "cpp"], docs: "https://gcc.gnu.org/onlinedocs/" }, { name: "git", version: "2.34", used: ["bash"], docs: "https://git-scm.com/doc" }] });
    if (what === "notices") return json({ notices: [] });
    if (what.startsWith("info")) return json({ name: "x", summary: "", version: "1" });
    if (what.startsWith("catalogue")) return json({ entries: entries.filter((e) => e.kind === "weblib" || e.place === "preview"), repls: [], categories: [], levels: [] });
    if (what === "preview-plugins") return json({ plugins: st.pp });
    if (what === "plugin-switch") { const p = st.pp.find((x) => x.id === body.id); p.on = body.on; return json({ ok: true }); }
    if (what === "request") return json({ ok: true, views, on: ["numpy-2.1.0"] });
    return json({ ok: true, views, on: ["numpy-2.1.0"] });
  });
  return st;
}

test("My apps lists the built-in apps, tagged, always on, with nothing to install or remove", async ({ page }) => {
  fake(page);
  await page.goto("/store#mine");
  await expect(page.locator("#store-side a", { hasText: "My apps" })).toContainText("+ 2 built in");
  const gcc = page.locator('[data-builtin="gcc"]');
  await expect(gcc).toContainText("gcc 11.4");
  await expect(gcc).toContainText("Built in");
  await expect(gcc).toContainText("used by c, cpp");
  await expect(gcc.locator(".sswitch")).toBeDisabled();
  await expect(gcc.locator(".sswitch")).toHaveAttribute("aria-checked", "true");
  await expect(gcc.getByRole("button", { name: "Uninstall" })).toBeDisabled();
  await expect(gcc.getByRole("link", { name: "Documentation" })).toHaveAttribute("href", "https://gcc.gnu.org/onlinedocs/");
  await expect(page.locator('[data-app="numpy"]')).toBeVisible(); // the apps of the user are there as before
});

test("the filters narrow the store by language, category and level; the page shows size and memory", async ({ page }) => {
  fake(page);
  await page.goto("/store");
  await expect(page.locator(".store__grid .sapp")).toHaveCount(4);
  await page.locator('select[data-filter="repl"]').selectOption("cpp");
  await expect(page.locator(".store__grid .sapp")).toHaveCount(1);
  await expect(page.locator('[data-app="cmake"]')).toBeVisible();
  await page.locator('select[data-filter="repl"]').selectOption("");
  await page.locator('select[data-filter="category"]').selectOption("Data science");
  await expect(page.locator(".store__grid .sapp")).toHaveCount(3);
  await page.locator('select[data-filter="level"]').selectOption("optional");
  await expect(page.locator(".store__grid .sapp")).toHaveCount(2);
  await expect(page.locator('[data-app="pytorch-cpu"]')).toContainText("226 MB when used");
  await expect(page.locator('[data-app="pytorch-cpu"]')).toContainText("747 MB");
  await expect(page.locator('[data-app="polars"]')).toContainText("221 MB");
});

test("an app's page has a link to its documentation, its licence and what it works with", async ({ page }) => {
  fake(page);
  await page.goto("/store#app/numpy");
  const link = page.locator(".store__kv a");
  await expect(link).toHaveAttribute("href", "https://numpy.org/doc/stable/");
  await expect(link).toHaveAttribute("rel", /noopener/);
  await expect(page.locator(".store__kv")).toContainText("BSD-3-Clause");
  await expect(page.locator(".store__kv")).toContainText("python, ipython3");
});

test("browser libraries and the plugins of the preview have their section; a plugin has a switch of its own", async ({ page }) => {
  const st = fake(page);
  await page.goto("/store#kind/browser");
  await expect(page.locator('[data-app="web:bootstrap"]')).toContainText("browser library");
  await expect(page.locator('[data-app="plugin:viewport"]')).toContainText("ours");
  await expect(page.locator('[data-app="plugin:eruda"]')).toContainText("third party");
  await expect(page.locator('[data-app="plugin:viewport"] .sswitch')).toHaveAttribute("aria-checked", "true");
  // our plugins link to a page of this site, the third party ones to theirs
  await expect(page.locator('[data-app="plugin:outline"] a.sbtn')).toHaveAttribute("href", "/docs/plugins/outline");
  await expect(page.locator('[data-app="plugin:eruda"] a.sbtn')).toHaveAttribute("href", "https://eruda.liriliri.io/docs/");
  await expect(page.locator('[data-app="plugin:vconsole"]')).toContainText("An admin installs it first");
  await page.locator('[data-app="plugin:eruda"] .sswitch').click();
  await expect(page.locator('[data-app="plugin:eruda"] .sswitch')).toHaveAttribute("aria-checked", "true");
  expect(st.calls).toContainEqual(["plugin-switch", { id: "plugin:eruda", on: true }]);
  await page.locator('[data-app="plugin:outline"] .sswitch').click();
  await expect(page.locator('[data-app="plugin:outline"] .sswitch')).toHaveAttribute("aria-checked", "false");
  expect(st.calls).toContainEqual(["plugin-switch", { id: "plugin:outline", on: false }]);
});

test("the request dialog asks for the language and says how many requests are left today", async ({ page }) => {
  const st = fake(page);
  await page.goto("/store#app/polars");
  await page.getByRole("button", { name: "Request polars" }).click();
  await expect(page.locator("#store-dialog")).toContainText("You can send 5 requests a day (2 used)");
  await expect(page.locator('#store-dialog select[data-request-repl]')).toHaveValue("python");
  await page.locator("#store-dialog textarea").fill("fast tables");
  await page.locator("#store-dialog").getByRole("button", { name: "Send request" }).click();
  await expect.poll(() => st.calls.find((c) => c[0] === "request")).toBeTruthy();
  expect(st.calls.find((c) => c[0] === "request")[1]).toMatchObject({ name: "polars", repl: "python", reason: "fast tables" });
});
