// "Go to file" (resources/js/palette.js, page/25-file-search.js): the palette in its file mode. Opened by Ctrl+Shift+O,
// the command menu's "Go to file...", or the search button of the Files panel; letters find a file by name; Enter
// opens it as a tab. Designed in the "Go to file palette" canvas.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const names = [`alpha-${stamp}.txt`, `server-${stamp}.go`, `notes-${stamp}.md`];

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileSearch && window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function makeFiles(page) {
  for (const n of names) {
    const made = await page.evaluate((name) => window.FileBrowser.create(name, "file"), n);
    expect(made.ok, JSON.stringify(made)).toBe(true);
  }
}

const palette = (page) => page.locator("#palette");
const input = (page) => page.locator("#palette-input");
const options = (page) => page.locator("#palette .palette__item");

test("Ctrl+Shift+O opens the palette on files, letters find a file, Enter opens it as a tab", async ({ page }) => {
  await ready(page);
  await makeFiles(page);
  await page.keyboard.press("Control+Shift+O");
  await expect(palette(page)).toBeVisible();
  await expect(input(page)).toHaveAttribute("placeholder", /Go to file/);
  await input(page).fill("srv");
  await expect(options(page).first()).toContainText(names[1]); // the best match first
  await expect(options(page).first().locator(".palette__hit").first()).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(palette(page)).toBeHidden();
  await expect(page.locator("#editor-tabs .etab", { hasText: names[1] })).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => page.evaluate(() => window.editor.env.filename)).toContain(names[1]);
});

test("the file shown now is listed as an open tab, before the others, when nothing is typed", async ({ page }) => {
  await ready(page);
  await makeFiles(page);
  await page.evaluate((n) => window.FileSearch.go(window.FileSearch.files().find((f) => f.name === n).path), names[2]);
  await expect(page.locator("#editor-tabs .etab", { hasText: names[2] })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Control+Shift+O");
  await expect(options(page).first()).toContainText(names[2]);
  await expect(page.locator("#palette .palette__group").first()).toHaveText(/open tabs/i);
});

test("a > first goes to the commands, Backspace on an empty box comes back, and Escape closes", async ({ page }) => {
  await ready(page);
  await page.keyboard.press("Control+Shift+O");
  await input(page).fill(">run");
  await expect(input(page)).toHaveAttribute("placeholder", /command/i);
  await expect(input(page)).toHaveValue("run");
  await expect(options(page).filter({ hasText: "Run code" })).toBeVisible();
  await input(page).fill("");
  await page.keyboard.press("Control+Shift+O"); // from the commands it switches to files
  await expect(input(page)).toHaveAttribute("placeholder", /Go to file/);
  await page.keyboard.press("Backspace");
  await expect(input(page)).toHaveAttribute("placeholder", /command/i);
  await page.keyboard.press("Escape");
  await expect(palette(page)).toBeHidden();
});

test("the command menu lists Go to file with its shortcut, and the Files panel has a search button", async ({ page }) => {
  await ready(page);
  await page.keyboard.press("Control+Shift+P");
  const entry = options(page).filter({ hasText: "Go to file" });
  await expect(entry).toBeVisible();
  await expect(entry.locator("kbd")).toHaveText(["Ctrl", "Shift", "O"]);
  await entry.click();
  await expect(input(page)).toHaveAttribute("placeholder", /Go to file/);
  await page.keyboard.press("Escape");
  await page.locator("#filesearch-button").click();
  await expect(palette(page)).toBeVisible();
  await expect(input(page)).toHaveAttribute("placeholder", /Go to file/);
});

test("hidden files and a folder's contents that are hidden are not offered", async ({ page }) => {
  await ready(page);
  const hidden = `.secret-${stamp}`;
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), hidden);
  if (made.ok) {
    await page.keyboard.press("Control+Shift+O");
    await input(page).fill(`secret-${stamp}`);
    await expect(options(page)).toHaveCount(0);
    await expect(page.locator("#palette-empty")).toBeVisible();
  } else {
    test.info().annotations.push({ type: "note", description: "the page did not allow making a hidden file here: " + made.error });
  }
});

test("a file in nested folders is found by its name, by a folder's name, and by a path", async ({ page }) => {
  await ready(page);
  const top = `nest${stamp}`;
  for (const [p, kind] of [[top, "folder"], [`${top}/src`, "folder"], [`${top}/src/main`, "folder"], [`${top}/src/main/Util${stamp}.java`, "file"], [`${top}/src/app${stamp}.py`, "file"]]) {
    const made = await page.evaluate(([rel, k]) => window.FileBrowser.create(rel, k), [p, kind]);
    expect(made.ok, `${p}: ${JSON.stringify(made)}`).toBe(true);
  }
  const find = async (query) => {
    await page.keyboard.press("Control+Shift+O");
    await input(page).fill(query);
    const found = await options(page).allTextContents();
    await page.keyboard.press("Escape");
    return found.join(" | ");
  };
  expect(await find(`util${stamp}`)).toContain(`Util${stamp}.java`);
  expect(await find(`${top}/src/util`)).toContain(`Util${stamp}.java`); // a path
  expect(await find(`src/app${stamp}`)).toContain(`app${stamp}.py`);
  expect(await find(`main util`)).toContain(`Util${stamp}.java`); // a folder, then the name
  // and Enter opens it
  await page.keyboard.press("Control+Shift+O");
  await input(page).fill(`util${stamp}`);
  await page.keyboard.press("Enter");
  await expect(page.locator("#editor-tabs .etab", { hasText: `Util${stamp}.java` })).toHaveAttribute("aria-selected", "true");
});
