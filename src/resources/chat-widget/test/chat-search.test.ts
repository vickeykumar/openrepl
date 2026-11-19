import { test } from "node:test";
import assert from "node:assert/strict";
import { markWords, modeOf, searchChats, snippetAround, textsOf, wordsOf } from "../src/chat-search.ts";
import { groupChats, restore, tabToClose } from "../src/chat-tabs.ts";
import type { ChatLine, ChatSummary } from "../src/chat-tabs.ts";

const chat = (id: string, title: string, updated: number, extra: Partial<ChatSummary> = {}): ChatSummary => ({ id, title, updated, messages: 2, size: 100, ...extra });
const lines = (...l: ChatLine[]): ChatLine[] => l;

const chats = [chat("a", "Starfall Salvager preview", 300, { starred: true }), chat("b", "make a web page", 200), chat("c", "solve it", 100)];
const bodies = new Map<string, ChatLine[]>([
  ["a", lines({ t: "state", mode: "agent" }, { t: "user", text: "why does the click say a stale ref?" }, { t: "answer", text: "e2 is not on the page now: a reload starts the numbers again" })],
  ["b", lines({ t: "state", mode: "chat" }, { t: "user", text: "make a web page for my shop" }, { t: "answer", text: "Here is a page. The stale ref error comes when the page redraws." })],
  ["c", lines({ t: "state", mode: "agent" }, { t: "user", text: "solve it" }, { t: "task", text: "the numbers start again at e1", outcome: "done" })],
]);

test("a word is found in a title or in what was written, with every word needed, newest first", () => {
  const r = searchChats(chats, bodies, "stale ref", "all");
  assert.deepEqual(r.map((x) => x.chat.id), ["a", "b"]);
  assert.match(r[0].snippet, /stale ref/);
  assert.equal(searchChats(chats, bodies, "starfall", "all")[0].snippet, "", "a match in the title only has no snippet");
  assert.deepEqual(searchChats(chats, bodies, "NUMBERS again", "all").map((x) => x.chat.id), ["a", "c"]);
  assert.deepEqual(searchChats(chats, bodies, "nothing like this", "all"), []);
});

test("the chips: starred, chat and agent, alone or with words", () => {
  assert.deepEqual(searchChats(chats, bodies, "", "starred").map((x) => x.chat.id), ["a"]);
  assert.deepEqual(searchChats(chats, bodies, "", "chat").map((x) => x.chat.id), ["b"]);
  assert.deepEqual(searchChats(chats, bodies, "", "agent").map((x) => x.chat.id), ["a", "c"]);
  assert.deepEqual(searchChats(chats, bodies, "stale", "agent").map((x) => x.chat.id), ["a"]);
  // a chat that is not read yet is shown under Chat and Agent, and matches by its title
  assert.deepEqual(searchChats(chats, new Map(), "web", "chat").map((x) => x.chat.id), ["b"]);
});

test("the steps of a task are not searched, its summary is", () => {
  const l = lines({ t: "task", text: "make the shop" }, { t: "user", text: "hello" });
  assert.deepEqual(textsOf(l), ["make the shop", "hello"]);
  assert.equal(modeOf(lines({ t: "state", mode: "agent" }, { t: "state", mode: "chat" })), "chat");
  assert.equal(modeOf(null), "chat");
});

test("a snippet is about 80 characters around the word, and the word is marked", () => {
  const long = "word ".repeat(30) + "NEEDLE " + "word ".repeat(30);
  const s = snippetAround(long, "needle");
  assert.ok(s.length <= 84 && /NEEDLE/.test(s) && s.startsWith("…") && s.endsWith("…"));
  assert.deepEqual(markWords("a stale ref here", wordsOf("Stale ref")), [{ text: "a ", hit: false }, { text: "stale", hit: true }, { text: " ", hit: false }, { text: "ref", hit: true }, { text: " here", hit: false }]);
  assert.deepEqual(markWords("plain", []), [{ text: "plain", hit: false }]);
  assert.equal(markWords("a (b) c", ["(b)"]).some((p) => p.hit), true, "characters of a pattern are taken as they are");
});

test("History puts the starred chats first, and a starred tab is not the one that closes", () => {
  const g = groupChats(chats, ["b"], new Date(1000 * 1000));
  assert.equal(g[0].label, "Starred");
  assert.deepEqual(g[0].chats.map((c) => c.id), ["a"]);
  assert.equal(g[1].label, "Open");
  const tabs = [{ id: "x", running: false, needsYou: false, unread: false, busy: false, starred: true }, { id: "y", running: false, needsYou: false, unread: false, busy: false }, { id: "z", running: false, needsYou: false, unread: false, busy: false }];
  assert.equal(tabToClose(tabs, "z"), "y");
  assert.equal(tabToClose([tabs[0]], "q"), null);
});

test("the last star line decides whether a chat is starred when it is restored", () => {
  assert.equal(restore(lines({ t: "meta" }, { t: "star", on: 1 })).starred, true);
  assert.equal(restore(lines({ t: "meta" }, { t: "star", on: 1 }, { t: "star", on: 0 })).starred, false);
  assert.equal(restore(lines({ t: "meta" })).starred, false);
});
