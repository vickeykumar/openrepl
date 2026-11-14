// A collaborator who joins or reloads sees what the owner's terminal already holds, and does not restart the owner's REPL
// (the page's language is told to the owner, who used to restart for it and lose the terminal).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const text = (p) => p.locator("#terminal-div .xterm-rows").first().innerText();

test("joining and reloading as a collaborator keeps the owner's terminal", async ({ browser }) => {
  test.setTimeout(150 * 1000);
  const octx = await browser.newContext();
  const owner = await octx.newPage();
  await owner.goto("/");
  await owner.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(owner);
  await owner.evaluate(() => window.pickLanguage("bash"));
  await expect(owner.locator("#terminal-div .xterm-rows").first()).toContainText("#", { timeout: 60 * 1000 });
  await owner.evaluate(() => window.sendToActiveTerminal("echo MARKER-ONE\r"));
  await expect.poll(() => text(owner)).toContain("MARKER-ONE");
  const url = new URL(await owner.evaluate(() => document.getElementById("share-it").value));

  const vctx = await browser.newContext();
  const viewer = await vctx.newPage();
  await viewer.goto("/" + url.hash);
  await viewer.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  // what was printed before the collaborator came is shown to them, and the owner's terminal is as it was
  await expect.poll(() => text(viewer), { timeout: 30 * 1000 }).toContain("MARKER-ONE");
  await owner.waitForTimeout(5000);
  expect(await text(owner)).toContain("MARKER-ONE");

  await owner.evaluate(() => window.sendToActiveTerminal("echo MARKER-TWO\r"));
  await expect.poll(() => text(viewer), { timeout: 30 * 1000 }).toContain("MARKER-TWO");

  // the collaborator reloads: the same terminal, with everything in it, and the owner's is not restarted
  await viewer.reload();
  await viewer.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await expect.poll(() => text(viewer), { timeout: 30 * 1000 }).toContain("MARKER-TWO");
  expect(await text(viewer)).toContain("MARKER-ONE");
  await owner.waitForTimeout(5000);
  const mine = await text(owner);
  expect(mine).toContain("MARKER-ONE");
  expect(mine).toContain("MARKER-TWO");
  await octx.close();
  await vctx.close();
});
