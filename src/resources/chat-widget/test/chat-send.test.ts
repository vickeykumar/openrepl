// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { definesSomething, answerBudget, errorBlock, errorRequest, looksLikeTask, messagesToSend, selectionRequest, shortenOldCode } from "../src/chat-send.ts";

const longCode = (n: number) => "```python\n" + Array.from({ length: n }, (_, i) => "line_" + i + " = " + i).join("\n") + "\n```";

test("a long code block of an older answer is cut, a short one is not", () => {
  const out = shortenOldCode("Here:\n" + longCode(100) + "\nand\n" + longCode(5));
  assert.match(out, /88 more lines left out/);
  assert.ok(out.includes("line_11 = 11") && !out.includes("line_12 = 12"));
  assert.ok(out.includes("line_4 = 4"), "the short block is whole");
  assert.equal(shortenOldCode("no code here"), "no code here");
});

test("the editor and terminal text goes at the end only when the history does not hold it already", () => {
  const ide = { role: "system", content: "ide now" };
  const history = [
    { role: "system", content: "welcome" },
    { role: "system", content: "ide now" }, // the entry kept up to date at each message
    { role: "user", content: "[user-a] q1" },
    { role: "assistant", content: "a1 " + longCode(80) },
    { role: "user", content: "[user-a] q2" },
    { role: "assistant", content: "a2 " + longCode(80) },
    { role: "user", content: "[user-a] q3" },
    { role: "assistant", content: "a3 " + longCode(80) },
    { role: "user", content: "[user-a] q4" },
  ];
  const same = messagesToSend(history, 2, ide);
  assert.equal(same.length, history.length, "a second copy of the editor was sent");
  // the first answer is old: its code is cut; the last two are whole
  assert.match(same[3].content, /more lines left out/);
  assert.ok(!same[5].content.includes("left out") && !same[7].content.includes("left out"));
  // the page's own history is whole
  assert.ok(!history[3].content.includes("left out"));
  // when the editor changed since, the new text is sent after the conversation
  const changed = messagesToSend(history, 2, { role: "system", content: "ide changed" });
  assert.equal(changed.length, history.length + 1);
  assert.equal(changed[changed.length - 1].content, "ide changed");
  // no mandatory entries at all: the text is sent
  assert.equal(messagesToSend([{ role: "user", content: "q" }], 0, ide).length, 2);
});

test("an answer may be longer for code and shorter for a short question, within bounds", () => {
  assert.equal(answerBudget("Write a function that reverses a list", 800), 1600);
  assert.equal(answerBudget("explain how recursion works step by step", 800), 1600);
  assert.equal(answerBudget("what is a tuple?", 800), 480);
  assert.equal(answerBudget("Why does my program print the same number twice when I run it in a loop with two threads?", 800), 800);
  assert.equal(answerBudget("write a parser", 2000), 2400, "never more than the most");
  assert.equal(answerBudget("hi", 400), 400, "never less than the least");
});

test("a message is a task when it asks for something to be done, not when it asks a question", () => {
  assert.ok(looksLikeTask("Create a Flask app with a login page and run it"));
  assert.ok(looksLikeTask("fix the failing test in auth.py"));
  assert.ok(!looksLikeTask("what does a decorator do?"));
  assert.ok(!looksLikeTask("explain how to add a column"));
  assert.ok(!looksLikeTask("fix it"), "too short to be a task");
  assert.ok(!looksLikeTask(""));
});

test("the last error of the terminal is found, with the lines before it", () => {
  const term = ["$ python3 a.py", "Traceback (most recent call last):", '  File "a.py", line 3, in <module>', "    print(x)", "NameError: name 'x' is not defined", "$ "].join("\n");
  const block = errorBlock(term);
  assert.match(block, /NameError: name 'x' is not defined$/);
  assert.match(block, /Traceback/);
  assert.equal(errorBlock("$ ls\na.py\n$ "), "");
  assert.equal(errorBlock(""), "");
  const many = Array.from({ length: 100 }, (_, i) => "line " + i).concat(["Error: boom"]).join("\n");
  assert.equal(errorBlock(many, 10).split("\n").length, 10);
});

test("the requests for the selection and the error carry the text in a code block", () => {
  assert.match(selectionRequest("x = 1", "Python"), /Explain this Python code[\s\S]*```\nx = 1\n```/);
  assert.match(selectionRequest("x = 1", ""), /Explain this code/);
  assert.ok(selectionRequest("y".repeat(9000), "").length < 4300);
  assert.match(errorRequest("Error: boom"), /printed this error[\s\S]*Error: boom/);
});

test("the apply button is offered for a function or a class, not for a loose statement", () => {
  assert.ok(definesSomething("def area(r):\n    return r"));
  assert.ok(definesSomething("# area\nclass Stack:\n    pass"));
  assert.ok(definesSomething("function add(a, b) {\n}"));
  assert.ok(definesSomething("const sum = (a, b) => a + b;"));
  assert.ok(definesSomething("func Handle(w int) {\n}"));
  assert.ok(definesSomething("public static int max(int a, int b) {\n}"));
  assert.ok(!definesSomething("print(1)\nx = 2"));
  assert.ok(!definesSomething("for i in range(3):\n    pass"));
  assert.ok(!definesSomething(""));
});
