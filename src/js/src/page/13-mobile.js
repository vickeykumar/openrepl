// Phone layout (T10): one pane at a time and the extra-keys row.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.setMobileView = setMobileView;
window.applyMobileLayout = applyMobileLayout;
window.sendToActiveTerminal = sendToActiveTerminal;

// ---------------------------------------------------------------------------
// Phone layout (T10): one pane at a time (Editor | Terminal), files in a
// drawer, an extra-keys row for the terminal and bigger touch targets.
// ---------------------------------------------------------------------------
window.MOBILE_QUERY = window.matchMedia ? window.matchMedia("(max-width: 800px)") : null;
window.mobileActive = false;

function setMobileView(view) {
  var split = document.getElementById("ide-split");
  if (!split) return;
  split.setAttribute("data-mobile-view", view);
  $(".mobile-view").each(function () {
    this.setAttribute("aria-selected", this.getAttribute("data-view") === view ? "true" : "false");
  });
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 30);
}

function applyMobileLayout() {
  var mobile = !!(MOBILE_QUERY && MOBILE_QUERY.matches);
  if (mobile === mobileActive) return;
  mobileActive = mobile;
  document.body.classList.toggle("is-mobile", mobile);
  var ide = document.getElementById("ide");
  if (mobile) {
    // a single pane at a time: no splitter
    if (einst) { einst.destroy(); einst = null; }
    if (ide) ide.style.display = "flex";
    document.body.classList.remove("editor-hidden");
    setMobileView("terminal");
    setFilesPanel(false);
  } else {
    var split = document.getElementById("ide-split");
    if (split) split.removeAttribute("data-mobile-view");
    if (einst === null) {
      direction = "horizontal";
      ToggleEditor();
    }
  }
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 60);
}

function sendToActiveTerminal(data) {
  var tab = document.querySelector("#terminal-tabs .tab.active");
  var gt = tab && tab.gottyterm;
  var term = gt && gt.term;   // the Xterm wrapper in gotty-bundle (xterm.ts)
  if (term && typeof term.typeInput === "function") {
    term.typeInput(data);
    if (typeof term.focus === "function") term.focus();
  }
}

window.EXTRA_KEYS = {
  "tab": "\t", "esc": "\x1b", "ctrl-c": "\x03",
  "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D"
};

$(function () {
  $(".mobile-view").on("click", function () { setMobileView(this.getAttribute("data-view")); });
  $("#extra-keys").on("click", "button[data-key]", function (e) {
    e.preventDefault();
    var data = EXTRA_KEYS[this.getAttribute("data-key")];
    if (data) sendToActiveTerminal(data);
  });
  // on phones, picking a file closes the drawer and shows the editor
  $("#file-browser").on("select_node.jstree", function (e, data) {
    if (mobileActive && data && data.node && data.node.type === "file") {
      setFilesPanel(false);
      setMobileView("editor");
    }
  });
  // Run from the editor on a phone: show the output
  $("#play-button, #debug-play-button").on("click", function () {
    if (mobileActive) setMobileView("terminal");
  });
  // keep the Terminal switch dot in step with the active terminal
  var termDiv = document.getElementById("terminal-div");
  if (termDiv) termDiv.addEventListener("ttystate", function () {
    setTimeout(function () {
      var dot = document.querySelector("#terminal-tabs .tab.active .tab-dot");
      var md = document.getElementById("mobile-term-dot");
      if (dot && md) md.setAttribute("data-state", dot.getAttribute("data-state") || "");
    }, 1600);
  });
  applyMobileLayout();
  if (MOBILE_QUERY) {
    if (MOBILE_QUERY.addEventListener) MOBILE_QUERY.addEventListener("change", applyMobileLayout);
    else if (MOBILE_QUERY.addListener) MOBILE_QUERY.addListener(applyMobileLayout);
  }
});

export {};  // an ES module: strict mode, bundled by webpack
