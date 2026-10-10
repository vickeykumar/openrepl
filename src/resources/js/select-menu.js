/*
 * A menu of our own for the IDE's dropdowns (language, editor syntax mode,
 * editor theme), in place of the list the browser draws: it follows the IDE's
 * colours, has a search box when the list is long, shows a file extension next
 * to a language and a colour next to a theme, and is the same on every browser.
 *
 * The <select> stays in the page and stays the source of truth: the editor, the
 * tour, the home page chips and the command palette all read it, set it and
 * listen to its "change". The menu only draws it and, when a visitor chooses,
 * sets it and fires "input" and "change" as the browser would. For the code that
 * sets it from outside (select.value = ..., jQuery's .val()) the instance has its
 * own value and selectedIndex that also redraw the button, and focus() and
 * showPicker() open the menu, which is what the palette and the home page's
 * "All 19" chip call.
 *
 *   <select data-select-menu> ... </select>      enhanced when the page loads
 *   <option data-ext=".py">, <option data-swatch="#272822">   a badge, a colour
 *
 * A touch screen keeps the browser's own picker, which is the better one there.
 * Without this script, or if it fails, the page has its plain selects.
 */
(function () {
  "use strict";

  var SEARCH_MIN = 8; // options in a list before it gets a search box

  // ---- the parts that need no page --------------------------------------------------

  // The options of a select as a flat list: {value, label, badge, swatch, group, disabled, index}.
  function itemsOf(select) {
    var out = [];
    function add(option, group) {
      out.push({
        value: option.value,
        label: String(option.text != null ? option.text : option.textContent || "").trim(),
        badge: option.getAttribute("data-ext") || "",
        swatch: option.getAttribute("data-swatch") || "",
        group: group || "",
        disabled: !!option.disabled,
        index: out.length
      });
    }
    Array.prototype.forEach.call(select.children, function (el) {
      var tag = String(el.tagName).toUpperCase();
      if (tag === "OPTGROUP") {
        Array.prototype.forEach.call(el.children, function (o) { add(o, el.getAttribute("label") || ""); });
      } else if (tag === "OPTION") {
        add(el, "");
      }
    });
    return out;
  }

  // The items that match what was typed: every word of it is somewhere in the
  // name, the extension or the group ("py" finds Python and IPython3, ".js" the two
  // JavaScripts, "dark" the dark themes).
  function filterItems(items, query) {
    var words = String(query || "").toLowerCase().split(/\s+/).filter(Boolean);
    if (!words.length) return items.slice();
    return items.filter(function (it) {
      var hay = (it.label + " " + it.badge + " " + it.group).toLowerCase();
      return words.every(function (w) { return hay.indexOf(w) >= 0; });
    });
  }

  // Where the menu goes: under its button, as wide as the button at least, inside the
  // window; above the button when there is no room under it (the status bar is at
  // the bottom of the IDE). anchor is the button's {left, top, right, bottom} and
  // view the window's {width, height}.
  function placeMenu(anchor, menuWidth, menuHeight, view, gap, margin) {
    gap = gap === undefined ? 4 : gap;
    margin = margin === undefined ? 8 : margin;
    var width = Math.min(Math.max(menuWidth, anchor.right - anchor.left), view.width - 2 * margin);
    var left = Math.max(margin, Math.min(anchor.left, view.width - width - margin));
    var below = view.height - anchor.bottom - gap - margin;
    var above = anchor.top - gap - margin;
    var up = below < menuHeight && above > below;
    var room = Math.max(120, up ? above : below);
    return {
      left: Math.round(left),
      width: Math.round(width),
      up: up,
      top: up ? null : Math.round(anchor.bottom + gap),
      bottom: up ? Math.round(view.height - anchor.top + gap) : null,
      maxHeight: Math.round(room)
    };
  }

  // The item a key moves to, skipping any that are disabled. items is what is
  // shown, active the position now (-1: none).
  function moveActive(items, active, key, pageSize) {
    var n = items.length;
    if (!n) return -1;
    var enabled = function (i) { return i >= 0 && i < n && !items[i].disabled; };
    var step = function (from, dir) {
      for (var i = from; i >= 0 && i < n; i += dir) if (enabled(i)) return i;
      return -1;
    };
    var to;
    switch (key) {
      case "ArrowDown": to = step(active + 1, 1); break;
      case "ArrowUp": to = step(active < 0 ? n - 1 : active - 1, -1); break;
      case "Home": to = step(0, 1); break;
      case "End": to = step(n - 1, -1); break;
      case "PageDown": to = step(Math.min(n - 1, Math.max(0, active) + (pageSize || 8)), -1); break;
      case "PageUp": to = step(Math.max(0, active - (pageSize || 8)), 1); break;
      default: return active;
    }
    return to < 0 ? active : to;
  }

  // The first item whose name starts with what was typed (for a list with no search box).
  function typeAhead(items, typed, from) {
    typed = String(typed || "").toLowerCase();
    if (!typed) return -1;
    var n = items.length;
    for (var k = 0; k < n; k++) {
      var i = (from + (typed.length === 1 ? 1 : 0) + k + n) % n;
      if (!items[i].disabled && items[i].label.toLowerCase().indexOf(typed) === 0) return i;
    }
    return -1;
  }

  // ---- the menu ---------------------------------------------------------------------

  var CHEVRON = '<svg class="sm-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6"></path></svg>';
  var SEARCH_ICON = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="7"></circle><path d="M20 20l-3.5-3.5"></path></svg>';
  var CHECK = '<svg class="sm-check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12l5 5 9-10"></path></svg>';

  var openMenu = null; // the one menu that is open

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function enhance(select) {
    if (!select || select.__selectMenu) return select && select.__selectMenu;
    var wrap = select.parentNode;
    if (!wrap) return null;
    var id = select.id || "select-" + Math.random().toString(36).slice(2, 7);
    var labelEl = select.id ? document.querySelector('label[for="' + select.id + '"]') : null;
    var labelText = (labelEl && labelEl.textContent.trim()) || select.getAttribute("aria-label") || "Choose";
    if (labelEl && !labelEl.id) labelEl.id = id + "-label";

    var nativeValue = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value");
    var nativeIndex = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "selectedIndex");

    // the button
    var button = el("button", "sm-btn");
    button.type = "button";
    button.id = id + "-button";
    button.setAttribute("aria-haspopup", "listbox");
    button.setAttribute("aria-expanded", "false");
    var valueEl = el("span", "sm-value");
    valueEl.id = id + "-value";
    button.append(valueEl);
    button.insertAdjacentHTML("beforeend", CHEVRON);
    button.setAttribute("aria-labelledby", (labelEl ? labelEl.id + " " : "") + valueEl.id);
    if (!labelEl) button.setAttribute("aria-label", labelText);
    if (select.title) button.title = select.title;
    select.insertAdjacentElement("afterend", button);
    wrap.classList.add("has-menu");
    select.setAttribute("tabindex", "-1");
    select.setAttribute("aria-hidden", "true");

    var items = itemsOf(select);
    var shown = [];
    var active = -1;
    var menu = null, list = null, input = null, empty = null, typed = "", typedAt = 0;
    var searchable = items.length > SEARCH_MIN;

    function selectedItem() {
      var i = nativeIndex.get.call(select);
      for (var k = 0; k < items.length; k++) if (items[k].index === i) return items[k];
      return null;
    }

    // the button shows the choice
    function refresh() {
      items = itemsOf(select);
      var cur = selectedItem();
      valueEl.textContent = "";
      if (cur && cur.swatch) {
        var sw = el("span", "sm-swatch");
        sw.style.background = cur.swatch;
        valueEl.appendChild(sw);
      }
      valueEl.appendChild(document.createTextNode(cur ? cur.label : ""));
      button.disabled = !!select.disabled;
    }

    function build() {
      menu = el("div", "sm-menu");
      menu.setAttribute("data-for", id);
      if (searchable) {
        var box = el("div", "sm-search");
        box.insertAdjacentHTML("beforeend", SEARCH_ICON);
        input = el("input");
        input.type = "text";
        input.setAttribute("role", "combobox");
        input.setAttribute("aria-label", "Search " + labelText.toLowerCase());
        input.setAttribute("aria-autocomplete", "list");
        input.setAttribute("aria-expanded", "true");
        input.setAttribute("aria-controls", id + "-listbox");
        input.setAttribute("autocomplete", "off");
        input.setAttribute("spellcheck", "false");
        input.placeholder = "Search " + items.length + " " + (labelText.toLowerCase() === "language" ? "languages" : "options");
        box.appendChild(input);
        menu.appendChild(box);
        input.addEventListener("input", function () { draw(input.value, true); });
      }
      list = el("div", "sm-list");
      list.id = id + "-listbox";
      list.setAttribute("role", "listbox");
      list.setAttribute("aria-label", labelText);
      list.tabIndex = -1;
      menu.appendChild(list);
      empty = el("p", "sm-empty", "Nothing matches.");
      empty.hidden = true;
      menu.appendChild(empty);
      var hint = el("div", "sm-hint");
      hint.append(el("span", "", "↑↓ move · Enter choose"), el("span", "", "Esc close"));
      menu.appendChild(hint);

      // keep the focus where it is (the search box) when the mouse is used
      menu.addEventListener("mousedown", function (e) { if (e.target !== input) e.preventDefault(); });
      list.addEventListener("mousemove", function (e) {
        var row = e.target.closest && e.target.closest(".sm-item");
        if (row && !row.classList.contains("is-disabled")) setActive(+row.getAttribute("data-at"), false);
      });
      list.addEventListener("click", function (e) {
        var row = e.target.closest && e.target.closest(".sm-item");
        if (row && !row.classList.contains("is-disabled")) choose(shown[+row.getAttribute("data-at")]);
      });
      menu.addEventListener("keydown", onKey);
      document.body.appendChild(menu);
    }

    // the rows for what is typed
    function draw(query, keepNone) {
      shown = filterItems(items, query);
      var cur = selectedItem();
      list.textContent = "";
      var lastGroup = null;
      shown.forEach(function (it, at) {
        if (it.group && it.group !== lastGroup) {
          list.appendChild(el("div", "sm-group", it.group));
          lastGroup = it.group;
        }
        var row = el("div", "sm-item" + (it.disabled ? " is-disabled" : ""));
        row.id = id + "-opt-" + at;
        row.setAttribute("role", "option");
        row.setAttribute("data-at", String(at));
        var isCur = !!cur && cur.index === it.index;
        row.setAttribute("aria-selected", isCur ? "true" : "false");
        if (isCur) row.classList.add("is-current");
        if (it.swatch) {
          var sw = el("span", "sm-swatch");
          sw.style.background = it.swatch;
          row.appendChild(sw);
        }
        row.appendChild(el("span", "sm-label", it.label));
        if (it.badge) row.appendChild(el("span", "sm-badge", it.badge));
        row.insertAdjacentHTML("beforeend", CHECK);
        list.appendChild(row);
      });
      empty.hidden = shown.length > 0;
      // the item to start on: the one typed for, else the current one, else the first
      var start = -1;
      if (query) start = moveActive(shown, -1, "ArrowDown");
      else if (cur) start = shown.findIndex(function (it) { return it.index === cur.index; });
      if (start < 0 && !keepNone) start = moveActive(shown, -1, "ArrowDown");
      setActive(start, true);
    }

    function setActive(at, scroll) {
      active = at;
      var rows = list.querySelectorAll(".sm-item");
      for (var i = 0; i < rows.length; i++) rows[i].classList.toggle("is-active", i === at);
      var row = rows[at];
      var target = input || list;
      if (row) {
        target.setAttribute("aria-activedescendant", row.id);
        if (scroll) {
          // keep the group heading in view above the first item of a group
          var prev = row.previousElementSibling;
          (at === 0 && prev ? prev : row).scrollIntoView({ block: "nearest" });
        }
      } else {
        target.removeAttribute("aria-activedescendant");
      }
    }

    function choose(item) {
      if (!item || item.disabled) return;
      var changed = nativeValue.get.call(select) !== item.value;
      close(true);
      if (changed) {
        nativeValue.set.call(select, item.value);
        refresh();
        select.dispatchEvent(new Event("input", { bubbles: true }));
        select.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }

    function onKey(e) {
      var k = e.key;
      if (k === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        close(true);
      } else if (k === "Tab") {
        close(true); // the focus is back on the button; Tab goes on from there
      } else if (k === "Enter") {
        e.preventDefault();
        if (active >= 0) choose(shown[active]);
      } else if (k === "ArrowDown" || k === "ArrowUp" || k === "Home" || k === "End" || k === "PageDown" || k === "PageUp") {
        if ((k === "Home" || k === "End") && input) return; // the caret of the search box
        e.preventDefault();
        setActive(moveActive(shown, active, k), true);
      } else if (!input && k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
        // a list with no search box: type to jump
        var now = Date.now();
        typed = now - typedAt > 700 ? k : typed + k;
        typedAt = now;
        var hit = typeAhead(shown, typed, active);
        if (hit >= 0) setActive(hit, true);
        e.preventDefault();
      }
    }

    function outside(e) {
      if (menu && !menu.contains(e.target) && !button.contains(e.target)) close(false);
    }
    function onScroll(e) {
      if (menu && !menu.contains(e.target)) close(false);
    }
    function onResize() { close(false); }

    function open() {
      if (button.disabled) return;
      if (openMenu && openMenu !== api) openMenu.close(false);
      if (api.isOpen()) return;
      items = itemsOf(select);
      searchable = items.length > SEARCH_MIN;
      if (!menu) build();
      if (input) input.value = "";
      draw("", false);
      menu.hidden = false;
      menu.classList.add("is-open");
      button.setAttribute("aria-expanded", "true");
      // measured at its largest, so that the list under it can grow and shrink while it is searched
      menu.style.maxHeight = "";
      menu.style.visibility = "hidden";
      var at = placeMenu(button.getBoundingClientRect(), 240, Math.min(menu.scrollHeight, 340), { width: window.innerWidth, height: window.innerHeight });
      menu.style.left = at.left + "px";
      menu.style.minWidth = at.width + "px";
      menu.style.top = at.top === null ? "auto" : at.top + "px";
      menu.style.bottom = at.bottom === null ? "auto" : at.bottom + "px";
      menu.style.maxHeight = at.maxHeight + "px";
      menu.classList.toggle("is-up", at.up);
      menu.style.visibility = "";
      setActive(active, true);
      (input || list).focus({ preventScroll: true });
      openMenu = api;
      document.addEventListener("mousedown", outside, true);
      window.addEventListener("scroll", onScroll, true);
      window.addEventListener("resize", onResize);
    }

    function close(returnFocus) {
      if (!api.isOpen()) return;
      menu.hidden = true;
      menu.classList.remove("is-open");
      button.setAttribute("aria-expanded", "false");
      document.removeEventListener("mousedown", outside, true);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
      if (openMenu === api) openMenu = null;
      if (returnFocus) button.focus({ preventScroll: true });
    }

    button.addEventListener("click", function () { api.isOpen() ? close(true) : open(); });
    button.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        open();
      }
    });

    // what the page does to the select from outside shows on the button, and its
    // focus() and showPicker() are the menu's
    Object.defineProperty(select, "value", {
      configurable: true,
      get: function () { return nativeValue.get.call(this); },
      set: function (v) { nativeValue.set.call(this, v); refresh(); }
    });
    Object.defineProperty(select, "selectedIndex", {
      configurable: true,
      get: function () { return nativeIndex.get.call(this); },
      set: function (v) { nativeIndex.set.call(this, v); refresh(); }
    });
    select.focus = function (o) { button.focus(o); };
    select.showPicker = function () { open(); };
    select.addEventListener("change", refresh);

    var api = {
      select: select,
      button: button,
      open: open,
      close: close,
      refresh: refresh,
      isOpen: function () { return !!menu && !menu.hidden; }
    };
    select.__selectMenu = api;
    refresh();
    return api;
  }

  function init() {
    // a touch screen keeps the browser's own picker
    if (window.matchMedia && window.matchMedia("(hover: none), (pointer: coarse)").matches) return;
    var selects = document.querySelectorAll("select[data-select-menu]");
    for (var i = 0; i < selects.length; i++) {
      try {
        enhance(selects[i]);
      } catch (e) {
        console.error("select menu:", e); // that select stays as the browser draws it
      }
    }
  }

  window.SelectMenu = { enhance: enhance, init: init, itemsOf: itemsOf, filterItems: filterItems, placeMenu: placeMenu, moveActive: moveActive, typeAhead: typeAhead };

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
