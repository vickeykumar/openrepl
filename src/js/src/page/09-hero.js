// Hero language chips and Start coding (T4).
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.syncLangChips = syncLangChips;
window.scrollToWorkspace = scrollToWorkspace;
window.pickLanguage = pickLanguage;

// ---------------------------------------------------------------------------
// Hero language chips and "Start coding" (T4). The chips drive the same
// #optionlist select that the workspace uses, so the terminal and editor
// switch exactly as if the language had been picked there.
// ---------------------------------------------------------------------------
function syncLangChips() {
  var sel = document.getElementById("optionlist");
  if (!sel) return;
  var chips = document.querySelectorAll("#lang-chips .lang-chip[data-lang]");
  for (var i = 0; i < chips.length; i++) {
    chips[i].setAttribute("aria-pressed", chips[i].getAttribute("data-lang") === sel.value ? "true" : "false");
  }
}

function scrollToWorkspace(focusTerminal) {
  var ws = document.getElementById("workspace");
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (ws && ws.scrollIntoView) ws.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
  if (focusTerminal) {
    setTimeout(function () {
      var t = document.querySelector("#terminal-div .terminal.active textarea");
      if (t) t.focus({ preventScroll: true });
    }, 500);
  }
}

function pickLanguage(value) {
  var sel = document.getElementById("optionlist");
  if (!sel) return;
  if (sel.value !== value) {
    sel.value = value;
    sel.dispatchEvent(new Event("change"));
  }
  syncLangChips();
  scrollToWorkspace(true);
}

$(function () {
  $("#lang-chips").on("click", ".lang-chip[data-lang]", function () {
    pickLanguage(this.getAttribute("data-lang"));
  });
  $("#lang-chip-more").on("click", function () {
    scrollToWorkspace(false);
    var sel = document.getElementById("optionlist");
    if (sel) {
      sel.focus({ preventScroll: true });
      if (sel.showPicker) { try { sel.showPicker(); } catch (e) {} }
    }
  });
  $("#start-coding").on("click", function (e) {
    e.preventDefault();
    scrollToWorkspace(true);
  });
  $("#optionlist").on("change", syncLangChips);
  syncLangChips();
});

export {};  // an ES module: strict mode, bundled by webpack
