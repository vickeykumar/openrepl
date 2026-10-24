// The colours the IDE takes from the editor theme (page/lib-ide-theme.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import {
  parseColor, toHex, mix, contrast, ensureContrast, derivePalette, paletteCss, THEMED_SCOPE,
} from "../src/page/lib-ide-theme.mjs";

const monokai = { bg: "#272822", fg: "#F8F8F2", selection: "#49483E", comment: "#75715E", keyword: "#F92672", string: "#E6DB74", number: "#AE81FF", fn: "#A6E22E", type: "#66D9EF" };
const solarizedLight = { bg: "rgb(253, 246, 227)", fg: "rgb(88, 110, 117)", selection: "#EEE8D5", comment: "#93A1A1", keyword: "#859900", string: "#2AA198", number: "#D33682", fn: "#268BD2", type: "#B58900" };
const bare = { bg: "#000000", fg: "#ffffff" }; // a theme with no accent colours at all

test("colours are read from hex and rgb(a) strings, alpha laid over the background", () => {
  assert.deepEqual(parseColor("#fff"), [255, 255, 255]);
  assert.deepEqual(parseColor("#272822"), [39, 40, 34]);
  assert.deepEqual(parseColor("rgb(10, 20, 30)"), [10, 20, 30]);
  assert.deepEqual(parseColor("rgba(255, 255, 255, 0.5)", [0, 0, 0]).map(Math.round), [128, 128, 128]);
  assert.equal(parseColor("papayawhip"), null);
  assert.equal(parseColor(undefined), null);
});

test("mix and contrast behave", () => {
  assert.equal(toHex(mix([0, 0, 0], [255, 255, 255], 0.5)), "#808080");
  assert.ok(contrast([0, 0, 0], [255, 255, 255]) > 20.9);
  assert.equal(contrast([10, 10, 10], [10, 10, 10]), 1);
});

test("ensureContrast leaves a good pair alone and fixes a bad one", () => {
  assert.deepEqual(ensureContrast([255, 255, 255], [0, 0, 0], 4.5), [255, 255, 255]);
  const fixed = ensureContrast([60, 60, 60], [40, 40, 40], 4.5);
  assert.ok(contrast(fixed, [40, 40, 40]) >= 4.5);
  const onLight = ensureContrast([200, 200, 200], [250, 250, 250], 4.5);
  assert.ok(contrast(onLight, [250, 250, 250]) >= 4.5);
});

test("a dark theme: surface is the theme's own, then darker files, chrome and edge", () => {
  const p = derivePalette(monokai);
  assert.equal(p.dark, true);
  assert.equal(p.surface, "#272822");
  const lum = (h) => { const c = parseColor(h); return c[0] + c[1] + c[2]; };
  assert.ok(lum(p.files) < lum(p.surface));
  assert.ok(lum(p.chrome) < lum(p.files));
  assert.ok(lum(p.edge) < lum(p.chrome));
});

test("a light theme gets a pale bar and a soft border, still darker than the surface", () => {
  const p = derivePalette(solarizedLight);
  assert.equal(p.dark, false);
  assert.equal(p.surface, "#fdf6e3");
  const lum = (h) => { const c = parseColor(h); return c[0] + c[1] + c[2]; };
  assert.ok(lum(p.chrome) < lum(p.surface));
  assert.ok(lum(p.edge) < lum(p.chrome));
  assert.ok(lum(p.chrome) > lum(p.surface) * 0.85, "the bar stays pale");
});

test("text on the bars and the surface passes 4.5:1 in every sample theme", () => {
  [monokai, solarizedLight, bare].forEach((theme) => {
    const p = derivePalette(theme);
    [["text", "surface"], ["chromeText", "chrome"], ["muted", "chrome"], ["textStrong", "chrome"]].forEach(([fg, bg]) => {
      const c = contrast(parseColor(p[fg]), parseColor(p[bg]));
      assert.ok(c >= 4.5, `${fg} on ${bg} is ${c.toFixed(2)} for ${theme.bg}`);
    });
  });
});

test("the Run accent stands out on the bar, and its label reads on it", () => {
  [monokai, solarizedLight, bare].forEach((theme) => {
    const p = derivePalette(theme);
    assert.ok(contrast(parseColor(p.accent), parseColor(p.chrome)) >= 3, "accent on chrome " + theme.bg);
    assert.ok(contrast(parseColor(p.onAccent), parseColor(p.accent)) >= 4.5, "label on accent " + theme.bg);
  });
});

test("the terminal takes its colours from the theme and keeps errors red-ish", () => {
  const p = derivePalette(monokai);
  assert.equal(p.term.background, p.surface);
  assert.equal(p.term.foreground, p.text);
  const red = parseColor(p.term.red);
  assert.ok(red[0] > red[1] && red[0] > red[2], "red is red: " + p.term.red);
  const green = parseColor(p.term.green);
  assert.ok(green[1] > green[0] || green[1] > green[2], "green is green-ish: " + p.term.green);
  ["red", "green", "yellow", "blue", "magenta", "cyan"].forEach((n) => {
    assert.ok(contrast(parseColor(p.term[n]), parseColor(p.surface)) >= 4.5, n + " reads on the terminal");
  });
});

test("a light theme's terminal colours read on its pale background", () => {
  const p = derivePalette(solarizedLight);
  ["red", "green", "yellow", "blue", "magenta", "cyan", "white"].forEach((n) => {
    assert.ok(contrast(parseColor(p.term[n]), parseColor(p.surface)) >= 3, n + " " + p.term[n]);
  });
  assert.ok(contrast(parseColor(p.term.black), parseColor(p.surface)) >= 7);
});

test("a theme with no colours still gets every terminal colour", () => {
  const p = derivePalette(bare);
  ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "brightBlack", "brightRed", "brightGreen", "brightYellow", "brightBlue", "brightMagenta", "brightCyan", "brightWhite"].forEach((n) => {
    assert.match(p.term[n], /^#[0-9a-f]{6}$/, n);
  });
});

test("a missing or odd background falls back instead of throwing", () => {
  assert.doesNotThrow(() => derivePalette({}));
  assert.doesNotThrow(() => derivePalette({ bg: "transparent", fg: "nope" }));
});

test("the css remaps the dark-surface tokens inside the IDE and what opens over it", () => {
  const css = paletteCss(derivePalette(monokai));
  assert.ok(css.includes(THEMED_SCOPE));
  ["--ink-950", "--ink-900", "--ink-850", "--ink-700", "--paper", "--muted-1", "--accent-color", "--on-accent-color"].forEach((v) => {
    assert.ok(css.includes(v + ":"), v);
  });
  assert.ok(css.includes("color-scheme: dark"));
  assert.ok(paletteCss(derivePalette(solarizedLight)).includes("color-scheme: light"));
  // nothing outside the IDE is touched
  assert.ok(!/^body|^:root|^html \{/m.test(css));
});

test("Genie pinned beside the IDE gets the theme's colours, readable on the file browser's shade", () => {
  [monokai, solarizedLight, bare].forEach((theme) => {
    const p = derivePalette(theme);
    const bg = parseColor(p.genie.bg);
    assert.equal(p.genie.bg, p.files);
    ["text", "strong", "muted", "icon", "link"].forEach((n) => {
      assert.ok(contrast(parseColor(p.genie[n]), bg) >= 4.5, n + " on " + p.genie.bg + " is " + contrast(parseColor(p.genie[n]), bg).toFixed(2));
    });
    assert.ok(contrast(parseColor(p.genie.userText), parseColor(p.genie.userBg)) >= 4.5, "your message");
    assert.ok(contrast(parseColor(p.genie.codeText), parseColor(p.genie.code)) >= 7, "code in a reply");
  });
});

test("the css gives the docked Genie panel (and only it) the palette", () => {
  const css = paletteCss(derivePalette(monokai));
  assert.ok(css.includes("html[data-ide-theme] #chat-widget__container.is-docked {"));
  assert.ok(css.includes("--cw-bg: " + derivePalette(monokai).files));
  assert.ok(!/#chat-widget__container \{/.test(css), "the floating panel is not touched");
});

test("small grey text reads on every shade it can sit on (file browser, card, menu, bars)", () => {
  [monokai, solarizedLight, bare].forEach((theme) => {
    const p = derivePalette(theme);
    ["muted", "muted3", "chromeText"].forEach((n) => {
      ["chrome", "files", "card", "menu", "surface"].forEach((back) => {
        const c = contrast(parseColor(p[n]), parseColor(p[back]));
        assert.ok(c >= 4.5, `${n} on ${back} is ${c.toFixed(2)} (${theme.bg})`);
      });
    });
  });
});

test("the css fixes the colours the stylesheet writes out for dark menus and the guest card", () => {
  const css = paletteCss(derivePalette(solarizedLight));
  ["html[data-ide-theme] .sm-menu { color: var(--ide-text); }", "html[data-ide-theme] .sm-item { color: var(--ide-text); }", ".workspace-card__text { color: var(--muted-1); }", ".sm-swatch { border-color: var(--ink-500); }"].forEach((s) => {
    assert.ok(css.includes(s), s);
  });
});

test("file browser names, icons and title read on the panel, a hovered row and the selected row", () => {
  [monokai, solarizedLight, bare].forEach((theme) => {
    const p = derivePalette(theme);
    [["filesText", 9], ["filesSelected", 9], ["filesIcon", 5.5], ["filesTitle", 6]].forEach(([n, min]) => {
      ["files", "menu", "selection", "card"].forEach((back) => {
        const c = contrast(parseColor(p[n]), parseColor(p[back]));
        assert.ok(c >= min - 0.01, `${n} on ${back} is ${c.toFixed(2)} (${theme.bg})`);
      });
    });
  });
});

test("the css puts the clear file browser colours on names, icons, title and the selected row", () => {
  const css = paletteCss(derivePalette(monokai));
  ["--ide-files-text", "--ide-files-icon", "--ide-files-title", "--ide-files-selected", ".jstree-clicked", ".files-panel__title"].forEach((s) => {
    assert.ok(css.includes(s), s);
  });
});
