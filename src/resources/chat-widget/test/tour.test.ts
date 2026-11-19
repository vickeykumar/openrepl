import { test } from "node:test";
import assert from "node:assert/strict";
import { TOUR, isTourCommand } from "../src/tour.ts";

test("the tour has a few short steps, each with a target, a title and words", () => {
  assert.ok(TOUR.length >= 3 && TOUR.length <= 5);
  for (const s of TOUR) {
    assert.ok(/^chat-widget__[a-z-]+$/.test(s.target), s.target);
    assert.ok(s.title.length > 0 && s.title.length <= 30, s.title);
    assert.ok(s.text.length > 20 && s.text.length <= 260, s.title + ": " + s.text.length);
  }
  assert.equal(new Set(TOUR.map((s) => s.target)).size, TOUR.length, "no target twice");
});

test("/tour starts the tour again, and only that", () => {
  assert.ok(isTourCommand("/tour"));
  assert.ok(isTourCommand("  /Tour  "));
  for (const m of ["tour", "/tour please", "take the /tour", "", "/tours"]) assert.ok(!isTourCommand(m), m);
});
