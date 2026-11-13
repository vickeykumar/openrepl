// The icons of the Files panel and of the editor tabs. They are Font Awesome icons, which the page loads from this site
// (css/fa): a kit script from kit.fontawesome.com was used before and answered 403, which left the panel without any icon.
const { test, expect } = require("@playwright/test");

async function ready(page) {
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.EditorTabs && window.FileIconClass);
  await expect(page.locator("#loader")).toBeHidden();
}

// an icon is drawn when its ::before has a glyph and the font is Font Awesome
const drawn = (el) => {
  const before = getComputedStyle(el, "::before").content;
  return before && before !== "none" && before !== "normal" && before !== '""' && /Font Awesome/.test(getComputedStyle(el).fontFamily);
};

test("the page loads Font Awesome from this site, not from a kit", async ({ page }) => {
  const requests = [];
  page.on("request", (r) => requests.push(r.url()));
  await ready(page);
  expect(requests.some((u) => /kit\.fontawesome\.com/.test(u))).toBe(false);
  expect(requests.some((u) => /\/css\/fa\/css\/all\.min\.css/.test(u))).toBe(true);
  const font = await page.evaluate(() => document.fonts.check('900 14px "Font Awesome 6 Free"'));
  expect(font).toBe(true);
});

test("files and folders have their icons in the Files panel", async ({ page }) => {
  await ready(page);
  const d = `ic${Date.now().toString(36)}`;
  await page.evaluate(async (d) => {
    await window.FileBrowser.create(d, "folder");
    for (const n of ["a.py", "notes.txt", "pic.png", "doc.pdf", "plain"]) await window.FileBrowser.create(d + "/" + n, "file");
  }, d);
  const anchors = page.locator("#file-browser .jstree-anchor");
  await expect(anchors.filter({ hasText: "a.py" })).toHaveCount(1);
  const result = await page.evaluate((d) => {
    const out = {};
    document.querySelectorAll("#file-browser .jstree-anchor").forEach((a) => {
      const i = a.querySelector(".jstree-icon");
      if (!i) return;
      const name = a.textContent.trim().replace(/\s.*$/, "");
      const before = getComputedStyle(i, "::before").content;
      out[name] = { cls: i.className, glyph: before && before !== "none" && before !== "normal" && before !== '""', font: /Font Awesome/.test(getComputedStyle(i).fontFamily) };
    });
    return out;
  }, d);
  for (const name of [d, "a.py", "notes.txt", "pic.png", "doc.pdf", "plain"]) {
    expect(result[name], name).toBeTruthy();
    expect(result[name].glyph, name + " has a glyph").toBe(true);
    expect(result[name].font, name + " is in the Font Awesome font").toBe(true);
  }
  expect(result["a.py"].cls).toContain("fa-file-code");
  expect(result["pic.png"].cls).toContain("fa-file-image");
  expect(result["doc.pdf"].cls).toContain("fa-file-pdf");
  expect(result[d].cls).toMatch(/fa-folder/);
});

test("an editor tab shows the icon of its file, as the Files panel does", async ({ page }) => {
  await ready(page);
  const d = `it${Date.now().toString(36)}`;
  await page.evaluate(async (d) => {
    await window.FileBrowser.create(d, "folder");
    await window.FileBrowser.create(d + "/t.py", "file");
    await window.FileBrowser.create(d + "/r.txt", "file");
  }, d);
  for (const n of ["t.py", "r.txt"]) {
    await page.evaluate(async ([d, n]) => { const id = window.homedir + "/" + d + "/" + n; $("#file-browser").jstree(true).select_node(id); }, [d, n]);
    await expect(page.locator(".etab", { hasText: n })).toHaveCount(1);
  }
  const tab = (n) => page.locator(".etab", { hasText: n }).locator(".etab__icon");
  // the same Font Awesome class as the file has in the Files panel
  for (const n of ["t.py", "r.txt"]) {
    const panel = await page.evaluate((n) => {
      const a = Array.from(document.querySelectorAll("#file-browser .jstree-anchor")).find((x) => x.textContent.trim().startsWith(n));
      return (a.querySelector(".jstree-icon").className.match(/fa-[a-z-]+/g) || []).join(" ");
    }, n);
    expect(panel).toBeTruthy();
    for (const c of panel.split(" ")) await expect(tab(n)).toHaveClass(new RegExp(c));
  }
  await expect(tab("t.py")).toHaveClass(/fa-file-code/);
  const ok = await page.evaluate(() => Array.from(document.querySelectorAll(".etab .etab__icon")).every((i) => {
    const before = getComputedStyle(i, "::before").content;
    return before && before !== "none" && before !== "normal" && before !== '""' && /Font Awesome/.test(getComputedStyle(i).fontFamily);
  }));
  expect(ok).toBe(true);
});
