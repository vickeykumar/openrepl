// A shared session whose owner is not there (page/30-owner-presence.js): the collaborator is told in a popup, a bar stays,
// Run is off, and a session that has ended says so. Two browser contexts: the owner at "/", and one that opens the share link.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function owner(browser) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.OwnerAway);
  await waitForTerminalReady(page);
  return { ctx, page, link: await page.evaluate(() => document.getElementById("share-it").value) };
}

async function viewer(browser, link) {
  const url = new URL(link);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/" + url.hash);
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.OwnerAway);
  return { ctx, page };
}

const dialog = (page) => page.locator("#owner-away");
const bar = (page) => page.locator("#owner-away-bar");

test("a collaborator is told when the owner leaves, and when nobody is there", async ({ browser }) => {
  test.setTimeout(180 * 1000);
  const o = await owner(browser);
  const v = await viewer(browser, o.link);
  expect(await v.page.evaluate(() => isMaster())).toBe(false);
  // the owner is here: nothing is shown, and the collaborator has seen them online
  await expect.poll(() => v.page.evaluate(() => window.OwnerAway.state().seenOnline), { timeout: 30 * 1000 }).toBe(true);
  await v.page.waitForTimeout(10 * 1000);
  await expect(dialog(v.page)).toBeHidden();
  await expect(bar(v.page)).toHaveCount(0);

  // the owner closes the page
  await o.ctx.close();
  await expect(dialog(v.page)).toBeVisible({ timeout: 90 * 1000 });
  await expect(dialog(v.page)).toContainText("The owner has left this session");
  await expect(dialog(v.page).getByRole("button", { name: "Copy it to my own editor" })).toBeVisible();
  await expect(dialog(v.page).getByRole("button", { name: "Stay and wait for them" })).toBeVisible();
  // Esc closes the popup, the bar stays
  await v.page.keyboard.press("Escape");
  await expect(dialog(v.page)).toBeHidden();
  await expect(bar(v.page)).toBeVisible();
  await expect(bar(v.page)).toContainText("The owner has left");
  // Run is off while the owner is away
  await v.page.evaluate(() => window.CompileandRun());
  await expect(v.page.locator(".notice, #notifications, [role=status]", { hasText: "The owner is not here to run it" }).first()).toBeVisible();
  // a change in the collaborator's page is not shared
  expect(await v.page.evaluate(() => window.OwnerAway.active())).toBe(true);

  // a collaborator who opens the link now finds nobody there
  const v2 = await viewer(browser, o.link);
  await expect(dialog(v2.page)).toBeVisible({ timeout: 60 * 1000 });
  await expect(dialog(v2.page)).toContainText("Nobody is hosting this session");
  await v.ctx.close();
  await v2.ctx.close();
});

test("a link to a session that has ended says so, and does not bring it back", async ({ browser }) => {
  test.setTimeout(90 * 1000);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/#-Nosuchsession" + Date.now().toString(36));
  await page.waitForFunction(() => window.OwnerAway && window.OwnerAway.state);
  await expect(dialog(page)).toBeVisible({ timeout: 30 * 1000 });
  await expect(dialog(page)).toContainText("This shared session has ended");
  await expect(bar(page)).toHaveCount(0);
  await ctx.close();
});
