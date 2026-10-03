// Connection state (T6): tab dots, the terminal footer and the stop banner.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.termTab = termTab;
window.setTabDot = setTabDot;
window.activeTermElem = activeTermElem;
window.hideTermBanner = hideTermBanner;
window.showTermBanner = showTermBanner;
window.updateTermFooter = updateTermFooter;

// ---------------------------------------------------------------------------
// Connection state (T6). gotty-bundle fires "ttystate" on each terminal
// element: connecting, connected, or closed with a kind (exited, killed,
// failed, timeout, lost, closed, limit). Each tab shows a dot; the active terminal
// gets a footer with the session time left and a banner when it stops.
// ---------------------------------------------------------------------------
window.SESSION_MINUTES = 60;

function termTab(elem) {
  return elem && elem.tab ? elem.tab : null;
}

function setTabDot(elem, state) {
  var tab = termTab(elem);
  if (!tab) return;
  var dot = tab.querySelector(".tab-dot");
  if (!dot) {
    dot = document.createElement("span");
    dot.className = "tab-dot";
    dot.setAttribute("aria-hidden", "true");
    tab.insertBefore(dot, tab.firstChild);
  }
  dot.setAttribute("data-state", state);
  tab.setAttribute("data-state", state);
}

function activeTermElem() {
  return document.querySelector("#terminal-div .terminal.active");
}

function hideTermBanner() {
  var b = document.getElementById("term-banner");
  if (b && !b.hidden) {
    b.hidden = true;
    window.dispatchEvent(new Event("resize"));
  }
}

function showTermBanner(kind, compiled) {
  var b = document.getElementById("term-banner");
  if (!b) return;
  var title = "", body = "", action = "Reconnect", tone = "danger", run = ToggleReconnect;
  if (kind === "killed" && compiled) {
    title = "Stopped: the program was killed.";
    body = "This usually means it went over its memory limit.";
    action = "Run again";
    run = CompileandRun;
  } else if (kind === "killed") {
    title = "The REPL stopped.";
    body = "It was killed, most likely by its memory limit. Restart it to keep going.";
    action = "Restart REPL";
  } else if (kind === "timeout") {
    tone = "warn";
    title = "This session reached its " + SESSION_MINUTES + "-minute limit.";
    body = "Your files are kept. Start a new session to keep going.";
    action = "Start new session";
  } else if (kind === "exited") {
    if (compiled) return;        // a finished Run is normal, nothing to report
    tone = "neutral";
    title = "The REPL exited.";
    body = "";
    action = "Restart REPL";
  } else if (kind === "failed") {
    title = "The REPL could not start.";
    body = "The server could not start this language just now. Try again, or pick another language.";
    action = "Try again";
  } else if (kind === "limit") {
    tone = "warn";
    title = "Too many open terminals.";
    body = "Close a terminal tab, then reconnect.";
  } else {
    title = "Connection lost.";
    body = "Your files are safe. Reconnect to start a fresh REPL in the same workspace.";
  }
  b.setAttribute("data-tone", tone);
  document.getElementById("term-banner-title").textContent = title;
  document.getElementById("term-banner-body").textContent = body ? " " + body : "";
  var btn = document.getElementById("term-banner-action");
  btn.textContent = action;
  btn.onclick = function () { hideTermBanner(); run(); };
  if (b.hidden) {
    b.hidden = false;
    window.dispatchEvent(new Event("resize"));
  }
}

function updateTermFooter() {
  var f = document.getElementById("term-footer");
  var t = document.getElementById("term-footer-text");
  var elem = activeTermElem();
  if (!f || !t || !elem) return;
  var st = elem.__ttyState || "connecting";
  f.setAttribute("data-state", st);
  if (st === "connected" && elem.__ttyStartedAt) {
    var left = SESSION_MINUTES - Math.floor((Date.now() - elem.__ttyStartedAt) / 60000);
    left = Math.max(0, left);
    f.setAttribute("data-warn", left <= 5 ? "true" : "false");
    t.textContent = "Connected · session ends in " + left + " min";
  } else if (st === "connected") {
    t.textContent = "Connected";
  } else if (st === "closed") {
    f.setAttribute("data-warn", "false");
    var k = elem.__ttyKind;
    t.textContent = k === "exited" ? "Finished" : k === "killed" ? "Stopped" : k === "failed" ? "Could not start" : "Disconnected";
  } else {
    t.textContent = "Connecting…";
  }
}

$(function () {
  var termDiv = document.getElementById("terminal-div");
  if (!termDiv) return;
  termDiv.addEventListener("ttystate", function (e) {
    var elem = e.target.closest ? e.target.closest(".terminal") : e.target;
    var d = e.detail || {};
    if (!elem) return;
    elem.__ttyState = d.state;
    if (d.state === "connecting") {
      elem.__ttyPending = true;
      setTabDot(elem, "connecting");
      if (elem === activeTermElem()) hideTermBanner();
    } else if (d.state === "connected") {
      elem.__ttyPending = false;
      elem.__ttyStartedAt = Date.now();
      setTabDot(elem, "connected");
      if (elem === activeTermElem()) hideTermBanner();
    } else if (d.state === "closed") {
      elem.__ttyPending = false;
      elem.__ttyKind = d.kind;
      elem.__ttyCompiled = !!d.compiled;
      // A reconnect closes the old connection first; wait to see whether a new one starts.
      setTimeout(function () {
        if (!document.contains(elem) || elem.__ttyPending || elem.__ttyState !== "closed") return;
        setTabDot(elem, d.kind === "exited" ? "exited" : "closed");
        if (elem === activeTermElem() && d.kind !== "closed") showTermBanner(d.kind, d.compiled);
        updateTermFooter();
      }, 1500);
    }
    updateTermFooter();
  });
  // switching tabs: show the state of the newly active terminal
  $("#terminal-tabs").on("click", ".tab", function () {
    setTimeout(function () {
      var elem = activeTermElem();
      hideTermBanner();
      if (elem && elem.__ttyState === "closed" && elem.__ttyKind && elem.__ttyKind !== "closed") {
        showTermBanner(elem.__ttyKind, elem.__ttyCompiled);
      }
      updateTermFooter();
    }, 150);
  });
  setInterval(updateTermFooter, 30000);
});

export {};  // an ES module: strict mode, bundled by webpack
