// Where the lines Genie changed are in a file, for showing them in the editor itself (35-genie-changes.js). Pure functions, no DOM and no
// Ace, so that node can test them (src/js/test).

import { makeHunks } from "./lib-diff.mjs";

// The changes between the text before and the text the editor holds now, in the order of the file: `row` is the line of the current text where
// a change starts (the first added line, or the line after the place where lines were taken out), `added` how many lines came in there, and
// `removed` the old lines that are gone, to be shown above `row`.
export function bandsOf(before, current) {
  const out = [];
  let shift = 0; // how far the lines of the current text have moved from the old ones
  makeHunks(before, current).hunks.forEach((h) => {
    out.push({ row: h.oldStart + shift, added: h.add.length, removed: h.del.slice() });
    shift += h.add.length - h.del.length;
  });
  return out;
}

// How many lines came in and how many went, over all the changes.
export function countsOf(bands) {
  let added = 0;
  let removed = 0;
  bands.forEach((b) => {
    added += b.added;
    removed += b.removed.length;
  });
  return { added, removed };
}
