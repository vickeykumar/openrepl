// What chat mode sends and offers, as plain code with no DOM (node runs it in
// test/chat-send.test.ts): the conversation sent to the model with less in it, how
// long an answer may be, and the one-click requests for the selection and the
// last error. index.ts uses it.

export type Msg = { role: string; content: string };

// ---- sending less ---------------------------------------------------------------------

const OLD_CODE_LINES = 40; // a code block of an older answer longer than this is cut
const KEEP_LINES = 12;
const KEEP_ANSWERS = 2; // the last answers stay whole

// An older answer without the middle of its long code blocks (the code is in the editor, or was shown to the user).
export function shortenOldCode(content: string): string {
  return content.replace(/```([^\n`]*)\n([\s\S]*?)```/g, (whole, lang: string, body: string) => {
    const lines = body.replace(/\n$/, "").split("\n");
    if (lines.length <= OLD_CODE_LINES) return whole;
    return "```" + lang + "\n" + lines.slice(0, KEEP_LINES).join("\n") + "\n... (" + (lines.length - KEEP_LINES) + " more lines left out of this older answer)\n```";
  });
}

// The conversation of a request: the history with older answers shortened, and the
// editor and terminal text at the end only when it is not the one the history already
// holds (the entry before the conversation, updated at each message). The original is not changed.
export function messagesToSend(history: Msg[], mandatory: number, fresh: Msg): Msg[] {
  const answers: number[] = [];
  history.forEach((m, i) => {
    if (i >= mandatory && m.role === "assistant") answers.push(i);
  });
  const old = new Set(answers.slice(0, Math.max(0, answers.length - KEEP_ANSWERS)));
  const out = history.map((m, i) => (old.has(i) ? { role: m.role, content: shortenOldCode(m.content) } : m));
  const held = mandatory > 0 && history[mandatory - 1] ? history[mandatory - 1].content : null;
  if (held !== fresh.content) out.push(fresh);
  return out;
}

// ---- how long an answer may be --------------------------------------------------------

const MIN_BUDGET = 400;
const MAX_BUDGET = 2400;
const CODE_WORDS = /\b(write|implement|create|generate|refactor|rewrite|convert|translate|complete|script|snippet)\b/i;
const LONG_WORDS = /\b(in detail|step by step|walk me through|thoroughly|explain how|explain why|full example|complete example)\b/i;

// The most tokens an answer may have for a message: the usual number, more when the user
// asks for code or for a long explanation, less for a short question. The admin's caps
// on the server stay the ceiling.
export function answerBudget(text: string, base: number): number {
  const t = String(text || "").trim();
  let n = base;
  if (CODE_WORDS.test(t) || LONG_WORDS.test(t)) n = base * 2;
  else if (t.length <= 80 && !/```/.test(t)) n = Math.round(base * 0.6);
  return Math.max(MIN_BUDGET, Math.min(MAX_BUDGET, Math.round(n)));
}

// ---- Agent for what is a task -----------------------------------------------------------

const TASK_WORDS = /\b(create|build|implement|write|add|fix|refactor|rename|move|delete|install|set up|make|convert|run|test|debug|update|change|replace|remove)\b/i;

// Whether what the user asked is a job to be done in their workspace (more than a question).
export function looksLikeTask(text: string): boolean {
  const t = String(text || "").trim();
  return t.length >= 15 && TASK_WORDS.test(t) && !/^\s*(what|why|how does|explain|is |are |can you explain)\b/i.test(t);
}

// ---- the one-click requests -------------------------------------------------------------

const MAX_SELECTION = 4000;
const ERROR_WORDS = /\b(error|exception|traceback|fatal|segmentation fault|undefined|not defined|cannot|can't find|no such|failed|panic|syntax)\b/i;

// The lines of the terminal that look like an error, with what comes before them; "" when none do.
export function errorBlock(terminal: string, maxLines: number = 30): string {
  const lines = String(terminal || "").replace(/\s+$/, "").split("\n");
  let last = -1;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (ERROR_WORDS.test(lines[i])) {
      last = i;
      break;
    }
  }
  if (last < 0) return "";
  const start = Math.max(0, last - maxLines + 1);
  // a traceback starts a few lines above its last line: take what is there
  return lines.slice(start, last + 1).join("\n").trim();
}

export function selectionRequest(selection: string, language: string): string {
  const s = selection.length > MAX_SELECTION ? selection.slice(0, MAX_SELECTION) + "\n..." : selection;
  return "Explain this " + (language ? language + " " : "") + "code, line by line where it helps:\n\n```\n" + s + "\n```";
}

export function errorRequest(block: string): string {
  return "My program printed this error. What does it mean, and how do I fix it in my code?\n\n```\n" + block + "\n```";
}

// Whether a code block starts with a function or a class: only then is it clear where "Apply as a diff" puts it
// (the page finds the definition by its name: js/src/page/lib-region.mjs).
const DEFINES = /^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:def|class|function|func|fn|pub\s+fn|const\s+\w+\s*=\s*(?:async\s*)?\(|(?:public|private|protected|static)\s+\S+\s+\w+\s*\()\b/;
export function definesSomething(code: string): boolean {
  for (const line of String(code || "").split("\n")) {
    if (line.trim() === "" || /^\s*(#|\/\/|\/\*|\*|@)/.test(line)) continue;
    return DEFINES.test(line) || /^\s*(?:(?:public|private|protected|static|final)\s+)*[A-Za-z_][\w<>\[\]:,*&]*\s+[A-Za-z_]\w*\s*\([^;]*\)\s*\{?\s*$/.test(line) || /^\s*(?:const|let|var)\s+\w+\s*=\s*(?:async\s*)?(?:\([^)]*\)|\w+)\s*=>/.test(line);
  }
  return false;
}
