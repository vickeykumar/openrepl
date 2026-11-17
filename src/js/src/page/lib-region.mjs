// Where a piece of code from a chat answer belongs in the editor: the function or class it
// defines, found in the editor by name, so that "Apply as a diff" can propose that one part
// as a change instead of the whole file. Plain code with no DOM (node runs it in
// test/lib-region.test.mjs); 18-genie-review.js uses it.

// The name a block of code defines on its first line of code, or "" when it is not a definition.
const DEFINITIONS = [
  /^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(/, // Python
  /^\s*class\s+([A-Za-z_]\w*)/, // Python, JavaScript, Java, C++, Ruby
  /^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)\s*\(/, // JavaScript
  /^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>/, // an arrow function
  /^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[(\[]/, // Go
  /^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)/, // Rust
  /^\s*def\s+([A-Za-z_]\w*[?!]?)/, // Ruby
  /^\s*(?:(?:public|private|protected|static|final|abstract|virtual|inline|const|unsigned|extern)\s+)*[A-Za-z_][\w<>\[\]:,*&]*(?:\s+[\w<>\[\]:,*&]+)*[\s*&]+([A-Za-z_]\w*)\s*\([^;]*\)\s*(?:const\s*)?\{?\s*$/, // Java, C, C++, C#
];

export function definedName(code) {
  const lines = String(code || "").split("\n");
  for (const line of lines) {
    if (line.trim() === "" || /^\s*(#|\/\/|\/\*|\*|@|--)/.test(line)) continue; // a comment or a decorator comes first
    for (const re of DEFINITIONS) {
      const m = re.exec(line);
      if (m && !/^(if|for|while|switch|catch|return|else|new|do)$/.test(m[1])) return m[1];
    }
    return "";
  }
  return "";
}

const indentOf = (line) => /^[ \t]*/.exec(line)[0].length;

// The lines (0 based, inclusive) of the definition of `name` that starts at line `at`: by indentation when the
// line ends with a colon (Python), else by braces; "end"/"}" lines are the end of a Ruby or brace body.
function regionFrom(lines, at) {
  const first = lines[at];
  const base = indentOf(first);
  let last = at;
  if (/:\s*(#.*)?$/.test(first.trimEnd()) && !first.includes("{")) {
    for (let i = at + 1; i < lines.length; i++) {
      if (lines[i].trim() === "") continue;
      if (indentOf(lines[i]) <= base) break;
      last = i;
    }
    return [at, last];
  }
  let depth = 0;
  let opened = false;
  for (let i = at; i < lines.length; i++) {
    const text = lines[i].replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`/g, "");
    for (const c of text) {
      if (c === "{") {
        depth++;
        opened = true;
      } else if (c === "}") depth--;
    }
    if (opened && depth <= 0) return [at, i];
    if (!opened && i > at + 3) break; // no body found near the line
  }
  // Ruby and its like: a body that ends with "end" at the same indentation
  for (let i = at + 1; i < lines.length; i++) {
    if (indentOf(lines[i]) === base && /^\s*end\b/.test(lines[i])) return [at, i];
  }
  return null;
}

// Finds the one definition in `text` that `code` replaces. Returns {text: the whole text with the code in its place},
// {error: "..."} when it cannot tell (not a definition, not in the editor, or in it more than once).
export function applyRegion(text, code) {
  const name = definedName(code);
  if (!name) return { error: "This code is not a function or a class, so it is not clear where it goes. Use Insert, or Replace file." };
  const lines = String(text).split("\n");
  const wanted = String(code).replace(/\s+$/, "");
  const starts = [];
  lines.forEach((line, i) => {
    if (definedName(line) === name && (!/^\s*(#|\/\/)/.test(line))) starts.push(i);
  });
  if (starts.length === 0) return { error: name + " is not in the editor, so there is nothing to replace. Use Insert to add it." };
  if (starts.length > 1) return { error: name + " is in the editor " + starts.length + " times, so it is not clear which one to replace. Use Insert, or select it and replace it by hand." };
  const region = regionFrom(lines, starts[0]);
  if (!region) return { error: "Could not find where " + name + " ends in the editor." };
  // the code takes the indentation of the definition it replaces
  const codeLines = wanted.split("\n");
  const codeBase = indentOf(codeLines.find((l) => l.trim() !== "") || "");
  const target = indentOf(lines[starts[0]]);
  const moved = codeLines.map((l) => (l.trim() === "" ? l : target >= codeBase ? " ".repeat(target - codeBase) + l : l.slice(Math.min(codeBase - target, indentOf(l)))));
  return { text: lines.slice(0, region[0]).concat(moved, lines.slice(region[1] + 1)).join("\n"), name, from: region[0] + 1, to: region[1] + 1 };
}
