// The text and the place of a button's tip (20-tips.js), without the page, so
// that node can test it (src/js/test).

// What a tip says: the button's name, its shortcut as key caps, one line about it.
//   name: what the button is called ("Run"); "" if nothing names it
//   keys: its shortcut, space separated ("Mod Enter"); Mod is Ctrl, or ⌘ on a Mac
//   desc: the line about it (the button's own title, unless one was written for the tip)
// A shortcut written at the end of the line, in brackets, is dropped when the
// key caps show it. A line that only repeats the name is dropped.
export function tipContent(input) {
  const name = clean(input.name);
  const keys = clean(input.keys)
    .split(" ")
    .filter(Boolean)
    .map(function (k) {
      return k === "Mod" ? (input.isMac ? "⌘" : "Ctrl") : k;
    });
  let desc = clean(input.desc);
  if (keys.length) desc = desc.replace(/\s*\([^()]*\)\s*$/, "");
  if (desc && name && sameWords(desc, name)) desc = "";
  // a tip with only a line shows it as its name
  if (!name && desc) return { name: desc, keys: keys, desc: "" };
  // a line reads as a sentence, whoever wrote it
  if (desc && !/[.!?\u2026]$/.test(desc)) desc += ".";
  return { name: name, keys: keys, desc: desc };
}

function clean(s) {
  return String(s == null ? "" : s).replace(/\s+/g, " ").trim();
}

function sameWords(a, b) {
  const norm = function (s) {
    return s.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
  };
  return norm(a) === norm(b);
}

// Where a tip goes: under its button, centred on it; above when there is no
// room under it; never outside the window. `arrow` is where the pointer of the
// tip sits, from the tip's left edge.
//   anchor: {left, top, right, bottom} of the button, in the window
//   tip:    {width, height}
//   view:   {width, height} of the window
export function placeTip(anchor, tip, view, gap, margin) {
  gap = gap === undefined ? 8 : gap;
  margin = margin === undefined ? 8 : margin;
  const centre = (anchor.left + anchor.right) / 2;
  const maxLeft = Math.max(margin, view.width - tip.width - margin);
  const left = Math.round(Math.min(maxLeft, Math.max(margin, centre - tip.width / 2)));
  const roomBelow = view.height - anchor.bottom - gap - margin;
  const roomAbove = anchor.top - gap - margin;
  const above = roomBelow < tip.height && roomAbove > roomBelow;
  const top = Math.round(above ? anchor.top - gap - tip.height : anchor.bottom + gap);
  const arrow = Math.round(Math.min(tip.width - 14, Math.max(14, centre - left)));
  return { left: left, top: top, above: above, arrow: arrow };
}
