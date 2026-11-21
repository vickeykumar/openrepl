// The Activity panel of the Genie panel (chat-widget/src/index.ts, docs/lld/19-genie-agent.md section 26): one card under the list icon before Close,
// with Peer chat, Context, Connections and the person's own recent calls.
//
// GENIE_LOCAL_ASSETS=1 serves the widget from this checkout's build.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const { waitForTerminalReady } = require("./helpers");

const root = path.join(__dirname, "..", "..", "src");

async function localAssets(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "resources/chat-widget/dist/index.umd.js")) }));
}

async function openPanel(page) {
  await localAssets(page);
  await page.route((u) => u.pathname.endsWith("/settings.js"), async (route) => {
    const res = await route.fetch();
    const extra = `;Object.assign(window.site_settings = window.site_settings || {}, { agentEnabled: true });document.addEventListener("DOMContentLoaded", function () { document.body.classList.add("is-signed-in"); });`;
    route.fulfill({ response: res, body: (await res.text()) + extra });
  });
  await page.route((u) => u.pathname.endsWith("/genie_conn"), (r) => {
    const b = JSON.parse(r.request().postData() || "{}");
    const ok = (o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
    if (b.op === "calls") return ok({ calls: [{ tool: "list_pull_requests", level: "read", ok: true, at: Math.floor(Date.now() / 1000) - 60 }] });
    return ok({ ok: true });
  });
  await page.goto("/");
  await page.waitForFunction(() => window.ChatWidget && window.editor && window.editor.env.editor);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
  await page.evaluate(() => window.ChatWidget.open());
  await expect(page.locator("#chat-widget__input")).toBeVisible();
}

test("the header has the Activity button before Close, and no Context pill or Peer chat switch", async ({ page }) => {
  await openPanel(page);
  await expect(page.locator("#chat-widget__act-btn")).toBeVisible();
  await expect(page.locator("#chat-widget__act-btn")).toHaveAttribute("aria-label", /Activity panel/);
  await expect(page.locator("#chat-widget__act")).toBeHidden();
  await expect(page.locator("header #chat-widget__ctx-btn, .chat-widget__header #chat-widget__ctx-btn")).toHaveCount(0);
});

test("the card opens with the button, shows Session, Connections and Recent calls, and closes with Esc, the button or a click outside", async ({ page }) => {
  await openPanel(page);
  const card = page.locator("#chat-widget__act");
  await page.locator("#chat-widget__act-btn").click();
  await expect(card).toBeVisible();
  await expect(card.locator("#peerchat-switch")).toHaveCount(1);
  await expect(card.locator("#chat-widget__ctx-btn")).toBeVisible();
  await expect(card.locator("#chat-widget__act-conns")).toBeVisible();
  await expect(card.locator("#chat-widget__act-calls")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(card).toBeHidden();
  await expect(page.locator("#chat-widget__input")).toBeVisible();
  await page.locator("#chat-widget__act-btn").click();
  await expect(card).toBeVisible();
  await page.locator("#chat-widget__act-btn").click();
  await expect(card).toBeHidden();
  await page.locator("#chat-widget__act-btn").click();
  await page.locator("#chat-widget__input").click();
  await expect(card).toBeHidden();
});

test("Context opens inside the card with a Back arrow", async ({ page }) => {
  await openPanel(page);
  await page.locator("#chat-widget__act-btn").click();
  await page.locator("#chat-widget__ctx-btn").click();
  const box = page.locator("#chat-widget__ctx");
  await expect(box).toBeVisible();
  await expect(page.locator("#chat-widget__act-main")).toBeHidden();
  await box.getByRole("button", { name: "Back to the Activity panel" }).click();
  await expect(box).toBeHidden();
  await expect(page.locator("#chat-widget__act-main")).toBeVisible();
});

test("with nobody else in the session the card says so and offers Share; a connection row opens and closes its tools", async ({ page }) => {
  await openPanel(page);
  await page.locator("#chat-widget__act-btn").click();
  await expect(page.locator("#chat-widget__act-people")).toContainText("Nobody else here");
  await expect(page.getByRole("button", { name: "Share this session" })).toBeVisible();
  const rows = page.locator("#chat-widget__act-conns .cw-act__row--btn");
  if (await rows.count()) {
    const first = rows.first();
    await expect(first).toHaveAttribute("aria-expanded", "false");
    await expect(page.locator("#chat-widget__act-conns .cw-act__exp").first()).toBeHidden();
    await first.click();
    await expect(page.locator("#chat-widget__act-conns .cw-act__exp").first()).toBeVisible();
    await first.click();
    await expect(page.locator("#chat-widget__act-conns .cw-act__exp").first()).toBeHidden();
  }
});
