// Workspace controls (T5): Run menu, shortcuts, Genie button, Files panel and editor status.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.closeRunMenu = closeRunMenu;
window.toggleRunMenu = toggleRunMenu;
window.setFilesPanel = setFilesPanel;
window.attachEditorStatus = attachEditorStatus;

// ---------------------------------------------------------------------------
// Workspace controls (T5): Run menu, shortcuts, Genie button, files panel,
// editor status bar.
// ---------------------------------------------------------------------------
window.IS_MAC = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

function closeRunMenu() {
  var menu = document.getElementById("run-menu");
  var btn = document.getElementById("run-menu-button");
  if (menu) menu.hidden = true;
  if (btn) btn.setAttribute("aria-expanded", "false");
}

function toggleRunMenu() {
  var menu = document.getElementById("run-menu");
  var btn = document.getElementById("run-menu-button");
  if (!menu || !btn) return;
  var open = menu.hidden;
  menu.hidden = !open;
  btn.setAttribute("aria-expanded", open ? "true" : "false");
  if (open) {
    var first = menu.querySelector(".run-menu__item");
    if (first) first.focus();
  }
}

function setFilesPanel(open) {
  document.body.classList.toggle("files-collapsed", !open);
  var t = document.getElementById("files-toggle");
  if (t) t.setAttribute("aria-expanded", open ? "true" : "false");
  try { localStorage.setItem("files-panel", open ? "open" : "closed"); } catch (e) {}
  setTimeout(function () { window.dispatchEvent(new Event("resize")); }, 60);
}

function attachEditorStatus() {
  var tries = 0;
  var timer = setInterval(function () {
    var el = document.getElementById("editor");
    var ed = el && el.env && el.env.editor;
    if (!ed && ++tries < 60) return;
    clearInterval(timer);
    if (!ed) return;
    var pos = document.getElementById("editor-cursor");
    var update = function () {
      var c = ed.getCursorPosition();
      if (pos) pos.textContent = "Ln " + (c.row + 1) + ", Col " + (c.column + 1);
    };
    ed.selection.on("changeCursor", update);
    update();
    updateEditorFileChip();
  }, 500);
}

$(function () {
  // shortcut labels match the platform
  $("[data-shortcut=run]").text(IS_MAC ? "⌘↵" : "Ctrl ↵");
  $("[data-shortcut=debug]").text(IS_MAC ? "⇧⌘↵" : "Shift Ctrl ↵");
  $("#play-button").attr("title", "Run the editor code (" + (IS_MAC ? "⌘↵" : "Ctrl+Enter") + ")");

  $("#run-menu-button").on("click", function (e) { e.stopPropagation(); toggleRunMenu(); });
  $("#run-menu .run-menu__item").on("click", closeRunMenu);
  $(document).on("click", function (e) {
    if (!$(e.target).closest(".run-split").length) closeRunMenu();
  });

  // The app bar button opens and closes the docked Genie panel.
  $("#genie-button").on("click", function () {
    if (!window.ChatWidget) return;
    if (window.ChatWidget.toggle) window.ChatWidget.toggle({ target: this });
    else window.ChatWidget.open({ target: this });
  });
  // keep the button's pressed state in step with the panel
  if (window.MutationObserver) {
    new MutationObserver(function () {
      var open = document.body.classList.contains("genie-open");
      $("#genie-button").attr("aria-pressed", open ? "true" : "false");
    }).observe(document.body, { attributes: true, attributeFilter: ["class"] });
  }

  $("#files-toggle").on("click", function () {
    setFilesPanel(document.body.classList.contains("files-collapsed"));
  });
  $("#files-collapse").on("click", function () { setFilesPanel(false); });
  var saved = null;
  try { saved = localStorage.getItem("files-panel"); } catch (e) {}
  if (saved === "closed" || (saved === null && window.innerWidth < 1100)) setFilesPanel(false);

  $("#download-workspace-button").on("click", function () {
    var tree = $("#file-browser").jstree && $("#file-browser").jstree(true);
    var root = homedir || (tree && tree.get_node("#").children[0]);
    if (!root) return;
    var link = document.createElement("a");
    link.href = preprocessurl("/ws_filebrowser?q=zip&filepath=" + root);
    link.click();
  });

  // Keyboard: Run, Debug, and Esc to leave the maximized workspace.
  document.addEventListener("keydown", function (e) {
    var inWorkspace = e.target && e.target.closest && e.target.closest("#workspace");
    var mod = IS_MAC ? e.metaKey : e.ctrlKey;
    if (e.key === "Enter" && mod && inWorkspace) {
      e.preventDefault();
      e.stopPropagation();
      if (e.shiftKey) RunandDebug(); else CompileandRun();
      return;
    }
    if (e.key === "Escape") {
      var menu = document.getElementById("run-menu");
      if (menu && !menu.hidden) { closeRunMenu(); return; }
      var sharePop = document.getElementById("myDropdown");
      if (sharePop && sharePop.classList.contains("show")) {
        sharePop.classList.remove("show");
        if (e.target && e.target.closest && e.target.closest(".share-wrap")) document.getElementById("share-btn-1").focus();
        return;
      }
      var inPane = e.target && e.target.closest && e.target.closest("#terminal-div, #editor");
      if (document.body.classList.contains("ide-maximized") && !inPane) ToggleFunction();
    }
  }, true);

  attachEditorStatus();
});

export {};  // an ES module: strict mode, bundled by webpack
