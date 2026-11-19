// Search across chats and the filter chips of History (canvas "Genie: search and star"): plain code with no DOM, run in the page over
// the chats the page reads from the workspace (chat-store.ts). Nothing is sent anywhere. It looks in titles and in what the user and Genie
// wrote and the summary of each task, not in the steps of a task.

import type { ChatLine, ChatSummary } from "./chat-tabs";

export type ChatFilter = "all" | "starred" | "chat" | "agent";
export const FILTERS: readonly ChatFilter[] = ["all", "starred", "chat", "agent"];

export interface SearchResult {
  chat: ChatSummary;
  snippet: string; // the words around the first match, "" for a match in the title only
  word: string; // the first word that matched (for finding the message again)
}

// "chat" or "agent": the last state line says
export function modeOf(lines: ChatLine[] | null | undefined): "chat" | "agent" {
  let mode: "chat" | "agent" = "chat";
  (lines || []).forEach((l) => {
    if (l && l.t === "state" && l.mode) mode = l.mode === "agent" ? "agent" : "chat";
  });
  return mode;
}

export function wordsOf(query: string): string[] {
  return String(query || "").toLowerCase().split(/\s+/).filter(Boolean);
}

// The text of a chat that is searched: what the user and Genie wrote, and what each task did, in order.
export function textsOf(lines: ChatLine[] | null | undefined): string[] {
  const out: string[] = [];
  (lines || []).forEach((l) => {
    if (!l || typeof l.text !== "string") return;
    if (l.t === "user" || l.t === "answer" || l.t === "task" || l.t === "summary") out.push(l.text);
  });
  return out;
}

// About `width` characters of the text around the word, with "…" where it was cut.
export function snippetAround(text: string, word: string, width: number = 80): string {
  const flat = text.replace(/\s+/g, " ").trim();
  const at = flat.toLowerCase().indexOf(word.toLowerCase());
  if (at < 0) return flat.slice(0, width);
  const start = Math.max(0, at - Math.floor(width / 3));
  const end = Math.min(flat.length, start + width);
  return (start > 0 ? "…" : "") + flat.slice(start, end) + (end < flat.length ? "…" : "");
}

// The snippet cut into parts, those that hold a word of the query marked, so that the page can draw them without HTML.
export function markWords(text: string, words: string[]): { text: string; hit: boolean }[] {
  const ws = words.filter(Boolean).map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  if (!ws.length) return [{ text, hit: false }];
  const re = new RegExp("(" + ws.join("|") + ")", "ig");
  return text.split(re).filter((t) => t !== "").map((t) => ({ text: t, hit: re.test(t) && (re.lastIndex = 0, true) }));
}

// Does the chat pass the chip? A chip of Chat or Agent needs the lines of the chat (null: not read yet, so it is shown).
export function passes(chat: ChatSummary, lines: ChatLine[] | null | undefined, filter: ChatFilter): boolean {
  if (filter === "all") return true;
  if (filter === "starred") return !!chat.starred;
  if (!lines) return true;
  return modeOf(lines) === filter;
}

// The chats that pass the chip and have every word of the query in the title or in one text (not necessarily the same one), newest
// first. With no words every chat that passes is a result, with no snippet. A chat whose lines are not read yet matches by its title.
export function searchChats(chats: ChatSummary[], bodies: Map<string, ChatLine[]>, query: string, filter: ChatFilter): SearchResult[] {
  const words = wordsOf(query);
  const out: SearchResult[] = [];
  chats.forEach((c) => {
    const lines = bodies.get(c.id) || null;
    if (!passes(c, lines, filter)) return;
    if (words.length === 0) {
      out.push({ chat: c, snippet: "", word: "" });
      return;
    }
    const title = (c.title + " " + (c.language || "")).toLowerCase();
    const texts = lines ? textsOf(lines) : [];
    const joined = texts.join("\n").toLowerCase();
    const all = words.every((w) => title.includes(w) || joined.includes(w));
    if (!all) return;
    // the first word that is in a message gives the snippet; a match in the title only has none
    let snippet = "";
    let word = words[0];
    for (const w of words) {
      const t = texts.find((x) => x.toLowerCase().includes(w));
      if (t) {
        snippet = snippetAround(t, w);
        word = w;
        break;
      }
    }
    out.push({ chat: c, snippet, word });
  });
  return out.sort((a, b) => b.chat.updated - a.chat.updated);
}
