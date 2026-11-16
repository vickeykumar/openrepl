// when() in the admin dashboard returns an element (a live "5 min ago"), so joining it to a string with + prints
// "[object HTMLSpanElement]" (it did, on the App Store's Servers tab). It must be given to h() as a child.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

test("admin.js never joins the result of when() to a string", () => {
  const src = readFileSync(new URL("../../resources/js/admin.js", import.meta.url), "utf8");
  const bad = src.split("\n").map((l, i) => [i + 1, l]).filter(([, l]) => /['"]\s*\+\s*when\(|when\([^)]*\)\s*\+\s*['"]/.test(l));
  assert.deepEqual(bad.map(([n]) => n), []);
});
