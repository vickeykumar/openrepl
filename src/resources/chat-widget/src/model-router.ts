// Auto model choice (canvas "Genie: Auto model choice"): which of two models, a small one and a strong one that the admin named, answers
// this message. It is decided by rules and no model is asked, so the choice costs nothing, is the same every time and can be said in one
// line. A plain question in Chat goes to the small model; Agent mode, a message that asks for a change, one with files attached, and one
// with code in it go to the strong one. The note under the answer says which and why, and one click asks again with the strong model.

export interface AutoConfig {
  small: string;
  strong: string;
  default: "new" | "all" | "none";
}

export type AutoSize = "small" | "strong";

export interface AutoPick {
  model: string;
  size: AutoSize;
  why: string; // "a plain question", "it asks for a change", "files are attached", "it has code in it", "Agent mode"
}

// the words of a change (the same kind of words the prompt groups use for the editor)
const CHANGE = /\b(make|makes|making|fix\w*|add\w*|chang\w*|build\w*|run\w*|creat\w*|writ\w*|edit\w*|refactor\w*|updat\w*|implement\w*|replac\w*|remov\w*|delet\w*|renam\w*|convert\w*|generat\w*|rewrit\w*|improv\w*|optimi[sz]\w*|debug\w*|install\w*|deploy\w*|set ?up|move|moving|extract\w*|split|merge|translate|port|migrat\w*)\b/i;
const CODE_IN_IT = /```|^\s{4,}\S|\n\s{2,}\S.*[{};]\s*$/m;
export const LONG_MESSAGE = 700; // characters

export function routeModel(o: { mode: "chat" | "agent"; message: string; attached: number; cfg: AutoConfig; usable?: (id: string) => boolean }): AutoPick {
  const usable = o.usable || (() => true);
  const want = (size: AutoSize, why: string): AutoPick => {
    const model = size === "strong" ? o.cfg.strong : o.cfg.small;
    const other = size === "strong" ? o.cfg.small : o.cfg.strong;
    // a model that cannot be used by this person (a guest, Agent mode): the other one answers, and the note says so
    if (!usable(model) && usable(other)) return { model: other, size: size === "strong" ? "small" : "strong", why: why + " (the other model is not open to you)" };
    return { model, size, why };
  };
  if (o.mode === "agent") return want("strong", "Agent mode");
  const m = String(o.message || "");
  if (o.attached > 0) return want("strong", "files are attached");
  if (CODE_IN_IT.test(m)) return want("strong", "it has code in it");
  if (m.length > LONG_MESSAGE) return want("strong", "it is a long message");
  if (CHANGE.test(m)) return want("strong", "it asks for a change");
  return want("small", "a plain question");
}

// Does this person start on Auto? An admin says "new" (the default: people who never picked a model), "all" or "none"; a person's own
// choice of Auto or of a model (stored "1" or "0") always wins.
export function autoStarts(cfg: AutoConfig | null | undefined, stored: string | null, hasPick: boolean): boolean {
  if (!cfg) return false;
  if (stored === "1") return true;
  if (stored === "0") return false;
  if (cfg.default === "none") return false;
  if (cfg.default === "all") return true;
  return !hasPick;
}

// "Auto chose a small model: a plain question" / "Auto chose GPT-4o mini for this task: Agent mode"
export function autoNote(p: AutoPick, name: string, task: boolean): string {
  return task ? "Auto chose " + name + " for this task: " + p.why : "Auto chose a " + p.size + " model: " + p.why;
}
