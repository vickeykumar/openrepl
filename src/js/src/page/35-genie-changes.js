// What Genie changed in a file, shown in the editor itself ("Genie: Review above the box", canvas boards 3 and 4; docs/lld/19-genie-agent.md
// section 12). The Review bar above Genie's box calls GenieChanges.show(...) for one file: the lines that came in get a green band, the lines
// that went stay in place as red lines that cannot be edited, and a thin bar at the top of the editor says what it is and has Keep and Undo
// (and ‹ › to the next changed file). The bands are worked out from the text before the task and the text the editor holds now, again after
// each edit, so they follow what the user types. What Keep and Undo do is the caller's (onKeep, onUndo): they return "" when done, or the reason
// they did not, which the bar shows. A file that was deleted has no text in the editor: its old text is shown read-only over it.
//
// Part of the page script (T20), bundled into js/scribbler.js.

import { bandsOf, countsOf } from "./lib-changes.mjs";

let state = null; // { session, markers, widgets, bar, note, ghost, off, opts }
let timer = null;

function aceEditor() {
  const el = window["editor"];
  return el && el.env && el.env.editor ? el.env.editor : null;
}

function host() {
  return document.querySelector(".editor-body") || document.getElementById("editor") || null;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function wipe() {
  if (!state) return;
  const s = state;
  try {
    s.markers.forEach((id) => s.session.removeMarker(id));
    if (s.session.widgetManager) s.widgets.forEach((w) => s.session.widgetManager.removeLineWidget(w));
  } catch (e) {
    // a session that is gone has nothing to take off
  }
  s.markers = [];
  s.widgets = [];
}

function hide() {
  if (!state) return;
  clearTimeout(timer);
  wipe();
  const s = state;
  state = null;
  s.off.forEach((f) => f());
  if (s.bar) s.bar.remove();
  if (s.ghost) s.ghost.remove();
  const ed = aceEditor();
  if (ed) ed.renderer.setScrollMargin(0, 0, 0, 0);
}

function lineBox(text) {
  const l = el("div", "genie-changes__line", text === "" ? " " : text);
  return l;
}

function draw() {
  const s = state;
  const ed = aceEditor();
  if (!s || !ed || s.opts.kind === "deleted" || ed.session !== s.session) return;
  wipe();
  const ace = window["ace"];
  const Range = ace.require("ace/range").Range;
  const LineWidgets = ace.require("ace/line_widgets").LineWidgets;
  if (!s.session.widgetManager) {
    s.session.widgetManager = new LineWidgets(s.session);
    s.session.widgetManager.attach(ed);
  }
  const bands = bandsOf(s.opts.before || "", ed.getValue());
  const length = s.session.getLength();
  bands.forEach((b) => {
    for (let i = 0; i < b.added; i++) {
      if (b.row + i < length) s.markers.push(s.session.addMarker(new Range(b.row + i, 0, b.row + i, 1), "genie-changes__add", "fullLine"));
    }
    if (b.removed.length) {
      const box = el("div", "genie-changes__del");
      b.removed.forEach((t) => box.appendChild(lineBox(t)));
      // a widget is drawn below its row: the removed lines go below the line before the change (or below the last line); at the very top of
      // the file there is no line before, so Ace makes room above the first line and the lines are moved up into it
      const w = { fixedWidth: true, el: box, rowCount: b.removed.length };
      if (b.row >= length) {
        w.row = length - 1;
      } else if (b.row >= 1) {
        w.row = b.row - 1;
      } else {
        w.row = 0;
        w.rowsAbove = b.removed.length;
        box.style.transform = "translateY(" + -((b.removed.length + 1) * ed.renderer.lineHeight) + "px)";
      }
      s.session.widgetManager.addLineWidget(w);
      s.widgets.push(w);
    }
  });
  const c = countsOf(bands);
  if (s.count) {
    s.count.textContent = "";
    const plus = el("span", "genie-changes__plus", "+" + c.added);
    const minus = el("span", "genie-changes__minus", "−" + c.removed);
    s.count.append(plus, document.createTextNode(" "), minus);
  }
}

function later() {
  clearTimeout(timer);
  timer = setTimeout(draw, 150);
}

function button(label, cls, onClick) {
  const b = el("button", "genie-changes__btn " + (cls || ""), label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

// show: opts = { kind: "changed" | "new" | "deleted", path, before, after, index, total, title, keepLabel, undoLabel, onKeep, onUndo, onPrev, onNext }
function show(opts) {
  hide();
  const ed = aceEditor();
  const box = host();
  if (!box || !ed) return false;
  const bar = el("div", "genie-changes__bar");
  bar.setAttribute("role", "region");
  bar.setAttribute("aria-label", "What Genie changed in this file");
  const title = el("b", "genie-changes__title", opts.title || (opts.kind === "new" ? "New file" : opts.kind === "deleted" ? (opts.path || "") + " · deleted" : "Genie changed this file"));
  const count = el("span", "genie-changes__count");
  const note = el("span", "genie-changes__note");
  note.setAttribute("role", "status");
  const spacer = el("span", "genie-changes__sp");
  bar.append(title, count, note, spacer);
  if (opts.total > 1) {
    bar.appendChild(el("span", "genie-changes__pos", (opts.index || 1) + " of " + opts.total));
    const prev = button("‹", "", () => opts.onPrev && opts.onPrev());
    prev.setAttribute("aria-label", "The previous changed file");
    const next = button("›", "", () => opts.onNext && opts.onNext());
    next.setAttribute("aria-label", "The next changed file");
    bar.append(prev, next);
  }
  const act = (fn, b) => async () => {
    if (!fn) return hide();
    b.disabled = true;
    let why = "";
    try {
      why = (await fn()) || "";
    } catch (e) {
      why = String((e && e.message) || e);
    }
    if (why) {
      note.textContent = why;
      b.disabled = false;
    } else if (state && state.bar === bar) {
      hide();
    }
  };
  const keep = button(opts.keepLabel || "Keep", "genie-changes__keep", () => {});
  const undo = button(opts.undoLabel || "Undo", "genie-changes__undo", () => {});
  keep.onclick = act(opts.onKeep, keep);
  undo.onclick = act(opts.onUndo, undo);
  bar.append(keep, undo);
  box.appendChild(bar);
  state = { session: ed.session, markers: [], widgets: [], bar, note, count, ghost: null, off: [], opts };
  ed.renderer.setScrollMargin(bar.offsetHeight || 34, 0, 0, 0);
  if (opts.kind === "deleted") {
    const ghost = el("div", "genie-changes__ghost");
    ghost.style.top = (bar.offsetHeight || 34) + "px";
    String(opts.before || "").split("\n").forEach((t) => ghost.appendChild(lineBox(t)));
    box.appendChild(ghost);
    state.ghost = ghost;
    count.textContent = "read only";
  } else {
    const onChange = () => later();
    ed.on("change", onChange);
    ed.on("changeSession", hide);
    state.off.push(() => ed.off("change", onChange), () => ed.off("changeSession", hide));
    draw();
  }
  return true;
}

window.GenieChanges = {
  show,
  hide,
  isOpen: () => !!state,
};
