// The addresses of the Preview with a preview host (js/src/page/27-preview.js, docs/lld/17-preview-pane.md "The preview host"): the history of a tab keeps
// the site's form (/preview/<token>/...), pub() makes the address a frame or a tab opens, and pathOf() brings an address the frame reports back.
// The four small functions are taken out of the page script and run with a pretend page.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "src", "page", "27-preview.js"), "utf8");

function fn(name) {
  const start = source.indexOf("function " + name + "(");
  assert.ok(start >= 0, name + " is not in the page script");
  let depth = 0, i = source.indexOf("{", start);
  for (let j = i; j < source.length; j++) {
    if (source[j] === "{") depth++;
    if (source[j] === "}" && --depth === 0) return source.slice(start, j + 1);
  }
  throw new Error("no end of " + name);
}

function page(base) {
  const ctx = { window: { site_settings: base === undefined ? {} : { previewBaseURL: base } }, location: { origin: "https://openrepl.test", href: "https://openrepl.test/" }, URL };
  vm.createContext(ctx);
  vm.runInContext(["previewBase", "pub", "popUrl", "pathOf"].map(fn).join("\n"), ctx);
  return ctx;
}

test("without a preview host the addresses are the site's own, as before", () => {
  for (const base of [undefined, "", null]) {
    const p = page(base);
    assert.equal(p.pub("/preview/T/f/index.html"), "/preview/T/f/index.html");
    assert.equal(p.popUrl("/preview/T/p/8000/"), "https://openrepl.test/preview/T/p/8000/");
    assert.equal(p.pathOf("https://openrepl.test/preview/T/f/a.html?x=1#h"), "/preview/T/f/a.html?x=1#h");
  }
});

test("with a preview host a frame opens <base>/<token>/..., without /preview", () => {
  const p = page("https://preview.example.test");
  assert.equal(p.pub("/preview/T/f/index.html"), "https://preview.example.test/T/f/index.html");
  assert.equal(p.pub("/preview/T/p/8000/x?y=1"), "https://preview.example.test/T/p/8000/x?y=1");
  assert.equal(p.popUrl("/preview/T/f/index.html"), "https://preview.example.test/T/f/index.html");
  // a trailing slash of the setting is no double slash
  assert.equal(page("https://preview.example.test/").pub("/preview/T/f/a.html"), "https://preview.example.test/T/f/a.html");
});

test("an address the frame reports on the preview host is brought back to the site's form, the history keeps one form", () => {
  const p = page("https://preview.example.test");
  assert.equal(p.pathOf("https://preview.example.test/T/f/a.html?render=md#top"), "/preview/T/f/a.html?render=md#top");
  assert.equal(p.pathOf("https://preview.example.test/T/p/8000/"), "/preview/T/p/8000/");
  // already in the site's form (the host accepts it too), or from the site itself
  assert.equal(p.pathOf("https://preview.example.test/preview/T/f/a.html"), "/preview/T/f/a.html");
  assert.equal(p.pathOf("https://openrepl.test/preview/T/f/a.html"), "/preview/T/f/a.html");
  assert.equal(p.pathOf("/preview/T/f/b.html"), "/preview/T/f/b.html");
});
