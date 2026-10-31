// The model picker of the Genie panel (chat-widget/src/index.ts, js/model-choice.js): a short list
// ("Your models"), the rest under "Other models" grouped by provider, search, pins, and the details
// of a model beside the list. The server of the test has no Cloudflare token, so the page is told it
// has one (config.js is answered with the flag added), which is what a server with a token does.
const { test, expect } = require("@playwright/test");

async function openPicker(page) {
  await page.route((url) => url.pathname.endsWith("/config.js"), async (route) => {
    const res = await route.fetch();
    await route.fulfill({ response: res, body: (await res.text()) + "\nvar cloudflare_enabled = true;\n" });
  });
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
  await page.evaluate(() => window.ChatWidget.open());
  await page.locator("#chat-widget__model-btn").click();
  return page.locator("#chat-widget__settings");
}

const yours = (page) => page.locator("#chat-widget__models .cw-model");

test("the picker opens with a short list and the rest one click away", async ({ page }) => {
  const picker = await openPicker(page);
  await expect(picker).toBeVisible();
  await expect(picker.locator("#cw-model-label")).toHaveText("Your models");
  await expect(yours(page)).toHaveCount(3);
  await expect(page.locator("#chat-widget__others")).toBeHidden();
  await expect(page.locator("#chat-widget__others-count")).toHaveText(/\d+ more/);

  await page.locator("#chat-widget__others-toggle").click();
  await expect(page.locator("#chat-widget__others")).toBeVisible();
  const headings = page.locator("#chat-widget__others .cw-set__group-label");
  await expect(headings.filter({ hasText: "Cloudflare Workers AI" })).toHaveCount(1);
  // the models of Your models are not listed twice
  const all = await page.locator("#chat-widget__settings .cw-model").evaluateAll((els) => els.map((e) => e.dataset.id));
  expect(new Set(all).size).toBe(all.length);
});

test("choosing a model from Other models uses it, and it moves into Your models", async ({ page }) => {
  const picker = await openPicker(page);
  await page.locator("#chat-widget__others-toggle").click();
  await page.locator('#chat-widget__others .cw-model[data-id="@cf/qwen/qwen3-30b-a3b-fp8"]').click();
  await expect(page.locator("#chat-widget__model-label")).toHaveText("Qwen3 30B");
  await expect(picker.locator('#chat-widget__models .cw-model[data-id="@cf/qwen/qwen3-30b-a3b-fp8"]')).toHaveAttribute("aria-checked", "true");
  // kept on this device
  await page.reload();
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
  expect(await page.evaluate(() => localStorage.getItem("genie-model"))).toContain("@cf/qwen/qwen3-30b-a3b-fp8");
});

test("search finds a model by name, provider or tag, and Esc clears it before it closes the list", async ({ page }) => {
  const picker = await openPicker(page);
  const search = page.locator("#chat-widget__model-search");
  await search.fill("qwen");
  await expect(page.locator("#chat-widget__results .cw-model")).toHaveCount(1);
  await expect(page.locator("#chat-widget__results mark")).toHaveText("Qwen");
  await expect(page.locator("#chat-widget__model-count")).toHaveText("1 model matches");
  await expect(page.locator("#chat-widget__models")).toBeHidden();

  await search.fill("free cloudflare");
  await expect(page.locator("#chat-widget__results .cw-model")).toHaveCount(3);
  await search.fill("zzzz");
  await expect(page.locator("#chat-widget__model-count")).toContainText("No model matches");

  await search.press("Escape");
  await expect(search).toHaveValue("");
  await expect(picker).toBeVisible();
  await expect(yours(page)).toHaveCount(3);
  await search.press("Escape");
  await expect(picker).toBeHidden();
});

test("a pinned model stays in Your models, and the star can be used from the keyboard", async ({ page }) => {
  const picker = await openPicker(page);
  await page.locator("#chat-widget__others-toggle").click();
  const id = "@cf/zai-org/glm-4.7-flash";
  const row = page.locator(`#chat-widget__others .cw-modelrow:has(.cw-model[data-id="${id}"])`);
  await row.hover();
  await row.locator(".cw-pin").click();
  await expect(picker.locator(`#chat-widget__models .cw-model[data-id="${id}"]`)).toHaveCount(1);

  await page.reload();
  await page.waitForFunction(() => window.ChatWidget && typeof window.ChatWidget.open === "function");
  await page.evaluate(() => window.ChatWidget.open());
  await page.locator("#chat-widget__model-btn").click();
  await expect(page.locator(`#chat-widget__models .cw-model[data-id="${id}"]`)).toHaveCount(1);
  const pin = page.locator(`#chat-widget__models .cw-pin[data-id="${id}"]`);
  await expect(pin).toHaveAttribute("aria-pressed", "true");
  await pin.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(`#chat-widget__models .cw-model[data-id="${id}"]`)).toHaveCount(0);
});

test("the details of a model show beside the list", async ({ page }) => {
  await openPicker(page);
  await page.locator("#chat-widget__models .cw-model").nth(0).hover();
  const details = page.locator("#chat-widget__details");
  await expect(details).toBeVisible();
  await expect(details).toContainText("Runs on");
  await expect(details).toContainText("Thinks first");
  await page.locator("#chat-widget__settings .cw-set__label--effort").hover();
  await expect(details).toBeHidden();
});

test("the / key puts the cursor in the search box", async ({ page }) => {
  const picker = await openPicker(page);
  await picker.locator(".cw-model").first().focus();
  await page.keyboard.press("/");
  await expect(page.locator("#chat-widget__model-search")).toBeFocused();
});
