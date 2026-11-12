// The practice question page (index.html, showQuestionInEditor; page/22-editor-tabs.js prepareGenerated): a question never
// replaces the text of a file. With a file shown, an untitled tab opens and takes the question; typed text in the
// untitled editor is asked about first.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const NAME = `two-sum-${stamp}`;
const fname = (i) => `pr${i}-${stamp}.txt`;
const tabs = (page) => page.locator("#editor-tabs .etab");
const fileTab = (page, i) => tabs(page).filter({ hasText: fname(i) });
const untitled = (page) => page.locator("#editor-tabs .etab--untitled");
const dialog = (page) => page.locator(".unsaved-dialog");

// a question that is already complete in the browser's list, with its starter for two languages: nothing is asked of a model
function question() {
  const t = (start, end, body) => ({ multiline_comment_start: start, multiline_comment_end: end, template: body });
  return {
    id: "e2e-" + NAME, name: "Two Sum " + stamp, nameHyphenated: NAME, difficulty: "Easy", description: "Return the indices of two numbers that add up to a target.",
    delimeter: "---- write your solution below ----",
    code_templates: { Python: t('"""', '"""', "def two_sum(nums, target):\n    pass"), Go: t("/*", "*/", "func twoSum(nums []int, target int) []int {\n\treturn nil\n}"), JavaScript: t("/*", "*/", "function twoSum(nums, target) {}") }
  };
}

async function practice(page) {
  await page.addInitScript((q) => { try { localStorage.setItem("questions", JSON.stringify([q])); } catch (e) {} }, question());
  await page.goto(`/practice?name=${NAME}`);
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready() && window.pickLanguage);
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

const editorText = (page) => page.evaluate(() => window.editor.env.editor.getValue());
const type = (page, text) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), text);
const otherLanguage = (page, not) => page.evaluate((n) => [...document.querySelectorAll("#optionlist option")].map((o) => o.value).find((v) => v !== n && ["python", "go", "javascript"].includes(v)), not);
const currentLanguage = (page) => page.evaluate(() => document.getElementById("optionlist").value);

test("with a file chosen, a language change brings no question: the file is left alone and no untitled tab opens", async ({ page }) => {
  await practice(page);
  await openFile(page, 1);
  await type(page, "my file, not a question");
  const lang = await otherLanguage(page, await currentLanguage(page));
  await page.evaluate((v) => window.pickLanguage(v), lang);
  // a question would have come within about 5 seconds
  await page.waitForTimeout(8000);
  expect(await editorText(page)).toBe("my file, not a question");
  await expect(untitled(page)).toHaveCount(0);
});

test("choosing a file changes the language by its extension and brings no question", async ({ page }) => {
  await practice(page);
  const name = `ext-${stamp}.py`;
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), name);
  expect(made.ok, JSON.stringify(made)).toBe(true);
  await page.locator("#file-browser .jstree-anchor", { hasText: name }).click();
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), name);
  // the language follows the extension, as it always did (page/07-file-browser.js changelangbyselectednode does this when the
  // shared selection comes back from Firebase, which a test server may not reach: the same event is sent here)
  await page.evaluate(() => { const s = document.getElementById("select-lang"); s.value = "python"; s.dispatchEvent(new CustomEvent("change", { detail: { silent: true } })); });
  await expect.poll(() => currentLanguage(page), { timeout: 20 * 1000 }).toBe("python");
  await page.waitForTimeout(8000);
  expect(await editorText(page)).toBe("");
  await expect(untitled(page)).toHaveCount(0);
});

test("text typed from scratch in the untitled editor is asked about before a question replaces it; Cancel keeps it", async ({ page }) => {
  await practice(page);
  await openFile(page, 2);
  await page.locator("#new-untitled").click();
  await type(page, "typed from scratch");
  const lang = await otherLanguage(page, await currentLanguage(page));
  await page.evaluate((v) => window.pickLanguage(v), lang);
  await expect(dialog(page)).toBeVisible({ timeout: 30 * 1000 });
  await expect(dialog(page).locator("h2")).toHaveText("Save the changes to untitled?");
  await dialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator(".notice", { hasText: "the question was not loaded" })).toBeVisible();
  expect(await editorText(page)).toBe("typed from scratch");
  // Don't save: the question replaces it
  const back = await otherLanguage(page, lang);
  await page.evaluate((v) => window.pickLanguage(v), back);
  await expect(dialog(page)).toBeVisible({ timeout: 30 * 1000 });
  await dialog(page).getByRole("button", { name: "Don't save" }).click();
  await expect.poll(() => editorText(page), { timeout: 30 * 1000 }).toContain("Two Sum " + stamp);
});

test("a solution written on top of a question is not asked about when the language changes", async ({ page }) => {
  await practice(page);
  await openFile(page, 3);
  await page.locator("#new-untitled").click(); // a question comes only into an untitled editor
  const first = await otherLanguage(page, await currentLanguage(page));
  await page.evaluate((v) => window.pickLanguage(v), first);
  await expect.poll(() => editorText(page), { timeout: 30 * 1000 }).toContain("Two Sum " + stamp);
  await page.evaluate(() => { const e = window.editor.env.editor; e.setValue(e.getValue() + "\nmy solution", -1); });
  const second = await otherLanguage(page, first);
  await page.evaluate((v) => window.pickLanguage(v), second);
  await expect.poll(() => editorText(page), { timeout: 30 * 1000 }).toContain("Two Sum " + stamp);
  await expect(dialog(page)).toBeHidden();
});
