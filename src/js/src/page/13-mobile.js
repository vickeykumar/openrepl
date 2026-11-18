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
  // Genie is the third pane (the panel lives in the dock, which the css shows in place of the split); the split keeps the
  // pane that was shown before, so that leaving Genie goes back to it
  var genie = view === "genie";
  document.body.classList.toggle("mobile-genie", genie);
  if (genie) {
    if (window.ChatWidget && window.ChatWidget.open) window.ChatWidget.open({});
  } else {
    split.setAttribute("data-mobile-view", view);
  }
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
    document.body.classList.remove("mobile-genie");
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

// ---------------------------------------------------------------------------
// Phone extras: the menu behind "more" (Fork, Share, Apps, commands), the Genie tab following the panel, the visible height
// (the keyboard takes some of it) and the back button leaving the maximized workspace. All of it acts only while mobileActive.
// ---------------------------------------------------------------------------
function closeMobileMore() {
  var menu = document.getElementById("mobile-more-menu");
  var btn = document.getElementById("mobile-more");
  if (menu) menu.hidden = true;
  if (btn) btn.setAttribute("aria-expanded", "false");
}

function wireMobileExtras() {
  var btn = document.getElementById("mobile-more");
  var menu = document.getElementById("mobile-more-menu");
  if (btn && menu) {
    btn.addEventListener("click", function (e) {
      e.stopPropagation();
      var open = menu.hidden;
      menu.hidden = !open;
      btn.setAttribute("aria-expanded", open ? "true" : "false");
      if (open) { var first = menu.querySelector("button"); if (first) first.focus(); }
    });
    menu.addEventListener("click", function (e) {
      var item = e.target.closest && e.target.closest("[data-more]");
      if (!item) return;
      var what = item.getAttribute("data-more");
      closeMobileMore();
      // the buttons are in the page (hidden on a phone): the menu presses them
      var target = what === "fork" ? document.querySelector("#fork-widget .forkbtn")
        : what === "share" ? document.getElementById("share-btn-1")
        : what === "apps" ? document.getElementById("appstore-button")
        : document.getElementById("palette-button");
      // after this click has gone through the page: the share popover closes on any click that is not on it, and this one is not
      if (target) setTimeout(function () { target.click(); }, 0);
    });
    document.addEventListener("click", function (e) {
      if (!menu.hidden && !(e.target.closest && e.target.closest("#mobile-more-menu"))) closeMobileMore();
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !menu.hidden) { closeMobileMore(); btn.focus(); }
    });
  }
  // the Genie tab shows what the lamp of the Ask Genie button shows, and follows the panel: closed, the tab goes back to the editor
  var tab = document.getElementById("mobile-view-genie");
  var bar = document.getElementById("genie-button");
  if (window.MutationObserver) {
    if (tab && bar) {
      new MutationObserver(function () {
        var st = bar.getAttribute("data-genie-state");
        if (st) tab.setAttribute("data-genie-state", st); else tab.removeAttribute("data-genie-state");
      }).observe(bar, { attributes: true, attributeFilter: ["data-genie-state"] });
    }
    new MutationObserver(function () {
      if (mobileActive && document.body.classList.contains("mobile-genie") && !document.body.classList.contains("genie-open")) setMobileView("editor");
    }).observe(document.body, { attributes: true, attributeFilter: ["class"] });
  }
  // the visible height of the page: what is left when the on-screen keyboard is up (css: --vvh)
  // (a phone that resizes the layout for the keyboard, interactive-widget=resizes-content, shrinks innerHeight too: the full
  // height is then the largest one seen while no keyboard was up)
  var vv = window.visualViewport;
  if (vv) {
    var fullH = Math.max(window.innerHeight, Math.round(vv.height));
    var setH = function () {
      var root = document.documentElement;
      if (!mobileActive) {
        root.style.removeProperty("--vvh");
        root.style.removeProperty("--vvt");
        root.classList.remove("keyboard-open");
        return;
      }
      var h = Math.round(vv.height);
      var kb = !!document.activeElement && /^(TEXTAREA|INPUT)$/.test(document.activeElement.tagName) && fullH - h > 120;
      if (!kb && !root.classList.contains("keyboard-open")) fullH = Math.max(fullH, h); // no keyboard: what is seen is the full height
      root.style.setProperty("--vvh", h + "px");
      root.style.setProperty("--vvt", Math.round(vv.offsetTop) + "px");
      root.classList.toggle("keyboard-open", kb);
      if (kb) window.dispatchEvent(new Event("resize")); // the terminal and the editor fit what is left
    };
    vv.addEventListener("resize", setH);
    vv.addEventListener("scroll", setH);
    window.addEventListener("resize", setH);
    document.addEventListener("focusin", function () { setTimeout(setH, 50); });
    document.addEventListener("focusout", function () { setTimeout(setH, 50); });
    // turning the phone changes the full height; only a change of the width counts as that (a keyboard changes the height alone)
    var lastW = window.innerWidth;
    window.addEventListener("orientationchange", function () {
      setTimeout(function () {
        if (Math.abs(window.innerWidth - lastW) < 40) return;
        lastW = window.innerWidth;
        fullH = Math.max(window.innerHeight, Math.round(vv.height));
        setH();
      }, 300);
    });
    setH();
  }
  // the back button of the phone leaves the maximized workspace (ToggleFunction adds one history entry when it maximizes)
  window.addEventListener("popstate", function () {
    if (mobileActive && document.body.classList.contains("ide-maximized")) ToggleFunction();
  });
}

$(function () {
  wireMobileExtras();
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
