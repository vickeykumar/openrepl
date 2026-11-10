// Forks (js/src/page/11-terminal-state.js): while a fork is open the tabs say which is the first terminal and
// which is a fork, the footer says that forks end with the first terminal, and the first fork of a visit gets a
// notice. A page opened by the Fork button (a jid in its address) labels its own tab as a fork. What a fork shows
// when the first terminal ends needs a host with sandboxes; it is checked by hand and in server/forks_test.go.
const { test, expect } = require("@playwright/test");

const tabs = (page) => page.locator("#terminal-tabs .tab");

test("with one terminal the tab is not labelled", async ({ page }) => {
  await page.goto("/python");
  await expect(tabs(page)).toHaveCount(1);
  await expect(tabs(page).first()).not.toHaveAttribute("data-fork-role", /.+/);
});

test("a second terminal makes the tabs say which is the first and which is a fork", async ({ page }) => {
  await page.goto("/python");
  await expect(tabs(page)).toHaveCount(1);
  // the + button works once the first terminal is connected
  await expect(page.locator("#term-footer-text")).toContainText("Connected", { timeout: 30000 });
  await page.locator("#terminal-tabs .add-tab").first().click();
  await expect(tabs(page)).toHaveCount(2);
  await expect(tabs(page).nth(0)).toHaveAttribute("data-fork-role", "first");
  await expect(tabs(page).nth(1)).toHaveAttribute("data-fork-role", "fork");
  // the page's tooltip script moves a title into data-tip-title
  const tip = await tabs(page).nth(0).evaluate((t) => t.getAttribute("title") || t.getAttribute("data-tip-title") || "");
  expect(tip).toMatch(/first terminal/i);
  await expect(page.locator("#term-footer-text")).toContainText("forks end with the first terminal");
  // the first fork of the visit is explained once
  await expect(page.locator("#notices")).toContainText("This terminal ends when the first terminal ends.");
});

test("a page opened by the Fork button labels its own tab as a fork", async ({ page }) => {
  await page.goto("/python?jid=abcd");
  await expect(tabs(page).first()).toHaveAttribute("data-fork-role", "fork");
  await expect(page.locator("#term-footer-text")).toContainText("forked from");
});
