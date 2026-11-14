// A share link with a time (page/31-share-session.js): started by opening Share, the same after a reload, ended by the owner or
// by the clock, and what a collaborator sees then.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function owner(browser) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.ShareSession);
  await waitForTerminalReady(page);
  return { ctx, page };
}
const link = (page) => page.evaluate(() => document.getElementById("share-it").value);
// the address is put in the box when the terminal opens: wait for it
async function readLink(page) {
  await expect.poll(() => link(page), { timeout: 30 * 1000 }).toContain("#");
  return link(page);
}
async function openShare(page) {
  await page.locator("#share-btn-1").click();
  await expect(page.locator("#share-session")).toBeVisible();
}
async function viewer(browser, url, ended) {
  const u = new URL(url);
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto("/" + u.hash);
  // the files of a link that is over cannot load: only the page's own script is waited for
  if (ended) await page.waitForFunction(() => window.OwnerAway && window.OwnerAway.state);
  else await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.OwnerAway);
  return { ctx, page };
}
const dialog = (page) => page.locator("#owner-away");

test("the link works for the time chosen and is the same after a reload", async ({ browser }) => {
  test.setTimeout(120 * 1000);
  const o = await owner(browser);
  const before = await readLink(o.page);
  await openShare(o.page);
  await expect(o.page.locator("#share-left")).toContainText("Ends in");
  expect(await o.page.locator("#share-mins").inputValue()).toBe("60");
  // a guest's choices stop at 1 hour
  expect(await o.page.locator("#share-mins option").allTextContents()).toEqual(["30 minutes", "1 hour"]);
  await o.page.locator("#share-mins").selectOption("30");
  await expect(o.page.locator("#share-left")).toContainText(/Ends in (29|30) minutes/);
  await o.page.locator("#share-add").click();
  await expect(o.page.locator("#share-left")).toContainText(/Ends in (5\d|1 hour [0-9]+) /);
  const kept = await readLink(o.page);
  expect(kept).toBe(before);
  // a reload: the same link, the same time running
  await o.page.reload();
  await o.page.waitForFunction(() => window.ShareSession && window.ShareSession.active());
  expect(await readLink(o.page)).toBe(kept);
  await openShare(o.page);
  await expect(o.page.locator("#share-left")).toContainText("Ends in");
  await o.ctx.close();
});

test("End session tells the collaborators, and a new Share makes a new link", async ({ browser }) => {
  test.setTimeout(150 * 1000);
  const o = await owner(browser);
  await openShare(o.page);
  const first = await readLink(o.page);
  const v = await viewer(browser, first);
  await expect.poll(() => v.page.evaluate(() => window.OwnerAway.state().seenOnline), { timeout: 30 * 1000 }).toBe(true);
  const id = new URL(first).hash.slice(1);
  // chat and terminal records exist while the session lives
  await v.page.evaluate((i) => firebase.database().ref("chat-list/" + i).push({ eventT: "join", uid: "x", timestamp: Date.now() }), id);
  await o.page.locator("#share-end").click();
  await o.page.getByRole("button", { name: "End session" }).last().click();
  await expect(dialog(v.page)).toBeVisible({ timeout: 30 * 1000 });
  await expect(dialog(v.page)).toContainText("The owner ended this session");
  await expect(dialog(v.page).getByRole("button", { name: "Copy it to my own editor" })).toBeVisible();
  // the collaborator's page can write nothing now: the editor is read-only, Firebase and the owner's files are shut
  await v.page.keyboard.press("Escape");
  expect(await v.page.evaluate(() => document.getElementById("editor").env.editor.getReadOnly())).toBe(true);
  const dropped = await v.page.evaluate(async (i) => {
    const ref = firebase.database().ref("chat-list/" + i);
    await ref.push({ eventT: "message", text: "after the end" });
    await firebase.database().ref("editor_values/" + i + "/content").set("changed after the end");
    let files = "reached";
    try { await fetch(preprocessurl("/ws_filebrowser?q=load")); } catch (e) { files = "refused"; }
    return files;
  }, id);
  expect(dropped).toBe("refused");
  // the owner's page loads again and gets a link of its own, not the ended one
  await expect.poll(async () => (await o.page.evaluate(() => (document.getElementById("share-it") || {}).value || "").catch(() => "")), { timeout: 60 * 1000 }).not.toBe(first);
  await o.page.waitForFunction(() => window.ShareSession && window.EditorTabs);
  const fresh = await readLink(o.page);
  expect(fresh).not.toBe(first);
  expect(await o.page.evaluate(() => window.ShareSession.active())).toBe(false);
  // somebody who opens the old link later finds it over, with no code
  const late = await viewer(browser, first, true);
  await expect(dialog(late.page)).toBeVisible({ timeout: 30 * 1000 });
  // nothing of the session is kept, so a late visitor is told that it has ended
  await expect(dialog(late.page)).toContainText("This shared session has ended");
  await expect(dialog(late.page).getByRole("button", { name: "Start my own editor" })).toBeVisible();
  // everything of the session is gone from Firebase: no code, no record
  await expect.poll(() => late.page.evaluate(async (id) => {
    const left = [];
    for (const path of ["editor_values", "file-browser", "openrepl", "chat-list"]) {
      const snap = await firebase.database().ref(path + "/" + id).once("value");
      if (snap.exists()) left.push(path);
    }
    return left;
  }, id), { timeout: 40 * 1000 }).toEqual([]);
  await o.ctx.close();
  await v.ctx.close();
  await late.ctx.close();
});

test("a link whose time ran out expires for the people on it", async ({ browser }) => {
  test.setTimeout(150 * 1000);
  const o = await owner(browser);
  await openShare(o.page);
  const v = await viewer(browser, await readLink(o.page));
  await expect.poll(() => v.page.evaluate(() => window.OwnerAway.state().seenOnline), { timeout: 30 * 1000 }).toBe(true);
  // the end comes now (the clock of the owner's page is checked every 15 seconds)
  await o.page.evaluate(() => { window.ShareSession.record().endsAt = Date.now() - 1000; });
  await expect(dialog(v.page)).toBeVisible({ timeout: 60 * 1000 });
  await expect(dialog(v.page)).toContainText("This link has expired");
  await o.ctx.close();
  await v.ctx.close();
});
