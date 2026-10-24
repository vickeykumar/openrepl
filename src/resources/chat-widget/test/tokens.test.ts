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
