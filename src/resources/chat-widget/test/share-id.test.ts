// node --test test/   (from src/resources/chat-widget; Node 22.18 or later runs TypeScript as it is)
//
// The widget keeps its own copy of the rule that tells a share link (/#<Firebase
// push key>) from an anchor of the page (/#languages), because it is built apart
// from the page script (js/src/share-id.ts). They must not drift.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const patternIn = (file: string) => {
  const m = fs.readFileSync(file, "utf8").match(/SHARE_ID_PATTERN\s*=\s*(\/\^[^\n]*?\$\/)/);
  assert.ok(m, "no SHARE_ID_PATTERN in " + file);
  return m![1];
};

test("the widget and the page script use the same rule for a share id", () => {
  const widget = patternIn(path.join(here, "..", "src", "index.ts"));
  const page = patternIn(path.join(here, "..", "..", "..", "js", "src", "share-id.ts"));
  assert.equal(widget, page);
});

test("the widget's rule takes a share link and leaves the anchors of the page", () => {
  const rule = new RegExp(patternIn(path.join(here, "..", "src", "index.ts")).slice(1, -1));
  for (const id of ["-JOJiRV-a8RYmm_zDNlx", "-KeGSp--p2fz1h5yyPnM", "-OJVdrF-2UPpWUafqBoU", "-P3WCLWG1hm_ZMkP2Al3"]) assert.ok(rule.test(id), id);
  for (const anchor of ["languages", "repl", "workspace", "request", "faq", "", "-Nx3kQ7", "-" + "a".repeat(40)]) assert.ok(!rule.test(anchor), anchor);
});
