// Saving a file whose text has characters outside Latin1 (an ellipsis, a dash, an arrow, emoji, other scripts): the page used to call
// btoa on the text, which throws InvalidCharacterError, so such a file could not be saved (src/js/src/page/lib-encoding.mjs).
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.EditorTabs && window.textToBase64);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

const TEXT = "loading… a — b → ✓ 日本語 😀 é ü";

test("a file with characters outside Latin1 is saved with the page's own save, and reads back as it was typed", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await ready(page);
  const name = `utf${Date.now().toString(36)}.txt`;
  const read = await page.evaluate(async ([name, text]) => {
    await window.FileBrowser.create(name, "file");
    const path = window.homedir + "/" + name;
    window.editor.env.editor.setValue(text, -1);
    window.SaveSelectedNodeToFile(path); // the call behind Ctrl+S
    await new Promise((r) => setTimeout(r, 1500));
    const res = await fetch(preprocessurl("/ws_filebrowser?q=load&enc=raw&filepath=" + encodeURIComponent(path)));
    return new TextDecoder("utf-8").decode(new Uint8Array(await res.arrayBuffer()));
  }, [name, TEXT]);
  expect(read).toBe(TEXT);
  expect(errors.filter((m) => /btoa|Latin1/.test(m))).toEqual([]);
});

test("the save of a tab (Save all, closing a changed tab) takes such text too", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await ready(page);
  const out = await page.evaluate(async (text) => {
    const b64 = window.textToBase64(text);
    const name = `utt${Date.now().toString(36)}.txt`;
    await window.FileBrowser.create(name, "file");
    const path = window.homedir + "/" + name;
    await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + path), method: "POST", processData: false, data: b64, contentType: "application/octet-stream" });
    const res = await fetch(preprocessurl("/ws_filebrowser?q=load&enc=raw&filepath=" + encodeURIComponent(path)));
    return new TextDecoder("utf-8").decode(new Uint8Array(await res.arrayBuffer()));
  }, TEXT);
  expect(out).toBe(TEXT);
  expect(errors).toEqual([]);
});
