// The language chosen last is chosen again when the home page opens (page/32-remember-language.js), for the picker, the
// editor's mode and the terminal; a share link does not take it (it follows the owner).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const state = (page) => page.evaluate(() => ({ picker: $("#optionlist").val(), mode: $("#select-lang").val() }));

test("a reload opens in the language that was chosen last", async ({ page }) => {
  test.setTimeout(120 * 1000);
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
  await page.evaluate(() => window.pickLanguage("python"));
  await expect.poll(() => state(page)).toEqual({ picker: "python", mode: "python" });
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toEqual({ picker: "python", mode: "python" });
  // the terminal is that language's too
  await expect(page.locator("#terminal-div .xterm-rows").first()).toContainText(">>>", { timeout: 60 * 1000 });
  // another language, another reload
  await page.evaluate(() => window.pickLanguage("bash"));
  await expect.poll(() => state(page)).toMatchObject({ picker: "bash" });
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await expect.poll(() => page.evaluate(() => $("#optionlist").val()), { timeout: 20 * 1000 }).toBe("bash");
});

test("a language left in the session by somebody else does not replace the owner's on a reload, or become the one remembered", async ({ page }) => {
  test.setTimeout(150 * 1000);
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready() && window.ShareSession);
  await waitForTerminalReady(page);
  await page.evaluate(() => window.pickLanguage("python"));
  await expect.poll(() => state(page)).toEqual({ picker: "python", mode: "python" });
  // Share starts a session that keeps its address through a reload
  await page.locator("#share-btn-1").click();
  await expect(page.locator("#share-session")).toBeVisible();
  const id = await page.evaluate(() => window.dbpath);
  // a record of another language in the session (what a collaborator's page used to leave there)
  await page.evaluate((i) => firebase.database().ref("editor_values/" + i + "/lang").set({ data: "assembly_x86", silent: true }), id);
  await page.waitForTimeout(1500);
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  expect(await page.evaluate(() => window.dbpath)).toBe(id); // the same session
  await page.waitForTimeout(6000);
  expect(await state(page)).toEqual({ picker: "python", mode: "python" });
  // what was applied from the session while the page was open was not kept as the language of the visitor
  await page.evaluate((i) => firebase.database().ref("editor_values/" + i + "/lang").set({ data: "assembly_x86", silent: true }), id);
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toMatchObject({ picker: "rappel" });
  expect(await page.evaluate(() => localStorage.getItem("openrepl-language"))).toBe("python");
});
