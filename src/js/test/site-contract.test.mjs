// Things that must hold across the files of the site, checked without a browser:
// the pages, the notes Genie searches, the page script parts and the stylesheets.
// A broken link to a script or a stylesheet, a page without its theme button or a note
// without its front matter shows only as something missing on the live site.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const resources = path.join(here, "..", "..", "resources");
const server = path.join(here, "..", "..", "server");
const pageDir = path.join(here, "..", "src", "page");

const read = (p) => fs.readFileSync(p, "utf8");
const pages = fs.readdirSync(resources).filter((f) => f.endsWith(".html") && f !== "index_backup.html");

// built by the build, not kept in resources/
const BUILT_JS = new Set(["scribbler.js", "gotty-bundle.js", "hterm.js", "preprocessing.js", "chat-widget.js", "jsconsole.js"]);
const BUILT_CSS = new Set(["xterm.css", "jsconsole.css"]);

test("every page that loads theme.js has the theme button", () => {
  const withTheme = pages.filter((f) => read(path.join(resources, f)).includes("js/theme.js"));
  assert.ok(withTheme.length >= 8, "the pages that follow the theme: " + withTheme.join(", "));
  for (const f of withTheme) {
    assert.ok(read(path.join(resources, f)).includes("data-theme-toggle"), f + " has no theme button");
  }
});

test("the template of the blog and error pages has the theme script and button too", () => {
  const go = read(path.join(server, "utils.go"));
  assert.ok(go.includes('src="/js/theme.js"'));
  assert.ok(go.includes("data-theme-toggle"));
});

test("every script and stylesheet a page names is there", () => {
  const missing = [];
  for (const f of pages) {
    const html = read(path.join(resources, f));
    for (const m of html.matchAll(/(?:src|href)="(?:\.|)\/(js|css)\/([^"?#{}]+)"/g)) {
      const [, kind, name] = m;
      if (kind === "js" && BUILT_JS.has(name)) continue;
      if (kind === "css" && BUILT_CSS.has(name)) continue;
      if (!fs.existsSync(path.join(resources, kind, name))) missing.push(`${f}: ${kind}/${name}`);
    }
  }
  assert.deepEqual(missing, []);
});

test("every note Genie can find has its front matter and a body", () => {
  const dir = path.join(resources, "knowledge");
  const notes = fs.readdirSync(dir).filter((f) => f.endsWith(".md"));
  assert.ok(notes.length > 10, "the notes: " + notes.length);
  for (const f of notes) {
    const text = read(path.join(dir, f)).replace(/\r\n/g, "\n");
    const m = /^---\n([\s\S]*?)\n---\n([\s\S]*)$/.exec(text);
    assert.ok(m, f + " has no front matter");
    for (const key of ["title", "keywords", "link"]) {
      assert.ok(new RegExp("^" + key + ":\\s*\\S", "m").test(m[1]), f + " has no " + key);
    }
    assert.ok(m[2].trim().length > 40, f + " has no text");
  }
});

test("every part of the page script is imported by index.js", () => {
  const index = read(path.join(pageDir, "index.js"));
  const parts = fs.readdirSync(pageDir).filter((f) => /^\d\d-.*\.js$/.test(f));
  assert.ok(parts.length >= 20);
  for (const f of parts) {
    assert.ok(index.includes(`import "./${f.replace(/\.js$/, "")}"`), f + " is not imported by index.js");
  }
});

// a style sheet with an unclosed brace swallows every rule after it
function braceBalance(css) {
  const bare = css.replace(/\/\*[\s\S]*?\*\//g, "").replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/g, '""');
  let depth = 0;
  for (const ch of bare) {
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth < 0) return -1;
    }
  }
  return depth;
}

test("the main style sheets have balanced braces", () => {
  for (const f of ["ui-refresh.css", "scribbler-global.css", "scribbler-doc.css", "admin.css"]) {
    assert.equal(braceBalance(read(path.join(resources, "css", f))), 0, f);
  }
});

test("the light theme turns the top and bottom of the pages light (the block exists)", () => {
  const css = read(path.join(resources, "css", "ui-refresh.css"));
  assert.ok(css.includes("Light theme: the top and bottom of the pages are light too"));
  for (const sel of ["#header-nav.site-nav", ".hero", ".practice-hero", ".workspace", ".band--dark", ".site-footer"]) {
    assert.ok(css.includes(sel), sel);
  }
});
