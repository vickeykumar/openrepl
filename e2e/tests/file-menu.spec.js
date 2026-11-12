// The right-click menu of the Files panel: Copy path and Copy relative path (page/07-file-browser.js, CopyPaths).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const dir = `menu${stamp}`;
const file = `deep${stamp}.txt`;

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function make(page, rel, kind) {
  const made = await page.evaluate(([r, k]) => window.FileBrowser.create(r, k), [rel, kind]);
  expect(made.ok, `${rel}: ${JSON.stringify(made)}`).toBe(true);
}

async function copyVia(page, anchor, item) {
  await anchor.click({ button: "right" });
  await page.locator(".vakata-context a", { hasText: item }).click();
  return page.evaluate(() => navigator.clipboard.readText());
}

test("Copy path and Copy relative path copy the path of a nested file, and of a folder, and of the workspace", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await ready(page);
  await make(page, dir, "folder");
  await make(page, `${dir}/${file}`, "file");
  const home = await page.evaluate(() => window.homedir);
  const anchorOf = (name) => page.locator("#file-browser .jstree-anchor", { hasText: name }).first();

  const fileAnchor = anchorOf(file);
  await expect(fileAnchor).toBeVisible();
  expect(await copyVia(page, fileAnchor, "Copy path")).toBe(`${home}/${dir}/${file}`);
  expect(await copyVia(page, anchorOf(file), "Copy relative path")).toBe(`${dir}/${file}`);
  expect(await copyVia(page, anchorOf(dir), "Copy relative path")).toBe(dir);
  expect(await copyVia(page, anchorOf(dir), "Copy path")).toBe(`${home}/${dir}`);
  // the workspace itself
  const homeName = home.split("/").pop();
  expect(await copyVia(page, page.locator("#file-browser .jstree-anchor", { hasText: homeName }).first(), "Copy relative path")).toBe(".");
});

test("a notice says it was copied", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await ready(page);
  await make(page, `note${stamp}.txt`, "file");
  await copyVia(page, page.locator("#file-browser .jstree-anchor", { hasText: `note${stamp}.txt` }).first(), "Copy path");
  await expect(page.locator(".notice", { hasText: "Copied the path" })).toBeVisible();
});
