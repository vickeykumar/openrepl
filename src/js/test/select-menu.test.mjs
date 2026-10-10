// The parts of the IDE's dropdown menu (resources/js/select-menu.js) that need no
// page: the list built from a select, the search, where the menu goes, how the
// keys move, and type-ahead. The page part is checked in a browser.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "..", "resources", "js", "select-menu.js"), "utf8");

function load() {
  const window = { matchMedia: () => ({ matches: true }) }; // a touch screen: init() enhances nothing
  const ctx = { window, document: { readyState: "complete", querySelectorAll: () => [] }, console, Object, HTMLSelectElement: function () {} };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return window.SelectMenu;
}
const SM = load();
// what the script made lives in another realm (the vm): compared as plain data
const plain = (x) => JSON.parse(JSON.stringify(x));

// a select as the page has it: options, some in groups, with data-ext and data-swatch
const opt = (value, text, attrs = {}, disabled = false) => ({
  tagName: "OPTION", value, text, disabled,
  getAttribute: (k) => (k in attrs ? attrs[k] : null),
});
const group = (label, children) => ({ tagName: "OPTGROUP", children, getAttribute: (k) => (k === "label" ? label : null) });
const select = (children) => ({ children });

const languages = select([
  opt("c", "C", { "data-ext": ".c" }), opt("python", "Python", { "data-ext": ".py" }), opt("python2.7", "Python2.7", { "data-ext": ".py" }),
  opt("ipython3", "IPython3", { "data-ext": ".py" }), opt("javascript", "JavaScript", { "data-ext": ".js" }), opt("node", "NodeJS", { "data-ext": ".js" }),
]);

test("the list is made of the options, in order, with their extension and colour", () => {
  const items = plain(SM.itemsOf(languages));
  assert.deepEqual(items.map((i) => i.label), ["C", "Python", "Python2.7", "IPython3", "JavaScript", "NodeJS"]);
  assert.deepEqual(items.map((i) => i.index), [0, 1, 2, 3, 4, 5]);
  assert.equal(items[1].badge, ".py");
  assert.equal(items[1].value, "python");
  assert.equal(items[0].swatch, "");
  assert.equal(items[0].group, "");
});

test("options in groups keep their group, and count through the groups", () => {
  const themes = select([
    group("Bright", [opt("a/chrome", "Chrome", { "data-swatch": "#ffffff" }), opt("a/github", "GitHub", { "data-swatch": "#ffffff" })]),
    group("Dark", [opt("a/monokai", "Monokai", { "data-swatch": "#272822" })]),
  ]);
  const items = plain(SM.itemsOf(themes));
  assert.deepEqual(items.map((i) => [i.label, i.group, i.index, i.swatch]), [["Chrome", "Bright", 0, "#ffffff"], ["GitHub", "Bright", 1, "#ffffff"], ["Monokai", "Dark", 2, "#272822"]]);
});

test("whitespace in an option's text is not shown, and a disabled option is marked", () => {
  const items = SM.itemsOf(select([opt("x", "  Spaced  \n"), opt("y", "Off", {}, true)]));
  assert.equal(items[0].label, "Spaced");
  assert.equal(items[1].disabled, true);
});

test("typing narrows the list by name, by extension and by group", () => {
  const items = SM.itemsOf(languages);
  const names = (q) => plain(SM.filterItems(items, q)).map((i) => i.label);
  assert.deepEqual(names(""), ["C", "Python", "Python2.7", "IPython3", "JavaScript", "NodeJS"]);
  assert.deepEqual(names("py"), ["Python", "Python2.7", "IPython3"], "inside a word too: IPython3");
  assert.deepEqual(names("PY"), ["Python", "Python2.7", "IPython3"], "any case");
  assert.deepEqual(names(".js"), ["JavaScript", "NodeJS"], "by extension");
  assert.deepEqual(names("py 2"), ["Python2.7"], "every word has to match");
  assert.deepEqual(names("zzz"), []);
  const themes = SM.itemsOf(select([group("Bright", [opt("a", "Chrome")]), group("Dark", [opt("b", "Monokai"), opt("c", "Dracula")])]));
  assert.deepEqual(plain(SM.filterItems(themes, "dark")).map((i) => i.label), ["Monokai", "Dracula"], "a group name finds its members");
});

test("the menu goes under its button, at least as wide, and inside the window", () => {
  const view = { width: 1200, height: 800 };
  const at = SM.placeMenu({ left: 100, right: 260, top: 60, bottom: 96 }, 240, 300, view);
  assert.deepEqual({ ...at }, { left: 100, width: 240, up: false, top: 100, bottom: null, maxHeight: 692 });
  // a wide button makes a wide menu
  assert.equal(SM.placeMenu({ left: 100, right: 500, top: 60, bottom: 96 }, 240, 300, view).width, 400);
  // against the right edge it slides left
  const right = SM.placeMenu({ left: 1150, right: 1190, top: 60, bottom: 96 }, 240, 300, view);
  assert.equal(right.left, 1200 - 240 - 8);
  // against the left edge it stays inside
  assert.equal(SM.placeMenu({ left: 0, right: 20, top: 60, bottom: 96 }, 240, 300, view).left, 8);
});

test("in the status bar at the bottom the menu opens upwards", () => {
  const view = { width: 1200, height: 800 };
  const at = SM.placeMenu({ left: 900, right: 1000, top: 760, bottom: 784 }, 240, 300, view);
  assert.equal(at.up, true);
  assert.equal(at.top, null);
  assert.equal(at.bottom, 800 - 760 + 4, "it hangs from the top of the button, so a shorter list stays attached");
  assert.equal(at.maxHeight, 760 - 4 - 8);
  // with room on neither side it uses the larger, and never less than 120px
  const cramped = SM.placeMenu({ left: 10, right: 80, top: 40, bottom: 70 }, 240, 300, { width: 1200, height: 100 });
  assert.ok(cramped.maxHeight >= 120);
  // a window narrower than the menu
  assert.equal(SM.placeMenu({ left: 10, right: 80, top: 60, bottom: 90 }, 240, 100, { width: 200, height: 800 }).width, 184);
});

test("the keys move through the shown items, and skip what is off", () => {
  const items = SM.itemsOf(select([opt("a", "A"), opt("b", "B", {}, true), opt("c", "C"), opt("d", "D")]));
  const m = (active, key) => SM.moveActive(items, active, key);
  assert.equal(m(-1, "ArrowDown"), 0);
  assert.equal(m(0, "ArrowDown"), 2, "B is off");
  assert.equal(m(3, "ArrowDown"), 3, "the end stays at the end");
  assert.equal(m(2, "ArrowUp"), 0);
  assert.equal(m(0, "ArrowUp"), 0);
  assert.equal(m(-1, "ArrowUp"), 3, "up from nothing is the last");
  assert.equal(m(2, "Home"), 0);
  assert.equal(m(0, "End"), 3);
  assert.equal(m(0, "x"), 0, "any other key leaves it");
  assert.equal(SM.moveActive([], -1, "ArrowDown"), -1);
  assert.equal(SM.moveActive(items, 0, "PageDown", 2), 2);
});

test("typing in a list with no search box jumps to a name that starts so", () => {
  const items = SM.itemsOf(select([opt("c", "Chrome"), opt("cl", "Clouds"), opt("d", "Dawn"), opt("dr", "Dracula")]));
  assert.equal(SM.typeAhead(items, "d", 0), 2, "from the one before, the next that starts with d");
  assert.equal(SM.typeAhead(items, "d", 2), 3, "the same letter again goes on to the next");
  assert.equal(SM.typeAhead(items, "dr", 2), 3, "more letters narrow it");
  assert.equal(SM.typeAhead(items, "cl", 0), 1);
  assert.equal(SM.typeAhead(items, "z", 0), -1);
  assert.equal(SM.typeAhead(items, "", 0), -1);
});
