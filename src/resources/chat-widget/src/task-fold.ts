// The one line a finished task card folds to: "Done · Changed 3 files · 7 steps · 14 s" (canvas "Genie: calmer task cards").
// It says the result first (the words of the Genie log, never "Success"), then what changed, then the size of the task, and for a task
// that did not end well the first words of the reason.

export interface FoldFacts {
  outcome: "done" | "stopped";
  changed: number; // files the task changed, shell changes counted (the Review rows)
  checks: number; // programs it ran (run, bash)
  steps: number;
  secs: number;
  reason?: string; // why it stopped, if known
}

export function whatChanged(changed: number, checks: number): string {
  if (changed > 0) return changed === 1 ? "1 file changed" : "Changed " + changed + " files";
  if (checks > 0) return "Ran " + checks + (checks === 1 ? " check" : " checks") + " · no file changed";
  return "no file changed";
}

export function foldWord(f: FoldFacts): string {
  return f.outcome === "done" ? "Done" : "Stopped";
}

export function foldLine(f: FoldFacts): string {
  const bits = [whatChanged(f.changed, f.checks), f.steps + (f.steps === 1 ? " step" : " steps"), Math.max(1, f.secs) + " s"];
  const why = (f.reason || "").replace(/\s+/g, " ").trim().slice(0, 60);
  if (f.outcome === "stopped" && why) bits.push(why);
  return bits.join(" · ");
}

// "Thought about the next step" lines are most of what a finished card says and tell nothing: they fold into one line.
export const THINKING = /^Thought about the next step/;

export function thinkingLabel(n: number): string {
  return n + (n === 1 ? " thinking step" : " thinking steps");
}
