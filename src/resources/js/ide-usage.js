// The IDE data chip, the one-time note at 80% and the notes at 100% (canvas "IDE data usage and limits", board 5). The server counts what the
// IDE sends to a user (package ideusage); this asks for where the user stands (GET ./ide_usage) every minute and shows
//   - at the warning (80% by default): a chip in the editor header, "160 of 200 MB today", and a note once for each day (each session for a guest),
//   - at the limit: a red chip, and a note under the header: for a guest the way to sign in for more, for a signed-in user "See usage" and a
//     greyed "Add credits, coming later". The terminal prints its own notice when its run is ended.
// It sends the time zone of the page (minutes east of UTC) so that a notice can say when the day resets in the user's own time.
(function () {
  "use strict";
  var POLL_MS = 60 * 1000;
  var chip = null, note = null, lastKey = "", wasHigh = false;

  function mb(n) {
    n = (n || 0) / 1048576;
    return (n >= 10 ? Math.round(n) : n.toFixed(1)) + " MB";
  }
  // "160 of 200 MB": the used number carries no unit of its own while the limit has it
  function ofText(used, limit) {
    var u = used / 1048576;
    return (u >= 10 ? Math.round(u) : u.toFixed(1)) + " of " + mb(limit);
  }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function resetText(ms) {
    return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", timeZoneName: "short" });
  }
  function seen(key) { try { return localStorage.getItem(key) === "1"; } catch (e) { return false; } }
  function mark(key) { try { localStorage.setItem(key, "1"); } catch (e) { /* storage off */ } }

  function ensureChip() {
    if (chip) return chip;
    var tools = document.querySelector(".editor-header__tools");
    if (!tools) return null;
    chip = el("a", "ide-data-chip");
    chip.setAttribute("href", "./profile");
    chip.setAttribute("data-tip-name", "Data used");
    chip.hidden = true;
    tools.insertBefore(chip, tools.firstChild);
    return chip;
  }

  function closeNote() {
    if (note && note.parentNode) note.parentNode.removeChild(note);
    note = null;
  }

  function showNote(kind, title, text, actions, dismissLabel) {
    closeNote();
    note = el("div", "ide-data-note ide-data-note--" + kind);
    note.setAttribute("role", kind === "limit" ? "alertdialog" : "status");
    note.setAttribute("aria-label", title);
    note.appendChild(el("b", "", title));
    note.appendChild(el("span", "", text));
    var row = el("span", "ide-data-note__actions");
    (actions || []).forEach(function (a) {
      var b;
      if (a.href) { b = el("a", "ide-data-btn", a.label); b.setAttribute("href", a.href); }
      else { b = el("button", "ide-data-btn", a.label); b.type = "button"; if (a.onclick) b.addEventListener("click", a.onclick); }
      if (a.disabled) { b.setAttribute("aria-disabled", "true"); b.disabled = true; b.className += " is-disabled"; }
      row.appendChild(b);
    });
    var x = el("button", "ide-data-btn ide-data-btn--quiet", dismissLabel || "Dismiss");
    x.type = "button";
    x.addEventListener("click", closeNote);
    row.appendChild(x);
    note.appendChild(row);
    document.body.appendChild(note);
  }

  function apply(data) {
    var st = data.state || {};
    var c = ensureChip();
    if (!st.on || st.exempt || !st.limit) { if (c) c.hidden = true; closeNote(); return; }
    var guest = st.scope === "session";
    var day = new Date().toISOString().slice(0, 10);
    var key = (guest ? "s" : day) + (st.atLimit ? "L" : st.warn ? "W" : "");
    if (c) {
      if (st.warn || st.atLimit) {
        c.hidden = false;
        c.className = "ide-data-chip " + (st.atLimit ? "is-limit" : "is-warn");
        c.textContent = ofText(st.used, st.limit) + (st.atLimit ? " used" : guest ? " this session" : " today");
        c.title = guest ? "Guests get " + mb(st.limit) + " per session (1 hour). Sign in for more." : "Resets at " + (st.resetsAt ? resetText(st.resetsAt) : "the next reset") + ". See your usage in your profile.";
      } else if (wasHigh) {
        // the day started again: the chip goes back to green for the rest of the page's life
        c.hidden = false;
        c.className = "ide-data-chip is-ok";
        c.textContent = ofText(st.used, st.limit) + (guest ? " this session" : " today");
        c.title = "The data limit is back to full.";
      } else {
        c.hidden = true;
      }
    }
    if (key === lastKey) return;
    var before = lastKey;
    lastKey = key;
    if (st.warn || st.atLimit) wasHigh = true;
    if (!st.warn && !st.atLimit && /[LW]$/.test(before)) {
      showNote("ok", "Data limit is back to full", "Terminals run again and Genie answers again. No action needed.", []);
      return;
    }
    if (st.atLimit) {
      if (guest) {
        showNote("limit", "You have used this session's " + mb(st.limit),
          "Guests get " + mb(st.limit) + " per session (1 hour). Sign in free to get " + mb(st.signInLimit || 0) + " a day. Your code and files stay here.",
          [{ label: "Sign in", onclick: function () { closeNote(); if (typeof window.openSignIn === "function") window.openSignIn(); } }], "Maybe later");
      } else {
        showNote("limit", "Daily data limit reached (" + mb(st.limit) + ")",
          "Output is paused until " + (st.resetsAt ? resetText(st.resetsAt) : "the next reset") + ". Your files are safe.",
          [{ label: "See usage", href: "./profile" }, { label: "Add credits, coming later", disabled: true }]);
      }
    } else if (st.warn && !seen("ide-data-warned-" + key)) {
      mark("ide-data-warned-" + key);
      showNote("warn", "You have used " + st.pct + "% of " + (guest ? "this session's" : "today's") + " data",
        guest ? "Sign in free to get " + mb(st.signInLimit || 0) + " a day." : "Resets at " + (st.resetsAt ? resetText(st.resetsAt) : "the next reset") + ". See usage in your profile.",
        guest ? [{ label: "Sign in", onclick: function () { closeNote(); if (typeof window.openSignIn === "function") window.openSignIn(); } }] : [{ label: "See usage", href: "./profile" }]);
    }
  }

  function poll() {
    var tz = -new Date().getTimezoneOffset();
    fetch("./ide_usage?days=0&tz=" + tz, { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d) apply(d); })
      .catch(function () { /* the chip stays as it was */ });
  }
  function start() {
    poll();
    setInterval(function () { if (!document.hidden) poll(); }, POLL_MS);
    document.addEventListener("visibilitychange", function () { if (!document.hidden) poll(); });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start); else start();
})();
