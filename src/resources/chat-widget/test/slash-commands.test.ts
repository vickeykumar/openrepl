import { test } from "node:test";
import assert from "node:assert/strict";
import { COMMANDS, matchCommands, slashQuery } from "../src/slash-commands.ts";

test("the menu belongs to the first word of a message that starts with a slash", () => {
  assert.equal(slashQuery("/", 1), "");
  assert.equal(slashQuery("/co", 3), "co");
  assert.equal(slashQuery("/Context", 8), "context");
  assert.equal(slashQuery("/tour now", 5), "tour", "the caret is still in the first word");
  assert.equal(slashQuery("/tour now", 9), null, "a space ends the command word");
  assert.equal(slashQuery("", 0), null);
  assert.equal(slashQuery("why /tour", 9), null, "only at the start of the message");
  assert.equal(slashQuery(" /tour", 6), null);
});

test("every command has a name, a line that says what it does, and a kind", () => {
  assert.deepEqual(COMMANDS.map((c) => c.name), ["context", "tour", "add-plugin", "save-skill", "remember"]);
  for (const c of COMMANDS) {
    assert.ok(/^[a-z-]+$/.test(c.name), c.name);
    assert.ok(c.desc.length > 10 && c.desc.length < 90, c.name);
    assert.ok(c.kind === "run" || (c.kind === "insert" && !!c.insert && c.insert.startsWith("/" + c.name)), c.name);
  }
});

test("matching: all for nothing, names that start with it first, then names that contain it, none for a path", () => {
  assert.equal(matchCommands("").length, 5);
  assert.deepEqual(matchCommands("co").map((c) => c.name), ["context"]);
  assert.deepEqual(matchCommands("t").map((c) => c.name), ["tour", "context"], "tour starts with t, context only contains it");
  assert.deepEqual(matchCommands("rem").map((c) => c.name), ["remember"]);
  assert.deepEqual(matchCommands("usr"), []);
  assert.deepEqual(matchCommands("CONTEXT").map((c) => c.name), ["context"]);
});
