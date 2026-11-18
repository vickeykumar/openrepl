// The sections of Site settings (src/resources/js/admin-sections.js): every card of the settings page is in exactly one section,
// the addresses, the search and the counts of changes.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));
const S = require("../../resources/js/admin-sections.js");
const adminSource = readFileSync(join(here, "../../resources/js/admin-site.js"), "utf8");

test("every card of the settings page is in one section, and no section names a card that is not there", () => {
  // the titles of the cards that viewSite draws: card('Title', ...) between viewSite and viewFeedback
  const part = adminSource.slice(adminSource.indexOf("function viewSite()"), adminSource.indexOf("function viewFeedback()"));
  const titles = [...part.matchAll(/\bcard\('([^']+)'/g)].map((m) => m[1]);
  assert.ok(titles.length >= 15, "found " + titles.length + " cards");
  const placed = new Map();
  S.SECTIONS.forEach((s) => S.cardsOf(s).forEach((c) => placed.set(c, (placed.get(c) || 0) + 1)));
  for (const t of titles) assert.equal(placed.get(t), 1, "the card " + t + " is in " + (placed.get(t) || 0) + " sections");
  for (const [c, n] of placed) assert.ok(titles.includes(c), "the section names a card that is not drawn: " + c);
  assert.equal(S.placeOfCard("Genie limits").section, "genie-limits");
  assert.deepEqual(S.placeOfCard("Added models"), { section: "genie-models", tab: "added" });
  assert.equal(S.placeOfCard("Nothing"), null);
});

test("the groups and the ids of the sections are in order and not repeated", () => {
  const ids = S.SECTIONS.map((s) => s.id);
  assert.equal(new Set(ids).size, ids.length);
  S.SECTIONS.forEach((s) => assert.ok(S.GROUPS.some((g) => g.id === s.group), s.id + " has a group"));
  assert.ok(S.sectionById(S.DEFAULT_SECTION));
});

test("an address names a section and a setting; an unknown section is the first one", () => {
  assert.deepEqual(S.parseHash("#site/genie-limits/history"), { view: "site", section: "genie-limits", anchor: "history" });
  assert.deepEqual(S.parseHash("#site"), { view: "site", section: "", anchor: "" });
  assert.deepEqual(S.parseHash("#site/nonsense"), { view: "site", section: "general", anchor: "" });
  assert.deepEqual(S.parseHash("#overview"), { view: "overview", section: "", anchor: "" });
  assert.equal(S.hashFor("genie-agent", "agent-steps"), "#site/genie-agent/agent-steps");
  assert.equal(S.hashFor("general"), "#site/general");
  assert.equal(S.slug("Characters before a chat is summarized"), "characters-before-a-chat-is-summarized");
});

test("the search needs every word, and puts a word of the label before a word of the help", () => {
  const entries = [
    { section: "genie-chats", card: "Usage record and credits", sectionTitle: "Chats and credits", label: "Tokens before a chat is summarized", help: "Empty: 6000.", anchor: "sum-tokens" },
    { section: "genie-limits", card: "Genie limits", sectionTitle: "Limits and context", label: "The conversation length", help: "How many messages are kept and summarized.", anchor: "history" },
    { section: "general", card: "Announcement", sectionTitle: "General", label: "Text", help: "A banner on top of every page.", anchor: "ann" },
  ];
  const r = S.search(entries, "summar");
  assert.deepEqual(r.map((e) => e.anchor), ["sum-tokens", "history"]);
  assert.deepEqual(S.search(entries, "chat summarized").map((e) => e.anchor), ["sum-tokens"], "every word");
  assert.deepEqual(S.search(entries, "banner").map((e) => e.anchor), ["ann"]);
  assert.deepEqual(S.search(entries, "limits").map((e) => e.anchor), ["history"], "the card title counts, less");
  assert.deepEqual(S.search(entries, "   "), []);
  assert.deepEqual(S.search(entries, "zzz"), []);
});

test("the changes of a section are counted by what differs, and said in a line", () => {
  assert.equal(S.changeCount({ a: "1", b: "x" }, { a: "1", b: "x" }), 0);
  assert.equal(S.changeCount({ a: "1", b: "x" }, { a: "2", b: "x" }), 1);
  assert.equal(S.changeCount({ a: "1" }, { a: "1", c: "new row" }), 1, "a row that was added");
  assert.equal(S.changeCount({ a: "1", b: "x" }, { a: "1" }), 1, "a row that was removed");
  assert.equal(S.changeSummary({}), "");
  assert.equal(S.changeSummary({ general: 1 }), "1 change: 1 in General");
  assert.equal(S.changeSummary({ "genie-models": 2, general: 1 }), "3 changes: 1 in General, 2 in Models and prices");
});
