// The practice coach: the rules of the page's side, with no DOM so that node can
// test them (test/coach.test.ts). The instructions the model gets are the
// server's (server/coach.go); what the page adds is the count of hints, and a
// check that a hint does not carry code.

export const MAX_HINTS = 3;

export type CoachKind = "hint" | "review" | "complexity";

// What the user is shown as having asked.
export function askedText(kind: CoachKind, level: number): string {
  if (kind === "hint") return "Hint " + level + " of " + MAX_HINTS;
  return kind === "review" ? "Review my solution" : "What is the complexity of my solution?";
}

// Hints used, per question, as the browser keeps them: {"two-sum": 2}.
export function hintsUsed(store: unknown, question: string): number {
  if (!store || typeof store !== "object") return 0;
  const n = (store as Record<string, unknown>)[question];
  return typeof n === "number" && Number.isInteger(n) && n > 0 ? Math.min(n, MAX_HINTS) : 0;
}

// The store with one more hint used for the question, and no more than the
// last 200 questions (the browser keeps this in localStorage).
export function withHint(store: unknown, question: string): Record<string, number> {
  const out: Record<string, number> = {};
  if (store && typeof store === "object") {
    for (const [k, v] of Object.entries(store as Record<string, unknown>)) {
      if (typeof v === "number" && Number.isInteger(v) && v > 0) out[k] = Math.min(v, MAX_HINTS);
    }
  }
  delete out[question]; // moved to the end: the newest are kept
  out[question] = Math.min(hintsUsed(store, question) + 1, MAX_HINTS);
  const keys = Object.keys(out);
  for (const k of keys.slice(0, Math.max(0, keys.length - 200))) delete out[k];
  return out;
}

// The question the page is on: ?name=two-sum, else the path.
export function questionKey(search: string, pathname: string): string {
  const name = new URLSearchParams(search).get("name");
  return (name && name.slice(0, 120)) || pathname.slice(0, 120);
}

// A hint is not allowed to carry code: a fenced block, or several lines that
// are indented like code. The server asks the model not to; this is the check.
export function carriesCode(text: string): boolean {
  if (/```|~~~/.test(text)) return true;
  const indented = text.split("\n").filter((l) => /^( {4}|\t)\S/.test(l));
  return indented.length >= 2;
}

export const NO_CODE_IN_A_HINT =
  "That hint came with code, and a hint is not meant to. Ask again for a different one, or ask the interviewer a question in the chat.";
export const NO_HINTS_LEFT =
  "You have used all " + MAX_HINTS + " hints for this question. Try Review my solution once you have some code, or ask the interviewer in the chat.";
