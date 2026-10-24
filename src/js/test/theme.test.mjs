// The light / dark / system theme choice (resources/js/theme.js): what it sets
// on <html>, what it keeps, and how it follows the device. The menu itself is
// checked in a browser.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "..", "resources", "js", "theme.js"), "utf8");

function page({ stored = null, storageThrows = false, deviceDark = false, buttons = 1 } = {}) {
  const attrs = {};
  const store = stored === null ? {} : { theme: stored };
  const events = [];
  const mqListeners = [];
  const mq = {
    matches: deviceDark,
    addEventListener: (t, fn) => mqListeners.push(fn),
  };
  const btns = [];
  for (let i = 0; i < buttons; i++) {
    const b = { attrs: {}, html: "", setAttribute(k, v) { this.attrs[k] = v; }, getAttribute(k) { return this.attrs[k] ?? null; }, hasAttribute(k) { return k in this.attrs; }, removeAttribute(k) { delete this.attrs[k]; } };
    Object.defineProperty(b, "innerHTML", { get() { return b.html; }, set(v) { b.html = v; } });
    btns.push(b);
  }
  const document = {
    head: null,
    documentElement: {
      setAttribute(k, v) { attrs[k] = v; },
      getAttribute(k) { return k in attrs ? attrs[k] : null; },
    },
    querySelectorAll: (sel) => (sel === "[data-theme-toggle]" ? btns : []),
    addEventListener() {},
  };
  const localStorage = {
    getItem(k) { if (storageThrows) throw new Error("blocked"); return k in store ? store[k] : null; },
    setItem(k, v) { if (storageThrows) throw new Error("blocked"); store[k] = v; },
    removeItem(k) { if (storageThrows) throw new Error("blocked"); delete store[k]; },
  };
  const window = {
    matchMedia: (q) => (/prefers-color-scheme/.test(q) ? mq : { matches: false }),
    addEventListener() {},
    dispatchEvent(e) { events.push(e.detail); return true; },
    innerWidth: 1280,
  };
  const ctx = { window, document, localStorage, CustomEvent: class { constructor(t, o) { this.type = t; this.detail = o && o.detail; } } };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  const deviceChanges = (dark) => { mq.matches = dark; mqListeners.forEach((fn) => fn()); };
  return { window, attrs, store, events, btns, deviceChanges };
}

test("with nothing saved the page follows the device: the choice is System", () => {
  const light = page({ deviceDark: false });
  assert.equal(light.attrs["data-theme"], "light");
  assert.equal(light.attrs["data-theme-choice"], "system");
  const dark = page({ deviceDark: true });
  assert.equal(dark.attrs["data-theme"], "dark");
  assert.equal(dark.attrs["data-theme-choice"], "system");
});

test("a saved light or dark choice wins over the device", () => {
  const p = page({ stored: "light", deviceDark: true });
  assert.equal(p.attrs["data-theme"], "light");
  assert.equal(p.attrs["data-theme-choice"], "light");
  const q = page({ stored: "dark", deviceDark: false });
  assert.equal(q.attrs["data-theme"], "dark");
  assert.equal(q.attrs["data-theme-choice"], "dark");
});

test("an unknown saved value is System", () => {
  const p = page({ stored: "sepia", deviceDark: true });
  assert.equal(p.attrs["data-theme-choice"], "system");
  assert.equal(p.attrs["data-theme"], "dark");
});

test("choosing Light or Dark is remembered; choosing System forgets it", () => {
  const p = page({ deviceDark: true });
  p.window.setThemeChoice("light");
  assert.equal(p.store.theme, "light");
  assert.equal(p.attrs["data-theme"], "light");
  p.window.setThemeChoice("dark");
  assert.equal(p.store.theme, "dark");
  p.window.setThemeChoice("system");
  assert.equal("theme" in p.store, false);
  assert.equal(p.attrs["data-theme"], "dark", "System on a dark device is dark");
  assert.equal(p.attrs["data-theme-choice"], "system");
});

test("anything else chosen is System", () => {
  const p = page({ stored: "dark" });
  assert.equal(p.window.setThemeChoice("blue"), "system");
  assert.equal("theme" in p.store, false);
});

test("on System the page follows the device when it changes; on a fixed choice it does not", () => {
  const p = page({ deviceDark: false });
  p.deviceChanges(true);
  assert.equal(p.attrs["data-theme"], "dark");
  p.deviceChanges(false);
  assert.equal(p.attrs["data-theme"], "light");
  p.window.setThemeChoice("light");
  p.deviceChanges(true);
  assert.equal(p.attrs["data-theme"], "light");
});

test("a browser that blocks storage still keeps the choice for the page", () => {
  const p = page({ storageThrows: true, deviceDark: true });
  assert.equal(p.attrs["data-theme-choice"], "system");
  p.window.setThemeChoice("light");
  assert.equal(p.attrs["data-theme"], "light");
  assert.equal(p.window.getThemeChoice(), "light");
  p.window.setThemeChoice("system");
  assert.equal(p.attrs["data-theme"], "dark");
});

test("themechange says what the choice and the theme became, once per change", () => {
  const p = page({ deviceDark: false });
  const before = p.events.length;
  p.window.setThemeChoice("dark");
  assert.deepEqual(JSON.parse(JSON.stringify(p.events.slice(before))), [{ choice: "dark", theme: "dark" }]);
  p.window.setThemeChoice("dark");
  assert.equal(p.events.length, before + 1, "nothing new to say");
});

test("toggleTheme flips light and dark, as it always did", () => {
  const p = page({ deviceDark: false });
  assert.equal(p.window.toggleTheme(), "dark");
  assert.equal(p.attrs["data-theme"], "dark");
  assert.equal(p.window.toggleTheme(), "light");
});

test("each theme button shows the choice and says what it does", () => {
  const p = page({ buttons: 2, deviceDark: true });
  p.btns.forEach((b) => {
    assert.match(b.getAttribute("aria-label"), /Theme: System \(Dark\)/);
    assert.equal(b.getAttribute("aria-haspopup"), "menu");
    assert.ok(b.html.includes("<svg"));
  });
  p.window.setThemeChoice("light");
  assert.match(p.btns[0].getAttribute("aria-label"), /Theme: Light\. Change theme/);
});
