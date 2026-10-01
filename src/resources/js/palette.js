/*
 * Command palette and keyboard shortcuts (T14).
 *
 *   Ctrl/Cmd + Shift + P   open the palette from anywhere, the editor and terminal included
 *   Ctrl/Cmd + K           open the palette outside the editor and terminal (they use Ctrl+K)
 *   Ctrl + `               move between the editor and the terminal
 *   ?                      list the shortcuts (outside text fields)
 *
 * The palette is a modal dialog with a search box (combobox) over a listbox of
 * commands. Commands are rebuilt each time it opens, so labels follow the
 * current state ("Hide files" or "Show files"). The functions it calls live in
 * scribbler.js, common.js, theme.js and gotty-bundle.js.
 */
(function () {
  "use strict";

  var MAC = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  var MOD = MAC ? "⌘" : "Ctrl";
  var SHIFT = MAC ? "⇧" : "Shift";

  var root = null, input = null, listEl = null, emptyEl = null, statusEl = null, titleEl = null;
  var searchRow = null, shortcutsEl = null, footEl = null;
  var items = [], shown = [], active = 0, returnFocus = null, mode = "commands";

  function byId(id) { return document.getElementById(id); }
  function hasWorkspace() { return !!byId("workspace"); }
  function call(name) {
    var args = Array.prototype.slice.call(arguments, 1);
    return function () { if (typeof window[name] === "function") window[name].apply(window, args); };
  }
  function clickId(id) { return function () { var el = byId(id); if (el) el.click(); }; }
  function esc(s) {
    return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; });
  }
  function kbd(keys) {
    if (!keys) return "";
    return '<span class="palette__keys">' + keys.map(function (k) { return "<kbd>" + esc(k) + "</kbd>"; }).join("") + "</span>";
  }

  // ---- focus helpers --------------------------------------------------------
  function editorInstance() {
    var el = byId("editor");
    return el && el.env && el.env.editor;
  }
  function editorShown() { return !document.body.classList.contains("editor-hidden"); }
  function inTerminal(el) { return !!(el && el.closest && el.closest("#terminal-div .terminal")); }
  function inCodeArea(el) { return !!(el && el.closest && el.closest("#ide .editor-body, #terminal-div .terminal")); }
  function isTextField(el) {
    if (!el || !el.closest) return false;
    if (el.isContentEditable) return true;
    var tag = el.tagName;
    if (tag === "TEXTAREA" || tag === "SELECT") return true;
    if (tag === "INPUT") return !/^(button|checkbox|radio|submit|reset|range|color|file|image)$/i.test(el.type || "");
    return false;
  }

  function focusTerminal() {
    var t = document.querySelector("#terminal-div .terminal.active textarea");
    if (t) t.focus();
  }
  function focusEditor() {
    if (!editorShown() && typeof window.ToggleEditor === "function") window.ToggleEditor();
    var ed = editorInstance();
    if (ed) setTimeout(function () { ed.focus(); }, 30);
  }

  // ---- actions the palette runs ---------------------------------------------
  function saveFile() {
    var ed = editorInstance();
    if (ed && ed.commands.byName.Save) ed.execCommand("Save");
  }
  function openShare(thenId) {
    var pop = byId("myDropdown");
    if (pop && !pop.classList.contains("show") && typeof window.myDropDownToggle === "function") window.myDropDownToggle();
    var target = byId(thenId || "share-it");
    if (target) target.focus();
  }
  function createCodeLink() {
    openShare("create-code-link");
    var b = byId("create-code-link");
    if (b && !b.disabled) b.click();
  }
  function openRunOptions() {
    var menu = byId("run-menu");
    if (menu && menu.hidden && typeof window.toggleRunMenu === "function") window.toggleRunMenu();
    var f = byId("compiler_flags");
    if (f) f.focus();
  }
  function openSelect(id) {
    return function () {
      var sel = byId(id);
      if (!sel) return;
      sel.focus();
      if (sel.showPicker) { try { sel.showPicker(); } catch (e) {} }
    };
  }
  function goTo(url) { return function () { location.href = url; }; }
  function sendFeedback() {
    var f = byId("feedback-name") || byId("message");
    if (f) f.focus();
  }

  function buildCommands() {
    var list = [];
    function add(group, label, run, opts) {
      opts = opts || {};
      list.push({ group: group, label: label, run: run, keys: opts.keys || null, words: opts.words || "" });
    }
    var ws = hasWorkspace();
    if (ws) {
      add("Run", "Run code", call("CompileandRun"), { keys: [MOD, "↵"], words: "execute start" });
      add("Run", "Debug code", call("RunandDebug"), { keys: [SHIFT, MOD, "↵"], words: "gdb breakpoint" });
      add("Run", "Set program arguments and environment variables", openRunOptions, { words: "args flags env options" });
      add("Run", "Start a fresh REPL in this tab", call("ToggleReconnect"), { words: "reconnect restart reset" });

      add("Terminal", "Focus the terminal", focusTerminal, { keys: ["Ctrl", "`"], words: "go to repl console" });
      add("Terminal", "New terminal tab", function () { if (window.gotty && gotty.addTab) gotty.addTab(); }, { words: "add open" });
      var tabs = document.querySelectorAll("#terminal-tabs .tab");
      for (var t = 0; t < tabs.length; t++) {
        if (tabs[t].classList.contains("active")) continue;
        var title = (tabs[t].querySelector(".tab-title") || tabs[t]).textContent.trim() || "Terminal " + (t + 1);
        add("Terminal", "Switch to tab: " + title, (function (tab) { return function () { tab.click(); focusTerminal(); }; })(tabs[t]), { words: "terminal tab" });
      }

      add("Editor", "Focus the editor", focusEditor, { keys: ["Ctrl", "`"], words: "go to code" });
      add("Editor", "Save the open file", saveFile, { keys: [MOD, "S"], words: "write" });
      add("Editor", "Download the editor code", call("DownloadEditor"), { words: "export save as" });
      add("Editor", editorShown() ? "Hide the editor" : "Show the editor", call("ToggleEditor"), { words: "toggle layout" });
      var rot = byId("rotate-button-label");
      add("Editor", "Layout: " + (rot ? rot.textContent.trim().toLowerCase() : "switch"), call("ToggleRotateEditor"), { words: "rotate stacked side by side split" });
      add("Editor", "Change the editor theme", openSelect("select-theme"), { words: "colors syntax" });
      add("Editor", "Change the editor syntax mode", openSelect("select-lang"), { words: "highlighting language mode" });
      var max = document.body.classList.contains("ide-maximized");
      add("Editor", max ? "Restore the workspace" : "Maximize the workspace", call("ToggleFunction"), { keys: max ? ["Esc"] : null, words: "fullscreen full screen" });

      var filesOpen = !document.body.classList.contains("files-collapsed");
      add("Files", filesOpen ? "Hide files" : "Show files", call("setFilesPanel", !filesOpen), { words: "panel tree sidebar" });
      add("Files", "Upload a file", clickId("upload-button"), { words: "import add" });
      add("Files", "Download the workspace as a zip", clickId("download-workspace-button"), { words: "export archive" });

      add("Share", "Share this session live", function () { openShare(); }, { words: "invite collaborate link" });
      add("Share", "Create a code link", createCodeLink, { words: "snippet share url copy" });
      var fork = document.querySelector("#fork-widget .forkbtn");
      if (fork) add("Share", "Fork: open a second terminal in this sandbox", function () { fork.click(); }, { words: "duplicate new window" });

      if (byId("genie-button")) add("Genie", "Ask Genie", clickId("genie-button"), { words: "ai chat help assistant explain" });

      var sel = byId("optionlist");
      if (sel) {
        for (var i = 0; i < sel.options.length; i++) {
          var o = sel.options[i];
          if (o.value === sel.value) continue;
          add("Language", "Switch to " + o.text.trim(), call("pickLanguage", o.value), { words: "language repl " + o.value });
        }
      }
    }

    var dark = document.documentElement.getAttribute("data-theme") === "dark";
    if (document.querySelector("[data-theme-toggle]")) {
      add("Appearance", dark ? "Switch to light theme" : "Switch to dark theme", function () {
        var b = document.querySelector("[data-theme-toggle]");
        if (b) b.click();
      }, { words: "mode color night day" });
    }

    add("Help", "Keyboard shortcuts", function () { open("shortcuts"); }, { keys: ["?"], words: "keys hotkeys keybindings" });
    if (ws && typeof window.StartTour === "function") add("Help", "Take the tour", call("StartTour"), { words: "intro getting started guide" });
    add("Help", "Practice DSA questions", goTo("/practice/dsa-questions"), { words: "problems interview" });
    add("Help", "Read the docs", goTo("/doc.html"), { words: "documentation help" });
    if (byId("feedback-name") || byId("message")) add("Help", "Send feedback or request a language", sendFeedback, { words: "contact message" });
    return list;
  }

  // ---- search ---------------------------------------------------------------
  function filter(query) {
    var q = query.trim().toLowerCase();
    if (!q) return items.slice();
    var words = q.split(/\s+/);
    var scored = [];
    items.forEach(function (it, idx) {
      var label = it.label.toLowerCase();
      var hay = label + " " + it.group.toLowerCase() + " " + it.words.toLowerCase();
      for (var w = 0; w < words.length; w++) if (hay.indexOf(words[w]) < 0) return;
      var score = 3;
      if (label.indexOf(q) === 0) score = 0;
      else if ((" " + label).indexOf(" " + words[0]) >= 0) score = 1;
      else if (label.indexOf(words[0]) >= 0) score = 2;
      scored.push({ it: it, score: score, idx: idx });
    });
    scored.sort(function (a, b) { return a.score - b.score || a.idx - b.idx; });
    return scored.map(function (s) { return s.it; });
  }

  function render() {
    shown = filter(input.value);
    var grouped = !input.value.trim();
    var html = "", lastGroup = null, openGroup = false;
    shown.forEach(function (it, i) {
      if (grouped && it.group !== lastGroup) {
        if (openGroup) html += "</div>";
        var gid = "palette-g-" + it.group.toLowerCase().replace(/\W+/g, "-");
        html += '<div role="group" aria-labelledby="' + gid + '"><div class="palette__group" id="' + gid + '" role="presentation">' + esc(it.group) + "</div>";
        openGroup = true;
        lastGroup = it.group;
      }
      var right = it.keys ? kbd(it.keys) : (grouped ? "" : '<span class="palette__tag">' + esc(it.group) + "</span>");
      html += '<div class="palette__item" role="option" id="palette-opt-' + i + '" data-i="' + i + '" aria-selected="false">' +
        '<span class="palette__label">' + esc(it.label) + "</span>" + right + "</div>";
    });
    if (openGroup) html += "</div>";
    listEl.innerHTML = html;
    emptyEl.hidden = shown.length > 0;
    listEl.hidden = shown.length === 0;
    statusEl.textContent = shown.length === 1 ? "1 command" : shown.length + " commands";
    setActive(0);
  }

  function setActive(i) {
    if (!shown.length) { input.removeAttribute("aria-activedescendant"); return; }
    active = (i + shown.length) % shown.length;
    var prev = listEl.querySelector('[aria-selected="true"]');
    if (prev) prev.setAttribute("aria-selected", "false");
    var el = byId("palette-opt-" + active);
    if (!el) return;
    el.setAttribute("aria-selected", "true");
    input.setAttribute("aria-activedescendant", el.id);
    var top = el.offsetTop, bottom = top + el.offsetHeight;
    if (active === 0) listEl.scrollTop = 0;
    else if (top < listEl.scrollTop) listEl.scrollTop = top - 8;
    else if (bottom > listEl.scrollTop + listEl.clientHeight) listEl.scrollTop = bottom - listEl.clientHeight + 8;
  }

  function runItem(it) {
    if (!it) return;
    close(true);
    // after the click or keypress has finished, so popovers the action opens stay open
    setTimeout(function () { it.run(); }, 0);
  }

  // ---- shortcuts list -------------------------------------------------------
  var SHORTCUTS = [
    ["Run the editor code", [MOD, "↵"]],
    ["Debug the editor code", [SHIFT, MOD, "↵"]],
    ["Open commands, from anywhere", [MOD, SHIFT, "P"]],
    ["Open commands, outside the editor and terminal", [MOD, "K"]],
    ["Move between the editor and the terminal", ["Ctrl", "`"]],
    ["Save the open file, in the editor", [MOD, "S"]],
    ["Leave the editor or terminal", ["Esc", "then", "Tab"]],
    ["Restore a maximized workspace", ["Esc"]],
    ["Show this list, outside text fields", ["?"]]
  ];
  function renderShortcuts() {
    var rows = SHORTCUTS.map(function (s) {
      var keys = s[1].map(function (k) { return k === "then" ? '<span class="palette__then">then</span>' : "<kbd>" + esc(k) + "</kbd>"; }).join("");
      return "<tr><td>" + esc(s[0]) + '</td><td><span class="palette__keys">' + keys + "</span></td></tr>";
    }).join("");
    shortcutsEl.innerHTML = '<table class="palette__table"><caption class="visually-hidden">Keyboard shortcuts</caption>' +
      '<thead class="visually-hidden"><tr><th scope="col">Action</th><th scope="col">Keys</th></tr></thead><tbody>' + rows + "</tbody></table>" +
      '<button type="button" class="palette__link" id="palette-all">Show all commands</button>';
    byId("palette-all").addEventListener("click", function () { setMode("commands"); });
  }

  // ---- dialog ---------------------------------------------------------------
  function build() {
    if (root) return;
    root = document.createElement("div");
    root.className = "palette-backdrop";
    root.id = "palette";
    root.hidden = true;
    root.innerHTML =
      '<div class="palette" role="dialog" aria-modal="true" aria-labelledby="palette-title">' +
        '<h2 class="visually-hidden" id="palette-title">Commands</h2>' +
        '<div class="palette__search" id="palette-search">' +
          '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="7"></circle><path d="M20 20l-3.5-3.5"></path></svg>' +
          '<input type="text" id="palette-input" role="combobox" aria-expanded="true" aria-controls="palette-list" aria-autocomplete="list" ' +
            'aria-label="Search commands" placeholder="Type a command or a language" autocomplete="off" spellcheck="false">' +
          "<kbd>Esc</kbd>" +
        "</div>" +
        '<div class="palette__head" id="palette-shortcuts-head" hidden>' +
          '<span class="palette__heading">Keyboard shortcuts</span>' +
          '<button type="button" class="palette__close" id="palette-close" aria-label="Close"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12 M18 6L6 18"></path></svg></button>' +
        "</div>" +
        '<div class="palette__list" id="palette-list" role="listbox" aria-label="Commands"></div>' +
        '<p class="palette__empty" id="palette-empty" hidden>No matching commands.</p>' +
        '<div class="palette__shortcuts" id="palette-shortcuts" hidden></div>' +
        '<div class="palette__foot" id="palette-foot" aria-hidden="true"><span><kbd>↑</kbd><kbd>↓</kbd> move</span><span><kbd>↵</kbd> run</span><span><kbd>Esc</kbd> close</span></div>' +
        '<p class="visually-hidden" id="palette-status" aria-live="polite"></p>' +
      "</div>";
    document.body.appendChild(root);
    input = byId("palette-input");
    listEl = byId("palette-list");
    emptyEl = byId("palette-empty");
    statusEl = byId("palette-status");
    titleEl = byId("palette-title");
    searchRow = byId("palette-search");
    shortcutsEl = byId("palette-shortcuts");
    footEl = byId("palette-foot");

    input.addEventListener("input", render);
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); setActive(active + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setActive(active - 1); }
      else if (e.key === "PageDown") { e.preventDefault(); setActive(Math.min(active + 8, shown.length - 1)); }
      else if (e.key === "PageUp") { e.preventDefault(); setActive(Math.max(active - 8, 0)); }
      else if (e.key === "Enter") { e.preventDefault(); runItem(shown[active]); }
    });
    // keep focus in the search box while the list is clicked
    listEl.addEventListener("mousedown", function (e) { e.preventDefault(); });
    listEl.addEventListener("click", function (e) {
      var opt = e.target.closest && e.target.closest(".palette__item");
      if (opt) runItem(shown[+opt.getAttribute("data-i")]);
    });
    listEl.addEventListener("mousemove", function (e) {
      var opt = e.target.closest && e.target.closest(".palette__item");
      if (opt && +opt.getAttribute("data-i") !== active) setActive(+opt.getAttribute("data-i"));
    });
    root.addEventListener("mousedown", function (e) { if (e.target === root) close(true); });
    byId("palette-close").addEventListener("click", function () { close(true); });
  }

  function setMode(m) {
    mode = m;
    var sc = m === "shortcuts";
    searchRow.hidden = sc;
    byId("palette-shortcuts-head").hidden = !sc;
    shortcutsEl.hidden = !sc;
    footEl.hidden = sc;
    titleEl.textContent = sc ? "Keyboard shortcuts" : "Commands";
    if (sc) {
      listEl.hidden = true;
      emptyEl.hidden = true;
      statusEl.textContent = "";
      renderShortcuts();
      byId("palette-close").focus();
    } else {
      items = buildCommands();
      input.value = "";
      render();
      input.focus();
    }
  }

  function isOpen() { return !!(root && !root.hidden); }

  function open(m) {
    build();
    if (!isOpen()) {
      var a = document.activeElement;
      returnFocus = a && a !== document.body ? a : null;
      root.hidden = false;
      document.body.classList.add("palette-open");
    }
    setMode(m || "commands");
  }

  function close(restore) {
    if (!isOpen()) return;
    root.hidden = true;
    document.body.classList.remove("palette-open");
    var r = returnFocus;
    returnFocus = null;
    if (restore && r && document.contains(r)) {
      if (r.closest && r.closest(".ace_editor") && editorInstance()) editorInstance().focus();
      else { try { r.focus({ preventScroll: true }); } catch (e) { r.focus(); } }
    }
  }

  function trapTab(e) {
    var focusables = Array.prototype.filter.call(
      root.querySelectorAll("input, button, [tabindex]:not([tabindex='-1'])"),
      function (el) { return el.offsetParent !== null; });
    if (!focusables.length) { e.preventDefault(); return; }
    var first = focusables[0], last = focusables[focusables.length - 1];
    if (focusables.length === 1 || (e.shiftKey && document.activeElement === first) || (!e.shiftKey && document.activeElement === last)) {
      e.preventDefault();
      (e.shiftKey ? last : first).focus();
    }
  }

  // Window capture runs before the editor, the terminal and the page's own
  // document handlers, so these keys reach the palette first.
  window.addEventListener("keydown", function (e) {
    var mod = MAC ? e.metaKey : e.ctrlKey;
    var isP = e.code === "KeyP" || e.key === "p" || e.key === "P";
    var isK = e.code === "KeyK" || e.key === "k" || e.key === "K";

    if (isOpen()) {
      if (e.key === "Escape" || (mod && !e.altKey && ((e.shiftKey && isP) || (!e.shiftKey && isK)))) {
        e.preventDefault();
        e.stopPropagation();
        close(true);
        return;
      }
      if (e.key === "Tab") trapTab(e);
      return;
    }
    if (e.defaultPrevented || e.isComposing) return;

    if (mod && e.shiftKey && !e.altKey && isP) {
      e.preventDefault();
      e.stopPropagation();
      open("commands");
      return;
    }
    if (mod && !e.shiftKey && !e.altKey && isK && !inCodeArea(e.target)) {
      e.preventDefault();
      e.stopPropagation();
      open("commands");
      return;
    }
    if (e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && e.code === "Backquote" && hasWorkspace()) {
      e.preventDefault();
      e.stopPropagation();
      if (inTerminal(e.target)) focusEditor(); else focusTerminal();
      return;
    }
    if (e.key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey && !isTextField(e.target) && !inCodeArea(e.target)) {
      e.preventDefault();
      open("shortcuts");
    }
  }, true);

  // The "Commands" button in the app bar
  function initButton() {
    var b = byId("palette-button");
    if (!b) return;
    var k = b.querySelector("kbd");
    if (k) k.textContent = MAC ? "⌘K" : "Ctrl K";
    b.setAttribute("aria-keyshortcuts", MAC ? "Meta+K Meta+Shift+P" : "Control+K Control+Shift+P");
    b.addEventListener("click", function () { open("commands"); });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initButton);
  else initButton();

  window.OpenreplPalette = { open: open, close: close, isOpen: isOpen };
})();
