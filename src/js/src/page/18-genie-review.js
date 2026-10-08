// Reviewing what Genie wants to put in the editor: its change is shown as a diff
// over the editor, with Accept and Reject for each part and for all of it, and
// what was accepted can be undone as one step.
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. The chat widget's Insert and Replace buttons
// (window.insertcodesnippet / replacecodesnippet, defined at the end of this
// file) and, later, the agent mode both come here: nothing Genie writes reaches
// the editor without it.
//
// Accepted changes go through Ace's own document API, so Ctrl+Z works as for
// typing. The panel goes away as soon as every change is accepted or rejected;
// a notice then says what was applied, with an Undo that restores the text from
// before the first accepted change, as long as the editor still holds what the
// last accepted change left.

import { makeHunks, locate, context } from "./lib-diff.mjs";

const CONTEXT_LINES = 2;
const SHOWN_LINES = 14; // lines of a hunk shown before "... more lines"

let review = null; // the open proposal, if any
let lastApplied = null; // the latest proposal that changed the editor, for undo()

function aceEditor() {
  const el = window["editor"];
  return el && el.env && el.env.editor ? el.env.editor : null;
}

function reviewHost() {
  return document.querySelector(".editor-body") || document.getElementById("editor") || null;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(label, cls, onClick, aria) {
  const b = el("button", "genie-review__btn " + (cls || ""), label);
  b.type = "button";
  if (aria) b.setAttribute("aria-label", aria);
  b.addEventListener("click", onClick);
  return b;
}

function plural(n, word) {
  return n + " " + word + (n === 1 ? "" : "s");
}

// ---- the proposal -----------------------------------------------------------------

// propose shows the change from the editor's text to newText. It returns the
// number of changes, 0 when there is nothing to review.
function propose(newText, opts) {
  opts = opts || {};
  const ed = aceEditor();
  const host = reviewHost();
  if (!ed || !host) return -1;
  closeReview();

  const doc = ed.getSession().getDocument();
  const base = doc.getValue();
  const made = makeHunks(base, String(newText));
  if (made.hunks.length === 0) return 0;

  review = {
    ed: ed,
    doc: doc,
    host: host,
    title: opts.title || "Genie proposes",
    oldLines: made.oldLines,
    hunks: made.hunks.map((h) => ({ h: h, state: "pending", stale: false, row: 0 })),
    drift: 0, // how far the user's own typing has moved the lines since the proposal
    applied: 0, // accepted changes
    checkpoint: null, // the text before the first accepted change
    after: null, // the text the last accepted change left
    root: null,
    focused: false,
    mixed: false, // the user edited between accepted changes
    onDone: typeof opts.onDone === "function" ? opts.onDone : null, // told once, when every change is decided or the review is closed
    told: false,
    offered: false, // the notice about what was applied has been shown
    undone: false,
  };
  render();
  return made.hunks.length;
}

// The row a hunk starts at in the editor now: its old row, moved by the
// accepted changes before it.
function rowOf(r, index) {
  let delta = 0;
  for (let k = 0; k < index; k++) {
    if (r.hunks[k].state === "accepted") delta += r.hunks[k].h.add.length - r.hunks[k].h.del.length;
  }
  return r.hunks[index].h.oldStart + delta;
}

// Looks where each pending change applies now, allowing for lines the user
// typed above it; a change that is not found any more is stale.
function refreshStale(r) {
  const lines = r.doc.getAllLines();
  r.hunks.forEach((x, i) => {
    if (x.state !== "pending") return;
    x.row = locate(lines, x.h, rowOf(r, i) + r.drift);
    x.stale = x.row < 0;
  });
}

function acceptOne(index) {
  const r = review;
  if (!r) return;
  refreshStale(r);
  const x = r.hunks[index];
  if (!x || x.state !== "pending" || x.stale) {
    render();
    return;
  }
  if (r.checkpoint === null) r.checkpoint = r.doc.getValue();
  // the user typed between two accepted changes: one step back to before the
  // first would take their typing away too, so the button gives way to Ctrl+Z
  if (r.after !== null && r.doc.getValue() !== r.after) r.mixed = true;
  const row = x.row;
  r.drift = row - rowOf(r, index);
  if (x.h.del.length) r.doc.removeFullLines(row, row + x.h.del.length - 1);
  if (x.h.add.length) r.doc.insertFullLines(row, x.h.add);
  x.state = "accepted";
  r.applied++;
  r.after = r.doc.getValue();
  try {
    r.ed.gotoLine(row + 1, 0, true);
  } catch (e) {
    // the cursor stays where it was
  }
  render();
}

function rejectOne(index) {
  const r = review;
  if (!r || r.hunks[index].state !== "pending") return;
  r.hunks[index].state = "rejected";
  render();
}

function acceptAll() {
  const r = review;
  if (!r) return;
  // from the first: each accepted change moves the rows of the ones after it
  for (let i = 0; i < r.hunks.length; i++) {
    if (r.hunks[i].state === "pending") {
      refreshStale(r);
      if (!r.hunks[i].stale) acceptOne(i);
    }
  }
}

function rejectAll() {
  const r = review;
  if (!r) return;
  r.hunks.forEach((x) => {
    if (x.state === "pending") x.state = "rejected";
  });
  render();
}

// Puts the text from before Genie's first accepted change back. It works on a
// review that is already closed: its panel is gone, its checkpoint is not.
function undo(r) {
  r = r || lastApplied;
  if (!r || r.checkpoint === null || r.undone) return;
  if (r.doc.getValue() !== r.after) {
    notify("The code changed since Genie's changes, so they cannot be undone as one step. Use Ctrl+Z.", { type: "info" });
    return;
  }
  r.doc.setValue(r.checkpoint);
  r.ed.clearSelection();
  r.undone = true;
  notify("Undid " + plural(r.applied, "change") + " from Genie.", { type: "info" });
}

// A notice, once the panel is gone, about what was applied.
function offerUndo(r) {
  if (!r.applied || r.offered) return;
  r.offered = true;
  lastApplied = r;
  const what = "Applied " + plural(r.applied, "change") + (r.applied < r.hunks.length ? " of " + r.hunks.length : "") + " from Genie.";
  if (r.mixed) {
    // one step back to before the first would take the user's own typing away too
    notify(what + " Undo with Ctrl+Z.", { type: "success" });
    return;
  }
  notify(what, {
    type: "success",
    timeout: 10000,
    action: {
      label: "Undo",
      onClick: function () {
        undo(r);
      },
    },
  });
}

// tell says what became of the changes, once, to whoever asked to be told (the
// agent waits for the user's decision).
function tell(r, closed) {
  if (!r || r.told || !r.onDone) return;
  r.told = true;
  const rejected = r.hunks.filter((x) => x.state !== "accepted").length;
  try {
    r.onDone({ total: r.hunks.length, accepted: r.applied, rejected: rejected, closed: !!closed });
  } catch (e) {
    console.error(e);
  }
}

// The review is over: every change is decided, or it was closed (what is still
// pending counts as rejected). The panel goes away; a notice says what was
// applied.
function finish(r, closed) {
  // focus goes back to the editor if it was in the panel or lost with it, not
  // from the chat box the user may be typing in
  const ae = document.activeElement;
  const hadFocus = !ae || ae === document.body || !!(r.root && r.root.contains(ae));
  if (r.root && r.root.parentNode) r.root.parentNode.removeChild(r.root);
  r.root = null;
  if (review === r) review = null;
  tell(r, closed);
  offerUndo(r);
  if (hadFocus) {
    try {
      r.ed.focus();
    } catch (e) {
      // focus is not essential
    }
  }
}

function closeReview() {
  if (review) finish(review, true);
}

// ---- drawing it -------------------------------------------------------------------

function lineRows(parent, lines, mark, cls) {
  lines.forEach((text) => {
    const row = el("div", "genie-review__line " + cls);
    row.appendChild(el("span", "genie-review__mark", mark));
    row.appendChild(el("span", "genie-review__text", text === "" ? " " : text));
    parent.appendChild(row);
  });
}

function limited(parent, lines, mark, cls) {
  if (lines.length <= SHOWN_LINES) {
    lineRows(parent, lines, mark, cls);
    return;
  }
  const head = Math.ceil(SHOWN_LINES / 2);
  const tail = SHOWN_LINES - head;
  lineRows(parent, lines.slice(0, head), mark, cls);
  parent.appendChild(el("div", "genie-review__more", "… " + plural(lines.length - head - tail, "more line") + " …"));
  lineRows(parent, lines.slice(lines.length - tail), mark, cls);
}

function hunkView(r, index) {
  const x = r.hunks[index];
  const h = x.h;
  const box = el("div", "genie-review__hunk genie-review__hunk--" + x.state);
  const head = el("div", "genie-review__hunk-head");
  const what = h.del.length === 0 ? "adds " + plural(h.add.length, "line") : h.add.length === 0 ? "removes " + plural(h.del.length, "line") : "changes " + plural(h.del.length, "line");
  head.appendChild(el("span", "genie-review__hunk-title", "Change " + (index + 1) + " of " + r.hunks.length + ": " + what));
  if (x.state === "pending") {
    const a = button("Accept", "genie-review__btn--ok", () => acceptOne(index), "Accept change " + (index + 1));
    const rj = button("Reject", "", () => rejectOne(index), "Reject change " + (index + 1));
    if (x.stale) a.disabled = true;
    head.appendChild(a);
    head.appendChild(rj);
  } else {
    head.appendChild(el("span", "genie-review__state", x.state === "accepted" ? "Accepted" : "Rejected"));
  }
  box.appendChild(head);
  if (x.stale && x.state === "pending") {
    box.appendChild(el("div", "genie-review__stale", "The code changed meanwhile, so this change cannot be applied. Reject it."));
  }
  const ctx = context(r.oldLines, h, CONTEXT_LINES);
  const body = el("div", "genie-review__code");
  lineRows(body, ctx.before, " ", "genie-review__line--ctx");
  limited(body, h.del, "-", "genie-review__line--del");
  limited(body, h.add, "+", "genie-review__line--add");
  lineRows(body, ctx.after, " ", "genie-review__line--ctx");
  box.appendChild(body);
  return box;
}

function render() {
  const r = review;
  if (!r) return;
  refreshStale(r);
  const pending = r.hunks.filter((x) => x.state === "pending").length;
  if (pending === 0) {
    finish(r, false); // everything is decided: the panel goes away
    return;
  }
  if (r.root && r.root.parentNode) r.root.parentNode.removeChild(r.root);

  const root = el("div", "genie-review");
  root.setAttribute("role", "region");
  root.setAttribute("aria-label", "Changes proposed by Genie");
  root.tabIndex = -1;

  const head = el("div", "genie-review__head");
  head.appendChild(
    el("span", "genie-review__title", r.title + " " + plural(r.hunks.length, "change") + (r.applied || pending !== r.hunks.length ? " (" + pending + " left)" : ""))
  );
  head.appendChild(button("Accept all", "genie-review__btn--ok", acceptAll));
  head.appendChild(button("Reject all", "", rejectAll));
  head.appendChild(button("×", "genie-review__close", closeReview, "Close the review"));
  root.appendChild(head);

  const list = el("div", "genie-review__list");
  r.hunks.forEach((x, i) => list.appendChild(hunkView(r, i)));
  root.appendChild(list);

  root.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      ev.stopPropagation();
      ev.preventDefault();
      rejectAll();
    } else if (ev.key === "Enter" && ev.target === root) {
      ev.preventDefault();
      acceptAll();
    }
  });
  r.root = root;
  r.host.appendChild(root);
  if (!r.focused) {
    // keyboard users land on the review once: Enter accepts, Esc rejects
    r.focused = true;
    try {
      root.focus({ preventScroll: true });
    } catch (e) {
      // focus is not essential
    }
  }
}

// The text of the editor with code put where the cursor or the selection is.
// At the start of a line that has text, the code goes in front of the line (a
// newline is added), not into its first words.
function textWithInserted(ed, code) {
  const doc = ed.getSession().getDocument();
  const text = doc.getValue();
  const range = ed.getSelectionRange();
  const from = doc.positionToIndex(range.start);
  const to = doc.positionToIndex(range.end);
  const startsLine = from === to && range.start.column === 0 && doc.getLine(range.start.row) !== "";
  const put = startsLine && code !== "" && !code.endsWith("\n") ? code + "\n" : code;
  return text.slice(0, from) + put + text.slice(to);
}

// ---- the chat widget's buttons ---------------------------------------------------

// Genie encodes code blocks as UTF-8 base64 (chat-widget renderer.code)
function decodeGenieCode(encoded) {
  return decodeURIComponent(escape(atob(encoded)));
}

function reportResult(n, what) {
  if (n === 0) notify("What Genie suggests is already in the editor.", { type: "info" });
  else if (n < 0) notify("Couldn't " + what + " the code in the editor.", { type: "error" });
}

window.insertcodesnippet = function (encoded) {
  try {
    const code = decodeGenieCode(encoded);
    const ed = aceEditor();
    if (!ed) return reportResult(-1, "insert");
    reportResult(propose(textWithInserted(ed, code), { title: "Genie wants to insert" }), "insert");
  } catch (error) {
    console.error(error);
    reportResult(-1, "insert");
  }
};

window.replacecodesnippet = function (encoded) {
  try {
    if (window.location.pathname.includes("practice")) {
      notify("In practice mode, Genie can insert code but not replace the whole file.", { type: "info" });
      return;
    }
    reportResult(propose(decodeGenieCode(encoded), { title: "Genie wants to replace the file:" }), "replace");
  } catch (error) {
    console.error(error);
    reportResult(-1, "replace");
  }
};

// For the agent mode: the same review, as a promise that is kept when the user
// has decided on every change, or closed the review. {total: 0} at once if
// there is nothing to review, and {error} if the editor is not there.
function proposeAsync(newText, opts) {
  return new Promise(function (resolve) {
    const o = Object.assign({}, opts, { onDone: resolve });
    const n = propose(newText, o);
    if (n < 0) resolve({ total: 0, accepted: 0, rejected: 0, closed: true, error: "the editor is not available" });
    else if (n === 0) resolve({ total: 0, accepted: 0, rejected: 0, closed: false });
  });
}

function proposeInsertAsync(code, opts) {
  const ed = aceEditor();
  if (!ed) return Promise.resolve({ total: 0, accepted: 0, rejected: 0, closed: true, error: "the editor is not available" });
  return proposeAsync(textWithInserted(ed, code), opts);
}

// For the agent mode and for tests.
window.GenieReview = {
  proposeAsync: proposeAsync,
  proposeInsertAsync: proposeInsertAsync,
  isOpen: function () {
    return !!review;
  },
  propose: propose,
  acceptAll: acceptAll,
  rejectAll: rejectAll,
  undo: function () {
    undo();
  },
  close: closeReview,
  pending: function () {
    return review ? review.hunks.filter((x) => x.state === "pending").length : 0;
  },
};
