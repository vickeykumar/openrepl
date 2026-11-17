// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { JUMP_AT, cardView, jumpLabel, jumpVisible, showLabel, waitingText } from "../src/wait-bar.ts";

test("the arrow to the last message is shown only when the user has scrolled up", () => {
  assert.equal(jumpVisible(0, true), false);
  assert.equal(jumpVisible(-JUMP_AT, true), false);
  assert.equal(jumpVisible(-(JUMP_AT + 1), true), true);
  assert.equal(jumpVisible(JUMP_AT + 1, true), true, "an engine that counts upwards");
  assert.equal(jumpVisible(-500, false), false, "nothing to scroll");
});

test("the card that waits is in view, above it or below it", () => {
  const box = { top: 100, bottom: 500 };
  assert.equal(cardView({ top: 300, bottom: 400 }, box), "in");
  assert.equal(cardView({ top: -200, bottom: -50 }, box), "above");
  assert.equal(cardView({ top: 600, bottom: 700 }, box), "below");
  assert.equal(cardView({ top: 60, bottom: 160 }, box), "above", "only 60% of it shows");
  assert.equal(cardView({ top: 80, bottom: 150 }, box), "in", "most of it shows");
  assert.equal(cardView({ top: 50, bottom: 900 }, box), "in", "a card taller than the conversation, which fills it");
  assert.equal(cardView({ top: 0, bottom: 0 }, box), "in", "not drawn yet");
});

test("the words of the bar and of the arrow", () => {
  assert.equal(waitingText("paused at the step limit"), "Waiting for you: paused at the step limit");
  assert.equal(showLabel("above"), "Show ↑");
  assert.equal(showLabel("below"), "Show ↓");
  assert.equal(showLabel(null), "Show");
  assert.equal(jumpLabel(0), "");
  assert.equal(jumpLabel(3), "Latest · 3 new");
});
