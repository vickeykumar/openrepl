import test from "node:test";
import assert from "node:assert/strict";
import { fenced, codeOf, withAction } from "../src/page/lib-actions.mjs";

test("a fence is long enough for the code inside it", () => {
  assert.equal(fenced("x = 1", "python"), "```python\nx = 1\n```");
  const inner = fenced("print('''a\n```\nb''')", "python");
  assert.ok(inner.startsWith("````python\n") && inner.endsWith("\n````"));
});

test("the code of an answer, with or without a fence", () => {
  assert.equal(codeOf("def f():\n    pass\n", "def f():\n    pass"), "def f():\n    pass");
  assert.equal(codeOf("```python\ndef f():\n    pass\n```", "def f():\n    pass"), "def f():\n    pass");
  assert.equal(codeOf("```\nx\n```\n\n", "x"), "x");
  assert.equal(codeOf("````js\nconst a = `\\`\\`\\``;\n````", "x"), "const a = `\\`\\`\\``;");
  // the selection decides about the last new line
  assert.equal(codeOf("a\nb", "a\nb\n"), "a\nb\n");
  assert.equal(codeOf("a\nb\n\n", "a\nb"), "a\nb");
  assert.equal(codeOf("", "x"), "");
  assert.equal(codeOf(null, "x"), "");
  // an answer that only starts with a fence is not unwrapped
  assert.equal(codeOf("Here you go:\n```\nx\n```", "x"), "Here you go:\n```\nx\n```");
});

test("a selection is replaced, tests go after it", () => {
  const base = "import os\ndef f(x):\n    retrun x\nprint(f(1))\n";
  const sel = "def f(x):\n    retrun x";
  const from = base.indexOf(sel);
  const to = from + sel.length;
  assert.equal(withAction(base, from, to, sel, "def f(x):\n    return x", false), "import os\ndef f(x):\n    return x\nprint(f(1))\n");
  assert.equal(
    withAction(base, from, to, sel, "assert f(2) == 2", true),
    "import os\ndef f(x):\n    retrun x\n\nassert f(2) == 2\nprint(f(1))\n"
  );
  // a selection that ends a line: one blank line, not two
  const lines = "a = 1\nb = 2\n";
  assert.equal(withAction(lines, 0, 6, "a = 1\n", "assert a", true), "a = 1\n\nassert a\nb = 2\n");
  // at the end of the file
  assert.equal(withAction("x = 1", 0, 5, "x = 1", "assert x", true), "x = 1\n\nassert x\n");
});
