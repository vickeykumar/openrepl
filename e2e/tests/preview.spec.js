// The Preview pane (page/27-preview.js, server/preview.go, package livepreview): a web page of the workspace shown next
// to the editor in a sandboxed frame, with its sibling files.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const dir = `site${stamp}`;

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.EditorTabs);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function makeSite(page) {
  const files = {
    "index.html": '<!doctype html><html><head><link rel="stylesheet" href="style.css"></head><body><h1 id="t">Hello preview</h1><a id="more" href="more/page.html">more</a><script src="app.js"></script></body></html>',
    "style.css": "h1{color:rgb(10,125,51)}",
    "app.js": "document.getElementById('t').textContent='Hello from app.js';",
    "more/page.html": "<!doctype html><p id='p2'>second page</p>",
  };
  await page.evaluate(async ([d, files]) => {
    await window.FileBrowser.create(d, "folder");
    await window.FileBrowser.create(d + "/more", "folder");
    for (const name of Object.keys(files)) await window.FileBrowser.create(d + "/" + name, "file");
    for (const [name, text] of Object.entries(files)) {
      await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/" + name), method: "POST", processData: false, data: btoa(text), contentType: "application/octet-stream" });
    }
  }, [dir, files]);
}

async function openIndex(page) {
  const opened = await page.evaluate((d) => window.FileBrowser.open(d + "/index.html"), dir);
  expect(opened.ok, JSON.stringify(opened)).toBe(true);
  await expect.poll(() => page.evaluate(() => window.editor.env.filename)).toContain("index.html");
}

test("the Preview button shows for an .html file, opens the pane in the terminal's place, and the page has its CSS and script", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await expect(page.locator("#preview-button")).toBeHidden(); // no .html file is open yet
  await openIndex(page);
  await expect(page.locator("#preview-button")).toBeVisible();
  await page.locator("#preview-button").click();
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.locator("#terminal")).toBeHidden();
  await expect(page.locator("#preview-address-text")).toHaveText(`${dir}/index.html`);
  const frame = page.frameLocator("#preview-frame");
  await expect(frame.locator("#t")).toHaveText("Hello from app.js"); // the script ran
  await expect(frame.locator("#t")).toHaveCSS("color", "rgb(10, 125, 51)"); // the stylesheet applied
  // a link inside the page works, relative to its folder
  await frame.locator("#more").click();
  await expect(frame.locator("#p2")).toHaveText("second page");
  // closing brings the terminal back
  await page.locator("#preview-close").click();
  await expect(page.locator("#preview-pane")).toBeHidden();
  await expect(page.locator("#terminal")).toBeVisible();
});

test("the frame is sandboxed: no origin of its own, no access to this page", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openIndex(page);
  await page.locator("#preview-button").click();
  const sandbox = await page.locator("#preview-frame").getAttribute("sandbox");
  expect(sandbox).not.toContain("allow-same-origin");
  expect(sandbox).toContain("allow-scripts");
  // the served page says so too, in its header, so that it is sandboxed in a tab of its own as well
  const src = await page.locator("#preview-frame").getAttribute("src");
  const head = await page.evaluate(async (u) => (await fetch(u)).headers.get("content-security-policy"), src);
  expect(head).toMatch(/^sandbox /);
  expect(head).not.toContain("allow-same-origin");
  // nothing outside the workspace, and a wrong token is "ended"
  const token = src.split("/")[2];
  expect(await page.evaluate(async (t) => (await fetch(`/preview/${t}/f/..%2F..%2Fetc%2Fpasswd`)).status, token)).not.toBe(200);
  expect(await page.evaluate(async () => (await fetch("/preview/wrong.token/f/x")).status)).toBe(410);
});

test("saving a file of the page reloads it", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openIndex(page);
  await page.locator("#preview-button").click();
  const frame = page.frameLocator("#preview-frame");
  await expect(frame.locator("#t")).toHaveText("Hello from app.js");
  await page.evaluate(() => {
    window.editor.env.editor.setValue('<!doctype html><h1 id="t">Changed in the editor</h1>', -1);
    window.SaveSelectedNodeToFile(window.editor.env.filename);
  });
  await expect(frame.locator("#t")).toHaveText("Changed in the editor");
});

test("Run on an .html file opens the preview, and the size menu sizes the page", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openIndex(page);
  await page.evaluate(() => window.CompileandRun());
  await expect(page.locator("#preview-pane")).toBeVisible();
  await page.locator("#preview-size").selectOption("phone");
  const w = await page.locator("#preview-device").evaluate((el) => el.style.width);
  expect(w).toBe("390px");
});

test("the Open preview entry is in the right-click menu of an .html file", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await page.evaluate((d) => { const t = $("#file-browser").jstree(true); t.open_node(t.get_node("#").children[0] + "/" + d); }, dir);
  const row = page.locator("#file-browser .jstree-anchor", { hasText: "index.html" }).first();
  await expect(row).toBeVisible();
  await row.click({ button: "right" });
  await page.locator(".vakata-context a", { hasText: "Open preview" }).click();
  await expect(page.locator("#preview-pane")).toBeVisible();
});

// A program of the workspace that listens on a port (phase 2): the server connects to it for the page. The program is a
// plain `python3 -m http.server`, started in a bash terminal. Needs a server where each workspace has its own network
// (the CI image and the dev container have it).
// Where the machine's network is shared there is nothing to preview a program through (see livepreview: it refuses), so
// the tests that need a program say so and stop.
async function needsPrivateNetwork(page) {
  const isolated = await page.evaluate(() => fetch("/preview_ports").then((r) => r.json()).then((j) => j.isolated));
  test.skip(!isolated, "this server has no private network for each workspace, so a program cannot be previewed");
}

async function startServer(page, port) {
  await page.evaluate(() => window.pickLanguage("bash"));
  await expect(page.locator("#terminal-div .xterm-rows").first()).toContainText("#", { timeout: 60 * 1000 });
  await page.evaluate(
    ([d, p]) => window.sendToActiveTerminal(`mkdir -p ${d} && echo '<h1 id="x">served by python</h1><a id="abs" href="/other.html">other</a>' > ${d}/index.html && echo 'other page' > ${d}/other.html && python3 -m http.server ${p} --directory ${d} >/dev/null 2>&1 &\r`),
    [`${await page.evaluate(() => window.homedir)}/${dir}-srv`, port]
  );
}

test("a program that listens on a port is previewed through the server, with its links and its port list", async ({ page }) => {
  await ready(page);
  await needsPrivateNetwork(page);
  const port = 5055;
  await startServer(page, port);
  await expect.poll(() => page.evaluate(() => fetch("/preview_ports").then((r) => r.json()).then((j) => j.ports)), { timeout: 60 * 1000 }).toContain(port);
  // the page asks every 20 seconds; here it is asked at once
  await page.evaluate(() => window.Preview.refresh());
  // the button shows a dot, and the pane lists the port
  await expect(page.locator("#preview-button")).toBeVisible();
  await expect(page.locator("#preview-dot")).toBeVisible();
  await page.locator("#preview-button").click();
  await expect(page.locator("#preview-pane")).toBeVisible();
  const frame = page.frameLocator("#preview-frame");
  await expect(frame.locator("#x")).toHaveText("served by python");
  await expect(page.locator("#preview-address-text")).toHaveText(`127.0.0.1:${port}`);
  // an absolute link of the program (href="/other") stays in the preview
  await frame.locator("#abs").click();
  await expect(frame.locator("body")).toContainText("other page");
  // the port list
  await page.locator("#preview-ports-button").click();
  await expect(page.locator("#preview-ports-list button", { hasText: `127.0.0.1:${port}` })).toBeVisible();
});

test("a port with nothing behind it says so in the page, and a port below 1024 is refused", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => window.Preview.openPort(5999));
  await expect(page.frameLocator("#preview-frame").locator("body")).toContainText(/Nothing is (listening on port 5999|running yet)|could not be reached|cannot be previewed|network isolation/);
  // below 1024: refused before any connection
  const token = await page.evaluate(() => fetch("/preview_token").then((r) => r.json()).then((j) => j.token));
  expect(await page.evaluate(async (t) => (await fetch(`/preview/${t}/p/22/`)).status, token)).toBe(400);
});

test("a program that starts listening is offered in a notice, and Open preview shows it", async ({ page }) => {
  await ready(page);
  await needsPrivateNetwork(page);
  await startServer(page, 5066);
  await expect.poll(() => page.evaluate(() => fetch("/preview_ports").then((r) => r.json()).then((j) => j.ports)), { timeout: 60 * 1000 }).toContain(5066);
  await page.evaluate(() => window.Preview.refresh());
  const notice = page.locator(".notice", { hasText: "listening on port 5066" });
  await expect(notice).toBeVisible();
  await notice.getByRole("button", { name: "Open preview" }).click();
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.frameLocator("#preview-frame").locator("#x")).toHaveText("served by python");
  // the same port is not offered again
  await page.locator("#preview-close").click();
  await page.evaluate(() => window.Preview.refresh());
  await expect(page.locator(".notice", { hasText: "listening on port 5066" })).toHaveCount(0);
});

test("a program that starts listening is offered within seconds by itself, and the notice stays until it is answered", async ({ page }) => {
  await ready(page);
  await needsPrivateNetwork(page);
  await startServer(page, 5067);
  // nothing asks for it: the page looks every 8 seconds (the program here prints nothing, so the terminal hint does not help)
  const notice = page.locator(".notice", { hasText: "listening on port 5067" });
  await expect(notice).toBeVisible({ timeout: 15 * 1000 });
  await page.waitForTimeout(6000);
  await expect(notice).toBeVisible();
});

test("the pane lists how to start a program, and Type it puts the command in the terminal", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => window.Preview.open()); // nothing to show yet, so the list of ports is what it opens with
  await expect(page.locator("#preview-ports")).toBeVisible();
  await page.locator("#preview-ports summary").click();
  await expect(page.locator("#preview-howto .preview-howto__row")).toHaveCount(6);
  await page.locator("#preview-howto .preview-howto__row", { hasText: "Python" }).getByRole("button", { name: /Type/ }).click();
  await expect(page.locator("#preview-pane")).toBeHidden(); // the terminal is back
  await expect.poll(async () => (await page.locator("#terminal-div .xterm-rows").first().innerText()).replace(/ /g, " ")).toContain("python3 -m http.server 8000");
});

test("with nothing to show the button is hidden, and the command menu opens the preview with its list of ports", async ({ page }) => {
  await ready(page);
  await expect(page.locator("#preview-button")).toBeHidden();
  await page.keyboard.press("ControlOrMeta+Shift+P");
  await expect(page.locator("#palette")).toBeVisible();
  await page.locator("#palette-input").fill("preview");
  await page.locator("#palette .palette__item", { hasText: "Open the preview" }).click();
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.locator("#preview-ports")).toBeVisible();
});

// ---- the tools of the pane: history, console, requests, tabs, address bar, menu, the Run drawer -----------------------

async function makePages(page, pages) {
  const d = `pg${Date.now().toString(36)}`;
  await page.evaluate(async ([d, pages]) => {
    await window.FileBrowser.create(d, "folder");
    for (const n of Object.keys(pages)) await window.FileBrowser.create(d + "/" + n, "file");
    for (const [n, t] of Object.entries(pages)) {
      await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/" + n), method: "POST", processData: false, data: btoa(t), contentType: "application/octet-stream" });
    }
  }, [d, pages]);
  return (name) => page.evaluate(([d, name]) => window.homedir + "/" + d + "/" + name, [d, name]);
}

test("Back and Forward follow the links of the page, and the address bar follows the page", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "one.html": '<h1>One</h1><a id="go" href="two.html">two</a>', "two.html": "<h1 id='h'>Two</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("one.html"));
  await expect(page.locator("#preview-address-text")).toContainText("one.html");
  await expect(page.locator("#preview-back")).toBeDisabled();
  await page.frameLocator("#preview-frame").locator("#go").click();
  await expect(page.locator("#preview-address-text")).toContainText("two.html");
  await expect(page.locator("#preview-back")).toBeEnabled();
  await page.locator("#preview-back").click();
  await expect(page.locator("#preview-address-text")).toContainText("one.html");
  await expect(page.locator("#preview-forward")).toBeEnabled();
  await page.locator("#preview-forward").click();
  await expect(page.locator("#preview-address-text")).toContainText("two.html");
  await expect(page.locator("#preview-status-text")).toContainText("Live");
});

test("the Console shows what the page writes and its errors, and Requests the calls it makes", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "c.html": '<h1>c</h1><script>console.log("hello console");console.warn("careful");fetch("c.html");setTimeout(function(){ null.x }, 30)</script>' });
  await page.evaluate((f) => window.Preview.openPath(f), await at("c.html"));
  await expect(page.locator("#preview-console-count")).toHaveText("1");
  await page.locator("#preview-console-button").click();
  await expect(page.locator("#preview-console-body")).toContainText("hello console");
  await expect(page.locator("#preview-console-body")).toContainText("careful");
  await page.locator("#preview-tab-requests").click();
  await expect(page.locator("#preview-console-body")).toContainText("200");
  await page.locator("#preview-console-clear").click();
  await expect(page.locator("#preview-console-body")).toContainText("No calls");
});

test("two files are two tabs, and a typed address opens a port", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "a.html": "<h1>a</h1>", "b.html": "<h1>b</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("a.html"));
  await page.evaluate((f) => window.Preview.openPath(f), await at("b.html"));
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toHaveCount(2);
  await page.locator("#preview-tabs .preview-tab[role=tab]").first().click();
  await expect(page.locator("#preview-address-text")).toContainText("a.html");
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("5999/x");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-address-text")).toContainText("127.0.0.1:5999");
});

test("the menu keeps its switches, and Run keeps the pane with the terminal as a drawer under it", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "r.html": "<h1>r</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("r.html"));
  await page.locator("#preview-menu-button").click();
  await expect(page.locator("#preview-opt-auto")).toBeChecked();
  await page.locator("#preview-opt-auto").uncheck();
  await page.keyboard.press("Escape");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("preview-opt-auto"))).toBe("0");
  await page.locator("#preview-menu-button").click();
  await page.locator("#preview-opt-auto").check();
  await page.keyboard.press("Escape");
  await page.evaluate(() => window.Preview.onRun());
  await expect(page.locator("#terminal-div.with-drawer")).toHaveCount(1);
  await expect(page.locator("#preview-pane")).toBeVisible();
  await page.locator("#hide-terminal-button").click();
  await expect(page.locator("#terminal-div.with-drawer")).toHaveCount(0);
  await expect(page.locator("#preview-pane")).toBeVisible();
});

// ---- more of the pane: keys, hint, the 500 banner, tokens of viewers, phone, the notice -----------------------------------

test("Ctrl+Alt+V opens and closes the pane, F6 moves the focus, Ctrl+L goes to the address bar", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "f6.html": "<h1>f6</h1>" });
  await page.locator("#terminal-div .xterm-helper-textarea").first().focus(); // the terminal has the focus, as it does in a real visit
  await page.keyboard.press("Control+Alt+V");
  await expect(page.locator("#preview-pane")).toBeVisible();
  await page.evaluate((f) => window.Preview.openPath(f), await at("f6.html")); // a page with the pane's script, so that keys inside it reach the pane
  await expect(page.frameLocator("#preview-frame").locator("h1")).toHaveText("f6");
  await page.locator("#preview-reload").focus();
  await page.keyboard.press("Control+L");
  await expect(page.locator("#preview-address-input")).toBeVisible();
  await expect(page.locator("#preview-address-input")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.locator("#preview-address-input")).toBeHidden();
  await page.locator("#preview-reload").focus();
  await page.keyboard.press("F6");
  await expect.poll(() => page.evaluate(() => document.activeElement && document.activeElement.id)).toBe("preview-frame");
  await page.keyboard.press("F6");
  await expect.poll(() => page.evaluate(() => { const e = window["editor"]; return !!(e && e.env && e.env.editor && e.env.editor.isFocused()); })).toBe(true);
  await page.keyboard.press("Control+Alt+V");
  await expect(page.locator("#preview-pane")).toBeHidden();
});

test("keys pressed inside the previewed page reach the editor: Esc twice goes back to it, Ctrl+Alt+V closes the pane", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "k.html": "<h1>keys</h1><input id='i'>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("k.html"));
  const frame = page.frameLocator("#preview-frame");
  await frame.locator("#i").click();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect.poll(() => page.evaluate(() => { const e = window["editor"]; return !!(e && e.env && e.env.editor && e.env.editor.isFocused()); })).toBe(true);
  await frame.locator("#i").click();
  await page.keyboard.press("Control+Alt+V");
  await expect(page.locator("#preview-pane")).toBeHidden();
});

test("the first time the button shows there is a hint, once", async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.removeItem("preview-hint"); } catch (e) {} });
  await ready(page);
  await makeSite(page);
  await openIndex(page);
  const hint = page.locator(".notice", { hasText: "See your web page here" });
  await expect(hint).toBeVisible();
  await expect(hint.getByRole("button", { name: "Show me" })).toBeVisible();
  await hint.getByRole("button", { name: "Got it" }).click();
  await expect(hint).toBeHidden();
  expect(await page.evaluate(() => localStorage.getItem("preview-hint"))).toBe("1");
});

test("a program that answers with a server error gets a banner, with Show in terminal and Show page anyway", async ({ page }) => {
  await ready(page);
  await page.route("**/preview/*/p/5077/**", (route) => route.fulfill({ status: 500, contentType: "text/html", body: "<h1>Internal Server Error</h1><p>KeyError: 'name'</p>" }));
  await page.evaluate(() => window.Preview.openPort(5077));
  const banner = page.locator("#preview-error");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("500");
  await expect(banner).toContainText("KeyError: 'name'");
  await banner.getByRole("button", { name: "Show in terminal" }).click();
  await expect(page.locator("#terminal-div.with-drawer")).toHaveCount(1);
  await expect(page.locator("#preview-pane")).toBeVisible();
  await banner.getByRole("button", { name: "Show page anyway" }).click();
  await expect(banner).toBeHidden();
});

test("each pane's token names its viewer, and the cookies of a preview can be cleared", async ({ page }) => {
  await ready(page);
  const claims = async (v) => page.evaluate(async (v) => {
    const r = await fetch("/preview_token?v=" + v).then((x) => x.json());
    return JSON.parse(atob(r.token.split(".")[0].replace(/-/g, "+").replace(/_/g, "/")));
  }, v);
  const a = await claims("viewerAAA"), b = await claims("viewerBBB");
  expect(a.v).toBe("viewerAAA");
  expect(b.v).toBe("viewerBBB");
  expect(a.h).toBe(b.h); // the same workspace
  const bad = await claims("../../etc");
  expect(bad.v).toBe("etc"); // letters and digits only
  const status = await page.evaluate(() => fetch("/preview_cookies?port=5000", { method: "POST" }).then((r) => r.status));
  expect(status).toBe(204);
  const get = await page.evaluate(() => fetch("/preview_cookies").then((r) => r.status));
  expect(get).toBe(405);
});

test("on a phone the open pane is a third tab, and the Terminal tab closes it", async ({ browser }) => {
  const ctx = await browser.newContext({ viewport: { width: 390, height: 800 }, isMobile: true, hasTouch: true });
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.EditorTabs);
  await expect(page.locator("#mobile-view-preview")).toBeHidden();
  await page.evaluate(() => window.Preview.open());
  await expect(page.locator("#mobile-view-preview")).toBeVisible();
  await expect(page.locator("#mobile-view-preview")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#preview-pane")).toBeVisible();
  await page.locator("#mobile-view-terminal").click();
  await expect(page.locator("#preview-pane")).toBeHidden();
  await expect(page.locator("#mobile-view-preview")).toBeHidden();
  await ctx.close();
});

test("a notice with several buttons keeps its text readable: the buttons go in a row of their own", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => window.notify("Open it in the Preview pane beside your editor.", { type: "info", variant: "program", title: "Your program is listening on port 8000", timeout: 0, actions: [{ label: "Open preview", primary: true, onClick() {} }, { label: "Not now", onClick() {} }, { label: "Don't ask again", quiet: true, onClick() {} }] }));
  const notice = page.locator(".notice--program");
  await expect(notice.locator(".notice__actions .notice__action")).toHaveCount(3);
  const width = await notice.locator(".notice__text").evaluate((e) => e.getBoundingClientRect().width);
  expect(width).toBeGreaterThan(200);
  await expect(notice.locator(".notice__action--primary")).toHaveCSS("background-color", "rgb(200, 240, 74)");
});

test("the Genie agent's preview actions open and read the pane", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "g.html": "<h1>g</h1><script>console.error('bad thing')</script>" });
  const out = await page.evaluate(async (f) => {
    const ok = window.Preview.openPath(f);
    await new Promise((r) => setTimeout(r, 1500));
    const s = window.Preview.state();
    return { ok, open: s.open, address: s.address, errors: s.errors, last: s.console.map((c) => c.text) };
  }, await at("g.html"));
  expect(out.ok).toBe(true);
  expect(out.open).toBe(true);
  expect(out.address).toContain("g.html");
  expect(out.errors).toBe(1);
  expect(out.last.join(" ")).toContain("bad thing");
  expect(await page.evaluate(() => window.Preview.openPath("x/notes.txt"))).toBe(false); // not a page that can be shown
});

test("a file name typed in the address bar opens that page, beside the one shown; a file that is no page says so", async ({ page }) => {
  await ready(page);
  const d = `ty${Date.now().toString(36)}`;
  await page.evaluate(async (d) => {
    await window.FileBrowser.create(d, "folder");
    for (const n of ["one.html", "two.html", "style.css"]) await window.FileBrowser.create(d + "/" + n, "file");
    const put = (n, t) => $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/" + n), method: "POST", processData: false, data: btoa(t), contentType: "application/octet-stream" });
    await put("one.html", "<h1>one</h1>"); await put("two.html", "<h1 id='h'>two</h1>"); await put("style.css", "h1{color:red}");
  }, d);
  await page.evaluate((d) => window.Preview.openPath(window.homedir + "/" + d + "/one.html"), d);
  await page.locator("#preview-address").click();
  await expect(page.locator("#preview-address-input")).toBeFocused();
  await page.locator("#preview-address-input").fill("two.html");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-address-text")).toContainText("two.html");
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("two");
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("style.css");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-message")).toContainText("style.css cannot be shown in the Preview");
});

test("localStorage, sessionStorage and cookies work in the previewed page, and survive a reload", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, {
    "store.html": `<p id="out"></p><script>
      var n = (parseInt(localStorage.getItem("n"), 10) || 0) + 1;
      localStorage.setItem("n", n);
      sessionStorage.setItem("s", "tab");
      var loads = (document.cookie.match(/loads=(\\d+)/) || [0, 0])[1] * 1 + 1;
      document.cookie = "loads=" + loads + "; max-age=1000";
      localStorage.theme = "dark";
      document.getElementById("out").textContent = [n, sessionStorage.getItem("s"), loads, localStorage.length, localStorage.theme, Object.keys(localStorage).join(",")].join("|");
    </script>`,
  });
  await page.evaluate((f) => window.Preview.openPath(f), await at("store.html"));
  const out = page.frameLocator("#preview-frame").locator("#out");
  await expect(out).toHaveText("1|tab|1|2|dark|n,theme");
  await page.waitForTimeout(600); // the writes go back to the server in the background
  await page.evaluate(() => window.Preview.reload());
  await expect(out).toHaveText("2|tab|2|2|dark|n,theme");
  // storage is not shown to the pane's own page
  expect(await page.evaluate(() => localStorage.getItem("n"))).toBeNull();
});

test("with one address there is a + for another, and Reload brings the page back after a typed address could not be shown", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "back.html": "<h1 id='h'>still here</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("back.html"));
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("still here");
  await expect(page.locator("#preview-tabs")).toBeVisible();
  await expect(page.locator("#preview-tabs .preview-tab--new")).toBeVisible();
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("back.html23242");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-message")).toContainText("back.html23242 cannot be shown");
  await page.locator("#preview-reload").click();
  await expect(page.locator("#preview-message")).toBeHidden();
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("still here");
});

test("a file name that does not exist says so in the pane, and the tab keeps its page", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "here.html": "<h1 id='h'>here</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("here.html"));
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("here");
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("nothere.html");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-message")).toContainText("nothere.html was not found in your workspace");
  await expect(page.locator("#preview-address-text")).toContainText("here.html");
});

test("there is no sandbox bar above the page: the status line below says it", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "n.html": "<h1>n</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("n.html"));
  await expect(page.locator("#preview-note")).toHaveCount(0);
  await expect(page.locator("#preview-status-text")).toContainText("sandboxed page");
});

test("an address typed in the bar loads in the same tab, and Back returns to the page before", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "one.html": "<h1 id='h'>one</h1>", "two.html": "<h1 id='h'>two</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("one.html"));
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("one");
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("5999/x");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-address-text")).toContainText("127.0.0.1:5999");
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toHaveCount(1);
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toContainText(":5999");
  await page.locator("#preview-back").click();
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("one");
  await expect(page.locator("#preview-address-text")).toContainText("one.html");
  // another site is refused and the tab keeps its page
  await page.locator("#preview-address").click();
  await page.locator("#preview-address-input").fill("https://example.com");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-message")).toContainText("Only your workspace");
});

test("+ opens an empty tab with suggestions; a name typed there loads in that tab", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "alpha.html": "<h1 id='h'>alpha</h1>", "beta.html": "<h1 id='h'>beta</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("alpha.html"));
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("alpha");
  await page.locator("#preview-tabs .preview-tab--new").click();
  await expect(page.locator("#preview-newpage")).toBeVisible();
  await expect(page.locator("#preview-address-input")).toBeFocused();
  await expect(page.locator("#preview-status-text")).toContainText("New tab");
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toHaveCount(2);
  await page.locator("#preview-address-input").fill("beta");
  await expect(page.locator("#preview-newpage-list .preview-newpage__item")).toContainText(["beta.html"]);
  // the arrow key picks the first suggestion, Enter opens it in this same tab
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.locator("#preview-newpage")).toBeHidden();
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("beta");
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toHaveCount(2);
  // a + while an empty tab exists reuses it: there are never two
  await page.locator("#preview-tabs .preview-tab--new").click();
  await page.locator("#preview-tabs .preview-tab--new").click();
  await expect(page.locator("#preview-tabs .preview-tab[role=tab]")).toHaveCount(3);
});

test("the pane can be docked over the editor, with the terminal usable beside it, and back", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "d.html": "<h1 id='h'>docked</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("d.html"));
  await expect(page.locator("#terminal-div > #preview-pane")).toHaveCount(1);
  await page.locator("#preview-menu-button").click();
  await page.locator("#preview-menu-dock").click();
  await expect(page.locator("#ide > #preview-pane")).toHaveCount(1);
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("docked");
  // the terminal is shown, not covered
  await expect(page.locator("#terminal-div .terminal.active").first()).toBeVisible();
  await expect(page.locator("#terminal-div")).not.toHaveClass(/is-previewing/);
  await page.locator("#preview-menu-button").click();
  await expect(page.locator("#preview-menu-dock")).toHaveText("Dock over the terminal");
  await page.locator("#preview-menu-dock").click();
  await expect(page.locator("#terminal-div > #preview-pane")).toHaveCount(1);
  await expect(page.locator("#terminal-div")).toHaveClass(/is-previewing/);
});

test("dragging the grip onto the editor docks the pane there, and onto the terminal brings it back", async ({ page }) => {
  await ready(page);
  const at = await makePages(page, { "g.html": "<h1>g</h1>" });
  await page.evaluate((f) => window.Preview.openPath(f), await at("g.html"));
  const grip = await page.locator("#preview-grip").boundingBox();
  const ed = await page.locator("#ide").boundingBox();
  const tm = await page.locator("#terminal-div").boundingBox();
  const drag = async (from, to) => {
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move((from.x + to.x) / 2, (from.y + to.y) / 2, { steps: 4 });
    await page.mouse.move(to.x, to.y, { steps: 4 });
    await expect(page.locator(".preview-drop")).toBeVisible();
    await page.mouse.up();
  };
  await drag({ x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 }, { x: ed.x + ed.width / 2, y: ed.y + ed.height / 2 });
  await expect(page.locator("#ide > #preview-pane")).toHaveCount(1);
  const grip2 = await page.locator("#preview-grip").boundingBox();
  await drag({ x: grip2.x + grip2.width / 2, y: grip2.y + grip2.height / 2 }, { x: tm.x + tm.width / 2, y: tm.y + tm.height / 2 });
  await expect(page.locator("#terminal-div > #preview-pane")).toHaveCount(1);
  await expect(page.locator(".preview-drop")).toBeHidden();
});
