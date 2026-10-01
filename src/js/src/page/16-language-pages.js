// Language pages (T12): the address, title and heading follow the language picker.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.langPageForRepl = langPageForRepl;

// ---------------------------------------------------------------------------
// Language pages (T12). On /python, /cpp and the rest, switching language
// moves the address to that language's page and updates the title and hero
// in place, so the URL always opens what is on screen. Variants without a
// page of their own (IPython, Python 2.7, Go-yaegi) keep the current address.
// ---------------------------------------------------------------------------
function langPageForRepl(repl) {
  var page = window.OPENREPL_PAGE || {};
  var pages = page.pages || [];
  for (var i = 0; i < pages.length; i++) if (pages[i].repl === repl) return pages[i];
  return null;
}
$(function () {
  var page = window.OPENREPL_PAGE;
  if (!page || !page.slug || !window.history || !history.replaceState) return;
  $("#optionlist").on("change", function () {
    var p = langPageForRepl(this.value);
    if (!p || ("/" + p.slug) === location.pathname) return;
    history.replaceState(null, "", "/" + p.slug + location.hash);
    document.title = p.title;
    $("#hero-eyebrow").text(p.eyebrow);
    $("#hero-heading").text(p.heading);
    $("#hero-heading-color").text(p.headingColor);
    $("#hero-blurb").text(p.blurb);
    $('link[rel="canonical"]').attr("href", location.origin + "/" + p.slug);
  });
});

export {};  // an ES module: strict mode, bundled by webpack
