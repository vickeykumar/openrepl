/*
 * Practice list page, /practice/dsa-questions (T17).
 * Search, filters, sorting and progress for the questions in PracticeStore
 * (js/practice-store.js). Plain JavaScript; DataTables and select2 are gone.
 * A question opens in the editor at /practice?name=<nameHyphenated>.
 */
(function () {
  "use strict";

  var LEVEL_ORDER = { Easy: 0, Medium: 1, Hard: 2 };
  var PREFS_KEY = "practiceFilters";
  var TRASH = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h16 M10 11v6 M14 11v6 M6 7l1 13h10l1-13 M9 7V4h6v3"></path></svg>';
  var armed = null, armTimer = null;

  function $(id) { return document.getElementById(id); }
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function levelClass(level) { return "level level--" + String(level || "").toLowerCase().replace(/[^a-z]/g, ""); }
  function questionUrl(q) { return "/practice?name=" + encodeURIComponent(q.nameHyphenated); }

  function relativeTime(ms) {
    var s = (Date.now() - ms) / 1000;
    if (!isFinite(s)) return "";
    if (s < 60) return "just now";
    if (s < 3600) return Math.floor(s / 60) + " min ago";
    if (s < 86400) { var h = Math.floor(s / 3600); return h + (h === 1 ? " hour ago" : " hours ago"); }
    if (s < 7 * 86400) { var d = Math.floor(s / 86400); return d === 1 ? "yesterday" : d + " days ago"; }
    var date = new Date(ms);
    var opts = { day: "numeric", month: "short" };
    if (date.getFullYear() !== new Date().getFullYear()) opts.year = "numeric";
    return date.toLocaleDateString(undefined, opts);
  }

  // ---- filters ---------------------------------------------------------------

  function prefs() {
    return { status: $("f-status").value, topic: $("f-topic").value, level: $("f-level").value, sort: $("f-sort").value };
  }
  function savePrefs() {
    try { localStorage.setItem(PREFS_KEY, JSON.stringify(prefs())); } catch (e) {}
  }
  function loadPrefs() {
    var p = null;
    try { p = JSON.parse(localStorage.getItem(PREFS_KEY)); } catch (e) {}
    if (!p) return;
    if (p.status != null) $("f-status").value = p.status;
    if (p.level != null) $("f-level").value = p.level;
    if (p.sort) $("f-sort").value = p.sort;
    $("f-topic").setAttribute("data-want", p.topic || "");
  }

  function syncTopicOptions(all) {
    var sel = $("f-topic");
    var want = sel.getAttribute("data-want");
    var current = want != null ? want : sel.value;
    sel.removeAttribute("data-want");
    var topics = {};
    all.forEach(function (q) { if (q.topic) topics[q.topic] = true; });
    var names = Object.keys(topics).sort(function (a, b) { return a.localeCompare(b); });
    if (current && !topics[current]) names.unshift(current); // keep a chosen topic until it's cleared
    sel.innerHTML = '<option value="">All topics</option>' + names.map(function (t) {
      return '<option value="' + esc(t) + '">' + esc(t) + "</option>";
    }).join("");
    sel.value = current || "";
  }

  function matches(q, f) {
    var done = PracticeStore.isDone(q.id);
    if (f.status === "done" && !done) return false;
    if (f.status === "todo" && done) return false;
    if (f.topic && q.topic !== f.topic) return false;
    if (f.level && q.difficulty !== f.level) return false;
    if (f.term) {
      var hay = (q.name + " " + q.topic + " " + q.difficulty).toLowerCase();
      var words = f.term.split(/\s+/);
      for (var i = 0; i < words.length; i++) if (hay.indexOf(words[i]) < 0) return false;
    }
    return true;
  }

  function sorter(kind) {
    var newest = function (a, b) { return (+b.added || 0) - (+a.added || 0); };
    switch (kind) {
      case "new": return newest;
      case "old": return function (a, b) { return -newest(a, b); };
      case "name": return function (a, b) { return String(a.name).localeCompare(String(b.name)); };
      case "level": return function (a, b) {
        var la = LEVEL_ORDER[a.difficulty], lb = LEVEL_ORDER[b.difficulty];
        return (la == null ? 9 : la) - (lb == null ? 9 : lb) || newest(a, b);
      };
      default: return function (a, b) { // to do first, newest first within each
        return (PracticeStore.isDone(a.id) ? 1 : 0) - (PracticeStore.isDone(b.id) ? 1 : 0) || newest(a, b);
      };
    }
  }

  // ---- rendering -------------------------------------------------------------

  function rowHtml(q) {
    var done = PracticeStore.isDone(q.id);
    var added = +q.added || 0;
    var iso = added ? new Date(added).toISOString() : "";
    return '<tr class="q-row' + (done ? " is-done" : "") + '" data-id="' + esc(q.id) + '">' +
      '<td class="q-done"><input type="checkbox" class="q-check" id="done-' + esc(q.id) + '"' + (done ? " checked" : "") +
        ' aria-label="Done: ' + esc(q.name) + '"></td>' +
      '<td class="q-name"><a class="q-link" href="' + esc(questionUrl(q)) + '" target="_blank" rel="noopener">' + esc(q.name) +
        '<span class="visually-hidden"> (opens in a new tab)</span></a></td>' +
      '<td class="q-topic" data-label="Topic">' + esc(q.topic || "") + "</td>" +
      '<td class="q-level" data-label="Level"><span class="' + levelClass(q.difficulty) + '">' + esc(q.difficulty || "") + "</span></td>" +
      '<td class="q-added" data-label="Added">' + (added ? '<time datetime="' + iso + '" title="' + esc(new Date(added).toLocaleString()) + '">' + esc(relativeTime(added)) + "</time>" : "") + "</td>" +
      '<td class="q-actions"><button type="button" class="q-delete" aria-label="Delete ' + esc(q.name) + '">' + TRASH + '<span class="q-delete__text">Delete?</span></button></td>' +
      "</tr>";
  }

  function renderProgress(all) {
    var done = all.filter(function (q) { return PracticeStore.isDone(q.id); }).length;
    var pct = all.length ? Math.round((done / all.length) * 100) : 0;
    $("progress-done").textContent = done;
    $("progress-total").textContent = all.length;
    $("progress-fill").style.width = pct + "%";
    $("progress-bar").setAttribute("aria-valuenow", String(pct));
    $("progress-bar").setAttribute("aria-valuetext", done + " of " + all.length + " done");
    ["Easy", "Medium", "Hard"].forEach(function (level) {
      var inLevel = all.filter(function (q) { return q.difficulty === level; });
      var d = inLevel.filter(function (q) { return PracticeStore.isDone(q.id); }).length;
      $("level-" + level.toLowerCase()).textContent = d + " of " + inLevel.length;
    });
  }

  function renderSync() {
    var el = $("sync-status");
    var s = PracticeStore.status();
    el.classList.toggle("is-synced", s === "synced");
    if (s === "synced") el.textContent = "Saved to your account.";
    else if (s === "syncing") el.textContent = "Saving to your account…";
    else if (s === "offline") el.textContent = "Saved in this browser. Your account couldn't be reached; it will try again.";
    else el.innerHTML = 'Saved in this browser. <a href="/">Sign in</a> to keep it on every device.';
  }

  function render() {
    var all = PracticeStore.all();
    renderProgress(all);
    syncTopicOptions(all);
    var f = prefs();
    f.term = $("q-search").value.trim().toLowerCase();
    var rows = all.filter(function (q) { return matches(q, f); }).sort(sorter(f.sort));
    $("q-body").innerHTML = rows.map(rowHtml).join("");
    var none = all.length === 0;
    $("questionsTable").hidden = none || rows.length === 0;
    $("q-empty").hidden = !none;
    $("q-nomatch").hidden = none || rows.length > 0;
    $("result-count").textContent = none ? "" :
      rows.length === all.length ? (all.length === 1 ? "1 question" : all.length + " questions") :
      "Showing " + rows.length + " of " + all.length;
    disarm();
  }

  // ---- delete in two steps: the first press asks, the second deletes ------------

  function disarm() {
    clearTimeout(armTimer);
    if (armed && document.contains(armed)) {
      armed.classList.remove("is-armed");
      armed.setAttribute("aria-label", armed.getAttribute("data-label"));
    }
    armed = null;
  }
  function arm(btn) {
    disarm();
    armed = btn;
    btn.setAttribute("data-label", btn.getAttribute("aria-label"));
    btn.setAttribute("aria-label", "Press again to delete " + btn.closest("tr").querySelector(".q-link").firstChild.textContent);
    btn.classList.add("is-armed");
    armTimer = setTimeout(disarm, 4000);
  }

  // ---- new and random question --------------------------------------------------

  function openDialog() { $("modal").classList.add("show-modal"); }
  function closeDialog() { $("modal").classList.remove("show-modal"); }

  function randomQuestion() {
    var all = PracticeStore.all();
    if (!all.length) {
      notify("Generate a question first.", { type: "info", title: "No questions yet" });
      openDialog();
      return;
    }
    var todo = all.filter(function (q) { return !PracticeStore.isDone(q.id); });
    var pool = todo.length ? todo : all;
    var q = pool[Math.floor(Math.random() * pool.length)];
    window.open(questionUrl(q), "_blank", "noopener");
  }

  function submitQuestion(e) {
    e.preventDefault();
    var btn = $("qsubmit-btn");
    if (btn.disabled) return;
    var topic = $("topic").value.trim();
    var level = $("difficulty").value.trim();
    var extra = $("customPrompt").value.trim();
    if (!level) {
      $("difficulty").focus();
      notify("Choose Easy, Medium or Hard.", { type: "error", title: "Pick a level" });
      return;
    }
    var temp = parseFloat($("temperature").value);
    if (!isNaN(temp)) {
      globaltemperature = Math.min(1, Math.max(0, temp));
      try { localStorage.setItem("temperature", globaltemperature); } catch (err) {}
    }
    try {
      localStorage.setItem("topic", topic);
      localStorage.setItem("difficultyLevel", level);
    } catch (err) {}
    // the dialog shows "Generating…" while common.js's loader is up (practice-store.js)
    generateNewQuestion(topic, level, extra)
      .then(function (q) {
        if (!q) return;
        var result = saveNewQuestions(q);
        if (result.error) {
          notify(String(result.error), { type: "error", title: "Couldn't save the new question" });
          return;
        }
        closeDialog();
        render();
        notify(q.name, { type: "success", title: "New question added" });
        var row = document.querySelector('.q-row[data-id="' + q.id + '"] .q-link');
        if (row) row.focus();
      })
      .catch(function (err) {
        console.error("Error generating a new question:", err);
        notify("Please try again.", { type: "error", title: "Couldn't generate a new question" });
      });
  }

  // ---- wiring -------------------------------------------------------------------

  function init() {
    loadPrefs();
    try {
      $("topic").value = localStorage.getItem("topic") || "";
      $("difficulty").value = localStorage.getItem("difficultyLevel") || "";
    } catch (e) {}
    $("temperature").value = globaltemperature;

    $("q-search").addEventListener("input", render);
    ["f-status", "f-topic", "f-level", "f-sort"].forEach(function (id) {
      $(id).addEventListener("change", function () { savePrefs(); render(); });
    });
    $("q-clear").addEventListener("click", function () {
      $("q-search").value = "";
      $("f-status").value = "";
      $("f-topic").value = "";
      $("f-level").value = "";
      savePrefs();
      render();
      $("q-search").focus();
    });

    var body = $("q-body");
    body.addEventListener("change", function (e) {
      if (!e.target.classList.contains("q-check")) return;
      var id = e.target.closest("tr").getAttribute("data-id");
      PracticeStore.setDone(id, e.target.checked);
      var again = $("done-" + id);  // the row may have moved
      if (again) again.focus();
    });
    body.addEventListener("click", function (e) {
      var btn = e.target.closest && e.target.closest(".q-delete");
      if (!btn) return;
      if (btn !== armed) { arm(btn); return; }
      var row = btn.closest("tr");
      var id = row.getAttribute("data-id");
      var name = row.querySelector(".q-link").firstChild.textContent;
      var next = row.nextElementSibling || row.previousElementSibling;
      var nextId = next && next.getAttribute("data-id");
      disarm();
      PracticeStore.remove(id);
      notify(name, { type: "info", title: "Question deleted" });
      var focusTo = nextId && document.querySelector('.q-row[data-id="' + nextId + '"] .q-delete');
      (focusTo || $("q-search")).focus();
    });
    body.addEventListener("focusout", function (e) {
      if (armed && e.target === armed) disarm();
    });

    $("newQuestionBtn").addEventListener("click", openDialog);
    $("q-empty-new").addEventListener("click", openDialog);
    $("randomQuestionBtn").addEventListener("click", randomQuestion);
    $("close").addEventListener("click", closeDialog);
    $("modal").addEventListener("mousedown", function (e) { if (e.target === this) closeDialog(); });
    $("question-form").addEventListener("submit", submitQuestion);

    PracticeStore.onChange(function (reason) {
      if (reason === "status") renderSync();
      else render();
    });
    render();
    renderSync();
    PracticeStore.init();
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
