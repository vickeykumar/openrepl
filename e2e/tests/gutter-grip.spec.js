// The gutter between the editor and the terminal has three dots in its middle, so that it looks like something to drag: a column of them
// when the two are side by side, a row of them when they are stacked (css/ui-refresh.css, ".ide-split .gutter::after").
// GENIE_LOCAL_ASSETS=1 serves the style sheet of this checkout instead of the one inside the server under test.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

test("the gutter shows a grip of three dots, side by side and stacked", async ({ page }) => {
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "resources", "css", "ui-refresh.css")) }));
  }
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/");
  await page.waitForFunction(() => document.querySelector("#ide-split .gutter") && window.FileBrowser && window.FileBrowser.ready());
  const grip = () =>
    page.evaluate(() => {
      const g = document.querySelector("#ide-split .gutter");
      const c = getComputedStyle(g, "::after");
      const r = g.getBoundingClientRect();
      return { dir: g.classList.contains("gutter-vertical") ? "stacked" : "side", display: c.display, w: parseFloat(c.width), h: parseFloat(c.height), image: c.backgroundImage, opacity: c.opacity, gutter: { w: r.width, h: r.height } };
    });
  // side by side: a column of dots on a vertical gutter
  const side = await grip();
  expect(side.dir).toBe("side");
  expect(side.display).toBe("block");
  expect(side.image).toContain("radial-gradient");
  expect(side.h).toBeGreaterThan(side.w);
  expect(side.opacity).toBe("1");
  // stacked: a row of dots on a horizontal gutter
  await page.locator("#rotate-button").click();
  await expect.poll(async () => (await grip()).dir).toBe("stacked");
  const stacked = await grip();
  expect(stacked.display).toBe("block");
  expect(stacked.image).toContain("radial-gradient");
  expect(stacked.w).toBeGreaterThan(stacked.h);
});
