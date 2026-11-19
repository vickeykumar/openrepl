import { test } from "node:test";
import assert from "node:assert/strict";
import { NOTES_HEADER, NOTES_MAX_LINES, addNote, parseRemember } from "../src/remember.ts";

test("a message that starts with remember is a note", () => {
  assert.equal(parseRemember("remember: use tabs"), "use tabs");
  assert.equal(parseRemember("Remember that tests run with pytest"), "tests run with pytest");
  assert.equal(parseRemember("/remember no semicolons"), "no semicolons");
  assert.equal(parseRemember("remember this - the API key is in .env, never print it"), "the API key is in .env, never print it");
  assert.equal(parseRemember("  remember   two\n lines "), "two lines");
  assert.equal(parseRemember("remember this works fine"), "this works fine");
});

test("other messages are not notes", () => {
  for (const m of ["", "remember", "remember:", "remember how we did the login?", "can you remember this", "please remember: x", "I remember it", "rememberable things"]) assert.equal(parseRemember(m), null, m);
});

test("a note is cut at 400 characters", () => {
  assert.equal(parseRemember("remember " + "x".repeat(900))!.length, 400);
});

test("the first note makes the file with its header; later ones add a line", () => {
  const first = addNote("", "use tabs")!;
  assert.equal(first, NOTES_HEADER + "- use tabs\n");
  assert.equal(addNote(first, "run tests with pytest"), NOTES_HEADER + "- use tabs\n- run tests with pytest\n");
  assert.equal(addNote("My own notes\nrun it with make", "no semicolons"), "My own notes\nrun it with make\n- no semicolons\n");
});

test("the same note twice, and a file that is full, are refused", () => {
  const f = addNote("", "use tabs")!;
  assert.equal(addNote(f, "Use  TABS"), null);
  const full = "x\n".repeat(NOTES_MAX_LINES);
  assert.equal(addNote(full, "one more"), null);
});
