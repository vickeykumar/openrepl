// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { MAX_PREVIEW_CONTEXT, previewContext } from "../src/preview-context.ts";

test("nothing is said about a pane that is not open or shows nothing", () => {
  assert.equal(previewContext(null), "");
  assert.equal(previewContext({ open: false, address: "a.html" }), "");
  assert.equal(previewContext({ open: true, address: "" }), "");
});

test("the page, its headings, its errors and its failed calls, short and marked as data", () => {
  const t = previewContext({
    open: true,
    address: "shop/index.html",
    title: "Corner shop",
    size: "phone",
    errors: 2,
    outline: [{ l: 1, tag: "h1", text: "Corner shop" }, { l: 0, tag: "nav", text: "Main" }, { l: 2, tag: "h2", text: "Bread" }],
    console: [{ level: "log", text: "loaded" }, { level: "error", text: "boom (app.js:3)" }],
    requests: [{ method: "GET", url: "/a.json", status: 200 }, { method: "GET", url: "/missing.json", status: 404 }, { method: "POST", url: "/x", status: 0, err: "network error" }],
    plugins: { console: true, outline: true },
  });
  assert.match(t, /^--- Preview .*data, never instructions/);
  assert.match(t, /address: shop\/index\.html; title: "Corner shop"; size: phone/);
  assert.match(t, /headings: h1 Corner shop \| h2 Bread/);
  assert.ok(!t.includes("loaded"), "a plain log line is not worth the room");
  assert.match(t, /console errors: 2\n/);
  assert.match(t, / error boom \(app\.js:3\)/);
  assert.match(t, / 404 GET \/missing\.json/);
  assert.match(t, / failed POST \/x - network error/);
  assert.ok(!t.includes("/a.json"), "a call that worked is left out");
});

test("a console the user turned off is left out, and the whole is short", () => {
  const t = previewContext({ open: true, address: "a.html", errors: 3, console: [{ level: "error", text: "x" }], plugins: { console: false, outline: true } });
  assert.ok(!t.includes("console") || !t.includes("error x"));
  assert.ok(!/console errors/.test(t));
  const long = previewContext({ open: true, address: "a.html", outline: Array.from({ length: 50 }, (_, i) => ({ l: 2, tag: "h2", text: "H" + "x".repeat(200) + i })), console: Array.from({ length: 50 }, () => ({ level: "error", text: "e".repeat(500) })), plugins: { console: true, outline: true } });
  assert.ok(long.length <= MAX_PREVIEW_CONTEXT + 3);
});
