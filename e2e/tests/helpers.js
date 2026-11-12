// Small helpers for the browser tests.

// "rgb(10, 20, 30)" or "rgba(10, 20, 30, 0.5)" -> {r, g, b, a}
function parseRgb(text) {
  const m = /rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+))?\s*\)/.exec(text || "");
  if (!m) return null;
  return { r: +m[1], g: +m[2], b: +m[3], a: m[4] === undefined ? 1 : +m[4] };
}

// relative luminance, 0 (black) to 1 (white)
function luminance({ r, g, b }) {
  const f = (v) => {
    v /= 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

// The luminance of what a visitor sees behind an element: its own background colour, or the
// nearest ancestor's when it has none.
async function backgroundLuminance(page, selector) {
  const color = await page.locator(selector).first().evaluate((el) => {
    for (let node = el; node; node = node.parentElement) {
      const c = getComputedStyle(node).backgroundColor;
      const m = /rgba?\(([^)]*)\)/.exec(c);
      if (!m) continue;
      const parts = m[1].split(/[,\s/]+/).filter(Boolean);
      const alpha = parts.length > 3 ? parseFloat(parts[3]) : 1;
      if (alpha > 0.5) return c;
    }
    return "rgb(255, 255, 255)";
  });
  return luminance(parseRgb(color));
}

// Chooses a theme through the page's own API (what the theme menu calls).
async function setTheme(page, choice) {
  await page.evaluate((c) => window.setThemeChoice(c), choice);
}

// Waits until the home page's terminal says Connected, and has had the time to take the keyboard focus. The terminal
// connects a little after the page loads and takes the focus when it does; a test that types, presses a shortcut or
// opens a menu before that can have its focus taken away in the middle (this made the theme menu, the Genie
// Ctrl+Shift+L and the terminal shortcut tests fail on a slow runner). Every test that uses the keyboard on the
// home page starts with this.
async function waitForTerminalReady(page) {
  const { expect } = require("@playwright/test");
  await expect(page.locator("#term-footer-text")).toContainText("Connected", { timeout: 30 * 1000 });
  // the focus moves to the terminal just after it connects: give it until it has, but do not insist
  await page
    .waitForFunction(() => !!(document.activeElement && document.activeElement.closest && document.activeElement.closest(".xterm")), null, { timeout: 3000 })
    .catch(() => {});
}

module.exports = { parseRgb, luminance, backgroundLuminance, setTheme, waitForTerminalReady };
