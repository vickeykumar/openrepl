// The IDE follows the editor theme: when a theme is picked in the status bar,
// the colours it paints are read from the editor and become the colours of the
// whole IDE shell (app bar, tabs, file browser, menus, status bar) and of the
// terminal. The maths is in lib-ide-theme.mjs; this part reads the page and
// puts the result on it.
//
// OpenREPL Dark, the default, keeps the page's own dark tokens, so nothing
// changes for someone who never picks a theme.
import { derivePalette, paletteCss } from "./lib-ide-theme.mjs";

var IDE_THEME_DEFAULT = "ace/theme/openrepl_dark";
var IDE_PALETTE_KEY = "ide-palette";   // localStorage: the last palette, so the next visit starts in it
var IDE_THEME_KEY = "editor-theme";    // localStorage: the theme picked in the status bar (06-editor.js)

function ideThemeStyle() {
  var el = document.getElementById("ide-theme-style");
  if (!el) {
    el = document.createElement("style");
    el.id = "ide-theme-style";
    document.head.appendChild(el);
  }
  return el;
}

function tellIdeTheme() {
  // the terminal's colours are set in script, so it is told
  try { window.dispatchEvent(new CustomEvent("idethemechange")); } catch (e) {}
}

function clearIdeTheme() {
  var el = document.getElementById("ide-theme-style");
  if (el) el.remove();
  document.documentElement.removeAttribute("data-ide-theme");
  window.__idePalette = null;
  tellIdeTheme();
}

function putIdeTheme(palette) {
  ideThemeStyle().textContent = paletteCss(palette);
  document.documentElement.setAttribute("data-ide-theme", "on");
  window.__idePalette = palette;
  tellIdeTheme();
}

// What the theme paints, read from the editor: its background and text, the
// selection, and the colours of the tokens a code line is made of. The probes
// sit inside the editor, so the theme's own selectors (".ace-monokai .ace_keyword")
// apply to them.
function readAceTheme(editor) {
  var host = editor.container;
  var cs = getComputedStyle(host);
  var layer = document.createElement("div");
  layer.className = "ace_layer ace_text-layer";
  layer.style.cssText = "position:absolute;visibility:hidden;pointer-events:none;left:0;top:0";
  var probes = {
    keyword: "ace_keyword",
    string: "ace_string",
    number: "ace_constant ace_numeric",
    fn: "ace_entity ace_name ace_function",
    type: "ace_storage ace_type",
    comment: "ace_comment"
  };
  Object.keys(probes).forEach(function (name) {
    var line = document.createElement("div");
    line.className = "ace_line";
    var span = document.createElement("span");
    span.className = probes[name];
    span.setAttribute("data-probe", name);
    span.textContent = "x";
    line.appendChild(span);
    layer.appendChild(line);
  });
  var marks = document.createElement("div");
  marks.className = "ace_marker-layer";
  var sel = document.createElement("div");
  sel.className = "ace_selection";
  sel.setAttribute("data-probe", "selection");
  marks.appendChild(sel);
  layer.appendChild(marks);
  host.appendChild(layer);
  var theme = { bg: cs.backgroundColor, fg: cs.color };
  try {
    Object.keys(probes).forEach(function (name) {
      theme[name] = getComputedStyle(layer.querySelector('[data-probe="' + name + '"]')).color;
    });
    theme.selection = getComputedStyle(sel).backgroundColor;
  } finally {
    host.removeChild(layer);
  }
  return theme;
}

// Called by 06-editor.js once an editor theme has been applied.
window.applyIdeTheme = function (editor, themeName) {
  try {
    if (!themeName || themeName === IDE_THEME_DEFAULT) {
      try { localStorage.removeItem(IDE_PALETTE_KEY); } catch (e) {}
      clearIdeTheme();
      return null;
    }
    var palette = derivePalette(readAceTheme(editor));
    putIdeTheme(palette);
    try { localStorage.setItem(IDE_PALETTE_KEY, JSON.stringify({ theme: themeName, palette: palette })); } catch (e) {}
    return palette;
  } catch (e) {
    // never leave the IDE half-themed
    console.log("IDE theme not applied: ", e);
    clearIdeTheme();
    return null;
  }
};

// The next visit starts in the last palette, before the editor has loaded its
// theme, so the page does not flash from the default to the chosen colours.
(function applyRememberedIdeTheme() {
  try {
    var picked = localStorage.getItem(IDE_THEME_KEY);
    var saved = JSON.parse(localStorage.getItem(IDE_PALETTE_KEY) || "null");
    if (picked && picked !== IDE_THEME_DEFAULT && saved && saved.theme === picked && saved.palette && saved.palette.term) {
      putIdeTheme(saved.palette);
    }
  } catch (e) { /* storage may be blocked: the theme is applied when the editor loads */ }
})();
