// The icons of the Files panel and of the editor tabs. Files are coloured badges (ui-refresh.css, .fi); folders and the rest of the page use Font Awesome icons, which the page loads from this site
// (css/fa): a kit script from kit.fontawesome.com was used before and answered 403, which left the panel without any icon.
const { test, expect } = require("@playwright/test");

async function ready(page) {
  if (process.env.GENIE_LOCAL_ASSETS) {
    const fs = require("fs"), path = require("path");
    const src = path.join(__dirname, "..", "..", "src");
    await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(src, "js", "dist", "scribbler.js")) }));
    await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), (r) => r.fulfill({ contentType: "text/css", body: fs.readFileSync(path.join(src, "resources", "css", "ui-refresh.css")) }));
  }
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
    for (const n of ["a.py", "notes.txt", "pic.png", "doc.pdf", "plain", "x.cpp", "run.sh"]) await window.FileBrowser.create(d + "/" + n, "file");
  }, d);
  const anchors = page.locator("#file-browser .jstree-anchor");
  await expect(anchors.filter({ hasText: "a.py" })).toHaveCount(1);
  const result = await page.evaluate((d) => {
    const out = {};
    document.querySelectorAll("#file-browser .jstree-anchor").forEach((a) => {
      const i = a.querySelector(".jstree-icon");
      if (!i) return;
      const name = a.textContent.trim().replace(/\s.*$/, "");
      const cs = getComputedStyle(i), before = getComputedStyle(i, "::before").content;
      out[name] = { cls: i.className, label: before, bg: cs.backgroundColor, w: parseFloat(cs.width), font: /Font Awesome/.test(cs.fontFamily), glyph: before && before !== "none" && before !== "normal" && before !== '""' };
    });
    return out;
  }, d);
  // a file is a coloured badge with a label (not a Font Awesome glyph), by its extension
  const kinds = { "a.py": ["fi--py", '"Py"'], "notes.txt": ["fi--md", '"M"'], "pic.png": ["fi--img"], "doc.pdf": ["fi--pdf", '"PDF"'], "plain": ["fi--file"], "x.cpp": ["fi--cpp", '"C+"'], "run.sh": ["fi--sh", '"$"'] };
  for (const [name, [cls, label]] of Object.entries(kinds)) {
    expect(result[name], name).toBeTruthy();
    expect(result[name].cls, name).toContain(cls);
    if (label) expect(result[name].label, name + " label").toBe(label);
    expect(result[name].bg, name + " has a colour").not.toBe("rgba(0, 0, 0, 0)");
    expect(result[name].w, name + " is a badge").toBeLessThan(20);
  }
  // a folder keeps its Font Awesome folder
  expect(result[d].cls).toMatch(/fa-folder/);
  expect(result[d].glyph).toBe(true);
  expect(result[d].font).toBe(true);
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
  // the same badge class as the file has in the Files panel
  for (const n of ["t.py", "r.txt"]) {
    const panel = await page.evaluate((n) => {
      const a = Array.from(document.querySelectorAll("#file-browser .jstree-anchor")).find((x) => x.textContent.trim().startsWith(n));
      return (a.querySelector(".jstree-icon").className.match(/fi--[a-z]+/g) || []).join(" ");
    }, n);
    expect(panel).toBeTruthy();
    for (const c of panel.split(" ")) await expect(tab(n)).toHaveClass(new RegExp(c));
  }
  await expect(tab("t.py")).toHaveClass(/fi--py/);
  const ok = await page.evaluate(() => Array.from(document.querySelectorAll(".etab .etab__icon.fi")).every((i) => {
    const cs = getComputedStyle(i);
    return cs.backgroundColor !== "rgba(0, 0, 0, 0)" && parseFloat(cs.width) < 20;
  }));
  expect(ok).toBe(true);
});
