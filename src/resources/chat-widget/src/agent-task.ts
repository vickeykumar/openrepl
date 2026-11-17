// What a task keeps track of, as plain code with no DOM (node runs it in
// test/agent-task.test.ts): the history sent at each step, the plan, whether the
// work was checked, and what changed. agent.ts uses it; the server's side is in
// server/agent.go.

import type { Action, StepResult } from "./agent-protocol";

export type Msg = { role: string; content: string };

// ---- sending less at each step ------------------------------------------------------

const RESULT_PREFIX = '{"step_results"';
const KEEP_FULL = 2; // the last results and answers stay whole
const LONG_TEXT = 300; // code in an old answer longer than this is left out
const SHORT_DETAIL = 80;

function oneResult(r: any): string {
  let s = String(r && r.type) + ": " + String(r && r.status);
  if (r && typeof r.detail === "string" && r.detail) s += " (" + r.detail.slice(0, SHORT_DETAIL) + (r.detail.length > SHORT_DETAIL ? "..." : "") + ")";
  if (r && typeof r.output === "string" && r.output) s += " [output of " + r.output.length + " characters left out]";
  return s;
}

// An older result, as one line.
export function shrinkResults(content: string): string {
  try {
    const v = JSON.parse(content);
    if (!v || !Array.isArray(v.step_results)) return content;
    const parts = v.step_results.map(oneResult);
    if (Array.isArray(v.not_done) && v.not_done.length) parts.push("not done: " + v.not_done.join("; ").slice(0, 200));
    return JSON.stringify({ step_results_older: parts.join("; ") || "no actions" });
  } catch (e) {
    return content;
  }
}

// An older answer of the model, without the code it wrote: that is in the editor now.
export function shrinkAnswer(content: string): string {
  try {
    const v = JSON.parse(content);
    if (!v || !Array.isArray(v.actions)) return content;
    let changed = false;
    for (const a of v.actions) {
      if (a && typeof a.text === "string" && a.text.length > LONG_TEXT && (a.type === "editor_write" || a.type === "editor_insert")) {
        a.text = "[" + a.text.length + " characters of code, left out of this older step]";
        changed = true;
      }
    }
    return changed ? JSON.stringify(v) : content;
  } catch (e) {
    return content;
  }
}

// The conversation of a task as it is sent: the user's first words and the last
// two steps whole, older results as a line each and older answers without their
// code. The original is not changed (the page keeps it whole).
export function compactHistory(messages: Msg[], keepFull: number = KEEP_FULL): Msg[] {
  const resultIdx: number[] = [];
  const answerIdx: number[] = [];
  messages.forEach((m, i) => {
    if (i === 0) return;
    if (m.role === "user" && m.content.startsWith(RESULT_PREFIX)) resultIdx.push(i);
    if (m.role === "assistant") answerIdx.push(i);
  });
  const oldResults = new Set(resultIdx.slice(0, Math.max(0, resultIdx.length - keepFull)));
  const oldAnswers = new Set(answerIdx.slice(0, Math.max(0, answerIdx.length - keepFull)));
  return messages.map((m, i) => {
    if (oldResults.has(i)) return { role: m.role, content: shrinkResults(m.content) };
    if (oldAnswers.has(i)) return { role: m.role, content: shrinkAnswer(m.content) };
    return m;
  });
}

// ---- the plan -------------------------------------------------------------------------

// The plan of a task as it stands: the items, which are done, and which is next.
export class PlanState {
  items: string[] = [];
  private done = new Set<number>();

  // an answer may bring a new plan (the first one, or a changed one) and the numbers done so far
  update(plan: string[] | null, planDone: number[]): boolean {
    let changed = false;
    if (plan && JSON.stringify(plan) !== JSON.stringify(this.items)) {
      // a changed plan keeps the items that are the same and still done
      const doneText = new Set<string>(Array.from(this.done).map((n) => this.items[n - 1]));
      this.items = plan;
      this.done = new Set<number>();
      plan.forEach((t, i) => {
        if (doneText.has(t)) this.done.add(i + 1);
      });
      changed = true;
    }
    for (const n of planDone) {
      if (n <= this.items.length && !this.done.has(n)) {
        this.done.add(n);
        changed = true;
      }
    }
    return changed;
  }

  isDone(n: number): boolean {
    return this.done.has(n);
  }

  // the first item not done (1 based), 0 when all are
  active(): number {
    for (let i = 1; i <= this.items.length; i++) if (!this.done.has(i)) return i;
    return 0;
  }

  doneCount(): number {
    return this.done.size;
  }

  // a line for the next task's recap
  line(): string {
    return this.items.length ? this.doneCount() + " of " + this.items.length + " plan items done" : "";
  }
}

// ---- checking the work ----------------------------------------------------------------

const CHANGING: string[] = ["editor_write", "editor_insert", "editor_replace"];
const CHECKING: string[] = ["run", "debug", "bash", "terminal_type"];

export const CHECK_BEFORE_DONE =
  "You changed code but have not run it or its tests since. Run the program or the tests now and read what they print, then fix what fails. " +
  "If this cannot be run here (nothing to run, no tests, it needs input), say so in \"say\" and finish with done:true.";

export const MAX_CARRIES = 3; // times a task may be carried on (the server keeps the same number: server/agent.go maxAgentCarries)
export const MAX_FIXES = 2; // fix attempts after a check, then the task says what still fails

export function checkLabel(a: Action): string {
  if (a.type === "run") return "Run";
  if (a.type === "debug") return "Debug run";
  return (a.text || "").replace(/\s+/g, " ").trim().slice(0, 60);
}

// Whether the work of a task was checked: a change of the code, and a run after it.
export class WorkTrack {
  changes = 0; // accepted editor changes (a change counts for each accepted action)
  checked: string | null = null; // what ran after the last change
  checkFailed = false; // what ran after the last change was told to have failed
  pushedBack = false; // the model was told once to check before it finished
  fixes = 0; // runs after a failed check
  files: string[] = []; // the files the changes were made in, in order

  noteAction(a: Action, r: StepResult, file: string) {
    if (CHANGING.indexOf(a.type) >= 0 && (r.status === "accepted" || r.status === "partly_accepted")) {
      this.changes++;
      if (this.checked !== null) this.fixes++; // a change after a check is a fix
      this.checked = null;
      this.checkFailed = false;
      const f = file || "the editor";
      if (this.files.indexOf(f) < 0) this.files.push(f);
    } else if (CHECKING.indexOf(a.type) >= 0 && this.changes > 0) {
      // a bash command that ran and exited with a code other than 0 is a check that failed;
      // one that could not run (denied, no shell) is no check at all
      const ranFailed = a.type === "bash" && r.status === "failed" && /exit code [1-9]|timed out|stopped after/i.test(r.detail || "");
      if (r.status === "done" || ranFailed) {
        this.checked = checkLabel(a);
        this.checkFailed = ranFailed;
      }
    }
  }

  // Whether a step that says "done" should be sent back once to check.
  mustCheck(): boolean {
    return this.changes > 0 && this.checked === null && !this.pushedBack;
  }

  get changed(): boolean {
    return this.changes > 0;
  }
}

// ---- what changed ---------------------------------------------------------------------

const MAX_DIFF_LINES = 3000;

// Lines added and removed between two texts (a longest common subsequence of lines).
export function countLines(before: string, after: string): { added: number; removed: number } {
  if (before === after) return { added: 0, removed: 0 };
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  // the same start and end are not worth comparing
  let s = 0;
  while (s < a.length && s < b.length && a[s] === b[s]) s++;
  let ea = a.length;
  let eb = b.length;
  while (ea > s && eb > s && a[ea - 1] === b[eb - 1]) {
    ea--;
    eb--;
  }
  const x = a.slice(s, ea);
  const y = b.slice(s, eb);
  if (x.length > MAX_DIFF_LINES || y.length > MAX_DIFF_LINES) return { added: y.length, removed: x.length };
  let prev = new Uint32Array(y.length + 1);
  let cur = new Uint32Array(y.length + 1);
  for (let i = 1; i <= x.length; i++) {
    for (let j = 1; j <= y.length; j++) {
      cur[j] = x[i - 1] === y[j - 1] ? prev[j - 1] + 1 : Math.max(prev[j], cur[j - 1]);
    }
    const t = prev;
    prev = cur;
    cur = t;
  }
  const common = prev[y.length];
  return { added: y.length - common, removed: x.length - common };
}

// "auth.py +9 -2"
export function changeLine(file: string, d: { added: number; removed: number }): string {
  return file + " +" + d.added + " −" + d.removed;
}

// ---- the chat before a task ---------------------------------------------------------------

// the start of the summary entry of a long chat (the same text as SUMMARY_PREFIX in chat-tabs.ts: a test checks it; node cannot
// import one of these files from the other without an extension)
const SUMMARY_PREFIX = "Summary of the earlier chat: ";

// The last turns of the chat as text for the first message of a task, so that "try again" or "do that" means something:
// the user's words and Genie's answers, each cut, the newest last. `skip` is the entries at the start of the history that
// are not turns (the prompts and the editor snapshot). "" when nothing was said before.
export function chatTurnsText(history: { role: string; content: string }[], skip: number, turns: number = 6, each: number = 700, total: number = 3500): string {
  const summary = history.slice(skip).find((m) => m.role === "system" && String(m.content).startsWith(SUMMARY_PREFIX));
  const said = history.slice(skip).filter((m) => (m.role === "user" || m.role === "assistant") && String(m.content || "").trim() !== "");
  const lines = said.slice(-turns).map((m) => {
    const text = String(m.content).replace(/^\[user-[^\]]*\]\s*/, "").trim();
    const cut = text.length > each ? text.slice(0, each) + " ..." : text;
    return (m.role === "user" ? "User: " : "Genie: ") + cut;
  });
  let out = lines.join("\n");
  while (out.length > total && lines.length > 1) {
    lines.shift();
    out = lines.join("\n");
  }
  if (summary) out = String(summary.content).trim() + (out ? "\n" + out : "");
  return out === "" ? "" : "Earlier in this chat (for context only; the task is the message above, and the editor and the terminal below are what is open now):\n" + out;
}

// ---- what is true now, read from the page, and a task that is too vague to act on -----------------------------

// The last lines of the terminal that look like an error (at most 3, each cut), or "".
export function lastErrorLines(terminal: string): string {
  const lines = String(terminal || "").split("\n").map((l) => l.trim()).filter(Boolean);
  const hit = lines.filter((l) => /(error|exception|traceback|failed|cannot|undefined|not found|fatal)/i.test(l));
  return hit.slice(-3).map((l) => (l.length > 160 ? l.slice(0, 160) + "..." : l)).join(" | ");
}

export interface Facts {
  language: string;
  file: string;
  files: string[] | null; // null: the user has not allowed the agent to use the files
  error: string;
}

// "Workspace now: ..." for the first message of a task: what the model would otherwise guess.
export function factsText(f: Facts): string {
  const bits = ["language " + (f.language || "unknown"), "open file " + (f.file || "none (an unnamed file)")];
  if (f.files) bits.push("files: " + (f.files.length ? f.files.slice(0, 40).join(", ") + (f.files.length > 40 ? ", ..." : "") : "none yet"));
  if (f.error) bits.push("last error in the terminal: " + f.error);
  return "Workspace now (read just now from the page, so it is true; trust it over your memory): " + bits.join("; ") + ".";
}

// A request that says too little to act on without the chat before it ("try again", "continue", "fix it").
export function isVagueTask(task: string): boolean {
  const t = String(task || "").trim().toLowerCase().replace(/[.!?]+$/, "");
  if (t === "") return true;
  if (/^(try again|again|retry|continue|go on|carry on|keep going|do it|do that|fix it|fix that|same|redo|yes|ok|okay|go ahead|finish it)$/.test(t)) return true;
  return t.split(/\s+/).length <= 2;
}

export const ASK_FIRST =
  "Note: this request is too short to act on, and the chat before it does not make it clear what is meant. Do not guess and do not start a new project: " +
  "ask the user ONE short question in \"say\" about what they want, and set \"done\": true.";
