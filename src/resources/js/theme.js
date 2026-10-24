/*
 * Light, dark and system theme (T11). Sets <html data-theme="light|dark"> before
 * the page paints. There are three choices:
 *
 *   system  follow the device's light or dark setting (the default, nothing saved)
 *   light   always light
 *   dark    always dark
 *
 * Every [data-theme-toggle] button in a nav becomes the theme menu: it opens a
 * small list with the three choices and says what the device is using. The
 * choice is remembered in localStorage ("theme": "light" or "dark"; System has
 * nothing saved). <html data-theme-choice> holds the choice, data-theme what it
 * resolves to; the "themechange" event on window says when either changed.
 */
(function () {
  var KEY = "theme";
  var mq = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;

  // What was chosen on this page, for a browser that does not let the page keep
  // anything (a private window, blocked site data): without it the menu would
  // set the choice, fail to remember it, and read the system's back at once.
  var memory = null;
  var hasMemory = false;
  function choice() {
    try {
      var s = localStorage.getItem(KEY);
      return s === "dark" || s === "light" ? s : "system";
    } catch (e) {
      // storage is blocked: what was chosen on this page is all there is
      return hasMemory && (memory === "dark" || memory === "light") ? memory : "system";
    }
  }
  function systemTheme() {
    return mq && mq.matches ? "dark" : "light";
  }
  function resolve() {
    var c = choice();
    return c === "system" ? systemTheme() : c;
  }

  var ICONS = {
    light: "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
    dark: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z",
    system: "M3 5h18v11H3zM8 20h8M12 16v4"
  };
  var NAMES = { light: "Light", dark: "Dark", system: "System" };
  function svg(path, size) {
    return '<svg width="' + size + '" height="' + size + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="' + path + '"></path></svg>';
  }

  function updateButtons() {
    var c = choice();
    var buttons = document.querySelectorAll("[data-theme-toggle]");
    for (var i = 0; i < buttons.length; i++) {
      var b = buttons[i];
      b.setAttribute("data-theme-menu", "");
      b.setAttribute("aria-haspopup", "menu");
      if (!b.hasAttribute("aria-expanded")) b.setAttribute("aria-expanded", "false");
      var label = "Theme: " + NAMES[c] + (c === "system" ? " (" + NAMES[systemTheme()] + ")" : "") + ". Change theme";
      b.setAttribute("aria-label", label);
      b.setAttribute("title", label);
      b.removeAttribute("aria-pressed");
      b.innerHTML = svg(ICONS[c], 18) + svg("M6 9l6 6 6-6", 11);
    }
  }
  function apply() {
    var theme = resolve();
    var root = document.documentElement;
    var changed = root.getAttribute("data-theme") !== theme || root.getAttribute("data-theme-choice") !== choice();
    root.setAttribute("data-theme", theme);
    root.setAttribute("data-theme-choice", choice());
    updateButtons();
    refreshMenu();
    if (changed) {
      try { window.dispatchEvent(new CustomEvent("themechange", { detail: { choice: choice(), theme: theme } })); } catch (e) {}
    }
  }

  window.getThemeChoice = choice;
  window.setThemeChoice = function (next) {
    next = next === "light" || next === "dark" ? next : "system";
    memory = next;
    hasMemory = true;
    try {
      if (next === "system") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, next);
    } catch (e) {}
    apply();
    // On a phone the first screen can look the same in both themes: say what happened.
    if (window.notify && window.matchMedia && window.matchMedia("(max-width: 600px)").matches) {
      window.notify(NAMES[next] + " theme on" + (next === "system" ? " (" + NAMES[systemTheme()] + ")" : ""), { type: "info", timeout: 1800 });
    }
    return next;
  };
  // kept for older callers: flips between light and dark
  window.toggleTheme = function () {
    return window.setThemeChoice(resolve() === "dark" ? "light" : "dark");
  };

  // ---- the menu -------------------------------------------------------------------
  var menu = null;
  var opener = null;
  var STYLE = [
    ".theme-menu{position:fixed;z-index:100050;box-sizing:border-box;width:236px;padding:6px;font:400 14px/1.4 var(--font-ui,'IBM Plex Sans',system-ui,sans-serif);color:#16151A;background:#FFFFFF;border:1px solid #E4E1DA;border-radius:12px;box-shadow:0 16px 36px -10px rgba(22,21,26,.35)}",
    "[data-theme=dark] .theme-menu{color:#F2F0EC;background:#2F2E35;border-color:rgba(255,255,255,.14);box-shadow:0 16px 36px -10px rgba(0,0,0,.6)}",
    ".theme-menu[hidden]{display:none}",
    ".theme-menu__item{display:flex;align-items:center;gap:10px;width:100%;box-sizing:border-box;padding:9px 10px;margin:0;font:inherit;font-weight:500;text-align:left;color:inherit;background:transparent;border:0;border-radius:8px;cursor:pointer}",
    ".theme-menu__item:hover,.theme-menu__item:focus-visible{background:#F1EEE8;outline:0}",
    "[data-theme=dark] .theme-menu__item:hover,[data-theme=dark] .theme-menu__item:focus-visible{background:rgba(255,255,255,.1)}",
    ".theme-menu__item:focus-visible{box-shadow:inset 0 0 0 2px #B8323A}",
    "[data-theme=dark] .theme-menu__item:focus-visible{box-shadow:inset 0 0 0 2px #FF9C9D}",
    ".theme-menu__item[aria-checked=true]{background:#F1EEE8}",
    "[data-theme=dark] .theme-menu__item[aria-checked=true]{background:rgba(255,255,255,.08)}",
    ".theme-menu__name{flex:1}",
    ".theme-menu__check{width:16px;text-align:center;font-weight:700;color:#B8323A}",
    "[data-theme=dark] .theme-menu__check{color:#FF9C9D}",
    ".theme-menu__hint{margin:4px 6px 2px;padding-top:8px;font-size:12px;line-height:1.4;color:#5B5866;border-top:1px solid #E4E1DA}",
    "[data-theme=dark] .theme-menu__hint{color:#B9B7C4;border-top-color:rgba(255,255,255,.14)}",
    ".theme-toggle[data-theme-menu]{width:auto;min-width:40px;gap:4px;padding:0 8px}"
  ].join("\n");

  function ensureStyle() {
    if (document.getElementById("theme-menu-style") || !document.head) return;
    var s = document.createElement("style");
    s.id = "theme-menu-style";
    s.textContent = STYLE;
    document.head.appendChild(s);
  }
  function hintText() {
    var c = choice();
    if (c === "system") return "Following your device: " + NAMES[systemTheme()] + ". Changes when your device does.";
    return "Always " + NAMES[c].toLowerCase() + ", whatever your device uses.";
  }
  function refreshMenu() {
    if (!menu) return;
    var c = choice();
    var items = menu.querySelectorAll("[data-theme-choice-item]");
    for (var i = 0; i < items.length; i++) {
      var on = items[i].getAttribute("data-theme-choice-item") === c;
      items[i].setAttribute("aria-checked", on ? "true" : "false");
      items[i].querySelector(".theme-menu__check").textContent = on ? "✓" : "";
    }
    var hint = menu.querySelector(".theme-menu__hint");
    if (hint) hint.textContent = hintText();
  }
  function buildMenu() {
    ensureStyle();
    menu = document.createElement("div");
    menu.className = "theme-menu";
    menu.setAttribute("role", "menu");
    menu.setAttribute("aria-label", "Theme");
    menu.hidden = true;
    ["light", "dark", "system"].forEach(function (name) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "theme-menu__item";
      b.setAttribute("role", "menuitemradio");
      b.setAttribute("data-theme-choice-item", name);
      b.innerHTML = svg(ICONS[name], 16) + '<span class="theme-menu__name">' + NAMES[name] + '</span><span class="theme-menu__check" aria-hidden="true"></span>';
      menu.appendChild(b);
    });
    var hint = document.createElement("p");
    hint.className = "theme-menu__hint";
    menu.appendChild(hint);
    document.body.appendChild(menu);
    refreshMenu();
  }
  function placeMenu() {
    var r = opener.getBoundingClientRect();
    var w = 236;
    var left = Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w));
    menu.style.left = left + "px";
    menu.style.top = Math.round(r.bottom + 6) + "px";
  }
  function openMenu(button) {
    if (!menu) buildMenu();
    opener = button;
    refreshMenu();
    menu.hidden = false;
    placeMenu();
    button.setAttribute("aria-expanded", "true");
    var current = menu.querySelector('[aria-checked="true"]') || menu.querySelector(".theme-menu__item");
    if (current) current.focus();
  }
  function closeMenu(restoreFocus) {
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    var buttons = document.querySelectorAll("[data-theme-toggle]");
    for (var i = 0; i < buttons.length; i++) buttons[i].setAttribute("aria-expanded", "false");
    if (restoreFocus && opener && opener.focus) opener.focus();
  }
  window.openThemeMenu = function (button) { openMenu(button || document.querySelector("[data-theme-toggle]")); };

  function moveFocus(step) {
    var items = [].slice.call(menu.querySelectorAll(".theme-menu__item"));
    var at = items.indexOf(document.activeElement);
    var next = items[(at + step + items.length) % items.length];
    if (next) next.focus();
  }

  apply();
  if (mq) {
    // the device's setting changed: matters only while the choice is System
    var onChange = function () { if (choice() === "system") apply(); };
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else if (mq.addListener) mq.addListener(onChange);
  }
  // another tab changed it
  window.addEventListener("storage", function (e) { if (e.key === KEY) apply(); });

  document.addEventListener("DOMContentLoaded", function () {
    apply();
    document.addEventListener("click", function (e) {
      var t = e.target;
      var b = t && t.closest && t.closest("[data-theme-toggle]");
      if (b) {
        e.preventDefault();
        if (menu && !menu.hidden && opener === b) closeMenu(false);
        else openMenu(b);
        return;
      }
      var item = t && t.closest && t.closest("[data-theme-choice-item]");
      if (item) {
        window.setThemeChoice(item.getAttribute("data-theme-choice-item"));
        closeMenu(true);
        return;
      }
      if (menu && !menu.hidden && !(t.closest && t.closest(".theme-menu"))) closeMenu(false);
    });
    document.addEventListener("keydown", function (e) {
      if (!menu || menu.hidden) return;
      if (e.key === "Escape") { e.preventDefault(); closeMenu(true); }
      else if (e.key === "ArrowDown") { e.preventDefault(); moveFocus(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); moveFocus(-1); }
      else if (e.key === "Tab") closeMenu(false);
    });
    window.addEventListener("resize", function () { if (menu && !menu.hidden) placeMenu(); });
  });
})();
