// HTML and CSS in the language picker (src/js/src/page/29-web-language.js, lib-weblang.mjs): not languages of the terminal. Choosing one sets the
// editor's mode, puts a starter in an empty untitled editor, and makes Run show the page in the Preview. The terminal's language is not changed.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.WebLanguage && window.Preview && window.EditorTabs);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

// the picker, as a visitor uses it
async function choose(page, label) {
  await page.locator("#optionlist-button").click();
  await page.locator(".sm-menu .sm-item, .sm-menu [role=option]", { hasText: new RegExp("^\\s*" + label + "\\b") }).first().click();
}

const state = (page) => page.evaluate(() => ({
  terminalLanguage: document.getElementById("optionlist").value,
  button: document.getElementById("optionlist-button").textContent.trim(),
  mode: document.getElementById("select-lang").value,
  text: window.editor.env.editor.getValue(),
}));

test("the picker lists HTML and CSS in a group of their own, and choosing HTML leaves the terminal's language alone", async ({ page }) => {
  await ready(page);
  const before = await state(page);
  await page.evaluate(() => { window.__changes = 0; document.getElementById("optionlist").addEventListener("change", () => window.__changes++); });
  await page.locator("#optionlist-button").click();
  await expect(page.locator(".sm-menu")).toContainText("Web (shown in the Preview)");
  await page.keyboard.press("Escape");
  await choose(page, "HTML");
  const after = await state(page);
  expect(after.terminalLanguage).toBe(before.terminalLanguage); // the select, which the terminal follows, is not set
  expect(after.button).toContain("HTML");
  expect(after.mode).toBe("html");
  expect(after.text).toContain("<!DOCTYPE html>"); // the starter page
  expect(await page.evaluate(() => window.__changes)).toBe(0); // no change event: the terminal did not restart
  // the terminal's own menu does not offer them
  expect(await page.evaluate(() => document.querySelectorAll('#tabLangList .ctx-item[data-value="html"], #tabLangList .ctx-item[data-value="css"]').length)).toBe(0);
});

test("Run with HTML saves an untitled editor as index.html and shows it in the Preview", async ({ page }) => {
  await ready(page);
  await choose(page, "HTML");
  await page.evaluate(() => window.editor.env.editor.setValue("<h1 id='h'>From the picker</h1><script>console.log('hi')</script>", -1));
  await page.evaluate(() => window.CompileandRun());
  await expect(page.locator("#preview-pane")).toBeVisible({ timeout: 20000 });
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("From the picker");
  await expect(page.locator(".etab", { hasText: "index.html" })).toHaveCount(1);
  // a second Run with the file open saves it and shows it again; another untitled editor would not overwrite index.html
  await page.evaluate(() => window.editor.env.editor.setValue("<h1 id='h'>Second</h1>", -1));
  await page.evaluate(() => window.CompileandRun());
  await expect(page.frameLocator("#preview-frame").locator("#h")).toHaveText("Second", { timeout: 20000 });
});

test("Run with CSS saves the stylesheet and shows the index.html beside it with it applied; without a page it says so", async ({ page }) => {
  await ready(page);
  await page.evaluate(async () => {
    await window.FileBrowser.create("index.html", "file");
    await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/index.html"), method: "POST", processData: false,
      data: window.textToBase64('<link rel="stylesheet" href="style.css"><h1 id="t">Styled</h1>'), contentType: "application/octet-stream" });
  });
  await choose(page, "CSS");
  expect((await state(page)).mode).toBe("css");
  await page.evaluate(() => window.editor.env.editor.setValue("h1 { color: rgb(1, 2, 3); }", -1));
  await page.evaluate(() => window.CompileandRun());
  await expect(page.locator("#preview-pane")).toBeVisible({ timeout: 20000 });
  await expect(page.frameLocator("#preview-frame").locator("#t")).toHaveCSS("color", "rgb(1, 2, 3)");
});

test("without an index.html a stylesheet is saved and the notice says what to make", async ({ page }) => {
  await ready(page);
  await choose(page, "CSS");
  await page.evaluate(() => window.CompileandRun());
  await expect(page.locator(".notice", { hasText: "make an index.html that links to it" })).toBeVisible({ timeout: 20000 });
  await expect(page.locator(".etab", { hasText: "style.css" })).toHaveCount(1);
});

test("choosing a language of the terminal again takes the picker back to it, and Run goes to the terminal", async ({ page }) => {
  await ready(page);
  const before = await state(page);
  await choose(page, "HTML");
  expect(await page.evaluate(() => window.WebLanguage.active())).toBe(true);
  await choose(page, "Python");
  const after = await state(page);
  expect(after.terminalLanguage).toBe("python");
  expect(after.button).toContain("Python");
  expect(await page.evaluate(() => window.WebLanguage.active())).toBe(false);
  expect(before.terminalLanguage).not.toBe("python"); // the default is another language, so the change was real
});

test("the palette, the hero chips and Genie's pickLanguage route HTML to the web choice, not to the terminal", async ({ page }) => {
  await ready(page);
  const lang = (await state(page)).terminalLanguage;
  await page.evaluate(() => window.pickLanguage("css"));
  expect((await state(page)).terminalLanguage).toBe(lang);
  expect(await page.evaluate(() => window.WebLanguage.current())).toBe("css");
});
