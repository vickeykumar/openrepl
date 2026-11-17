import test from "node:test";
import assert from "node:assert/strict";
import { answerTokens, fmtCost, fmtTokens, tokenRows } from "../src/tokens.ts";

test("numbers are told with thousands separators", () => {
  assert.equal(fmtTokens(1204950), "1,204,950");
  assert.equal(fmtTokens(0), "0");
  assert.equal(fmtTokens(-5), "0");
  assert.equal(fmtTokens(12.6), "13");
});

test("a cost is told in cents, and a tiny one as under a cent", () => {
  assert.equal(fmtCost(0.64), "$0.64");
  assert.equal(fmtCost(12), "$12.00");
  assert.equal(fmtCost(0.004), "under $0.01");
  assert.equal(fmtCost(0), "$0.00");
  assert.match(fmtCost(1.5, true), /not all models are priced/);
});

test("a visitor sees the visit, once there is something to count", () => {
  assert.deepEqual(tokenRows(undefined), { rows: [], note: "" });
  assert.deepEqual(tokenRows({ visit: { in: 0, out: 0, answers: 0 } }), { rows: [], note: "" });
  const { rows, note } = tokenRows({ visit: { in: 9310, out: 3170, answers: 14 } });
  assert.deepEqual(rows.slice(1), [
    ["Tokens used (this visit)", "12,480"],
    ["Sent to Genie", "9,310"],
    ["Written by Genie", "3,170"],
    ["Answers", "14"],
  ]);
  assert.match(note, /Sign in to keep a total/);
});

test("a signed-in user sees the month and all time, and the cost only when it is sent", () => {
  const usage = { month: { in: 141200, out: 45220, answers: 212 }, all: { in: 900000, out: 304950, answers: 1480 } };
  let got = tokenRows(usage);
  assert.deepEqual(got.rows.slice(1), [
    ["Tokens this month", "186,420"],
    ["Tokens, all time", "1,204,950"],
    ["This month: sent / written", "141,200 / 45,220"],
    ["Answers this month", "212"],
  ]);
  got = tokenRows({ ...usage, cost: { month: 0.64, all: 4.2 } });
  assert.deepEqual(got.rows.slice(-2), [["Cost this month", "$0.64"], ["Cost, all time", "$4.20"]]);
  assert.deepEqual(tokenRows({ month: usage.month }), { rows: [], note: "" });
});

test("the line under an answer says what that answer used", () => {
  assert.equal(answerTokens({ prompt_tokens: 310, completion_tokens: 930 }), "1,240 tokens (310 sent, 930 written)");
  assert.equal(answerTokens({ prompt_tokens: 0, completion_tokens: 0 }), "");
  assert.equal(answerTokens(undefined), "");
  assert.equal(answerTokens(null), "");
  assert.equal(answerTokens({ total_tokens: 9 }), "");
  assert.equal(answerTokens("x"), "");
});

import { creditLine, creditRows, fmtCredits, fmtDay } from "../src/tokens.ts";

test("credits are told with one decimal at most, and a day as day and month", () => {
  assert.equal(fmtCredits(412), "412");
  assert.equal(fmtCredits(0.04), "0");
  assert.equal(fmtCredits(12.34), "12.3");
  assert.equal(fmtCredits(-3), "0");
  assert.equal(fmtDay("2026-12-01"), "1 Dec");
  assert.equal(fmtDay("2026-01-15"), "15 Jan");
  assert.equal(fmtDay("garbage"), "");
});

test("the credits line leads only for a plan with a monthly cap", () => {
  assert.equal(creditLine(undefined), null);
  assert.equal(creditLine({ plan: "Free", of: 0, used: 5, left: 0, resets: "2026-12-01" }), null);
  assert.deepEqual(creditLine({ plan: "Free", of: 500, used: 88, left: 412, resets: "2026-12-01" }), { label: "412 of 500 credits left", part: 0.824, state: "ok" });
  assert.equal(creditLine({ plan: "Free", of: 500, used: 460, left: 40, resets: "2026-12-01" })!.state, "low");
  assert.deepEqual(creditLine({ plan: "Free", of: 500, used: 500, left: 0, resets: "2026-12-01" }), { label: "No credits left · back on 1 Dec", part: 0, state: "empty" });
});

test("the details start with the plan, the credits left and the budget of a task", () => {
  const c = { plan: "Free", of: 500, used: 88, left: 412, resets: "2026-12-01", taskBudget: 40 };
  assert.deepEqual(creditRows(c, { credits: 9, tokens: 7200 }), [
    ["Plan", "Free"],
    ["Credits left this month", "412 of 500"],
    ["Resets", "1 Dec"],
    ["Most one task may use", "about 40 credits"],
    ["Last task", "9 credits (7,200 tokens)"],
    ["", ""],
  ]);
  assert.deepEqual(creditRows({ plan: "Team", of: 0, used: 12.5, left: 0, resets: "2026-12-01" }), [["Plan", "Team"], ["Credits used this month", "12.5"], ["", ""]]);
  assert.deepEqual(creditRows(undefined), []);
});
