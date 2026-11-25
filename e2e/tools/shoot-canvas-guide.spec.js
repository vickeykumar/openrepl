// Takes the pictures of the "Using the canvas" section of the Genie guide (src/resources/genie.html, #canvas). A tool, not a test:
//   BASE_URL=http://localhost:8088 GENIE_LOCAL_ASSETS=1 npx playwright test tools/shoot-canvas-guide.spec.js --project=chromium
// It draws a small login flow on a canvas, then saves PNGs of the pane in src/resources/images/genie/canvas-*.png.
const { test, expect } = require("@playwright/test");
const path = require("path");
const { waitForTerminalReady } = require("../tests/helpers");

const out = (n) => path.join(__dirname, "..", "..", "src", "resources", "images", "genie", n);
test.use({ viewport: { width: 1440, height: 1250 }, deviceScaleFactor: 1 });

const card = (title, body, accent) => `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title></head><body style="margin:0;font-family:system-ui,-apple-system,sans-serif;background:#f6f5fb;color:#1b1b22">
<div style="padding:34px 38px"><div style="font-size:12px;letter-spacing:.08em;text-transform:uppercase;color:#6b6877">Acme</div><h1 id="title" style="margin:6px 0 16px;font-size:30px">${title}</h1>${body}
<button id="cta" style="margin-top:18px;padding:11px 20px;border-radius:9px;border:0;background:${accent};color:#fff;font-size:15px;font-weight:600">Continue</button></div></body></html>`;
const field = (l) => `<div style="margin:10px 0"><div style="font-size:13px;color:#6b6877;margin-bottom:4px">${l}</div><div style="height:36px;border:1px solid #cfccdb;border-radius:8px;background:#fff"></div></div>`;
const tile = (n, l) => `<div style="flex:1;border:1px solid #dcd9e6;border-radius:12px;background:#fff;padding:16px"><div style="font-size:12px;color:#6b6877">${l}</div><div style="font-size:30px;font-weight:700">${n}</div></div>`;

const LOGIN = card("Sign in", field("Email") + field("Password"), "#FE6A6B");
const DASH = card("Dashboard", `<div style="display:flex;gap:12px">${tile("128", "Visits")}${tile("12", "Sign-ups")}${tile("4%", "Rate")}</div>`, "#FE6A6B");
const PHONE = card("Sign in", field("Email") + field("Password"), "#5B6CFF");

test("pictures for the canvas guide", async ({ page }) => {
  const root = path.join(__dirname, "..", "..", "src");
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: require("fs").readFileSync(root + "/js/dist/scribbler.js") }));
    await page.route((u) => u.pathname.endsWith("/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: require("fs").readFileSync(root + "/resources/css/ui-refresh.css") }));
  }
  await page.addInitScript(() => { try { localStorage.setItem("preview-dock", "editor"); localStorage.setItem("genie-tour-done", "1"); localStorage.setItem("genie-tour", "1"); } catch (e) {} });
  await page.goto("/");
  await page.waitForFunction(() => window.Canvas && window.Preview);
  await waitForTerminalReady(page);
  const name = "login-flow";
  await page.evaluate(async ([n, a, b, c]) => {
    await window.Canvas.agent({ type: "canvas_open", name: n, plan: [{ board: "login", title: "Login", w: 560, h: 380 }, { board: "dashboard", title: "Dashboard", w: 560, h: 380 }, { board: "login-phone", title: "Login, phone", w: 300, h: 380 }] });
    await window.Canvas.agent({ type: "canvas_board", board: "login", title: "Login", html: a, w: 560, h: 380 });
    await window.Canvas.agent({ type: "canvas_board", board: "dashboard", title: "Dashboard", html: b, w: 560, h: 380 });
    await window.Canvas.agent({ type: "canvas_board", board: "login-phone", title: "Login, phone", html: c, w: 300, h: 380 });
  }, [name, LOGIN, DASH, PHONE]);
  await expect(page.locator(".cv-board")).toHaveCount(3);
  const pane = page.locator("#preview-pane");
  const shot = async (file, box) => page.screenshot({ path: out(file), clip: box || (await pane.boundingBox()) });
  // a click at a part of a board (0 to 1 across, 0 to 1 down), held for a moment as a person does
  const clickAt = async (id, fx, fy, hold = 120) => {
    const b = await page.locator(`.cv-board[data-id=${id}] .cv-hit`).boundingBox();
    await page.mouse.move(b.x + b.width * fx, b.y + b.height * fy);
    await page.mouse.down(); await page.waitForTimeout(hold); await page.mouse.up();
  };
  const fit = async () => { await page.getByRole("button", { name: "Fit all" }).click(); await page.waitForTimeout(700); };
  await fit();

  // 1 the canvas with its boards
  await shot("canvas-boards.png");

  // 2 a board selected: its bar, its handles, and the Board chip in Genie's box
  await page.locator(".cv-board[data-id=dashboard] .cv-hit").click();
  await page.waitForTimeout(300);
  await page.locator("#genie-button").click();
  await page.waitForTimeout(900);
  await fit();
  await page.locator(".cv-board[data-id=dashboard] .cv-hit").click();
  await page.waitForTimeout(300);
  const dock = await page.locator("#genie-dock").boundingBox().catch(() => null);
  const pb = await pane.boundingBox();
  const box = dock ? { x: Math.min(pb.x, dock.x), y: Math.min(pb.y, dock.y), width: Math.max(pb.x + pb.width, dock.x + dock.width) - Math.min(pb.x, dock.x), height: Math.max(pb.y + pb.height, dock.y + dock.height) - Math.min(pb.y, dock.y) } : pb;
  await shot("canvas-select-genie.png", box);
  await page.locator("#genie-button").click().catch(() => {});

  // 3 a pin with its note
  await fit();
  await page.getByRole("button", { name: /^Pin a note/ }).click();
  await clickAt("login", 0.62, 0.86);
  await page.waitForTimeout(500);
  await page.locator(".cv-pop__ta").fill("Make this button bigger and put it on the right");
  await shot("canvas-pin.png");

  // 4 versions and Compare: Genie changes the heading, then the versions list and the diff
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.evaluate(async (h) => { await window.Canvas.agent({ type: "canvas_board", board: "login", title: "Login", html: h, label: "bigger button" }); }, LOGIN.replace("padding:11px 20px", "padding:16px 34px").replace("font-size:15px", "font-size:18px"));
  await page.waitForTimeout(800);
  
  await clickAt("login", 0.2, 0.3, 30);
  await page.locator(".cv-ctx").getByRole("button", { name: "Versions" }).click();
  await page.waitForTimeout(400);
  await page.locator(".cv-ver").nth(1).getByRole("button", { name: "Compare" }).click();
  await page.waitForTimeout(400);
  await shot("canvas-versions.png");
  await page.keyboard.press("Escape");

  // 5 the notes of the canvas
  await page.evaluate(() => document.querySelector(".cv-menu") && (document.querySelector(".cv-menu").hidden = true));
  await fit();
  await page.getByRole("button", { name: /^Pin a note/ }).click();
  await clickAt("dashboard", 0.25, 0.55);
  await page.waitForTimeout(500);
  await page.locator(".cv-pop__ta").fill("Show the numbers with a sparkline");
  await page.getByRole("button", { name: "Note", exact: true }).click();
  await page.waitForTimeout(500);
  if (!(await page.locator(".cv-panel").isVisible())) await page.locator(".cv-bar").getByRole("button", { name: /^Comments/ }).click();
  await page.waitForTimeout(400);
  await shot("canvas-comments.png");
  await page.locator(".cv-panel").getByRole("button").first().click().catch(() => {});

  // 6 export
  await page.locator(".cv-bar").getByRole("button", { name: "Export" }).click();
  await page.waitForTimeout(300);
  await shot("canvas-export.png");
});
