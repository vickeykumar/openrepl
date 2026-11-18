// The script the preview puts in a previewed page (src/livepreview/storage.go, storageJS): a write is sent after 40 ms, but one made while the page
// is going away (pagehide, beforeunload, hidden) is sent at once, since a timer does not run in a page that is being torn down. Without that, the
// page that takes its entry out of a cookie when it closes (the terminal count of OpenREPL itself) loses the removal.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const go = fs.readFileSync(path.join(here, "..", "..", "livepreview", "storage.go"), "utf8");
const js = go.slice(go.indexOf("const storageJS = `") + "const storageJS = `".length, go.indexOf("`", go.indexOf("const storageJS = `") + 20)).replace(/<\/script>\s*$/, "");

function page() {
  const sent = [];
  const listeners = {};
  const docListeners = {};
  const timers = [];
  const document = { visibilityState: "visible", addEventListener: (t, f) => ((docListeners[t] = docListeners[t] || []).push(f)) };
  const window = {
    fetch: (url, opts) => { sent.push(JSON.parse(opts.body)); return Promise.resolve(); },
    addEventListener: (t, f) => ((listeners[t] = listeners[t] || []).push(f)),
  };
  const ctx = {
    window, document, Date, JSON, Object, Math, Proxy, String, parseInt, isNaN, Array,
    setTimeout: (f, ms) => { timers.push(f); return timers.length; },
    clearTimeout: (n) => { if (n) timers[n - 1] = null; },
  };
  vm.createContext(ctx);
  vm.runInContext("(function(){var S={cookie:{}},U='/s',SC='p:8080'" + js, ctx);
  const fire = (t) => (listeners[t] || []).forEach((f) => f());
  const flushTimers = () => timers.splice(0).forEach((f) => f && f());
  return { document, sent, fire, flushTimers, docListeners };
}

test("a cookie write is sent after a moment, and not twice", () => {
  const p = page();
  p.document.cookie = "a=1";
  assert.equal(p.sent.length, 0);
  p.flushTimers();
  assert.equal(p.sent.length, 1);
  assert.equal(p.sent[0].area, "cookie");
});

test("a write made while the page is going away is sent at once", () => {
  const p = page();
  p.fire("pagehide"); // the script's own listener runs before the page's, so the flag is set when the page writes
  p.document.cookie = "count=gone; max-age=0";
  assert.equal(p.sent.length, 1, "no timer: it is sent in the event");
  assert.ok(!("count" in p.sent[0].data), "the removal is what was sent");
});

test("a write that is still waiting when the page goes away is sent then", () => {
  const p = page();
  p.document.cookie = "b=2";
  assert.equal(p.sent.length, 0);
  p.fire("beforeunload");
  assert.equal(p.sent.length, 1);
  assert.deepEqual(p.sent[0].data.b.v, "2");
  p.flushTimers();
  assert.equal(p.sent.length, 1, "the timer does not send it again");
});

test("a page that is shown again goes back to the short delay", () => {
  const p = page();
  p.fire("pagehide");
  p.fire("pageshow");
  p.document.cookie = "c=3";
  assert.equal(p.sent.length, 0);
});
