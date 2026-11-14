// Share-code links (T13) and keyboard access to the Share popover (T14).
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.applySharedSnippet = applySharedSnippet;
window.onStarterCodeLoaded = onStarterCodeLoaded;

// ---------------------------------------------------------------------------
// Share-code links (T13). "Create code link" in the Share popover saves a
// copy of the editor (POST /snippet) and shows /s/<id>. Opening that link
// lands on ?s=<id>, and the code replaces the language's starter code.
// ---------------------------------------------------------------------------
window.sharedSnippet = null;          // {id, lang, code, applied}
window.demoLoadedForLang = null;      // the language whose starter code is in the editor

function applySharedSnippet() {
  if (!sharedSnippet || sharedSnippet.applied || !isMaster()) return;
  var lang = $("#optionlist").val();
  if (lang !== sharedSnippet.lang || demoLoadedForLang !== lang) return;
  var ed = document.getElementById("editor");
  if (!ed || !ed.env || !ed.env.editor) return;
  sharedSnippet.applied = true;
  updateEditorContent(window[CMD_KEY] || "", sharedSnippet.code, true);
  notify("It's your own copy: edit and run it freely. The link keeps the original.", { type: "info", title: "Shared code loaded" });
}

// called after a language's starter code has been put in the editor
function onStarterCodeLoaded(lang) {
  demoLoadedForLang = lang;
  applySharedSnippet();
}

// The day a new code link works until: the calendar (a date input) offers tomorrow up to today plus the most the admin allows, and
// starts on a month from today, or on the most when that is shorter. Days are counted in UTC, as the server counts them
// (server/snippets.go, snippetExpiryFor), so a day it offers is a day the server takes.
function utcDay(offsetDays) {
  var d = new Date();
  d.setUTCDate(d.getUTCDate() + offsetDays);
  return d.toISOString().slice(0, 10);
}
function codeLinkMaxDays() {
  var n = Number((window.site_settings || {}).snippetMaxDays);
  return n > 0 ? n : 30;
}
function setupCodeLinkDate() {
  var input = document.getElementById("code-link-until");
  if (!input) return;
  var max = codeLinkMaxDays();
  input.min = utcDay(1);
  input.max = utcDay(max);
  input.value = utcDay(Math.min(30, max));
  var hint = document.getElementById("code-link-until-hint");
  if (hint) hint.textContent = "Chosen from tomorrow up to " + (max === 1 ? "1 day" : max + " days") + " (the most this site allows). " + (max >= 30 ? "A month is picked for you." : "The most is picked for you.");
}
window.setupCodeLinkDate = setupCodeLinkDate;

$(function () {
  setupCodeLinkDate();
  var params = new URLSearchParams(location.search);
  var id = params.get("s");
  if (id && /^[A-Za-z0-9]{8}$/.test(id) && isMaster()) {
    fetch("/snippet?id=" + encodeURIComponent(id), { credentials: "same-origin" })
      .then(function (r) { return r.json().then(function (body) { return { ok: r.ok, body: body }; }); })
      .then(function (res) {
        if (!res.ok) {
          notify(res.body.error || "This code link doesn't exist.", { type: "error", title: "Code link not found" });
          return;
        }
        sharedSnippet = { id: id, lang: res.body.lang, code: res.body.code, applied: false };
        if ($("#optionlist").val() !== sharedSnippet.lang && typeof pickLanguage === "function") {
          pickLanguage(sharedSnippet.lang);   // an old link to a language without its own page
        } else {
          applySharedSnippet();
        }
      })
      .catch(function () {
        notify("Check your connection and reload the page.", { type: "error", title: "Couldn't load the shared code" });
      });
  }

  $("#create-code-link").on("click", function () {
    var btn = this;
    var ed = document.getElementById("editor");
    var code = ed && ed.env && ed.env.editor ? ed.env.editor.getValue() : "";
    if (!code.trim()) {
      notify("Write some code in the editor first.", { type: "info" });
      return;
    }
    btn.disabled = true;
    btn.textContent = "Creating…";
    fetch("/snippet", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ lang: $("#optionlist").val(), code: code, expires: $("#code-link-until").val() || "" })
    })
      .then(function (r) { return r.json().then(function (body) { return { ok: r.ok, body: body }; }); })
      .then(function (res) {
        if (!res.ok) {
          notify(res.body.error || "Try again in a moment.", { type: "error", title: "Couldn't create the link" });
          return;
        }
        $("#code-link").val(res.body.url);
        var ends = $("#code-link-ends");
        if (!ends.length) ends = $('<p class="share-code__ends" id="code-link-ends"></p>').insertAfter("#code-link-row");
        ends.text(res.body.expires ? "Works until " + new Date(res.body.expires * 1000).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" }) + ". Then it says it has expired." : "");
        $("#code-link-row").prop("hidden", false);
        $("#code-link").trigger("focus").trigger("select");
        btn.textContent = "Create a new link";
      })
      .catch(function () {
        notify("Check your connection and try again.", { type: "error", title: "Couldn't create the link" });
      })
      .then(function () {
        btn.disabled = false;
        if (btn.textContent === "Creating…") btn.textContent = "Create code link";
      });
  });

  $("#copy-code-link").on("click", function () {
    var text = $("#code-link").val();
    if (!text) return;
    var done = function () { notify("Anyone with the link can open a copy of this code.", { type: "success", title: "Code link copied" }); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { $("#code-link").trigger("select"); });
    } else {
      $("#code-link").trigger("select");
      document.execCommand("copy");
      done();
    }
  });
});

// ---------------------------------------------------------------------------
// Share popover from the keyboard (T14): Enter or Space on Share opens it,
// and aria-expanded follows it. Esc closes it (the workspace key handler).
// ---------------------------------------------------------------------------
$(function () {
  var btn = document.getElementById("share-btn-1");
  var pop = document.getElementById("myDropdown");
  if (!btn || !pop) return;
  function isOpen() { return pop.classList.contains("show"); }
  function sync() { btn.setAttribute("aria-expanded", isOpen() ? "true" : "false"); }
  sync();
  if (window.MutationObserver) new MutationObserver(sync).observe(pop, { attributes: true, attributeFilter: ["class"] });
  btn.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    myDropDownToggle();
    if (isOpen()) { var first = document.getElementById("share-it"); if (first) first.focus(); }
  });
});

export {};  // an ES module: strict mode, bundled by webpack
