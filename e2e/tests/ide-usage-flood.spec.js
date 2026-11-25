// A program that prints without stopping is paused and then ended by the server (LLD 20, "The terminal's speed cap"): the user sees the notice,
// and a fresh terminal runs again. `yes` is the shortest flood there is. It is typed into the bash terminal, so it needs no editor.
const { test, expect } = require("@playwright/test");

async function terminalText(page) {
  const rows = page.locator("#terminal-div .xterm-rows");
  if ((await rows.count()) === 0) return "";
  return (await rows.first().innerText()).replace(/ /g, " ").replace(/\n/g, "");
}

test("a program that prints for ever is stopped with a notice, and a fresh terminal runs again", async ({ page }) => {
  test.setTimeout(180 * 1000);
  await page.goto("/bash");
  await expect(page.locator("#terminal-div .xterm-rows").first()).toContainText(/\S/, { timeout: 60 * 1000 });
  await page.waitForTimeout(2500); // the prompt is out
  await page.locator("#terminal-div").click();
  await page.keyboard.type("yes\n");
  await expect
    .poll(() => terminalText(page), { message: "the flood was not stopped", timeout: 120 * 1000, intervals: [1000, 2000] })
    .toMatch(/Output was too fast \(over 20 MB in 10 s\), so this run was stopped/);

  // a fresh terminal in the same tab works again
  await page.locator("#redo-button").click();
  await page.waitForTimeout(3000);
  await page.locator("#terminal-div").click();
  await page.keyboard.type("echo still-works-$((20+22))\n");
  await expect.poll(() => terminalText(page), { timeout: 90 * 1000, intervals: [1000, 2000] }).toMatch(/still-works-42/);
});
