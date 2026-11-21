// The "/" menu of the message box: typing / at the start of a message lists the commands the panel understands, each with a line that
// says what it does (the same shape as the # list of files). The commands are plain data here; index.ts runs them. A message that only
// starts with a path ("/usr/bin/python fails") shows no menu, because no command matches it.

import type { SkillInfo } from "./skills";

export interface SlashCommand {
  name: string; // "context"
  desc: string;
  // "run": it runs at once and the box is emptied; "insert": it puts its words in the box for the user to finish
  kind: "run" | "insert" | "skill";
  insert?: string;
  // a skill of the workspace, a plugin or the built-in ones (skills.ts): choosing it puts "/name " in the message
  skill?: SkillInfo;
  group?: string; // the title the menu shows above it
}

export const COMMANDS: readonly SlashCommand[] = [
  { name: "context", desc: "Show what goes with your next message, with sizes", kind: "run" },
  { name: "tour", desc: "Take the short tour of Genie again", kind: "run" },
  { name: "add-plugin", desc: "Browse and install plugins", kind: "run" },
  { name: "save-skill", desc: "Save what we just did as a skill, for example /save-skill release-notes", kind: "insert", insert: "/save-skill " },
  { name: "remember", desc: "Add a note to GENIE.md, for example /remember indent with tabs", kind: "insert", insert: "/remember " },
];

// The word after the "/" while it is being typed: only when the message starts with "/" and the caret is still in that first word.
// null when this is not the start of a command.
export function slashQuery(value: string, caret: number): string | null {
  const text = String(value || "");
  if (!text.startsWith("/")) return null;
  const upto = text.slice(1, Math.max(1, caret));
  if (/\s/.test(upto)) return null; // a space ends the command word
  return upto.toLowerCase();
}

// The commands whose name starts with the query, then those that contain it; all of them for an empty query.
export function matchCommands(query: string, list: readonly SlashCommand[] = COMMANDS): SlashCommand[] {
  const q = String(query || "").toLowerCase();
  if (!q) return list.slice();
  const starts = list.filter((c) => c.name.startsWith(q));
  const has = list.filter((c) => !c.name.startsWith(q) && c.name.includes(q));
  return starts.concat(has);
}
