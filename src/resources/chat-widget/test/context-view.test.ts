import { test } from "node:test";
import assert from "node:assert/strict";
import { contextRows, estimateTokens, fmtSize, totalChars } from "../src/context-view.ts";
import type { ContextFacts } from "../src/context-view.ts";

const full = (over: Partial<ContextFacts> = {}): ContextFacts => ({
  editor: { on: true, file: "index.html", chars: 7300, total: 7300, lines: 214, limit: 12000 },
  terminal: { lines: 6, chars: 400, limit: 4000 },
  preview: { on: true, open: true, chars: 1900, parts: 4 },
  notes: { known: true, exists: true, lines: 3, chars: 400 },
  chat: { sent: 12, total: 31, chars: 3100 },
  attached: { names: ["style.css", "app.js"], chars: 2200 },
  hidden: 0,
  ...over,
});
const by = (rows: ReturnType<typeof contextRows>, id: string) => rows.find((r) => r.id === id)!;

test("every part of a message is a line, in a fixed order, with the state of what is sent", () => {
  const rows = contextRows(full());
  assert.deepEqual(rows.map((r) => r.id), ["editor", "terminal", "preview", "notes", "chat", "attached", "other"]);
  assert.equal(by(rows, "editor").state, "sent");
  assert.equal(by(rows, "editor").size, "7.1 KB");
  assert.equal(by(rows, "editor").count, "214 lines");
  assert.equal(by(rows, "preview").count, "4 headings");
  assert.equal(by(rows, "chat").count, "12 of 31");
  assert.match(by(rows, "chat").detail, /last 12 messages/);
  assert.equal(by(rows, "attached").state, "yours");
  assert.match(by(rows, "attached").detail, /style.css, app.js/);
  assert.equal(by(rows, "other").state, "off");
});

test("what is not there says why, and a switch that is off says what to click", () => {
  const rows = contextRows(full({ editor: { on: true, file: "", chars: 0, total: 0, lines: 0, limit: 12000 }, preview: { on: false, open: false, chars: 0, parts: 0 }, terminal: { lines: 0, chars: 0, limit: 4000 }, notes: { known: true, exists: false, lines: 0, chars: 0 }, chat: { sent: 0, total: 0, chars: 0 }, attached: { names: [], chars: 0 } }));
  assert.equal(by(rows, "editor").state, "empty");
  assert.equal(by(rows, "terminal").state, "empty");
  assert.equal(by(rows, "preview").state, "off");
  assert.match(by(rows, "preview").hint || "", /Click the chip/);
  assert.match(by(rows, "notes").name, /no GENIE.md yet/);
  assert.match(by(rows, "notes").detail, /remember:/);
  assert.equal(by(rows, "chat").state, "empty");
  assert.equal(by(rows, "attached").state, "empty");
  assert.equal(totalChars(rows), 0);
});

test("an editor that is cut says how much of it goes, and an editor that is not shared is not sent", () => {
  const cut = by(contextRows(full({ editor: { on: true, file: "big.py", chars: 12000, total: 31000, lines: 400, limit: 12000 } })), "editor");
  assert.match(cut.detail, /first 12,000 characters of 31,000/);
  const off = by(contextRows(full({ editor: { on: false, file: "x", chars: 0, total: 5, lines: 1, limit: 12000 } })), "editor");
  assert.equal(off.state, "off");
  assert.equal(off.chars, 0);
});

test("hidden files are counted once and never named", () => {
  const rows = contextRows(full({ hidden: 2 }));
  assert.match(by(rows, "other").detail, /2 hidden files are never sent/);
  assert.match(by(contextRows(full()), "other").detail, /start with a dot are never sent/);
});

test("the estimate is four characters to a token, in hundreds, and only what is sent counts", () => {
  assert.equal(estimateTokens(17200), 4300);
  const rows = contextRows(full());
  assert.equal(totalChars(rows), 7300 + 400 + 1900 + 400 + 3100 + 2200);
  assert.equal(fmtSize(950), "950 B");
  assert.equal(fmtSize(12000), "12 KB");
  assert.equal(fmtSize(0), "");
});
