// "What Genie sees now" (canvas "Genie: Context view"): one list, opened from the Context button in the header, of what goes with the next
// message, each line saying what, which part and how big. It is worked out from what the widget gathers for a message (index.ts
// getcurrentIDECode and takeContext) when the list opens, so it cannot drift from what is sent. Only sizes and names are shown, never the
// text. Tokens are an estimate (4 characters to a token), not the model's count.

export type CtxState = "sent" | "yours" | "off" | "empty";

export interface ContextFacts {
  editor: { on: boolean; file: string; chars: number; total: number; lines: number; limit: number };
  terminal: { lines: number; chars: number; limit: number };
  preview: { on: boolean; open: boolean; chars: number; parts: number /* headings in the outline */ };
  notes: { known: boolean; exists: boolean; lines: number; chars: number };
  chat: { sent: number; total: number; chars: number };
  attached: { names: string[]; chars: number };
  hidden: number; // files with a dot name that were left out of the attached items (never sent)
}

export interface CtxRow {
  id: "editor" | "terminal" | "preview" | "notes" | "chat" | "attached" | "other";
  label: string;
  name: string; // what it is: the file, "the page that is open"
  detail: string; // which part, and why it is grey
  size: string; // "7.3 KB", "not sent", ""
  count: string; // "214 lines", "4 parts"
  state: CtxState;
  hint?: string; // what to do about a grey line
  chars: number; // what it adds to the estimate (0 when it is not sent)
}

const n = (x: number) => x.toLocaleString("en-US");

export function fmtSize(chars: number): string {
  if (chars <= 0) return "";
  if (chars < 1000) return chars + " B";
  return (chars / 1024).toFixed(chars < 10240 ? 1 : 0) + " KB";
}

export function estimateTokens(chars: number): number {
  return Math.round(chars / 4 / 100) * 100;
}

export function contextRows(f: ContextFacts): CtxRow[] {
  const rows: CtxRow[] = [];
  // the editor
  if (!f.editor.on) {
    rows.push({ id: "editor", label: "Editor", name: "not shared", detail: "You turned off sharing the editor with Genie. What you add to a message is all the code it has.", size: "not sent", count: "", state: "off", chars: 0 });
  } else if (f.editor.total === 0) {
    rows.push({ id: "editor", label: "Editor", name: "nothing is open", detail: "Open a file or type some code, and it goes with the next message.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    const cut = f.editor.total > f.editor.chars;
    rows.push({
      id: "editor", label: "Editor", name: f.editor.file || "the editor",
      detail: cut ? "The first " + n(f.editor.chars) + " characters of " + n(f.editor.total) + ". The rest is left out; select the part you mean and add it." : "The whole file that is open.",
      size: fmtSize(f.editor.chars), count: n(f.editor.lines) + (f.editor.lines === 1 ? " line" : " lines"), state: "sent", chars: f.editor.chars,
    });
  }
  // the terminal
  if (f.terminal.chars === 0) {
    rows.push({ id: "terminal", label: "Terminal", name: "nothing in it yet", detail: "Run something and its last lines go with the next message.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    rows.push({ id: "terminal", label: "Terminal", name: "the active terminal", detail: "The most recent lines, at most " + n(f.terminal.limit) + " characters.", size: fmtSize(f.terminal.chars), count: n(f.terminal.lines) + (f.terminal.lines === 1 ? " line" : " lines"), state: "sent", chars: f.terminal.chars });
  }
  // the Preview
  if (!f.preview.on) {
    rows.push({ id: "preview", label: "Preview page", name: "not read", detail: "The chip “the preview” under the box is off.", size: "not sent", count: "", state: "off", hint: "Click the chip to turn it on.", chars: 0 });
  } else if (!f.preview.open) {
    rows.push({ id: "preview", label: "Preview page", name: "the Preview is closed", detail: "Open a page in the Preview and its words go with the next message.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    rows.push({ id: "preview", label: "Preview page", name: "the page that is open", detail: "The address, the headings, the console errors and the failed calls of the page, not a picture. An Agent task can read the whole page.", size: fmtSize(f.preview.chars), count: f.preview.parts + (f.preview.parts === 1 ? " heading" : " headings"), state: "sent", chars: f.preview.chars });
  }
  // the project notes (an Agent task reads them at its start)
  if (!f.notes.known) {
    rows.push({ id: "notes", label: "Project notes", name: "GENIE.md", detail: "Read at the start of an Agent task: the first 80 lines.", size: "", count: "", state: "empty", chars: 0 });
  } else if (!f.notes.exists) {
    rows.push({ id: "notes", label: "Project notes", name: "no GENIE.md yet", detail: "Type remember: and a sentence in the box to start one.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    rows.push({ id: "notes", label: "Project notes", name: "GENIE.md", detail: "The first 80 lines, at the start of an Agent task.", size: fmtSize(f.notes.chars), count: f.notes.lines + (f.notes.lines === 1 ? " note" : " notes"), state: "sent", chars: f.notes.chars });
  }
  // this chat
  if (f.chat.total === 0) {
    rows.push({ id: "chat", label: "This chat", name: "no messages yet", detail: "Only your next message goes.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    rows.push({ id: "chat", label: "This chat", name: "the messages so far", detail: f.chat.sent < f.chat.total ? "The last " + f.chat.sent + " messages. Older ones are left out." : "All the messages of this chat.", size: fmtSize(f.chat.chars), count: f.chat.sent + " of " + f.chat.total, state: "sent", chars: f.chat.chars });
  }
  // what the user attached
  if (f.attached.names.length === 0) {
    rows.push({ id: "attached", label: "Attached by you", name: "nothing", detail: "Add a file, a folder or some lines with +, or from the right-click menu of a file.", size: "", count: "", state: "empty", chars: 0 });
  } else {
    const names = f.attached.names;
    rows.push({ id: "attached", label: "Attached by you", name: names.length + (names.length === 1 ? " item" : " items"), detail: names.slice(0, 4).join(", ") + (names.length > 4 ? " and " + (names.length - 4) + " more" : "") + ". They stay until you remove them with ✕ in the box.", size: fmtSize(f.attached.chars), count: "", state: "yours", chars: f.attached.chars });
  }
  // everything else
  rows.push({
    id: "other", label: "Other files", name: "not sent",
    detail: "Agent reads other files by name when a task needs it. Chat does not." + (f.hidden > 0 ? " " + f.hidden + " hidden " + (f.hidden === 1 ? "file is" : "files are") + " never sent." : " Files whose names start with a dot are never sent."),
    size: "", count: "", state: "off", chars: 0,
  });
  return rows;
}

export function totalChars(rows: CtxRow[]): number {
  return rows.reduce((a, r) => a + r.chars, 0);
}

// ---- the panel ---------------------------------------------------------------------------------------------------------------

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// Draws the list into `host` (a dialog over the chat). `onClose` is called by the Close button.
// With `inCard` the list is drawn inside the Activity panel: its button is a "‹ Back" to the panel instead of a ✕.
export function drawContextView(host: HTMLElement, rows: CtxRow[], modelText: string, onClose: () => void, inCard: boolean = false) {
  host.textContent = "";
  const head = el("div", "cw-ctx__head");
  const title = el("h2", "cw-ctx__title", "What Genie sees now");
  title.id = "chat-widget__ctx-title";
  const close = el("button", "cw-ctx__close");
  close.type = "button";
  close.setAttribute("aria-label", inCard ? "Back to the Activity panel" : "Close the list");
  close.textContent = inCard ? "‹ Back" : "✕";
  close.addEventListener("click", onClose);
  head.append(title, close);
  const lede = el("p", "cw-ctx__lede", "This is what goes with your next message. Nothing else is read unless an Agent task asks for it.");
  const list = el("ul", "cw-ctx__list");
  rows.forEach((r) => {
    const li = el("li", "cw-ctx__row cw-ctx__row--" + r.state);
    li.dataset.row = r.id;
    const dot = el("span", "cw-ctx__dot");
    dot.setAttribute("aria-hidden", "true");
    const body = el("div", "cw-ctx__body");
    const top = el("div", "cw-ctx__top");
    top.append(el("b", "cw-ctx__label", r.label), el("span", "cw-ctx__name", r.name));
    const state = el("span", "cw-ctx__state", r.state === "sent" ? "sent" : r.state === "yours" ? "yours" : r.state === "off" ? "not sent" : "empty");
    body.append(top, el("div", "cw-ctx__detail", r.detail));
    if (r.hint) body.appendChild(el("div", "cw-ctx__hint", r.hint));
    const size = el("div", "cw-ctx__size");
    size.append(el("span", "cw-ctx__bytes", r.size), el("span", "cw-ctx__count", r.count));
    li.append(dot, body, size, state);
    list.appendChild(li);
  });
  const foot = el("div", "cw-ctx__foot");
  const tokens = estimateTokens(totalChars(rows));
  foot.append(el("b", "", "About " + n(tokens) + " tokens"), el("span", "", " go with the next message"), el("span", "cw-ctx__model", modelText ? " · " + modelText : ""));
  const note = el("p", "cw-ctx__note", "Sizes are measured now. Tokens are an estimate of 4 characters each, not the model’s own count. Green is sent, yellow is yours, grey is not sent.");
  host.append(head, lede, list, foot, note);
  return close;
}
