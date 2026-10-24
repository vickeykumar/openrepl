// The IDE follows the editor theme (21-ide-theme.js): the colours the theme
// paints are read from the page, and this turns them into the layers of the
// IDE shell and the terminal's colours. Pure functions, no DOM, so they can be
// tested on their own (test/ide-theme.test.mjs).
//
//   surface  the editor and the terminal: the theme's own background
//   files    the file browser: a step darker
//   chrome   app bar, tab strips, status bar, menus: a darker shade
//   edge     every 1px border and divider: darker again
//
// Dark themes darken by 18 / 35 / 60 %, light themes by 3 / 6 / 16 %, the way
// editors like VS Code do, so a light theme gets a pale bar and a soft border.

const BLACK = [0, 0, 0];
const WHITE = [255, 255, 255];

// "#rgb", "#rrggbb", "rgb(...)" or "rgba(...)" -> [r, g, b] (alpha is laid over `over` when given)
export function parseColor(input, over) {
  if (typeof input !== "string") return null;
  const s = input.trim().toLowerCase();
  let m = /^#([0-9a-f]{3})$/.exec(s);
  if (m) return [...m[1]].map((h) => parseInt(h + h, 16));
  m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/.exec(s);
  if (m) {
    const rgb = [0, 2, 4].map((i) => parseInt(m[1].slice(i, i + 2), 16));
    return m[2] ? over_(rgb, parseInt(m[2], 16) / 255, over) : rgb;
  }
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:[\s,/]+([\d.]+%?))?\s*\)$/.exec(s);
  if (m) {
    const rgb = [m[1], m[2], m[3]].map(Number);
    let a = 1;
    if (m[4] !== undefined) a = m[4].endsWith("%") ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
    return a >= 1 ? rgb : over_(rgb, a, over);
  }
  return null;
}
function over_(rgb, alpha, over) {
  if (alpha >= 1) return rgb;
  if (!over) return rgb; // nothing to lay it over: keep the colour, the caller checks contrast anyway
  return rgb.map((v, i) => v * alpha + over[i] * (1 - alpha));
}

export function toHex(rgb) {
  return "#" + rgb.map((v) => Math.round(Math.max(0, Math.min(255, v))).toString(16).padStart(2, "0")).join("");
}

export function mix(a, b, t) {
  return a.map((v, i) => v + (b[i] - v) * t);
}

export function luminance(rgb) {
  const f = (v) => {
    v /= 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
}

export function contrast(a, b) {
  const la = luminance(a), lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

// Move `fg` toward white or black (whichever the background is far from)
// until it reads on `bg` at `min`:1.
export function ensureContrast(fg, bg, min) {
  if (contrast(fg, bg) >= min) return fg;
  const target = luminance(bg) < 0.4 ? WHITE : BLACK;
  let out = fg;
  for (let i = 1; i <= 20; i++) {
    out = mix(fg, target, i / 20);
    if (contrast(out, bg) >= min) return out;
  }
  return out;
}

function hsl(rgb) {
  const [r, g, b] = rgb.map((v) => v / 255);
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return { h: 0, s: 0, l };
  const s = d / (1 - Math.abs(2 * l - 1));
  let h;
  if (max === r) h = ((g - b) / d) % 6;
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  h *= 60;
  if (h < 0) h += 360;
  return { h, s, l };
}
function hueGap(a, b) {
  const d = Math.abs(a - b) % 360;
  return d > 180 ? 360 - d : d;
}

// The terminal's eight colours, each taken from the theme colour nearest in hue
// (so an error is red-ish in every theme), or a default when the theme has none.
const ANSI_HUES = { red: 5, green: 125, yellow: 48, blue: 215, magenta: 295, cyan: 185 };
const FALLBACK_DARK = { red: "#ff7b7c", green: "#7fd6a4", yellow: "#f2c66d", blue: "#7aa7ff", magenta: "#d39bff", cyan: "#6fd3e3" };
const FALLBACK_LIGHT = { red: "#c62828", green: "#2e7d32", yellow: "#a15c00", blue: "#1565c0", magenta: "#7b1fa2", cyan: "#00797f" };

function ansiColors(theme, bg, dark, fg) {
  const candidates = [theme.keyword, theme.string, theme.number, theme.fn, theme.type]
    .map((c) => parseColor(c, bg))
    .filter(Boolean)
    .map((rgb) => ({ rgb, ...hsl(rgb) }))
    .filter((c) => c.s >= 0.2);
  const fallback = dark ? FALLBACK_DARK : FALLBACK_LIGHT;
  const out = {};
  Object.keys(ANSI_HUES).forEach((name) => {
    let best = null;
    candidates.forEach((c) => {
      const gap = hueGap(c.h, ANSI_HUES[name]);
      if (gap <= 40 && (!best || gap < best.gap)) best = { gap, rgb: c.rgb };
    });
    const rgb = best ? best.rgb : parseColor(fallback[name]);
    out[name] = ensureContrast(rgb, bg, 4.5);
  });
  out.black = dark ? ensureContrast(mix(bg, fg, 0.3), bg, 1.8) : fg;
  out.white = dark ? mix(fg, bg, 0.2) : ensureContrast(mix(fg, bg, 0.35), bg, 3);
  const edgeTarget = dark ? WHITE : BLACK;
  const bright = {};
  Object.keys(out).forEach((name) => {
    bright[name] = ensureContrast(mix(out[name], edgeTarget, 0.2), bg, name === "black" ? 2.2 : 4.5);
  });
  bright.black = dark ? ensureContrast(mix(bg, fg, 0.5), bg, 3) : mix(fg, bg, 0.3);
  bright.white = dark ? fg : mix(fg, bg, 0.15);
  return { out, bright };
}

// The colours of Ace's Monokai theme, the default editor theme: the IDE can start in them
// before the editor has loaded its theme, so a first visit does not flash.
export const MONOKAI_THEME = {
  bg: "#272822", fg: "#F8F8F2", selection: "#49483E", comment: "#75715E",
  keyword: "#F92672", string: "#E6DB74", number: "#AE81FF", fn: "#A6E22E", type: "#66D9EF"
};

// theme: colours read from the page, any CSS colour string:
//   bg, fg, gutterBg, gutterFg, selection, comment, keyword, string, number, fn, type
// returns hex strings for the shell, and `term` for xterm.js.
export function derivePalette(theme) {
  const bg = parseColor(theme.bg) || [13, 13, 18];
  const fg = ensureContrast(parseColor(theme.fg, bg) || [230, 228, 236], bg, 7);
  const dark = luminance(bg) < 0.3;
  const k = dark ? { files: 0.18, chrome: 0.35, edge: 0.6 } : { files: 0.03, chrome: 0.06, edge: 0.16 };
  const files = mix(bg, BLACK, k.files);
  const chrome = mix(bg, BLACK, k.chrome);
  const edge = mix(bg, BLACK, k.edge);
  const textStrong = dark ? fg : ensureContrast(mix(fg, BLACK, 0.35), chrome, 7);
  const chromeText = ensureContrast(dark ? mix(fg, chrome, 0.2) : mix(fg, BLACK, 0.15), chrome, 7);
  // the card the guest-workspace note sits on, and the menus: shades small grey text can land on
  const card = dark ? mix(chrome, WHITE, 0.06) : mix(chrome, BLACK, 0.03);
  const menuShade = dark ? mix(chrome, WHITE, 0.04) : bg;
  const everywhere = (c, min) => [chrome, files, card, menuShade, bg].reduce((acc, back) => ensureContrast(acc, back, min), c);
  const muted = everywhere(mix(fg, chrome, dark ? 0.5 : 0.35), 4.5);
  const muted3 = everywhere(mix(muted, chrome, 0.3), 4.5);
  const hover = dark ? mix(chrome, WHITE, 0.08) : mix(chrome, BLACK, 0.05);
  let selection = parseColor(theme.selection, bg);
  if (!selection || contrast(selection, bg) < 1.12) selection = mix(bg, fg, 0.15);
  const menu = menuShade;

  // the file browser: names, icons and the title must read on the panel, a hovered row and the selected row
  const hoverRow = menuShade;
  const filesBacks = [files, hoverRow, selection, card];
  const filesEach = (c, min) => filesBacks.reduce((acc, back) => ensureContrast(acc, back, min), c);
  const filesText = filesEach(fg, 9);
  const filesIcon = filesEach(mix(fg, files, 0.2), 5.5);
  const filesTitle = filesEach(mix(fg, files, 0.3), 6);
  const filesSelected = filesEach(textStrong, 9);

  // the one strong colour of the shell (the Run button): the theme's function colour, or the next lively one, nudged to stand out on the bar
  let accent = null;
  [theme.fn, theme.keyword, theme.string, theme.number].forEach((c) => {
    const rgb = parseColor(c, bg);
    if (!accent && rgb && hsl(rgb).s >= 0.2) accent = ensureContrast(rgb, chrome, 3);
  });
  if (!accent) accent = textStrong;
  // the label on it: white or near-black, and the accent is moved away from the label until it reads (4.5:1)
  let onAccent = contrast(accent, BLACK) >= contrast(accent, WHITE) ? [18, 18, 22] : [255, 255, 255];
  for (let i = 1; i <= 20 && contrast(accent, onAccent) < 4.5; i++) {
    accent = mix(accent, onAccent === WHITE || onAccent[0] === 255 ? BLACK : WHITE, 0.05);
  }
  const ok = ensureContrast(parseColor(theme.string, bg) || accent, chrome, 3);

  // Genie pinned beside the IDE: the panel is a side bar, so it sits on the file browser's shade
  const onFiles = (c, min) => ensureContrast(c, files, min);
  const genie = {
    bg: files,
    border: edge,
    text: onFiles(fg, 7),
    strong: onFiles(textStrong, 7),
    muted: onFiles(mix(fg, files, dark ? 0.5 : 0.35), 4.5),
    icon: onFiles(chromeText, 4.5),
    fieldBg: bg,
    fieldBorder: dark ? mix(edge, WHITE, 0.1) : mix(edge, BLACK, 0.08),
    code: dark ? mix(bg, BLACK, 0.3) : mix(bg, BLACK, 0.04),
    codeText: ensureContrast(fg, dark ? mix(bg, BLACK, 0.3) : mix(bg, BLACK, 0.04), 7),
    secondary: dark ? mix(files, WHITE, 0.08) : mix(files, BLACK, 0.05),
    userBg: accent,
    userText: onAccent,
    link: onFiles(accent, 4.5),
    chip: chrome,
    chipText: ensureContrast(chromeText, chrome, 4.5),
    popBg: menu,
    popBorder: edge,
    segBg: chrome,
    segOn: selection
  };
  const genieHex = Object.fromEntries(Object.keys(genie).map((n) => [n, toHex(genie[n])]));
  const { out, bright } = ansiColors(theme, bg, dark, fg);
  const hex = (rgb) => toHex(rgb);
  const termHex = (o) => Object.fromEntries(Object.keys(o).map((n) => [n, hex(o[n])]));
  const a = termHex(out), b = termHex(bright);
  return {
    dark,
    surface: hex(bg),
    files: hex(files),
    chrome: hex(chrome),
    edge: hex(edge),
    menu: hex(menu),
    text: hex(fg),
    textStrong: hex(textStrong),
    chromeText: hex(chromeText),
    muted: hex(muted),
    muted3: hex(muted3),
    filesText: hex(filesText),
    filesIcon: hex(filesIcon),
    filesTitle: hex(filesTitle),
    filesSelected: hex(filesSelected),
    card: hex(card),
    hover: hex(hover),
    selection: hex(selection),
    accent: hex(accent),
    onAccent: hex(onAccent),
    ok: hex(ok),
    genie: genieHex,
    term: {
      background: hex(bg),
      foreground: hex(fg),
      cursor: hex(accent),
      cursorAccent: hex(bg),
      selectionBackground: hex(selection),
      selectionForeground: hex(fg),
      black: a.black, red: a.red, green: a.green, yellow: a.yellow, blue: a.blue, magenta: a.magenta, cyan: a.cyan, white: a.white,
      brightBlack: b.black, brightRed: b.red, brightGreen: b.green, brightYellow: b.yellow, brightBlue: b.blue, brightMagenta: b.magenta, brightCyan: b.cyan, brightWhite: b.white
    }
  };
}

// The CSS that puts the palette on the IDE: the page's own dark-surface tokens
// (--ink-*, --paper, --muted-*) are remapped inside the IDE and what opens over
// it, and the few colours written out in ui-refresh.css are overridden.
export const THEMED_SCOPE =
  "html[data-ide-theme] :is(.ide-shell, .vakata-context, .vakata-context ul, .ctx-menu, .sm-menu, .palette, .ide-tip, .run-menu)";

export function paletteCss(p) {
  const dark = p.dark;
  const mixHex = (a, b, t) => toHex(mix(parseColor(a), parseColor(b), t));
  const ink = {
    "--ink-950": p.edge,
    "--ink-900": p.files,
    "--ink-850": p.chrome,
    "--ink-800": p.card,
    "--ink-750": p.menu,
    "--ink-700": p.edge,
    "--ink-600": dark ? mixHex(p.edge, p.text, 0.14) : mixHex(p.edge, "#000000", 0.1),
    "--ink-500": dark ? mixHex(p.edge, p.text, 0.24) : mixHex(p.edge, "#000000", 0.2),
    "--paper": p.textStrong,
    "--muted-1": p.chromeText,
    "--muted-2": p.muted,
    "--muted-3": p.muted3,
    "--accent-color": p.accent,
    "--on-accent-color": p.onAccent,
    "--accent-on-dark": p.accent,
    "--ok-color": p.ok,
    "--ide-surface": p.surface,
    "--ide-selection": p.selection,
    "--ide-text": p.text,
    "--ide-chrome-text": p.chromeText,
    "--ide-files-text": p.filesText,
    "--ide-files-icon": p.filesIcon,
    "--ide-files-title": p.filesTitle,
    "--ide-files-selected": p.filesSelected
  };
  const vars = Object.keys(ink).map((n) => "  " + n + ": " + ink[n] + ";").join("\n");
  const S = "html[data-ide-theme] ";
  const g = p.genie;
  return [
    THEMED_SCOPE + " {\n" + vars + "\n  color-scheme: " + (dark ? "dark" : "light") + ";\n}",
    // colours the stylesheet writes out
    S + ".ide-shell .ide-btn, " + S + ".ide-shell .app-bar .share-btn, " + S + ".ide-shell .app-bar .forkbtn, " +
      S + ".ide-shell .editor-header__tools .ide-btn, " + S + ".ide-shell .terminal-tabs__actions .ide-btn, " +
      S + ".ide-shell .app-bar #newQuestionBtn, " + S + ".ide-shell .app-bar #randomQuestionBtn { color: var(--ide-chrome-text); }",
    S + ".ide-split .terminal-div, " + S + ".ide-split .terminal-div .terminal, " + S + ".ide-split .terminal-div .xterm, " +
      S + ".ide-split .terminal-div .xterm .xterm-viewport { background-color: var(--ide-surface); }",
    S + ".ide-shell .files-panel .jstree-default .jstree-wholerow-clicked, " + S + ".ide-shell .files-panel .jstree-default .jstree-clicked { background: var(--ide-selection); }",
    S + ".ide-shell .term-banner__text { color: var(--ide-text); }",
    S + ".ide-shell .workspace-card__text { color: var(--muted-1); }",
    // the file browser: clear names, icons and title, a stronger selected row
    S + ".ide-shell .files-panel .jstree-default .jstree-anchor, " + S + ".ide-shell .files-panel .jstree-default .jstree-anchor:hover, " + S + ".ide-shell .files-panel .main-menu { color: var(--ide-files-text); }",
    S + ".ide-shell .files-panel .jstree-default .jstree-clicked, " + S + ".ide-shell .files-panel .jstree-default .jstree-clicked:hover { color: var(--ide-files-selected); font-weight: 600; }",
    S + ".ide-shell .files-panel .jstree-default .jstree-wholerow-clicked { box-shadow: inset 2px 0 0 var(--accent-color); }",
    S + ".ide-shell .files-panel .jstree-default .jstree-icon, " + S + ".ide-shell .files-panel .ide-btn--icon { color: var(--ide-files-icon); }",
    S + ".ide-shell .files-panel__title { color: var(--ide-files-title); }",
    S + ".ide-shell .files-empty { color: var(--ide-files-text); }",
    S + ".ide-shell .files-empty__link { color: var(--ide-files-selected); }",
    // the language and theme menus, and the palette: text and swatch edges written out for a dark menu
    S + ".sm-menu { color: var(--ide-text); }",
    S + ".sm-item { color: var(--ide-text); }",
    S + ".sm-item.is-current { color: var(--paper); }",
    S + ".sm-swatch { border-color: var(--ink-500); }",
    S + ".palette__item[aria-selected=\"true\"] { background: var(--ink-600); }",
    S + ".ide-shell { box-shadow: none; }",
    // Genie pinned beside the IDE
    S + "#chat-widget__container.is-docked {\n" + [
      ["--cw-bg", g.bg], ["--cw-border", g.border], ["--cw-text", g.text], ["--cw-strong", g.strong], ["--cw-muted", g.muted],
      ["--cw-icon", g.icon], ["--cw-field-bg", g.fieldBg], ["--cw-field-border", g.fieldBorder], ["--cw-code-bg", g.code],
      ["--cw-code-text", g.codeText], ["--cw-secondary-bg", g.secondary], ["--cw-user-bg", g.userBg], ["--cw-user-text", g.userText],
      ["--cw-accent", p.accent], ["--cw-on-accent", p.onAccent], ["--cw-link", g.link], ["--cw-chip-bg", g.chip],
      ["--cw-chip-text", g.chipText], ["--cw-pop-bg", g.popBg], ["--cw-pop-border", g.popBorder], ["--cw-seg-bg", g.segBg],
      ["--cw-seg-on", g.segOn]
    ].map((kv) => "  " + kv[0] + ": " + kv[1] + ";").join("\n") + "\n}"
  ].join("\n");
}
