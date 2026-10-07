// Workspace card (T7) and the landing sections (T8).
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.refreshWorkspaceUsage = refreshWorkspaceUsage;
window.syncUsageHeading = syncUsageHeading;
window.focusContactForm = focusContactForm;

// ---------------------------------------------------------------------------
// Workspace card (T7): guests see that files are deleted after an hour and
// how much of the 50 MB they use; signed-in users see their usage.
// ---------------------------------------------------------------------------
function refreshWorkspaceUsage() {
  var card = document.getElementById("workspace-card");
  if (!card || typeof preprocessurl !== "function") return;
  $.ajax({ url: preprocessurl("/ws_filebrowser?q=usage"), method: "GET", dataType: "json" })
    .done(function (u) {
      if (!u || typeof u.usedMB !== "number") return;
      var used = u.usedMB < 0.1 ? "Less than 0.1" : u.usedMB.toFixed(1);
      var pct = Math.min(100, Math.max(1, (u.usedMB / u.limitMB) * 100));
      document.getElementById("usage-bar").style.width = pct + "%";
      document.getElementById("usage-bar").setAttribute("data-full", pct >= 90 ? "true" : "false");
      document.getElementById("usage-text").textContent = used + " MB of " + u.limitMB + " MB used";
      var mins = u.deleteAfterMinutes || 60;
      document.getElementById("guest-deadline").textContent =
        "Files are deleted " + (mins % 60 === 0 ? (mins / 60) + (mins === 60 ? " hour" : " hours") : mins + " minutes") + " after your last visit.";
      card.setAttribute("data-guest", u.guest ? "true" : "false");
      card.hidden = false;
    });
}

$(function () {
  setTimeout(refreshWorkspaceUsage, 1500);
  setInterval(refreshWorkspaceUsage, 60000);
  // after runs, uploads and file changes the size can change
  $("#terminal").on("optionrun", function () { setTimeout(refreshWorkspaceUsage, 4000); });
  $(document).ajaxComplete(function (e, xhr, opts) {
    if (opts && opts.url && opts.url.indexOf("q=usage") === -1 &&
        (opts.url.indexOf("/upload_file") !== -1 || (opts.url.indexOf("/ws_filebrowser") !== -1 && opts.type === "POST"))) {
      setTimeout(refreshWorkspaceUsage, 500);
    }
  });
});

// ---------------------------------------------------------------------------
// Landing sections (T8): language cards, usage heading, CTA, request form.
// ---------------------------------------------------------------------------
window.LANG_LABELS = {
  "c": "C", "cpp": "C++", "go": "Go", "yaegi": "Go (yaegi)", "java": "Java",
  "javascript": "JavaScript", "ts-node": "TypeScript", "jq-repl": "JSON (jq)",
  "node": "Node.js", "python": "Python", "python2.7": "Python 2.7",
  "ipython3": "IPython", "irb": "Ruby", "perli": "Perl", "bash": "Bash",
  "tclsh": "Tcl", "evcxr": "Rust", "sqlite3": "SQLite", "rappel": "Assembly x86"
};

function syncUsageHeading() {
  var sel = document.getElementById("optionlist");
  var out = document.getElementById("usage-lang");
  if (!sel || !out) return;
  var opt = sel.options[sel.selectedIndex];
  out.textContent = LANG_LABELS[sel.value] || (opt ? opt.text : sel.value);
}

// The reCAPTCHA check appears once someone starts a message.
function showRequestCaptcha() {
  var c = document.getElementById("request-captcha");
  if (c) c.hidden = false;
  loadScriptOnce(RECAPTCHA_JS).catch(function () {}); // renders the .g-recaptcha box when it loads (T16)
}

// "Contact" in the nav and the footer: bring the form at the bottom into view, put the
// cursor in it and show the reCAPTCHA check.
function focusContactForm() {
  var form = document.getElementById("feedback-form");
  if (!form) return;
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  // a phone's opened menu would stay over the page
  var menuButton = document.querySelector(".menu.responsive .toggle__button");
  if (menuButton) menuButton.click();
  form.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "center" });
  setTimeout(function () {
    var first = document.getElementById("feedback-name");
    if (first) first.focus({ preventScroll: true });
    showRequestCaptcha();
  }, reduce ? 0 : 450);
}

$(function () {
  $("[data-contact]").on("click", function (e) {
    e.preventDefault();
    focusContactForm();
    if (window.history && history.replaceState) history.replaceState(null, "", "#request");
  });
  // /#request, from the Contact link of another page or a shared link. The terminal
  // takes the focus when it connects, a moment after the page loads, so focus the
  // form again then, unless the visitor has put the cursor somewhere else by now.
  if (location.hash === "#request") {
    setTimeout(focusContactForm, 300);
    setTimeout(function () {
      var a = document.activeElement;
      if (!a || a === document.body || (a.closest && a.closest("#terminal-div"))) focusContactForm();
    }, 3000);
  }
  $("#languages").on("click", ".lang-card[data-lang]", function (e) {
    e.preventDefault();
    pickLanguage(this.getAttribute("data-lang"));
  });
  $("#final-start").on("click", function (e) {
    e.preventDefault();
    scrollToWorkspace(true);
  });
  $("#copy-docker").on("click", function () {
    var text = $("#docker-cmd").text();
    var done = function () { notify("Paste it into a terminal on a machine with Docker.", { type: "success", title: "Command copied" }); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { notify(text, { type: "info", title: "Copy this command" }); });
    } else {
      notify(text, { type: "info", title: "Copy this command" });
    }
  });
  $("#feedback-form").on("focus", "input, textarea", showRequestCaptcha);
  // The message goes to /feedback with fetch, which answers in JSON, so the result
  // shows here instead of in a hidden frame. The server limits size and how often
  // a visitor may write; a refusal is shown as it says it.
  $("#feedback-form").on("submit", function (e) {
    e.preventDefault();
    var form = this;
    var button = document.getElementById("feedback-submit");
    // the dashboard shows a name for every message
    var n = document.getElementById("feedback-name");
    if (n && !n.value.trim()) n.value = "Anonymous";
    if (button) button.disabled = true;
    var failed = function (text) {
      notify(text || "Couldn't send your message. Please try again in a moment.", { type: "error" });
      if (button) button.disabled = false;
    };
    fetch("/feedback", {
      method: "POST",
      headers: { "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(new FormData(form)).toString()
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (body) {
        if (res.ok) {
          var done = document.getElementById("feedbackDiv");
          if (done) done.innerHTML = '<p class="request-form__done">Thanks! Your message was sent.</p>';
        } else {
          failed(body.message);
        }
      });
    }, function () { failed(); });
  });
  $("#optionlist").on("change", syncUsageHeading);
  syncUsageHeading();
});

export {};  // an ES module: strict mode, bundled by webpack
