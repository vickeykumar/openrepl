// What picking another language does to the editor's text (page/02-layout.js, updateEditorContent): the starter code of the
// new language loads only into an untitled editor that holds nothing of the visitor's own; a file is never touched, and
// typed text is kept, with a notice that offers the starter.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const fname = (i) => `ls${i}-${stamp}.txt`;
const tabs = (page) => page.locator("#editor-tabs .etab");
const fileTab = (page, i) => tabs(page).filter({ hasText: fname(i) });
const untitled = (page) => page.locator("#editor-tabs .etab--untitled");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready() && window.pickLanguage);
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
  // the file has been read (its text is the baseline of the dot): typing before that would count as the file's own text
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), fname(i));
}

const editorText = (page) => page.evaluate(() => window.editor.env.editor.getValue());
const type = (page, text) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), text);

// another language than the one chosen now
async function otherLanguage(page) {
  return page.evaluate(() => {
    const sel = document.getElementById("optionlist");
    return ["python", "go", "java", "javascript"].find((v) => v !== sel.value && [...sel.options].some((o) => o.value === v));
  });
}

test("an empty untitled editor gets the starter code of the new language, and it is not a change of the visitor's", async ({ page }) => {
  await ready(page);
  await openFile(page, 1);
  await page.locator("#new-untitled").click();
  expect(await editorText(page)).toBe("");
  const lang = await otherLanguage(page);
  await page.evaluate((v) => window.pickLanguage(v), lang);
  await expect.poll(() => editorText(page), { timeout: 20 * 1000 }).not.toBe("");
  // the starter is not the visitor's own text: no dot on the tab
  await expect(untitled(page).locator(".etab__dot")).toHaveCount(0);
});

test("text typed in an untitled editor is kept when the language changes, and a notice offers the starter", async ({ page }) => {
  await ready(page);
  await openFile(page, 2);
  await page.locator("#new-untitled").click();
  await type(page, "my own code");
  const lang = await otherLanguage(page);
  await page.evaluate((v) => window.pickLanguage(v), lang);
  const notice = page.locator(".notice", { hasText: "Your text is kept" });
  await expect(notice).toBeVisible({ timeout: 20 * 1000 });
  expect(await editorText(page)).toBe("my own code");
  await notice.getByRole("button", { name: "Load starter code" }).click();
  await expect.poll(() => editorText(page)).not.toBe("my own code");
  expect(await editorText(page)).not.toBe("");
});

test("a file shown in the editor is never replaced by a starter, whatever the Files panel highlights", async ({ page }) => {
  await ready(page);
  await openFile(page, 3);
  await type(page, "file text, unsaved");
  const lang = await otherLanguage(page);
  await page.evaluate((v) => window.pickLanguage(v), lang);
  // the starter would have come within about 3 seconds
  await page.waitForTimeout(6000);
  expect(await editorText(page)).toBe("file text, unsaved");
  // (a change of language also saves the open file before the terminal restarts: that is how the page already worked)
  await expect(untitled(page)).toHaveCount(0);
  await expect(page.locator(".notice", { hasText: "Your text is kept" })).toHaveCount(0);
});
