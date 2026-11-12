// Hide and show the terminal (src/js/src/page/02-layout.js: HideTerminal, ShowTerminal, ToggleTerminal): the Hide button
// in the terminal bar gives the editor the whole window, a slim bar shows it again, Ctrl+` does both, Run brings the
// terminal back, and the choice is kept for the next visit. Designed in docs/preview-pane-design.md (board 2).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const terminal = (page) => page.locator("#terminal-div");
const bar = (page) => page.locator("#term-collapsed");

test.describe("terminal hide and show", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    // wait for the terminal to be connected and to have the focus: it takes it a little after the page loads
    await waitForTerminalReady(page);
  });

  test("Hide gives the editor the window, and Show brings the terminal back", async ({ page }) => {
    await expect(terminal(page)).toBeVisible();
    await expect(bar(page)).toBeHidden();

    await page.locator("#hide-terminal-button").click();
    await expect(terminal(page)).toBeHidden();
    await expect(bar(page)).toBeVisible();
    await expect(page.locator("#ide")).toBeVisible();

    await page.locator("#show-terminal-button").click();
    await expect(terminal(page)).toBeVisible();
    await expect(bar(page)).toBeHidden();
    await expect(page.locator("#ide")).toBeVisible();
  });

  test("Ctrl+` toggles it", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+Backquote");
    await expect(terminal(page)).toBeHidden();
    await expect(bar(page)).toBeVisible();
    await page.keyboard.press("ControlOrMeta+Backquote");
    await expect(terminal(page)).toBeVisible();
  });

  test("Run brings a hidden terminal back", async ({ page }) => {
    await page.locator("#hide-terminal-button").click();
    await expect(terminal(page)).toBeHidden();
    await page.locator("#play-button").click();
    await expect(terminal(page)).toBeVisible();
    await expect(bar(page)).toBeHidden();
  });

  test("the choice is kept for the next visit", async ({ page }) => {
    await page.locator("#hide-terminal-button").click();
    await expect(terminal(page)).toBeHidden();
    await page.reload();
    await expect(bar(page)).toBeVisible();
    await expect(terminal(page)).toBeHidden();
    await page.locator("#show-terminal-button").click();
    await page.reload();
    await expect(terminal(page)).toBeVisible();
    await expect(bar(page)).toBeHidden();
  });

  test("hiding the editor while the terminal is hidden brings the terminal back", async ({ page }) => {
    await page.locator("#hide-terminal-button").click();
    await expect(terminal(page)).toBeHidden();
    await page.locator("#editor-button").click();
    await expect(terminal(page)).toBeVisible();
    await expect(page.locator("#ide")).toBeHidden();
    await expect(bar(page)).toBeHidden();
  });
});
