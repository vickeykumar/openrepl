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
// a collaborator's click on a tab also selects it, and the owner's answer comes back through Firebase while the menu opens:
// the menu is opened again if that closed it
async function openMenu(page, tab) {
  for (let attempt = 0; attempt < 4; attempt++) {
    await tab.click({ button: "right" });
    try {
      await menu(page).waitFor({ state: "visible", timeout: 4000 });
      await page.waitForTimeout(1500); // the echo is over
      if (await menu(page).isVisible()) return;
    } catch (e) { /* again */ }
  }
  await expect(menu(page)).toBeVisible();
}
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

test("a collaborator's tab menu has the Files menu for the file and the Close items (they ask the owner); the untitled tab's has Save, Download and Close", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 4);
    await owner.page.locator("#new-untitled").click();
    await expect(fileTab(viewer.page, 4)).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(untitled(viewer.page)).toHaveCount(1);
    await openMenu(viewer.page, fileTab(viewer.page, 4));
    for (const label of ["Rename", "Save", "Download", "Copy path"]) expect(await off(viewer.page, label), label).toBe(false);
    expect(await off(viewer.page, "Paste"), "Paste").toBe(true);
    for (const label of ["Close", "Close others", "Close all"]) expect(await off(viewer.page, label), label).toBe(false); // they ask the owner
    await viewer.page.keyboard.press("Escape");
    await openMenu(viewer.page, untitled(viewer.page));
    expect(await off(viewer.page, "Download")).toBe(false);
    for (const label of ["Rename", "Delete"]) expect(await off(viewer.page, label), label).toBe(true);
    for (const label of ["Save", "Close"]) expect(await off(viewer.page, label), label).toBe(false);
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
    // a tab with changes: the collaborator sees the dot, and is the one who is asked (Save or Cancel, no Don't save)
    await openFile(owner.page, 7);
    await owner.page.waitForTimeout(800); // the file has been read: its text is the baseline of the dot
    await owner.page.evaluate(() => window.editor.env.editor.setValue("owner's unsaved text", -1));
    await expect(fileTab(owner.page, 7).locator(".etab__dot")).toBeVisible();
    await expect(fileTab(viewer.page, 7).locator(".etab__dot")).toBeVisible({ timeout: 20 * 1000 });
    await fileTab(viewer.page, 7).locator(".etab__x").click({ force: true });
    const vdialog = viewer.page.locator(".unsaved-dialog");
    await expect(vdialog).toBeVisible({ timeout: 20 * 1000 });
    await expect(vdialog).toContainText("unsaved changes of the person who started the session");
    await expect(vdialog.getByRole("button", { name: "Don't save" })).toHaveCount(0);
    // the owner's page shows a notice, not the question
    await expect(owner.page.locator(".notice", { hasText: "A collaborator is closing" })).toBeVisible({ timeout: 20 * 1000 });
    await expect(owner.page.locator(".unsaved-backdrop")).toBeHidden();
    // Cancel: nothing happens
    await vdialog.getByRole("button", { name: "Cancel" }).click();
    await expect(fileTab(owner.page, 7)).toHaveCount(1);
    // Save: the owner's page writes its text and closes the tab
    await fileTab(viewer.page, 7).locator(".etab__x").click({ force: true });
    await expect(vdialog).toBeVisible({ timeout: 20 * 1000 });
    await vdialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(fileTab(owner.page, 7)).toHaveCount(0, { timeout: 20 * 1000 });
    await expect(fileTab(viewer.page, 7)).toHaveCount(0, { timeout: 20 * 1000 });
    const saved = await owner.page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), fname(7));
    expect(saved).toBe("owner's unsaved text");
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("one person is asked at a time: while the owner is asked about a tab, a collaborator's close is told so", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 8);
    await owner.page.waitForTimeout(800); // the file has been read: its text is the baseline of the dot
    await owner.page.evaluate(() => window.editor.env.editor.setValue("owner's text", -1));
    await expect(fileTab(owner.page, 8).locator(".etab__dot")).toBeVisible();
    await expect(fileTab(viewer.page, 8).locator(".etab__dot")).toBeVisible({ timeout: 20 * 1000 });
    await fileTab(owner.page, 8).locator(".etab__x").click();
    const odialog = owner.page.locator(".unsaved-dialog");
    await expect(odialog).toBeVisible();
    await fileTab(viewer.page, 8).locator(".etab__x").click({ force: true });
    await expect(viewer.page.locator(".notice", { hasText: "already being closed" })).toBeVisible({ timeout: 20 * 1000 });
    await expect(viewer.page.locator(".unsaved-dialog")).toBeHidden();
    await odialog.getByRole("button", { name: "Cancel" }).click();
    await expect(fileTab(owner.page, 8)).toHaveCount(1);
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("a collaborator saves the owner's untitled text as a file: they name it, the owner's page makes it and the tab becomes that file", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 9);
    await owner.page.locator("#new-untitled").click();
    await owner.page.evaluate(() => window.editor.env.editor.setValue("scratch of the owner", -1));
    await expect(untitled(viewer.page)).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(untitled(viewer.page)).toHaveAttribute("aria-selected", "true", { timeout: 20 * 1000 });
    await expect(untitled(viewer.page).locator(".etab__dot")).toBeVisible({ timeout: 20 * 1000 });
    // the menu's Save, on the collaborator's page
    await openMenu(viewer.page, untitled(viewer.page));
    await item(viewer.page, "Save").click();
    const vdialog = viewer.page.locator(".unsaved-dialog");
    await expect(vdialog.locator("h2")).toHaveText("Save the untitled file as", { timeout: 20 * 1000 });
    // a name that exists is refused on the collaborator's page, before anything is sent
    await vdialog.locator("input").fill(fname(9));
    await vdialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(vdialog).toContainText("exists already");
    const name = `collab-${stamp}.txt`;
    await vdialog.locator("input").fill(name);
    await vdialog.getByRole("button", { name: "Save", exact: true }).click();
    // the owner's page: the tab is that file now, with the text, saved
    const tab = tabs(owner.page).filter({ hasText: name });
    await expect(tab).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(untitled(owner.page)).toHaveCount(0);
    await expect(tab.locator(".etab__dot")).toHaveCount(0);
    expect(await owner.page.evaluate(() => window.editor.env.editor.getValue())).toBe("scratch of the owner");
    const got = await owner.page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), name);
    expect(got).toBe("scratch of the owner");
    // and the collaborator sees the tab as that file
    await expect(tabs(viewer.page).filter({ hasText: name })).toHaveCount(1, { timeout: 20 * 1000 });
    await expect(untitled(viewer.page)).toHaveCount(0, { timeout: 20 * 1000 });
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});

test("a collaborator closing the owner's untitled tab with text is asked Save or Cancel; Save asks for a name, saves and closes it", async ({ browser }) => {
  const owner = await ownerPage(browser);
  const viewer = await viewerPage(browser, owner);
  try {
    await openFile(owner.page, 10);
    await owner.page.locator("#new-untitled").click();
    await owner.page.evaluate(() => window.editor.env.editor.setValue("text to keep", -1));
    await expect(untitled(viewer.page).locator(".etab__dot")).toBeVisible({ timeout: 20 * 1000 });
    await untitled(viewer.page).locator(".etab__x").click();
    const vdialog = viewer.page.locator(".unsaved-dialog");
    await expect(vdialog).toContainText("unsaved changes of the person who started the session", { timeout: 20 * 1000 });
    await expect(vdialog.getByRole("button", { name: "Don't save" })).toHaveCount(0);
    await vdialog.getByRole("button", { name: "Cancel" }).click();
    await expect(untitled(owner.page)).toHaveCount(1);
    await untitled(viewer.page).locator(".etab__x").click();
    await expect(vdialog).toContainText("unsaved changes of the person who started the session", { timeout: 20 * 1000 });
    await vdialog.getByRole("button", { name: "Save", exact: true }).click();
    await expect(vdialog.locator("h2")).toHaveText("Save the untitled file as", { timeout: 20 * 1000 });
    const name = `collab-close-${stamp}.txt`;
    await vdialog.locator("input").fill(name);
    await vdialog.getByRole("button", { name: "Save", exact: true }).click();
    // saved by the owner's page, and the tab is gone for both
    await expect(untitled(owner.page)).toHaveCount(0, { timeout: 20 * 1000 });
    await expect(tabs(owner.page).filter({ hasText: name })).toHaveCount(0);
    const got = await owner.page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), name);
    expect(got).toBe("text to keep");
    await expect(untitled(viewer.page)).toHaveCount(0, { timeout: 20 * 1000 });
  } finally {
    await owner.ctx.close();
    await viewer.ctx.close();
  }
});
