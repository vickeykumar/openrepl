// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { afterClose, clock, filterChats, fitText, groupChats, MAX_LINE_TEXT, moveTab, newChatId, restore, sizeText, staleForAgent, staleNote, staleParts, tabMark, tabToClose, titleFrom, toHistory } from "../src/chat-tabs.ts";

test("a chat id is eight letters and digits, and differs", () => {
  const a = newChatId();
  assert.match(a, /^[a-z0-9]{8}$/);
  assert.notEqual(a, newChatId());
  let n = 0;
  assert.match(newChatId(() => (n++ % 10) / 10), /^[a-z0-9]{8}$/);
});

test("a title is the first message in one line, cut at a word", () => {
  assert.equal(titleFrom("why does my loop print 0 ten times"), "why does my loop print 0 ten times");
  assert.equal(titleFrom("[user-ab12] fix this\n\nplease"), "fix this please");
  assert.equal(titleFrom("explain ```python\nx=1\n``` here"), "explain here");
  assert.equal(titleFrom("   "), "New chat");
  const long = titleFrom("explain how a hash map works and why the lookup is fast even when the table is large and busy");
  assert.ok(long.length <= 62 && long.endsWith("…") && !long.includes("  "));
  assert.equal(titleFrom("x".repeat(100)).length, 61);
});

test("a long text is cut to fit a line of a chat", () => {
  assert.equal(fitText("short"), "short");
  assert.ok(fitText("x".repeat(MAX_LINE_TEXT + 50)).length < MAX_LINE_TEXT + 60);
  assert.match(fitText("x".repeat(MAX_LINE_TEXT + 50)), /cut: too long/);
  assert.match(clock(new Date(2026, 10, 17, 9, 5).getTime()), /^09:05$/);
});

const lines = [
  { t: "meta", id: "k3x9", context: "chat", created: "2026-11-17T14:52:00+05:30" },
  { t: "title", text: "loop prints 0" },
  { t: "state", mode: "chat", model: "gpt-4o-mini" },
  { t: "user", ts: 10, text: "why does my loop print 0", file: "main.py", language: "Python" },
  { t: "answer", ts: 11, model: "gpt-4o-mini", tokens: 412, text: "The variable is set again" },
  { t: "task", ts: 12, text: "Add checks", steps: 6, checked: "pytest -q", changed: "auth.py +9 −2", outcome: "done" },
  { t: "title", text: "Add input checks" },
  { t: "state", mode: "agent" },
] as any[];

test("a chat file is read as a conversation: the last title and state win, the messages are in order", () => {
  const r = restore(lines);
  assert.equal(r.title, "Add input checks");
  assert.equal(r.mode, "agent");
  assert.equal(r.model, "gpt-4o-mini", "an older model stays when a later state has none");
  assert.equal(r.language, "Python");
  assert.equal(r.file, "main.py");
  assert.deepEqual(r.messages.map((m) => m.kind), ["user", "answer", "task"]);
  assert.equal(r.messages[2].summary, "Finished, 6 steps, checked with pytest -q, changed auth.py +9 −2");
  assert.equal(r.messages[1].tokens, 412);
});

test("a chat with no title gets one from its first message, and odd lines are ignored", () => {
  const r = restore([{ t: "meta" }, null, "text", { t: "user", text: "what is a tuple" }, { t: "mystery" }] as any);
  assert.equal(r.title, "what is a tuple");
  assert.equal(r.messages.length, 1);
  assert.equal(restore([]).title, "New chat");
});

test("the conversation of a restored chat is the user's words and the answers, tagged as the page tags them", () => {
  const r = restore(lines);
  assert.deepEqual(toHistory(r.messages, "u1"), [
    { role: "user", content: "[user-u1] why does my loop print 0" },
    { role: "assistant", content: "The variable is set again" },
  ]);
});

const tab = (id: string, o: any = {}) => ({ id, running: false, needsYou: false, unread: false, busy: false, ...o });

test("the strip says a ring for a task, a dot for what is ready, and the question first", () => {
  assert.equal(tabMark(tab("a"), true), "none");
  assert.equal(tabMark(tab("a", { running: true }), false), "ring");
  assert.equal(tabMark(tab("a", { running: true, needsYou: true }), false), "needs");
  assert.equal(tabMark(tab("a", { unread: true }), false), "dot");
  assert.equal(tabMark(tab("a", { unread: true }), true), "none", "the shown tab has nothing unread");
  assert.equal(tabMark(tab("a", { busy: true }), false), "ring");
  assert.equal(tabMark(tab("a", { busy: true }), true), "none");
});

test("the tab that closes for a new one is the oldest that is idle and not shown", () => {
  const tabs = [tab("a", { running: true }), tab("b", { unread: true }), tab("c"), tab("d")];
  assert.equal(tabToClose(tabs, "d"), "c");
  assert.equal(tabToClose(tabs, "c"), "d");
  assert.equal(tabToClose([tab("a", { running: true }), tab("b", { needsYou: true })], "x"), null);
  assert.equal(tabToClose([tab("a", { busy: true })], "x"), null);
});

test("moving and closing tabs", () => {
  assert.deepEqual(moveTab(["a", "b", "c"], "a", 2), ["b", "c", "a"]);
  assert.deepEqual(moveTab(["a", "b", "c"], "c", 0), ["c", "a", "b"]);
  assert.deepEqual(moveTab(["a", "b"], "x", 0), ["a", "b"]);
  assert.deepEqual(moveTab(["a", "b"], "a", 99), ["b", "a"]);
  assert.equal(afterClose(["a", "b", "c"], "b"), "c");
  assert.equal(afterClose(["a", "b", "c"], "c"), "b");
  assert.equal(afterClose(["a"], "a"), null);
  assert.equal(afterClose(["a", "b"], "x"), "a");
});

const chat = (id: string, title: string, updated: number, language = "") => ({ id, title, updated, messages: 2, size: 100, language });

test("all chats are grouped: the open ones first, then the closed by day", () => {
  const now = new Date(2026, 10, 17, 15, 0);
  const t = (d: number, h = 10) => new Date(2026, 10, d, h, 0).getTime() / 1000;
  const groups = groupChats([chat("a", "open one", t(16)), chat("b", "today", t(17)), chat("c", "yesterday", t(16, 20)), chat("d", "old", t(2)), chat("e", "another open", t(1))], ["a", "e"], now);
  assert.deepEqual(groups.map((g) => g.label), ["Open", "Closed today", "Closed yesterday", "Closed earlier"]);
  assert.deepEqual(groups[0].chats.map((c) => c.id), ["a", "e"]);
  assert.deepEqual(groupChats([], [], now), []);
});

test("the chats are searched by every word of the title or language", () => {
  const all = [chat("a", "Flask login page", 1, "Python"), chat("b", "segfault when I free twice", 2, "C"), chat("c", "loop prints 0", 3, "Python")];
  assert.deepEqual(filterChats(all, "flask").map((c) => c.id), ["a"]);
  assert.deepEqual(filterChats(all, "PYTHON loop").map((c) => c.id), ["c"]);
  assert.equal(filterChats(all, "  ").length, 3);
  assert.equal(filterChats(all, "nothing like it").length, 0);
});

test("sizes, and the note when the editor is not what the chat was about", () => {
  assert.equal(sizeText(80), "80 B");
  assert.equal(sizeText(2048), "2 KB");
  assert.equal(sizeText(1.4 * 1024 * 1024), "1.4 MB");
  assert.equal(staleNote({ file: "main.py", language: "Python" }, { file: "main.py", language: "Python" }), "");
  assert.equal(staleNote({ file: "", language: "" }, { file: "x.go", language: "Go" }), "");
  assert.match(staleNote({ file: "main.py", language: "Python" }, { file: "util.go", language: "Go" }), /This chat was about main\.py \(Python\)\. The editor now has util\.go \(Go\)\./);
  assert.match(staleNote({ file: "", language: "Python" }, { file: "", language: "" }), /The editor now has something else/);
});

test("the stale note keeps a dot in a file name, and an unnamed file is said so", () => {
  const p = staleParts({ file: "index.html", language: "JavaScript" }, { file: "", language: "JavaScript" });
  assert.deepEqual(p, { was: "index.html (JavaScript)", is: "an unnamed file (JavaScript)" });
  assert.equal(staleParts({ file: "a.py", language: "Python" }, { file: "a.py", language: "Python" }), null);
  assert.match(staleForAgent({ file: "index.html", language: "JavaScript" }, { file: "main.c", language: "C" }), /about index\.html \(JavaScript\), but the editor now has main\.c \(C\).*set_language.*files_open/);
  assert.equal(staleForAgent({ file: "", language: "" }, { file: "main.c", language: "C" }), "");
});

import { estimateTokens, SUMMARY_KEEP, SUMMARY_PREFIX, applySummary, cleanSummary, summaryInput, summaryPlan } from "../src/chat-tabs.ts";

test("a long chat is summarized once its older turns pass the limit, and the summary stands in their place", () => {
  const base = [{ role: "system", content: "prompt" }, { role: "system", content: "editor" }];
  const turn = (i: number, n: number) => ({ role: i % 2 ? "assistant" : "user", content: "t" + i + " " + "x".repeat(n) });
  const turns = Array.from({ length: 10 }, (_, i) => turn(i, 1000));
  const h = base.concat(turns);
  assert.equal(summaryPlan(h, 2, 0), null, "off");
  assert.equal(summaryPlan(h, 2, 2000), null, "6 older turns of about 250 tokens are 1500, under 2000");
  assert.equal(summaryPlan(h.slice(0, 2 + SUMMARY_KEEP), 2, 100), null, "nothing older than the last turns");
  assert.equal(summaryPlan(base.concat(Array.from({ length: 10 }, (_, i) => turn(i, 10))), 2, 1000), null, "short");
  const plan = summaryPlan(h, 2, 1000)!; // 6 older turns of about 250 tokens
  assert.equal(plan.older.length, 6);
  assert.equal(plan.drop, 6);
  const next = applySummary(h, 2, plan, "The user builds a shop site.");
  assert.equal(next.length, 2 + 1 + SUMMARY_KEEP);
  assert.equal(next[2].content, SUMMARY_PREFIX + "The user builds a shop site.");
  assert.equal(next[3].content, h[8].content);
  // a second time: the old summary goes in, and is replaced
  const more = next.concat(Array.from({ length: 8 }, (_, i) => turn(i + 10, 1000)));
  const plan2 = summaryPlan(more, 2, 1000)!;
  assert.equal(plan2.prior, "The user builds a shop site.");
  assert.equal(plan2.drop, 1 + 8);
  assert.match(summaryInput(plan2), /^Summary so far: The user builds a shop site\.\n\nTurns to add:\nUser: t6 /);
  assert.equal(applySummary(more, 2, plan2, "S2")[2].content, SUMMARY_PREFIX + "S2");
});

test("a summary the model wrote is used only when it is text of some length", () => {
  assert.equal(cleanSummary(""), "");
  assert.equal(cleanSummary("ok"), "");
  assert.equal(cleanSummary('{"error":"x"}'.padEnd(60, " ")), "");
  assert.equal(cleanSummary("  The user is making a shop site in HTML, CSS and JavaScript.  "), "The user is making a shop site in HTML, CSS and JavaScript.");
  assert.ok(cleanSummary("y".repeat(3000)).length <= 1503);
});

test("a saved summary comes back: the chat restores to it, and the turns after it", () => {
  const lines: any[] = [{ t: "meta", id: "abcd1234" }];
  for (let i = 0; i < 6; i++) lines.push({ t: i % 2 ? "answer" : "user", text: "m" + i, ts: 100 + i });
  lines.push({ t: "summary", text: "Summary text of the first two.", steps: 4 });
  lines.push({ t: "user", text: "m6", ts: 200 });
  const r = restore(lines);
  assert.equal(r.messages.length, 7, "all of them are still shown");
  assert.deepEqual(r.summary, { text: "Summary text of the first two.", covered: 2 });
  const h = toHistory(r.messages, "u1", r.summary);
  assert.equal(h[0].content, SUMMARY_PREFIX + "Summary text of the first two.");
  assert.deepEqual(h.slice(1).map((m) => m.content.replace("[user-u1] ", "")), ["m2", "m3", "m4", "m5", "m6"]);
  assert.equal(toHistory(r.messages, "u1").length, 7, "without a summary it is the whole chat");
});

test("tokens are four characters each", () => {
  assert.equal(estimateTokens(""), 0);
  assert.equal(estimateTokens("abcd"), 1);
  assert.equal(estimateTokens("abcde"), 2);
});
