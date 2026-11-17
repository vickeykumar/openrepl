// node --test test/   (from src/js)
import test from "node:test";
import assert from "node:assert/strict";
import { applyRegion, definedName } from "../src/page/lib-region.mjs";

test("the name a block of code defines is found in the common languages", () => {
  assert.equal(definedName("def area(r):\n    return r * r"), "area");
  assert.equal(definedName("async def fetch(url):\n    pass"), "fetch");
  assert.equal(definedName("class Stack:\n    pass"), "Stack");
  assert.equal(definedName("# a comment\n@decorator\ndef f():\n    pass"), "f");
  assert.equal(definedName("function add(a, b) {\n  return a + b;\n}"), "add");
  assert.equal(definedName("export default async function main() {}"), "main");
  assert.equal(definedName("const sum = (a, b) => a + b;"), "sum");
  assert.equal(definedName("func (s *Server) Handle(w int) {\n}"), "Handle");
  assert.equal(definedName("pub fn parse(x: &str) -> i32 {\n}"), "parse");
  assert.equal(definedName("public static int max(int a, int b) {\n  return a;\n}"), "max");
  assert.equal(definedName("int main(void) {\n  return 0;\n}"), "main");
  assert.equal(definedName("x = 1\nprint(x)"), "");
  assert.equal(definedName("if (x) {\n}"), "");
  assert.equal(definedName(""), "");
});

test("a Python function is replaced by its body's indentation, and the rest of the file stays", () => {
  const editor = ["import math", "", "def area(r):", "    return r * r", "", "def main():", "    print(area(2))", "", "main()"].join("\n");
  const r = applyRegion(editor, "def area(r):\n    return math.pi * r * r\n");
  assert.equal(r.text, ["import math", "", "def area(r):", "    return math.pi * r * r", "", "def main():", "    print(area(2))", "", "main()"].join("\n"));
  assert.equal(r.name, "area");
  assert.equal(r.from, 3);
  assert.equal(r.to, 4);
});

test("a method keeps its indentation inside its class", () => {
  const editor = ["class A:", "    def f(self):", "        return 1", "", "    def g(self):", "        return 2"].join("\n");
  const r = applyRegion(editor, "def f(self):\n    return 10\n    # more\n");
  assert.equal(r.text, ["class A:", "    def f(self):", "        return 10", "        # more", "", "    def g(self):", "        return 2"].join("\n"));
});

test("a brace function is replaced as far as its closing brace, strings with braces do not count", () => {
  const editor = ['function a() {', '  const s = "}";', '  return 1;', '}', '', 'function b() {', '  return 2;', '}'].join("\n");
  const r = applyRegion(editor, "function a() {\n  return 5;\n}\n");
  assert.equal(r.text, ["function a() {", "  return 5;", "}", "", "function b() {", "  return 2;", "}"].join("\n"));
});

test("it says why it cannot tell where the code goes", () => {
  assert.match(applyRegion("x = 1\n", "print(1)").error, /not a function or a class/);
  assert.match(applyRegion("x = 1\n", "def f():\n    pass").error, /f is not in the editor/);
  assert.match(applyRegion("def f():\n    pass\ndef f():\n    pass\n", "def f():\n    return 1").error, /f is in the editor 2 times/);
  assert.match(applyRegion("def f(\n", "def f(a):\n    pass").error || "", /./);
});
