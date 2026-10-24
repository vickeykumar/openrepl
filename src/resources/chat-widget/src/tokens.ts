// How the tokens Genie used are told to the user: the rows of the usage details
// and the line under an answer. No DOM, so that node can test it
// (test/tokens.test.ts). The numbers come from the server (server/tokens.go).

export interface Totals {
  in: number;
  out: number;
  answers: number;
}

export interface TokenUsage {
  visit?: Totals; // a visitor: this visit
  month?: Totals; // a signed-in user
  all?: Totals;
  cost?: { month: number; all: number; partial?: boolean };
}

export function fmtTokens(n: number): string {
  return Math.max(0, Math.round(n)).toLocaleString("en-US");
}

// Dollars as a user reads them: cents, and "under 1 cent" for what rounds to none.
export function fmtCost(usd: number, partial?: boolean): string {
  const text = usd > 0 && usd < 0.01 ? "under $0.01" : "$" + usd.toFixed(2);
  return partial ? text + " (not all models are priced)" : text;
}

const SEP: [string, string] = ["", ""]; // a line between two groups of rows

// The rows to add to the usage details, and a note for a visitor; [] when there is
// nothing to tell yet.
export function tokenRows(t: TokenUsage | undefined): { rows: [string, string][]; note: string } {
  if (!t) return { rows: [], note: "" };
  if (t.visit) {
    if (t.visit.answers === 0) return { rows: [], note: "" };
    return {
      rows: [
        SEP,
        ["Tokens used (this visit)", fmtTokens(t.visit.in + t.visit.out)],
        ["Sent to Genie", fmtTokens(t.visit.in)],
        ["Written by Genie", fmtTokens(t.visit.out)],
        ["Answers", fmtTokens(t.visit.answers)],
      ],
      note: "Counted for this visit. Sign in to keep a total across visits and devices.",
    };
  }
  if (!t.month || !t.all) return { rows: [], note: "" };
  const rows: [string, string][] = [
    SEP,
    ["Tokens this month", fmtTokens(t.month.in + t.month.out)],
    ["Tokens, all time", fmtTokens(t.all.in + t.all.out)],
    ["This month: sent / written", fmtTokens(t.month.in) + " / " + fmtTokens(t.month.out)],
    ["Answers this month", fmtTokens(t.month.answers)],
  ];
  if (t.cost) {
    rows.push(["Cost this month", fmtCost(t.cost.month, t.cost.partial)], ["Cost, all time", fmtCost(t.cost.all, t.cost.partial)]);
  }
  return { rows, note: "What the models report for your requests, agent steps included." };
}

// The line under an answer: what that one answer used, from the usage of the reply.
export function answerTokens(usage: unknown): string {
  if (!usage || typeof usage !== "object") return "";
  const u = usage as Record<string, unknown>;
  const sent = Number(u.prompt_tokens);
  const written = Number(u.completion_tokens);
  if (!(sent >= 0) || !(written >= 0) || sent + written === 0) return "";
  return fmtTokens(sent + written) + " tokens (" + fmtTokens(sent) + " sent, " + fmtTokens(written) + " written)";
}
