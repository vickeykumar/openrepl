// The phone layout (width up to 800 px): Maximize in the app bar, an app bar of two rows with the menu behind "more", and Genie
// as the third tab of Editor | Terminal | Genie (the panel is in the pane, not floating). And the desktop as it was.
// (js/src/page/13-mobile.js, 02-layout.js, ui-refresh.css, chat-widget/src/index.ts)
//
// GENIE_LOCAL_ASSETS=1 serves the page, its style and the two scripts from this checkout instead of the ones inside the server under
// test, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..", "..", "src");
const local = (file) => fs.readFileSync(path.join(root, file));

async function localAssets(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  const js = (file) => (route) => route.fulfill({ contentType: "application/javascript", body: local(file) });
  await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), js("resources/chat-widget/dist/index.umd.js"));
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), js("js/dist/scribbler.js"));
  await page.route((u) => u.pathname.endsWith("/js/model-choice.js"), js("resources/js/model-choice.js"));
  await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (route) => route.fulfill({ contentType: "text/css", body: local("resources/css/ui-refresh.css") }));
  // the page is a template: the server's own answer gives the values, this checkout's file gives the markup (only with
  // GENIE_LOCAL_PAGE=1, for a server whose image is older than this checkout)
  if (!process.env.GENIE_LOCAL_PAGE) return;
  await page.route((u) => u.pathname === "/", async (route) => {
    if (route.request().resourceType() !== "document") return route.continue();
    const res = await route.fetch();
    const served = await res.text();
    const client = (served.match(/window\.OPENREPL_PAGE = (.*?);<\/script>/) || [])[1] || "{}";
    const html = local("resources/index.html").toString("utf8").replace("{{.Page.Client}}", client).replace(/\{\{\.Page\.[A-Za-z]+\}\}/g, "");
    route.fulfill({ response: res, contentType: "text/html", body: html });
  });
}

async function ready(page) {
  await localAssets(page);
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && window.FileBrowser && window.FileBrowser.ready());
  await expect(page.locator("#loader")).toBeHidden();
  // (the layout does not need a connected terminal: a REPL that is slow to start must not fail these)
  await page.waitForFunction(() => window.setMobileView && document.getElementById("mobile-more") !== null);
}

test.describe("a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test("Maximize is in the app bar, fills the screen, and the back button restores the page", async ({ page }) => {
    await ready(page);
    const max = page.locator("#togglescreen-button");
    await expect(max).toBeVisible();
    const box = await max.boundingBox();
    const lang = await page.locator("#optionlist").locator("xpath=ancestor::*[contains(@class,'select-wrap')][1]").boundingBox();
    expect(Math.abs(box.y - lang.y)).toBeLessThan(30); // the same row as the language
    // (the terminal and the page behind are drawn by now: the page has a block wider than the screen, which must not widen the
    // page when Maximize stops it from scrolling)
    await page.waitForTimeout(3000);
    await max.tap();
    const shell = page.locator("#ide-shell");
    await expect(shell).toHaveClass(/is-maximized/);
    await page.waitForTimeout(600);
    expect(await page.evaluate(() => innerWidth)).toBe(390);
    const s = await shell.boundingBox();
    expect(s.x).toBe(0);
    expect(s.y).toBe(0);
    expect(Math.round(s.width)).toBe(390);
    expect(Math.round(s.height)).toBe(844);
    // nothing of the page is over it: what is at the top of the screen belongs to the workspace
    expect(await page.evaluate(() => !!document.elementFromPoint(195, 40).closest("#ide-shell"))).toBe(true);
    await expect(max).toHaveAttribute("aria-pressed", "true");
    // the phone's back button leaves it
    await page.goBack();
    await expect(shell).not.toHaveClass(/is-maximized/);
    await expect(max).toHaveAttribute("aria-pressed", "false");
    // and the button does the same, without leaving history entries behind
    await max.tap();
    await expect(shell).toHaveClass(/is-maximized/);
    await max.tap();
    await expect(shell).not.toHaveClass(/is-maximized/);
  });

  test("the app bar has two rows: Fork, Share, Apps and the commands are behind the more button", async ({ page }) => {
    await ready(page);
    for (const id of ["#genie-button", "#fork-widget", "#share-btn-1", "#palette-button", "#appstore-button"]) await expect(page.locator(id)).toBeHidden();
    const more = page.locator("#mobile-more");
    await expect(more).toBeVisible();
    // two rows: everything of the bar is within 2 rows of 44 px and the gaps
    const bar = await page.locator("#optionMenu").boundingBox();
    expect(bar.height).toBeLessThan(130);
    const menu = page.locator("#mobile-more-menu");
    await expect(menu).toBeHidden();
    await more.tap();
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem")).toHaveCount(4);
    await menu.getByRole("menuitem", { name: "Share this session" }).tap();
    await expect(menu).toBeHidden();
    await expect(page.locator("#myDropdown")).toHaveClass(/show/);
  });

  test("Genie is the third tab: the panel is in the pane, always there, and the other tabs leave it running", async ({ page }) => {
    await ready(page);
    const tab = page.locator("#mobile-view-genie");
    await expect(tab).toBeVisible();
    await expect(page.locator(".mobile-view")).toHaveCount(4); // Editor, Terminal, Genie, and Preview (hidden)
    await tab.tap();
    await expect(tab).toHaveAttribute("aria-selected", "true");
    const panel = page.locator("#genie-dock #chat-widget__container");
    await expect(panel).toBeVisible();
    await expect(panel).toHaveClass(/is-docked/);
    // it fills the pane, which is as wide as the screen; no close or pin, nothing floats
    const pane = await page.locator("#genie-dock").boundingBox();
    const p = await panel.boundingBox();
    expect(Math.round(pane.width)).toBeGreaterThanOrEqual(388);
    expect(Math.abs(p.width - pane.width)).toBeLessThan(2);
    expect(Math.abs(p.height - pane.height)).toBeLessThan(2);
    await expect(page.locator("#chat-widget__close")).toBeHidden();
    await expect(page.locator("#chat-widget__pin")).toBeHidden();
    await expect(page.locator("#ide-split")).toBeHidden();
    // the input is big enough not to make the browser zoom
    expect(await page.locator("#chat-widget__input").evaluate((e) => parseFloat(getComputedStyle(e).fontSize))).toBeGreaterThanOrEqual(16);
    // to the Editor and back: the panel stays open and is the same one
    await page.locator("#mobile-view-editor").tap();
    await expect(panel).toBeHidden();
    await expect(page.locator("body")).toHaveClass(/genie-open/);
    await expect(page.locator("#ide")).toBeVisible();
    await tab.tap();
    await expect(panel).toBeVisible();
  });
});

test.describe("a phone, keyboard and chat menu", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test("a tap on the chat that is shown opens its menu, with All chats first", async ({ page }) => {
    await ready(page);
    await page.locator("#mobile-view-genie").tap();
    const tab = page.locator("#genie-dock .cw-tab.is-active");
    await expect(tab).toBeVisible();
    await tab.tap();
    const menu = page.locator(".cw-tabmenu");
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem").first()).toHaveText("All chats…");
    await expect(menu.getByRole("menuitem", { name: "Rename" })).toBeVisible();
    const row = await menu.getByRole("menuitem", { name: "Rename" }).boundingBox();
    expect(row.height).toBeGreaterThanOrEqual(40); // a row for a thumb
    // All chats opens the list
    await menu.getByRole("menuitem", { name: "All chats…" }).tap();
    await expect(page.locator(".cw-tabmenu--list")).toBeVisible();
    await expect(page.getByRole("searchbox", { name: "Search all chats" })).toBeVisible();
  });

  // The keyboard is simulated by making the screen shorter while a text box has the focus (the browser of a phone shrinks the
  // visible part the same way).
  test("with the keyboard up the workspace is the visible screen: the keys row and the cursor line are above it, and nothing scrolls", async ({ page }) => {
    await ready(page);
    await page.waitForTimeout(3000); // (the terminal has connected: a late connect would take the focus back)
    await page.locator("#mobile-view-terminal").tap();
    await page.locator("#terminal textarea").first().focus();
    await page.setViewportSize({ width: 390, height: 480 });
    await expect(page.locator("html")).toHaveClass(/keyboard-open/);
    const shell = await page.locator("#ide-shell").boundingBox();
    expect(Math.round(shell.y)).toBe(0);
    expect(Math.round(shell.height)).toBe(480);
    const keys = await page.locator("#extra-keys").boundingBox();
    expect(keys.y + keys.height).toBeLessThanOrEqual(480);
    // the page behind does not move when the user drags or scrolls
    const y0 = await page.evaluate(() => window.scrollY);
    await page.mouse.move(195, 300);
    await page.mouse.wheel(0, 400);
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => window.scrollY)).toBe(y0);
    // the keyboard goes away: the page is as it was
    await page.locator("#terminal textarea").first().blur();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator("html")).not.toHaveClass(/keyboard-open/);
  });

  test("maximized, with the keyboard up: the workspace is held at the top and the page does not scroll or pan", async ({ page }) => {
    await ready(page);
    await page.waitForTimeout(3000); // (the terminal has connected: a late connect would take the focus back)
    await page.locator("#togglescreen-button").tap();
    await expect(page.locator("#ide-shell")).toHaveClass(/is-maximized/);
    await page.locator("#mobile-view-terminal").tap();
    await page.locator("#terminal textarea").first().focus();
    await page.setViewportSize({ width: 390, height: 480 });
    await expect(page.locator("html")).toHaveClass(/keyboard-open/);
    const shell = await page.locator("#ide-shell").boundingBox();
    expect(Math.round(shell.y)).toBe(0);
    expect(Math.round(shell.height)).toBe(480);
    const y0 = await page.evaluate(() => window.scrollY);
    await page.mouse.move(195, 300);
    await page.mouse.wheel(0, 400);
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => window.scrollY)).toBe(y0);
    expect(await page.evaluate(() => visualViewport.offsetTop)).toBe(0);
  });
});

test.describe("a desktop", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("has none of the phone parts: no more button, no Genie tab, a floating panel, and Maximize as before", async ({ page }) => {
    await ready(page);
    await expect(page.locator("#mobile-more")).toBeHidden();
    await expect(page.locator("#mobile-view-genie")).toBeHidden();
    await expect(page.locator("#fork-widget")).toBeVisible();
    await expect(page.locator("#genie-button")).toBeVisible();
    await page.locator("#genie-button").click();
    const panel = page.locator("#chat-widget__container");
    await expect(panel).toBeVisible();
    await expect(page.locator("#chat-widget__close")).toBeVisible();
    expect(await page.locator("body").evaluate((b) => b.classList.contains("mobile-genie"))).toBe(false);
    await page.locator("#chat-widget__close").click();
    // Maximize: the workspace fills the window, and the history is not touched
    const before = await page.evaluate(() => history.length);
    await page.locator("#togglescreen-button").click();
    await expect(page.locator("#ide-shell")).toHaveClass(/is-maximized/);
    expect(await page.evaluate(() => history.length)).toBe(before);
    await page.locator("#togglescreen-button").click();
    await expect(page.locator("#ide-shell")).not.toHaveClass(/is-maximized/);
  });
});
