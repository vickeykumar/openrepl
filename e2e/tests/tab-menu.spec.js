// The right-click menu of a tab (page/22-editor-tabs.js): the Files panel's own menu for the file, with what does not apply to
// a tab greyed out, and Close, Close others and Close all. The untitled tab has Save, Download and the three Close items on.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const fname = (i) => `tm${i}-${stamp}.txt`;
const tabs = (page) => page.locator("#editor-tabs .etab");
const fileTab = (page, i) => tabs(page).filter({ hasText: fname(i) });
const untitled = (page) => page.locator("#editor-tabs .etab--untitled");
const menu = (page) => page.locator("ul.vakata-context:visible");
const item = (page, label) => menu(page).locator("li", { hasText: new RegExp("^\\s*" + label + "\\s*(\\u2318|Ctrl)?") }).first();
const dialog = (page) => page.locator(".unsaved-dialog");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready());
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function openFile(page, i) {
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), fname(i));
  expect(made.ok, JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: fname(i) });
  await expect(anchor).toBeVisible();
  await anchor.click();
  await expect(fileTab(page, i)).toHaveAttribute("aria-selected", "true");
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(i));
}

const disabled = async (page, label) => (await item(page, label).getAttribute("class") || "").includes("vakata-contextmenu-disabled");
const type = (page, text) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), text);

test("right-clicking a tab shows it first and opens the Files menu for the file, with Paste off and the Close items added", async ({ page }) => {
  await ready(page);
  await openFile(page, 1);
  await openFile(page, 2);
  // a right-click on the other tab brings it forward, then the menu opens
  await fileTab(page, 1).click({ button: "right" });
  await expect(fileTab(page, 1)).toHaveAttribute("aria-selected", "true");
  await expect(menu(page)).toBeVisible();
  for (const label of ["Rename", "Cut", "Copy", "Paste", "Save", "Download", "Delete", "Copy path", "Copy relative path", "Close", "Close others", "Close all"]) {
    await expect(item(page, label), label).toBeVisible();
  }
  expect(await disabled(page, "Paste")).toBe(true);
  for (const label of ["Rename", "Save", "Download", "Delete", "Copy path", "Close", "Close others", "Close all"]) expect(await disabled(page, label), label).toBe(false);
  await page.keyboard.press("Escape");
});

test("Close others keeps the tab, Close all closes every one, and a tab with changes asks first", async ({ page }) => {
  await ready(page);
  await openFile(page, 3);
  await openFile(page, 4);
  await openFile(page, 5);
  await type(page, "unsaved text in the last tab");
  await fileTab(page, 3).click({ button: "right" });
  await item(page, "Close others").click();
  // the last tab has changes: it asks, and Don't save closes it
  await expect(dialog(page)).toBeVisible();
  await dialog(page).getByRole("button", { name: "Don't save" }).click();
  await expect(fileTab(page, 3)).toHaveCount(1);
  await expect(fileTab(page, 4)).toHaveCount(0);
  await expect(fileTab(page, 5)).toHaveCount(0);
  // Close all: nothing is left but the untitled buffer
  await fileTab(page, 3).click({ button: "right" });
  await item(page, "Close all").click();
  await expect(fileTab(page, 3)).toHaveCount(0);
  await expect(untitled(page)).toHaveCount(1);
});

test("Save in the menu of a tab saves that tab's file", async ({ page }) => {
  await ready(page);
  await openFile(page, 6);
  await openFile(page, 7);
  await fileTab(page, 6).click();
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(6));
  await type(page, "text for file six");
  await expect(fileTab(page, 6).locator(".etab__dot")).toBeVisible();
  // another tab is shown, then the first one is right-clicked: its text must be in the editor before Save writes it
  await fileTab(page, 7).click();
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(7));
  await fileTab(page, 6).click({ button: "right" });
  await item(page, "Save").click();
  await expect(fileTab(page, 6).locator(".etab__dot")).toHaveCount(0);
  const got = await page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), fname(6));
  expect(got).toBe("text for file six");
  const other = await page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), fname(7));
  expect(other).toBe("");
});

test("the untitled tab's menu has Save, Download and the Close items on, and everything about a file off", async ({ page }) => {
  await ready(page);
  await openFile(page, 8);
  await page.locator("#new-untitled").click();
  await type(page, "scratch text");
  await untitled(page).click({ button: "right" });
  await expect(menu(page)).toBeVisible();
  for (const label of ["Rename", "Cut", "Copy", "Paste", "Delete", "Copy path", "Copy relative path", "Add to Genie chat"]) expect(await disabled(page, label), label).toBe(true);
  for (const label of ["Save", "Download", "Close", "Close others", "Close all"]) expect(await disabled(page, label), label).toBe(false);
  // Save asks for a name
  await item(page, "Save").click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("h2")).toHaveText("Save the untitled file as");
  await page.keyboard.press("Escape");
});

test("the Menu key on a focused tab opens the menu too", async ({ page }) => {
  await ready(page);
  await openFile(page, 9);
  await fileTab(page, 9).focus();
  await page.keyboard.press("Shift+F10");
  await expect(menu(page)).toBeVisible();
  await expect(item(page, "Close")).toBeVisible();
  await page.keyboard.press("Escape");
});
