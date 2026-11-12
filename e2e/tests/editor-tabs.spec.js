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
