// The Files panel on first sight, and the colors of changed and new files (page/26-file-marks.js, lib-filemarks.mjs).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.FileMarks && window.EditorTabs);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

const anchor = (page, name) => page.locator("#file-browser .jstree-anchor", { hasText: name }).first();
const make = async (page, rel, kind) => {
  const made = await page.evaluate(([r, k]) => window.FileBrowser.create(r, k), [rel, kind]);
  expect(made.ok, `${rel}: ${JSON.stringify(made)}`).toBe(true);
};

test("the workspace folder is open when the page loads", async ({ page }) => {
  await ready(page);
  await make(page, `open${stamp}.txt`, "file");
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  const root = page.locator("#file-browser li.jstree-open").first();
  await expect(root).toBeVisible();
  await expect(anchor(page, `open${stamp}.txt`)).toBeVisible(); // a file of the workspace, with no click on the folder
});

test("the Files panel starts open on a window wider than 800 px, a saved choice wins, and a phone has it in a drawer", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 800 });
  await page.goto("/");
  await expect(page.locator("body")).not.toHaveClass(/files-collapsed/);
  // a choice already made wins over the width
  await page.evaluate(() => localStorage.setItem("files-panel", "closed"));
  await page.reload();
  await expect(page.locator("body")).toHaveClass(/files-collapsed/);
  // up to 800 px the page is in its phone layout, where the files are in a drawer (13-mobile.js)
  await page.evaluate(() => localStorage.removeItem("files-panel"));
  await page.setViewportSize({ width: 700, height: 800 });
  await page.reload();
  await expect(page.locator("body")).toHaveClass(/is-mobile/);
  await expect(page.locator("body")).toHaveClass(/files-collapsed/);
});

test("a new file is green with U, a saved one amber with M, unsaved edits add a dot, and the folders above show it", async ({ page }) => {
  await ready(page);
  const dir = `marks${stamp}`;
  const file = `a${stamp}.txt`;
  await make(page, dir, "folder");
  await make(page, `${dir}/${file}`, "file");
  // created in this session: new
  await expect(anchor(page, file)).toHaveClass(/fm-new/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "U");
  // and it is drawn in the color of the theme: the IDE theme colors every row with a longer selector
  const green = await page.evaluate((name) => {
    const probe = document.createElement("span");
    probe.style.color = "var(--fm-new)";
    document.body.appendChild(probe);
    const expected = getComputedStyle(probe).color;
    probe.remove();
    const row = [...document.querySelectorAll("#file-browser .jstree-anchor")].find((a) => a.textContent.includes(name));
    return { expected, row: getComputedStyle(row).color };
  }, file);
  expect(green.row).toBe(green.expected);
  await expect(anchor(page, dir)).toHaveClass(/fm-new/); // the folder above it
  // after a reload the marks are gone, and a save makes the file modified
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.FileMarks);
  await expect(anchor(page, dir)).not.toHaveClass(/fm-new|fm-modified/);
  await page.evaluate((d) => { const t = $("#file-browser").jstree(true); const id = t.get_node("#").children[0] + "/" + d; t.open_node(id); }, dir);
  await anchor(page, file).click();
  await expect(page.locator("#editor-tabs .etab", { hasText: file })).toHaveAttribute("aria-selected", "true");
  // typed, not saved: a dot
  await page.evaluate(() => window.editor.env.editor.setValue("changed text", -1));
  await expect(anchor(page, file)).toHaveClass(/fm-unsaved/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "●");
  await expect(page.locator("#editor-tabs .etab", { hasText: file }).locator(".etab__dot")).toBeVisible();
  // saved: modified (amber, M), and the tab name has the same color
  await page.evaluate(() => window.SaveSelectedNodeToFile(window.editor.env.filename));
  await expect(anchor(page, file)).toHaveClass(/fm-modified/);
  await expect(anchor(page, file)).toHaveAttribute("data-fm", "M");
  await expect(anchor(page, file)).not.toHaveClass(/fm-unsaved/);
  await expect(page.locator("#editor-tabs .etab.etab--modified", { hasText: file })).toBeVisible();
  // the name is really drawn in the color of the theme: a probe with the same variable is the reference
  const colors = await page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.color = "var(--fm-modified)";
    document.querySelector("#file-browser").appendChild(probe);
    const expected = getComputedStyle(probe).color;
    probe.remove();
    const name = document.querySelector("#editor-tabs .etab--modified .etab__name");
    const row = document.querySelector("#file-browser .jstree-anchor.fm-modified");
    const plain = document.querySelector("#file-browser .jstree-anchor:not(.fm-modified):not(.fm-new)");
    return { expected, tab: getComputedStyle(name).color, row: getComputedStyle(row).color, plain: plain ? getComputedStyle(plain).color : "" };
  });
  expect(colors.expected).not.toBe("");
  expect(colors.row).toBe(colors.expected);
  expect(colors.tab).toBe(colors.expected);
  expect(colors.plain).not.toBe(colors.expected); // an unmarked row keeps the ordinary color
});

test("Clear change marks removes the colors", async ({ page }) => {
  await ready(page);
  const file = `clear${stamp}.txt`;
  await make(page, file, "file");
  await expect(anchor(page, file)).toHaveClass(/fm-new/);
  await page.keyboard.press("ControlOrMeta+Shift+P");
  await page.locator("#palette .palette__item", { hasText: "Clear change marks" }).click();
  await expect(anchor(page, file)).not.toHaveClass(/fm-new/);
  await expect(anchor(page, file)).not.toHaveAttribute("data-fm", /.+/);
});

// Hidden files: a name that starts with a dot is out of the tree by default; "Show hidden files" in the menu of a folder (or
// Ctrl/Cmd+Shift+.) shows them in that folder and below, "Hide hidden files" hides them again (page/07-file-browser.js).
async function localScript(page) {
  const fs = require("fs");
  const path = require("path");
  if (process.env.GENIE_LOCAL_ASSETS) await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "js", "dist", "scribbler.js")) }));
}
// forget the setting of an earlier run, once (a reload inside the test keeps what the page saved)
async function clearHidden(page) {
  await page.addInitScript(() => {
    try {
      if (sessionStorage.getItem("hf-cleared")) return;
      sessionStorage.setItem("hf-cleared", "1");
      localStorage.removeItem("openrepl.hiddenfiles");
    } catch (e) {}
  });
}
async function hiddenTree(page) {
  // the page script of this checkout, and a workspace tree with hidden names: .hid (a folder with a file), a folder subx
  // with .inner in it (in CI the server under test already has this script; the checkout's build is used only for a run
  // with GENIE_LOCAL_ASSETS=1)
  await localScript(page);
  await page.route((u) => u.pathname.endsWith("/ws_filebrowser"), async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    const res = await route.fetch();
    const tree = await res.json();
    const id = tree.id;
    tree.children = (tree.children || []).concat([
      { id: id + "/.hid", text: ".hid", type: "default", children: [{ id: id + "/.hid/a.txt", text: "a.txt", type: "file" }] },
      { id: id + "/.rc", text: ".rc", type: "file" },
      { id: id + "/subx", text: "subx", type: "default", children: [{ id: id + "/subx/.inner", text: ".inner", type: "file" }, { id: id + "/subx/plain.txt", text: "plain.txt", type: "file" }] },
      { id: id + "/.git", text: ".git", type: "default", children: [{ id: id + "/.git/config", text: "config", type: "file" }] },
    ]);
    route.fulfill({ response: res, contentType: "application/json", body: JSON.stringify(tree) });
  });
}
const rootAnchor = (page) => page.locator("#file-browser li.jstree-open > a.jstree-anchor").first();
async function menu(page, locator, item) {
  await locator.click({ button: "right" });
  await page.locator(".vakata-context a", { hasText: item }).click();
}
const count = (page, name) => page.locator("#file-browser .jstree-anchor", { hasText: name }).count();

test("names that start with a dot are hidden by default, and Show / Hide hidden files on the workspace toggles them", async ({ page }) => {
  await clearHidden(page);
  await hiddenTree(page);
  await ready(page);
  await expect(anchor(page, "subx")).toBeVisible();
  expect(await count(page, ".hid")).toBe(0);
  expect(await count(page, ".rc")).toBe(0);
  expect(await count(page, ".git")).toBe(0);
  await menu(page, rootAnchor(page), "Show hidden files");
  await expect(anchor(page, ".hid")).toBeVisible();
  await expect(anchor(page, ".rc")).toBeVisible();
  await expect(anchor(page, ".hid")).toHaveClass(/is-dot/);
  await expect(anchor(page, ".hid")).toHaveAttribute("title", "Hidden folder");
  await expect(rootAnchor(page)).toHaveClass(/hidden-on/); // the folder says it shows them
  // .git is listed, but what is inside it is not (it holds thousands of files nobody edits by hand)
  await expect(anchor(page, ".git")).toBeVisible();
  expect(await count(page, "config")).toBe(0);
  // a reload keeps the choice
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  await expect(anchor(page, ".rc")).toBeVisible();
  await menu(page, rootAnchor(page), "Hide hidden files");
  await expect(anchor(page, ".rc")).toHaveCount(0);
  await expect(anchor(page, ".hid")).toHaveCount(0);
});

test("the nearest folder with a setting wins, and a click on a folder drops the settings below it; the keys toggle the selected folder", async ({ page }) => {
  await clearHidden(page);
  await hiddenTree(page);
  await ready(page);
  // open subx, then show hidden files only there: .inner appears, the workspace's .rc does not
  await anchor(page, "subx").dblclick();
  await expect(anchor(page, "plain.txt")).toBeVisible();
  expect(await count(page, ".inner")).toBe(0);
  await menu(page, anchor(page, "subx"), "Show hidden files");
  await expect(anchor(page, ".inner")).toBeVisible();
  expect(await count(page, ".rc")).toBe(0);
  // show on the workspace, hide on subx: the nearest setting wins
  await menu(page, rootAnchor(page), "Show hidden files");
  await expect(anchor(page, ".rc")).toBeVisible();
  await expect(anchor(page, ".inner")).toBeVisible(); // the workspace click dropped subx's own setting, and now shows it too
  await menu(page, anchor(page, "subx"), "Hide hidden files");
  await expect(anchor(page, ".inner")).toHaveCount(0);
  await expect(anchor(page, ".rc")).toBeVisible();
  // the keys: Ctrl/Cmd+Shift+. on the selected folder toggles it
  await anchor(page, "subx").click();
  await page.keyboard.press("ControlOrMeta+Shift+.");
  await expect(anchor(page, ".inner")).toBeVisible();
  await page.keyboard.press("ControlOrMeta+Shift+.");
  await expect(anchor(page, ".inner")).toHaveCount(0);
});

test("a hidden text file made by the person is in the tree and opens in the editor like any file", async ({ page }) => {
  await clearHidden(page);
  await localScript(page);
  await ready(page);
  const name = `.hid${stamp}rc`;
  await page.evaluate(([n]) => { localStorage.setItem("openrepl.hiddenfiles", JSON.stringify({ "": true })); }, [name]);
  await page.reload();
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  // made by the person (the editor's way): the folder shows hidden files, so the new hidden name is in the tree
  const made = await page.evaluate((n) => window.FileBrowser.create(n, "file", true), name);
  expect(made.ok, JSON.stringify(made)).toBe(true);
  await expect(anchor(page, name)).toBeVisible();
  await expect(anchor(page, name)).not.toHaveClass(/jstree-disabled/);
  await anchor(page, name).click();
  await expect.poll(() => page.evaluate(() => window.editor.env.filename)).toContain(name);
});

test("in a shared session the owner and the collaborator see the same tree, and either can show or hide hidden files for both", async ({ browser }) => {
  test.setTimeout(120 * 1000);
  const octx = await browser.newContext();
  const owner = await octx.newPage();
  await clearHidden(owner);
  await hiddenTree(owner);
  await ready(owner);
  await expect.poll(() => owner.evaluate(() => (document.getElementById("share-it").value.split("#")[1] || "").length), { timeout: 30 * 1000 }).toBeGreaterThan(0);
  const url = new URL(await owner.evaluate(() => document.getElementById("share-it").value));
  const vctx = await browser.newContext();
  const viewer = await vctx.newPage();
  await hiddenTree(viewer); // the same made-up workspace for the collaborator's own requests
  await viewer.goto("/" + url.hash);
  await viewer.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready());
  expect(await viewer.evaluate(() => isMaster())).toBe(false);
  // the collaborator's workspace folder comes closed; open it
  const vroot = viewer.locator("#file-browser a.jstree-anchor").first();
  await expect(vroot).toBeVisible({ timeout: 20 * 1000 });
  await expect.poll(async () => {
    if (!(await anchor(viewer, "subx").isVisible())) await viewer.evaluate(() => { const t = $("#file-browser").jstree(true); t.open_node(window.homedir); });
    return anchor(viewer, "subx").isVisible();
  }, { timeout: 30 * 1000 }).toBe(true);
  expect(await count(viewer, ".rc")).toBe(0);
  // the owner shows them: the collaborator's panel follows
  await menu(owner, rootAnchor(owner), "Show hidden files");
  await expect(anchor(owner, ".rc")).toBeVisible();
  await expect(anchor(viewer, ".rc")).toBeVisible({ timeout: 20 * 1000 });
  // the collaborator hides them: the owner's panel follows (the owner's page builds the tree)
  await menu(viewer, vroot, "Hide hidden files");
  await expect(anchor(viewer, ".rc")).toHaveCount(0, { timeout: 20 * 1000 });
  await expect(anchor(owner, ".rc")).toHaveCount(0, { timeout: 20 * 1000 });
  await octx.close();
  await vctx.close();
});
