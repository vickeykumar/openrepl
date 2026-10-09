import test from "node:test";
import assert from "node:assert/strict";
import { tipContent, placeTip } from "../src/page/lib-tips.mjs";

test("a tip has the button's name, its keys and its line", () => {
  assert.deepEqual(tipContent({ name: "Run", keys: "Mod Enter", desc: "Run the editor code (Ctrl+Enter)", isMac: false }), {
    name: "Run",
    keys: ["Ctrl", "Enter"],
    desc: "Run the editor code.",
  });
  // on a Mac the same shortcut is shown with its own key
  assert.deepEqual(tipContent({ name: "Run", keys: "Mod Enter", desc: "Run the editor code (⌘↵)", isMac: true }).keys, ["⌘", "Enter"]);
});

test("brackets at the end of a line stay when they are not a shortcut the key caps show", () => {
  const t = tipContent({ name: "New tab", keys: "", desc: "New terminal tab (up to 5)" });
  assert.equal(t.desc, "New terminal tab (up to 5).");
  assert.deepEqual(t.keys, []);
  // brackets in the middle are never touched
  assert.equal(tipContent({ name: "X", keys: "Esc", desc: "Close (the panel) now (Esc)" }).desc, "Close (the panel) now.");
});

test("a line that only repeats the name is left out", () => {
  assert.equal(tipContent({ name: "Hide files", desc: "Hide files" }).desc, "");
  assert.equal(tipContent({ name: "Commands", desc: "commands." }).desc, "");
  assert.equal(tipContent({ name: "Files", desc: "Show or hide files" }).desc, "Show or hide files.");
  // a line that ends a sentence already is left as it is
  assert.equal(tipContent({ name: "Close", desc: "Hide Genie. A task carries on." }).desc, "Hide Genie. A task carries on.");
});

test("a button with only a line shows it as its name, and one with nothing has no tip", () => {
  assert.deepEqual(tipContent({ desc: "Forks opened from this page" }), { name: "Forks opened from this page", keys: [], desc: "" });
  assert.equal(tipContent({}).name, "");
  assert.equal(tipContent({ name: null, keys: null, desc: null }).name, "");
});

test("a tip goes under its button, centred, with its pointer on the button", () => {
  const at = placeTip({ left: 400, right: 440, top: 10, bottom: 46 }, { width: 200, height: 50 }, { width: 1200, height: 800 });
  assert.deepEqual(at, { left: 320, top: 54, above: false, arrow: 100 });
});

test("a tip near an edge stays in the window, and its pointer still points at the button", () => {
  const left = placeTip({ left: 4, right: 40, top: 10, bottom: 46 }, { width: 200, height: 50 }, { width: 1200, height: 800 });
  assert.equal(left.left, 8);
  assert.equal(left.arrow, 14); // the button's middle (22) is 14 from the tip's edge
  const right = placeTip({ left: 1150, right: 1190, top: 10, bottom: 46 }, { width: 200, height: 50 }, { width: 1200, height: 800 });
  assert.equal(right.left, 992);
  assert.equal(right.arrow, 178);
  // a pointer never sits on the tip's rounded corner
  const corner = placeTip({ left: 1196, right: 1200, top: 10, bottom: 46 }, { width: 200, height: 50 }, { width: 1200, height: 800 });
  assert.equal(corner.arrow, 186);
});

test("a tip with no room under its button goes above it", () => {
  const at = placeTip({ left: 400, right: 440, top: 760, bottom: 790 }, { width: 200, height: 50 }, { width: 1200, height: 800 });
  assert.equal(at.above, true);
  assert.equal(at.top, 702);
  // with room on neither side it stays under, where it started
  const cramped = placeTip({ left: 400, right: 440, top: 20, bottom: 60 }, { width: 200, height: 50 }, { width: 1200, height: 90 });
  assert.equal(cramped.above, false);
});

test("a window narrower than the tip does not push it off the left edge", () => {
  const at = placeTip({ left: 10, right: 50, top: 10, bottom: 40 }, { width: 300, height: 50 }, { width: 200, height: 600 });
  assert.equal(at.left, 8);
});
