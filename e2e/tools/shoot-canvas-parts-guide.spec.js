// Takes the pictures of "11. Change one part of a board" in the Genie guide (src/resources/genie.html, #canvas):
//   BASE_URL=http://localhost:8088 GENIE_LOCAL_ASSETS=1 npx playwright test tools/shoot-canvas-parts-guide.spec.js --project=chromium
// (copy it into tests/ to run it: the config only looks there). Saves src/resources/images/genie/canvas-part-*.png.
const { test, expect } = require("@playwright/test");
const path = require("path");
const { waitForTerminalReady } = require("../tests/helpers");

const out = (n) => path.join(__dirname, "..", "..", "src", "resources", "images", "genie", n);
test.use({ viewport: { width: 1440, height: 1300 }, deviceScaleFactor: 1 });

const PAGE = `<!doctype html><html><head><meta charset="utf-8"><title>Pricing</title></head><body style="margin:0;font-family:system-ui,sans-serif;background:#f6f5fb;color:#1b1b22"><div id="card" style="padding:40px 46px"><div style="font-size:12px;letter-spacing:.08em;color:#6b6877">ACME</div><h1 id="title" style="margin:6px 0 8px;font-size:34px">Simple pricing</h1><p id="sub" style="margin:0 0 20px;color:#555;font-size:16px">One plan, everything included.</p><div id="row" style="display:flex;gap:12px"><button id="buy" style="padding:12px 22px;border-radius:9px;border:0;background:#FE6A6B;color:#fff;font-size:16px;font-weight:600">Buy now</button><button id="talk" style="padding:12px 22px;border-radius:9px;border:1px solid #cfccdb;background:#fff;color:#1b1b22;font-size:16px">Talk to us</button></div></div></body></html>`;

test("pictures for the part tools", async ({ page }) => {
  const root = path.join(__dirname, "..", "..", "src");
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: require("fs").readFileSync(root + "/js/dist/scribbler.js") }));
  await page.route((u) => u.pathname.endsWith("/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: require("fs").readFileSync(root + "/resources/css/ui-refresh.css") }));
  await page.addInitScript(() => { try { localStorage.setItem("preview-dock", "editor"); } catch (e) {} });
  await page.goto("/");
  await page.waitForFunction(() => window.Canvas && window.Preview);
  await waitForTerminalReady(page);
  await page.evaluate(async (h) => {
    await window.Canvas.agent({ type: "canvas_open", name: "pricing-" + Date.now().toString(36) });
    await window.Canvas.agent({ type: "canvas_board", board: "pricing", title: "Pricing", html: h, w: 760, h: 420 });
  }, PAGE);
  await expect(page.locator(".cv-board")).toHaveCount(1);
  await page.getByRole("button", { name: "Fit all" }).click();
  await page.locator(".cv-board[data-id=pricing] .cv-hit").click();
  const frame = page.frameLocator(".cv-board[data-id=pricing] .cv-frame");
  const pane = page.locator("#preview-pane");
  const shot = async (file) => page.screenshot({ path: out(file), clip: await pane.boundingBox() });
  await frame.locator("#buy").dispatchEvent("click", { bubbles: true });
  await expect(page.locator(".nb")).toBeVisible();
  await page.waitForTimeout(500);
  await shot("canvas-part-toolbar.png");
  await page.locator(".nb").getByRole("button", { name: "Fill", exact: true }).click();
  await page.locator(".fp__hex").first().fill("#1b6ef3");
  await page.waitForTimeout(600);
  await shot("canvas-part-fill.png");
  await page.locator(".fp").getByRole("button", { name: "Keep" }).click();
  await frame.locator("#talk").dispatchEvent("click", { shiftKey: true, bubbles: true });
  await page.waitForTimeout(500);
  await page.locator(".nb").getByRole("button", { name: "Align", exact: true }).click();
  await page.waitForTimeout(400);
  await shot("canvas-part-align.png");
});
