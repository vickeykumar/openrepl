// Right-click actions on selected code: with text selected in the editor, the
// context menu offers Explain, Fix problems, Add comments and Write tests. Part
// of the page script (T20), bundled into js/scribbler.js.
//
// Explain is a question: it goes to the Genie panel as a message
// (window.ChatWidget.ask), and the answer has the usual Insert and Replace
// buttons. The other three ask the server for the code (context "action",
// server/action.go) and show the answer as a diff to accept (GenieReview in
// 18-genie-review.js): Fix and Add comments replace the selection, Write tests
// goes right after it. On the practice page only Explain is offered.
//
// Shift or Ctrl with the right button, or no selection, leaves the browser's own
// menu. The menu also opens from the keyboard (the Menu key, Shift+F10).

import { fenced, codeOf, withAction } from "./lib-actions.mjs";

const MENU_ID = "genie-menu";
const CODE_ACTIONS = {
  fix: { label: "Fix problems", title: "Genie wants to fix the selection:", nothing: "Genie found nothing to fix in the selection." },
  comment: { label: "Add comments", title: "Genie wants to add comments:", nothing: "Genie added no comments." },
  tests: { label: "Write tests", title: "Genie wants to add tests after the selection:", nothing: "Genie wrote no tests." },
};

let menu = null; // the open menu, if any
let busy = false; // a code action is waiting for the model

function aceEditor() {
  const el = window["editor"];
  return el && el.env && el.env.editor ? el.env.editor : null;
}

function onPractice() {
  return window.location.pathname.includes("practice");
}

function genieAvailable() {
  const s = window.site_settings || {};
  return !s.genieDisabled && !!window.ChatWidget;
}

function say(message, type, opts) {
  if (typeof window.notify === "function") return window.notify(message, Object.assign({ type: type || "info" }, opts || {}));
  return function () {};
}

function picker() {
  return document.getElementById("optionlist");
}

function pickerLanguage() {
  const p = picker();
  return p && p.selectedIndex >= 0 ? p.options[p.selectedIndex].text.trim() : "";
}

// the tail of the active terminal, for what Fix should know about the error
function terminalTail() {
  try {
    const tab = document.querySelector("#terminal-tabs .tab.active");
    const term = tab && tab.gottyterm && tab.gottyterm.term;
    return term && typeof term.recentText === "function" ? String(term.recentText(40)) : "";
  } catch (e) {
    return "";
  }
}

// ---- the selection --------------------------------------------------------------------

function captureSelection(ed) {
  const text = ed.getSelectedText();
  if (!text || text.trim() === "") return null;
  const doc = ed.getSession().getDocument();
  const range = ed.getSelectionRange();
  return { text: text, from: doc.positionToIndex(range.start), to: doc.positionToIndex(range.end) };
}

// ---- the menu --------------------------------------------------------------------------

function closeMenu(refocus) {
  if (!menu) return;
  menu.remove();
  menu = null;
  document.removeEventListener("mousedown", onOutside, true);
  window.removeEventListener("resize", onDismiss);
  window.removeEventListener("scroll", onDismiss, true);
  if (refocus) {
    const ed = aceEditor();
    if (ed) ed.focus();
  }
}

function onOutside(ev) {
  if (menu && !menu.contains(ev.target)) closeMenu(false);
}

function onDismiss() {
  closeMenu(false);
}

function item(label, hint, onClick, extra) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "genie-menu__item" + (extra ? " " + extra : "");
  b.setAttribute("role", "menuitem");
  const l = document.createElement("span");
  l.textContent = label;
  b.appendChild(l);
  if (hint) {
    const h = document.createElement("span");
    h.className = "genie-menu__hint";
    h.textContent = hint;
    b.appendChild(h);
  }
  b.addEventListener("click", onClick);
  return b;
}

function openMenu(x, y, sel) {
  closeMenu(false);
  const root = document.createElement("div");
  root.id = MENU_ID;
  root.className = "genie-menu";
  root.setAttribute("role", "menu");
  root.setAttribute("aria-label", "Ask Genie about the selected code");

  const head = document.createElement("div");
  head.className = "genie-menu__head";
  head.textContent = "Ask Genie about this code";
  root.appendChild(head);

  const practice = onPractice();
  root.appendChild(
    item("Explain", "in chat", function () {
      closeMenu(false);
      explain(sel);
    })
  );
  if (!practice) {
    Object.keys(CODE_ACTIONS).forEach(function (id) {
      root.appendChild(
        item(CODE_ACTIONS[id].label, "diff", function () {
          closeMenu(false);
          codeAction(id, sel);
        })
      );
    });
  }
  const foot = document.createElement("div");
  foot.className = "genie-menu__foot";
  foot.textContent = "Shift + right-click: browser menu";
  root.appendChild(foot);

  root.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopPropagation();
      closeMenu(true);
      return;
    }
    const items = Array.prototype.filter.call(root.querySelectorAll("button"), function (b) {
      return !b.closest("[hidden]");
    });
    const at = items.indexOf(document.activeElement);
    let next = -1;
    if (ev.key === "ArrowDown") next = (at + 1) % items.length;
    else if (ev.key === "ArrowUp") next = (at - 1 + items.length) % items.length;
    else if (ev.key === "Home") next = 0;
    else if (ev.key === "End") next = items.length - 1;
    else if (ev.key === "Tab") {
      ev.preventDefault();
      closeMenu(true);
      return;
    }
    if (next >= 0) {
      ev.preventDefault();
      items[next].focus();
    }
  });

  document.body.appendChild(root);
  // inside the window, wherever it was asked for
  const w = root.offsetWidth;
  const h = root.offsetHeight;
  root.style.left = Math.max(8, Math.min(x, window.innerWidth - w - 8)) + "px";
  root.style.top = Math.max(8, Math.min(y, window.innerHeight - h - 8)) + "px";
  menu = root;
  document.addEventListener("mousedown", onOutside, true);
  window.addEventListener("resize", onDismiss);
  window.addEventListener("scroll", onDismiss, true);
  root.querySelector("button").focus();
}

document.addEventListener("contextmenu", function (ev) {
  if (!genieAvailable()) return;
  const target = ev.target;
  if (!(target instanceof Element) || !target.closest(".ace_editor")) return;
  if (ev.shiftKey || ev.ctrlKey) return; // the browser's own menu, on purpose
  const ed = aceEditor();
  if (!ed) return;
  const sel = captureSelection(ed);
  if (!sel) return;
  ev.preventDefault();
  let x = ev.clientX;
  let y = ev.clientY;
  if (!x && !y) {
    // from the keyboard: at the cursor
    const pos = ed.getCursorPosition();
    const at = ed.renderer.textToScreenCoordinates(pos.row, pos.column);
    x = at.pageX - window.pageXOffset;
    y = at.pageY - window.pageYOffset + 18;
  }
  openMenu(x, y, sel);
});

// ---- Explain: a question for the chat ---------------------------------------------------

function explain(sel) {
  window.ChatWidget.ask("Explain this " + pickerLanguage() + " code:\n\n" + fenced(sel.text, (picker() || {}).value || ""));
}

// ---- Fix, Add comments, Write tests: code, shown as a diff -----------------------------------

async function codeAction(id, sel) {
  const spec = CODE_ACTIONS[id];
  const ed = aceEditor();
  if (!spec || !ed) return;
  if (busy) {
    say("Genie is still working on the last action.");
    return;
  }
  // a task of agent mode shows its changes in the same panel: one at a time
  if (typeof window.ChatWidget.busy === "function" && window.ChatWidget.busy()) {
    say("Genie is busy with a task or an answer. Try again when it is done.");
    return;
  }
  const cfg = window.ChatWidget.config || {};
  if (!cfg.url) {
    say("Genie is not available right now.", "error");
    return;
  }
  busy = true;
  const done = say("Genie is working on it…", "info", { timeout: 0 });
  try {
    const mc = window.ModelChoice;
    const fields = mc ? mc.fields({ temperature: 0.2, maxTokens: 3000, extraTokens: 2500 }) : {};
    const headers = { "Content-Type": "application/json" };
    if (cfg.api_key) headers["Authorization"] = "Bearer " + cfg.api_key;
    const res = await fetch(cfg.url, {
      method: "POST",
      headers: headers,
      body: JSON.stringify(
        Object.assign({}, fields, {
          context: "action",
          action: id,
          language: pickerLanguage(),
          selection: sel.text,
          file: ed.getValue(),
          output: terminalTail(),
        })
      ),
    });
    if (!res.ok) {
      let message = "Genie could not answer (" + res.status + ").";
      try {
        const e = await res.json();
        if (e && e.error && typeof e.error.message === "string") message = e.error.message;
      } catch (e) {
        // the generic message stays
      }
      say(message, "error");
      return;
    }
    const data = await res.json();
    const answer = data && data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content;
    if (typeof answer !== "string" || answer.trim() === "") {
      say("Genie sent no answer. Try again.", "error");
      return;
    }
    // the editor may have changed while the model worked: the diff is made
    // against the text that was selected, so it has to still be there
    const doc = ed.getSession().getDocument();
    const base = doc.getValue();
    if (base.slice(sel.from, sel.to) !== sel.text) {
      say("The code changed while Genie worked, so its answer was not applied. Try again.");
      return;
    }
    const code = codeOf(answer, sel.text);
    const next = withAction(base, sel.from, sel.to, sel.text, code, id === "tests");
    const n = window.GenieReview.propose(next, { title: spec.title });
    if (n === 0) say(spec.nothing);
    else if (n < 0) say("Couldn't show the change in the editor.", "error");
  } catch (e) {
    console.error("genie action:", e);
    say("Genie could not be reached. Try again.", "error");
  } finally {
    busy = false;
    if (typeof done === "function") done();
  }
}

