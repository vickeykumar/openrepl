// Every number or text field of the Settings form of the admin dashboard tells the form that it changed (oninput: update), or
// Save stays off after it is edited ("All changes are saved." with a new value in the box).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, "../../resources/js/admin.js"), "utf8");

test("the fields of the settings form that are assigned to form.<name> all call update when they change", () => {
  const lines = source.split(/\r?\n/);
  const missing = [];
  lines.forEach((line, i) => {
    // form.something = h('input', { ... }) written on one line; the line has to carry oninput: update
    const m = /^\s*form\.(\w+)\s*=\s*h\('input',/.exec(line);
    if (!m) return;
    // a field written over two lines carries it on the next
    const text = line + (line.includes("oninput") ? "" : " " + (lines[i + 1] || ""));
    if (!/oninput:\s*update/.test(text)) missing.push(m[1] + " (line " + (i + 1) + ")");
  });
  assert.deepEqual(missing, [], "these fields do not call update on input, so Save is not enabled when they change");
});
