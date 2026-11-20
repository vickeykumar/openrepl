// The Genie guide (src/resources/genie.html, served at /genie) and the places that lead to it: the site menu, the footer, the main page's Genie
// section and FAQ, the practice page's menu, and the command menu. Needs a server built with the page.
const { test, expect } = require("@playwright/test");

test("the guide is at /genie, with its sections and screenshots", async ({ page }) => {
  const res = await page.goto("/genie");
  expect(res.status()).toBe(200);
  await expect(page.locator("h1")).toHaveText("How to use Genie");
  for (const id of ["start", "code", "modes", "agent", "chats", "models", "tips", "skills", "phone", "coach", "keys", "questions"]) {
    await expect(page.locator(`#${id}`)).toHaveCount(1);
  }
  await expect(page.locator(".guide-toc a")).toHaveCount(12);
  // the pictures are there
  for (const img of await page.locator(".guide figure img").all()) {
    expect((await (await page.request.get(await img.getAttribute("src"))).status())).toBe(200);
  }
  await expect(page).toHaveTitle(/How to use Genie/);
});

test("the main page, the practice page and the command menu lead to the guide", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#genie_guide a")).toHaveAttribute("href", "/genie");
  await expect(page.locator('footer a[href="/genie"]').first()).toBeAttached();
  await expect(page.locator('.bento-card a[href="/genie"]')).toBeAttached();
  await page.goto("/practice/coding-practice");
  await expect(page.locator('#header-nav a[href="/genie"]')).toBeAttached();
  await page.goto("/");
  await page.waitForFunction(() => window.OpenreplPalette);
  await page.evaluate(() => window.OpenreplPalette.open("commands"));
  await page.locator(".palette input").fill("genie guide");
  await expect(page.locator(".palette").getByText("Genie guide: how to use Genie")).toBeVisible();
});

test("the Genie panel has a help icon that opens the guide in a new tab", async ({ page }) => {
  if (process.env.GENIE_LOCAL_ASSETS) {
    const fs = require("fs");
    const path = require("path");
    await page.route((u) => u.pathname.endsWith("/js/model-choice.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "js", "model-choice.js")) }));
    await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "chat-widget", "dist", "index.umd.js")) }));
    await page.route((u) => u.pathname.endsWith("/js/chat-widget-agent.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "chat-widget", "dist", "agent.js")) }));
  }
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
  await page.evaluate(() => window.ChatWidget.open());
  const help = page.locator("#chat-widget__help");
  await expect(help).toBeVisible();
  await expect(help).toHaveAttribute("href", "/genie");
  await expect(help).toHaveAttribute("target", "_blank");
});
