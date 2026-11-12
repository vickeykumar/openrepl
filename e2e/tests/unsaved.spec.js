// Unsaved changes (page/28-unsaved.js, designed in the "Unsaved changes prompts" canvas): the browser's question on reload
// and close, the bar with Save all, the question when a changed tab is closed, Save all from the keyboard, and what comes
// back after a reload.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const fname = (i) => `un${i}-${stamp}.txt`;
const tabs = (page) => page.locator("#editor-tabs .etab");
const tabOf = (page, i) => tabs(page).filter({ hasText: fname(i) });
const dialog = (page) => page.locator(".unsaved-dialog");
const bar = (page) => page.locator("#unsaved-bar");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready());
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function makeFile(page, name) {
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), name);
  expect(made.ok, JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: name });
  await expect(anchor).toBeVisible();
  return anchor;
}

// n files, each opened once (so each is a tab); the anchors, in order
async function openFiles(page, n) {
  const anchors = [];
  for (let i = 1; i <= n; i++) anchors.push(await makeFile(page, fname(i)));
  for (let i = 0; i < n; i++) {
    await anchors[i].click();
    await expect(tabOf(page, i + 1)).toHaveAttribute("aria-selected", "true");
  }
  return anchors;
}

const type = (page, text) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), text);
const pathOf = (page, i) => page.evaluate((n) => window.EditorTabs.state().tabs.find((p) => p.endsWith("/" + n)), fname(i));
const onServer = (page, path) => page.evaluate(async (p) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + p), method: "GET" })), path);

test("closing a tab that has changes asks, and Cancel, Don't save and Save each do what they say", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 2);
  const path = await pathOf(page, 2);
  await type(page, "changed text");
  await expect(tabOf(page, 2).locator(".etab__dot")).toBeVisible();
  // Cancel: the tab stays, with its text
  await tabOf(page, 2).locator(".etab__x").click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("h2")).toHaveText(`Save the changes to ${fname(2)}?`);
  await page.keyboard.press("Escape");
  await expect(dialog(page)).toBeHidden();
  await expect(tabs(page)).toHaveCount(2);
  expect(await page.evaluate(() => window.editor.env.editor.getValue())).toBe("changed text");
  // Don't save: the tab closes, and the file is as it was on the server
  await tabOf(page, 2).locator(".etab__x").click();
  await dialog(page).getByRole("button", { name: "Don't save" }).click();
  await expect(tabs(page)).toHaveCount(1);
  expect(await onServer(page, path)).toBe("");
  // Save: written, and the tab closes
  await anchors[1].click();
  await expect(tabOf(page, 2)).toHaveAttribute("aria-selected", "true");
  await type(page, "saved text");
  await tabOf(page, 2).locator(".etab__x").click();
  await dialog(page).getByRole("button", { name: "Save", exact: true }).click();
  await expect(tabs(page)).toHaveCount(1);
  await expect.poll(() => onServer(page, path)).toBe("saved text");
});

test("a tab with no changes closes without a question", async ({ page }) => {
  await ready(page);
  await openFiles(page, 2);
  await tabOf(page, 2).locator(".etab__x").click();
  await expect(tabs(page)).toHaveCount(1);
  await expect(dialog(page)).toBeHidden();
});

test("a sixth file drops the oldest tab without changes, and asks only when every tab has changes", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 5);
  // the first two tabs have changes: the third, the oldest without, is the one that goes
  for (const i of [1, 2]) {
    await anchors[i - 1].click();
    await expect(tabOf(page, i)).toHaveAttribute("aria-selected", "true");
    await type(page, "changed " + i);
  }
  const sixth = await makeFile(page, fname(6));
  await sixth.click();
  await expect(tabOf(page, 6)).toHaveAttribute("aria-selected", "true");
  await expect(dialog(page)).toBeHidden();
  await expect(tabs(page)).toHaveCount(5);
  await expect(tabOf(page, 3)).toHaveCount(0);
  await expect(tabOf(page, 1)).toHaveCount(1);
  // now every tab has changes: the next file asks about the oldest
  for (const i of [4, 5, 6]) {
    await tabOf(page, i).click();
    await expect(tabOf(page, i)).toHaveAttribute("aria-selected", "true");
    await type(page, "changed " + i);
  }
  const seventh = await makeFile(page, fname(7));
  await seventh.click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("h2")).toHaveText(`Save the changes to ${fname(1)}?`);
  await dialog(page).getByRole("button", { name: "Don't save" }).click();
  await expect(tabOf(page, 1)).toHaveCount(0);
});

test("the browser asks before a reload while a tab has changes, and Cancel leaves a bar with Save all", async ({ page }) => {
  await ready(page);
  await openFiles(page, 2);
  const kinds = [];
  page.on("dialog", (d) => { kinds.push(d.type()); d.dismiss(); });
  // nothing changed: no question
  await page.evaluate(() => { window.__stay = true; });
  await page.evaluate(() => window.dispatchEvent(new Event("beforeunload", { cancelable: true })));
  expect(kinds).toEqual([]);
  await type(page, "changed once");
  const p1 = await pathOf(page, 2);
  // a reload asks (the browser's own question); Cancel stays on the page
  page.evaluate(() => location.reload()).catch(() => {});
  await expect.poll(() => kinds.length).toBe(1);
  expect(kinds[0]).toBe("beforeunload");
  await expect(bar(page)).toBeVisible();
  await expect(bar(page)).toContainText("1 file has changes that are not saved");
  await expect(bar(page)).toContainText(fname(2));
  await expect.poll(() => page.title()).toMatch(/^● /);
  // Save all writes it, and the bar and the dot go
  await bar(page).getByRole("button", { name: "Save all" }).click();
  await expect(bar(page)).toBeHidden();
  await expect.poll(() => onServer(page, p1)).toBe("changed once");
  await expect.poll(() => page.title()).not.toMatch(/^● /);
});

test("after a reload the changes come back, and Save all, Review and Discard all deal with them", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 2);
  const p1 = await pathOf(page, 1);
  const p2 = await pathOf(page, 2);
  await anchors[0].click();
  await expect(tabOf(page, 1)).toHaveAttribute("aria-selected", "true");
  await type(page, "one changed");
  await anchors[1].click();
  await expect(tabOf(page, 2)).toHaveAttribute("aria-selected", "true");
  await type(page, "two changed");
  await expect.poll(() => page.evaluate(() => window.EditorTabs.dirtyPaths().length)).toBe(2);
  // the browser's question is accepted: the page reloads
  page.once("dialog", (d) => d.accept());
  await page.evaluate(() => location.reload());
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.FileBrowser && window.FileBrowser.ready());
  await expect(bar(page)).toBeVisible();
  await expect(bar(page)).toContainText("Changes you had not saved are back in 2 files");
  // Review: one by one. Discard the second: it is read from the server again, and the first is kept
  await bar(page).getByRole("button", { name: "Review" }).click();
  await expect(bar(page).locator(".unsaved-bar__item")).toHaveCount(2);
  await bar(page).locator(".unsaved-bar__item", { hasText: fname(2) }).getByRole("button", { name: "Discard" }).click();
  await expect(bar(page)).toContainText("back in 1 file");
  await expect.poll(() => page.evaluate(() => window.EditorTabs.dirtyPaths().length)).toBe(1);
  expect(await onServer(page, p2)).toBe(""); // nothing was written
  // Save all writes the kept one
  await bar(page).getByRole("button", { name: "Save all" }).click();
  await expect(bar(page)).toBeHidden();
  await expect.poll(() => onServer(page, p1)).toBe("one changed");
});

test("Discard all gives up what came back", async ({ page }) => {
  await ready(page);
  await openFiles(page, 2);
  const path = await pathOf(page, 2);
  await type(page, "to be thrown away");
  page.once("dialog", (d) => d.accept());
  await page.evaluate(() => location.reload());
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.FileBrowser && window.FileBrowser.ready());
  await expect(bar(page)).toBeVisible();
  await bar(page).getByRole("button", { name: "Discard all" }).click();
  await expect(bar(page)).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.EditorTabs.dirtyPaths().length)).toBe(0);
  // nothing was written, and showing the file again reads it from the server
  expect(await onServer(page, path)).toBe("");
  await tabOf(page, 2).click();
  await expect.poll(() => page.evaluate(() => window.editor.env.editor.getValue())).toBe("");
});

test("Save all from the keyboard and the command menu writes every changed tab", async ({ page }) => {
  await ready(page);
  const anchors = await openFiles(page, 2);
  const paths = [await pathOf(page, 1), await pathOf(page, 2)];
  await anchors[0].click();
  await expect(tabOf(page, 1)).toHaveAttribute("aria-selected", "true");
  await type(page, "first");
  await anchors[1].click();
  await expect(tabOf(page, 2)).toHaveAttribute("aria-selected", "true");
  await type(page, "second");
  // the command is in the command menu while something is unsaved
  await page.keyboard.press("ControlOrMeta+Shift+P");
  await expect(page.locator("#palette .palette__item", { hasText: "Save all files" })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.keyboard.press("ControlOrMeta+Alt+S");
  await expect.poll(() => onServer(page, paths[0])).toBe("first");
  await expect.poll(() => onServer(page, paths[1])).toBe("second");
  await expect(tabs(page).locator(".etab__dot")).toHaveCount(0);
  await page.keyboard.press("ControlOrMeta+Shift+P");
  await expect(page.locator("#palette .palette__item", { hasText: "Save all files" })).toHaveCount(0);
});
