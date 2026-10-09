// The page's side of pinning Genie beside the IDE (src/page/02-layout.js):
// pinned, the editor goes over the terminal to make room; unpinned, the layout
// goes back to what it was, unless the user chose one in between. The widget
// (chat-widget/src/index.ts) only fires "genie-pin"; this runs the page script
// with a small pretend page and the real layout functions.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "src", "page", "02-layout.js"), "utf8").replace(/^export \{\};.*$/m, "");

function page(startDirection = "horizontal", editorShown = true) {
  const listeners = {};
  const classList = { toggle() {}, add() {}, remove() {}, contains: () => false };
  const el = { classList, textContent: "", hidden: false, style: {}, setAttribute() {}, removeAttribute() {} };
  const window = {
    addEventListener: (type, fn) => (listeners[type] = fn),
    dispatchEvent: () => true,
    fire: (pinned) => listeners["genie-pin"]({ detail: { pinned } }),
  };
  const ctx = {
    window,
    document: { body: { classList }, documentElement: { style: { setProperty() {} } }, getElementById: () => el },
    get: () => el,
    ismob: () => false,
    Split: () => ({ destroy() {} }),
    getComputedStyle: () => ({ getPropertyValue: () => "0" }),
    setTimeout: () => 0,
    Event: class {},
    console,
    $: () => ({}),
  };
  // the script keeps its state in page globals: window.direction, window.einst
  ctx.direction = startDirection;
  ctx.einst = editorShown ? { destroy() {} } : null;
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  // the script's own top-level `window.einst = null` is the page's first state: put ours back
  ctx.direction = startDirection;
  ctx.einst = editorShown ? { destroy() {} } : null;
  return { ctx, fire: window.fire };
}

test("pinned, the IDE is stacked; unpinned, it goes back", () => {
  const p = page("horizontal");
  p.fire(true);
  assert.equal(p.ctx.direction, "vertical");
  p.fire(false);
  assert.equal(p.ctx.direction, "horizontal");
});

test("a layout the user chose while pinned is kept when it is unpinned", () => {
  const p = page("horizontal");
  p.fire(true);
  p.ctx.ToggleRotateEditor(); // the user goes back to side by side
  assert.equal(p.ctx.direction, "horizontal");
  p.fire(false);
  assert.equal(p.ctx.direction, "horizontal");
});

test("an IDE that was stacked already stays stacked after the pin", () => {
  const p = page("vertical");
  p.fire(true);
  assert.equal(p.ctx.direction, "vertical");
  p.fire(false);
  assert.equal(p.ctx.direction, "vertical");
});

test("a hidden editor stays hidden: pinning only turns the direction", () => {
  const p = page("horizontal", false);
  p.fire(true);
  assert.equal(p.ctx.direction, "vertical");
  assert.equal(p.ctx.einst, null, "the editor was shown by the pin");
  p.fire(false);
  assert.equal(p.ctx.direction, "horizontal");
  assert.equal(p.ctx.einst, null);
});

test("a pin before the page has chosen a layout does nothing, and later ones work", () => {
  const p = page(null);
  p.fire(true);
  assert.equal(p.ctx.direction, null);
  p.fire(false);
  p.ctx.direction = "horizontal";
  p.fire(true);
  assert.equal(p.ctx.direction, "vertical");
});

test("a second pin event does not forget what the layout was", () => {
  const p = page("horizontal");
  p.fire(true);
  p.fire(true);
  p.fire(false);
  assert.equal(p.ctx.direction, "horizontal");
});
