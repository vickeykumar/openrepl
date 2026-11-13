// The agent's changes to the editor without a review, and the choice in its review card (src/js/src/page/18-genie-review.js,
// the "Genie agent: remember my answers for the session" design): applyDirect puts the text in and can undo it; the review card of an
// agent change offers "apply later changes without asking" while the agent allows it.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.GenieReview && window.GenieAgent);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

const editorText = (page) => page.evaluate(() => window.editor.env.editor.getValue());
const setText = (page, t) => page.evaluate((t) => window.editor.env.editor.setValue(t, -1), t);

test("applyDirect puts Genie's text in the editor at once and undo puts the old text back", async ({ page }) => {
  await ready(page);
  await setText(page, "a\nb\nc\n");
  const out = await page.evaluate(() => {
    const d = window.GenieReview.applyDirect({ text: "a\nB\nc\nd\n" });
    window.__undo = d.undo;
    return { total: d.total, accepted: d.accepted, rejected: d.rejected, hasUndo: typeof d.undo === "function" };
  });
  expect(out).toEqual({ total: 2, accepted: 2, rejected: 0, hasUndo: true });
  expect(await editorText(page)).toBe("a\nB\nc\nd\n");
  expect(await page.evaluate(() => window.GenieReview.isOpen())).toBe(false); // no review was shown
  expect(await page.evaluate(() => window.__undo())).toBe(true);
  expect(await editorText(page)).toBe("a\nb\nc\n");
});

test("undo says so and does nothing when the code was changed since", async ({ page }) => {
  await ready(page);
  await setText(page, "one\n");
  await page.evaluate(() => { window.__undo = window.GenieReview.applyDirect({ text: "two\n" }).undo; });
  await setText(page, "two and the user's own typing\n");
  expect(await page.evaluate(() => window.__undo())).toBe(false);
  expect(await editorText(page)).toBe("two and the user's own typing\n");
  await expect(page.locator(".notice", { hasText: "cannot be undone as one step" })).toBeVisible();
});

test("an insert is applied at the cursor, and the same text is a no-change", async ({ page }) => {
  await ready(page);
  await setText(page, "x = 1\n");
  const r = await page.evaluate(() => { const d = window.GenieReview.applyDirect({ insert: "y = 2" }); return { total: d.total }; });
  expect(r.total).toBeGreaterThan(0);
  expect(await editorText(page)).toContain("y = 2");
  const same = await page.evaluate(() => window.GenieReview.applyDirect({ text: window.editor.env.editor.getValue() }).total);
  expect(same).toBe(0);
});

test("the review card of an agent change offers to apply later changes without asking, and Accept all with it ticked says so to the agent", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => {
    window.__enabled = 0;
    window.GenieAgent = { canAutoApply: () => true, enableAutoApply: () => { window.__enabled++; } };
  });
  await setText(page, "a\nb\n");
  await page.evaluate(() => { window.GenieReview.propose("a\nc\n", { title: "Genie wants to replace the file:", agent: true }); });
  const box = page.locator("#genie-review-auto");
  await expect(box).toBeVisible();
  await expect(page.locator(".genie-review__auto")).toContainText("apply Genie's later changes without asking");
  await box.check();
  await page.locator(".genie-review__head button", { hasText: "Accept all" }).click();
  expect(await page.evaluate(() => window.__enabled)).toBe(1);
  expect(await editorText(page)).toBe("a\nc\n");
});

test("without the tick, Accept all does not change what the agent may do; a review that is not the agent's has no such choice", async ({ page }) => {
  await ready(page);
  await page.evaluate(() => {
    window.__enabled = 0;
    window.GenieAgent = { canAutoApply: () => true, enableAutoApply: () => { window.__enabled++; } };
  });
  await setText(page, "a\nb\n");
  await page.evaluate(() => { window.GenieReview.propose("a\nc\n", { title: "Genie wants to replace the file:", agent: true }); });
  await page.locator(".genie-review__head button", { hasText: "Accept all" }).click();
  expect(await page.evaluate(() => window.__enabled)).toBe(0);
  await setText(page, "a\nb\n");
  await page.evaluate(() => { window.GenieReview.propose("a\nz\n", { title: "Genie proposes" }); }); // a chat answer's change, not the agent's
  await expect(page.locator(".genie-review")).toBeVisible();
  await expect(page.locator("#genie-review-auto")).toHaveCount(0);
  await page.evaluate(() => {
    window.GenieAgent = { canAutoApply: () => false, enableAutoApply: () => {} };
    window.GenieReview.propose("a\nq\n", { title: "Genie wants to replace the file:", agent: true });
  });
  await expect(page.locator("#genie-review-auto")).toHaveCount(0); // the admin switched it off
});
