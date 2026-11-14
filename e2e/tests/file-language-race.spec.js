// Choosing files of different languages one after another (in the Files panel or on the editor tabs) leaves the language of the
// last one chosen, in the picker and in the editor's list, however fast the choices come.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const state = (page) => page.evaluate(() => ({ picker: $("#optionlist").val(), list: $("#select-lang").val() }));

async function make(page, name) {
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), name);
  expect(made.ok, JSON.stringify(made)).toBe(true);
}
const anchor = (page, name) => page.locator("#file-browser .jstree-anchor", { hasText: name });

test("the last file chosen decides the language, whatever came before it and however fast", async ({ page }) => {
  test.setTimeout(240 * 1000);
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
  const files = { c: `a${stamp}.c`, cpp: `b${stamp}.cpp`, go: `c${stamp}.go`, java: `Main${stamp}.java` };
  for (const n of Object.values(files)) await make(page, n);
  for (const n of Object.values(files)) await expect(anchor(page, n)).toBeVisible();
  const want = { c: { picker: "c", list: "c" }, cpp: { picker: "cpp", list: "cpp" }, go: { picker: "go", list: "golang" }, java: { picker: "java", list: "java" } };

  // a few gaps, from quick to slow. The picker follows a file a second after it is chosen, and the page then looks at the editor's
  // language again 2.5 seconds after that: a second file chosen between 2.5 and 3.5 seconds after the first is the case that failed.
  for (const gap of [150, 1300, 2800, 3100]) {
    for (const seq of [["c", "go"], ["cpp", "go"], ["go", "java"], ["java", "c"], ["go", "cpp"]]) {
      for (const k of seq) {
        await anchor(page, files[k]).click();
        await page.waitForTimeout(gap);
      }
      const last = seq[seq.length - 1];
      // after every timer has had its say
      await expect.poll(() => state(page), { timeout: 25 * 1000, message: `after ${seq.join(" then ")} with ${gap} ms between` }).toEqual(want[last]);
      await page.waitForTimeout(4500);
      expect(await state(page), `settled after ${seq.join(" then ")} with ${gap} ms between`).toEqual(want[last]);
    }
  }
});
