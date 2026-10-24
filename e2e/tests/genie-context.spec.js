// Add to Genie chat: files, folders, selected code and terminal text become chips above the
// message box (chat-widget/src/attach.ts, js/src/page/07-file-browser.js and 19-genie-actions.js).
// Nothing here sends a message: what Genie is told is covered by the widget's unit tests.
const { test, expect } = require("@playwright/test");

// the page, with the editor and the Genie widget ready
async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(
    () =>
      window.ChatWidget &&
      typeof window.ChatWidget.addContext === "function" &&
      window.editor &&
      window.editor.env &&
      window.editor.env.editor &&
      window.FileBrowser &&
      window.FileBrowser.ready()
  );
}

// a file in the guest's workspace, shown in the Files panel
async function makeFile(page, name) {
  const made = await page.evaluate(async (n) => {
    window.FileBrowser.reveal();
    return window.FileBrowser.create(n, "file");
  }, name);
  expect(made.ok, "the file was made: " + JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: name });
  await expect(anchor).toBeVisible();
  return anchor;
}

const chips = (page) => page.locator("#chat-widget__attached .cw-chip:not(.cw-chip--current)");

test("a file of the Files panel is added from its right-click menu", async ({ page }) => {
  await ready(page);
  const anchor = await makeFile(page, "notes.txt");
  await anchor.click({ button: "right" });
  await page.locator(".vakata-context a", { hasText: "Add to Genie chat" }).click();
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).toContainText("notes.txt");
  // the cross takes it off
  await chips(page).first().getByRole("button", { name: /Remove/ }).click();
  await expect(chips(page)).toHaveCount(0);
});

test("selected code is added with Ctrl+Shift+L, with its lines", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => {
    const ed = window.editor.env.editor;
    ed.setValue("x = 1\nprint(x)\n", -1);
    ed.selectAll();
    ed.focus();
  });
  await page.keyboard.press("Control+Shift+L");
  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).toContainText("1–2");
});

test("selected code is added from the editor's right-click menu", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => {
    const ed = window.editor.env.editor;
    ed.setValue("a = 1\nb = 2\n", -1);
    ed.selectAll();
  });
  await page.locator(".ace_content").click({ button: "right", position: { x: 40, y: 20 } });
  const item = page.locator("#genie-menu button", { hasText: "Add to Genie chat" });
  await expect(item).toBeVisible();
  await item.click();
  await expect(chips(page)).toHaveCount(1);
});

test("the same thing is added once, and the ninth chip is refused", async ({ page }) => {
  await ready(page);
  const result = await page.evaluate(() => {
    const add = (n) => window.ChatWidget.addContext({ kind: "terminal", path: "", name: "Terminal", text: "line " + n });
    const same = [add(1), add(1)];
    const more = [2, 3, 4, 5, 6, 7, 8, 9].map(add);
    return { same, more };
  });
  expect(result.same).toEqual([true, true]); // the second is the same chip: nothing to refuse
  expect(result.more.filter(Boolean).length).toBe(7); // 8 chips in all, the ninth is refused
  await expect(chips(page)).toHaveCount(8);
});

test("typing # lists the files, and choosing one adds it and takes the word out", async ({ page }) => {
  await ready(page);
  await makeFile(page, "report-draft.txt");
  await page.evaluate(() => window.ChatWidget.open());
  const input = page.locator("#chat-widget__input");
  await input.click();
  await page.keyboard.type("look at #report");
  const first = page.locator("#chat-widget__picker .cw-picker__item").first();
  await expect(first).toContainText("report-draft.txt");
  await page.keyboard.press("Enter");
  await expect(chips(page)).toHaveCount(1);
  await expect(chips(page).first()).toContainText("report-draft.txt");
  await expect(input).toHaveValue("look at ");
  await expect(page.locator("#chat-widget__picker")).toBeHidden();
});

test("the current file chip is a switch, and it is remembered", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => window.ChatWidget.open());
  const current = page.locator("#chat-widget__attached .cw-chip--current");
  await expect(current).toHaveAttribute("aria-pressed", "true");
  await current.click();
  await expect(current).toHaveAttribute("aria-pressed", "false");
  expect(await page.evaluate(() => localStorage.getItem("genie-read-editor"))).toBe("0");
  await expect(page.locator("#chat-widget__context")).toContainText("terminal only");
  await current.click();
  await expect(current).toHaveAttribute("aria-pressed", "true");
});
