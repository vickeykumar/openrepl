// The language of a shared session: a collaborator who opens the share link takes the owner's language; it must not
// replace it with the first entry of the editor's language list (Assembly) on both pages.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const state = (page) => page.evaluate(() => ({ picker: $("#optionlist").val(), mode: $("#select-lang").val() }));

test("a collaborator joining keeps the owner's language on both pages", async ({ browser }) => {
  test.setTimeout(120 * 1000);
  const octx = await browser.newContext();
  const owner = await octx.newPage();
  await owner.goto("/");
  await owner.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(owner);
  await owner.evaluate(() => window.pickLanguage("python"));
  await expect.poll(() => state(owner)).toEqual({ picker: "python", mode: "python" });
  const url = new URL(await owner.evaluate(() => document.getElementById("share-it").value));
  const vctx = await browser.newContext();
  const viewer = await vctx.newPage();
  await viewer.goto("/" + url.hash);
  await viewer.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  expect(await viewer.evaluate(() => isMaster())).toBe(false);
  // the collaborator takes the owner's language, and the owner's page is not moved
  await expect.poll(() => state(viewer), { timeout: 20 * 1000 }).toEqual({ picker: "python", mode: "python" });
  await viewer.waitForTimeout(6000);
  expect(await state(viewer)).toEqual({ picker: "python", mode: "python" });
  expect(await state(owner)).toEqual({ picker: "python", mode: "python" });
  // the owner changes it later: both follow
  await owner.evaluate(() => window.pickLanguage("go"));
  await expect.poll(() => state(viewer), { timeout: 20 * 1000 }).toMatchObject({ picker: "go" });
  await octx.close();
  await vctx.close();
});
