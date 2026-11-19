// Which groups of the agent's actions a step is sent (server/agent.go, agentSystemPromptFor).
//
// The core is always sent. The others come when something says the task needs them: the words of the task, the page (the
// Preview is open, an .html file is open) or what the terminal printed (a module that is missing). A guess that is too low
// costs one step, because the model can ask for a group (need_tools) and a group that came stays for the rest of the task; a
// guess that is too high costs the tokens it saves. So the lists of words are wide and plain, and they are all in this file.

import type { Group } from "./agent-protocol";

// the groups in their fixed order (agent-protocol.ts GROUPS says the same: a test compares them; this file takes no runtime import so that
// the tests can load it as it is)
const ORDER: readonly Group[] = ["editor", "files", "terminal", "preview", "store"];

export interface GroupSignals {
  task: string; // what the user asked
  previewOpen: boolean;
  openFile: string; // the file open in the editor ("" for none)
  terminal: string; // the end of the terminal
  haveEditorText: boolean; // the editor holds something
}

const WORDS: Record<Group, RegExp> = {
  editor: /\b(creat\w*|edit\w*|writ\w*|chang\w*|fix\w*|add\w*|mak\w*|build\w*|refactor\w*|updat\w*|implement\w*|modif\w*|replac\w*|insert\w*|remov\w*|delet\w*|rewrit\w*|improv\w*|generat\w*|code|function|class|file|script|program|page|bug|error|save|open|convert|translat\w*|optimi[sz]\w*|complete|finish|continue)\b/i,
  files: /\b(renam\w*|mov(e|ing)|copy\w*|cut|paste|delet\w*|remov\w*|folder\w*|director\w*|new file|organi[sz]\w*|restructur\w*|tidy|clean ?up|trash|split (it )?into (files|modules)|separate files)\b/i,
  terminal: /\b(run\w*|execut\w*|install\w*|test\w*|build\w*|compil\w*|start\w*|serve\w*|server|debug\w*|command\w*|shell|bash|terminal|output|print\w*|pip|npm|node|python|git|curl|make|pytest|error|bug|crash\w*|fail\w*|exception|traceback|stack ?trace|works?|working|try|check)\b/i,
  preview: /\b(html|css|website|web ?site|web ?page|page|site|ui|button|form|browser|preview|layout|responsive|phone|mobile|tablet|click\w*|style\w*|dark mode|landing|navbar|menu|canvas|svg|animation|draw\w*|diagram|chart|game|console|screenshot|fits? (a )?(phone|screen)|looks?)\b/i,
  store: /\b(install\w*|librar\w*|package\w*|pip|npm|module\w*|import\w*|dependenc\w*|conda|app ?store|apt|requirements|numpy|pandas|flask|django|express|react|matplotlib|requests)\b/i,
};

// the page and the terminal say more than the words: these are checked on each step
const MISSING_MODULE = /ModuleNotFoundError|No module named|Cannot find module|ImportError|command not found|is not installed|cannot import/i;
const WEB_FILE = /\.(html?|css|js|mjs|jsx|ts|tsx|svg|vue)$/i;

// The groups a step is sent, in the fixed order of GROUPS: the ones already given to the task, and the ones the signals add.
export function groupsFor(have: readonly string[], s: GroupSignals): Group[] {
  const on = new Set<string>(have);
  const text = s.task || "";
  if (s.openFile || s.haveEditorText || WORDS.editor.test(text)) on.add("editor");
  if (WORDS.files.test(text)) on.add("files");
  if (WORDS.terminal.test(text) || MISSING_MODULE.test(s.terminal || "")) on.add("terminal");
  if (s.previewOpen || WEB_FILE.test(s.openFile || "") || WORDS.preview.test(text)) on.add("preview");
  if (WORDS.store.test(text) || MISSING_MODULE.test(s.terminal || "")) on.add("store");
  return ORDER.filter((g) => on.has(g));
}

// A group that a model asked for with need_tools: only the known ones, none twice, in the fixed order.
export function addGroups(have: readonly string[], asked: readonly string[]): Group[] {
  const on = new Set<string>([...have, ...asked]);
  return ORDER.filter((g) => on.has(g));
}
