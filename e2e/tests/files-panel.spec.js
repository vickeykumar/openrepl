// The Files panel on first sight, and the colors of changed and new files (page/26-file-marks.js, lib-filemarks.mjs).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.FileMarks && window.EditorTabs);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

const anchor = (page, name) => page.locator("#file-browser .jstree-anchor", { hasText: name }).first();
const make = async (page, rel, kind) => {
  const made = await page.evaluate(([r, k]) => window.FileBrowser.create(r, k), [rel, kind]);
  expect(made.ok, `${rel}: ${JSON.stringify(made)}`).toBe(true);
};

test("the workspace folder is open when the page loads", async ({ page }) => {
  await ready(page);
  await make(page, `open${stamp}.txt`, "file");
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  const root = page.locator("#file-browser li.jstree-open").first();
  await expect(root).toBeVisible();
  await expect(anchor(page, `open${stamp}.txt`)).toBeVisible(); // a file of the workspace, with no click on the folder
});

test("the Files panel starts open on a window wider than 800 px, a saved choice wins, and a phone has it in a drawer", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await page.goto("/");
  await expect(page.locator("body")).not.toHaveClass(/files-collapsed/);
  // a choice already made wins over the width
  await page.evaluate(() => localStorage.setItem("files-panel", "closed"));
  await page.reload();
  await expect(page.locator("body")).toHaveClass(/files-collapsed/);
  // up to 800 px the page is in its phone layout, where the files are in a drawer (13-mobile.js)
  await page.evaluate(() => localStorage.removeItem("files-panel"));
  await page.setViewportSize({ width: 700, height: 800 });
  await page.reload();
  await expect(page.locator("body")).toHaveClass(/is-mobile/);
  await expect(page.locator("body")).toHaveClass(/files-collapsed/);
});

test("a new file is green with U, a saved one amber with M, unsaved edits add a dot, and the folders above show it", async ({ page }) => {
  await ready(page);
  const dir = `marks${stamp}`;
  const file = `a${stamp}.txt`;
  await make(page, dir, "folder");
  await make(page, `${dir}/${file}`, "file");
  // created in this session: new
  await expect(anchor(page, file)).toHaveClass(/fm-new/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "U");
  // and it is drawn in the color of the theme: the IDE theme colors every row with a longer selector
  const green = await page.evaluate((name) => {
    const probe = document.createElement("span");
    probe.style.color = "var(--fm-new)";
    document.body.appendChild(probe);
    const expected = getComputedStyle(probe).color;
    probe.remove();
    const row = [...document.querySelectorAll("#file-browser .jstree-anchor")].find((a) => a.textContent.includes(name));
    return { expected, row: getComputedStyle(row).color };
  }, file);
  expect(green.row).toBe(green.expected);
  await expect(anchor(page, dir)).toHaveClass(/fm-new/); // the folder above it
  // after a reload the marks are gone, and a save makes the file modified
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.FileMarks);
  await expect(anchor(page, dir)).not.toHaveClass(/fm-new|fm-modified/);
  await page.evaluate((d) => { const t = $("#file-browser").jstree(true); const id = t.get_node("#").children[0] + "/" + d; t.open_node(id); }, dir);
  await anchor(page, file).click();
  await expect(page.locator("#editor-tabs .etab", { hasText: file })).toHaveAttribute("aria-selected", "true");
  // typed, not saved: a dot
  await page.evaluate(() => window.editor.env.editor.setValue("changed text", -1));
  await expect(anchor(page, file)).toHaveClass(/fm-unsaved/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "●");
  await expect(page.locator("#editor-tabs .etab", { hasText: file }).locator(".etab__dot")).toBeVisible();
  // saved: modified (amber, M), and the tab name has the same color
  await page.evaluate(() => window.SaveSelectedNodeToFile(window.editor.env.filename));
  await expect(anchor(page, file)).toHaveClass(/fm-modified/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "M");
  await expect(anchor(page, file)).not.toHaveClass(/fm-unsaved/);
  await expect(page.locator("#editor-tabs .etab.etab--modified", { hasText: file })).toBeVisible();
  // the name is really drawn in the color of the theme: a probe with the same variable is the reference
  const colors = await page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.color = "var(--fm-modified)";
    document.querySelector("#file-browser").appendChild(probe);
    const expected = getComputedStyle(probe).color;
    probe.remove();
    const name = document.querySelector("#editor-tabs .etab--modified .etab__name");
    const row = document.querySelector("#file-browser .jstree-anchor.fm-modified");
    const plain = document.querySelector("#file-browser .jstree-anchor:not(.fm-modified):not(.fm-new)");
    return { expected, tab: getComputedStyle(name).color, row: getComputedStyle(row).color, plain: plain ? getComputedStyle(plain).color : "" };
  });
  expect(colors.expected).not.toBe("");
  expect(colors.row).toBe(colors.expected);
  expect(colors.tab).toBe(colors.expected);
  expect(colors.plain).not.toBe(colors.expected); // an unmarked row keeps the ordinary color
});

test("Clear change marks removes the colors", async ({ page }) => {
  await ready(page);
  const file = `clear${stamp}.txt`;
  await make(page, file, "file");
  await expect(anchor(page, file)).toHaveClass(/fm-new/);
  await page.keyboard.press("ControlOrMeta+Shift+P");
  await page.locator("#palette .palette__item", { hasText: "Clear change marks" }).click();
  await expect(anchor(page, file)).not.toHaveClass(/fm-new/);
  await expect(anchor(page, file)).not.toHaveAttribute("data-fm", /.+/);
});
