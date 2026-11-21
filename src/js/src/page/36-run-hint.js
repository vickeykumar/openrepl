// A hint in the terminal of a first visit: "Press Run to see output", in the strip of the terminal tabs. It goes away for
// good when Run is pressed (the button or Ctrl/Cmd+Enter) or when something is typed in the terminal, and is remembered
// on this device. Not on a phone
// (the terminal is another tab there), not on the practice page, and not for a viewer of a shared session.
// It only draws a note over the corner of the terminal: it takes no input and changes nothing in the page.

(function () {
  var KEY = "openrepl-run-hint";
  var note = null;

  function seen() {
    try {
      return localStorage.getItem(KEY) === "1";
    } catch (e) {
      return false;
    }
  }

  function remember() {
    try {
      localStorage.setItem(KEY, "1");
    } catch (e) {
      // not remembered: it shows again on the next visit
    }
  }

  function hide(forever) {
    if (note) {
      note.remove();
      note = null;
    }
    if (forever) remember();
  }

  function show() {
    if (note || seen()) return;
    if (document.documentElement.classList.contains("is-practice")) return;
    if (window.matchMedia && window.matchMedia("(max-width: 800px)").matches) return;
    if (typeof window.isMaster === "function" && !window.isMaster()) return;
    var terminal = document.getElementById("terminal-div");
    if (!terminal) return;
    if (getComputedStyle(terminal).position === "static") terminal.style.position = "relative";
    note = document.createElement("div");
    note.className = "run-hint";
    note.setAttribute("role", "status");
    note.appendChild(document.createTextNode("Press "));
    var key = document.createElement("kbd");
    key.textContent = "Run";
    note.appendChild(key);
    note.appendChild(document.createTextNode(" to see output"));
    terminal.appendChild(note);
  }

  // typing in the terminal: the visitor found their way
  document.addEventListener(
    "keydown",
    function (e) {
      var t = e.target;
      if (t && t.closest && t.closest("#terminal-div")) hide(true);
    },
    true
  );

  // Run: its button (and the arrow beside it), or the shortcut
  document.addEventListener(
    "click",
    function (e) {
      var t = e.target;
      if (t && t.closest && t.closest(".run-split")) hide(true);
    },
    true
  );
  document.addEventListener(
    "keydown",
    function (e) {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) hide(true);
    },
    true
  );
  window.addEventListener("load", function () {
    setTimeout(show, 1200);
  });
})();
