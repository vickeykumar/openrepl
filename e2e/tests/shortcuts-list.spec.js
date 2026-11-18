// The list of keyboard shortcuts (src/resources/js/palette.js, SHORTCUTS; "?" outside a text field, or Commands > Keyboard shortcuts): it is
// in groups, and has the keys of the editor and terminal, of the command menus, of Genie and of the Preview.
// GENIE_LOCAL_ASSETS=1 serves the script and the style from this checkout, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const root = path.join(__dirname, "..", "..", "src", "resources");

test("the shortcuts are listed in groups and include Genie's and the Preview's keys", async ({ page }) => {
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/palette.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "js/palette.js")) }));
    await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(root, "css/ui-refresh.css")) }));
  }
  await page.goto("/");
  await page.waitForFunction(() => window.OpenreplPalette);
  await page.evaluate(() => window.OpenreplPalette.open("shortcuts"));
  const table = page.locator(".palette__table");
  await expect(table).toBeVisible();
  for (const group of ["Editor and terminal", "Commands and files", "Genie", "Preview"]) {
    await expect(table.locator(".palette__group th", { hasText: group })).toBeVisible();
  }
  const row = (text) => table.locator("tr", { hasText: text });
  await expect(row("Open or close the Preview pane").locator("kbd")).toHaveText(["Ctrl", "Alt", "V"]);
  await expect(row("Move between the editor, the pane's bar and the page").locator("kbd")).toHaveText(["F6"]);
  await expect(row("Show or hide the terminal").locator("kbd")).toHaveText(["Ctrl", "`"]);
  await expect(row("New chat, while the panel has the focus").locator("kbd")).toHaveText(["Alt", "T"]);
  await expect(row("Send the message").locator("kbd")).toHaveText(["↵"]);
  // the old wording, which said Ctrl+` moves the focus, is gone
  await expect(table.locator("tr", { hasText: "Move between the editor and the terminal" })).toHaveCount(0);
});
