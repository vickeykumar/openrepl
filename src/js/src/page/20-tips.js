// Tips for the buttons of the workspace and of the Genie panel: a small pane by
// the button with its name, its shortcut and one line about it. Part of the page
// script (T20), bundled into js/scribbler.js.
//
// A tip is made from what the button already has:
//   title            the line about it (the browser's own tooltip, which this replaces)
//   data-tip-name    its name; without one, its visible words or its aria-label
//   data-tip-keys    its shortcut ("Mod Enter": Mod is Ctrl, or ⌘ on a Mac)
//   data-tip-desc    a line written for the tip, where the title is not one
// A few buttons change with their state (Maximize or Restore, Pin or Unpin,
// Send or Stop): NAMES below reads the state, so nothing else has to keep the
// tip up to date.
//
// Nothing here changes what a button does. While the mouse is on a button its
// title is kept in data-tip-title, so that the browser does not show its own
// tooltip on top, and put back when the mouse leaves. Code that REMOVES a title
// (the Genie panel's send button does) must remove data-tip-title with it, or
// the old one would come back.
//
// Tips are for a mouse and for the keyboard (a button reached with Tab). There
// are none on a touch screen, during the tour, or for a button whose menu is open.

import { tipContent, placeTip } from "./lib-tips.mjs";

const SCOPE = "#ide-shell, #chat-widget__container, .genie-fab";
const CANDIDATE = "[title], [data-tip-title], [data-tip-name], [data-tip-desc]";
// not the editor, the terminal or the file tree: their own titles stay as they are
const NOT_IN = "#editor, .terminal, #file-browser, .genie-menu, .ctx-menu";
const SHOW_AFTER_MS = 350;
const QUICK_WITHIN_MS = 400; // from one button to the next, the tip follows at once

const NAMES = {
  "togglescreen-button": function (el) {
    const on = el.getAttribute("aria-pressed") === "true";
    return on
      ? { name: "Restore", keys: "Esc", desc: "Back to the normal size." }
      : { name: "Maximize", keys: "", desc: "Fill the window with the workspace. Esc restores it." };
  },
  "rotate-button": function (el) {
    return { name: visibleWords(el) || "Layout" };
  },
  "chat-widget__pin": function (el) {
    return { name: el.getAttribute("aria-pressed") === "true" ? "Unpin" : "Pin" };
  },
  "chat-widget__submit": function (el) {
    return el.classList.contains("is-stop")
      ? { name: "Stop", keys: "", desc: "Stop the task Genie is working on." }
      : { name: "Send", keys: "Enter", desc: "Send your message to Genie." };
  },
};

let pane = null;
let hover = null; // { el, watch } the button the mouse is on
let shown = null; // the button the pane is shown for
let timer = 0;
let hiddenAt = 0;

function isMac() {
  return typeof window.IS_MAC === "boolean" ? window.IS_MAC : /Mac|iPhone|iPad/.test(navigator.platform || "");
}

function visibleWords(el) {
  const copy = el.cloneNode(true);
  copy.querySelectorAll("kbd, svg, .visually-hidden, .social-count, input, select").forEach(function (n) {
    n.remove();
  });
  const words = (copy.textContent || "").replace(/\s+/g, " ").trim();
  return words.length <= 28 ? words : "";
}

function candidate(target) {
  if (!(target instanceof Element)) return null;
  const el = target.closest(CANDIDATE);
  if (!el || !el.closest(SCOPE) || el.closest(NOT_IN)) return null;
  return el;
}

function contentOf(el) {
  const fromState = NAMES[el.id] ? NAMES[el.id](el) : {};
  const label = el.getAttribute("aria-label") || "";
  return tipContent({
    name: fromState.name || el.getAttribute("data-tip-name") || visibleWords(el) || (label.length <= 28 ? label : ""),
    keys: fromState.keys !== undefined ? fromState.keys : el.getAttribute("data-tip-keys"),
    desc: fromState.desc || el.getAttribute("data-tip-desc") || el.getAttribute("data-tip-title") || el.getAttribute("title") || "",
    isMac: isMac(),
  });
}

function quiet(el) {
  // not during the tour, not for a button whose menu or dialog is open (a button
  // that only says a panel is shown, like Files, keeps its tip), not for one that is off
  const menuOpen = el.hasAttribute("aria-haspopup") && el.getAttribute("aria-expanded") === "true";
  return !!document.querySelector(".introjs-overlay") || menuOpen || el.disabled === true;
}

function ensurePane() {
  if (pane) return pane;
  pane = document.createElement("div");
  pane.className = "ide-tip";
  pane.setAttribute("role", "tooltip");
  pane.hidden = true;
  const arrow = document.createElement("span");
  arrow.className = "ide-tip__arrow";
  const head = document.createElement("div");
  head.className = "ide-tip__head";
  const desc = document.createElement("div");
  desc.className = "ide-tip__desc";
  pane.append(arrow, head, desc);
  document.body.appendChild(pane);
  return pane;
}

function show(el) {
  if (!el.isConnected || quiet(el)) return;
  const c = contentOf(el);
  if (!c.name) return;
  const p = ensurePane();
  const head = p.querySelector(".ide-tip__head");
  const desc = p.querySelector(".ide-tip__desc");
  head.textContent = "";
  const name = document.createElement("span");
  name.textContent = c.name;
  head.appendChild(name);
  c.keys.forEach(function (k) {
    const kbd = document.createElement("kbd");
    kbd.textContent = k;
    head.appendChild(kbd);
  });
  desc.textContent = c.desc;
  desc.hidden = !c.desc;
  p.hidden = false;
  p.classList.remove("is-shown");
  p.style.left = "0px";
  p.style.top = "0px";
  const at = placeTip(el.getBoundingClientRect(), { width: p.offsetWidth, height: p.offsetHeight }, { width: window.innerWidth, height: window.innerHeight });
  p.style.left = at.left + "px";
  p.style.top = at.top + "px";
  p.classList.toggle("ide-tip--above", at.above);
  p.querySelector(".ide-tip__arrow").style.left = at.arrow + "px";
  void p.offsetWidth; // so that it fades in from here
  p.classList.add("is-shown");
  shown = el;
}

function hide() {
  clearTimeout(timer);
  if (pane && !pane.hidden) {
    pane.hidden = true;
    pane.classList.remove("is-shown");
    hiddenAt = Date.now();
  }
  shown = null;
}

// ---- the button's own title, while the mouse is on it ---------------------------------

function keepTitle(el) {
  const t = el.getAttribute("title");
  if (t === null) return;
  el.setAttribute("data-tip-title", t);
  el.removeAttribute("title");
}

function giveTitleBack(el) {
  const kept = el.getAttribute("data-tip-title");
  if (kept === null) return;
  el.removeAttribute("data-tip-title");
  if (!el.hasAttribute("title")) el.setAttribute("title", kept);
}

function enter(el) {
  leave();
  keepTitle(el);
  // a title set while the mouse is still there (a click that changes the button) is kept too
  const watch = new MutationObserver(function () {
    if (el.hasAttribute("title")) {
      keepTitle(el);
      if (shown === el) show(el);
    }
  });
  watch.observe(el, { attributes: true, attributeFilter: ["title"] });
  hover = { el: el, watch: watch };
  clearTimeout(timer);
  timer = setTimeout(function () {
    if (hover && hover.el === el) show(el);
  }, Date.now() - hiddenAt < QUICK_WITHIN_MS ? 0 : SHOW_AFTER_MS);
}

function leave() {
  if (!hover) return;
  hover.watch.disconnect();
  giveTitleBack(hover.el);
  if (shown === hover.el) hide();
  else clearTimeout(timer);
  hover = null;
}

// ---- the mouse ---------------------------------------------------------------------------

document.addEventListener("pointerover", function (ev) {
  if (ev.pointerType !== "mouse") return;
  const el = candidate(ev.target);
  if (hover && hover.el === el) return;
  if (el) enter(el);
  else leave();
});

document.addEventListener("pointerout", function (ev) {
  if (!hover || ev.pointerType !== "mouse") return;
  const to = ev.relatedTarget;
  if (to instanceof Node && hover.el.contains(to)) return;
  leave();
});

// a button that is gone (a panel that closed) sends no pointerout
document.addEventListener("pointermove", function () {
  if (hover && !hover.el.isConnected) leave();
});

// a tip is in the way of nothing: any press, key, scroll or resize puts it away
// (the mouse may still be on the button: the tip comes back when it comes back)
["pointerdown", "keydown", "wheel"].forEach(function (type) {
  document.addEventListener(type, hide, { capture: true, passive: true });
});
window.addEventListener("scroll", hide, true);
window.addEventListener("resize", hide);
window.addEventListener("blur", hide);

// ---- the keyboard ------------------------------------------------------------------------

document.addEventListener("focusin", function (ev) {
  const el = candidate(ev.target);
  if (!el || el !== ev.target) return;
  let byKeyboard = false;
  try {
    byKeyboard = el.matches(":focus-visible");
  } catch (e) {
    // an old browser: no tips from the keyboard
  }
  if (byKeyboard) show(el);
});

document.addEventListener("focusout", function (ev) {
  if (shown && shown === ev.target && !(hover && hover.el === shown)) hide();
});

export {}; // an ES module: strict mode, bundled by webpack
