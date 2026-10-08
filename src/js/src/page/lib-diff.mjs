// A line diff, split into changes ("hunks") that can be accepted or rejected one
// by one, for the review of what Genie wants to put in the editor. Pure
// functions, no DOM and no Ace, so that node can test them (src/js/test).
//
// Lines are what Ace's document holds: text.split("\n"), so "a\nb\n" is
// ["a", "b", ""].

// the most cells of the table the diff may fill; a bigger change is one hunk
export const MAX_CELLS = 4000000;

export function splitLines(text) {
  return String(text).split("\n");
}

// A hunk: the old lines [oldStart, oldStart + del.length) are replaced by add.
// before and after are the old lines around it, to check, when it is applied,
// that the editor still holds what the hunk was made against.
function hunk(id, oldLines, oldStart, del, add) {
  return {
    id: id,
    oldStart: oldStart,
    del: del,
    add: add,
    before: oldStart > 0 ? oldLines[oldStart - 1] : null,
    after: oldStart + del.length < oldLines.length ? oldLines[oldStart + del.length] : null,
  };
}

// makeHunks returns the changes that turn oldText into newText, in order.
export function makeHunks(oldText, newText) {
  const a = splitLines(oldText);
  const b = splitLines(newText);

  // what both texts start and end with is not a change
  let pre = 0;
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++;
  let suf = 0;
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++;
  const am = a.slice(pre, a.length - suf);
  const bm = b.slice(pre, b.length - suf);
  if (am.length === 0 && bm.length === 0) return { oldLines: a, newLines: b, hunks: [] };

  // the table of longest common subsequence lengths, filled from the end
  const n = am.length;
  const m = bm.length;
  if ((n + 1) * (m + 1) > MAX_CELLS) {
    return { oldLines: a, newLines: b, hunks: [hunk(0, a, pre, am, bm)] };
  }
  const w = m + 1;
  const t = new Uint16Array((n + 1) * w);
  const cap = 65535;
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      t[i * w + j] =
        am[i] === bm[j] ? Math.min(cap, t[(i + 1) * w + j + 1] + 1) : Math.max(t[(i + 1) * w + j], t[i * w + j + 1]);
    }
  }

  // walk it: a run of deleted and added lines between two common lines is one hunk
  const hunks = [];
  let i = 0;
  let j = 0;
  let del = [];
  let add = [];
  let delStart = 0;
  const close = () => {
    if (del.length || add.length) {
      hunks.push(hunk(hunks.length, a, pre + delStart, del, add));
      del = [];
      add = [];
    }
  };
  while (i < n || j < m) {
    if (i < n && j < m && am[i] === bm[j]) {
      close();
      i++;
      j++;
      delStart = i;
    } else if (j >= m || (i < n && t[(i + 1) * w + j] >= t[i * w + j + 1])) {
      if (!del.length && !add.length) delStart = i;
      del.push(am[i]);
      i++;
    } else {
      if (!del.length && !add.length) delStart = i;
      add.push(bm[j]);
      j++;
    }
  }
  close();
  return { oldLines: a, newLines: b, hunks: hunks };
}

// applyHunks returns the lines of oldLines with the hunks whose id is in
// accepted applied.
export function applyHunks(oldLines, hunks, accepted) {
  const out = [];
  let pos = 0;
  for (const h of hunks) {
    for (; pos < h.oldStart; pos++) out.push(oldLines[pos]);
    if (accepted.has(h.id)) {
      for (const l of h.add) out.push(l);
    } else {
      for (const l of h.del) out.push(l);
    }
    pos = h.oldStart + h.del.length;
  }
  for (; pos < oldLines.length; pos++) out.push(oldLines[pos]);
  return out;
}

// holds says whether a hunk can still be applied to the lines the editor has
// now, with row the line it starts at: the lines it removes must be there, and
// the lines around it must be the ones it was made against.
export function holds(lines, h, row) {
  if (row < 0 || row + h.del.length > lines.length) return false;
  for (let k = 0; k < h.del.length; k++) {
    if (lines[row + k] !== h.del[k]) return false;
  }
  if (h.before !== null && lines[row - 1] !== h.before) return false;
  if (h.after !== null && lines[row + h.del.length] !== h.after) return false;
  return true;
}

// locate finds the row a hunk applies at: the expected one, or the nearest row
// within radius where holds is true (the user may have typed above it). It
// returns -1 if there is none.
export function locate(lines, h, expected, radius) {
  const max = radius === undefined ? 200 : radius;
  for (let d = 0; d <= max; d++) {
    if (holds(lines, h, expected + d)) return expected + d;
    if (d > 0 && holds(lines, h, expected - d)) return expected - d;
  }
  return -1;
}

// context returns up to n old lines before and after a hunk, for showing it.
export function context(oldLines, h, n) {
  const from = Math.max(0, h.oldStart - n);
  const to = Math.min(oldLines.length, h.oldStart + h.del.length + n);
  return {
    before: oldLines.slice(from, h.oldStart),
    after: oldLines.slice(h.oldStart + h.del.length, to),
    firstRow: from,
  };
}
