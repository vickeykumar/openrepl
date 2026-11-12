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
