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

// The right corner of the terminal bar (docs: the canvas "Terminal bar: Hide, Reconnect and close"): Reconnect is an icon, Hide is the X
// in the corner, and a terminal whose connection is lost lights the icon. GENIE_LOCAL_ASSETS=1 takes the bar's markup and the style from
// this checkout, for a run before an image is built.
test.describe("terminal bar corner", () => {
  test.beforeEach(async ({ page }) => {
    let mineBlock = null;
    if (process.env.GENIE_LOCAL_ASSETS) {
      const fs = require("fs");
      const path = require("path");
      const root = path.join(__dirname, "..", "..", "src", "resources");
      mineBlock = fs.readFileSync(path.join(root, "index.html"), "utf8").match(/<div class="terminal-tabs__actions">([\s\S]*?<\/button>)\s*<\/div>/)[1];
      await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(root, "css/ui-refresh.css")) }));
    }
    await page.goto("/");
    await waitForTerminalReady(page);
    // the bar's buttons from this checkout, put in place of the ones of the server under test
    if (mineBlock) await page.evaluate((html) => { document.querySelector(".terminal-tabs__actions").innerHTML = html; }, mineBlock);
  });

  test("Reconnect is an icon with a name, and Hide is the X at the right end", async ({ page }) => {
    const redo = page.locator("#redo-button");
    const hide = page.locator("#hide-terminal-button");
    await expect(redo).toHaveAttribute("aria-label", /Reconnect/);
    await expect(hide).toHaveAttribute("aria-label", "Hide the terminal");
    await expect(redo).toHaveText("");
    await expect(hide).toHaveText("");
    const [r, h] = [await redo.boundingBox(), await hide.boundingBox()];
    expect(h.x).toBeGreaterThan(r.x); // the X is last, in the corner
    const tabs = await page.locator("#terminal-tabs").boundingBox();
    expect(tabs.x + tabs.width - (h.x + h.width)).toBeLessThan(16);
    await hide.click();
    await expect(page.locator("#term-collapsed")).toBeVisible();
  });

  test("the Reconnect icon lights up when the connection is lost", async ({ page }) => {
    const redo = page.locator("#redo-button");
    await expect.poll(() => redo.evaluate((e) => getComputedStyle(e).boxShadow)).toBe("none");
    await page.evaluate(() => document.querySelector("#terminal-tabs .tab.active").setAttribute("data-state", "closed"));
    await expect.poll(() => redo.evaluate((e) => getComputedStyle(e).boxShadow)).not.toBe("none");
  });
});
