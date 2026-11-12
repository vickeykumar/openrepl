// What the Preview shows besides .html pages (page/27-preview.js, package livepreview) and how the language list follows
// the file (page/07-file-browser.js, 06-editor.js, 24-large-file-viewer.js): Markdown rendered, a picture or a PDF opened
// as a card (never as hex first), the list saying "Image" or "PDF" for them.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const stamp = Date.now().toString(36);
const dir = `types${stamp}`;
// a 1 x 1 PNG
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.EditorTabs && window.LargeFileViewer);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

// name: a file of the folder; data: the file's bytes as base64
async function putFiles(page, files) {
  await page.evaluate(async ([d, files]) => {
    await window.FileBrowser.create(d, "folder");
    for (const name of Object.keys(files)) await window.FileBrowser.create(d + "/" + name, "file");
    for (const [name, b64] of Object.entries(files)) {
      await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/" + name), method: "POST", processData: false, data: b64, contentType: "application/octet-stream" });
    }
  }, [dir, files]);
}

async function open(page, name) {
  const opened = await page.evaluate((p) => window.FileBrowser.open(p), `${dir}/${name}`);
  expect(opened.ok, JSON.stringify(opened)).toBe(true);
}

const b64 = (text) => Buffer.from(text).toString("base64");

test("the language list has Markdown, and Image and PDF as entries that only a file sets", async ({ page }) => {
  await ready(page);
  const options = await page.evaluate(() => [...document.querySelectorAll("#select-lang option")].map((o) => ({ v: o.value, label: o.textContent, disabled: o.disabled })));
  expect(options.find((o) => o.v === "markdown")).toMatchObject({ label: "Markdown", disabled: false });
  expect(options.find((o) => o.v === "image")).toMatchObject({ label: "Image", disabled: true });
  expect(options.find((o) => o.v === "pdf")).toMatchObject({ label: "PDF", disabled: true });
  // Markdown sits between Makefile and Perl
  const order = options.map((o) => o.v);
  expect(order.indexOf("markdown")).toBe(order.indexOf("makefile") + 1);
});

test("a Markdown file sets the list, and the Preview shows it rendered, with its links, and runs nothing", async ({ page }) => {
  await ready(page);
  await putFiles(page, {
    "a.md": b64("# Heading one\n\nSee [the second](b.md) and `code`.\n\n<script>document.title='pwned'</script>\n\n- item\n"),
    "b.md": b64("# Second file\n"),
  });
  await open(page, "a.md");
  await expect(page.locator("#select-lang")).toHaveValue("markdown");
  await expect(page.locator("#preview-button")).toBeVisible();
  await page.locator("#preview-button").click();
  await expect(page.locator("#preview-address-text")).toHaveText(`${dir}/a.md`);
  const frame = page.frameLocator("#preview-frame");
  await expect(frame.locator("h1")).toHaveText("Heading one");
  await expect(frame.locator("code")).toHaveText("code");
  await expect(frame.locator("li")).toHaveText("item");
  // the tag in the file is text, not a script
  await expect(frame.locator("body")).toContainText("<script>document.title='pwned'</script>");
  await expect(frame.locator("script")).toHaveCount(0);
  // a link to another Markdown file of the workspace stays rendered
  await frame.getByRole("link", { name: "the second" }).click();
  await expect(frame.locator("h1")).toHaveText("Second file");
});

test("a picture opens as a card, not as hex, and the Preview shows it; View as hex is one click away", async ({ page }) => {
  await ready(page);
  await putFiles(page, { "p.png": PNG, "plain": b64("just text") });
  await open(page, "p.png");
  const viewer = page.locator("#large-viewer");
  await expect(viewer).toBeVisible();
  await expect(viewer).toHaveClass(/lv--media/);
  await expect(viewer.locator(".lv__media-title")).toHaveText("p.png is an image");
  await expect(page.locator("#select-lang")).toHaveValue("image");
  // the Preview opened on its own and draws the picture
  await expect(page.locator("#preview-pane")).toBeVisible();
  const img = page.frameLocator("#preview-frame").locator("img");
  await expect(img).toBeVisible();
  expect(await img.evaluate((i) => i.naturalWidth)).toBe(1);
  // the hex view
  await viewer.getByRole("button", { name: "View as hex" }).click();
  await expect(viewer).not.toHaveClass(/lv--media/);
  await expect(viewer.locator(".lv__text")).toContainText("PNG");
  // a text file after it: the list does not keep saying Image
  await open(page, "plain");
  await expect(page.locator("#select-lang")).not.toHaveValue("image");
  await expect(page.locator("#large-viewer")).toBeHidden();
});

test("a PDF opens as a card, the Preview gives it a frame of its own, and the server sends it as a PDF", async ({ page }) => {
  await ready(page);
  await putFiles(page, { "d.pdf": b64("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n") });
  await open(page, "d.pdf");
  await expect(page.locator("#large-viewer .lv__media-title")).toHaveText("d.pdf is a PDF");
  await expect(page.locator("#select-lang")).toHaveValue("pdf");
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.locator("#preview-pdf-frame")).toBeVisible();
  await expect(page.locator("#preview-frame")).toBeHidden();
  const src = await page.locator("#preview-pdf-frame").getAttribute("src");
  expect(src).toContain("d.pdf");
  const answer = await page.evaluate(async (u) => { const r = await fetch(u); return { status: r.status, type: r.headers.get("content-type"), csp: r.headers.get("content-security-policy"), sniff: r.headers.get("x-content-type-options") }; }, src);
  expect(answer.status).toBe(200);
  expect(answer.type).toBe("application/pdf");
  expect(answer.sniff).toBe("nosniff");
  expect(answer.csp).toBeNull();
});
