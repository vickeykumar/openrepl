// What the agent tells the server about itself (the Genie log, server/genie_log.go) and how it notices that a task is stuck.
//
// Both are about the work and never about the person: an event holds the model, the action, the file extension, a short
// reason and counts. The server cuts and scrubs the reason again before it keeps it. No message, file content, file name
// or account goes into an event, and nothing here reads the user's text.

// Changes when the agent's instructions change in a way that may change how models do (the Genie log keeps it with each
// event, so that a rise or a fall can be put next to the change that caused it).
export const PROMPT_VERSION = "g2";

export const STUCK_SAME = 3; // the same step failed this many times
export const STUCK_ERROR = 3; // steps in a row that failed with the same words, whatever they were (the first failure of a kind gets the hint a step's result carries)
export const STUCK_IDLE = 6; // steps in a row that changed nothing and printed nothing new
export const STUCK_BUDGET = 0.8; // the part of the steps of a stretch that is used, with nothing changed
export const STUCK_BUDGET_MIN = 15; // ... asked only for a stretch of this many steps or more: with the usual 8 the pause at the step limit comes one step later anyway
export const ESCALATE_STEPS = 5; // steps a stronger model keeps a stuck task before the user is asked

type ActionLike = { type: string; [k: string]: any };

// Steps that only look: reading the page or a file does not change anything, so it is no sign that a way of working succeeded.
// A task that reads before each retry of a failing click is still failing in a row.
const LOOKING = /^(preview_(read|console|network|inspect|find|state|wait|storage)|files_(list|search|read)|read_output|editor_read)$/;
export function isLooking(type: string): boolean {
  return LOOKING.test(type);
}

// The thing an action works on, short: a file, a part of the page, the start of a text or a command.
export function targetOf(a: ActionLike): string {
  const pick = a.path || a.ref || a.name || a.to || a.query || a.old || a.text || a.command || a.language || "";
  return String(pick).replace(/\s+/g, " ").slice(0, 40);
}

// The file extension of a path, lower case and at most 8 letters or digits ("" for none).
export function extOf(path: string): string {
  const m = /\.([A-Za-z0-9]{1,8})$/.exec(path || "");
  return m ? m[1].toLowerCase() : "";
}

// A reason with the numbers taken out, so that "has only 35 lines" and "has only 36 lines" are the same words.
export function sameWords(reason: string): string {
  return reason
    .toLowerCase()
    .replace(/[0-9]+/g, "#")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 80);
}

// A command that ran and exited with an error is a check that failed (agent-task.ts, ranFailed), what a program does and not
// something Genie could not do: it is neither a failed step nor a reason to stop.
export function isCheckFailure(a: ActionLike, status: string, detail: string): boolean {
  return status === "failed" && a.type === "bash" && /exit code [1-9]|timed out|stopped after/i.test(detail || "");
}

// A refusal the loop already handles by telling the model (the Preview limit of a task, an admin's switch, the fix attempts that are
// used up): the model is told once what to do, so repeating it is not a step that could not be done.
export function isLimit(status: string, detail: string): boolean {
  return status === "failed" && /a task may use the Preview page|an admin switched off|fix attempts were already made/i.test(detail || "");
}

// What an action that touches the user's program or page says when it fails may hold their output, and the editor's own
// message holds lines of their code: for the log only the plain reason is kept. The first line only, and for what runs the
// program or reads the page a few words that say how it failed.
const OUTPUT_TYPES = /^(bash|run|debug|terminal_|read_output|preview_(eval|read|console|network|inspect|storage|state|wait))/;
export function safeReason(type: string, detail: string): string {
  const d = String(detail || "");
  if (OUTPUT_TYPES.test(type)) {
    if (/timed out|stopped after/i.test(d)) return "timed out";
    if (/exit code [1-9]/i.test(d)) return "exited with an error";
    if (/not available|not possible|switched off|cannot|can't|away|not open/i.test(d)) return "not available";
    return "failed";
  }
  return d.split("\n")[0].split(/the last lines are:/i)[0].replace(/\s+/g, " ").trim().slice(0, 160);
}

export interface StuckInfo {
  rule: "same-step" | "same-error" | "no-progress" | "budget";
  action: string; // the type of the action that failed last
  target: string;
  count: number;
  reason: string; // what the last failure said
}

// Watches the results of a task's actions. A step that failed STUCK_SAME times in a row (no step that worked between), or STUCK_ERROR steps in
// a row that failed with the same words, is stuck: the caller stops the task and lets the user choose. A success of the same step starts its
// count again; the user's own "no" to a card (denied, rejected) is a decision and not a failure, so only "failed" counts.
export class StuckDetector {
  private count = new Map<string, number>();
  private lastWords = "";
  private run = 0;

  note(a: ActionLike, status: string, detail: string): StuckInfo | null {
    const key = a.type + "|" + targetOf(a);
    if (status !== "failed") {
      // a step that worked and changed something (or ran something): the run of failures is over. A step that only looked leaves the counts as they are.
      if (status !== "skipped" && !isLooking(a.type)) {
        this.count.clear();
        this.lastWords = "";
        this.run = 0;
      }
      return null;
    }
    const n = (this.count.get(key) || 0) + 1;
    this.count.set(key, n);
    const words = sameWords(detail || "");
    this.run = words !== "" && words === this.lastWords ? this.run + 1 : 1;
    this.lastWords = words;
    const info = (rule: StuckInfo["rule"], count: number): StuckInfo => ({ rule, action: a.type, target: targetOf(a), count, reason: (detail || "").replace(/\s+/g, " ").slice(0, 200) });
    if (n >= STUCK_SAME) {
      this.reset();
      return info("same-step", n);
    }
    if (this.run >= STUCK_ERROR) {
      this.reset();
      return info("same-error", STUCK_ERROR);
    }
    return null;
  }

  private idle = 0;
  private budgetAsked = false;

  // After each step that goes on: did it change anything or print something new? STUCK_IDLE steps that did not are stuck (a model that reads
  // the same things again). And when STUCK_BUDGET of the steps of a stretch are used and the task has changed nothing at all, it asks once,
  // since a task that only looks around can be what was asked: the card then has Keep going.
  noteStep(s: { progress: boolean; step: number; max: number; changedAnything: boolean }): StuckInfo | null {
    this.idle = s.progress ? 0 : this.idle + 1;
    if (this.idle >= STUCK_IDLE) {
      this.idle = 0;
      return { rule: "no-progress", action: "", target: "", count: STUCK_IDLE, reason: STUCK_IDLE + " steps with no change and no new output" };
    }
    if (!this.budgetAsked && !s.changedAnything && s.max >= STUCK_BUDGET_MIN && s.step >= Math.ceil(s.max * STUCK_BUDGET)) {
      this.budgetAsked = true;
      return { rule: "budget", action: "", target: "", count: s.step, reason: "step " + s.step + " of " + s.max + " and nothing changed" };
    }
    return null;
  }

  // a stretch the user carried on starts again: the budget is asked about again
  newStretch() {
    this.budgetAsked = false;
    this.idle = 0;
  }

  // after the user chose what happens next: the counts start again
  reset() {
    this.idle = 0;
    this.count.clear();
    this.lastWords = "";
    this.run = 0;
  }
}

// One id for this page load. It tells the log which events belong together and is not kept anywhere else.
let sessionId = "";
export function pageSessionId(): string {
  if (sessionId) return sessionId;
  let r = "";
  try {
    const a = new Uint8Array(9);
    (window.crypto || (window as any).msCrypto).getRandomValues(a);
    r = Array.prototype.map.call(a, (b: number) => (b % 36).toString(36)).join("");
  } catch (e) {
    r = Math.random().toString(36).slice(2, 11);
  }
  sessionId = "s" + r;
  return sessionId;
}

// A short code for why a step failed, so that the log can be counted by it (the reason's text is cut and varies). Plain words of the page's own messages.
export function reasonCode(detail: string): string {
  const d = String(detail || "");
  const table: [RegExp, string][] = [
    [/is not on the page now: it was called/i, "stale_ref_renamed"],
    [/is not on the page now: it was removed/i, "stale_ref_removed"],
    [/is not on the page now: no preview_read/i, "stale_ref_reload"],
    [/is not on the page now/i, "stale_ref"],
    [/no file is open/i, "no_file_open"],
    [/answer could not be read/i, "unreadable"],
    [/answer was too long/i, "too_long"],
    [/is covered by/i, "covered"],
    [/is disabled/i, "disabled"],
    [/a task may use the Preview page/i, "limit"],
    [/old text was not found|more than once|has only \d+ lines/i, "edit_mismatch"],
    [/exists already/i, "exists"],
    [/not in the Files panel|no such (path|file|option)/i, "no_such_path"],
    [/refused|403|401|not allowed/i, "refused"],
    [/switched off|not available|not possible|cannot|away|not open/i, "not_available"],
    [/timed out|too long to|did not answer in time/i, "timeout"],
  ];
  for (const [re, code] of table) if (re.test(d)) return code;
  return d ? "other" : "";
}

// An id for one task, so that the events of a page that ran several tasks can be told apart. Random, and used for nothing else.
export function newTaskId(): string {
  let r = "";
  try {
    const a = new Uint8Array(6);
    (window.crypto || (window as any).msCrypto).getRandomValues(a);
    r = Array.prototype.map.call(a, (b: number) => (b % 36).toString(36)).join("");
  } catch (e) {
    r = Math.random().toString(36).slice(2, 8);
  }
  return "t" + r;
}

export interface GenieEvent {
  event: "step_failed" | "stuck" | "stuck_answer" | "retry" | "need_tools";
  model: string;
  step?: number;
  action?: string;
  ext?: string;
  reason?: string;
  failed?: number;
  groups?: string; // the groups of actions the task has been sent, comma separated
  tid?: string; // the task
  code?: string; // reasonCode of the reason
  rule?: string; // stuck: which rule fired (same-step, same-error, no-progress, budget)
  choice?: string; // stuck_answer: what the user chose (go, hint, stronger, stop)
  trail?: string; // the last actions of the task, comma separated (names of actions only)
  lang?: string; // the language in the picker
}

// What the page sends for an event, whitelisted here once more.
export function eventBody(e: GenieEvent): Record<string, any> {
  return {
    event: e.event,
    sid: pageSessionId(),
    model: e.model,
    mode: "agent",
    step: e.step || 0,
    action: e.action || "",
    ext: e.ext || "",
    reason: (e.reason || "").slice(0, 300),
    failed: e.failed || 0,
    groups: (e.groups || "").slice(0, 60),
    tid: e.tid || "",
    code: e.code || "",
    rule: e.rule || "",
    choice: e.choice || "",
    trail: (e.trail || "").slice(0, 200),
    lang: (e.lang || "").slice(0, 20),
    prompt: PROMPT_VERSION,
  };
}

// The words of the stuck card.
export function stuckText(i: StuckInfo): string {
  if (i.rule === "no-progress") return i.count + " steps in a row changed nothing and printed nothing new. It is paused, so no more steps are used.";
  if (i.rule === "budget") return "Most of the steps for this stretch are used (step " + i.count + ") and no file has changed. If the task only runs code or looks around, choose Keep going. It is paused, so no more steps are used.";
  const what = i.rule === "same-step" ? "The same step failed " + i.count + " times in a row" + (i.target ? ": " + i.action + " " + i.target : ": " + i.action) : i.count + " steps in a row failed for the same reason";
  const reason = i.reason.replace(/[.\s]+$/, "").slice(0, 140);
  const why = reason ? " (" + reason + ")" : "";
  return what + why + ". It is paused, so no more steps are used.";
}
