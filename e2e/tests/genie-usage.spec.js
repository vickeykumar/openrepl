// The tokens Genie used, in the usage details of the Genie panel (chat-widget/src/tokens.ts).
// The server's answer is made up here: what the server counts is tested in Go.
const { test, expect } = require("@playwright/test");

async function openWith(page, tokens, extra = {}) {
  await page.route((url) => url.pathname.endsWith("/chat/usage"), (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: false, tokens, ...extra }),
    })
  );
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
  await page.evaluate(() => window.ChatWidget.open());
  await page.locator("#chat-widget__usage-line").click();
  return page.locator("#chat-widget__usage-more");
}

test("a visitor sees the tokens of the visit, and a way to keep them", async ({ page }) => {
  const more = await openWith(page, { visit: { in: 9310, out: 3170, answers: 14 } });
  await expect(more).toContainText("Tokens used (this visit)");
  await expect(more).toContainText("12,480");
  await expect(more).toContainText("Sent to Genie");
  await expect(more).toContainText("9,310");
  await expect(more).toContainText("Sign in to keep a total");
});

test("a signed-in user sees the month and all time, and the cost only when it is sent", async ({ page }) => {
  const usage = { month: { in: 141200, out: 45220, answers: 212 }, all: { in: 900000, out: 304950, answers: 1480 } };
  const more = await openWith(page, usage, { signedIn: true });
  await expect(more).toContainText("Tokens this month");
  await expect(more).toContainText("186,420");
  await expect(more).toContainText("1,204,950");
  await expect(more).not.toContainText("Cost this month");
});

test("the cost shows when an admin turned it on", async ({ page }) => {
  const usage = { month: { in: 1, out: 1, answers: 1 }, all: { in: 2, out: 2, answers: 2 }, cost: { month: 0.64, all: 4.2 } };
  const more = await openWith(page, usage, { signedIn: true });
  await expect(more).toContainText("Cost this month");
  await expect(more).toContainText("$0.64");
  await expect(more).toContainText("$4.20");
});

test("with nothing used yet there are no token rows", async ({ page }) => {
  const more = await openWith(page, { visit: { in: 0, out: 0, answers: 0 } });
  await expect(more).toContainText("Requests left");
  await expect(more).not.toContainText("Tokens used");
});
