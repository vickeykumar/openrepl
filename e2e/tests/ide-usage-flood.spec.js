// A program that prints without stopping is paused and then ended by the server (LLD 20, "The terminal's speed cap"): the user sees the notice,
// and a fresh terminal runs again. The flood is `cat /dev/zero`: the server only sees the speed the browser reads at (a full socket holds the writes
// back), and a slow CI runner cannot draw a flood of letters fast enough to pass 20 MB in 10 s, but it throws away the NUL bytes of /dev/zero at once.
// It is typed into the bash terminal, so it needs no editor.
//
// This spec runs on a developer's machine, not in CI: a shared CI runner sends terminal output at well under 2 MB/s (each read of the program is its own
// small WebSocket frame), so a flood there never passes 20 MB in 10 s and the cap is not reached whatever the browser does. The cap itself is tested
// in Go with a real WebSocket flood (src/server/ideusage_test.go), which runs in CI.
const { test, expect } = require("@playwright/test");

async function terminalText(page) {
  const rows = page.locator("#terminal-div .xterm-rows");
  if ((await rows.count()) === 0) return "";
  return (await rows.first().innerText()).replace(/ /g, " ").replace(/\n/g, "");
}

test.skip(!!process.env.CI, "a shared CI runner is too slow to reach the cap");

test("a program that prints for ever is stopped with a notice, and a fresh terminal runs again", async ({ page }) => {
  test.setTimeout(180 * 1000);
  await page.goto("/bash");
  await expect(page.locator("#terminal-div .xterm-rows").first()).toContainText(/\S/, { timeout: 60 * 1000 });
  await page.waitForTimeout(2500); // the prompt is out
  await page.locator("#terminal-div").click();
  await page.keyboard.type("cat /dev/zero\n");
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
