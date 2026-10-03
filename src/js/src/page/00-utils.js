// Small helpers, storage keys, the OpenREPL Dark editor theme, on-demand script loading (T16) and the web-font re-measure.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.loadScriptOnce = loadScriptOnce;
window.loadStyleOnce = loadStyleOnce;

// utilities
// Notes: usage "string"==["string"] (loose) vs "string"===["string"] (strict)
window.get = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelector(selector);
};

window.getAll = function (selector, scope) {
  scope = scope ? scope : document;
  return scope.querySelectorAll(selector);
};

window.CONTENT_KEY = "editorContent";
window.CMD_KEY = "command";
window.MAX_FILESIZE = 20 * 1024 * 1024;
window.HOME_DIR_KEY = "homedir";
// "OpenREPL Dark" editor theme: the colours of the IDE mockup (near-black
// background, coral keywords, blue functions, green strings, amber numbers).
// It is the default; every other theme stays in the status-bar picker.
(function defineOpenreplAceTheme() {
  if (!window.ace || typeof ace.define !== "function") return;
  ace.define("ace/theme/openrepl_dark", ["require", "exports", "module", "ace/lib/dom"], function (require, exports) {
    exports.isDark = true;
    exports.cssClass = "ace-openrepl-dark";
    exports.cssText = [
      ".ace-openrepl-dark { background-color: #0D0D12; color: #ECEAE6; }",
      ".ace-openrepl-dark .ace_gutter { background: #0D0D12; color: #807E8C; }",
      ".ace-openrepl-dark .ace_gutter-active-line { background-color: #17171F; color: #C9C7D1; }",
      ".ace-openrepl-dark .ace_print-margin { width: 1px; background: #1B1B24; }",
      ".ace-openrepl-dark .ace_cursor { color: #F2F0EC; }",
      ".ace-openrepl-dark .ace_marker-layer .ace_selection { background: #2E2E3A; }",
      ".ace-openrepl-dark.ace_multiselect .ace_selection.ace_start { box-shadow: 0 0 3px 0 #0D0D12; }",
      ".ace-openrepl-dark .ace_marker-layer .ace_step { background: #5C4A12; }",
      ".ace-openrepl-dark .ace_marker-layer .ace_bracket { margin: -1px 0 0 -1px; border: 1px solid #4A4855; }",
      ".ace-openrepl-dark .ace_marker-layer .ace_active-line { background: #17171F; }",
      ".ace-openrepl-dark .ace_marker-layer .ace_selected-word { border: 1px solid #34343F; }",
      ".ace-openrepl-dark .ace_invisible { color: #2E2E3A; }",
      ".ace-openrepl-dark .ace_indent-guide { background: linear-gradient(to right, transparent calc(100% - 1px), #22222C calc(100% - 1px)); }",
      ".ace-openrepl-dark .ace_fold { background-color: #8CC8FF; border-color: #ECEAE6; }",
      ".ace-openrepl-dark .ace_keyword, .ace-openrepl-dark .ace_meta, .ace-openrepl-dark .ace_storage, .ace-openrepl-dark .ace_storage.ace_type, .ace-openrepl-dark .ace_support.ace_type, .ace-openrepl-dark .ace_entity.ace_name.ace_tag { color: #FF9D8A; }",
      ".ace-openrepl-dark .ace_keyword.ace_operator, .ace-openrepl-dark .ace_punctuation, .ace-openrepl-dark .ace_paren { color: #C9C7D1; }",
      ".ace-openrepl-dark .ace_constant.ace_numeric, .ace-openrepl-dark .ace_constant.ace_character, .ace-openrepl-dark .ace_constant.ace_language, .ace-openrepl-dark .ace_support.ace_constant, .ace-openrepl-dark .ace_keyword.ace_other.ace_unit, .ace-openrepl-dark .ace_string.ace_regexp { color: #F5C37A; }",
      ".ace-openrepl-dark .ace_entity.ace_name.ace_function, .ace-openrepl-dark .ace_support.ace_function, .ace-openrepl-dark .ace_entity.ace_other.ace_attribute-name, .ace-openrepl-dark .ace_variable.ace_language { color: #8CC8FF; }",
      ".ace-openrepl-dark .ace_support.ace_class, .ace-openrepl-dark .ace_entity.ace_name.ace_type, .ace-openrepl-dark .ace_entity.ace_other.ace_inherited-class { color: #F5C37A; }",
      ".ace-openrepl-dark .ace_string, .ace-openrepl-dark .ace_markup.ace_heading, .ace-openrepl-dark .ace_heading { color: #B5E08F; }",
      ".ace-openrepl-dark .ace_comment { color: #8A8896; }",
      ".ace-openrepl-dark .ace_variable, .ace-openrepl-dark .ace_identifier { color: #ECEAE6; }",
      ".ace-openrepl-dark .ace_invalid { color: #F2F0EC; background-color: #B42323; }",
      ".ace-openrepl-dark .ace_invalid.ace_deprecated { color: #F2F0EC; background-color: #5B5866; }"
    ].join("\n");
    var dom = require("../lib/dom");
    dom.importCssString(exports.cssText, exports.cssClass, false);
  });
})();

// Loads a script or stylesheet once, the first time a feature needs it (T16),
// so sign-in (FirebaseUI), the tour (intro.js) and reCAPTCHA stay off the
// critical path. Returns a promise.
window.__loadedAssets = {};
function loadScriptOnce(src) {
  if (!__loadedAssets[src]) {
    __loadedAssets[src] = new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = src;
      s.async = true;
      s.onload = function () { resolve(); };
      s.onerror = function () { delete __loadedAssets[src]; reject(new Error("Could not load " + src)); };
      document.head.appendChild(s);
    });
  }
  return __loadedAssets[src];
}
function loadStyleOnce(href) {
  if (!__loadedAssets[href]) {
    __loadedAssets[href] = new Promise(function (resolve) {
      var l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = href;
      l.onload = function () { resolve(); };
      l.onerror = function () { resolve(); }; // a missing style should not block the feature
      document.head.appendChild(l);
    });
  }
  return __loadedAssets[href];
}
window.INTROJS_JS = "https://cdnjs.cloudflare.com/ajax/libs/intro.js/6.0.0/intro.min.js";
window.INTROJS_CSS = "https://cdnjs.cloudflare.com/ajax/libs/intro.js/6.0.0/introjs.min.css";
window.FIREBASEUI_JS = "https://www.gstatic.com/firebasejs/ui/6.0.2/firebase-ui-auth.js";
window.FIREBASEUI_CSS = "https://www.gstatic.com/firebasejs/ui/6.0.2/firebase-ui-auth.css";
window.RECAPTCHA_JS = "https://www.google.com/recaptcha/api.js";

// Same stack as --font-code in css/scribbler-global.css.
window.CODE_FONT_STACK = "'JetBrains Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace";

// Terminals size themselves from the width of one character. Once the web
// fonts arrive, fire a resize so every terminal and the editor re-measure.
if (document.fonts && document.fonts.load) {
  Promise.all([
    document.fonts.load("14px 'JetBrains Mono'"),
    document.fonts.load("600 14px 'JetBrains Mono'")
  ]).then(function () {
    window.dispatchEvent(new Event('resize'));
  }).catch(function () {});
}

export {};  // an ES module: strict mode, bundled by webpack
