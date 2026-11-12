// Files the editor does not take, and what to do with them.
//
// The editor is one document shared through Firebase and has a hard limit on what it can carry (07-file-browser.js,
// maxEditorText(), the admin's setting up to 16 MB). Text over that limit opens here instead: a read-only view, in pieces, that only the person who
// chose the file sees. Nothing in it is shared, saved or written to the server; it reads the file a piece at a time
// (q=load&offset=&length=, package fileload), so a big file is never read whole. A binary file opens here too, as a hex
// dump (offset, bytes, text), in smaller pieces: the editor would damage it when saving, and the shared editor cannot
// carry it, but looking at it is safe.
// Also here: Download (the file as a zip, like the file's menu) and "Terminal" (a command typed into the active
// terminal when it is a shell, else copied; never run).
import { hexDump } from "./lib-hexdump.mjs";

var CHUNK = 1024 * 1024;
var HEX_CHUNK = 256 * 1024; // a hex dump is five times the size of its bytes, so its pieces are smaller
var el = null;
var hexMode = false; // the file is binary: shown as a hex dump
var view = null;   // { path, size, offset, length }
var seq = 0;       // the newest read; an older answer is dropped

function chunk() { return hexMode ? HEX_CHUNK : CHUNK; }
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

// The terminal image has no less and no xxd, but it has more and od (a hex dump with the text beside it).
function moreCommand(path) { return "more " + shellQuote(path); }
function hexCommand(path) { return "od -A x -t x1z " + shellQuote(path) + " | more"; }

function mb(bytes) { return (bytes / 1048576).toFixed(1) + " MB"; }

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
  bar.appendChild(button("Previous", "prev", function () { go(view.offset - chunk()); }));
  bar.appendChild(button("Next", "next", function () { go(view.offset + chunk()); }));
  bar.appendChild(button("End", "end", function () { go(view.size - chunk()); }));
  bar.appendChild(button("Terminal", "terminal", function () { typeInTerminal(hexMode ? hexCommand(view.path) : moreCommand(view.path)); }));
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

function hasNul(bytes) {
  for (var i = 0; i < bytes.length; i++) if (bytes[i] === 0) return true;
  return false;
}

function draw(bytes) {
  var text = el.querySelector(".lv__text");
  // a piece is cut at a byte, so a character at an edge can be cut: it shows as a replacement mark
  text.textContent = hexMode ? hexDump(bytes, view.offset) : new TextDecoder("utf-8").decode(bytes);
  text.scrollTop = 0;
  el.querySelector(".lv__note").textContent = hexMode
    ? "Binary file, shown as hex: the offset, the bytes, then the text. Read-only. Only you see this: nothing here is shared, saved or changed."
    : "Read-only. Only you see this: nothing here is shared, saved or changed. It is shown in pieces.";
  el.querySelector(".lv__name").textContent = nameOf(view.path) + (hexMode ? " (hex)" : "");
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
  if (hexMode) offset -= offset % 16; // a line of the dump starts at a multiple of 16
  var mine = ++seq;
  var url = "/ws_filebrowser?q=load&enc=raw&filepath=" + encodeURIComponent(path) + "&offset=" + offset + "&length=" + (first ? CHUNK : chunk());
  fetch(preprocessurl(url), { credentials: "same-origin" })
    .then(function (res) {
      if (!res.ok) return res.text().then(function (t) { throw new Error(t || res.statusText); });
      return res.arrayBuffer().then(function (buf) { return { bytes: new Uint8Array(buf), headers: res.headers }; });
    })
    .then(function (got) {
      if (mine !== seq) return; // a newer choice was made
      var bytes = got.bytes;
      // the first piece decides how the file is shown: a zero byte in it means binary, shown as hex (a smaller piece of it)
      if (first) { hexMode = hasNul(bytes); if (hexMode) bytes = bytes.subarray(0, HEX_CHUNK); }
      var total = parseInt(got.headers.get("X-File-Size"), 10);
      view = { path: path, size: isFinite(total) ? total : size, offset: parseInt(got.headers.get("X-Chunk-Offset"), 10) || 0, length: bytes.length };
      el.hidden = false;
      draw(bytes);
      if (first) { var next = el.querySelector('[data-lv="next"]'); (next && !next.disabled ? next : el.querySelector('[data-lv="close"]')).focus(); }
    }, function (error) {
      if (mine !== seq) return;
      close();
      say("Couldn't read " + nameOf(path) + ". " + (error && error.message ? error.message : "Please try again."), { type: "error" });
    });
}

// Opens a file the editor does not take. size: its size in bytes, when known.
function open(path, size) {
  if (!build()) return;
  view = null;
  current = String(path);
  hexMode = false;
  go(0, { path: String(path), size: size || CHUNK });
}

// The file the viewer shows or is about to show (it is known before the first piece has come)
var current = null;

// A file is chosen in the tree: the viewer closes, unless it is the file the viewer is about
function closeUnlessFor(path) {
  if (current && String(path) === current) return;
  close();
}

function close() {
  seq++;
  view = null;
  current = null;
  if (el) el.hidden = true;
}

window.LargeFileViewer = {
  open: open,
  close: close,
  closeUnlessFor: closeUnlessFor,
  isOpen: function () { return !!view; },
  typeInTerminal: typeInTerminal,
  moreCommand: moreCommand,
  hexCommand: hexCommand,
  shellQuote: shellQuote
};
