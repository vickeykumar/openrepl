// Finding a file by a few letters of its name ("Go to file", 25-file-search.js and the palette), without the page, so that
// node can test it (src/js/test).
//
// A file is { path, name, dir, open } (open: it has a tab; dir: its folder, shown beside the name). A search gives the
// best files first, each with `indices`, the positions in the NAME that matched, to draw them underlined.

export const MAX_RESULTS = 50;

const isBoundary = (name, i) => {
  if (i === 0) return true;
  const prev = name[i - 1];
  if (prev === "." || prev === "_" || prev === "-" || prev === " " || prev === "/") return true;
  // a capital after a small letter: camelCase
  return prev === prev.toLowerCase() && prev !== prev.toUpperCase() && name[i] !== name[i].toLowerCase();
};

// Where the letters of `q` sit in `text`, in order (not next to each other), or null. A whole run of the text is taken
// when there is one; else each letter prefers the start of a word (after . _ - or a capital) within reach.
function subsequence(text, q) {
  const t = text.toLowerCase();
  const at = t.indexOf(q);
  if (at >= 0) return Array.from({ length: q.length }, (_, k) => at + k);
  const out = [];
  let from = 0;
  for (const ch of q) {
    let found = -1;
    let firstAny = -1;
    for (let i = from; i < t.length; i++) {
      if (t[i] !== ch) continue;
      if (firstAny < 0) firstAny = i;
      if (isBoundary(text, i)) { found = i; break; }
      // a letter that is not at a word start is taken only when no word start is near (within 6 letters)
      if (i - firstAny > 6) break;
    }
    if (found < 0) found = firstAny;
    if (found < 0) return null;
    out.push(found);
    from = found + 1;
  }
  return out;
}

function scoreOf(file, q, hit, inName) {
  let score = inName ? 1000 : 0;
  const name = file.name.toLowerCase();
  if (inName) {
    if (name === q) score += 600;
    else if (name.startsWith(q)) score += 400;
    else if (name.includes(q)) score += 250;
    // words that start at a matched letter, and letters that sit together
    let runs = 0;
    for (let k = 0; k < hit.length; k++) {
      if (isBoundary(file.name, hit[k])) score += 12;
      if (k > 0 && hit[k] === hit[k - 1] + 1) runs++;
    }
    score += runs * 6;
    score -= hit[0] * 2; // a match that starts late is worse
    score -= (hit[hit.length - 1] - hit[0] - (hit.length - 1)) * 3; // and one that is spread out
  }
  score -= Math.min(file.name.length, 60) * 0.5; // among equals, the short name
  if (file.open) score += 150;
  return score;
}

// files: the list; query: what was typed; returns [{ file, indices, score }] (at most max).
export function searchFiles(files, query, max = MAX_RESULTS) {
  const q = String(query || "").trim().toLowerCase().replace(/\s+/g, "");
  const byName = (a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()) || a.path.localeCompare(b.path);
  if (!q) {
    // nothing typed: the open files first (in the order given), then the others by name
    const open = files.filter((f) => f.open);
    const rest = files.filter((f) => !f.open).sort(byName);
    return open.concat(rest).slice(0, max).map((file) => ({ file, indices: [], score: 0 }));
  }
  const found = [];
  for (const file of files) {
    let hit = subsequence(file.name, q);
    let inName = true;
    if (!hit) {
      // not in the name: the whole path may still hold the letters (a folder name plus a file name)
      const full = file.dir ? file.dir + "/" + file.name : file.name;
      if (!subsequence(full, q)) continue;
      inName = false;
      hit = [];
    }
    found.push({ file, indices: hit, score: scoreOf(file, q, hit, inName) });
  }
  found.sort((a, b) => b.score - a.score || byName(a.file, b.file));
  return found.slice(0, max);
}

// The folder to show beside a file: its path under the workspace, without the file name ("" for the top).
export function dirOf(path, home) {
  const cut = path.lastIndexOf("/");
  let dir = cut < 0 ? "" : path.slice(0, cut);
  if (home && (dir === home || dir.startsWith(home + "/"))) dir = dir.slice(home.length).replace(/^\//, "");
  return dir;
}
