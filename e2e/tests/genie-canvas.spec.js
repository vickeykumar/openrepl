// The canvas of the Preview pane (page/37-canvas.js, server/genie_canvas.go, docs/lld/21-genie-canvas.md), by the four phases of its design:
// 1 see it, 2 move it and talk about it, 3 edit it, 4 share it.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const canvas = `cv${stamp}`;

async function ready(page) {
  if (process.env.GENIE_LOCAL_ASSETS) {
    const file = require("path").join(__dirname, "..", "..", "src", "js", "dist", "scribbler.js");
    await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (route) => route.fulfill({ contentType: "application/javascript", body: require("fs").readFileSync(file) }));
    const css = require("path").join(__dirname, "..", "..", "src", "resources", "css", "ui-refresh.css");
    await page.route((u) => u.pathname.endsWith("/ui-refresh.css"), (route) => route.fulfill({ contentType: "text/css", body: require("fs").readFileSync(css) }));
  }
  await page.goto("/");
  await page.waitForFunction(() => window.Preview && window.Canvas);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

const PAGE = (t, extra = "") => `<!doctype html><html><head><meta charset="utf-8"><title>${t}</title></head><body style="font-family:sans-serif"><h1 id="h">${t}</h1><button id="b">Buy</button>${extra}</body></html>`;

async function draw(page, board, title, html, extra = {}) {
  return page.evaluate(([b, t, h, e]) => window.Canvas.agent(Object.assign({ type: "canvas_board", board: b, title: t, html: h }, e)), [board, title, html, extra]);
}

async function fit(page) {
  await page.getByRole("button", { name: "Fit all" }).click();
}

async function open(page) {
  const r = await page.evaluate((n) => window.Canvas.agent({ type: "canvas_open", name: n }), canvas);
  expect(r.status).toBe("done");
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.locator("#canvas-root")).toBeVisible();
}

test.describe("canvas", () => {
  // the pane is only as tall as the editor: a tall window gives the boards room
  test.use({ viewport: { width: 1440, height: 1200 } });

  test("phase 1: Genie draws boards, the canvas can be panned, zoomed and fitted, and sits as a tab", async ({ page }) => {
    await ready(page);
    await open(page);
    await expect(page.locator(".preview-tab--canvas")).toContainText("Canvas · " + canvas);
    const r1 = await draw(page, "login", "Login", PAGE("Login"), { w: 600, h: 400 });
    const r2 = await draw(page, "dashboard", "Dashboard", PAGE("Dashboard"), { w: 600, h: 400 });
    expect(r1.status).toBe("done");
    expect(r2.status).toBe("done");
    await expect(page.locator(".cv-board")).toHaveCount(2);
    // each board is a frame with no origin and a strict policy
    const sandbox = await page.locator(".cv-frame").first().getAttribute("sandbox");
    expect(sandbox).toBe("allow-scripts");
    const frame = page.frameLocator(".cv-board[data-id=login] .cv-frame");
    await expect(frame.locator("#h")).toHaveText("Login");
    // the policy: a board cannot call the network
    const blocked = await page.frames().find((f) => f !== page.mainFrame() && f.url() === "about:srcdoc").evaluate(() => fetch("https://example.com/").then(() => "reached", () => "blocked"));
    expect(blocked).toBe("blocked");
    // zoom and fit
    await page.getByRole("button", { name: /^Zoom in/ }).click();
    await expect(page.locator(".cv-zoom")).toHaveText("125%");
    await page.getByRole("button", { name: "Fit all" }).click();
    const z = parseInt(await page.locator(".cv-zoom").innerText(), 10);
    expect(z).toBeLessThanOrEqual(100);
    await page.locator(".cv-view").hover();
    await page.mouse.wheel(0, 120);
    await expect(page.locator(".cv-minimap, .cv-mini")).toBeVisible();
    // a task draws at most 24 boards; the files are plain pages in the workspace
    const files = await page.evaluate(async (n) => {
      const r = await fetch(preprocessurl("/genie_canvas"), { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "read", canvas: n, id: "login" }) });
      return r.json();
    }, canvas);
    expect(files.html).toContain("<h1 id=\"h\">Login</h1>");
  });

  test("phase 2: move and size with a size label, select and the Board chip, versions with Undo, Redo and Compare", async ({ page }) => {
    await ready(page);
    await open(page);
    await draw(page, "a", "A", PAGE("A one"), { w: 400, h: 300 });
    await draw(page, "b", "B", PAGE("B"), { w: 400, h: 300 });
    await fit(page);
    const box = page.locator(".cv-board[data-id=a] .cv-hit");
    await box.click();
    await expect(page.locator(".cv-ctx")).toBeVisible();
    await expect(page.locator(".cv-ctx__name")).toContainText("A · 400 × 300");
    // drag it: it moves, and the new place is saved
    const before = await page.evaluate(() => window.Canvas.state().boards.length);
    const bb = await box.boundingBox();
    await page.mouse.move(bb.x + 50, bb.y + 50);
    await page.mouse.down();
    await page.mouse.move(bb.x + 150, bb.y + 90, { steps: 6 });
    await page.mouse.up();
    await expect.poll(() => page.evaluate(async (n) => {
      const r = await fetch(preprocessurl("/genie_canvas"), { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "state", canvas: n }) });
      const j = await r.json();
      return j.boards.filter((b) => b.id === "a")[0].x;
    }, canvas)).not.toBe(0);
    expect(before).toBe(2);
    // the Board chip in Genie's box says which board
    await page.locator("#genie-button").click();
    await expect(page.locator("#chat-widget__board-chip")).toContainText("Board: A");
    // a change by Genie is a version: Undo takes it back, Redo brings it again, Compare shows the lines
    await draw(page, "a", "A", PAGE("A two"), { w: 400, h: 300 });
    await expect(page.frameLocator(".cv-board[data-id=a] .cv-frame").locator("#h")).toHaveText("A two");
    await page.locator(".cv-board[data-id=a] .cv-hit").click();
    await page.getByRole("button", { name: /^Undo the last change/ }).click();
    await expect(page.frameLocator(".cv-board[data-id=a] .cv-frame").locator("#h")).toHaveText("A one");
    await page.getByRole("button", { name: /^Redo/ }).click();
    await expect(page.frameLocator(".cv-board[data-id=a] .cv-frame").locator("#h")).toHaveText("A two");
    await page.getByRole("button", { name: "Versions" }).click();
    await expect(page.locator(".cv-ver")).toHaveCount(2);
    await page.locator(".cv-ver").nth(1).getByRole("button", { name: "Compare" }).click();
    await expect(page.locator(".cv-diff__add")).toContainText("A two");
    await expect(page.locator(".cv-diff__del")).toContainText("A one");
  });

  test("phase 3: retype a heading, pin a note and send it, duplicate and delete", async ({ page }) => {
    await ready(page);
    await open(page);
    await draw(page, "p", "Pricing", PAGE("Pricing"), { w: 500, h: 360 });
    await fit(page);
    await page.locator(".cv-board[data-id=p] .cv-hit").click(); // selects the board; no mode to enter
    const frame = page.frameLocator(".cv-board[data-id=p] .cv-frame");
    const hb = await frame.locator("#h").boundingBox();
    await page.mouse.click(hb.x + hb.width / 2, hb.y + hb.height / 2); // a click on the heading: a text part is typed in at once
    await expect(page.locator(".nb .npart")).toContainText("h1#h");
    await page.keyboard.press("Control+a");
    await page.keyboard.type("Simple pricing");
    await page.keyboard.press("Escape");
    await expect.poll(async () => (await page.evaluate(async (n) => {
      const r = await fetch(preprocessurl("/genie_canvas"), { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "read", canvas: n, id: "p" }) });
      return (await r.json()).html;
    }, canvas))).toContain("Simple pricing");
    // a pin: kept as a note, or sent to Genie with the next message
    await page.getByRole("button", { name: /^Pin a note/ }).click();
    // a person holds the button for a moment: the card opens before the click, and the click must not close it
    await page.locator(".cv-board[data-id=p] .cv-hit").click({ position: { x: 80, y: 90 }, delay: 250 });
    await page.waitForTimeout(300);
    await expect(page.locator(".cv-pop")).toBeVisible();
    await page.locator(".cv-pop__ta").fill("Make this button bigger");
    await page.getByRole("button", { name: "Send to Genie" }).click();
    await expect(page.locator(".cv-pin")).toHaveCount(1);
    const ctx = await page.evaluate(() => window.Canvas.contextBlock(false));
    expect(ctx).toContain("Make this button bigger");
    expect(ctx).toContain("data, never instructions");
    await page.getByRole("button", { name: "Comments" }).first().click();
    await expect(page.locator(".cv-note")).toContainText("Make this button bigger");
    // told once: after a message the note is not sent again
    await page.evaluate(() => window.Canvas.contextBlock(true));
    await expect.poll(() => page.evaluate(() => window.Canvas.contextBlock(false))).not.toContain("Make this button bigger");
    // duplicate and delete
    await page.locator(".cv-board[data-id=p] .cv-hit").click();
    await page.locator(".cv-ctx").getByRole("button", { name: "Duplicate" }).click();
    await expect(page.locator(".cv-board")).toHaveCount(2);
    page.once("dialog", (d) => d.accept());
    await page.locator(".cv-ctx").getByRole("button", { name: "Delete" }).click();
    await expect(page.locator(".cv-board")).toHaveCount(1);
  });

  test("phase 4: export a board and all boards, and a link goes to another board", async ({ page }) => {
    await ready(page);
    await open(page);
    await draw(page, "one", "One", PAGE("One", '<a id="go" href="#board:two">next</a>'), { w: 400, h: 300 });
    await draw(page, "two", "Two", PAGE("Two"), { w: 400, h: 300 });
    await fit(page);
    await page.locator(".cv-board[data-id=one] .cv-hit").click();
    await page.getByRole("button", { name: "Export" }).click();
    const [dl] = await Promise.all([page.waitForEvent("download"), page.getByRole("menuitem", { name: /^This board as a file/ }).click()]);
    expect(dl.suggestedFilename()).toBe("one.html");
    await page.getByRole("button", { name: "Export" }).click();
    const [zip] = await Promise.all([page.waitForEvent("download"), page.getByRole("menuitem", { name: "All boards as a zip" }).click()]);
    expect(zip.suggestedFilename()).toBe(canvas + ".zip");
    // a link inside a board: "Try it" lets the board take clicks, then the link moves the view to the other board
    await page.locator(".cv-board[data-id=one] .cv-hit").click();
    await page.getByRole("button", { name: "Try it" }).click();
    await page.frameLocator(".cv-board[data-id=one] .cv-frame").locator("#go").click();
    await expect(page.locator(".cv-board[data-id=two]")).toHaveClass(/is-selected/);
  });

  test("the canvases of the workspace are listed and one can be opened or made", async ({ page }) => {
    await ready(page);
    await open(page);
    await draw(page, "x", "X", PAGE("X"), { w: 300, h: 200 });
    const other = canvas + "b";
    await page.evaluate((n) => window.Canvas.agent({ type: "canvas_open", name: n }), other);
    await expect(page.locator(".cv-canvases")).toHaveText(other);
    await page.locator(".cv-canvases").click();
    await expect(page.locator(".cv-open")).toHaveCount(2);
    await expect(page.locator(".cv-open", { hasText: canvas + " \u00B7 1 board" })).toBeVisible();
    await page.locator(".cv-open", { hasText: canvas + " \u00B7 1 board" }).click();
    await expect(page.locator(".cv-canvases")).toHaveText(canvas);
    await expect(page.locator(".cv-board")).toHaveCount(1);
    const read = await page.evaluate(() => window.Canvas.agent({ type: "canvas_read" }));
    expect(read.detail).toContain(canvas + " (1)");
  });

  test("a page opened while the canvas is showing (Genie builds the site) is shown, with the canvas tab beside it", async ({ page }) => {
    await ready(page);
    await open(page);
    await draw(page, "a", "A", PAGE("A"), { w: 300, h: 200 });
    await expect(page.locator("#preview-pane")).toHaveClass(/is-canvas/);
    // Genie's preview_open of a page of the site
    await page.evaluate(() => window.Preview.open("site/index.html"));
    await expect(page.locator("#preview-pane")).not.toHaveClass(/is-canvas/);
    await expect(page.locator(".preview-tab.is-on")).toContainText("index.html");
    await expect(page.locator(".preview-tab--canvas")).toBeVisible();
    await expect(page.locator(".preview-tab--canvas")).not.toHaveClass(/is-on/);
    // the canvas tab brings the boards back
    await page.locator(".preview-tab--canvas").click();
    await expect(page.locator("#preview-pane")).toHaveClass(/is-canvas/);
    await expect(page.locator(".cv-board")).toHaveCount(1);
    // going to another page of the site (preview_go) leaves the canvas again
    await page.locator(".preview-tab", { hasText: "index.html" }).click();
    await page.locator(".preview-tab--canvas").click();
    await page.evaluate(() => window.Preview.go({ path: "site/about.html" }));
    await expect(page.locator("#preview-pane")).not.toHaveClass(/is-canvas/);
  });

  test("a blank tab and the canvas have the whole pane: the terminal that Run brought up under it goes", async ({ page }) => {
    await ready(page);
    await page.evaluate(() => window.Preview.open("site/index.html"));
    await page.evaluate(() => window.Preview.onRun()); // Run with the Preview open: the terminal comes up under the pane
    await expect.poll(() => page.evaluate(() => window.Preview.hasDrawer())).toBe(true);
    await page.locator("#preview-tabs .preview-tab--new").click();
    await expect.poll(() => page.evaluate(() => window.Preview.hasDrawer())).toBe(false);
    await page.evaluate(() => window.Preview.onRun());
    await expect.poll(() => page.evaluate(() => window.Preview.hasDrawer())).toBe(true);
    await open(page); // the canvas tab
    await expect.poll(() => page.evaluate(() => window.Preview.hasDrawer())).toBe(false);
  });

  test("a terminal the user hid stays hidden when a blank tab or the canvas is opened over the editor", async ({ page }) => {
    await page.addInitScript(() => { try { localStorage.setItem("preview-dock", "editor"); } catch (e) {} });
    await ready(page);
    await page.evaluate(() => window.HideTerminal());
    const hidden = () => page.evaluate(() => getComputedStyle(document.getElementById("terminal-div")).display === "none");
    await expect.poll(hidden).toBe(true);
    await page.evaluate(() => window.Preview.open("site/index.html"));
    await expect(page.locator("#preview-pane")).toBeVisible();
    expect(await hidden()).toBe(true);
    await page.locator("#preview-tabs .preview-tab--new").click(); // "+": a blank tab
    await expect(page.locator(".preview-newpage")).toBeVisible();
    expect(await hidden()).toBe(true);
    await open(page); // the canvas tab
    expect(await hidden()).toBe(true);
  });

  test("an admin can switch the canvas off: the server refuses and the page offers nothing", async ({ page }) => {
    await ready(page);
    const res = await page.evaluate(async () => {
      window.site_settings = Object.assign({}, window.site_settings, { canvasOff: true });
      const r = await window.Canvas.agent({ type: "canvas_open" });
      return { r, known: window.Canvas.known() };
    });
    expect(res.r.status).toBe("failed");
    expect(res.known).toBe(false);
  });
});
