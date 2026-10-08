import test from "node:test";
import assert from "node:assert/strict";
import { MAX_HINTS, askedText, carriesCode, hintsUsed, questionKey, withHint } from "../src/coach.ts";

test("what the user is shown as having asked", () => {
  assert.equal(askedText("hint", 2), "Hint 2 of 3");
  assert.equal(askedText("review", 0), "Review my solution");
  assert.match(askedText("complexity", 0), /complexity/);
});

test("hints are counted per question, to three", () => {
  let store: unknown = null;
  assert.equal(hintsUsed(store, "two-sum"), 0);
  store = withHint(store, "two-sum");
  store = withHint(store, "two-sum");
  store = withHint(store, "valid-parentheses");
  assert.equal(hintsUsed(store, "two-sum"), 2);
  assert.equal(hintsUsed(store, "valid-parentheses"), 1);
  for (let i = 0; i < 5; i++) store = withHint(store, "two-sum");
  assert.equal(hintsUsed(store, "two-sum"), MAX_HINTS);
  // nonsense in the store counts as nothing
  for (const bad of [undefined, "x", 5, [], { a: "2" }, { a: -1 }, { a: 1.5 }, { a: 99 }]) {
    const n = hintsUsed(bad, "a");
    assert.ok(n === 0 || (bad as any).a === 99, JSON.stringify(bad));
  }
  assert.equal(hintsUsed({ a: 99 }, "a"), MAX_HINTS);
});

test("the store keeps the newest two hundred questions", () => {
  let store: Record<string, number> = {};
  for (let i = 0; i < 250; i++) store = withHint(store, "q" + i);
  assert.equal(Object.keys(store).length, 200);
  assert.equal(hintsUsed(store, "q249"), 1);
  assert.equal(hintsUsed(store, "q0"), 0);
  // using one again moves it to the end
  store = withHint(store, "q60");
  assert.equal(Object.keys(store).pop(), "q60");
});

test("the question is the name in the address", () => {
  assert.equal(questionKey("?name=two-sum", "/practice"), "two-sum");
  assert.equal(questionKey("", "/practice"), "/practice");
  assert.equal(questionKey("?name=", "/practice"), "/practice");
  assert.equal(questionKey("?name=" + "x".repeat(300), "/practice").length, 120);
});

test("a hint with code in it is found", () => {
  assert.equal(carriesCode("Think about what you need to remember."), false);
  assert.equal(carriesCode("Use a map:\n```python\nseen = {}\n```"), true);
  assert.equal(carriesCode("Like this:\n~~~\nx\n~~~"), true);
  assert.equal(carriesCode("Try:\n    seen = {}\n    for x in nums:\n        pass"), true);
  assert.equal(carriesCode("One indented line is a list item:\n    - remember the numbers"), false);
  assert.equal(carriesCode("Use `dict` to look things up in O(1)."), false);
});
