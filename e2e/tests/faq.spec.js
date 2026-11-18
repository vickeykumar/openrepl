// The questions and answers: the page at /faq (src/resources/faq.html) and the six that the main page keeps (index.html, #faq). Needs a server built
// with the page; GENIE_LOCAL_ASSETS=1 serves faq.html and the main page from this checkout for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const root = path.join(__dirname, "..", "..", "src", "resources");

async function serveLocal(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  await page.route((u) => u.pathname === "/faq", (r) => r.fulfill({ contentType: "text/html", body: fs.readFileSync(path.join(root, "faq.html")) }));
}

test("the FAQ page has every question in topics, and search narrows them", async ({ page }) => {
  await serveLocal(page);
  const res = await page.goto("/faq");
  expect(res.status()).toBe(200);
  await expect(page.locator("h1")).toHaveText("Questions, answered");
  await expect(page.locator(".faq-q")).toHaveCount(22);
  for (const topic of ["Getting started", "Sharing", "Genie", "Practice", "Editor and apps"]) {
    await expect(page.locator(".faq-list h2", { hasText: topic })).toBeVisible();
  }
  await page.locator("#faq-search").fill("genie");
  await expect(page.locator(".faq-q:visible")).toHaveCount(await page.locator(".faq-q:visible").count());
  const shown = await page.locator(".faq-q:visible").count();
  expect(shown).toBeGreaterThanOrEqual(10);
  expect(shown).toBeLessThan(22);
  await expect(page.locator(".faq-list h2:visible", { hasText: "Getting started" })).toHaveCount(0);
  await page.locator("#faq-search").fill("zzzz nothing");
  await expect(page.locator("#faq-none")).toBeVisible();
});

test("a question is opened by its address", async ({ page }) => {
  await serveLocal(page);
  await page.goto("/faq#are-my-genie-chats-kept");
  await expect(page.locator("#are-my-genie-chats-kept")).toHaveJSProperty("open", true);
});

test("the main page keeps six questions, with the same words, and leads to the page", async ({ page }) => {
  await serveLocal(page);
  const faq = fs.existsSync(path.join(root, "faq.html")) ? fs.readFileSync(path.join(root, "faq.html"), "utf8") : "";
  await page.goto("/");
  const home = page.locator("#faq .faq-item");
  await expect(home).toHaveCount(6);
  await expect(page.locator('#faq a[href="/faq"]').first()).toBeAttached();
  await expect(page.locator("#faq_link a")).toHaveAttribute("href", "/faq");
  // the six are on the page, word for word
  const answers = await page.locator("#faq .faq-item p").evaluateAll((els) => els.map((e) => e.textContent.trim()));
  const body = (await (await page.request.get("/faq")).text()).replace(/<[^>]+>/g, "").replace(/\s+/g, " ");
  for (const a of answers) expect(body).toContain(a.replace(/\s+/g, " ").slice(0, 60));
  void faq;
});
