// The tabs of the Genie panel, as plain code with no DOM (node runs it in test/chat-tabs.test.ts): what a chat file holds
// and how it becomes a conversation again, how a title is made, which tab closes when there are too many, how the list of
// all chats is grouped and searched, and what the strip says about a tab. index.ts draws it; chat-store.ts talks to the
// server (server/genie_chats.go). The format is docs/lld/19-genie-agent.md, section 9.

export interface ChatLine {
  t: "meta" | "title" | "state" | "user" | "answer" | "task" | "summary";
  id?: string;
  created?: string;
  context?: string;
  language?: string;
  file?: string;
  text?: string;
  at?: string;
  ts?: number;
  model?: string;
  effort?: string;
  mode?: string;
  tokens?: number;
  steps?: number;
  checked?: string;
  changed?: string;
  outcome?: string;
}

export interface ChatSummary {
  id: string;
  title: string;
  created?: string;
  updated: number; // seconds
  messages: number;
  context?: string;
  language?: string;
  size: number;
}

export const MAX_TITLE = 60;
export const MAX_LINE_TEXT = 60000; // the server keeps a message up to 64 KB

const ID_ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789";

// A chat id: 8 characters, lower case letters and digits (the server accepts 4 to 16).
export function newChatId(random: () => number = Math.random): string {
  let id = "";
  for (let i = 0; i < 8; i++) id += ID_ALPHABET[Math.floor(random() * ID_ALPHABET.length) % ID_ALPHABET.length];
  return id;
}

// The title of a chat from its first message: one line, cut at a word when it is long.
export function titleFrom(text: string): string {
  const one = String(text || "")
    .replace(/\[user-[^\]]*\]\s*/, "")
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (one === "") return "New chat";
  if (one.length <= MAX_TITLE) return one;
  const cut = one.slice(0, MAX_TITLE);
  const at = cut.lastIndexOf(" ");
  return (at > 30 ? cut.slice(0, at) : cut).trimEnd() + "…";
}

// "14:52" for a time
export function clock(ms: number): string {
  const d = new Date(ms);
  return ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2);
}

// A text that fits in a line of a chat file.
export function fitText(text: string): string {
  const s = String(text || "");
  return s.length > MAX_LINE_TEXT ? s.slice(0, MAX_LINE_TEXT) + "\n… (cut: too long to keep)" : s;
}

// ---- reading a chat ------------------------------------------------------------------

export interface RestoredMessage {
  kind: "user" | "answer" | "task";
  text: string;
  ts: number;
  model?: string;
  tokens?: number;
  summary?: string; // a task: how it went, in a line
}

export interface Restored {
  title: string;
  context: string;
  language: string;
  file: string;
  mode: string;
  model: string;
  effort: string;
  messages: RestoredMessage[];
  summary: { text: string; covered: number } | null; // the last summary line: it stands for the messages before index `covered`
}

// What the task line says, in words: "Finished, 6 steps, checked with pytest -q, changed auth.py +9 −2".
export function taskSummary(l: ChatLine): string {
  const bits = [l.outcome === "done" ? "Finished" : l.outcome === "stopped" ? "Stopped" : l.outcome ? "Ended" : ""];
  if (typeof l.steps === "number") bits.push(l.steps + (l.steps === 1 ? " step" : " steps"));
  if (l.checked) bits.push("checked with " + l.checked);
  if (l.changed) bits.push("changed " + l.changed);
  return bits.filter(Boolean).join(", ");
}

// The lines of a chat file as a conversation: the last title and state win, the messages are in order.
export function restore(lines: ChatLine[]): Restored {
  const r: Restored = { title: "", context: "chat", language: "", file: "", mode: "chat", model: "", effort: "", messages: [], summary: null };
  lines.forEach((l, i) => {
    if (!l || typeof l !== "object") return;
    const ts = typeof l.ts === "number" ? l.ts : i;
    switch (l.t) {
      case "meta":
        if (l.context) r.context = l.context;
        break;
      case "title":
        if (l.text) r.title = l.text;
        break;
      case "state":
        if (l.mode) r.mode = l.mode;
        if (l.model) r.model = l.model;
        if (l.effort) r.effort = l.effort;
        break;
      case "user":
        r.messages.push({ kind: "user", text: String(l.text || ""), ts });
        if (l.language) r.language = l.language;
        if (l.file) r.file = l.file;
        break;
      case "answer":
        r.messages.push({ kind: "answer", text: String(l.text || ""), ts, model: l.model, tokens: l.tokens });
        break;
      case "task":
        r.messages.push({ kind: "task", text: String(l.text || ""), ts, summary: taskSummary(l) });
        break;
      case "summary": {
        // the last `steps` questions and answers before this line were kept as they were; the ones before them are in the text
        let keep = typeof l.steps === "number" ? Math.max(0, l.steps) : 4;
        let at = r.messages.length;
        while (at > 0 && keep > 0) {
          at--;
          if (r.messages[at].kind !== "task") keep--;
        }
        if (l.text) r.summary = { text: String(l.text), covered: at };
        break;
      }
    }
  });
  if (!r.title) {
    const first = r.messages.find((m) => m.kind === "user");
    r.title = first ? titleFrom(first.text) : "New chat";
  }
  return r;
}

// The conversation the model is sent: the user's words and the answers; a task is not in it (the recap of tasks is the runner's).
export function toHistory(messages: RestoredMessage[], uid: string, summary: { text: string; covered: number } | null = null): { role: string; content: string }[] {
  const lead = summary && summary.text ? [{ role: "system", content: SUMMARY_PREFIX + summary.text }] : [];
  return lead.concat(messages
    .slice(summary ? summary.covered : 0)
    .filter((m) => m.kind === "user" || m.kind === "answer")
    .map((m) => (m.kind === "user" ? { role: "user", content: "[user-" + uid + "] " + m.text } : { role: "assistant", content: m.text }))
  );
}

// ---- a long chat, summarized ------------------------------------------------------------

export const SUMMARY_PREFIX = "Summary of the earlier chat: ";
export const SUMMARY_KEEP = 4; // the last turns that are sent as they were
export const MAX_SUMMARY = 1500; // characters of a summary

export interface SummaryPlan {
  prior: string; // the summary there is already, or ""
  older: { role: string; content: string }[]; // the turns that go into the new one
  drop: number; // how many entries of the history they are
}

// About how many tokens a text is: four characters to a token, as the usage line does.
export function estimateTokens(text: string): number {
  return Math.ceil(String(text || "").length / 4);
}

// Whether the older turns (all but the last SUMMARY_KEEP) are longer than `threshold` tokens, and what to summarize. Only the
// turns count: the editor and the terminal text is not in them (it is sent as it is, in every request).
// `skip` is the entries at the start of the history that are not turns (the prompts and the editor snapshot); a summary
// that was made before is the first entry after them.
export function summaryPlan(history: { role: string; content: string }[], skip: number, threshold: number): SummaryPlan | null {
  if (!(threshold > 0)) return null;
  const rest = history.slice(skip);
  const hasPrior = rest.length > 0 && rest[0].role === "system" && rest[0].content.startsWith(SUMMARY_PREFIX);
  const turns = hasPrior ? rest.slice(1) : rest;
  if (turns.length <= SUMMARY_KEEP) return null;
  const older = turns.slice(0, turns.length - SUMMARY_KEEP);
  const tokens = older.reduce((n, m) => n + estimateTokens(m.content), 0);
  if (tokens <= threshold) return null;
  return { prior: hasPrior ? rest[0].content.slice(SUMMARY_PREFIX.length) : "", older, drop: older.length + (hasPrior ? 1 : 0) };
}

// The history with the summary in place of the older turns.
export function applySummary(history: { role: string; content: string }[], skip: number, plan: SummaryPlan, text: string): { role: string; content: string }[] {
  return history.slice(0, skip).concat([{ role: "system", content: SUMMARY_PREFIX + text }], history.slice(skip + plan.drop));
}

export const SUMMARY_PROMPT =
  "Write a summary of the chat so far, in at most 1200 characters, for yourself to continue it: what the user wants, what was decided, " +
  "the names of files, the language, what is done and what is still open. Facts only, no greeting, no advice. Plain text.";

// What is sent to the model to summarize: the old summary and the turns that go into the new one.
export function summaryInput(plan: SummaryPlan): string {
  const lines = plan.older.map((m) => (m.role === "user" ? "User: " : "Genie: ") + String(m.content).replace(/^\[user-[^\]]*\]\s*/, "").slice(0, 1500));
  return (plan.prior ? "Summary so far: " + plan.prior + "\n\n" : "") + "Turns to add:\n" + lines.join("\n");
}

// The summary the model wrote, or "" when it is not usable (empty, a few words, or a refusal that is JSON).
export function cleanSummary(raw: unknown): string {
  const t = String(raw || "").trim();
  if (t.length < 30 || t.startsWith("{") || t.startsWith("[")) return "";
  return t.length > MAX_SUMMARY ? t.slice(0, MAX_SUMMARY) + "..." : t;
}

// ---- the strip -------------------------------------------------------------------------

export interface TabView {
  id: string;
  running: boolean; // a task is running in it
  needsYou: boolean; // the task waits for the user
  unread: boolean; // an answer came while it was not shown
  busy: boolean; // an answer is being written
}

// What the strip shows at the side of a tab: a ring while a task runs, a dot when it needs you or an answer is ready.
export function tabMark(t: TabView, active: boolean): "ring" | "needs" | "dot" | "none" {
  if (t.needsYou) return "needs";
  if (t.running) return "ring";
  if (t.busy && !active) return "ring";
  if (t.unread && !active) return "dot";
  return "none";
}

// The tab to close to make room for a new one: the oldest in the strip that is not shown, not running, and has no unread
// answer or question waiting. null when every tab is in use.
export function tabToClose(tabs: TabView[], activeId: string): string | null {
  for (const t of tabs) {
    if (t.id !== activeId && !t.running && !t.needsYou && !t.unread && !t.busy) return t.id;
  }
  return null;
}

// Where a tab goes when it is moved: the ids in the new order (the same ids; an unknown id is ignored).
export function moveTab(ids: string[], id: string, to: number): string[] {
  const from = ids.indexOf(id);
  if (from < 0) return ids.slice();
  const out = ids.slice();
  out.splice(from, 1);
  out.splice(Math.max(0, Math.min(out.length, to)), 0, id);
  return out;
}

// The tab that is shown when one is closed: the one to its right, else the one to its left.
export function afterClose(ids: string[], closing: string): string | null {
  const i = ids.indexOf(closing);
  if (i < 0) return ids[0] || null;
  return ids[i + 1] || ids[i - 1] || null;
}

// ---- all chats --------------------------------------------------------------------------

export interface ChatGroup {
  label: string;
  chats: ChatSummary[];
}

// Open chats first, then the closed ones by day.
export function groupChats(chats: ChatSummary[], openIds: string[], now: Date): ChatGroup[] {
  const open = new Set(openIds);
  const groups: ChatGroup[] = [];
  const opened = chats.filter((c) => open.has(c.id));
  if (opened.length) groups.push({ label: "Open", chats: opened });
  const closed = chats.filter((c) => !open.has(c.id)).sort((a, b) => b.updated - a.updated);
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const today = startOfDay(now);
  const byLabel = new Map<string, ChatSummary[]>();
  closed.forEach((c) => {
    const day = startOfDay(new Date(c.updated * 1000));
    const label = day >= today ? "Closed today" : day >= today - 86400000 ? "Closed yesterday" : "Closed earlier";
    byLabel.set(label, (byLabel.get(label) || []).concat([c]));
  });
  ["Closed today", "Closed yesterday", "Closed earlier"].forEach((label) => {
    const list = byLabel.get(label);
    if (list) groups.push({ label, chats: list });
  });
  return groups;
}

// The chats whose title or language has every word of the query, without regard to case.
export function filterChats(chats: ChatSummary[], query: string): ChatSummary[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return chats;
  return chats.filter((c) => {
    const hay = (c.title + " " + (c.language || "")).toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

// "12 chats · 1.4 MB"
export function sizeText(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return Math.round(bytes / 102.4) / 10 + " KB";
  return Math.round(bytes / 104857.6) / 10 + " MB";
}

// What a chat was about and what the editor has now, in words, when they differ: null when there is nothing to say.
export function staleParts(chat: { file: string; language: string }, now: { file: string; language: string }): { was: string; is: string } | null {
  if (!chat.file && !chat.language) return null;
  const same = (a: string, b: string) => (a || "").trim() === (b || "").trim();
  if (same(chat.file, now.file) && same(chat.language, now.language)) return null;
  const was = [chat.file, chat.language ? "(" + chat.language + ")" : ""].filter(Boolean).join(" ");
  const nowFile = now.file || (now.language ? "an unnamed file" : "");
  const is = [nowFile, now.language ? "(" + now.language + ")" : ""].filter(Boolean).join(" ") || "something else";
  return { was, is };
}

// A line for when a chat is shown again and the editor is not what it was about: "" when there is nothing to say.
export function staleNote(chat: { file: string; language: string }, now: { file: string; language: string }): string {
  const p = staleParts(chat, now);
  if (!p) return "";
  return "This chat was about " + p.was + ". The editor now has " + p.is + ". Genie reads the editor as it is now when you ask something new.";
}

// The same, for the agent at the start of a task: it is told, so that it does not work on whatever the editor holds now.
export function staleForAgent(chat: { file: string; language: string }, now: { file: string; language: string }): string {
  const p = staleParts(chat, now);
  if (!p) return "";
  return (
    "Note: this chat was about " + p.was + ", but the editor now has " + p.is + " (the page was reloaded or the language changed). " +
    "If the task continues that work, first switch the language (set_language) and open the file (files_open) it was about; " +
    "if you are not sure which project is meant, ask the user in \"say\" and set \"done\": true. Do not start a different project in the language that is open now."
  );
}
