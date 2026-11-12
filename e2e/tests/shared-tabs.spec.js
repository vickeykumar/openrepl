// Tabs in a shared session (page/22-editor-tabs.js): what a collaborator sees and can do on their page: the owner's tabs
// (also the untitled one), the owner's shown tab, the + (a request to the owner), and the tab menu with the owner-only items off.
// Two browser contexts: the owner at "/", and a second one that opens the share link.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const fname = (i) => `sh${i}-${stamp}.txt`;

async function ownerPage(browser) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
  return { ctx, page };
}

async function viewerPage(browser, owner) {
  const link = await owner.page.evaluate(() => document.getElementById("share-it").value);
  const url = new URL(link);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/" + url.hash);
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  expect(await page.evaluate(() => isMaster())).toBe(false);
  return { ctx, page };
}

async function openFile(page, i) {
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), fname(i));
  expect(made.ok, JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: fname(i) });
  await expect(anchor).toBeVisible();
  await anchor.click();
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(i));
}

const tabs = (page) => page.locator("#editor-tabs .etab");
const fileTab = (page, i) => tabs(page).filter({ hasText: fname(i) });
const untitled = (page) => page.locator("#editor-tabs .etab--untitled");
const menu = (page) => page.locator("ul.vakata-context:visible");
const item = (page, label) => menu(page).locator("li", { hasText: new RegExp("^\\s*" + label + "\\s*(\\u2318|Ctrl)?") }).first();
const off = async (page, label) => ((await item(page, label).getAttribute("class")) || "").includes("vakata-contextmenu-disabled");

test("a collaborator sees the owner's tabs and the untitled one, gets the +, and follows the owner's shown tab", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 1);
    await openFile(owner.page, 2);
    await owner.page.locator("#new-untitled").click();
    // the collaborator's strip has both files and the untitled tab, and it is the shown one
    await expect(fileTab(viewer.page, 1)).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(fileTab(viewer.page, 2)).toHaveCount(1);
    await expect(untitled(viewer.page)).toHaveCount(1);
    await expect(untitled(viewer.page)).toHaveAttribute("aria-selected", "true");
    await expect(viewer.page.locator("#new-untitled")).toBeVisible();
    // the owner shows a file: the collaborator's strip follows
    await fileTab(owner.page, 1).click();
    await expect(fileTab(viewer.page, 1)).toHaveAttribute("aria-selected", "true", { timeout: 20 * 1000 });
    await expect(untitled(viewer.page)).toHaveAttribute("aria-selected", "false");
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("a collaborator's click on the untitled tab, or on +, makes the owner show the untitled tab", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 3);
    await owner.page.locator("#new-untitled").click();
    await fileTab(owner.page, 3).click();
    await owner.page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(3));
    await expect(untitled(viewer.page)).toHaveCount(1, { timeout: 20 * 1000 });
    // the collaborator chooses the untitled tab
    await untitled(viewer.page).click();
    await owner.page.waitForFunction(() => window.EditorTabs.shown() === "untitled:", null, { timeout: 20 * 1000 });
    // the owner closes it; the collaborator's + asks for a new one
    await owner.page.locator("#editor-tabs .etab--untitled .etab__x").click();
    await expect(untitled(owner.page)).toHaveCount(0);
    await expect(untitled(viewer.page)).toHaveCount(0, { timeout: 20 * 1000 });
    await viewer.page.locator("#new-untitled").click();
    await expect(untitled(owner.page)).toHaveCount(1, { timeout: 20 * 1000 });
    await owner.page.waitForFunction(() => window.EditorTabs.shown() === "untitled:");
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("a collaborator's tab menu has the Files menu for the file and the Close items (they ask the owner); the untitled tab's has Download and Close", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 4);
    await owner.page.locator("#new-untitled").click();
    await expect(fileTab(viewer.page, 4)).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(untitled(viewer.page)).toHaveCount(1);
    await fileTab(viewer.page, 4).click({ button: "right" });
    await expect(menu(viewer.page)).toBeVisible();
    for (const label of ["Rename", "Save", "Download", "Copy path"]) expect(await off(viewer.page, label), label).toBe(false);
    expect(await off(viewer.page, "Paste"), "Paste").toBe(true);
    for (const label of ["Close", "Close others", "Close all"]) expect(await off(viewer.page, label), label).toBe(false); // they ask the owner
    await viewer.page.keyboard.press("Escape");
    await untitled(viewer.page).click({ button: "right" });
    await expect(menu(viewer.page)).toBeVisible();
    expect(await off(viewer.page, "Download")).toBe(false);
    for (const label of ["Save", "Rename", "Delete"]) expect(await off(viewer.page, label), label).toBe(true);
    expect(await off(viewer.page, "Close"), "Close").toBe(false);
    await viewer.page.keyboard.press("Escape");
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("a collaborator's close button asks the owner to close the tab, with the usual question when it has changes", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 5);
    await openFile(owner.page, 6);
    await expect(fileTab(viewer.page, 5)).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(fileTab(viewer.page, 6)).toHaveCount(1);
    // the collaborator sees the close buttons
    await expect(fileTab(viewer.page, 5).locator(".etab__x")).toHaveCount(1);
    // an unchanged tab: closed on the owner's page, and so on the collaborator's
    await fileTab(viewer.page, 5).locator(".etab__x").click({ force: true });
    await expect(fileTab(owner.page, 5)).toHaveCount(0, { timeout: 20 * 1000 });
    await expect(fileTab(viewer.page, 5)).toHaveCount(0, { timeout: 20 * 1000 });
    // a tab with changes: the owner's page asks
    await openFile(owner.page, 7);
    await owner.page.evaluate(() => window.editor.env.editor.setValue("owner's unsaved text", -1));
    await expect(fileTab(owner.page, 7).locator(".etab__dot")).toBeVisible();
    await expect(fileTab(viewer.page, 7)).toHaveCount(1, { timeout: 20 * 1000 });
    await fileTab(viewer.page, 7).locator(".etab__x").click({ force: true });
    const dialog = owner.page.locator(".unsaved-dialog");
    await expect(dialog).toBeVisible({ timeout: 20 * 1000 });
    await dialog.getByRole("button", { name: "Don't save" }).click();
    await expect(fileTab(owner.page, 7)).toHaveCount(0);
    await expect(fileTab(viewer.page, 7)).toHaveCount(0, { timeout: 20 * 1000 });
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});
