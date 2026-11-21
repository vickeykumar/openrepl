// The main page polish (css/ui-refresh.css "Main page polish", js/src/page/36-run-hint.js): the FAQ as cards that open, the coloured
// language tags, the one-time hint in the terminal, and no jump of the terminal while the page loads.
const { test, expect } = require("@playwright/test");

test("the FAQ is a list of cards: the first is open, the others open on a click, and the Genie answer is short with a link to the guide", async ({ page }) => {
  await page.goto("/");
  const items = page.locator("#faq details.faq-item");
  await expect(items).toHaveCount(6);
  await expect(items.first()).toHaveAttribute("open", "");
  await expect(items.nth(1)).not.toHaveAttribute("open", "");
  await items.nth(1).locator("summary").click();
  await expect(items.nth(1)).toHaveAttribute("open", "");
  const agent = items.filter({ hasText: "Can Genie do a task for me?" });
  await agent.locator("summary").click();
  await expect(agent.locator("a[href='/genie']")).toBeVisible();
  expect((await agent.innerText()).length).toBeLessThan(420);
});

test("each language tile has a tag of its own colour", async ({ page }) => {
  await page.goto("/");
  const colours = await page.locator(".lang-card__ext").evaluateAll((els) => els.map((e) => getComputedStyle(e).backgroundColor));
  expect(colours.length).toBeGreaterThanOrEqual(15);
  expect(new Set(colours).size).toBeGreaterThan(5);
});

test("the terminal hint shows on a first visit, goes when Run is pressed and stays away afterwards", async ({ page }) => {
  await page.goto("/");
  await page.evaluate(() => localStorage.removeItem("openrepl-run-hint"));
  await page.reload();
  const hint = page.locator(".run-hint");
  await expect(hint).toBeVisible({ timeout: 15000 });
  await expect(hint).toContainText("Press Run to see output");
  await page.locator(".run-split button").first().click();
  await expect(hint).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("openrepl-run-hint"))).toBe("1");
  await page.reload();
  await page.waitForTimeout(3000);
  await expect(page.locator(".run-hint")).toHaveCount(0);
});

test("the terminal does not jump while the page loads", async ({ page }) => {
  await page.addInitScript(() => {
    window.__shift = 0;
    window.__src = [];
    new PerformanceObserver((l) => l.getEntries().forEach((e) => {
      if (e.hadRecentInput) return;
      window.__shift += e.value;
      // what moved, when and how far: the failure message says it, so a run that fails in the CI can be read without a trace
      window.__src.push(e.value.toFixed(3) + " at " + Math.round(e.startTime) + " ms: " + (e.sources || []).map((s) => ((s.node && (s.node.id || s.node.className || s.node.nodeName)) || "?") + " y " + Math.round(s.previousRect.y) + "->" + Math.round(s.currentRect.y) + " h " + Math.round(s.currentRect.height)).join("; "));
    })).observe({ type: "layout-shift", buffered: true });
  });
  await page.goto("/");
  await page.waitForTimeout(4000);
  const shift = await page.evaluate(() => window.__shift);
  const sources = await page.evaluate(() => window.__src.filter((x) => parseFloat(x) > 0.003).join(" | "));
  expect(shift, "layout shifts: " + sources).toBeLessThan(0.1);
  expect(await page.locator("#ide-split").getAttribute("data-split-init")).toBe("1");
});
