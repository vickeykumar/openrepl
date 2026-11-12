// The open files as tabs above the editor (src/js/src/page/22-editor-tabs.js, rules in lib-tabs.mjs): up to five, the
// oldest leaves at six, an open file is only shown, a tab keeps its unsaved text while another is shown, closing
// shows the neighbour, and the strip scrolls when there is no room. Designed in the "Editor file tabs" canvas.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const tabs = (page) => page.locator("#editor-tabs .etab");
const names = async (page) => (await tabs(page).locator(".etab__name").allTextContents()).map((s) => s.trim());

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(
    () =>
      window.EditorTabs &&
      window.editor &&
      window.editor.env &&
      window.editor.env.editor &&
      window.FileBrowser &&
      window.FileBrowser.ready()
  );
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

// a file in the guest's workspace, shown in the Files panel; clicking it opens it
async function makeFile(page, name) {
  const made = await page.evaluate(async (n) => {
    window.FileBrowser.reveal();
    return window.FileBrowser.create(n, "file");
  }, name);
  expect(made.ok, "the file was made: " + JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: name });
  await expect(anchor).toBeVisible();
  return anchor;
}

const stamp = Date.now().toString(36);
const fname = (i) => `tab${i}-${stamp}.txt`;

async function openFiles(page, count) {
  const anchors = [];
  for (let i = 1; i <= count; i++) anchors.push(await makeFile(page, fname(i)));
  for (let i = 0; i < count; i++) {
    await anchors[i].click();
    await expect(tabs(page).filter({ hasText: fname(i + 1) })).toHaveAttribute("aria-selected", "true");
  }
  return anchors;
}

test("a file chosen in the Files panel becomes a tab, and the same file is not a second tab", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 2);
  expect(await names(page)).toEqual([fname(1), fname(2)]);
  await anchors[0].click();
  await expect(tabs(page).filter({ hasText: fname(1) })).toHaveAttribute("aria-selected", "true");
  expect(await names(page)).toEqual([fname(1), fname(2)]);
});

test("the sixth file drops the oldest tab", async ({ page }) => {
  await ready(page);
  await openFiles(page, 6);
  await expect(tabs(page)).toHaveCount(5);
  const now = await names(page);
  expect(now).toEqual([2, 3, 4, 5, 6].map(fname));
});

test("a tab keeps its unsaved text while another file is shown", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 2);
  await anchors[0].click();
  await expect(tabs(page).filter({ hasText: fname(1) })).toHaveAttribute("aria-selected", "true");
  await page.evaluate(() => window.editor.env.editor.setValue("typed but not saved", -1));
  await anchors[1].click();
  await expect(tabs(page).filter({ hasText: fname(2) })).toHaveAttribute("aria-selected", "true");
  await expect(tabs(page).filter({ hasText: fname(1) }).locator(".etab__dot")).toBeVisible();
  await tabs(page).filter({ hasText: fname(1) }).click();
  await expect(tabs(page).filter({ hasText: fname(1) })).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => page.evaluate(() => window.editor.env.editor.getValue())).toBe("typed but not saved");
});

test("clicking a tab selects its file in the Files panel", async ({ page }) => {
  await ready(page);
  await openFiles(page, 2);
  await tabs(page).filter({ hasText: fname(1) }).click();
  await expect(page.locator("#file-browser .jstree-anchor.jstree-clicked", { hasText: fname(1) })).toBeVisible();
});

test("closing the shown tab shows its neighbour, and the file stays in the workspace", async ({ page }) => {
  await ready(page);
  await openFiles(page, 2);
  await tabs(page).filter({ hasText: fname(2) }).locator(".etab__x").click();
  await expect(tabs(page)).toHaveCount(1);
  await expect(tabs(page).first()).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#file-browser .jstree-anchor", { hasText: fname(2) })).toBeVisible();
});

test("the arrow keys move between tabs", async ({ page }) => {
  await ready(page);
  await openFiles(page, 3);
  await tabs(page).nth(2).focus();
  await page.keyboard.press("ArrowLeft");
  await expect(tabs(page).nth(1)).toHaveAttribute("aria-selected", "true");
});

test("with no room the strip scrolls and the buttons beside it stay", async ({ page }) => {
  await page.setViewportSize({ width: 820, height: 900 });
  await ready(page);
  await openFiles(page, 5);
  const lane = page.locator("#editor-tabs");
  await expect.poll(() => lane.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
  const tools = page.locator(".editor-header__tools");
  const box = await tools.boundingBox();
  expect(box.x + box.width).toBeLessThanOrEqual(820);
});

// The bug this guards: choosing files quickly let a late answer of the server land in the editor under the name of the
// file chosen last; closing that tab then saved the wrong text into the file.
test("choosing files quickly never puts one file's text in another, and closing saves nothing wrong", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 3);
  const paths = await page.evaluate(() => window.EditorTabs.state().tabs);
  expect(paths).toHaveLength(3);
  // give each file its own text, saved to the server
  for (let i = 0; i < 3; i++) {
    await anchors[i].click();
    await expect(tabs(page).filter({ hasText: fname(i + 1) })).toHaveAttribute("aria-selected", "true");
    await page.evaluate(async (text) => {
      window.editor.env.editor.setValue(text, -1);
      window.SaveSelectedNodeToFile(window.editor.env.filename);
    }, "text of file " + (i + 1));
    await expect(tabs(page).filter({ hasText: fname(i + 1) }).locator(".etab__dot")).toHaveCount(0);
  }
  // reload: the tabs come back, but their text has to be read from the server again
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await expect(tabs(page)).toHaveCount(3);
  // the first file's answer comes late, the second's at once
  await page.route(/q=load.*tab1-/, async (route) => {
    await new Promise((r) => setTimeout(r, 2500));
    try { await route.continue(); } catch (e) { /* the page gave the request up: fine */ }
  });
  await tabs(page).filter({ hasText: fname(1) }).click();
  await tabs(page).filter({ hasText: fname(2) }).click();
  await expect.poll(() => page.evaluate(() => window.editor.env.editor.getValue())).toBe("text of file 2");
  await page.waitForTimeout(3000); // the late answer has come (or was given up) by now
  expect(await page.evaluate(() => window.editor.env.editor.getValue())).toBe("text of file 2");
  expect(await page.evaluate(() => window.editor.env.filename)).toContain(fname(2));
  await tabs(page).filter({ hasText: fname(2) }).locator(".etab__x").click();
  await expect(tabs(page)).toHaveCount(2);
  // every file on the server still has its own text
  for (let i = 1; i <= 3; i++) {
    const got = await page.evaluate(async (path) => {
      return atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + path), method: "GET" }));
    }, paths[i - 1]);
    expect(got, "file " + i).toBe("text of file " + i);
  }
});

test("closing a tab that was only opened writes nothing to the server, whatever the file holds", async ({ page }) => {
  await ready(page);
  const anchor = await makeFile(page, fname(1));
  // odd line ends and a high character: the editor may write them differently from the file
  await anchor.click();
  await expect(tabs(page).filter({ hasText: fname(1) })).toHaveAttribute("aria-selected", "true");
  const file = await page.evaluate(() => window.EditorTabs.state().tabs[0]);
  await page.evaluate(
    (p) => $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + p), method: "POST", processData: false, data: btoa("a\r\nb\rc\ndé"), contentType: "application/octet-stream" }),
    file
  );
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  const saves = [];
  page.on("request", (r) => { if (/q=save/.test(r.url())) saves.push(r.url()); });
  await tabs(page).filter({ hasText: fname(1) }).click();
  await expect.poll(() => page.evaluate(() => window.editor.env.filename)).toContain(fname(1));
  await expect(tabs(page).filter({ hasText: fname(1) }).locator(".etab__dot")).toHaveCount(0);
  await tabs(page).filter({ hasText: fname(1) }).locator(".etab__x").click();
  await expect(tabs(page)).toHaveCount(0);
  expect(saves).toEqual([]);
});

test("a binary file is not opened in the shared editor: it opens read-only as a hex dump, and its tab does not stay", async ({ page }) => {
  await ready(page);
  const anchor = await makeFile(page, fname(1));
  await anchor.click();
  const file = await page.evaluate(() => window.EditorTabs.state().tabs[0]);
  await page.evaluate(
    (p) => $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + p), method: "POST", processData: false, data: btoa("ELF\u0000\u0001binary"), contentType: "application/octet-stream" }),
    file
  );
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await tabs(page).filter({ hasText: fname(1) }).click();
  const viewer = page.locator("#large-viewer");
  await expect(viewer).toBeVisible();
  await expect(viewer.locator(".lv__name")).toContainText("(hex)");
  await expect(viewer.locator(".lv__text")).toContainText("00000000  45 4c 46 00 01 62 69 6e");
  await expect(viewer.locator(".lv__text")).toContainText("|ELF..binary|");
  await expect(tabs(page)).toHaveCount(0);
  expect(await page.evaluate(() => window.editor.env.editor.getValue())).not.toContain("ELF");
});

// A text file over the editor's limit opens read-only in a viewer, in pieces. The server's answers are faked here (a
// real 17 MB file would only slow the test): the plain load says "too large", the pieces come from the offset.
async function fakeBigFile(page, size) {
  const seen = { saves: 0 };
  await page.route(/q=save/, (route) => { seen.saves++; route.continue(); });
  await page.route(/q=load.*tab1-/, (route) => {
    const q = new URL(route.request().url()).searchParams;
    if (q.get("offset") === null) {
      return route.fulfill({ status: 413, headers: { "X-File-Size": String(size) }, body: "file too large" });
    }
    const offset = Math.min(Number(q.get("offset")), size);
    // as long as the real server's piece: up to 1 MB, and shorter at the end of the file
    const text = `piece at ${offset}\n`.padEnd(Math.min(1048576, size - offset), " ");
    return route.fulfill({
      status: 200,
      headers: { "X-File-Size": String(size), "X-Chunk-Offset": String(offset), "X-Chunk-Length": String(text.length), "Content-Type": "application/octet-stream" },
      body: Buffer.from(text),
    });
  });
  return seen;
}

async function reopenFirstFile(page) {
  const anchor = await makeFile(page, fname(1));
  await anchor.click();
  await expect(tabs(page).filter({ hasText: fname(1) })).toHaveAttribute("aria-selected", "true");
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
}

test("text over the editor's limit opens read-only in pieces, and nothing is written or shared", async ({ page }) => {
  await ready(page);
  await reopenFirstFile(page);
  const seen = await fakeBigFile(page, 3000000);
  await tabs(page).filter({ hasText: fname(1) }).click();
  const viewer = page.locator("#large-viewer");
  await expect(viewer).toBeVisible();
  await expect(viewer.locator(".lv__text")).toContainText("piece at 0");
  await expect(viewer.locator(".lv__range")).toContainText("3,000,000");
  await expect(tabs(page)).toHaveCount(0); // no tab: it is not in the editor
  await viewer.getByRole("button", { name: "Next" }).click();
  await expect(viewer.locator(".lv__text")).toContainText("piece at 1048576");
  await viewer.getByRole("button", { name: "End" }).click();
  await expect(viewer.locator(".lv__text")).toContainText("piece at 1951424");
  await expect(viewer.getByRole("button", { name: "Next" })).toBeDisabled();
  await viewer.getByRole("button", { name: "Start" }).click();
  await expect(viewer.getByRole("button", { name: "Previous" })).toBeDisabled();
  expect(await page.evaluate(() => window.editor.env.editor.getValue())).not.toContain("piece at");
  await viewer.getByRole("button", { name: "Close" }).click();
  await expect(viewer).toBeHidden();
  expect(seen.saves).toBe(0);
});

test("the viewer's Terminal button types a command into the terminal and does not run it", async ({ page }) => {
  await ready(page);
  await reopenFirstFile(page);
  await fakeBigFile(page, 3000000);
  await tabs(page).filter({ hasText: fname(1) }).click();
  const viewer = page.locator("#large-viewer");
  await expect(viewer).toBeVisible();
  await viewer.getByRole("button", { name: "Terminal" }).click();
  await expect
    .poll(async () => (await page.locator("#terminal-div .xterm-rows").first().innerText()).replace(/ /g, " "))
    .toContain("more '");
  // a long path wraps at the width of the terminal: the screen text breaks it into lines
  const shown = (await page.locator("#terminal-div .xterm-rows").first().innerText()).replace(/ /g, " ").replace(/\n/g, "");
  expect(shown).toContain(fname(1));
});

test("bytes become the same text the editor has always held, for any byte and any length", async ({ page }) => {
  await ready(page);
  const ok = await page.evaluate(() => {
    const bytes = new Uint8Array(20000);
    for (let i = 0; i < bytes.length; i++) bytes[i] = i % 256;
    const text = window.bytesToBinaryString(bytes);
    if (text.length !== 20000) return "length " + text.length;
    for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) !== i % 256) return "byte " + i;
    return window.bytesToBinaryString(new Uint8Array([97, 233, 255, 0])) === "aéÿ\u0000" ? "ok" : "small";
  });
  expect(ok).toBe("ok");
});

test("the page is told the largest file the editor opens, from 1 to 16 MB", async ({ page }) => {
  await ready(page);
  const mb = await page.evaluate(() => window.site_settings && window.site_settings.maxEditorFileMB);
  expect(mb).toBeGreaterThanOrEqual(1);
  expect(mb).toBeLessThanOrEqual(16);
});

// The site gzips its responses. A file of many kilobytes has to arrive as itself: once it arrived as gzip bytes, which
// the page took for a binary file.
test("a text file of many kilobytes opens with its own text", async ({ page }) => {
  await ready(page);
  const anchor = await makeFile(page, fname(1));
  await anchor.click();
  const file = await page.evaluate(() => window.EditorTabs.state().tabs[0]);
  const text = Array.from({ length: 1500 }, (_, i) => `line ${i}: 2026-11-12 INFO request id=${i * 7} took=${i % 90}ms`).join("\n");
  await page.evaluate(
    ([p, t]) => $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + p), method: "POST", processData: false, data: btoa(t), contentType: "application/octet-stream" }),
    [file, text]
  );
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await tabs(page).filter({ hasText: fname(1) }).click();
  await expect.poll(() => page.evaluate(() => window.editor.env.editor.getValue()), { timeout: 15000 }).toBe(text);
  await expect(page.locator("#large-viewer")).toBeHidden();
});
