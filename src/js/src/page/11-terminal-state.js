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
window.awayWaitMs = awayWaitMs;

// ---------------------------------------------------------------------------
// Connection state (T6). gotty-bundle fires "ttystate" on each terminal
// element: connecting, connected, or closed with a kind (exited, killed,
// failed, timeout, lost, away, closed, limit). Each tab shows a dot; the active terminal
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

// ---------------------------------------------------------------------------
// Execution node away. A gateway whose worker for this session is away cannot
// start a terminal until it places the session again. It says how long that
// takes (kind "away", detail.retryIn seconds), and the page counts it down:
// Reconnect, Run and Debug stay disabled until the time is over, so pressing
// them cannot only fail again.
// ---------------------------------------------------------------------------
var AWAY_CONTROLS = "#play-button, #debug-play-button, #redo-button, #tabrefresh, #run-menu .run-menu__item, #term-banner-action";
var awayTimer = null;

function awayLeftMs(elem) {
  if (!elem || !elem.__ttyAwayUntil) return 0;
  return Math.max(0, elem.__ttyAwayUntil - Date.now());
}

// How long the active terminal still has to wait, 0 if it need not.
function awayWaitMs() {
  var elem = activeTermElem();
  return elem && elem.__ttyKind === "away" && elem.__ttyState === "closed" ? awayLeftMs(elem) : 0;
}

function clockText(ms) {
  var s = Math.ceil(ms / 1000);
  var m = Math.floor(s / 60);
  s = s % 60;
  return m + ":" + (s < 10 ? "0" : "") + s;
}

// Only the controls this disabled are enabled again, so a control that is
// disabled for another reason stays so.
function setAwayDisabled(on) {
  document.querySelectorAll(AWAY_CONTROLS).forEach(function (el) {
    if (on) {
      el.disabled = true;
      el.setAttribute("aria-disabled", "true");
      el.classList.add("is-away");
    } else if (el.classList.contains("is-away")) {
      el.disabled = false;
      el.removeAttribute("aria-disabled");
      el.classList.remove("is-away");
    }
  });
}

function awayBannerBody(left) {
  return left > 0
    ? "Your files are safe. Reconnect in " + clockText(left) + "."
    : "Your files are safe. You can reconnect now.";
}

function tickAway() {
  var left = awayWaitMs();
  setAwayDisabled(left > 0);
  var b = document.getElementById("term-banner");
  var elem = activeTermElem();
  if (b && !b.hidden && elem && elem.__ttyKind === "away") {
    document.getElementById("term-banner-body").textContent = " " + awayBannerBody(left);
  }
  updateTermFooter();
  if (left <= 0 && awayTimer) {
    clearInterval(awayTimer);
    awayTimer = null;
  }
}

function startAwayTimer() {
  if (!awayTimer) awayTimer = setInterval(tickAway, 250);
  tickAway();
}

function hideTermBanner() {
  var b = document.getElementById("term-banner");
  if (b && !b.hidden) {
    b.hidden = true;
    window.dispatchEvent(new Event("resize"));
  }
}

function showTermBanner(kind, compiled, notice) {
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
  } else if (kind === "away") {
    tone = "warn";
    title = "Your execution node is away.";
    body = awayBannerBody(awayWaitMs());
  } else if (kind === "notice") {
    // The site refused the terminal on purpose; the text is the admin's.
    tone = "warn";
    title = "Not available right now.";
    body = notice || "Please try again later.";
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
  if (kind === "away") startAwayTimer();
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
    var wait = k === "away" ? awayLeftMs(elem) : 0;
    t.textContent = wait > 0 ? "Node away · reconnect in " + clockText(wait)
      : k === "exited" ? "Finished" : k === "killed" ? "Stopped" : k === "failed" ? "Could not start" : k === "notice" ? "Not available" : "Disconnected";
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
    if (d.state !== "closed") elem.__ttyAwayUntil = 0;
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
      elem.__ttyNotice = d.notice || "";
      elem.__ttyAwayUntil = d.kind === "away" ? Date.now() + (d.retryIn || 0) * 1000 : 0;
      // The controls are disabled at once, not when the banner appears.
      if (d.kind === "away") startAwayTimer();
      // A reconnect closes the old connection first; wait to see whether a new one starts.
      setTimeout(function () {
        if (!document.contains(elem) || elem.__ttyPending || elem.__ttyState !== "closed") return;
        setTabDot(elem, d.kind === "exited" ? "exited" : "closed");
        if (elem === activeTermElem() && d.kind !== "closed") showTermBanner(d.kind, d.compiled, d.notice);
        updateTermFooter();
      }, 1500);
    }
    tickAway();
  });
  // switching tabs: show the state of the newly active terminal
  $("#terminal-tabs").on("click", ".tab", function () {
    setTimeout(function () {
      var elem = activeTermElem();
      hideTermBanner();
      if (elem && elem.__ttyState === "closed" && elem.__ttyKind && elem.__ttyKind !== "closed") {
        showTermBanner(elem.__ttyKind, elem.__ttyCompiled, elem.__ttyNotice);
      }
      tickAway();
    }, 150);
  });
  setInterval(updateTermFooter, 30000);
});

export {};  // an ES module: strict mode, bundled by webpack
