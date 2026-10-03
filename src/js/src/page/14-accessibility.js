// Accessibility (T15): terminal input names, keyboard access to tabs, and Esc then Tab out of the editor and terminal.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.syncTerminalTabsA11y = syncTerminalTabsA11y;
window.focusableElements = focusableElements;
window.codeAreaOf = codeAreaOf;
window.moveFocusOutOf = moveFocusOutOf;
window.describeCodeInputs = describeCodeInputs;

// ---------------------------------------------------------------------------
// Accessibility (T15): names for the terminals' hidden inputs, and keyboard
// access to the terminal tabs. The strip also holds the + and Reconnect
// buttons, so each tab is a pressed/unpressed button rather than an ARIA tab:
// arrows move between tabs, Enter or Space opens one, Delete closes an extra tab.
// ---------------------------------------------------------------------------
function syncTerminalTabsA11y() {
  var list = document.getElementById("terminal-tabs");
  if (!list) return;
  var tabs = list.querySelectorAll(".tab");
  for (var i = 0; i < tabs.length; i++) {
    var tab = tabs[i];
    var active = tab.classList.contains("active");
    tab.setAttribute("role", "button");
    tab.setAttribute("aria-pressed", active ? "true" : "false");
    tab.setAttribute("tabindex", active ? "0" : "-1");
    var term = tab.gottyterm && tab.gottyterm.elem;
    if (term && term.id) {
      tab.setAttribute("aria-controls", term.id);
    }
    var title = tab.querySelector(".tab-title");
    var close = tab.querySelector(".close-tab");
    if (close) {
      close.setAttribute("aria-label", "Close " + (title ? title.textContent : "terminal"));
      close.setAttribute("tabindex", "-1");
    }
  }
  var inputs = document.querySelectorAll("#terminal-div .xterm-helper-textarea:not([aria-label])");
  for (var j = 0; j < inputs.length; j++) inputs[j].setAttribute("aria-label", "Terminal input");
}

$(function () {
  var list = document.getElementById("terminal-tabs");
  if (!list) return;
  syncTerminalTabsA11y();
  if (window.MutationObserver) {
    new MutationObserver(function () { syncTerminalTabsA11y(); })
      .observe(document.getElementById("terminal-div") || list, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  }
  list.addEventListener("keydown", function (e) {
    var tab = e.target.closest ? e.target.closest(".tab") : null;
    if (!tab) return;
    var tabs = Array.prototype.slice.call(list.querySelectorAll(".tab"));
    var i = tabs.indexOf(tab);
    var next = null;
    if (e.key === "ArrowRight") next = tabs[(i + 1) % tabs.length];
    else if (e.key === "ArrowLeft") next = tabs[(i - 1 + tabs.length) % tabs.length];
    else if (e.key === "Home") next = tabs[0];
    else if (e.key === "End") next = tabs[tabs.length - 1];
    else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); tab.click(); return; }
    else if (e.key === "Delete") {
      var close = tab.querySelector(".close-tab");
      if (close) { e.preventDefault(); close.click(); setTimeout(function () { var a = list.querySelector(".tab.active"); if (a) a.focus(); }, 250); }
      return;
    }
    if (next) { e.preventDefault(); next.click(); next.focus(); }
  });
});

// Leaving the editor or a terminal from the keyboard (T15). Both keep Tab for
// indenting and completion, so, as in other code editors, Esc then Tab (or
// Shift+Tab) within two seconds moves focus to the next (or previous) control
// on the page. Runs in the capture phase so Ace and xterm never see that Tab.
window.ESC_TAB_WINDOW_MS = 2000;
window.escPressedAt = 0;
function focusableElements() {
  var sel = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
  return Array.prototype.filter.call(document.querySelectorAll(sel), function (el) {
    if (el.closest(".ace_editor, .xterm")) return false;   // their own hidden inputs
    return el.offsetParent !== null || el === document.activeElement;
  });
}
function codeAreaOf(el) {
  return el && el.closest ? el.closest("#ide .editor-body, #terminal-div .terminal") : null;
}
function moveFocusOutOf(area, backwards) {
  var all = focusableElements();
  var pick = null;
  for (var i = 0; i < all.length; i++) {
    var rel = area.compareDocumentPosition(all[i]);
    if (backwards && (rel & Node.DOCUMENT_POSITION_PRECEDING)) pick = all[i];
    if (!backwards && (rel & Node.DOCUMENT_POSITION_FOLLOWING) && !(rel & Node.DOCUMENT_POSITION_CONTAINED_BY)) { pick = all[i]; break; }
  }
  if (pick) pick.focus();
}
document.addEventListener("keydown", function (e) {
  var area = codeAreaOf(e.target);
  if (!area) return;
  if (e.key === "Escape") { escPressedAt = Date.now(); return; }
  if (e.key === "Shift" || e.key === "Control" || e.key === "Alt" || e.key === "Meta") return; // Shift+Tab starts with Shift
  if (e.key === "Tab" && Date.now() - escPressedAt < ESC_TAB_WINDOW_MS) {
    e.preventDefault();
    e.stopPropagation();
    escPressedAt = 0;
    moveFocusOutOf(area, e.shiftKey);
    return;
  }
  escPressedAt = 0;
}, true);
// screen readers announce the way out
function describeCodeInputs() {
  var inputs = document.querySelectorAll(".ace_text-input, #terminal-div .xterm-helper-textarea");
  for (var i = 0; i < inputs.length; i++) inputs[i].setAttribute("aria-describedby", "kbd-escape-hint");
}
$(function () {
  describeCodeInputs();
  if (window.MutationObserver && document.getElementById("terminal-div")) {
    new MutationObserver(describeCodeInputs).observe(document.getElementById("terminal-div"), { childList: true, subtree: true });
  }
  setTimeout(describeCodeInputs, 3000);
});

export {};  // an ES module: strict mode, bundled by webpack
