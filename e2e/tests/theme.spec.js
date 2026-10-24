// The Light, Dark and System theme (src/resources/js/theme.js and the light-theme block at
// the end of css/ui-refresh.css). Light means the whole page is light, dark the whole page.
const { test, expect } = require("@playwright/test");
const { backgroundLuminance, setTheme } = require("./helpers");

const BANDS = ["#header-nav", ".hero", ".workspace", ".band--dark", ".site-footer"];

test.describe("theme choice", () => {
  test("a first visit follows the device: the choice is System", async ({ browser }) => {
    for (const scheme of ["light", "dark"]) {
      const context = await browser.newContext({ colorScheme: scheme });
      const page = await context.newPage();
      await page.goto("/");
      await expect(page.locator("html")).toHaveAttribute("data-theme-choice", "system");
      await expect(page.locator("html")).toHaveAttribute("data-theme", scheme);
      await context.close();
    }
  });

  test("on System the page follows the device while it is open", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  });

  test("the menu offers Light, Dark and System, and the choice is kept", async ({ page }) => {
    await page.goto("/");
    const html = page.locator("html");
    const button = page.locator("[data-theme-toggle]").first();
    await button.click();
    const menu = page.locator(".theme-menu");
    await expect(menu).toBeVisible();
    await expect(menu.locator("[data-theme-choice-item]")).toHaveCount(3);
    await expect(menu.locator('[data-theme-choice-item="system"]')).toHaveAttribute("aria-checked", "true");

    await menu.locator('[data-theme-choice-item="dark"]').click();
    await expect(html).toHaveAttribute("data-theme", "dark");
    await expect(html).toHaveAttribute("data-theme-choice", "dark");
    expect(await page.evaluate(() => localStorage.getItem("theme"))).toBe("dark");

    await page.reload();
    await expect(html).toHaveAttribute("data-theme", "dark");

    await page.locator("[data-theme-toggle]").first().click();
    await page.locator('.theme-menu [data-theme-choice-item="system"]').click();
    await expect(html).toHaveAttribute("data-theme-choice", "system");
    expect(await page.evaluate(() => localStorage.getItem("theme"))).toBeNull();
  });

  test("Escape closes the menu and gives the focus back to its button", async ({ page }) => {
    await page.goto("/");
    const button = page.locator("[data-theme-toggle]").first();
    await button.click();
    await expect(page.locator(".theme-menu")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator(".theme-menu")).toBeHidden();
    await expect(button).toBeFocused();
  });
});

test.describe("the whole page follows the theme", () => {
  test("light: the top, the bands and the footer are light", async ({ page }) => {
    await page.goto("/");
    await setTheme(page, "light");
    for (const sel of BANDS) {
      expect(await backgroundLuminance(page, sel), `${sel} in the light theme`).toBeGreaterThan(0.75);
    }
  });

  test("dark: the top, the bands and the footer are dark", async ({ page }) => {
    await page.goto("/");
    await setTheme(page, "dark");
    for (const sel of BANDS) {
      expect(await backgroundLuminance(page, sel), `${sel} in the dark theme`).toBeLessThan(0.25);
    }
  });

  test("the chosen language of \"Try a language\" can be seen in the light theme", async ({ page }) => {
    await page.goto("/");
    await setTheme(page, "light");
    const chosen = page.locator('.lang-chip[aria-pressed="true"]').first();
    await expect(chosen).toBeVisible();
    // the chosen chip has the accent colour, not the white of the other chips
    expect(await backgroundLuminance(page, '.lang-chip[aria-pressed="true"]')).toBeLessThan(0.7);
  });

  test("practice page: a light hero in the light theme", async ({ page }) => {
    await page.goto("/practice/dsa-questions");
    await setTheme(page, "light");
    expect(await backgroundLuminance(page, ".practice-hero")).toBeGreaterThan(0.75);
    expect(await backgroundLuminance(page, "#header-nav, .site-nav")).toBeGreaterThan(0.75);
  });

  test("blog and docs pages have a light footer in the light theme", async ({ page }) => {
    for (const path of ["/blog", "/doc.html"]) {
      await page.goto(path);
      await setTheme(page, "light");
      expect(await backgroundLuminance(page, ".footer"), `${path} footer`).toBeGreaterThan(0.75);
    }
  });
});
