// Files the editor does not take, and what to do with them.
//
// The editor is one document shared through Firebase and has a hard limit on what it can carry (07-file-browser.js,
// MAX_EDITOR_TEXT). Text over that limit opens here instead: a read-only view, in pieces, that only the person who
// chose the file sees. Nothing in it is shared, saved or written to the server; it reads the file a piece at a time
// (q=load&offset=&length=, package fileload), so a big file is never read whole. A binary file is not opened at all
// (the editor would damage it when saving): the notice offers Download and the terminal.
// Also here: the two actions of those notices, Download (the file as a zip, like the file's menu) and "Terminal"
// (a command typed into the active terminal, not run).

var CHUNK = 1024 * 1024;
var el = null;
var view = null;   // { path, size, offset, length }
var seq = 0;       // the newest read; an older answer is dropped

function say(text, opts) { if (window.notify) window.notify(text, opts || {}); }
function nameOf(path) { return String(path).split("/").pop(); }

function shellQuote(path) { return "'" + String(path).replace(/'/g, "'\\''") + "'"; }

function download(path) {
  var link = document.createElement("a");
  link.href = preprocessurl("/ws_filebrowser?q=zip&filepath=" + path);
  link.click();
}

// Types a command into the active terminal without running it; the person presses Enter.
function typeInTerminal(command) {
  if (typeof window.ShowTerminal === "function") window.ShowTerminal(); // a hidden terminal is shown first
  var tab = document.querySelector("#terminal-tabs .tab.active");
  var gt = tab && tab.gottyterm;
  var term = gt && gt.term;
  if (term && typeof term.typeInput === "function" && term.typeInput(command)) {
    if (typeof term.focus === "function") term.focus();
    say("Typed in the terminal. Press Enter to run it.", { type: "info", timeout: 4000 });
  } else {
    say("The terminal is not ready. Run this yourself: " + command, { type: "info", timeout: 12000 });
  }
}

function lessCommand(path) { return "less " + shellQuote(path); }
function hexCommand(path) { return "xxd " + shellQuote(path) + " | less"; }

function mb(bytes) { return (bytes / 1048576).toFixed(1) + " MB"; }

function notOpenedBinary(path) {
  say(nameOf(path) + " is a binary file. The editor cannot show or save it safely.", {
    type: "info", title: "Not opened", timeout: 15000,
    actions: [
      { label: "Download", onClick: function () { download(path); } },
      { label: "Terminal", onClick: function () { typeInTerminal(hexCommand(path)); } }
    ]
  });
}

// ---- the viewer ------------------------------------------------------------------------------------------------------

function button(label, name, onClick) {
  var b = document.createElement("button");
  b.type = "button";
  b.className = "lv__btn";
  b.dataset.lv = name;
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

function build() {
  if (el) return el;
  var body = document.querySelector(".editor-body");
  if (!body) return null;
  el = document.createElement("div");
  el.id = "large-viewer";
  el.className = "large-viewer";
  el.hidden = true;
  el.setAttribute("role", "region");
  el.setAttribute("aria-label", "Read-only view of a large file");
  var bar = document.createElement("div");
  bar.className = "lv__bar";
  var name = document.createElement("span"); name.className = "lv__name";
  var range = document.createElement("span"); range.className = "lv__range";
  var spacer = document.createElement("span"); spacer.className = "lv__spacer";
  bar.appendChild(name); bar.appendChild(range); bar.appendChild(spacer);
  bar.appendChild(button("Start", "start", function () { go(0); }));
  bar.appendChild(button("Previous", "prev", function () { go(view.offset - CHUNK); }));
  bar.appendChild(button("Next", "next", function () { go(view.offset + CHUNK); }));
  bar.appendChild(button("End", "end", function () { go(view.size - CHUNK); }));
  bar.appendChild(button("Terminal", "terminal", function () { typeInTerminal(lessCommand(view.path)); }));
  bar.appendChild(button("Download", "download", function () { download(view.path); }));
  bar.appendChild(button("Close", "close", close));
  var note = document.createElement("div");
  note.className = "lv__note";
  note.textContent = "Read-only. Only you see this: nothing here is shared, saved or changed. It is shown in pieces.";
  var text = document.createElement("pre");
  text.className = "lv__text";
  text.tabIndex = 0;
  el.appendChild(bar); el.appendChild(note); el.appendChild(text);
  el.addEventListener("keydown", function (e) { if (e.key === "Escape") { e.preventDefault(); close(); } });
  body.appendChild(el);
  return el;
}

function decode(base64) {
  var raw = atob(base64);
  var bytes = new Uint8Array(raw.length);
  for (var i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

function hasNul(bytes) {
  for (var i = 0; i < bytes.length; i++) if (bytes[i] === 0) return true;
  return false;
}

function draw(bytes) {
  var text = el.querySelector(".lv__text");
  // a piece is cut at a byte, so a character at an edge can be cut: it shows as a replacement mark
  text.textContent = new TextDecoder("utf-8").decode(bytes);
  text.scrollTop = 0;
  el.querySelector(".lv__name").textContent = nameOf(view.path);
  el.querySelector(".lv__name").title = view.path;
  var from = view.offset, to = view.offset + view.length;
  el.querySelector(".lv__range").textContent = (view.length ? from.toLocaleString() + " to " + to.toLocaleString() : "no bytes") + " of " + view.size.toLocaleString() + " bytes (" + mb(view.size) + ")";
  var set = function (n, off) { el.querySelector('[data-lv="' + n + '"]').disabled = off; };
  set("start", from <= 0);
  set("prev", from <= 0);
  set("next", to >= view.size);
  set("end", to >= view.size);
}

// Reads the piece at `offset` (clamped to the file). first: this is the first piece, so a binary file is turned away.
function go(offset, first) {
  if (!view && !first) return;
  var path = first ? first.path : view.path;
  var size = first ? first.size : view.size;
  offset = Math.max(0, Math.min(offset, Math.max(0, size - 1)));
  var mine = ++seq;
  $.ajax({
    url: preprocessurl("/ws_filebrowser?q=load&filepath=" + path + "&offset=" + offset + "&length=" + CHUNK),
    method: "GET"
  }).done(function (data, status, xhr) {
    if (mine !== seq) return; // a newer choice was made
    var bytes = decode(data);
    if (first && hasNul(bytes)) { close(); notOpenedBinary(path); return; }
    var total = parseInt(xhr.getResponseHeader("X-File-Size"), 10);
    view = { path: path, size: isFinite(total) ? total : size, offset: parseInt(xhr.getResponseHeader("X-Chunk-Offset"), 10) || 0, length: bytes.length };
    el.hidden = false;
    draw(bytes);
    if (first) { var next = el.querySelector('[data-lv="next"]'); (next && !next.disabled ? next : el.querySelector('[data-lv="close"]')).focus(); }
  }).fail(function (xhr) {
    if (mine !== seq) return;
    close();
    say("Couldn't read " + nameOf(path) + ". " + (xhr.responseText || "Please try again."), { type: "error" });
  });
}

// Opens a file the editor does not take. size: its size in bytes, when known.
function open(path, size) {
  if (!build()) return;
  view = null;
  go(0, { path: String(path), size: size || CHUNK });
}

function close() {
  seq++;
  view = null;
  if (el) el.hidden = true;
}

window.LargeFileViewer = {
  open: open,
  close: close,
  isOpen: function () { return !!view; },
  notOpenedBinary: notOpenedBinary,
  typeInTerminal: typeInTerminal,
  lessCommand: lessCommand,
  hexCommand: hexCommand,
  shellQuote: shellQuote
};
