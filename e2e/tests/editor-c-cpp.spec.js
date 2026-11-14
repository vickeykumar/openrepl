// C and C++ are two entries of the editor's language list (and of the picker): the list follows the picker, a file by its
// extension, and a shared session. Ace colours both with its one mode (c_cpp).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const state = (page) => page.evaluate(() => ({ picker: $("#optionlist").val(), list: $("#select-lang").val() }));
const aceMode = (page) => page.evaluate(() => document.getElementById("editor").env.editor.getSession().getMode().$id);

async function ready(page, url = "/") {
  await page.goto(url);
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
}

async function openFile(page, name) {
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file"), name);
  expect(made.ok, JSON.stringify(made)).toBe(true);
  const anchor = page.locator("#file-browser .jstree-anchor", { hasText: name });
  await expect(anchor).toBeVisible();
  await anchor.click();
  await page.waitForFunction((n) => (window.EditorTabs.shown() || "").endsWith("/" + n), name);
}

test("the editor's list has C and C++ and follows the picker, files and Ace colours both", async ({ page }) => {
  test.setTimeout(120 * 1000);
  await ready(page);
  await waitForTerminalReady(page);
  const labels = await page.locator("#select-lang option").allTextContents();
  expect(labels).toContain("C");
  expect(labels).toContain("C++");
  expect(labels).not.toContain("C and C++");

  await page.evaluate(() => window.pickLanguage("cpp"));
  await expect.poll(() => state(page)).toEqual({ picker: "cpp", list: "cpp" });
  expect(await aceMode(page)).toBe("ace/mode/c_cpp");
  await page.waitForTimeout(2500);
  await page.evaluate(() => window.pickLanguage("c"));
  await expect.poll(() => state(page)).toEqual({ picker: "c", list: "c" });
  expect(await aceMode(page)).toBe("ace/mode/c_cpp");
  await page.waitForTimeout(2500);

  // the list drives the picker
  await page.evaluate(() => $("#select-lang").val("cpp").trigger("change"));
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toEqual({ picker: "cpp", list: "cpp" });
  await page.waitForTimeout(2500); // the terminal of the new language starts a moment after the change
  await page.evaluate(() => $("#select-lang").val("c").trigger("change"));
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toEqual({ picker: "c", list: "c" });
  await page.waitForTimeout(2500);

  // a file by its extension
  await openFile(page, `x${stamp}.cpp`);
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toEqual({ picker: "cpp", list: "cpp" });
  await page.waitForTimeout(2500); // the page changes the language of a file a second after it is chosen
  await openFile(page, `y${stamp}.c`);
  await expect.poll(() => state(page), { timeout: 20 * 1000 }).toEqual({ picker: "c", list: "c" });
});

test("a collaborator sees C++ as C++, and a record of an older page that says c_cpp is C", async ({ browser }) => {
  test.setTimeout(150 * 1000);
  const octx = await browser.newContext();
  const owner = await octx.newPage();
  await ready(owner);
  await waitForTerminalReady(owner);
  await owner.evaluate(() => window.pickLanguage("cpp"));
  await expect.poll(() => state(owner)).toEqual({ picker: "cpp", list: "cpp" });
  const url = new URL(await owner.evaluate(() => document.getElementById("share-it").value));
  const vctx = await browser.newContext();
  const viewer = await vctx.newPage();
  await ready(viewer, "/" + url.hash);
  await expect.poll(() => state(viewer), { timeout: 30 * 1000 }).toEqual({ picker: "cpp", list: "cpp" });
  // the owner goes back to C: the collaborator follows
  await owner.evaluate(() => window.pickLanguage("c"));
  await expect.poll(() => state(viewer), { timeout: 30 * 1000 }).toEqual({ picker: "c", list: "c" });
  // an older page wrote the one mode of both
  const id = url.hash.slice(1);
  await owner.evaluate((i) => firebase.database().ref("editor_values/" + i + "/lang").set({ data: "c_cpp", silent: true }), id);
  await owner.waitForTimeout(3000);
  expect((await state(viewer)).list).toBe("c");
  await octx.close();
  await vctx.close();
});
