// The text work of the right-click actions (19-genie-actions.js), without the
// page, so that node can test it (src/js/test).

// A code fence that the code cannot end early.
export function fenced(code, lang) {
  let fence = "```";
  while (code.indexOf(fence) >= 0) fence += "`";
  return fence + lang + "\n" + code + "\n" + fence;
}

// The code of an answer: the whole answer, or what is inside its fence. The
// selection decides whether it ends a line.
export function codeOf(answer, selection) {
  let code = String(answer || "");
  const fence = code.match(/^\s*(`{3,})[^\n`]*\n([\s\S]*?)\n?\1\s*$/);
  if (fence) code = fence[2];
  code = code.replace(/\s+$/, "");
  return selection.endsWith("\n") ? code + "\n" : code;
}

// The editor's text with the selection [from, to) replaced by code, or, for
// tests, with code after it and a blank line between.
export function withAction(base, from, to, selection, code, after) {
  const rest = base.slice(to);
  if (!after) return base.slice(0, from) + code + rest;
  const gap = selection.endsWith("\n") ? "\n" : "\n\n";
  return base.slice(0, to) + gap + code + (rest.startsWith("\n") ? "" : "\n") + rest;
}
