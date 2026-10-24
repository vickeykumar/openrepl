// An accessibility scan (axe) of the main pages in both themes. It only REPORTS for now:
// the violations are attached to the test and listed in its annotations, they do not fail
// it. Once the list is short, fail on the ones that matter (see docs/ci.md).
const { test, expect } = require("@playwright/test");
const AxeBuilder = require("@axe-core/playwright").default || require("@axe-core/playwright");

const PAGES = ["/", "/practice/dsa-questions", "/privacy.html"];

for (const scheme of ["light", "dark"]) {
  for (const path of PAGES) {
    test(`axe: ${path} (${scheme})`, async ({ browser }) => {
      const context = await browser.newContext({ colorScheme: scheme });
      const page = await context.newPage();
      await page.goto(path);
      await page.waitForLoadState("load");
      const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
      const summary = results.violations.map((v) => `${v.impact || "?"}: ${v.id} (${v.nodes.length})`);
      test.info().annotations.push({ type: "violations", description: summary.join("; ") || "none" });
      await test.info().attach("axe-violations.json", {
        body: JSON.stringify(results.violations, null, 2),
        contentType: "application/json",
      });
      expect(Array.isArray(results.violations)).toBe(true);
      await context.close();
    });
  }
}
