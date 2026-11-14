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

// A collaborator's page starts in the owner's language, and does not make the owner's page start again in the picker's first one.
test("a collaborator starts in the owner's language and the owner's terminal is left alone", async ({ browser }) => {
  test.setTimeout(150 * 1000);
  const octx = await browser.newContext();
  const owner = await octx.newPage();
  await owner.goto("/");
  await owner.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(owner);
  await owner.evaluate(() => window.pickLanguage("python"));
  const text = (p) => p.locator("#terminal-div .xterm-rows").first().innerText();
  await expect.poll(() => text(owner), { timeout: 60 * 1000 }).toContain(">>>");
  await owner.evaluate(() => window.sendToActiveTerminal("print('OWNER-MARKER')\r"));
  await expect.poll(() => text(owner)).toContain("OWNER-MARKER");
  const url = new URL(await owner.evaluate(() => document.getElementById("share-it").value));

  const vctx = await browser.newContext();
  const viewer = await vctx.newPage();
  await viewer.goto("/" + url.hash);
  await viewer.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  // the collaborator is in Python from the start, not in C
  expect(await viewer.evaluate(() => $("#optionlist").val())).toBe("python");
  await expect.poll(() => text(viewer), { timeout: 30 * 1000 }).toContain("OWNER-MARKER");
  // and the owner's page did not start again in C: its terminal still holds what it printed, in Python
  await owner.waitForTimeout(8000);
  expect(await owner.evaluate(() => $("#optionlist").val())).toBe("python");
  expect(await text(owner)).toContain("OWNER-MARKER");
  await octx.close();
  await vctx.close();
});
