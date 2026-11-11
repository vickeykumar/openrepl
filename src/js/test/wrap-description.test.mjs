// The description of a question is wrapped at 100 characters where it is put in the editor
// (resources/js/practice-store.js, wrapDescription), because a model often answers with one long line.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "..", "resources", "js", "practice-store.js"), "utf8");
const window = { addEventListener() {}, fetch: () => Promise.resolve({ ok: false, status: 404, json: async () => null }) };
const ctx = vm.createContext({
  window, console, setTimeout, clearTimeout, Promise, JSON, Date, Math, fetch: window.fetch,
  document: { readyState: "complete", addEventListener() {}, getElementById: () => null },
  localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
  navigator: {}, location: { search: "" },
});
vm.runInContext(source, ctx);
const wrap = window.PracticeStore.wrapDescription;
const longest = (t) => Math.max(...t.split("\n").map((l) => l.length));

test("a one-line description is wrapped at word boundaries, and nothing is lost", () => {
  const one = "The problem requires implementing a basic calculator II in C. Define an unimplemented function `basic_calculator` that takes two integers and returns their sum. Include a main function to test this function with sample inputs. The main function must call `basic_calculator` for two test cases: (1) 1 + 2 and (2) 5 - 3. Output results clearly.";
  const out = wrap(one, 100);
  assert.ok(out.split("\n").length >= 4);
  assert.ok(longest(out) <= 100, "a line is too long: " + longest(out));
  assert.equal(out.replace(/\n/g, " "), one);
});

test("a description that is already wrapped is left as it is", () => {
  const ok = "Line one is short.\n\nLine two is short too.\n  indented line";
  assert.equal(wrap(ok, 100), ok);
});

test("a long list item keeps its indent on the lines it makes", () => {
  const item = "- " + "word ".repeat(40).trim();
  const lines = wrap(item, 60).split("\n");
  assert.ok(lines.length > 1);
  assert.ok(lines[0].startsWith("- "));
  for (const l of lines.slice(1)) assert.ok(l.startsWith("  "), JSON.stringify(l));
  assert.ok(longest(lines.join("\n")) <= 60);
});

test("a drawing is not touched, even when it is long", () => {
  const art = "   o   +--------------------------------------------------------------------------------------------------+";
  assert.equal(wrap(art, 100), art);
  const bar = "| " + "x".repeat(120) + " |";
  assert.equal(wrap(bar, 100), bar);
});

test("a word longer than the width stays whole", () => {
  const w = "a".repeat(130);
  assert.equal(wrap("see " + w + " end", 100), "see\n" + w + "\nend");
});

test("nothing and null give an empty text", () => {
  assert.equal(wrap("", 100), "");
  assert.equal(wrap(null, 100), "");
});
