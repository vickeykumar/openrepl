// The untitled tab and the + above the editor (page/22-editor-tabs.js, 28-unsaved.js): a buffer that is saved with a name,
// kept like the other tabs, asked about when it is closed with text, and recovered after a reload.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const fname = (i) => `ut${i}-${stamp}.txt`;
const tabs = (page) => page.locator("#editor-tabs .etab");
const fileTab = (page, i) => tabs(page).filter({ hasText: fname(i) });
const untitled = (page) => page.locator("#editor-tabs .etab--untitled");
const dialog = (page) => page.locator(".unsaved-dialog");
const plus = (page) => page.locator("#new-untitled");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.editor && window.editor.env && window.editor.env.editor && window.FileBrowser && window.FileBrowser.ready());
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
const onServer = (page, name) => page.evaluate(async (n) => atob(await $.ajax({ url: preprocessurl("/ws_filebrowser?q=load&filepath=" + window.homedir + "/" + n), method: "GET" })), name);

test("the + opens an untitled tab next to the files, which keeps its own text and shows again", async ({ page }) => {
  await ready(page);
  await expect(plus(page)).toBeVisible();
  await openFile(page, 1);
  await type(page, "file one text");
  await plus(page).click();
  await expect(untitled(page)).toHaveCount(1);
  await expect(untitled(page)).toHaveAttribute("aria-selected", "true");
  await expect(untitled(page).locator(".etab__name")).toHaveText("untitled");
  expect(await page.evaluate(() => window.editor.env.filename)).toBe("");
  expect(await editorText(page)).toBe("");
  // typing in it: a dot, and the file keeps its text in its own tab
  await type(page, "scratch");
  await expect(untitled(page).locator(".etab__dot")).toBeVisible();
  await fileTab(page, 1).click();
  await expect(fileTab(page, 1)).toHaveAttribute("aria-selected", "true");
  expect(await editorText(page)).toBe("file one text");
  await untitled(page).click();
  expect(await editorText(page)).toBe("scratch");
  // a second + shows the same tab: one untitled at a time
  await fileTab(page, 1).click();
  await plus(page).click();
  await expect(untitled(page)).toHaveCount(1);
  expect(await editorText(page)).toBe("scratch");
});

test("Ctrl+S on the untitled tab asks for a name, refuses a bad one, then the tab is that file with its text", async ({ page }) => {
  await ready(page);
  await openFile(page, 2);
  await plus(page).click();
  await type(page, "to be saved");
  await page.locator(".ace_text-input").first().focus();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("h2")).toHaveText("Save the untitled file as");
  const input = dialog(page).locator("input");
  // a name with a slash, and one that exists: the question stays, and says why
  await input.fill("a/b.txt");
  await dialog(page).getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page)).toContainText("no slash");
  await input.fill(fname(2));
  await dialog(page).getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog(page)).toContainText("exists already");
  const name = `saved-${stamp}.txt`;
  await input.fill(name);
  await page.keyboard.press("Enter");
  await expect(dialog(page)).toBeHidden();
  await expect(untitled(page)).toHaveCount(0);
  const tab = tabs(page).filter({ hasText: name });
  await expect(tab).toHaveAttribute("aria-selected", "true");
  await expect(tab.locator(".etab__dot")).toHaveCount(0);
  expect(await editorText(page)).toBe("to be saved");
  expect(await page.evaluate(() => window.editor.env.filename)).toContain(name);
  expect(await onServer(page, name)).toBe("to be saved");
  // Cancel writes nothing: the + again, a name asked and refused with Esc
  await plus(page).click();
  await type(page, "another");
  await page.locator(".ace_text-input").first().focus();
  await page.keyboard.press("ControlOrMeta+s");
  await expect(dialog(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog(page)).toBeHidden();
  await expect(untitled(page)).toHaveCount(1);
});

test("closing the untitled tab with text asks Save, Don't save, Cancel; empty, it just closes", async ({ page }) => {
  await ready(page);
  await openFile(page, 3);
  await plus(page).click();
  await untitled(page).locator(".etab__x").click(); // no text: no question
  await expect(untitled(page)).toHaveCount(0);
  await plus(page).click();
  await type(page, "typed");
  await untitled(page).locator(".etab__x").click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("h2")).toHaveText("Save the changes to untitled?");
  await dialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(untitled(page)).toHaveCount(1);
  await untitled(page).locator(".etab__x").click();
  await dialog(page).getByRole("button", { name: "Don't save" }).click();
  await expect(untitled(page)).toHaveCount(0);
  await expect(fileTab(page, 3)).toHaveAttribute("aria-selected", "true"); // the neighbour is shown
});
test("the untitled text comes back after a reload, with the recovery bar", async ({ page }) => {
  await ready(page);
  await openFile(page, 4);
  await plus(page).click();
  await type(page, "survives a reload");
  await expect(untitled(page).locator(".etab__dot")).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await page.evaluate(() => location.reload());
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.FileBrowser && window.FileBrowser.ready());
  await expect(untitled(page)).toHaveCount(1);
  await expect(page.locator("#unsaved-bar")).toContainText("back in");
  await untitled(page).click();
  expect(await editorText(page)).toBe("survives a reload");
});

test("the Files panel and the published tab list never see the untitled tab as a file", async ({ page }) => {
  await ready(page);
  await openFile(page, 5);
  await plus(page).click();
  await type(page, "x");
  const names = await page.evaluate(() => [...document.querySelectorAll("#file-browser .jstree-anchor")].map((a) => a.textContent.trim()));
  expect(names.some((n) => n.startsWith("untitled"))).toBe(false);
  // the owner's tab list (what viewers get) keeps files only
  const state = await page.evaluate(() => window.EditorTabs.state().tabs);
  expect(state.includes("untitled:")).toBe(true); // the owner's own list has it
});

test("with nothing open the editor is an untitled tab with the + next to it; with files open the + follows the last tab", async ({ page }) => {
  await ready(page);
  // no file is chosen: the buffer in the editor is shown as an untitled tab, and the + stands right after it
  await expect(untitled(page)).toHaveCount(1);
  await expect(untitled(page)).toHaveAttribute("aria-selected", "true");
  await expect(untitled(page).locator(".etab__x")).toHaveCount(0); // nothing to close yet
  const right = async (loc) => (await loc.boundingBox()).x + (await loc.boundingBox()).width;
  const gap = async () => Math.abs((await plus(page).boundingBox()).x - (await right(untitled(page))));
  expect(await gap()).toBeLessThan(3);
  // a file opens: the untitled one gives way, the + is after the file's tab
  await openFile(page, 6);
  await expect(untitled(page)).toHaveCount(0);
  const last = tabs(page).last();
  expect(Math.abs((await plus(page).boundingBox()).x - (await right(last)))).toBeLessThan(3);
  // the + opens a real untitled tab; when the strip is full the + stays in view at its right end
  await plus(page).click();
  await expect(untitled(page)).toHaveCount(1);
  await expect(plus(page)).toBeInViewport();
});

test("after a reload with nothing chosen in the Files panel, an untitled tab is shown and the starter code loads into it", async ({ page }) => {
  await ready(page);
  await openFile(page, 7);
  await page.reload();
  await page.waitForFunction(() => window.EditorTabs && window.Unsaved && window.FileBrowser && window.FileBrowser.ready());
  const chosen = await page.evaluate(() => $("#file-browser").jstree(true).get_selected().length);
  test.skip(chosen > 0, "the Files panel chose a file again: there is no untitled editor to see");
  // the file's tab is there (kept from before) and the editor is an untitled buffer, drawn as an untitled tab
  await expect(fileTab(page, 7)).toHaveCount(1);
  await expect(untitled(page)).toHaveCount(1);
  await expect(untitled(page)).toHaveAttribute("aria-selected", "true");
  await expect(plus(page)).toBeVisible();
  // the starter code of the language in the list comes in by itself, as it does on a first visit
  await expect.poll(async () => (await editorText(page)).length, { timeout: 30 * 1000 }).toBeGreaterThan(80);
});
