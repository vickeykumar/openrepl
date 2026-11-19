// The editor and terminal layout (src/js/src/page/02-layout.js): Side by side or Stacked is the visitor's
// choice and the next visit starts in it. Genie pinned beside the IDE stacks the editor by itself; that is not
// a choice and must not be kept.
const { test, expect } = require("@playwright/test");

const PAGE = "/c";

async function stacked(page) {
  return page.locator("#ide-split").evaluate((el) => el.classList.contains("is-vertical"));
}

test.describe("editor layout", () => {
  test("a first visit is side by side", async ({ page }) => {
    await page.goto(PAGE);
    await expect(page.locator("#rotate-button")).toBeVisible();
    expect(await stacked(page)).toBe(false);
  });

  test("the layout the visitor chose is there on the next visit", async ({ page }) => {
    await page.goto(PAGE);
    await page.locator("#rotate-button").click();
    await expect.poll(() => stacked(page)).toBe(true);

    await page.reload();
    await expect(page.locator("#rotate-button")).toBeVisible();
    expect(await stacked(page)).toBe(true);
    await expect(page.locator("#rotate-button-label")).toHaveText("Side by side");

    await page.locator("#rotate-button").click();
    await expect.poll(() => stacked(page)).toBe(false);
    await page.reload();
    await expect(page.locator("#rotate-button")).toBeVisible();
    expect(await stacked(page)).toBe(false);
  });

  test("Genie pinned beside the IDE stacks it but does not change what is kept", async ({ page }) => {
    await page.goto(PAGE);
    await expect(page.locator("#rotate-button")).toBeVisible();
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("genie-pin", { detail: { pinned: true } })));
    await expect.poll(() => stacked(page)).toBe(true);
    expect(await page.evaluate(() => localStorage.getItem("editor-layout"))).toBeNull();

    await page.reload();
    await expect(page.locator("#rotate-button")).toBeVisible();
    expect(await stacked(page)).toBe(false);
  });
});

// GENIE_LOCAL_ASSETS=1 serves the page script of this checkout, for a run before an image is built
async function localPageScript(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  const file = require("path").join(__dirname, "..", "..", "src", "js", "dist", "scribbler.js");
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (route) => route.fulfill({ contentType: "application/javascript", body: require("fs").readFileSync(file) }));
}

test.describe("double click on a bar", () => {
  test("an empty part of the top bar maximizes the workspace and a second double click restores it; a button keeps its own click", async ({ page }) => {
    await localPageScript(page);
    await page.goto(PAGE);
    await expect(page.locator("#rotate-button")).toBeVisible();
    const maxed = () => page.locator("body").evaluate((b) => b.classList.contains("ide-maximized"));
    expect(await maxed()).toBe(false);
    // the bar moves when the workspace is maximized: its empty part is measured again each time
    const dbl = async () => {
      const bar = await page.locator("#optionMenu").boundingBox();
      const spacer = await page.locator("#optionMenu .app-bar__spacer").boundingBox();
      await page.mouse.dblclick(spacer.x + spacer.width / 2, bar.y + bar.height / 2);
    };
    await dbl();
    await expect.poll(maxed).toBe(true);
    await dbl();
    await expect.poll(maxed).toBe(false);
    // a double click on the language list or a button does not toggle
    await page.locator("#rotate-button").dblclick();
    await page.waitForTimeout(300);
    expect(await maxed()).toBe(false);
  });

  test("an empty part of the terminal's tab bar hides the terminal, the strip it leaves shows it again; a tab keeps its own", async ({ page }) => {
    await localPageScript(page);
    await page.goto(PAGE);
    await expect(page.locator("#rotate-button")).toBeVisible();
    const termShown = () => page.locator("#terminal-div").evaluate((el) => getComputedStyle(el).display !== "none");
    expect(await termShown()).toBe(true);
    await page.locator("#terminal-tabs").scrollIntoViewIfNeeded();
    const bar = await page.locator("#terminal-tabs").boundingBox();
    await page.mouse.dblclick(bar.x + bar.width * 0.55, bar.y + bar.height / 2);
    await expect.poll(termShown).toBe(false);
    await expect(page.locator("#term-collapsed")).toBeVisible();
    await page.locator("#term-collapsed").scrollIntoViewIfNeeded();
    const strip = await page.locator("#term-collapsed").boundingBox();
    await page.mouse.dblclick(strip.x + strip.width * 0.55, strip.y + strip.height / 2);
    await expect.poll(termShown).toBe(true);
    await page.locator("#terminal-tabs .tab").first().dblclick();
    await page.waitForTimeout(300);
    expect(await termShown()).toBe(true);
  });
});
