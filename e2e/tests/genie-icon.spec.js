// Genie's lamp icon (chat-widget/src/widget.css, .genie-ico): it is in the Ask Genie buttons and the
// panel header, still when idle, and it moves while an answer is on its way.
const { test, expect } = require("@playwright/test");

async function openGenie(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
}

test("the lamp is in the Ask Genie buttons and in the panel header, and is still when idle", async ({ page }) => {
  await openGenie(page);
  await expect(page.locator("#genie-button svg.genie-ico")).toHaveCount(1);
  await expect(page.locator("[data-chat-widget-button] svg.genie-ico")).toHaveCount(1);
  await page.evaluate(() => window.ChatWidget.open());
  await expect(page.locator("#chat-widget__title_icon.genie-ico")).toHaveCount(1);
  const animation = await page.locator("#chat-widget__title_icon .gi-spark").evaluate((e) => getComputedStyle(e).animationName);
  expect(animation).toBe("none");
});

test("the lamp moves while an answer is on its way, and stops when it arrives", async ({ page }) => {
  await openGenie(page);
  let release;
  const held = new Promise((resolve) => (release = resolve));
  await page.route((url) => url.pathname.endsWith("/chat/completions"), async (route) => {
    await held;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ choices: [{ message: { role: "assistant", content: "Hello" }, finish_reason: "stop" }], usage: { prompt_tokens: 3, completion_tokens: 1 } }),
    });
  });
  await page.evaluate(() => window.ChatWidget.open());
  await page.locator("#chat-widget__input").fill("hello");
  await expect(page.locator("#chat-widget__submit")).toBeEnabled();
  await page.locator("#chat-widget__submit").click();

  const panel = page.locator("#chat-widget__container");
  await expect(panel).toHaveAttribute("data-genie-work", "chat");
  const spark = page.locator("#chat-widget__title_icon .gi-spark");
  await expect.poll(() => spark.evaluate((e) => getComputedStyle(e).animationName)).toBe("gi-twinkle");
  // the button of the app bar says the same, so a closed panel still shows it
  await expect(page.locator("#genie-button")).toHaveAttribute("data-genie-work", "chat");

  release();
  await expect(panel).not.toHaveAttribute("data-genie-work", /.+/);
  await expect.poll(() => spark.evaluate((e) => getComputedStyle(e).animationName)).toBe("none");
});

// An Agent task and a ready result are checked by setting what renderActivity sets, because a real task needs
// a signed-in user and a model; the chat state above is the real thing.
test("an Agent task rocks the lamp and pops sparkles, and a ready result shows the green tick", async ({ page }) => {
  await openGenie(page);
  const button = page.locator("#genie-button");
  const css = (selector, property) => button.locator(selector).evaluate((e, p) => getComputedStyle(e)[p], property);
  await button.evaluate((b) => b.setAttribute("data-genie-work", "agent"));
  await expect.poll(() => css(".gi-lamp", "animationName")).toBe("gi-rub");
  await expect.poll(() => css(".gi-spark", "animationName")).toBe("gi-spin");
  for (const mini of [".gi-m1", ".gi-m2", ".gi-m3"]) {
    await expect.poll(() => css(mini, "animationName")).toBe("gi-pop");
  }
  expect(await css(".gi-m3", "animationDelay")).toBe("1s");

  await button.evaluate((b) => { b.removeAttribute("data-genie-work"); b.setAttribute("data-genie-state", "done"); });
  await expect.poll(() => css(".gi-tick", "opacity")).toBe("1");
  await expect.poll(() => css(".gi-spark", "animationName")).toBe("gi-burst");

  await button.evaluate((b) => b.removeAttribute("data-genie-state"));
  await expect.poll(() => css(".gi-tick", "opacity")).toBe("0");
  await expect.poll(() => css(".gi-lamp", "animationName")).toBe("none");
});

test("an answer that arrives while the panel is open shows the tick for a few seconds, then it goes", async ({ page }) => {
  await openGenie(page);
  await page.route((url) => url.pathname.endsWith("/chat/completions"), (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ choices: [{ message: { role: "assistant", content: "Hello" }, finish_reason: "stop" }], usage: { prompt_tokens: 3, completion_tokens: 1 } }),
    })
  );
  await page.evaluate(() => window.ChatWidget.open());
  await page.locator("#chat-widget__input").fill("hello");
  await expect(page.locator("#chat-widget__submit")).toBeEnabled();
  await page.locator("#chat-widget__submit").click();

  const button = page.locator("#genie-button");
  const panel = page.locator("#chat-widget__container");
  await expect(button).toHaveAttribute("data-genie-state", "done");
  await expect(panel).toHaveAttribute("data-genie-state", "done");
  await expect(page.locator("#chat-widget__title_icon .gi-tick")).toHaveCSS("opacity", "1");
  await expect(button.locator(".gi-tick")).toHaveCSS("opacity", "1");
  // it is a flash, not a mark to clear: a few seconds later the lamp is quiet again
  await expect(button).not.toHaveAttribute("data-genie-state", /.+/, { timeout: 9000 });
  await expect(button.locator(".gi-tick")).toHaveCSS("opacity", "0");
});
