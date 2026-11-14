// The language of the last visit (page/32-remember-language.js). The page opens in the picker's first choice (C) on "/", so a
// reload put the visitor back in C whatever they had been using. The language chosen last is kept in this browser and chosen
// again when the page opens. A file that is open wins: choosing a file sets the language by its extension, after this.
//
// Only on the home address: a language page (/python) says its own language, a share link follows the owner, and a code link
// (?s=) brings its own.

var KEY = "openrepl-language";

function read() {
  try { return localStorage.getItem(KEY) || ""; } catch (e) { return ""; }
}
function write(v) {
  try { if (v) localStorage.setItem(KEY, v); } catch (e) { /* not essential */ }
}

function applies() {
  if (location.pathname !== "/" && location.pathname !== "/index.html") return false;
  if (typeof isMaster === "function" && !isMaster()) return false; // a collaborator has the owner's language
  if (new URLSearchParams(location.search).get("s")) return false; // a code link has its own
  return true;
}

$(function () {
  var sel = document.getElementById("optionlist");
  if (!sel || !applies()) return;
  var saved = read();
  var option = saved ? Array.prototype.filter.call(sel.options, function (o) { return o.value === saved && !o.disabled && o.getAttribute("data-web") !== "1"; })[0] : null;
  if (option && sel.value !== saved) {
    sel.value = saved;
    sel.dispatchEvent(new Event("change"));
    if (typeof window.syncLangChips === "function") window.syncLangChips();
  }
  // from now on, what is chosen is what is kept (the HTML and CSS entries are not languages of the terminal)
  $(sel).on("change", function () {
    if (window.__languageFromSession) return; // the session's language, set by somebody else's change
    var chosen = sel.options[sel.selectedIndex];
    if (chosen && chosen.getAttribute("data-web") !== "1") write(sel.value);
  });
});

export {};
