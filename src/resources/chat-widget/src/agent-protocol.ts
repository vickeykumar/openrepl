// The answers of Genie in agent mode, and what is sent back. No DOM in here: it
// is plain code that node runs in test/ (node --test test/). The server's side
// of the protocol, and the instructions the model gets, are in
// server/agent.go (agentSystemPrompt, agentActions): the list of actions has to
// be the same, and a Go test compares them.
//
// A step is one JSON object: {"say": "...", "actions": [...], "done": false}.
// What the model sends is untrusted text, so everything is checked here before
// the page acts on it, and what does not pass is dropped and reported back to
// the model in the results of the step.

export const ACTIONS = [
  "editor_write",
  "editor_insert",
  "set_language",
  "run",
  "debug",
  "terminal_type",
  "terminal_interrupt",
  "terminal_reconnect",
  "terminal_new_tab",
  "terminal_select_tab",
  "terminal_close_tab",
  "files_list",
  "files_open",
  "files_new",
  "files_save",
  "files_rename",
  "files_cut",
  "files_copy",
  "files_paste",
  "files_move",
  "files_delete",
  "read_output",
  "finish",
] as const;
export type ActionType = (typeof ACTIONS)[number];

export const MAX_ACTIONS = 3; // a step does at most this many
export const MAX_TEXT = 200000; // characters of code in one action
export const MAX_SAY = 600; // characters shown to the user for a step
export const MAX_OUTPUT = 4000; // characters of terminal output sent back
export const MAX_TERMINAL_TEXT = 500; // characters of the line Genie types in the terminal
export const MAX_TABS = 5; // terminal tabs the page allows (js/src/main.ts)
export const MAX_PATH = 400; // characters of a path in the Files panel, relative to the home directory
export const MAX_NAME = 100; // characters of one name in it

export interface Action {
  type: ActionType;
  text?: string; // editor_write, editor_insert; the line of terminal_type
  language?: string; // set_language, terminal_new_tab
  tab?: number; // terminal_select_tab and terminal_close_tab, 1 to MAX_TABS
  path?: string; // files_*: relative to the home directory, "" is the home directory itself
  to?: string; // files_move: the folder it goes into
  name?: string; // files_rename: the new name, one segment
  kind?: "file" | "folder"; // files_new
  waitSeconds?: number; // read_output and terminal_type, 1 to 20
}

export interface Step {
  say: string;
  actions: Action[];
  done: boolean;
  dropped: string[]; // what was left out, and why
}

export type Status = "done" | "accepted" | "partly_accepted" | "rejected" | "denied" | "failed" | "skipped";

export interface StepResult {
  type: string;
  status: Status;
  detail?: string;
  output?: string;
  accepted?: number;
  rejected?: number;
}

// the kinds of action the user is asked about; reading and finishing need no
// permission (the code and the output are sent to Genie with every message).
// Switching the language is a kind of its own: it is not a change the user
// reviews, the page puts the language's starter code in the editor and starts
// the terminal again.
export type Scope = "editor" | "language" | "run" | "terminal" | "files";

export function scopeOf(a: Action): Scope | null {
  switch (a.type) {
    // a new tab in another language changes the language, with what that does to the editor
    case "terminal_new_tab":
      return a.language ? "language" : "terminal";
    case "editor_write":
    case "editor_insert":
      return "editor";
    case "set_language":
      return "language";
    case "run":
    case "debug":
      return "run";
    case "terminal_type":
    case "terminal_interrupt":
    case "terminal_reconnect":
    case "terminal_select_tab":
    case "terminal_close_tab":
      return "terminal";
    case "files_list":
    case "files_open":
    case "files_new":
    case "files_save":
    case "files_rename":
    case "files_cut":
    case "files_copy":
    case "files_paste":
    case "files_move":
    case "files_delete":
      return "files";
    default:
      return null;
  }
}

// ---- paths in the Files panel --------------------------------------------------------
//
// Genie names a file or folder by its path relative to the home directory, as
// files_list shows it ("src/main.py"; "" is the home directory). What it sends is
// checked here: a path that leaves the home directory or hides something
// (an absolute path, "..", a backslash, a control character) is not a path.

// cleanPath returns the path in its plain form, or null if it is not one.
// allowEmpty: "" and "." mean the home directory.
export function cleanPath(raw: unknown, allowEmpty: boolean): string | null {
  if (typeof raw !== "string") return null;
  let p = raw.trim();
  if (p === "" || p === "." || p === "./") return allowEmpty ? "" : null;
  if (p.startsWith("./")) p = p.slice(2);
  p = p.replace(/\/+$/, ""); // a folder may be written with its slash
  if (p === "" || p.length > MAX_PATH) return null;
  if (/[\u0000-\u001f\u007f-\u009f\\]/.test(p)) return null;
  if (p.startsWith("~")) return null; // a path from the shell's home, not this one
  const parts = p.split("/");
  for (const s of parts) {
    // a name with a dot in front is a hidden file or folder (.env, .git, .ssh):
    // those are not listed and not Genie's to name
    if (s === "" || s.startsWith(".") || s.length > MAX_NAME) return null;
  }
  return parts.join("/");
}

// cleanName: one name, no slash.
export function cleanName(raw: unknown): string | null {
  const n = cleanPath(raw, false);
  return n !== null && !n.includes("/") ? n : null;
}

// the parent folder of a path ("" for the home directory) and its last name
export function splitPath(p: string): { parent: string; name: string } {
  const at = p.lastIndexOf("/");
  return at < 0 ? { parent: "", name: p } : { parent: p.slice(0, at), name: p.slice(at + 1) };
}

// What the page says about a path in words: "the home folder" or the path.
export function where(p: string | undefined): string {
  return !p ? "the home folder" : p;
}

// What a file action changes for good, so that it is asked about every time,
// even when the Files panel was allowed for the session; "" for the others.
// bufferMode is what the panel holds from a cut or a copy.
export function filesAskReason(a: Action, bufferMode: "cut" | "copy" | null): string {
  switch (a.type) {
    case "files_delete":
      return "deletes it for good";
    case "files_rename":
      return "changes the name of something of yours";
    case "files_move":
      return "moves something of yours";
    case "files_paste":
      return bufferMode === "cut" ? "moves something of yours" : "";
    default:
      return "";
  }
}

// The words the page uses to ask for a permission, and what it says will
// happen. They must be true of what the action does.
export function scopeQuestion(scope: Scope): string {
  if (scope === "editor") return "Genie wants to change your editor";
  if (scope === "language") return "Genie wants to switch the language";
  if (scope === "terminal") return "Genie wants to use your terminal";
  if (scope === "files") return "Genie wants to use your files";
  return "Genie wants to run your code";
}

// openFile: the file that is open in the editor, if any. The page leaves the
// editor alone on a change of language then, and the card must not say otherwise.
export function scopeDetail(scope: Scope, a: Action, openFile: string = ""): string {
  if (scope === "editor") return "It will propose changes. You review each one before anything is applied.";
  if (scope === "language") {
    const lang = a.language || "another language";
    const editor = openFile
      ? "The editor keeps " + openFile + ", the file that is open."
      : "The editor then shows that language's starter code in place of what it holds now.";
    if (a.type === "terminal_new_tab") return "It will open a new terminal tab in " + lang + ". " + editor;
    return "It will switch to " + lang + ", and the terminal starts again. " + editor;
  }
  if (scope === "files") {
    switch (a.type) {
      case "files_list":
        return "It will look at the names of the files and folders in " + where(a.path) + ". The names are sent to the model.";
      case "files_open":
        return "It will save the file that is open, then show " + a.path + " in the editor in its place. Code in the editor that is not in a file is replaced.";
      case "files_new":
        return "It will create the " + (a.kind === "folder" ? "folder " : "file ") + a.path + ". A new file is empty and is not opened.";
      case "files_save":
        return "It will save what is in the editor to the file that is open.";
      case "files_rename":
        return "It will rename " + a.path + " to " + a.name + ".";
      case "files_cut":
        return "It will mark " + a.path + " to be moved. Nothing changes until it is pasted.";
      case "files_copy":
        return "It will mark " + a.path + " to be copied. Nothing changes until it is pasted.";
      case "files_paste":
        return "It will paste what was cut or copied into " + where(a.to) + ".";
      case "files_move":
        return "It will move " + a.path + " into " + where(a.to) + ".";
      default:
        return "It will delete " + a.path + " for good, and everything in it if it is a folder.";
    }
  }
  if (scope === "terminal") {
    if (a.type === "terminal_interrupt") return "It will press Ctrl+C in your terminal, which stops the program that is running there.";
    if (a.type === "terminal_reconnect") {
      return "It will restart your terminal in the same language. A program that is running in it is stopped. Your files and your editor stay as they are.";
    }
    if (a.type === "terminal_new_tab") return "It will open a new terminal tab, in the language that is chosen now. You can have up to " + MAX_TABS + " tabs.";
    if (a.type === "terminal_select_tab") return "It will switch to terminal tab " + (a.tab || "?") + ". Nothing is stopped.";
    if (a.type === "terminal_close_tab") {
      return "It will close terminal tab " + (a.tab || "?") + ". A program running in it is stopped. Your editor and your other tabs stay as they are. The first tab, the main terminal, is never closed.";
    }
    return "It will type this line in your terminal and press Enter. If a program is waiting for input, the program gets it.";
  }
  return "It will press " + (a.type === "debug" ? "Debug" : "Run") + " for the code in your editor.";
}

// ---- risky lines for the terminal --------------------------------------------------
//
// Genie's terminal is the REPL of the chosen language (a shell for Bash), in the
// user's own sandbox. A line that can delete or change things, install software,
// or build a command out of other text is "risky": it is always asked about, even
// when the user allowed the terminal for the session. The list is meant to be
// wider than needed: a wrong guess costs one click, a miss costs a file. It is a
// help for the person reading the prompt, not a lock; the sandbox is the lock.

const RISKS: [RegExp, string][] = [
  [/(^|[\s;&|(`])(sudo|doas|su|pkexec)(\s|$)/i, "runs as another user"],
  [/(^|[\s;&|(`])(rm|rmdir|unlink|shred|truncate|srm)(\s|$)/i, "deletes files"],
  [/\bfind\b[^\n]*(-delete|-exec|-execdir)\b/i, "deletes files or runs a command on many"],
  [/\bxargs\b/i, "runs a command on many things"],
  [/(^|[\s;&|(`])(mkfs[.\w]*|fdisk|parted|mount|umount|dd|wipefs)(\s|$)/i, "changes disks or file systems"],
  [/(^|[\s;&|(`])(mv|cp|ln)(\s|$)/i, "can overwrite files"],
  [/\bsed\b[^\n]*\s-[a-zA-Z]*i|\bperl\b[^\n]*\s-[a-zA-Z]*i/, "changes files in place"],
  [/(^|[\s;&|(`])tee(\s|$)/i, "writes a file"],
  // a redirect into a file: "> out.txt", ">> log", not "x > 0.5", "a >= b", "x => x.y", "-> int", "2>&1", "> /dev/null"
  [/(^|[^=\-<>&])>{1,2}\s*(?!\/dev\/null\b)(?=[\w.\/~-]*[A-Za-z])[\w.\/~-]*[.\/][\w.\/~-]+/, "writes a file"],
  [/(^|[\s;&|(`])(python3?|node|perl|ruby|php)\s+-[ce]\b/i, "runs text it is given"],
  [/(^|[\s;&|(`])(env|printenv)(\s|$)/i, "shows environment variables, which may hold secrets"],
  [/\bof=\s*\/dev\//i, "writes to a device"],
  [/>\s*\/dev\/(?!null\b|stdout\b|stderr\b)/i, "writes to a device"],
  [/\bchmod\s+(-\w*R|--recursive|[0-7]*7{2,3}\b)|\bchown\b|\bchgrp\b/i, "changes permissions or owners"],
  [/(^|[\s;&|(`])(shutdown|reboot|halt|poweroff|init|telinit|systemctl)(\s|$)/i, "stops or restarts the system"],
  [/(^|[\s;&|(`])(kill|killall|pkill)(\s|$)/i, "stops other programs"],
  [/:\s*\(\s*\)\s*\{/, "looks like a fork bomb"],
  [/(^|[\s;&|(`])(curl|wget|fetch|ftp|scp|sftp|rsync|nc|ncat|netcat|ssh|telnet)(\s|$)/i, "uses the network"],
  [/(^|[\s;&|(`])(pip3?|pipx|npm|npx|yarn|pnpm|apt|apt-get|aptitude|dpkg|yum|dnf|apk|brew|snap|gem|cpan|conda)\s+(install|add|remove|uninstall|update|upgrade|i)\b/i, "installs or removes software"],
  [/\b(go|cargo)\s+(get|install)\b/i, "installs software"],
  [/\bgit\s+(reset\s+--hard|clean|push|checkout\s+(--|\.)|restore|rebase|branch\s+-D|stash\s+(drop|clear)|rm)(\s|$)/i, "can lose changes in git"],
  [/\|\s*(sudo\s+)?(ba|z|da|k)?sh\b|\|\s*(python3?|perl|ruby|node)\b/i, "runs text it is given"],
  [/(^|[\s;&|(`])(eval|exec|source|\.)\s/i, "runs text it builds"],
  [/\b(sh|bash|zsh|dash)\s+-c\b/i, "runs text it builds"],
  [/\$\(|`/, "builds a command out of other text"],
  [/base64\s+(-d|--decode)/i, "decodes something to run"],
  // /dev/null and the standard streams are not system files in this sense
  [/(^|[\s"'=<>])\/(etc|proc|sys|boot|root|usr|bin|sbin|lib|var|dev(?!\/(null|stdout|stderr|stdin)\b))\b/i, "touches system files"],
  [/~\/\.|\.\.\//, "reaches outside the workspace"],
  [/\b(drop\s+(table|database|schema|index|view)|delete\s+from|truncate\s+table|alter\s+table)\b/i, "deletes or changes data"],
  [/\bos\s*\.\s*(remove|unlink|rmdir|removedirs|rename|replace|system|popen|kill|exec\w*|spawn\w*|chmod|chown)\b/i, "deletes files or runs other programs"],
  [/\b(shutil|subprocess|pathlib)\b|\bpty\.spawn\b|__import__|\b(eval|exec|compile)\s*\(/i, "deletes files or runs other programs"],
  [/\bopen\s*\([^)]*,\s*['"][^'"]*[wax+][^'"]*['"]/i, "writes a file"],
  [/\b(fs|File|FileUtils|Files|Path)\s*\.\s*(rm|rmdir|unlink|delete|remove|rename|write|append|copy|move|mv|cp)\w*/i, "deletes or writes files"],
  [/\b(child_process|exec[Ss]ync|spawn[Ss]ync|Runtime\.getRuntime|ProcessBuilder|os\/exec|exec\.Command|system\s*\(|popen\s*\()/, "runs other programs"],
  [/\b(ioutil|os)\.(Remove\w*|WriteFile|Create|Rename|Chmod|Chown)\b/, "deletes or writes files"],
];

// riskOf says why a line is risky, or "" when nothing in it is. It looks at the
// whole line, so a danger after a semicolon or in a pipe is found too.
export function riskOf(text: string): string {
  const why: string[] = [];
  for (const [pattern, reason] of RISKS) {
    if (pattern.test(text) && !why.includes(reason)) why.push(reason);
    if (why.length === 3) break;
  }
  return why.join("; ");
}

// What a terminal shows when it has been told to take a line: the program in it
// is over. Only the last lines count, not a word in the middle of the output.
export function endedAtTail(terminalText: string): boolean {
  const lines = String(terminalText || "")
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
  return PROGRAM_ENDED.test(lines.slice(-2).join("\n"));
}

// The part of the terminal's text that is new since `before`: the screen may
// have scrolled, so it looks for the end of what was there, and falls back to
// the end of the text.
export function newOutput(before: string, after: string, max: number = MAX_OUTPUT): string {
  let out = after;
  if (after.startsWith(before)) {
    out = after.slice(before.length);
  } else {
    const anchor = before.slice(-120);
    const at = anchor.trim() === "" ? -1 : after.lastIndexOf(anchor);
    if (at >= 0) out = after.slice(at + anchor.length);
  }
  out = out.replace(/^\n+/, "");
  return out.length > max ? "..." + out.slice(-max) : out;
}

// Where the run that a task started is. Every Run closes the terminal and opens
// a new one for the program, so the old terminal's text says nothing about it:
// the run has started when the terminal is another object than at Run (or, where
// the page cannot tell, when a moment has passed), and it is over when the new
// terminal says so.
export type RunPhase = "none" | "starting" | "running" | "ended";

export function runPhase(o: { ran: boolean; term: unknown; runTerm: unknown; sinceRunMs: number; startMs: number; text: string }): RunPhase {
  if (!o.ran) return "none";
  const started = o.term !== null && o.term !== undefined ? o.term !== o.runTerm : o.sinceRunMs > o.startMs;
  if (!started) return "starting";
  return programEnded(o.text) ? "ended" : "running";
}

// What the terminal prints when a program that Run started is over: it exited,
// it was killed (the memory limit), or the connection went.
const PROGRAM_ENDED = /\[Program Exited\]|\[Program stopped:|connection closed/;

export function programEnded(terminalText: string): boolean {
  return PROGRAM_ENDED.test(String(terminalText || ""));
}

// The languages that run in a console inside the page (an iframe), not in a
// terminal of the server: Genie can neither run them nor read their output.
export const PAGE_ONLY_LANGUAGES = ["javascript"];

// extractJSON finds the JSON object in a model's answer: the whole text, or
// the text of a fenced block, or the first {...} that is balanced. Models that
// were told to send only JSON still sometimes wrap it.
export function extractJSON(text: string): unknown | null {
  const t = String(text || "").trim();
  if (!t) return null;
  const attempts: string[] = [t];
  const fence = t.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fence) attempts.push(fence[1].trim());
  const start = t.indexOf("{");
  if (start >= 0) {
    let depth = 0;
    let inString = false;
    let escaped = false;
    for (let i = start; i < t.length; i++) {
      const c = t[i];
      if (inString) {
        if (escaped) escaped = false;
        else if (c === "\\") escaped = true;
        else if (c === '"') inString = false;
      } else if (c === '"') inString = true;
      else if (c === "{") depth++;
      else if (c === "}") {
        depth--;
        if (depth === 0) {
          attempts.push(t.slice(start, i + 1));
          break;
        }
      }
    }
  }
  for (const a of attempts) {
    try {
      const v = JSON.parse(a);
      if (v && typeof v === "object" && !Array.isArray(v)) return v;
    } catch (e) {
      // try the next form
    }
  }
  return null;
}

function clampSeconds(v: unknown, fallback: number = 5): number {
  const n = typeof v === "number" && isFinite(v) ? Math.round(v) : fallback;
  return Math.min(20, Math.max(1, n));
}

// parseStep checks the answer of a step.
export function parseStep(content: string): { ok: true; step: Step } | { ok: false; error: string } {
  const v = extractJSON(content) as Record<string, unknown> | null;
  if (!v) return { ok: false, error: "the answer was not a JSON object" };
  const dropped: string[] = [];
  const say = typeof v.say === "string" ? v.say.trim().slice(0, MAX_SAY) : "";
  let done = v.done === true;
  let finish = false;
  let overflow = false;
  const actions: Action[] = [];
  const raw = Array.isArray(v.actions) ? v.actions : v.actions === undefined ? [] : null;
  if (raw === null) dropped.push("actions has to be a list");
  (raw || []).forEach((item, i) => {
    const a = item as Record<string, unknown> | null;
    // finish is not something to carry out: it does not count towards the
    // limit and it comes last. It is not honoured after an action that was
    // left out for the limit, which the model expected to happen first.
    if (a && typeof a === "object" && a.type === "finish") {
      if (overflow) dropped.push(`action ${i + 1} left out: at most ${MAX_ACTIONS} actions in a step`);
      else finish = true;
      return;
    }
    if (actions.length >= MAX_ACTIONS) {
      overflow = true;
      dropped.push(`action ${i + 1} left out: at most ${MAX_ACTIONS} actions in a step`);
      return;
    }
    if (!a || typeof a !== "object" || typeof a.type !== "string") {
      dropped.push(`action ${i + 1} left out: it has no type`);
      return;
    }
    if (!(ACTIONS as readonly string[]).includes(a.type)) {
      dropped.push(`action ${i + 1} left out: "${a.type.slice(0, 40)}" is not an action`);
      return;
    }
    const type = a.type as ActionType;
    switch (type) {
      case "editor_write":
      case "editor_insert":
        if (typeof a.text !== "string" || (type === "editor_write" && a.text === "")) {
          dropped.push(`${type} left out: it needs "text"`);
        } else if (a.text.length > MAX_TEXT) {
          dropped.push(`${type} left out: the text is longer than ${MAX_TEXT} characters`);
        } else {
          actions.push({ type, text: a.text });
        }
        break;
      case "set_language":
        if (typeof a.language !== "string" || !a.language.trim() || a.language.length > 40) {
          dropped.push("set_language left out: it needs a language name");
        } else {
          actions.push({ type, language: a.language.trim() });
        }
        break;
      case "terminal_type": {
        // one line of plain text: what the user is shown is all there is
        const line = typeof a.text === "string" ? a.text.replace(/[\r\n]+$/, "") : null;
        if (line === null || line.trim() === "") {
          dropped.push('terminal_type left out: it needs "text"');
        } else if (/[\u0000-\u001f\u007f-\u009f]/.test(line)) {
          dropped.push("terminal_type left out: the text has to be one line of plain text (no new lines, tabs or control characters)");
        } else if (line.length > MAX_TERMINAL_TEXT) {
          dropped.push(`terminal_type left out: the line is longer than ${MAX_TERMINAL_TEXT} characters`);
        } else {
          actions.push({ type, text: line, waitSeconds: clampSeconds(a.wait_seconds, 3) });
        }
        break;
      }
      case "terminal_new_tab":
        if (a.language !== undefined && (typeof a.language !== "string" || a.language.length > 40)) {
          dropped.push("terminal_new_tab left out: language has to be a language name");
        } else {
          const lang = typeof a.language === "string" ? a.language.trim() : "";
          actions.push(lang ? { type, language: lang } : { type });
        }
        break;
      case "terminal_select_tab":
      case "terminal_close_tab": {
        const n = typeof a.tab === "number" && Number.isInteger(a.tab) ? a.tab : NaN;
        if (!(n >= 1 && n <= MAX_TABS)) dropped.push(`${type} left out: "tab" has to be a number from 1 to ${MAX_TABS}`);
        else actions.push({ type, tab: n });
        break;
      }
      case "files_list": {
        const p = cleanPath(a.path === undefined ? "" : a.path, true);
        if (p === null) dropped.push(`files_list left out: "path" has to be a folder relative to the home directory (like "src"), or left out for the home directory`);
        else actions.push({ type, path: p });
        break;
      }
      case "files_open":
      case "files_cut":
      case "files_copy":
      case "files_delete": {
        const p = cleanPath(a.path, false);
        if (p === null) dropped.push(`${type} left out: "path" has to be a path relative to the home directory, as files_list shows it (no leading slash, no "..")`);
        else actions.push({ type, path: p });
        break;
      }
      case "files_new": {
        const p = cleanPath(a.path, false);
        const kind = a.kind === undefined || a.kind === "file" ? "file" : a.kind === "folder" ? "folder" : null;
        if (p === null) dropped.push(`files_new left out: "path" has to be the new path relative to the home directory (like "src/util.py")`);
        else if (kind === null) dropped.push(`files_new left out: "kind" is "file" or "folder"`);
        else actions.push({ type, path: p, kind });
        break;
      }
      case "files_save":
        actions.push({ type });
        break;
      case "files_rename": {
        const p = cleanPath(a.path, false);
        const n = cleanName(a.name);
        if (p === null) dropped.push(`files_rename left out: "path" has to be a path relative to the home directory`);
        else if (n === null) dropped.push(`files_rename left out: "name" has to be one new name, without a slash`);
        else actions.push({ type, path: p, name: n });
        break;
      }
      case "files_paste": {
        const to = cleanPath(a.to === undefined ? "" : a.to, true);
        if (to === null) dropped.push(`files_paste left out: "to" has to be a folder relative to the home directory, or left out for the home directory`);
        else actions.push({ type, to });
        break;
      }
      case "files_move": {
        const p = cleanPath(a.path, false);
        const to = cleanPath(a.to === undefined ? "" : a.to, true);
        if (p === null) dropped.push(`files_move left out: "path" has to be a path relative to the home directory`);
        else if (to === null) dropped.push(`files_move left out: "to" has to be a folder relative to the home directory, or left out for the home directory`);
        else actions.push({ type, path: p, to });
        break;
      }
      case "read_output":
        actions.push({ type, waitSeconds: clampSeconds(a.wait_seconds) });
        break;
      default:
        actions.push({ type });
    }
  });
  if (finish) {
    done = true;
    actions.push({ type: "finish" });
  }
  // A step that asks for nothing has nothing to report back: the task is over
  // (Genie said what it had to say, or asked the user something). Going on
  // would only use up steps on more words.
  if (actions.length === 0 && dropped.length === 0) done = true;
  return { ok: true, step: { say, actions, done, dropped } };
}

// a short line for the step list, for an action
export function describe(a: Action): string {
  switch (a.type) {
    case "editor_write":
      return "Writing in the editor";
    case "editor_insert":
      return "Inserting code in the editor";
    case "set_language":
      return `Switching the language to ${a.language}`;
    case "run":
      return "Pressing Run";
    case "debug":
      return "Pressing Debug";
    case "terminal_type":
      return "Typing in the terminal: " + (a.text || "");
    case "terminal_interrupt":
      return "Pressing Ctrl+C in the terminal";
    case "terminal_reconnect":
      return "Restarting the terminal";
    case "terminal_new_tab":
      return a.language ? "Opening a new terminal tab in " + a.language : "Opening a new terminal tab";
    case "terminal_select_tab":
      return "Switching to terminal tab " + a.tab;
    case "terminal_close_tab":
      return "Closing terminal tab " + a.tab;
    case "files_list":
      return "Listing the files in " + where(a.path);
    case "files_open":
      return "Opening " + a.path + " in the editor";
    case "files_new":
      return "Creating the " + (a.kind === "folder" ? "folder " : "file ") + a.path;
    case "files_save":
      return "Saving the editor to the open file";
    case "files_rename":
      return "Renaming " + a.path + " to " + a.name;
    case "files_cut":
      return "Cutting " + a.path;
    case "files_copy":
      return "Copying " + a.path;
    case "files_paste":
      return "Pasting into " + where(a.to);
    case "files_move":
      return "Moving " + a.path + " into " + where(a.to);
    case "files_delete":
      return "Deleting " + a.path;
    case "read_output":
      return "Reading the output";
    default:
      return "Finishing";
  }
}

// What to tell the model when its answer was cut off at the length limit: the
// JSON is not whole, so nothing of it can be used.
export const CUT_OFF = "the answer was cut off because it was too long: send a shorter file, or change less in one step";

// results is the next message to the model: what happened to each action, and
// what the dropped ones were.
export function resultsMessage(results: StepResult[], dropped: string[]): string {
  const body = results.map((r) => {
    const o: StepResult = { type: r.type, status: r.status };
    if (r.detail) o.detail = r.detail;
    if (r.accepted !== undefined) o.accepted = r.accepted;
    if (r.rejected !== undefined) o.rejected = r.rejected;
    if (r.output !== undefined) o.output = r.output.length > MAX_OUTPUT ? "..." + r.output.slice(-MAX_OUTPUT) : r.output;
    return o;
  });
  const msg: { step_results: StepResult[]; not_done?: string[] } = { step_results: body };
  if (dropped.length) msg.not_done = dropped;
  return JSON.stringify(msg);
}

// A language of the picker, by the name the model gave: the visible name or the
// value, in any case. options are the picker's {value, text}.
export function findLanguage(options: { value: string; text: string }[], name: string): string | null {
  const n = name.trim().toLowerCase();
  for (const o of options) {
    if (o.value.toLowerCase() === n || o.text.trim().toLowerCase() === n) return o.value;
  }
  for (const o of options) {
    if (o.text.trim().toLowerCase().startsWith(n) && n.length >= 2) return o.value;
  }
  return null;
}
