// What the quick App Store dialog does with its lists (src/page/33-app-store.js): the words a search needs, the category and language filters
// with their counts, and which apps an open file needs. Pure functions: no page, no network.

// -> the lower case words of a search
export function words(q) {
  return String(q || "").toLowerCase().split(/[\s,]+/).filter(Boolean);
}

// -> the text a search looks in: the name, the summary, the category, the package and the languages of an app
export function haystack(v) {
  return [v.name, v.summary, v.category, v.source, (v.repls || []).join(" ")].filter(Boolean).join(" ").toLowerCase();
}

// -> true when every word of the search is in the text of the app (words, not the whole phrase: "json java" finds gson)
export function matches(v, q) {
  const ws = words(q);
  if (!ws.length) return true;
  const h = haystack(v);
  return ws.every((w) => h.indexOf(w) >= 0);
}

// -> the category chips of a list: [{ name, count }] with the biggest first, an app with no category counted under "Other" (last)
export function categories(list) {
  const n = {};
  list.forEach((v) => { const c = v.category || "Other"; n[c] = (n[c] || 0) + 1; });
  return Object.keys(n)
    .sort((a, b) => (a === "Other") - (b === "Other") || n[b] - n[a] || (a < b ? -1 : 1))
    .map((c) => ({ name: c, count: n[c] }));
}

// -> the chips of a list of apps for the apps that a search found: every category of the whole list (in its own order, the biggest first), with
// how many of the found apps it holds, so that a category the search emptied stays on the row, dimmed, and the user can see why the list is empty
export function chipsFor(whole, found) {
  const have = {};
  found.forEach((v) => { const c = v.category || "Other"; have[c] = (have[c] || 0) + 1; });
  return categories(whole).map((c) => ({ name: c.name, count: have[c.name] || 0 }));
}

// -> the languages (REPL ids) the apps of a list work with, sorted
export function languages(list) {
  const s = {};
  list.forEach((v) => (v.repls || []).forEach((r) => { s[r] = true; }));
  return Object.keys(s).sort();
}

// -> the list narrowed by category ("" = all, "Other" = no category) and language ("" = all)
export function narrow(list, category, repl) {
  return list.filter((v) => (!category || (v.category || "Other") === category) && (!repl || (v.repls || []).indexOf(repl) >= 0));
}

// -> the last three names, newest first, with the new one first and no repeat
export function pushRecent(list, name, max) {
  const out = [String(name)].concat((list || []).filter((x) => typeof x === "string" && x !== name));
  return out.slice(0, max || 3);
}

const NAME = /^[@A-Za-z0-9_][A-Za-z0-9_.@/-]{0,127}$/;

// -> the names a file imports, by language: Python modules (top level), Node packages, Go import paths, Java imports, Rust crates
export function importsOf(text, lang) {
  const t = String(text || "").slice(0, 200000);
  const out = {};
  const add = (x) => { if (x && NAME.test(x)) out[x] = true; };
  let m;
  if (lang === "python" || lang === "ipython3" || lang === "python2.7") {
    const re = /^[ \t]*(?:from[ \t]+([A-Za-z_][A-Za-z0-9_]*)[A-Za-z0-9_.]*[ \t]+import\b|import[ \t]+([^\n#]+))/gm;
    while ((m = re.exec(t))) {
      if (m[1]) add(m[1]);
      else m[2].split(",").forEach((p) => add((p.trim().split(/[ \t]+as[ \t]+/)[0] || "").split(".")[0]));
    }
  } else if (lang === "node" || lang === "javascript" || lang === "ts-node") {
    const re = /(?:require\(\s*|from\s+|import\s+)['"]([^'"./][^'"]*)['"]/g;
    while ((m = re.exec(t))) {
      const p = m[1].split("/");
      add(p[0].charAt(0) === "@" ? p.slice(0, 2).join("/") : p[0]);
    }
  } else if (lang === "go" || lang === "yaegi") {
    const re = /^[ \t]*(?:import[ \t]+)?(?:[A-Za-z_.][A-Za-z0-9_]*[ \t]+)?"([^"\n]+\.[^"\n]+\/[^"\n]+)"/gm;
    while ((m = re.exec(t))) add(m[1]);
  } else if (lang === "java") {
    const re = /^[ \t]*import[ \t]+(?:static[ \t]+)?([A-Za-z0-9_.]+)(?:\.\*)?[ \t]*;/gm;
    while ((m = re.exec(t))) add(m[1]);
  } else if (lang === "evcxr") {
    const re = /^[ \t]*(?:use[ \t]+|extern[ \t]+crate[ \t]+|:dep[ \t]+)([A-Za-z_][A-Za-z0-9_]*)/gm;
    while ((m = re.exec(t))) add(m[1]);
  }
  return Object.keys(out);
}

// -> the apps (views) that the imports of a file need and that are not on for the user: an app is needed when an import is its module, or lies
// inside it (a Go path, a Java package). Only apps that the store can give: not failed, not rejected.
export function needed(imports, views) {
  const seen = {};
  const hit = [];
  (views || []).forEach((v) => {
    if (!v.mod || v.on || seen[v.name] || v.state === "failed" || v.state === "rejected" || v.state === "installing") return;
    const ok = imports.some((i) => i === v.mod || i.indexOf(v.mod + "/") === 0 || i.indexOf(v.mod + ".") === 0);
    if (ok) { seen[v.name] = true; hit.push(v); }
  });
  return hit;
}
