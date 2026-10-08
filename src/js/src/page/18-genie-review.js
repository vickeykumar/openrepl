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
// typing. Undo what Genie did restores the text from before the first accepted
// change, as long as the editor still holds what the last accepted change left.

import { makeHunks, locate, context } from "./lib-diff.mjs";

const CONTEXT_LINES = 2;
const SHOWN_LINES = 14; // lines of a hunk shown before "... more lines"

let review = null; // the open proposal, if any

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
  announce();
}

function rejectAll() {
  const r = review;
  if (!r) return;
  r.hunks.forEach((x) => {
    if (x.state === "pending") x.state = "rejected";
  });
  render();
  announce();
}

function undo() {
  const r = review;
  if (!r || r.checkpoint === null) return;
  if (r.doc.getValue() !== r.after) {
    notify("The code changed since Genie's changes, so they cannot be undone as one step. Use Ctrl+Z.", { type: "info" });
    return;
  }
  r.doc.setValue(r.checkpoint);
  r.ed.clearSelection();
  const n = r.applied;
  closeReview();
  notify("Undid " + plural(n, "change") + " from Genie.", { type: "info" });
}

function closeReview() {
  if (review && review.root && review.root.parentNode) review.root.parentNode.removeChild(review.root);
  review = null;
}

function announce() {
  const r = review;
  if (!r || !r.root) return;
  const live = r.root.querySelector(".genie-review__live");
  if (!live) return;
  const done = r.hunks.every((x) => x.state !== "pending");
  live.textContent = done ? "Applied " + plural(r.applied, "change") + " of " + r.hunks.length + "." : "";
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
  if (r.root && r.root.parentNode) r.root.parentNode.removeChild(r.root);
  const pending = r.hunks.filter((x) => x.state === "pending").length;

  const root = el("div", "genie-review");
  root.setAttribute("role", "region");
  root.setAttribute("aria-label", "Changes proposed by Genie");
  root.tabIndex = -1;
  root.appendChild(el("div", "genie-review__live visually-hidden")).setAttribute("aria-live", "polite");

  const head = el("div", "genie-review__head");
  if (pending > 0) {
    head.appendChild(
      el("span", "genie-review__title", r.title + " " + plural(r.hunks.length, "change") + (r.applied || pending !== r.hunks.length ? " (" + pending + " left)" : ""))
    );
    head.appendChild(button("Accept all", "genie-review__btn--ok", acceptAll));
    head.appendChild(button("Reject all", "", rejectAll));
  } else {
    head.appendChild(
      el("span", "genie-review__title", r.applied ? "Applied " + plural(r.applied, "change") + " of " + r.hunks.length : "No changes applied")
    );
    if (r.applied && !r.mixed) {
      head.appendChild(button("Undo what Genie did", "genie-review__btn--link", undo));
    } else if (r.applied) {
      head.appendChild(el("span", "genie-review__state", "You edited in between: undo with Ctrl+Z"));
    }
  }
  head.appendChild(button("×", "genie-review__close", closeReview, "Close the review"));
  root.appendChild(head);

  if (pending > 0 || r.hunks.length <= 6) {
    const list = el("div", "genie-review__list");
    r.hunks.forEach((x, i) => {
      if (pending > 0 || x.state === "accepted") list.appendChild(hunkView(r, i));
    });
    root.appendChild(list);
  }

  root.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      ev.stopPropagation();
      ev.preventDefault();
      if (review && review.hunks.some((x) => x.state === "pending")) rejectAll();
      else closeReview();
    } else if (ev.key === "Enter" && ev.target === root && review && review.hunks.some((x) => x.state === "pending")) {
      ev.preventDefault();
      acceptAll();
    }
  });
  r.root = root;
  r.host.appendChild(root);
  if (pending > 0 && !r.focused) {
    // keyboard users land on the review once: Enter accepts, Esc rejects
    r.focused = true;
    try {
      root.focus({ preventScroll: true });
    } catch (e) {
      // focus is not essential
    }
  }
  announce();
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
    const doc = ed.getSession().getDocument();
    const text = doc.getValue();
    const range = ed.getSelectionRange();
    const from = doc.positionToIndex(range.start);
    const to = doc.positionToIndex(range.end);
    // at the start of a line that has text, the code goes in front of that line
    // and not into its first words
    const startsLine = from === to && range.start.column === 0 && doc.getLine(range.start.row) !== "";
    const put = startsLine && code !== "" && !code.endsWith("\n") ? code + "\n" : code;
    reportResult(propose(text.slice(0, from) + put + text.slice(to), { title: "Genie wants to insert" }), "insert");
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

// For the agent mode and for tests.
window.GenieReview = {
  propose: propose,
  acceptAll: acceptAll,
  rejectAll: rejectAll,
  undo: undo,
  close: closeReview,
  pending: function () {
    return review ? review.hunks.filter((x) => x.state === "pending").length : 0;
  },
};
