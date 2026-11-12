// Unsaved changes (designed in the "Unsaved changes prompts" canvas): the browser's question when the page is reloaded or
// closed while a tab has text that is not saved, a bar that offers Save all (after Cancel, and for what comes back after
// a reload), the Save / Don't save / Cancel question when a changed tab is closed, the Save all command and key, and the
// dot in the title of the browser tab. The page's rules about WHAT is unsaved are in 22-editor-tabs.js; this part asks.
//
// A web page cannot change the words of the browser's question or put Save in it, and a browser may not show it at all
// (a phone, or a page the visitor has not used yet): the copy of unsaved text that 22-editor-tabs.js keeps in the browser
// is what stays, and it comes back after a reload through the bar of this part. Nothing is written to the server without
// the visitor's choice.

var MAC = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
var DOT = "● ";

var recovered = [];        // paths whose text came back after a reload and has not been dealt with
var leaveCancelled = false; // the browser asked and the visitor stayed
var leaveTimer = 0;
var reviewOpen = false;
var applying = false;       // the title is being set by this part

function tabs() { return window.EditorTabs; }
function dirtyList() { return tabs() && tabs().dirtyPaths ? tabs().dirtyPaths() : []; }
function nameOf(p) { return p === "untitled:" ? "untitled" : String(p).split("/").pop(); }
function say(text, type) { if (window.notify) window.notify(text, { type: type || "info", timeout: 3500 }); }

// ---- the question -----------------------------------------------------------------------------------------------------

var dialog = null;

function build() {
  if (dialog) return dialog;
  dialog = document.createElement("div");
  dialog.className = "unsaved-backdrop";
  dialog.hidden = true;
  dialog.innerHTML =
    '<div class="unsaved-dialog" role="alertdialog" aria-modal="true" aria-labelledby="unsaved-title" aria-describedby="unsaved-body">' +
    '<h2 id="unsaved-title"></h2><p id="unsaved-body"></p><div id="unsaved-field"></div><div class="unsaved-dialog__buttons" id="unsaved-buttons"></div>' +
    '<p class="unsaved-dialog__hint" id="unsaved-hint"></p></div>';
  document.body.appendChild(dialog);
  return dialog;
}

// ask({ title, body, hint, buttons: [{ id, label, primary }], cancel: id, input: { value, label } }) -> a promise of the id
// chosen; with an input, of { id, value }
function ask(o) {
  return new Promise(function (resolve) {
    var d = build();
    var returnFocus = document.activeElement;
    d.querySelector("#unsaved-title").textContent = o.title;
    d.querySelector("#unsaved-body").textContent = o.body;
    d.querySelector("#unsaved-hint").textContent = o.hint || "";
    var field = d.querySelector("#unsaved-field");
    field.textContent = "";
    var input = null;
    if (o.input) {
      input = document.createElement("input");
      input.type = "text";
      input.className = "unsaved-dialog__input";
      input.value = o.input.value || "";
      input.spellcheck = false;
      input.autocomplete = "off";
      input.setAttribute("aria-label", o.input.label || "Name");
      field.appendChild(input);
    }
    var row = d.querySelector("#unsaved-buttons");
    row.textContent = "";
    var primary = null;
    var finish = function (id) {
      d.hidden = true;
      document.removeEventListener("keydown", onKey, true);
      if (returnFocus && returnFocus.focus && document.contains(returnFocus)) { try { returnFocus.focus({ preventScroll: true }); } catch (e) {} }
      resolve(input ? { id: id, value: input.value } : id);
    };
    function onKey(e) {
      if (e.key === "Escape" && o.cancel) { e.preventDefault(); e.stopPropagation(); finish(o.cancel); }
      else if (e.key === "Enter" && primary && document.activeElement && !document.activeElement.closest(".unsaved-dialog__buttons")) { e.preventDefault(); e.stopPropagation(); primary.click(); }
      else if (e.key === "Tab") {
        var bs = [].slice.call(row.querySelectorAll("button"));
        if (!bs.length) return;
        var i = bs.indexOf(document.activeElement);
        var to = e.shiftKey ? (i <= 0 ? bs[bs.length - 1] : bs[i - 1]) : (i < 0 || i === bs.length - 1 ? bs[0] : bs[i + 1]);
        e.preventDefault();
        to.focus();
      }
    }
    o.buttons.forEach(function (b) {
      var el = document.createElement("button");
      el.type = "button";
      el.className = "unsaved-dialog__btn" + (b.primary ? " is-primary" : "");
      el.textContent = b.label;
      el.dataset.id = b.id;
      el.addEventListener("click", function () { finish(b.id); });
      if (b.primary) primary = el;
      row.appendChild(el);
    });
    d.hidden = false;
    document.addEventListener("keydown", onKey, true);
    if (input) {
      input.focus();
      var dot = input.value.lastIndexOf(".");
      input.setSelectionRange(0, dot > 0 ? dot : input.value.length); // the name is selected, not its extension
    } else {
      (primary || row.querySelector("button")).focus();
    }
  });
}

// the name for an untitled file that is being saved: the name, or null when it was cancelled. `note` says what was wrong
// with the last one.
function askName(value, note) {
  return ask({
    title: "Save the untitled file as",
    body: note || "In your workspace folder. Pick another name if you like.",
    hint: "Enter saves. Esc cancels.",
    input: { value: value, label: "File name" },
    buttons: [{ id: "cancel", label: "Cancel" }, { id: "save", label: "Save", primary: true }],
    cancel: "cancel"
  }).then(function (r) { return r.id === "save" ? String(r.value).trim() : null; });
}

// a changed tab is being closed: "save", "discard" or "cancel"
function askClose(path) {
  return ask({
    title: "Save the changes to " + nameOf(path) + "?",
    body: "Your changes will be lost if you do not save them. The file is saved to your workspace.",
    hint: "Enter saves. Esc cancels, and the tab stays.",
    buttons: [{ id: "discard", label: "Don't save" }, { id: "cancel", label: "Cancel" }, { id: "save", label: "Save", primary: true }],
    cancel: "cancel"
  });
}

// a collaborator closes the owner's tab that has unsaved changes: "save" or "cancel" (dropping them is the owner's to decide)
function askShared(path) {
  return ask({
    title: "Save the changes to " + nameOf(path) + "?",
    body: "These are unsaved changes of the person who started the session. Save writes them to the file and closes the tab.",
    hint: "Enter saves. Esc cancels, and the tab stays.",
    buttons: [{ id: "cancel", label: "Cancel" }, { id: "save", label: "Save", primary: true }],
    cancel: "cancel"
  });
}

// a sixth file pushes a changed tab out (every tab has changes): "save" or "discard"
function askDropped(path) {
  return ask({
    title: "Save the changes to " + nameOf(path) + "?",
    body: "Opening this file closes the oldest tab, and all five have unsaved changes.",
    hint: "Enter saves.",
    buttons: [{ id: "discard", label: "Don't save" }, { id: "save", label: "Save", primary: true }]
  });
}

// ---- the bar ----------------------------------------------------------------------------------------------------------

var bar = null;

function buildBar() {
  if (bar) return bar;
  var ide = document.getElementById("ide");
  var header = ide && ide.querySelector(".editor-header");
  if (!ide || !header) return null;
  bar = document.createElement("div");
  bar.className = "unsaved-bar";
  bar.id = "unsaved-bar";
  bar.setAttribute("role", "status");
  bar.hidden = true;
  ide.insertBefore(bar, header);
  bar.addEventListener("click", function (e) {
    var b = e.target.closest("button[data-act]");
    if (!b) return;
    var act = b.dataset.act;
    var path = b.dataset.path;
    if (act === "save-all") saveAll();
    else if (act === "review") { reviewOpen = !reviewOpen; refresh(); }
    else if (act === "discard-all") discardAll();
    else if (act === "keep") { recovered = recovered.filter(function (p) { return p !== path; }); refresh(); }
    else if (act === "discard") { tabs().discard(path); recovered = recovered.filter(function (p) { return p !== path; }); refresh(); }
  });
  return bar;
}

function button(label, act, cls, path) {
  var b = document.createElement("button");
  b.type = "button";
  b.className = "unsaved-bar__btn" + (cls ? " " + cls : "");
  b.textContent = label;
  b.dataset.act = act;
  if (path) b.dataset.path = path;
  return b;
}

function drawBar(dirty) {
  var el = buildBar();
  if (!el) return;
  var mine = recovered.filter(function (p) { return dirty.indexOf(p) >= 0; });
  recovered = mine;
  var show = mine.length > 0 || (leaveCancelled && dirty.length > 0);
  el.hidden = !show;
  if (!show) { reviewOpen = false; el.textContent = ""; return; }
  el.textContent = "";
  var row = document.createElement("div");
  row.className = "unsaved-bar__row";
  var dot = document.createElement("span");
  dot.className = "unsaved-bar__dot";
  dot.setAttribute("aria-hidden", "true");
  var text = document.createElement("span");
  text.className = "unsaved-bar__text";
  if (mine.length) {
    text.textContent = "Changes you had not saved are back in " + mine.length + (mine.length === 1 ? " file" : " files") + ".";
  } else {
    var names = dirty.map(nameOf);
    text.textContent = dirty.length + (dirty.length === 1 ? " file has" : " files have") + " changes that are not saved: " + names.slice(0, 3).join(", ") + (names.length > 3 ? " and " + (names.length - 3) + " more" : "") + ".";
  }
  row.appendChild(dot); row.appendChild(text);
  row.appendChild(button("Save all", "save-all", "is-primary"));
  if (mine.length) {
    var rv = button("Review", "review");
    rv.setAttribute("aria-expanded", reviewOpen ? "true" : "false");
    row.appendChild(rv);
    row.appendChild(button("Discard all", "discard-all"));
  }
  el.appendChild(row);
  if (reviewOpen && mine.length) {
    var list = document.createElement("div");
    list.className = "unsaved-bar__list";
    mine.forEach(function (p) {
      var item = document.createElement("div");
      item.className = "unsaved-bar__item";
      var n = document.createElement("span");
      n.textContent = nameOf(p);
      n.title = p;
      item.appendChild(n);
      item.appendChild(button("Keep", "keep", "", p));
      item.appendChild(button("Discard", "discard", "", p));
      list.appendChild(item);
    });
    el.appendChild(list);
  }
}

// ---- the title of the browser tab ---------------------------------------------------------------------------------------

function titleEl() { return document.querySelector("head > title"); }

function applyTitle(count) {
  var t = titleEl();
  if (!t) return;
  var base = document.title.replace(/^● /, "");
  var want = count > 0 ? DOT + base : base;
  if (document.title !== want) { applying = true; document.title = want; applying = false; }
}

// ---- saving all -------------------------------------------------------------------------------------------------------

var saving = false;
function saveAll() {
  if (saving || !tabs()) return Promise.resolve(false);
  var list = dirtyList();
  if (!list.length) return Promise.resolve(true);
  saving = true;
  var done = 0;
  var chain = Promise.resolve(true);
  list.forEach(function (p) {
    chain = chain.then(function (ok) {
      if (!ok) return false;
      return new Promise(function (resolve) { tabs().saveFile(p, function (saved) { if (saved) done++; resolve(saved); }); });
    });
  });
  return chain.then(function (ok) {
    saving = false;
    leaveCancelled = false;
    recovered = [];
    refresh();
    if (ok) say(done === 1 ? "Saved 1 file." : "Saved " + done + " files.", "success");
    return ok;
  });
}

function discardAll() {
  dirtyList().forEach(function (p) { if (recovered.indexOf(p) >= 0) tabs().discard(p); });
  recovered = [];
  refresh();
}

// ---- keeping it all up to date ---------------------------------------------------------------------------------------

function refresh() {
  var dirty = dirtyList();
  if (!dirty.length) { leaveCancelled = false; recovered = []; }
  drawBar(dirty);
  applyTitle(dirty.length);
}

// a tab came back with text that was not saved
function setRecovered(paths) {
  recovered = paths.slice();
  reviewOpen = false;
  refresh();
}

function init() {
  if (!tabs()) return;
  // the browser's question, while a tab has unsaved text: shown by the browser on reload, close and leave
  window.addEventListener("beforeunload", function (e) {
    if (!(typeof isMaster === "function" ? isMaster() : true) || !dirtyList().length) return;
    e.preventDefault();
    e.returnValue = ""; // the browser's own words
    // if the page is still here a moment later, the visitor chose to stay
    clearTimeout(leaveTimer);
    leaveTimer = setTimeout(function () { leaveCancelled = true; refresh(); }, 700);
  });
  window.addEventListener("pagehide", function () { clearTimeout(leaveTimer); });
  // Save all, from the keyboard (Ctrl or Cmd, Alt and S; Ctrl or Cmd and S saves the file that is shown)
  window.addEventListener("keydown", function (e) {
    var mod = MAC ? e.metaKey : e.ctrlKey;
    if (mod && e.altKey && !e.shiftKey && (e.code === "KeyS" || e.key === "s" || e.key === "S")) {
      if (!dirtyList().length) return;
      e.preventDefault();
      e.stopPropagation();
      saveAll();
    }
  }, true);
  // other code writes the title too (the terminal's): the dot is put back
  var t = titleEl();
  if (t && window.MutationObserver) new MutationObserver(function () { if (!applying) applyTitle(dirtyList().length); }).observe(t, { childList: true, characterData: true, subtree: true });
  refresh();
}

window.Unsaved = {
  askClose: askClose,
  askDropped: askDropped,
  askShared: askShared,
  askName: askName,
  saveAll: saveAll,
  recovered: setRecovered,
  refresh: refresh,
  count: function () { return dirtyList().length; },
  init: init
};

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
