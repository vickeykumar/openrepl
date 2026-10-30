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
