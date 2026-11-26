// Tools on a part of a board (page/canvas-parts.js, the helper in page/lib-canvas.mjs, docs/lld/21-genie-canvas.md "Tools on a part"; boards 11 to 15 of the canvas design):
// Select part mode and the floating toolbar, the panels (fill, corner, text, layout, size, spacing, border and shadow, opacity), lock, note, more, picture, align,
// order, make it a style, hide, the responsive check, and what Genie is told.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const canvas = `cp${stamp}`;
const BOARD = `<!doctype html><html><head><meta charset="utf-8"><title>Pricing</title></head><body style="margin:0;font-family:sans-serif"><div class="card" id="card" style="padding:24px"><h1 id="t">Pricing</h1><p class="sub" id="sub">Simple</p><div class="row" id="row"><button class="buy" id="buy" style="background:#FE6A6B;color:#fff;border:0;padding:10px 16px">Buy</button><button class="alt" id="alt" style="background:#FE6A6B;color:#fff;border:0;padding:10px 16px">Alt</button><span id="third" style="display:inline-block;width:40px;height:20px;background:#ddd">x</span></div><img id="pic" alt="" width="60" height="40"></div></body></html>`;

test.use({ viewport: { width: 1440, height: 1300 } });

async function ready(page) {
  const root = require("path").join(__dirname, "..", "..", "src");
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: require("fs").readFileSync(root + "/js/dist/scribbler.js") }));
    await page.route((u) => u.pathname.endsWith("/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: require("fs").readFileSync(root + "/resources/css/ui-refresh.css") }));
  }
  await page.addInitScript(() => { try { localStorage.setItem("preview-dock", "editor"); } catch (e) {} });
  await page.goto("/");
  await page.waitForFunction(() => window.Canvas && window.Preview);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.evaluate((n) => window.Canvas.agent({ type: "canvas_open", name: n }), canvas);
  await page.evaluate((h) => window.Canvas.agent({ type: "canvas_board", board: "p", title: "Pricing", html: h, w: 600, h: 420 }), BOARD);
  await expect(page.locator(".cv-board")).toHaveCount(1);
  await page.getByRole("button", { name: "Fit all" }).click();
  await page.locator(".cv-board[data-id=p] .cv-hit").click(); // a selected board is in part mode at once: no button to press
  await expect(page.locator(".cv-ctx")).toBeVisible();
}

const frame = (page) => page.frameLocator(".cv-board[data-id=p] .cv-frame");
const bar = (page) => page.locator(".nb");
const tool = (page, name) => bar(page).getByRole("button", { name, exact: true });
// A part is picked with a click on it. The toolbar can float over the neighbours of the picked part, so a test sends the click to the element itself.
async function pick(page, sel, opts) {
  const shift = !!(opts && opts.modifiers && opts.modifiers.includes("Shift"));
  const name = await frame(page).locator(sel).evaluate((e) => e.tagName.toLowerCase() + (e.id ? "#" + e.id : ""));
  await frame(page).locator(sel).dispatchEvent("click", { shiftKey: shift, bubbles: true });
  if (!shift) await expect(bar(page).locator(".npart")).toContainText(name);
  await expect(bar(page)).toBeVisible();
}
async function board(page) {
  return page.evaluate(async (n) => {
    const r = await fetch(preprocessurl("/genie_canvas"), { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "read", canvas: n, id: "p" }) });
    return (await r.json()).html;
  }, canvas);
}
async function log(page) {
  return page.evaluate(async (n) => {
    const r = await fetch(preprocessurl("/genie_canvas"), { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op: "state", canvas: n }) });
    return (await r.json()).log.map((l) => l.text);
  }, canvas);
}
async function keep(page) { await page.locator(".fp").getByRole("button", { name: "Keep" }).click(); await expect(page.locator(".fp")).toHaveCount(0); }
const el = (page, sel) => frame(page).locator(sel);

test.describe("tools on a part", () => {
  test("select a part: an outline with its name, the toolbar in three groups, the parent on a second click, several parts with Shift", async ({ page }) => {
    await ready(page);
    await pick(page, "#buy");
    await expect(bar(page).locator(".npart")).toContainText("button#buy");
    for (const g of ["Look", "Shape", "Guard"]) await expect(bar(page).locator(".tgn", { hasText: g })).toBeVisible();
    for (const b of ["Fill", "Corner", "Text", "Layout", "Size", "Lock", "Note", "More"]) await expect(tool(page, b)).toBeVisible();
    await pick(page, "#row");
    await frame(page).locator("#row").dispatchEvent("click", { bubbles: true }); // again: its parent
    await expect(bar(page).locator(".npart")).toContainText("div#card");
    await pick(page, "#t");
    await pick(page, "#sub", { modifiers: ["Shift"] });
    await expect(bar(page).locator(".npart")).toContainText("2 parts");
    await expect(bar(page).locator(".tgn", { hasText: "Line up" })).toBeVisible();
    await page.keyboard.press("Escape");
  });

  test("no Select part or Edit text button: a click on a board picks the part, and a text part is typed in at once", async ({ page }) => {
    await ready(page);
    await expect(page.locator(".cv-ctx").getByRole("button", { name: "Select part" })).toHaveCount(0);
    await expect(page.locator(".cv-ctx").getByRole("button", { name: "Edit text" })).toHaveCount(0);
    const hb = await frame(page).locator("#t").boundingBox();
    await page.mouse.click(hb.x + hb.width / 2, hb.y + hb.height / 2); // a real click on the board, on its heading
    await expect(bar(page).locator(".npart")).toContainText("h1#t");
    await page.keyboard.type("Plans and ");
    await expect(el(page, "#t")).toContainText("Plans and ");
    await el(page, "#sub").dispatchEvent("click", { bubbles: true }); // another part: the change is saved
    await expect.poll(async () => await board(page)).toContain("Plans and ");
    await expect.poll(async () => await log(page)).toContain("you changed the text of h1#t");
    // the toolbar shows with the text part selected, and Esc lets go step by step
    await expect(bar(page)).toBeVisible();
  });

  test("fill, corner, text, layout and size are changed with a live preview, kept as a version, and Esc drops a change", async ({ page }) => {
    await ready(page);
    await pick(page, "#buy");
    await tool(page, "Fill").click();
    await page.locator(".fp__hex").first().fill("#1b6ef3");
    await expect.poll(async () => el(page, "#buy").evaluate((e) => getComputedStyle(e).backgroundColor)).toBe("rgb(27, 110, 243)");
    await page.locator(".fp").getByRole("button", { name: "Esc" }).click(); // dropped
    await expect.poll(async () => el(page, "#buy").evaluate((e) => getComputedStyle(e).backgroundColor)).toBe("rgb(254, 106, 107)");
    await tool(page, "Fill").click();
    await page.locator(".fp__hex").first().fill("#1b6ef3");
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/rgb\(27, 110, 243\)/);
    expect(await board(page)).not.toMatch(/cursor:/); // the helper's own cursor is never saved in the page
    expect(await log(page)).toContain("you changed the fill of button#buy to #1b6ef3");

    await tool(page, "Corner").click();
    await page.locator(".fp").getByRole("spinbutton").first().fill("14");
    await keep(page);
    await expect.poll(async () => await board(page)).toContain("border-radius: 14px");

    await tool(page, "Text").click();
    await page.locator(".fp").getByRole("spinbutton").first().fill("22");
    await page.locator(".fp").getByRole("combobox").first().selectOption("700");
    await keep(page);
    await expect.poll(async () => await board(page)).toContain("font-size: 22px");
    const html = await board(page);
    expect(html).toContain("font-size: 22px");
    expect(html).toContain("font-weight: 700");

    await pick(page, "#row");
    await tool(page, "Layout").click();
    await page.locator(".fp").getByRole("button", { name: "Stack" }).click();
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/id="row"[^>]*style="[^"]*flex-direction: column/);

    await pick(page, "#alt");
    await tool(page, "Size").click();
    await page.locator(".fp").getByRole("combobox").first().selectOption("fill");
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/id="alt"[^>]*style="[^"]*width: 100%/);
  });

  test("spacing, border and shadow, opacity from the More menu", async ({ page }) => {
    await ready(page);
    await pick(page, "#sub");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Spacing…" }).click();
    await page.locator(".fp__box").first().getByRole("spinbutton").first().fill("12");
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/id="sub"[^>]*style="[^"]*padding-top: 12px/);
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Border and shadow…" }).click();
    await page.locator(".fp").getByRole("button", { name: "Lifted" }).click();
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/id="sub"[^>]*style="[^"]*box-shadow: rgba\(0, 0, 0, 0\.22\) 0px 10px 24px/);
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Opacity…" }).click();
    await page.locator(".fp").getByRole("spinbutton").first().fill("40");
    await keep(page);
    await expect.poll(async () => await board(page)).toMatch(/id="sub"[^>]*style="[^"]*opacity: 0\.4/);
  });

  test("lock: a locked part refuses the tools and Genie, an unlocked page is accepted, and Genie is told which parts are locked", async ({ page }) => {
    await ready(page);
    await pick(page, "#t");
    await tool(page, "Lock").click();
    await expect(tool(page, "Unlock")).toBeVisible();
    await expect(tool(page, "Fill")).toBeDisabled();
    await expect.poll(async () => await board(page)).toContain("data-locked");
    await expect.poll(async () => await log(page)).toContain("you locked h1#t");
    // Genie rewriting the board must keep the locked heading as it is
    const changed = BOARD.replace('<h1 id="t">Pricing</h1>', '<h1 id="t">Changed</h1>');
    const refused = await page.evaluate((h) => window.Canvas.agent({ type: "canvas_board", board: "p", title: "Pricing", html: h }), changed);
    expect(refused.status).toBe("failed");
    expect(refused.detail).toContain("locked");
    expect(refused.detail).toContain("h1#t");
    const live = await board(page);
    const keepLocked = live.replace("Simple", "Simple and clear");
    const ok = await page.evaluate((h) => window.Canvas.agent({ type: "canvas_board", board: "p", title: "Pricing", html: h }), keepLocked);
    expect(ok.status).toBe("done");
    const ctx = await page.evaluate(() => window.Canvas.contextBlock(false));
    expect(ctx).toContain("locked parts");
    expect(ctx).toContain("h1#t");
    expect(ctx).toContain("the user selected this part");
    // unlock
    await tool(page, "Unlock").click();
    await expect(tool(page, "Lock")).toBeVisible();
    await expect.poll(async () => await board(page)).not.toContain("data-locked");
  });

  test("note on a part opens the note card with the part under it", async ({ page }) => {
    await ready(page);
    await pick(page, "#buy");
    await tool(page, "Note").click();
    await expect(page.locator(".cv-pop")).toBeVisible();
    await expect(page.locator(".cv-pop__el")).toContainText("button");
    await page.locator(".cv-pop__ta").fill("Make this bigger");
    await page.getByRole("button", { name: "Note", exact: true }).last().click();
    await expect(page.locator(".cv-pin")).toHaveCount(1);
  });

  test("more: duplicate, wrap, hide and show all, delete, copy and paste style, order", async ({ page }) => {
    await ready(page);
    await pick(page, "#alt");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Duplicate part" }).click();
    await expect(el(page, "button.alt")).toHaveCount(2);
    await pick(page, "#third");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Wrap in a container" }).click();
    await expect(el(page, "#row > div > #third")).toHaveCount(1);
    await pick(page, "#sub");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Hide part" }).click();
    await expect(el(page, "#sub")).toBeHidden();
    await pick(page, "#t");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: /^Show all hidden parts/ }).click();
    await expect(el(page, "#sub")).toBeVisible();
    // copy the style of the Buy button and paste it on the heading
    await pick(page, "#buy");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Copy style" }).click();
    await pick(page, "#t");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Paste style" }).click();
    await expect.poll(async () => el(page, "#t").evaluate((e) => getComputedStyle(e).backgroundColor)).toBe("rgb(254, 106, 107)");
    // order
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "To the front" }).click();
    await expect.poll(async () => await board(page)).toMatch(/id="t"[^>]*style="[^"]*z-index: 1/);
    // delete asks first
    await pick(page, "#third");
    page.once("dialog", (d) => d.accept());
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Delete part" }).click();
    await expect(el(page, "#third")).toHaveCount(0);
  });

  test("a picture is put on a part (kept in the board), too big a picture is refused", async ({ page }) => {
    await ready(page);
    await pick(page, "#pic");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Picture…" }).click();
    const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
    await page.locator('.fp input[type=file]').setInputFiles({ name: "dot.png", mimeType: "image/png", buffer: png });
    await expect(page.locator(".fp__hint").first()).toContainText("dot.png is ready");
    await page.locator(".fp").getByRole("button", { name: "Keep" }).click();
    await expect.poll(async () => await board(page)).toContain("data:image/png;base64,");
    // the part stays picked after a change
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Picture…" }).click();
    await page.locator('.fp input[type=file]').setInputFiles({ name: "big.png", mimeType: "image/png", buffer: Buffer.alloc(260 * 1024, 1) });
    await expect(page.locator(".fp__hint").first()).toContainText("limit is 200 KB");
  });

  test("align several parts, and make a style that updates every part with the same value", async ({ page }) => {
    await ready(page);
    await pick(page, "#buy");
    await pick(page, "#t", { modifiers: ["Shift"] });
    await tool(page, "Align").click();
    await page.locator(".fp").getByRole("button", { name: "Align right edges" }).click();
    await expect.poll(async () => {
      const a = await el(page, "#buy").boundingBox(), b = await el(page, "#t").boundingBox();
      return Math.round(a.x + a.width) - Math.round(b.x + b.width);
    }).toBe(0);
    expect(await log(page)).toContain("you aligned 2 parts");
    // a style
    await pick(page, "#buy");
    await tool(page, "More").click();
    await page.locator(".fp").getByRole("button", { name: "Make it a style…" }).click();
    await page.locator(".fp").getByRole("textbox").fill("brand");
    await page.locator(".fp").getByRole("button", { name: "Keep" }).click();
    await expect.poll(async () => await board(page)).toContain("--brand:");
    const html = await board(page);
    expect(html).toContain("var(--brand)");
    expect((html.match(/var\(--brand\)/g) || []).length).toBeGreaterThanOrEqual(2); // the Buy and the Alt button had the same colour
  });

  test("the canvas can be popped out into a window of its own and brought back", async ({ page, context }) => {
    await ready(page);
    const [pop] = await Promise.all([context.waitForEvent("page"), page.evaluate(() => window.Canvas.popOut())]);
    await pop.waitForLoadState();
    await expect(pop.locator("#canvas-root")).toBeVisible();
    await expect(pop.locator(".cv-board")).toHaveCount(1);
    await expect(page.locator(".cv-away")).toBeVisible();
    // it works there: the board loads again, a part can be picked and its toolbar is in that window
    await expect(pop.frameLocator(".cv-board[data-id=p] .cv-frame").locator("#t")).toHaveText("Pricing");
    // Select part mode was on before the move and stays on there
    await pop.frameLocator(".cv-board[data-id=p] .cv-frame").locator("#buy").dispatchEvent("click", { bubbles: true });
    await expect(pop.locator(".nb")).toBeVisible();
    await page.getByRole("button", { name: "Bring it back" }).click();
    await expect(page.locator("#preview-pane #canvas-root")).toBeVisible();
    await expect(page.locator(".cv-away")).toHaveCount(0);
    await expect(page.locator(".cv-board")).toHaveCount(1);
  });

  test("Try it: the board takes clicks like a page, so its buttons, scripts and links to other boards work", async ({ page }) => {
    await ready(page);
    const TRY = `<!doctype html><html><body style="margin:0;font-family:sans-serif"><h1 id="h">Hello</h1><button id="go" style="padding:10px 16px" onclick="document.getElementById('h').textContent='Clicked'">Click me</button> <a id="next" href="#board:q" style="margin-left:12px">Next board</a></body></html>`;
    await page.evaluate((h) => window.Canvas.agent({ type: "canvas_board", board: "p", title: "Pricing", html: h, w: 600, h: 420 }), TRY);
    await page.evaluate(() => window.Canvas.agent({ type: "canvas_board", board: "q", title: "Second", html: "<h1>Second board</h1>", w: 300, h: 200 }));
    await page.getByRole("button", { name: "Fit all" }).click();
    await page.locator(".cv-board[data-id=p] .cv-hit").click({ position: { x: 20, y: 200 } });
    // before Try it the board is arranged, not used: a click only picks a part
    await frame(page).locator("#go").dispatchEvent("click", { bubbles: true });
    await expect(el(page, "#h")).toHaveText("Hello");
    await page.locator(".cv-ctx").getByRole("button", { name: "Try it" }).click();
    await expect(page.locator(".cv-board[data-id=p]")).toHaveClass(/is-live/);
    await expect(page.locator(".cv-board[data-id=p] .cv-hit")).toBeHidden();
    const gb = await frame(page).locator("#go").boundingBox();
    await page.mouse.click(gb.x + gb.width / 2, gb.y + gb.height / 2);
    await expect(el(page, "#h")).toHaveText("Clicked");
    const nb = await frame(page).locator("#next").boundingBox();
    await page.mouse.click(nb.x + nb.width / 2, nb.y + nb.height / 2);
    await expect(page.locator(".cv-board[data-id=q]")).toHaveClass(/is-selected/);
    // clicking the empty canvas ends it
    await page.locator(".cv-view").click({ position: { x: 5, y: 5 } });
    await expect(page.locator(".cv-board[data-id=p]")).not.toHaveClass(/is-live/);
  });

  test("closing the popped-out window, even in the middle of a drag, brings the canvas back alive", async ({ page, context }) => {
    await ready(page);
    const [pop] = await Promise.all([context.waitForEvent("page"), page.evaluate(() => window.Canvas.popOut())]);
    await pop.waitForLoadState();
    await expect(pop.locator(".cv-board")).toHaveCount(1);
    await pop.locator(".cv-view").hover({ position: { x: 30, y: 30 } });
    await pop.mouse.down();
    await pop.mouse.move(120, 140, { steps: 4 });
    await pop.evaluate(() => window.close());
    await expect(page.locator("#preview-pane #canvas-root")).toBeVisible();
    await expect(page.locator(".cv-away")).toHaveCount(0);
    await expect(frame(page).locator("#t")).toHaveText("Pricing"); // the board loaded again
    await page.getByRole("button", { name: "Fit all" }).click();
    await page.locator(".cv-board[data-id=p] .cv-hit").click(); // the drag in the other window began on the empty canvas, which deselected the board
    await expect(async () => { await el(page, "#buy").dispatchEvent("click", { bubbles: true }); await expect(bar(page)).toBeVisible({ timeout: 1500 }); }).toPass({ timeout: 15000 }); // the board is picked again once its page is back
    const before = await page.locator(".cv-stage").evaluate((e) => e.style.transform);
    await page.mouse.move(600, 800); // the pointer is not held: moving it must not pan the canvas
    expect(await page.locator(".cv-stage").evaluate((e) => e.style.transform)).toBe(before);
  });

  test("Try it on a board with nothing to click says so", async ({ page }) => {
    await ready(page);
    await page.evaluate(() => window.Canvas.agent({ type: "canvas_board", board: "p", title: "Pricing", html: "<!doctype html><body><h1>Static</h1><p>Only text</p></body>", w: 600, h: 420 }));
    await page.getByRole("button", { name: "Fit all" }).click();
    await page.locator(".cv-board[data-id=p] .cv-hit").click({ position: { x: 20, y: 200 } });
    await page.locator(".cv-ctx").getByRole("button", { name: "Try it" }).click();
    await expect(page.locator(".cv-status")).toContainText("nothing to click");
  });

  test("the responsive check shows the board at a phone width without changing its size; the helper refuses a bad value", async ({ page }) => {
    await ready(page);
    await page.locator(".cv-ctx").getByRole("button", { name: "Responsive" }).click();
    await page.getByRole("menuitem", { name: /^Phone/ }).click();
    expect(await page.locator(".cv-board[data-id=p] .cv-frame").evaluate((e) => e.style.width)).toBe("390px");
    const b = await page.evaluate(() => window.Canvas.state().boards[0]);
    expect(b.w).toBe(600);
    await page.locator(".cv-ctx").getByRole("button", { name: "Responsive" }).click();
    await page.getByRole("menuitem", { name: /^The board's own size/ }).click();
    expect(await page.locator(".cv-board[data-id=p] .cv-frame").evaluate((e) => e.style.width)).toBe("600px");
    // a value that is not plain CSS never reaches the page
    await pick(page, "#buy");
    await page.evaluate(() => { const ifr = document.querySelector(".cv-board[data-id=p] .cv-frame"); ifr.contentWindow.postMessage({ canvas: 1, c: "apply", live: false, label: "x", items: [{ p: "body > div:nth-child(1) > div:nth-child(3) > button:nth-child(1)", props: { "background-image": "url(https://evil.example/x)", width: "10px; position: fixed" } }] }, "*"); });
    await page.waitForTimeout(600);
    const html = await board(page);
    expect(html).not.toContain("evil.example");
    expect(html).not.toContain("position: fixed");
  });

  test("a drag that starts on the picked part moves that part and not the board; Esc cancels it; the arrow keys nudge it; a locked part stays", async ({ page }) => {
    await ready(page);
    const boardBox = () => page.locator(".cv-board[data-id=p]").boundingBox();
    const centre = async (sel) => { const b = await frame(page).locator(sel).boundingBox(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }; };
    // a real click picks the part, then a drag on it moves it
    let c = await centre("#buy");
    await page.mouse.click(c.x, c.y);
    await expect(bar(page).locator(".npart")).toContainText("button#buy");
    const b0 = await boardBox();
    await page.mouse.move(c.x, c.y);
    await page.mouse.down();
    await page.mouse.move(c.x + 40, c.y + 20, { steps: 6 });
    await page.mouse.up();
    await expect.poll(async () => /translate:\s*-?\d+(\.\d+)?px -?\d+(\.\d+)?px/.test(await board(page))).toBe(true);
    const b1 = await boardBox();
    expect(Math.round(b1.x)).toBe(Math.round(b0.x));
    expect(Math.round(b1.y)).toBe(Math.round(b0.y));
    await expect.poll(async () => (await log(page)).some((l) => /you moved button#buy by/.test(l))).toBe(true);
    // Esc during a drag puts it back
    const before = await board(page);
    c = await centre("#buy");
    await page.mouse.move(c.x, c.y);
    await page.mouse.down();
    await page.mouse.move(c.x + 60, c.y + 60, { steps: 6 });
    await page.keyboard.press("Escape");
    await page.mouse.up();
    await page.waitForTimeout(300);
    expect(await board(page)).toBe(before);
    // the arrow keys move it by 1 px, 10 with Shift, and a run of presses is one change
    await pick(page, "#alt");
    await page.locator(".cv-view").focus();
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("Shift+ArrowDown");
    await expect.poll(async () => /id="alt"[^>]*translate:\s*1px 10px/.test(await board(page)), { timeout: 8000 }).toBe(true);
    // a locked part is not moved by a drag, and the board stays too
    await pick(page, "#third");
    await tool(page, "Lock").click();
    const b2 = await boardBox();
    c = await centre("#third");
    await page.mouse.move(c.x, c.y);
    await page.mouse.down();
    await page.mouse.move(c.x + 50, c.y + 30, { steps: 5 });
    await page.mouse.up();
    const b3 = await boardBox();
    expect(Math.round(b3.x)).toBe(Math.round(b2.x));
    expect(await board(page)).not.toMatch(/id="third"[^>]*translate/);
  });

  test("a picked part has handles: drag the right edge, the bottom edge or the corner to size it; Shift and drag a box picks the parts inside it", async ({ page }) => {
    await ready(page);
    const boardBox = () => page.locator(".cv-board[data-id=p]").boundingBox();
    const box = (sel) => frame(page).locator(sel).boundingBox();
    // the handles: the corner of #buy resizes it
    let b = await box("#buy");
    await page.mouse.click(b.x + b.width / 2, b.y + b.height / 2);
    await expect(bar(page).locator(".npart")).toContainText("button#buy");
    b = await box("#buy");
    const b0 = await boardBox();
    await page.mouse.move(b.x + b.width, b.y + b.height);
    await page.mouse.down();
    await page.mouse.move(b.x + b.width + 40, b.y + b.height + 24, { steps: 6 });
    await page.mouse.up();
    await expect.poll(async () => /id="buy"[^>]*width:\s*\d+px/.test(await board(page))).toBe(true);
    const grown = await box("#buy");
    expect(grown.width).toBeGreaterThan(b.width + 20);
    expect(grown.height).toBeGreaterThan(b.height + 10);
    const b1 = await boardBox();
    expect(Math.round(b1.x)).toBe(Math.round(b0.x));
    await expect.poll(async () => (await log(page)).some((l) => /you changed the size of button#buy to \d+ . \d+/.test(l))).toBe(true);
    // Esc during a resize puts it back
    const before = await board(page);
    b = await box("#buy");
    await page.mouse.move(b.x + b.width, b.y + b.height / 2);
    await page.mouse.down();
    await page.mouse.move(b.x + b.width + 50, b.y + b.height / 2, { steps: 5 });
    await page.keyboard.press("Escape");
    await page.mouse.up();
    await page.waitForTimeout(300);
    expect(await board(page)).toBe(before);
    // Shift and drag a box over the three parts of the row picks them
    const a = await box("#buy"), c = await box("#third");
    await page.keyboard.down("Shift");
    await page.mouse.move(a.x - 6, a.y - 6);
    await page.mouse.down();
    await page.mouse.move(c.x + c.width + 6, Math.max(a.y + a.height, c.y + c.height) + 6, { steps: 6 });
    await page.mouse.up();
    await page.keyboard.up("Shift");
    await expect(bar(page).locator(".npart")).toContainText("3 parts");
  });
});
