/*
 * Light and dark theme (T11). Sets <html data-theme="light|dark"> before the
 * page paints. It follows the system setting until someone presses a
 * [data-theme-toggle] button; that choice is remembered in localStorage.
 */
(function () {
  var KEY = "theme";
  var mq = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;

  // What was chosen on this page, for a browser that does not let the page keep
  // anything (a private window, blocked site data): without it the button would
  // set the choice, fail to remember it, and read the system's back at once.
  var memory = null;
  function saved() {
    try {
      var s = localStorage.getItem(KEY);
      if (s === "dark" || s === "light") return s;
    } catch (e) {}
    return memory;
  }
  function resolve() {
    var s = saved();
    if (s === "dark" || s === "light") return s;
    return mq && mq.matches ? "dark" : "light";
  }
  function updateButtons(theme) {
    var buttons = document.querySelectorAll("[data-theme-toggle]");
    for (var i = 0; i < buttons.length; i++) {
      var label = theme === "dark" ? "Switch to light theme" : "Switch to dark theme";
      buttons[i].setAttribute("aria-label", label);
      buttons[i].setAttribute("title", label);
      buttons[i].setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
    }
  }
  function apply() {
    var theme = resolve();
    document.documentElement.setAttribute("data-theme", theme);
    updateButtons(theme);
  }

  window.toggleTheme = function () {
    var next = resolve() === "dark" ? "light" : "dark";
    memory = next;
    try { localStorage.setItem(KEY, next); } catch (e) {}
    apply();
    // On a phone the first screen (the header and the workspace) is dark in
    // both themes, so nothing there seems to change: say what happened.
    if (window.notify && window.matchMedia && window.matchMedia("(max-width: 600px)").matches) {
      window.notify((next === "dark" ? "Dark" : "Light") + " theme on", { type: "info", timeout: 1800 });
    }
    return next;
  };

  apply();
  if (mq) {
    var onChange = function () { if (!saved()) apply(); };
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else if (mq.addListener) mq.addListener(onChange);
  }
  document.addEventListener("DOMContentLoaded", function () {
    apply();
    document.addEventListener("click", function (e) {
      var b = e.target && e.target.closest && e.target.closest("[data-theme-toggle]");
      if (b) {
        e.preventDefault();
        window.toggleTheme();
      }
    });
  });
})();
