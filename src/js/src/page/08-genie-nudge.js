// Genie nudge (T3): a note next to the Genie button after the first error in a terminal.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.setupGenieErrorNudge = setupGenieErrorNudge;
window.genieAnchor = genieAnchor;
window.showGenieNudge = showGenieNudge;

// ---------------------------------------------------------------------------
// Genie nudge (T3): Genie no longer opens on page load. The first time a
// terminal prints something that looks like an error, a small note appears
// next to the Genie button. "Ask Genie" opens the chat with that error filled
// in. The note never takes focus, so typing in the REPL is not interrupted.
// ---------------------------------------------------------------------------
window.GENIE_ERROR_RE = /(Traceback \(most recent call last\)|\b\w*(Error|Exception)\b:|\berror(\[\w+\])?:|\bpanic:|Segmentation fault|undefined reference|command not found|No such file or directory)/;

function setupGenieErrorNudge() {
  var termDiv = document.getElementById("terminal-div");
  if (!termDiv || !window.MutationObserver) return;
  var shown = false, pending = null, startedAt = Date.now();

  function lastErrorLine() {
    // read the active terminal's buffer; older builds only had the DOM rows
    var tab = document.querySelector("#terminal-tabs .tab.active");
    var term = tab && tab.gottyterm && tab.gottyterm.term;
    var text = term && typeof term.recentText === "function" ? term.recentText(60) : null;
    if (text === null) {
      var rows = termDiv.querySelector(".terminal.active .xterm-rows") || termDiv.querySelector(".xterm-rows");
      if (!rows) return null;
      text = rows.innerText;
    }
    var lines = text.split("\n");
    for (var i = lines.length - 1; i >= 0; i--) {
      var line = lines[i].replace(/ /g, " ").trim();
      if (line && GENIE_ERROR_RE.test(line)) return line.slice(0, 300);
    }
    return null;
  }

  function scan() {
    pending = null;
    if (shown || Date.now() - startedAt < 3000) return;
    var line = lastErrorLine();
    if (!line) return;
    shown = true;
    observer.disconnect();
    showGenieNudge(line);
  }

  var observer = new MutationObserver(function () {
    if (!pending) pending = setTimeout(scan, 1200);
  });
  observer.observe(termDiv, { childList: true, subtree: true, characterData: true });
}

// Genie opens next to the floating button, or next to the app bar button
// when the floating one is hidden (phones).
function genieAnchor() {
  var floating = document.querySelector("[data-chat-widget-button]");
  if (floating && floating.offsetParent !== null) return floating;
  return document.getElementById("genie-button") || floating;
}

function showGenieNudge(errorLine) {
  if (document.getElementById("genie-nudge")) return;
  var nudge = document.createElement("div");
  nudge.id = "genie-nudge";
  nudge.className = "genie-nudge";
  nudge.setAttribute("role", "status");
  nudge.innerHTML =
    '<p class="genie-nudge__text">Got an error? Genie can explain it.</p>' +
    '<div class="genie-nudge__actions">' +
    '<button type="button" class="genie-nudge__ask">Ask Genie</button>' +
    '<button type="button" class="genie-nudge__close" aria-label="Dismiss">&times;</button>' +
    '</div>';
  document.body.appendChild(nudge);

  var hideTimer = setTimeout(hide, 15000);
  function hide() {
    clearTimeout(hideTimer);
    nudge.classList.add("genie-nudge--hide");
    setTimeout(function () { nudge.remove(); }, 300);
  }
  nudge.querySelector(".genie-nudge__close").addEventListener("click", hide);
  nudge.querySelector(".genie-nudge__ask").addEventListener("click", function () {
    hide();
    var btn = genieAnchor();
    if (window.ChatWidget && btn) {
      window.ChatWidget.open({ target: btn });
      setTimeout(function () {
        var input = document.getElementById("chat-widget__input");
        if (input) {
          input.value = "Why am I getting this error?\n" + errorLine;
          input.focus();
        }
      }, 100);
    }
  });
}

export {};  // an ES module: strict mode, bundled by webpack
