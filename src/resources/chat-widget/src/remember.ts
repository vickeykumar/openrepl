// "Remember this": a message that starts with "remember" (or /remember) is added to GENIE.md in the workspace, the notes that
// every task of the project starts with (agent.ts projectNotes). It is the user's own words, written by the user's own command:
// no model is asked, and nothing is written that the user did not type.

export const NOTES_HEADER = "# Notes for Genie\n\nWhat Genie should remember about this project. Edit or delete any line.\n\n";
export const NOTE_MAX = 400; // characters of one note
export const NOTES_MAX_LINES = 60; // lines of notes: Genie reads the first 80 lines of the file at the start of a task

// The note in a message like "remember: use tabs", "remember that tests run with pytest" or "/remember no semicolons"; null when the
// message is not one. "Remember" alone, or a question ("remember how we did it?"), is not a note.
export function parseRemember(msg: string): string | null {
  const m = /^\s*(?:\/remember|remember(?:\s+this(?=\s*[:,-]))?)(?:\s*[:,-]\s*|\s+)(?:that\s+)?([\s\S]+)$/i.exec(String(msg || ""));
  if (!m) return null;
  const text = m[1].replace(/\s+/g, " ").trim();
  if (!text || /\?$/.test(text)) return null;
  return text.slice(0, NOTE_MAX);
}

// The file with the note added as one line at the end. The same note twice is not added again. Returns the new text, or
// null when the note is already there or the file already holds as many lines of notes as Genie reads.
export function addNote(existing: string, note: string): string | null {
  const line = "- " + note.replace(/\s+/g, " ").trim();
  const text = existing && existing.trim() ? existing.replace(/\s+$/, "") + "\n" : NOTES_HEADER;
  const lines = text.split("\n");
  if (lines.some((l) => l.trim().toLowerCase() === line.toLowerCase())) return null;
  if (lines.length >= NOTES_MAX_LINES) return null;
  return text + line + "\n";
}
