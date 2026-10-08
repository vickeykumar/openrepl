// No `break` and no `continue` in a loop that awaits, anywhere in the widget.
//
// The bundle is built by microbundle, which rewrites every async function into
// promise chains. For loops that await it has got two shapes wrong, silently:
//
//   - a `continue` after an awaited branch was dropped, so the rest of the loop
//     body ran with what the `continue` was there to skip
//     ("Cannot read properties of undefined (reading 'say')": one unreadable
//     answer from the model ended the whole agent task);
//   - a `break` inside a try/finally became a reference to a helper that was
//     never declared ("_interrupt4 is not defined").
//
// TypeScript cannot see either, the source is right. So the pattern is not
// written at all: a loop that awaits ends by its condition (a flag), and a loop
// body that needs to leave early is a function with a `return`. This test reads
// the source and fails on the pattern; test/agent-compiled.test.mjs runs the
// agent's loop as the build tool leaves it.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const here = path.dirname(fileURLToPath(import.meta.url));
const srcDir = path.join(here, "..", "src");

const isLoop = (n) => ts.isForStatement(n) || ts.isForInStatement(n) || ts.isForOfStatement(n) || ts.isWhileStatement(n) || ts.isDoStatement(n);
const isFunction = (n) => ts.isFunctionDeclaration(n) || ts.isFunctionExpression(n) || ts.isArrowFunction(n) || ts.isMethodDeclaration(n) || ts.isConstructorDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n);

// Does the loop await, in its own function (not in a callback inside it)?
function awaits(loop) {
  if (ts.isForOfStatement(loop) && loop.awaitModifier) return true;
  let found = false;
  const visit = (n) => {
    if (found || isFunction(n)) return;
    if (ts.isAwaitExpression(n)) {
      found = true;
      return;
    }
    ts.forEachChild(n, visit);
  };
  ts.forEachChild(loop, visit);
  return found;
}

// The break and continue statements that leave or restart this loop: not the
// ones of a loop or (for break) a switch inside it, not the ones in a callback.
function jumpsOf(loop) {
  const out = [];
  const visit = (n, inSwitch) => {
    if (isFunction(n) || isLoop(n)) return;
    if (ts.isBreakStatement(n) && (!inSwitch || n.label)) out.push(n);
    if (ts.isContinueStatement(n)) out.push(n);
    const sw = inSwitch || ts.isSwitchStatement(n);
    ts.forEachChild(n, (c) => visit(c, sw));
  };
  visit(loop.statement, false);
  return out;
}

// findJumps returns "file:line: break|continue in a loop that awaits" for a source text.
export function findJumps(fileName, text) {
  const sf = ts.createSourceFile(fileName, text, ts.ScriptTarget.Latest, true);
  const found = [];
  const visit = (n) => {
    if (isLoop(n) && awaits(n)) {
      for (const j of jumpsOf(n)) {
        const { line } = sf.getLineAndCharacterOfPosition(j.getStart());
        found.push(fileName + ":" + (line + 1) + ": " + (ts.isBreakStatement(j) ? "break" : "continue") + " in a loop that awaits");
      }
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return found;
}

test("the check finds a break or a continue in a loop that awaits, and only there", () => {
  const f = (code) => findJumps("x.ts", code).map((s) => s.replace(/^x\.ts:\d+: /, ""));
  assert.deepEqual(f("async function a() { for (;;) { await x(); if (y) break; } }"), ["break in a loop that awaits"]);
  assert.deepEqual(f("async function a() { while (z) { if (q) continue; await x(); } }"), ["continue in a loop that awaits"]);
  assert.deepEqual(f("async function a() { for (const v of w) { await x(v); if (v) { if (y) { break; } } } }"), ["break in a loop that awaits"]);
  // a loop that does not await may do as it likes
  assert.deepEqual(f("function a() { for (;;) { if (y) break; else continue; } }"), []);
  assert.deepEqual(f("async function a() { await x(); for (;;) { if (y) break; } }"), []);
  // the break of a switch is the switch's; its continue is the loop's
  assert.deepEqual(f("async function a() { for (;;) { await x(); switch (k) { case 1: break; } } }"), []);
  assert.deepEqual(f("async function a() { for (;;) { await x(); switch (k) { case 1: continue; } } }"), ["continue in a loop that awaits"]);
  // an inner loop without an await owns its own jumps; a callback is another function
  assert.deepEqual(f("async function a() { for (;;) { await x(); for (;;) { break; } list.forEach(function () { return; }); } }"), []);
  assert.deepEqual(f("async function a() { for (;;) { list.map(async (v) => { await v; }); if (y) break; } }"), []);
  // leaving by the condition, or by a return from a function, is the way
  assert.deepEqual(f("async function a() { let go = true; while (go) { go = await step(); } }"), []);
});

test("no loop of the widget that awaits uses break or continue", () => {
  const files = fs.readdirSync(srcDir).filter((f) => f.endsWith(".ts") && f !== "widgetHtmlString.ts");
  assert.ok(files.includes("agent.ts") && files.includes("index.ts"), "the sources were not found in " + srcDir);
  const found = files.flatMap((f) => findJumps(f, fs.readFileSync(path.join(srcDir, f), "utf8")));
  assert.deepEqual(found, [], "rewrite these so the loop ends by its condition:\n" + found.join("\n"));
});
