// The home page, the IDE around the editor, the practice page and the legal pages.
const { test, expect } = require("@playwright/test");
const { backgroundLuminance } = require("./helpers");

test("the home page has its hero, the workspace and the contact form", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle(/OpenREPL/);
  await expect(page.locator("h1.hero__title")).toContainText("Share it");
  await expect(page.locator(".ide-shell")).toBeVisible();
  await expect(page.locator("#feedback-form")).toBeVisible();
  // the Send button waits for the reCAPTCHA box
  await expect(page.locator("#feedback-submit")).toBeDisabled();
});

test("the language and theme dropdowns show what the editor really uses", async ({ page }) => {
  await page.goto("/");
  await page.locator(".select-wrap--status .sm-btn").first().waitFor();
  const shown = await page.evaluate(() =>
    [...document.querySelectorAll(".editor-status select[data-select-menu]")].map((select) => {
      const button = select.parentElement.querySelector(".sm-btn");
      return {
        id: select.id,
        selected: select.options[select.selectedIndex] ? select.options[select.selectedIndex].text.trim() : "",
        button: button ? button.textContent.trim() : "",
      };
    })
  );
  expect(shown.length).toBeGreaterThanOrEqual(2);
  for (const s of shown) {
    expect(s.button, `${s.id} shows its selected option`).toContain(s.selected);
  }
});

test("with no theme picked the editor is in Monokai and the IDE follows it", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("data-ide-theme", "on");
  const theme = page.locator("#select-theme");
  await expect(theme).toHaveValue("ace/theme/monokai");
  await expect(page.locator(".select-wrap--status .sm-btn", { hasText: "Monokai" })).toBeVisible();
  // the IDE is a dark screen on the (light or dark) page
  expect(await backgroundLuminance(page, ".ide-shell .app-bar")).toBeLessThan(0.25);
});

test("the practice page lists the questions", async ({ page }) => {
  await page.goto("/practice/dsa-questions");
  await expect(page.locator("text=/\\d+ questions/").first()).toBeVisible();
  expect(await page.locator(".q-table tbody tr").count()).toBeGreaterThan(5);
});

test("the footer of a short legal page sits at the bottom of the window", async ({ page }) => {
  await page.goto("/privacy.html");
  await page.addStyleTag({ content: ".legal > *:not(h1):not(.legal__meta) { display: none !important; }" });
  const { bottom, height, scroll } = await page.evaluate(() => ({
    bottom: document.querySelector(".site-footer").getBoundingClientRect().bottom,
    height: window.innerHeight,
    scroll: document.scrollingElement.scrollHeight,
  }));
  expect(Math.abs(bottom - height)).toBeLessThanOrEqual(1);
  expect(scroll).toBeLessThanOrEqual(height + 1);
});
