import { createFocusTrap } from "focus-trap";
import { marked } from "marked";

import { widgetHTML } from "./widgetHtmlString";
import { AgentRunner, AgentState } from "./agent";
import { CoachKind, MAX_HINTS, NO_CODE_IN_A_HINT, NO_HINTS_LEFT, askedText, carriesCode, hintsUsed, questionKey, withHint } from "./coach";
import css from "./widget.css";

const WIDGET_BACKDROP_ID = "chat-widget__backdrop";
const WIDGET_CONTAINER_ID = "chat-widget__container";
const WIDGET_MESSAGES_HISTORY_CONTAINER_ID =
  "chat-widget__messages_history";
const WIDGET_THINKING_BUBBLE_ID = "chat-widget__thinking_bubble";
const CHAT_LIST_KEY = "chat-list"

const interviewPrompt = `
You are an expert coding interviewer conducting a technical interview. Your goal is to assess the candidate's ability to solve a coding problem independently. 

### Interview Process:
1. Start by presenting the problem statement and constraints clearly from IDE.
2. Do NOT give the solution or direct hints unless the user explicitly asks for help or is stuck.
3. Encourage the candidate to **think aloud** and explain their approach.
4. If the candidate provides an incorrect approach, ask **clarifying questions** to guide them.
5. Only give small hints when necessary, helping them think in the right direction without revealing the full solution.
6. Use Socratic questioning to probe their understanding:
   - "What data structure might be useful for this problem?"
   - "Can you optimize your current approach?"
   - "What are the edge cases you need to consider?"
7. If the candidate asks for a full solution, politely **decline** and encourage them to try again.
8. If they are truly stuck (e.g., multiple failed attempts), provide a **small hint** to unblock them.
9. Once they reach a correct approach, let them implement it and provide constructive feedback.

### Response Guidelines:
- Be professional and supportive but **not too helpful**.
- Encourage the user to debug their code rather than fixing it for them.
- Give feedback in a way that promotes learning and problem-solving skills.
`

function generateFiveCharUUID(): string {
  // Generate a UUID and extract the first 5 characters
  const uuid: string = crypto.randomUUID();
  return uuid.substring(0, 5);
}

function fetchEditorContent(): string {
    var editor: any = window["editor"];
    if( editor && editor.env && editor.env.editor && 
      editor.env.editor.getValue && (typeof(editor.env.editor.getValue) === "function")) {
        return editor.env.editor.getValue();
    }
    return "";
}

let chatfirebasedbref: firebase.database.Reference | null = null;
const UID = generateFiveCharUUID();
let peerchatmode: boolean = false;

export type WidgetConfig = {
  url: string;
  responseIsAStream: boolean;
  user: Record<any, any>;
  widgetTitle: string;
  greetingMessage: string | null;
  closeOnOutsideClick: boolean;
  openOnLoad: boolean;
  temperature: number;
  max_tokens: number;
  api_key: string;
  firebaseconfig: any;
  dbpath: string;
  submitOnKeydown: boolean;
};


const renderer = new marked.Renderer();
const linkRenderer = renderer.link;
// To open links in a new tab
renderer.link = (href, title, text) => {
  const parsed = linkRenderer.call(renderer, href, title, text);
  return parsed.replace(/^<a /, '<a target="_blank" rel="nofollow" ');
};

const coderenderer = renderer.code;
renderer.code = (code, infostring, escaped) => {
  console.log("code: ", code, infostring, escaped);
  const parsedcode = coderenderer.call(renderer, code, infostring, escaped);
  
  // Insert / Replace file buttons under each code block (see the mockup's Genie panel)
  const encodedcode = btoa(unescape(encodeURIComponent(code)));
  const insertButton = `<button type="button" class="chat-widget__code-btn chat-widget__code-btn--primary" onclick="insertcodesnippet('${encodedcode}')">Insert</button>`
  const replaceButton = `<button type="button" class="chat-widget__code-btn" onclick="replacecodesnippet('${encodedcode}')">Replace file</button>`

  return `<div class="chat-widget__code">${parsedcode}<div class="chat-widget__code-actions">${insertButton}${replaceButton}</div></div>`;
};

marked.setOptions({
  renderer,
  gfm: true,
  breaks: true,
});


const config: WidgetConfig = {
  url: "",
  responseIsAStream: false,
  user: {},
  widgetTitle: "Chatbot",
  greetingMessage: null,
  closeOnOutsideClick: true,
  openOnLoad: false,
  temperature: 0.5,
  max_tokens: 800,
  api_key: "",
  firebaseconfig: {},
  dbpath: "xyz",
  submitOnKeydown: false,
  ...(window as any).ChatWidget?.config,
};

// Define the MessageType interface
interface MessageType {
  role: string;
  content: string;
}

// The models and four effort levels, and the saved choice, come from
// js/model-choice.js, which the page loads before this widget; the New question
// dialog shares them (js/common.js). The server holds the same lists
// (server/chatmodels.go) and sends on nothing else.
type ModelOption = {
  id: string;
  name: string; // in the picker and under a reply
  short: string; // on the chip
  group: string; // who runs it: "OpenAI" or "OpenRouter"; the picker's sections
  tag: string; // may be empty
  
  desc: string;
  reasoning: boolean; // takes an effort level; 4o mini and Gemma do not
};
type EffortOption = { id: string; label: string; tokens: number; note: string };
type FieldOptions = { extraTokens?: number; maxTokens?: number; temperature?: number };
type SharedChoice = {
  models: ModelOption[];
  efforts: EffortOption[];
  get(): { model: string; effort: string };
  set(model?: string, effort?: string): void;
  fields(opts?: FieldOptions): Record<string, any>;
  fallback(exceptId: string): ModelOption | null;
  onChange(fn: () => void): void;
};
const MC: SharedChoice = (window as any).ModelChoice;
if (!MC) {
  console.error("Chat widget: js/model-choice.js has to load before the widget.");
}
const MODELS = MC.models;
const EFFORTS = MC.efforts;

function chosenModel(): ModelOption {
  const id = MC.get().model;
  return MODELS.filter((m) => m.id === id)[0] || MODELS[0];
}
function chosenEffort(): EffortOption {
  const id = MC.get().effort;
  return EFFORTS.filter((e) => e.id === id)[0] || EFFORTS[1];
}
function chipText(): string {
  const m = chosenModel();
  return m.reasoning ? `${m.short} · ${chosenEffort().label}` : m.short;
}
// "GPT-6 Luna · Low" or "Gemma 4 31B · OpenRouter", under a reply
function captionText(): string {
  const m = chosenModel();
  if (m.reasoning) return `${m.name} · ${chosenEffort().label}`;
  return m.group === "OpenAI" ? m.name : `${m.name} · ${m.group}`;
}

// The request fields for the chosen model: Luna takes an effort and an answer
// budget, 4o mini and Gemma a temperature and max_tokens (model-choice.js).
function modelFields(): Record<string, any> {
  return MC.fields({ temperature: config.temperature, maxTokens: config.max_tokens });
}

// What Genie is told about the page, and how many messages it keeps. An admin
// can change the numbers (settings.js: site_settings.genieContext); these are
// the built-in ones.
// The messages at the start of the history that are never trimmed: what Genie
// is told about itself, and the last of them is the editor snapshot that every
// user message refreshes. Set in init() once they are in; it was a fixed 4
// while two of them were the page text and the documentation list.
let NUM_MANDATORY_ENTRIES = 4;
function genieLimit(name: "editorChars" | "terminalChars" | "terminalLines" | "history", fallback: number): number {
  const n = Number((((window as any).site_settings || {}).genieContext || {})[name]);
  return n > 0 ? n : fallback;
}
const maxHistorySize = () => genieLimit("history", 20);
// older conversation history might not be usefull
// Initialize the conversationHistory array
let conversationHistory: MessageType[] = [];

// What Genie is told about the page on every request: the editor's code and the
// most recent output of the active terminal, so "why did this fail?" needs no
// pasting. Both are cut to a size that leaves room for the conversation.
const maxEditorChars = () => genieLimit("editorChars", 12000);
const maxTerminalChars = () => genieLimit("terminalChars", 4000);
const terminalLines = () => genieLimit("terminalLines", 20);

// The terminal of the active tab (js/src/gotty.ts keeps it on the tab), or null.
// Every Run and every change of language replaces it with a new one.
function activeTerminal(): any {
  try {
    const tab = document.querySelector("#terminal-tabs .tab.active") as any;
    return (tab && tab.gottyterm && tab.gottyterm.term) || null;
  } catch (e) {
    return null;
  }
}

function fetchTerminalOutput(lines: number = terminalLines()): string {
  try {
    // the xterm adapter keeps its buffer; older builds only have the rows in the DOM
    const term = activeTerminal();
    if (term && typeof term.recentText === "function") {
      return String(term.recentText(lines));
    }
    const rows =
      document.querySelector(".terminal.active .xterm-rows") || document.querySelector(".xterm-rows");
    return rows ? (rows as HTMLElement).innerText : "";
  } catch (e) {
    return "";
  }
}

function getcurrentIDECode(): MessageType {
  const picker = document.getElementById("optionlist") as HTMLSelectElement | null;
  const language = picker && picker.selectedIndex >= 0 ? picker.options[picker.selectedIndex].text : "";
  const fileChip = document.getElementById("editor-filename");
  const file = fileChip ? (fileChip.textContent || "").trim() : "";

  let code = fetchEditorContent();
  if (code.length > maxEditorChars()) {
    code = code.slice(0, maxEditorChars()) + "\n... (the editor holds more; the rest is not shown)";
  }
  let output = fetchTerminalOutput().replace(/\s+$/, "");
  if (output.length > maxTerminalChars()) {
    output = "... " + output.slice(-maxTerminalChars());
  }

  const parts = [
    "Openrepl IDE real-time context of what the user is working on. Use it whenever the user asks to debug, explain or fix their code, or an error, without pasting anything.",
    language ? `Language: ${language}` : "",
    file && file !== "untitled" ? `File: ${file}` : "",
    "--- Editor code ---",
    code || "(the editor is empty)",
    "--- Terminal output (the most recent lines of the active terminal) ---",
    output || "(nothing in the terminal yet)",
  ].filter(Boolean);
  return { role: "system", content: parts.join("\n") };
}

// Function to add a message to the conversation history
function addMessageToHistory(role: string, content: string, uid: string=UID): void {
  if (role=="user") {
    // to handle multiple peer users
    content = `[${role}-${uid}] ` + content;
    if (conversationHistory.length >= NUM_MANDATORY_ENTRIES) {
      // update editors content to msg history everytime user writes/sends message
      conversationHistory[NUM_MANDATORY_ENTRIES-1] = getcurrentIDECode();
    }
  }

  conversationHistory.push({ role: role, content: content });
  if (conversationHistory.length > maxHistorySize()) {
      // Trim the oldest non-mandatory message from the beginning, preserving mandatory entries of docs
      conversationHistory.splice(NUM_MANDATORY_ENTRIES, 1);
  }
}

function isMaster() : boolean {
    var hash = window.location.hash.replace(/#/g, '');
    if (!hash) {
        return true;
    } else {
        return false;
    }
}

let cleanup = () => {
  if (isMaster() && chatfirebasedbref) {
    chatfirebasedbref.remove();
  }
  console.log("cleanup done.");
};

let peerchatSwitchlistener = (e: Event) => {
  const peerchatSwitchElem = (e?.target as HTMLInputElement) || null;
  if (peerchatSwitchElem) {
    if (peerchatSwitchElem.checked) {
      console.log('PeerChat switch is ON');
      peerchatmode=true;
    } else {
      console.log('PeerChat switch is OFF');
      peerchatmode=false;
    }
    refreshChip();
    if (chatfirebasedbref) {
      // push event to firebase db
      chatfirebasedbref.push ({
           eventT: "peerchatmode",
           val: peerchatmode,
           uid: UID,
      });
    }
  } else {
    console.error("peerchatSwitchlistener: an unexpected error occurred: null element.");
  }
};

const setupFBListener = () => {
  try {
    // window firebase should have been loaded already
    if (window.firebase.apps.length === 0) {
       firebase.initializeApp(config.firebaseconfig);
    }
    chatfirebasedbref = window.firebase.database().ref(CHAT_LIST_KEY).child(config.dbpath);
    
    // firebase callback
    chatfirebasedbref.on("child_added", (data: firebase.database.DataSnapshot | null, prevChildKey?: string | undefined) => {
      if (!data) {
        console.log("No data received from Firebase.");
        return;
      }
      let d = data.val();
      if (d.uid==UID) {
        console.log("chat event triggered by me only, skipping..");
        return;
      }
      if (d.eventT && d.eventT=="peerchatmode") {
        console.log("received an peerchatmode event: ", d);
        // its a event message
        peerchatmode=d.val;
        refreshChip();
        const peerchatSwitchElem = document.getElementById("peerchat-switch") as HTMLInputElement;
        if (peerchatSwitchElem) {
          peerchatSwitchElem.checked=peerchatmode;
        }
      } else {
        createNewMessageEntry(d.message, d.timestamp, d.from, true);
        addMessageToHistory(d.from, d.message, d.uid); // remote uid needed here as its not my chat
      }
    });
  } catch(error) {
    console.error("Error setup firebase handle: ", error);
  }
};

async function init() {
  const styleElement = document.createElement("style");
  styleElement.innerHTML = css;

  document.head.insertBefore(styleElement, document.head.firstChild);

  // Slight delay to allow DOMContent to be fully loaded
  // (particularly for the button to be available in the `if (config.openOnLoad)` block below).
  await new Promise((resolve) => setTimeout(resolve, 500));

  document
    .querySelector("[data-chat-widget-button]")
    ?.addEventListener("click", open);

  if (config.openOnLoad) {
    const target = document.querySelector(
      "[data-chat-widget-button]"
    );
    open({ target } as Event);
  }

  let welcomeprompt = "welcome to openrepl.com!! you are Genie. An OpenRepl AI";
  // the permanent prompts: who Genie is, (practice: the interviewer prompt), and the editor snapshot
  if (window.location.pathname.includes("practice")) {
    addMessageToHistory("system", welcomeprompt+" Interviewer.");
    addMessageToHistory("system", interviewPrompt);
  } else {
    addMessageToHistory("system", welcomeprompt+" Assistant.");
  }
  addMessageToHistory("system", "Openrepl IDE/EditorCodeContent: "+ fetchEditorContent());
  NUM_MANDATORY_ENTRIES = conversationHistory.length; // the editor snapshot is the last of them
  setupFBListener();
}
window.addEventListener("load", init);
window.addEventListener("unload", cleanup);

const containerElement = document.createElement("div");
containerElement.id = WIDGET_CONTAINER_ID;

const messagesHistory = document.createElement("div");
messagesHistory.id = WIDGET_MESSAGES_HISTORY_CONTAINER_ID;

const optionalBackdrop = document.createElement("div");
optionalBackdrop.id = WIDGET_BACKDROP_ID;

const thinkingBubble = document.createElement("div");
thinkingBubble.id = WIDGET_THINKING_BUBBLE_ID;
thinkingBubble.innerHTML = `
    <span class="circle"></span>
    <span class="circle"></span>
    <span class="circle"></span>
    <span class="chat-widget__thinking-label"></span>
  `;

const trap = createFocusTrap(containerElement, {
  initialFocus: "#chat-widget__input",
  allowOutsideClick: true,
});

// Drag handle in the top-left corner. The panel is docked to the bottom-right
// corner (widget.css), so dragging up and left makes it bigger.
function makeResizable(containerElement: HTMLElement) {
  const resizer = document.createElement("div");
  resizer.className = "chat-widget__resizer";
  resizer.setAttribute("aria-hidden", "true");
  resizer.innerHTML = `
    <svg width="14" height="14" viewBox="0 0 20 20">
      <line x1="4" y1="16" x2="16" y2="4" stroke="currentColor" stroke-width="2" />
      <line x1="8" y1="16" x2="16" y2="8" stroke="currentColor" stroke-width="2" />
    </svg>
  `;
  containerElement.appendChild(resizer);

  resizer.addEventListener("mousedown", (e) => {
    e.preventDefault();
    const startX = e.clientX;
    const startY = e.clientY;
    const startWidth = containerElement.offsetWidth;
    const startHeight = containerElement.offsetHeight;

    function resize(e: MouseEvent) {
      const newWidth = Math.min(window.innerWidth - 32, Math.max(320, startWidth + (startX - e.clientX)));
      const newHeight = Math.min(window.innerHeight - 48, Math.max(320, startHeight + (startY - e.clientY)));
      containerElement.style.width = `${newWidth}px`;
      containerElement.style.height = `${newHeight}px`;
    }

    function stopResize() {
      document.removeEventListener("mousemove", resize);
      document.removeEventListener("mouseup", stopResize);
    }

    document.addEventListener("mousemove", resize);
    document.addEventListener("mouseup", stopResize);
  });
}

// "Reads main.py": the file Genie sees, from the editor header chip.
function editorContextLabel(): string {
  const chip = document.getElementById("editor-filename");
  const name = chip ? (chip.textContent || "").trim() : "";
  if (!document.getElementById("editor")) return "";
  return name && name !== "untitled" ? `Reads ${name} + terminal` : "Reads editor + terminal";
}

function autoGrow(input: HTMLTextAreaElement) {
  input.style.height = "auto";
  input.style.height = `${Math.min(input.scrollHeight + 2, 140)}px`;
}

// ---- Model and effort ---------------------------------------------------

function settingsEl(): HTMLElement | null {
  return document.getElementById("chat-widget__settings");
}
function chipEl(): HTMLButtonElement | null {
  return document.getElementById("chat-widget__model-btn") as HTMLButtonElement | null;
}
function settingsOpen(): boolean {
  const el = settingsEl();
  return !!el && !el.hidden;
}
function setSettingsOpen(open: boolean, focusChip: boolean = false) {
  const el = settingsEl();
  const chip = chipEl();
  if (!el || !chip) return;
  el.hidden = !open;
  chip.setAttribute("aria-expanded", open ? "true" : "false");
  if (!open && focusChip) chip.focus();
}

// Peer chat messages go to the people in the session, not to a model, so the
// chip is not shown while it is on.
function refreshChip() {
  refreshMode(); // peer chat and agent mode do not go together
  refreshCoach();
  renderUsage(); // peer chat messages use nothing: the line is hidden
  const chip = chipEl();
  if (!chip) return;
  chip.hidden = peerchatmode;
  if (peerchatmode) setSettingsOpen(false);
}

// ---- The practice coach ----------------------------------------------------
// On the practice page, three buttons above the composer: a hint (three levels,
// a little more each time, never code), a review of the solution and its
// complexity. The server writes the instructions (server/coach.go); the page
// counts the hints, in this browser, per question (coach.ts).

const HINTS_KEY = "practiceHints";
let coachBusy = false;

function readHints(): unknown {
  try {
    return JSON.parse(localStorage.getItem(HINTS_KEY) || "null");
  } catch (e) {
    return null;
  }
}

function coachQuestion(): string {
  return questionKey(window.location.search, window.location.pathname);
}

function refreshCoach() {
  const bar = document.getElementById("chat-widget__coach");
  if (!bar) return;
  bar.hidden = !(onPracticePage() && !peerchatmode);
  const count = document.getElementById("chat-widget__coach-count");
  if (count) count.textContent = "Coach · hints used: " + hintsUsed(readHints(), coachQuestion()) + " of " + MAX_HINTS;
  bar.querySelectorAll("button").forEach((b) => ((b as HTMLButtonElement).disabled = coachBusy));
}

async function coachAsk(kind: CoachKind) {
  if (genieBusy() || agentRunner.running || !config.url) return;
  const used = hintsUsed(readHints(), coachQuestion());
  if (kind === "hint" && used >= MAX_HINTS) {
    await createNewMessageEntry(NO_HINTS_LEFT, Date.now(), "system", false, "Coach");
    return;
  }
  const level = used + 1;
  const asked = askedText(kind, level);
  coachBusy = true;
  refreshCoach();
  const sendBtn = document.getElementById("chat-widget__submit");
  if (sendBtn) sendBtn.setAttribute("disabled", "");
  addMessageToHistory("user", asked);
  await createNewMessageEntry(asked, Date.now(), "user");
  const label = thinkingBubble.querySelector(".chat-widget__thinking-label");
  if (label) label.textContent = chosenModel().reasoning && chosenEffort().id !== "none" ? `${chosenModel().short} is thinking…` : "";
  messagesHistory.prepend(thinkingBubble);
  const picker = document.getElementById("optionlist") as HTMLSelectElement | null;
  try {
    const res = await fetch(config.url, {
      method: "POST",
      headers: requestHeaders(),
      body: JSON.stringify({
        ...MC.fields({ temperature: 0.3, maxTokens: 700, extraTokens: 1500 }),
        context: "coach",
        kind,
        level: kind === "hint" ? level : undefined,
        language: picker && picker.selectedIndex >= 0 ? picker.options[picker.selectedIndex].text : "",
        file: fetchEditorContent(),
        output: fetchTerminalOutput(40),
      }),
    });
    thinkingBubble.remove();
    if (!res.ok) {
      await handleErrorResponse(await res.json().catch(() => ({})));
      return;
    }
    const data: any = await res.json();
    const text: string = (data && data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content) || "";
    if (!text.trim()) {
      await createNewMessageEntry("The coach sent no answer. Try again.", Date.now(), "system", false, "Coach");
      return;
    }
    if (kind === "hint" && carriesCode(text)) {
      // not counted: the user did not get a hint
      await createNewMessageEntry(NO_CODE_IN_A_HINT, Date.now(), "system", false, "Coach");
      return;
    }
    if (kind === "hint") {
      try {
        localStorage.setItem(HINTS_KEY, JSON.stringify(withHint(readHints(), coachQuestion())));
      } catch (e) {
        // the count is not kept
      }
    }
    addMessageToHistory("assistant", text);
    await createNewMessageEntry(text, Date.now(), "system", false, "Coach · " + asked + " · " + captionText());
  } catch (e) {
    thinkingBubble.remove();
    console.error("Chat Widget: coach:", e);
    await createNewMessageEntry("Unable to reach the coach now. Try again.", Date.now(), "system", false, "Coach");
  } finally {
    coachBusy = false;
    if (sendBtn) sendBtn.removeAttribute("disabled");
    refreshCoach();
  }
}

function wireCoach() {
  const bar = document.getElementById("chat-widget__coach");
  if (!bar) return;
  bar.querySelectorAll("button").forEach((b) =>
    b.addEventListener("click", () => {
      void coachAsk((b.getAttribute("data-coach") || "hint") as CoachKind);
    })
  );
  refreshCoach();
}

// ---- Agent mode ----------------------------------------------------------
// Genie works on a task in steps and carries them out in the editor, after the
// user allowed each kind of action (agent.ts). Only the owner of a session has
// it, not in peer chat, only for signed-in users (the server refuses a guest),
// and not where an admin switched it off (settings.js: agentEnabled,
// agentOnPractice). Chat is the mode a visitor starts in; the mode chosen last
// is kept for the next time the panel opens.

let agentMode = false;

const MODE_KEY = "genie-mode";

function rememberedMode(): "chat" | "agent" {
  try {
    return localStorage.getItem(MODE_KEY) === "agent" ? "agent" : "chat";
  } catch (e) {
    return "chat"; // storage can be blocked; the panel then starts in chat
  }
}

function rememberMode(mode: "chat" | "agent") {
  try {
    localStorage.setItem(MODE_KEY, mode);
  } catch (e) {
    // not remembered
  }
}

function agentAvailability(): "off" | "signin" | "ready" {
  const s = ((window as any).site_settings || {}) as any;
  if (!s.agentEnabled) return "off";
  if (onPracticePage() && !s.agentOnPractice) return "off";
  if (!isMaster() || peerchatmode) return "off";
  if (!document.body.classList.contains("is-signed-in")) return "signin";
  return "ready";
}

function requestHeaders(): Headers {
  const h = new Headers();
  h.append("Content-Type", "application/json");
  if (config.api_key) h.append("Authorization", "Bearer " + config.api_key);
  return h;
}

let lastAgentState: AgentState = "idle";
const agentRunner = new AgentRunner(
  {
    url: () => config.url,
    headers: requestHeaders,
    // a whole file may be in an answer: more room than a chat answer needs
    modelFields: () => MC.fields({ temperature: 0.2, maxTokens: 3000, extraTokens: 2500 }),
    ideContext: () => getcurrentIDECode(),
    terminalText: () => fetchTerminalOutput(120),
    terminal: () => activeTerminal(),
    terminalType: (data: string) => {
      const term = activeTerminal();
      if (!term || typeof term.typeInput !== "function") return false;
      try {
        return term.typeInput(data) === true;
      } catch (e) {
        return false;
      }
    },
    tabs: () => {
      const all = Array.from(document.querySelectorAll("#terminal-tabs .tab"));
      const at = all.findIndex((t) => t.classList.contains("active"));
      return { count: all.length, active: at + 1 };
    },
    terminalReady: () => {
      const term = activeTerminal();
      return !!term && typeof term.hasInput === "function" && term.hasInput();
    },
    reconnect: () => {
      const fn = (window as any).ToggleReconnect;
      if (typeof fn !== "function") return false;
      fn();
      return true;
    },
    addTab: () => {
      const g = (window as any).gotty;
      if (!g || typeof g.addTab !== "function") return false;
      const before = document.querySelectorAll("#terminal-tabs .tab").length;
      g.addTab();
      return document.querySelectorAll("#terminal-tabs .tab").length > before;
    },
    files: {
      // the page's Files panel (js/src/page/07-file-browser.js); without it nothing is ready
      ready: () => !!(window as any).FileBrowser && (window as any).FileBrowser.ready(),
      reveal: () => (window as any).FileBrowser.reveal(),
      info: (path: string) => (window as any).FileBrowser.info(path),
      current: () => (window as any).FileBrowser.current(),
      buffer: () => (window as any).FileBrowser.buffer(),
      list: (path: string) => (window as any).FileBrowser.list(path),
      open: (path: string) => (window as any).FileBrowser.open(path),
      create: (path: string, kind: "file" | "folder") => (window as any).FileBrowser.create(path, kind),
      save: () => (window as any).FileBrowser.save(),
      rename: (path: string, name: string) => (window as any).FileBrowser.rename(path, name),
      cut: (path: string) => (window as any).FileBrowser.cut(path),
      copy: (path: string) => (window as any).FileBrowser.copy(path),
      paste: (to: string) => (window as any).FileBrowser.paste(to),
      move: (path: string, to: string) => (window as any).FileBrowser.move(path, to),
      remove: (path: string) => (window as any).FileBrowser.remove(path),
    },
    closeTab: (n: number) => {
      const tab = document.querySelectorAll("#terminal-tabs .tab")[n - 1];
      const x = tab && (tab.querySelector(".close-tab") as HTMLElement | null);
      if (!x) return false;
      x.click(); // the page's own handler (gotty.closeTab) closes it
      return true;
    },
    selectTab: (n: number) => {
      const tab = document.querySelectorAll("#terminal-tabs .tab")[n - 1] as HTMLElement | undefined;
      if (!tab) return false;
      tab.click();
      return true;
    },
    thinking: (on: boolean) => {
      if (!on) {
        thinkingBubble.remove();
        return;
      }
      // the dots of the chat, while Genie works on a step
      const label = thinkingBubble.querySelector(".chat-widget__thinking-label");
      if (label) label.textContent = chosenModel().reasoning && chosenEffort().id !== "none" ? `${chosenModel().short} is thinking…` : "";
      messagesHistory.prepend(thinkingBubble);
    },
    userSaid: async (text: string) => {
      addMessageToHistory("user", text);
      await createNewMessageEntry(text, Date.now(), "user");
    },
    genieSaid: async (text: string, context?: string | null) => {
      addMessageToHistory("assistant", text);
      // the notes of the site that the step was answered from, as in the chat; a
      // step that used none says nothing (most steps of a task are about code)
      const used = parseContextHeader(context || null);
      await createNewMessageEntry(text, Date.now(), "system", false, "Agent · " + captionText(), used && used.length ? used : null);
    },
    note: async (text: string) => {
      // shown, not remembered: the model must not read an error as its own words
      await createNewMessageEntry(text, Date.now(), "system", false, "Agent");
    },
    messages: () => messagesHistory,
    maxSteps: () => Number((((window as any).site_settings || {}) as any).agentMaxSteps) || 8,
    uid: () => UID,
    repl: () => {
      // the same name the page asks /demo for (01-session.js: getSelectValue)
      const get = (window as any).getSelectValue;
      const picker = document.getElementById("optionlist") as HTMLSelectElement | null;
      return String((typeof get === "function" ? get() : picker && picker.value) || "");
    },
    usage: (raw: string | null) => {
      if (raw) takeUsage(raw);
      else fetchUsage();
    },
    attention: (on: boolean) => {
      needsYou = Math.max(0, needsYou + (on ? 1 : -1));
      renderActivity();
    },
    progress: (n: number, max: number) => {
      stepNow = n;
      stepMax = max;
      renderActivity();
    },
    ended: (outcome: "done" | "failed" | "stopped") => {
      stepNow = stepMax = needsYou = 0;
      // somebody has to be told only when the panel is not there to show it
      if (outcome !== "stopped" && !isOpen()) {
        unread = outcome === "done" ? { kind: "done", label: "Task done" } : { kind: "failed", label: "Task stopped" };
      }
      renderActivity();
    },
  },
  (state: AgentState) => {
    lastAgentState = state;
    showAgentState(state);
    renderActivity();
  }
);

// ---- Genie's button while the panel is closed ----------------------------------
// Closing the panel hides it and stops nothing: a task or an answer goes on, and
// the Ask Genie buttons (the floating one, the app bar's on phones) say what
// Genie is doing, and the tab title says when it is done or needs the user. What
// they show is worked out from these facts each time, so it cannot go stale.

let chatInFlight = false; // a chat message has been sent and not answered
let chatFailed = false; // the last chat message got an error, not an answer
let needsYou = 0; // questions and changes waiting for the user
let stepNow = 0;
let stepMax = 0;
// a result that came while the panel was closed, until the panel is opened
let unread: { kind: "done" | "failed"; label: string } | null = null;
let titleShown: { base: string; shown: string } | null = null;

type Activity = { state: "idle" | "working" | "needs" | "done" | "failed"; label: string };

function genieActivity(): Activity {
  if (needsYou > 0) return { state: "needs", label: "Genie needs you" };
  if (agentRunner.running) return { state: "working", label: stepMax > 0 ? `Genie is working… ${stepNow}/${stepMax}` : "Genie is working…" };
  if (chatInFlight) return { state: "working", label: "Genie is thinking…" };
  if (unread) return { state: unread.kind, label: unread.label };
  return { state: "idle", label: "Ask Genie" };
}

function setTabTitle(text: string | null) {
  // the page may have changed the title meanwhile (a language page does): then
  // what it set is the title to go back to
  if (titleShown && document.title === titleShown.shown) document.title = titleShown.base;
  titleShown = null;
  if (text) {
    const base = document.title;
    const shown = `${text} · ${base}`;
    document.title = shown;
    titleShown = { base, shown };
  }
}

function renderActivity() {
  const a = genieActivity();
  const fab = document.querySelector("[data-chat-widget-button]") as HTMLElement | null;
  const bar = document.getElementById("genie-button");
  [fab, bar].forEach((b) => {
    if (!b) return;
    if (a.state === "idle") b.removeAttribute("data-genie-state");
    else b.setAttribute("data-genie-state", a.state);
    b.setAttribute("aria-label", a.state === "idle" ? "Ask Genie, the AI helper" : a.label);
  });
  const text = fab && fab.querySelector("span");
  if (text) text.textContent = a.label;
  // the tab and the screen reader hear it only when the panel is not open to show it
  const away = !isOpen() && a.state !== "idle";
  setTabTitle(!away ? null : a.state === "working" ? a.label.replace(/ \d+\/\d+$/, "") : "(1) " + a.label);
  let live = document.getElementById("genie-status");
  if (!live) {
    live = document.createElement("div");
    live.id = "genie-status";
    live.setAttribute("role", "status");
    live.setAttribute("aria-live", "polite");
    live.style.cssText = "position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap";
    document.body.appendChild(live);
  }
  const say = away && a.state !== "working" ? a.label : "";
  if (live.textContent !== say) live.textContent = say;
}

// ---- what is left of Genie ------------------------------------------------------
// A line under the composer: the requests left (in Agent mode, the tasks left
// this hour), with the details behind a click. The numbers come from the server
// (GET <chat>/usage when the panel opens, and the X-OpenREPL-Usage header of
// every answer); between answers the recharge is counted here.

type Usage = {
  left: number;
  cap: number;
  perMinute: number;
  unlimited?: boolean;
  signedIn?: boolean;
  agent?: { used: number; perHour: number; nextFreeSeconds: number; maxSteps: number };
};
let usage: Usage | null = null;
let usageAt = 0; // when it was told
let usageTimer: any = null;
let usageDetails = false;

function takeUsage(raw: string | null) {
  if (!raw) return;
  try {
    const u = JSON.parse(raw);
    if (!u || typeof u.left !== "number" || typeof u.cap !== "number" || typeof u.perMinute !== "number") return;
    usage = u as Usage;
    usageAt = Date.now();
    renderUsage();
  } catch (e) {
    // not shown
  }
}

async function fetchUsage() {
  if (!config.url) return;
  try {
    const res = await fetch(config.url.replace(/completions\/?$/, "usage"), { credentials: "same-origin" });
    if (res.ok) takeUsage(await res.text());
  } catch (e) {
    // the line stays as it was
  }
}

// The usage as it is now: the balance with the recharge since it was told.
function usageNow() {
  const u = usage!;
  const minutes = (Date.now() - usageAt) / 60000;
  const left = Math.min(u.cap, u.left + minutes * u.perMinute);
  // the next request is taken while the balance is above 0
  const wait = left > 0 || u.perMinute <= 0 ? 0 : Math.ceil((-left / u.perMinute) * 60) + 1;
  let tasksLeft = -1; // no limit, or no agent mode
  let taskWait = 0;
  if (u.agent && u.agent.perHour > 0) {
    const freed = u.agent.used > 0 && minutes * 60 >= u.agent.nextFreeSeconds ? 1 : 0;
    tasksLeft = Math.max(0, u.agent.perHour - u.agent.used + freed);
    taskWait = Math.max(0, Math.ceil(u.agent.nextFreeSeconds - minutes * 60));
  }
  return { left, count: Math.max(0, Math.ceil(left)), cap: Math.round(u.cap), wait, tasksLeft, taskWait };
}

function span(seconds: number): string {
  if (seconds < 90) return Math.max(1, seconds) + " s";
  return Math.ceil(seconds / 60) + " min";
}

// Nothing can be sent now: a request would be refused.
function noneLeft(): boolean {
  if (!usage || usage.unlimited || peerchatmode) return false;
  const n = usageNow();
  return n.left <= 0 || (agentMode && n.tasksLeft === 0);
}

function renderUsage() {
  const box = document.getElementById("chat-widget__usage");
  const text = document.getElementById("chat-widget__usage-text");
  const fill = document.getElementById("chat-widget__usage-fill");
  const more = document.getElementById("chat-widget__usage-more");
  const line = document.getElementById("chat-widget__usage-line");
  if (!box || !text || !fill || !more || !line) return; // the panel is closed
  box.hidden = !usage || peerchatmode;
  if (!usage || peerchatmode) return;
  const n = usageNow();
  const u = usage;
  const agentLine = agentMode && !!u.agent && n.left > 0;
  const rate = u.perMinute >= 1 ? `${Math.round(u.perMinute)} a minute` : u.perMinute > 0 ? `1 every ${Math.round(1 / u.perMinute)} minutes` : "none";
  let label = "";
  let part = 1;
  let state = "ok";
  if (u.unlimited) {
    label = "No limit (admin)";
    state = "free";
  } else if (n.left <= 0) {
    label = `No requests left · next in ${span(n.wait)}`;
    part = 0;
    state = "empty";
  } else if (agentLine && n.tasksLeft >= 0) {
    part = n.tasksLeft / u.agent!.perHour;
    if (n.tasksLeft === 0) {
      label = `No tasks left this hour · next in ${span(n.taskWait)}`;
      state = "empty";
    } else {
      label = `${n.tasksLeft} of ${u.agent!.perHour} tasks left this hour`;
      state = n.tasksLeft <= 2 ? "low" : "ok";
    }
  } else {
    label = `${n.count} of ${n.cap} requests left`;
    part = n.cap > 0 ? n.count / n.cap : 0;
    state = n.count <= Math.max(2, Math.round(n.cap * 0.1)) ? "low" : "ok";
  }
  if (text.textContent !== label) text.textContent = label;
  fill.style.width = Math.round(Math.min(1, Math.max(0, part)) * 100) + "%";
  ["ok", "low", "empty", "free"].forEach((s) => box.classList.toggle("cw-usage--" + s, s === state));

  // the details
  const rows: [string, string][] = [];
  if (u.unlimited) {
    rows.push(["Requests", "Not limited for admins"]);
    if (u.agent) rows.push(["Agent tasks", "Not limited for admins"], ["Steps in a task", "up to " + u.agent.maxSteps]);
  } else {
    rows.push(["Requests left", `${n.count} of ${n.cap}`]);
    if (n.left <= 0) rows.push(["Next request in", span(n.wait)]);
    rows.push(["Refill", rate]);
    if (n.left > 0 && n.left < u.cap && u.perMinute > 0) rows.push(["Full again in", span(Math.ceil(((u.cap - n.left) / u.perMinute) * 60))]);
    if (u.agent && n.tasksLeft >= 0) {
      rows.push(["Agent tasks this hour", `${u.agent.perHour - n.tasksLeft} of ${u.agent.perHour} used`]);
      if (n.tasksLeft === 0) rows.push(["Next task in", span(n.taskWait)]);
      rows.push(["Steps in a task", "up to " + u.agent.maxSteps + " (a task uses 1 request)"]);
    }
  }
  const sig = JSON.stringify(rows) + (u.signedIn ? "" : "guest");
  if (more.getAttribute("data-sig") !== sig) {
    more.setAttribute("data-sig", sig);
    more.textContent = "";
    rows.forEach(([k, v]) => {
      const row = document.createElement("div");
      const dt = document.createElement("dt");
      dt.textContent = k;
      const dd = document.createElement("dd");
      dd.textContent = v;
      row.append(dt, dd);
      more.appendChild(row);
    });
    if (!u.signedIn && !u.unlimited) {
      const p = document.createElement("p");
      p.textContent = "Sign in for more requests and Agent mode.";
      more.appendChild(p);
    }
  }
  more.hidden = !usageDetails;
  line.setAttribute("aria-expanded", String(usageDetails));

  // with nothing left the send button looks it, and the box says when to come back
  const empty = noneLeft();
  const submitBtn = document.getElementById("chat-widget__submit");
  if (submitBtn) submitBtn.classList.toggle("is-empty", empty);
  const input = document.getElementById("chat-widget__input") as HTMLTextAreaElement | null;
  if (input && !agentRunner.running) {
    input.placeholder = empty ? `You can send again in ${span(n.left <= 0 ? n.wait : n.taskWait)}` : modePlaceholder();
  }
}

// Called by submit when nothing is left: the line says why nothing was sent.
function flashUsage() {
  const box = document.getElementById("chat-widget__usage");
  if (!box) return;
  box.classList.remove("cw-usage--flash");
  void box.offsetWidth; // so that the animation runs again
  box.classList.add("cw-usage--flash");
}

// While a task runs, the send button is a stop button (the two icons are both
// in widget.html; .is-stop shows the square). While the task winds down after
// Stop it waits, and then it is the send button again.
function showAgentState(state: AgentState) {
  const submitBtn = document.getElementById("chat-widget__submit");
  if (submitBtn) {
    submitBtn.classList.toggle("is-stop", state !== "idle");
    submitBtn.setAttribute("aria-label", state === "idle" ? "Send" : "Stop the task");
    if (state === "idle") submitBtn.removeAttribute("title");
    else submitBtn.setAttribute("title", "Stop the task");
    if (state === "stopping") submitBtn.setAttribute("disabled", "");
    else submitBtn.removeAttribute("disabled");
  }
  const input = document.getElementById("chat-widget__input") as HTMLTextAreaElement | null;
  if (input) {
    input.placeholder = state === "running" ? "Genie is working on your task…" : state === "stopping" ? "Stopping…" : modePlaceholder();
  }
}

function modePlaceholder(): string {
  return agentMode ? "Describe a task for Genie to carry out" : "Ask about your code";
}

function setMode(agent: boolean) {
  agentMode = agent;
  const box = document.getElementById("chat-widget__mode");
  if (box) {
    box.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String((b.getAttribute("data-mode") === "agent") === agent)));
  }
  const input = document.getElementById("chat-widget__input") as HTMLTextAreaElement | null;
  if (input && !agentRunner.running) input.placeholder = modePlaceholder();
  renderUsage(); // the line counts tasks in Agent mode
}

function refreshMode() {
  const box = document.getElementById("chat-widget__mode");
  if (!box) return;
  const a = agentAvailability();
  box.hidden = a === "off";
  const agentBtn = box.querySelector('button[data-mode="agent"]') as HTMLButtonElement | null;
  if (agentBtn) {
    // For a guest it is not disabled (a disabled button swallows the click and
    // says nothing): it looks locked, and a click says why, with a way to sign in.
    agentBtn.disabled = false;
    agentBtn.classList.toggle("is-locked", a === "signin");
    agentBtn.setAttribute("aria-disabled", a === "signin" ? "true" : "false");
    agentBtn.title = a === "signin" ? "Sign in to access agent mode" : "Genie carries out a task in your editor, step by step, with your permission";
  }
  if (a !== "ready") {
    // not available now (peer chat, signed out): chat, but the choice is kept
    if (agentMode) setMode(false);
  } else if (!agentMode && !agentRunner.running && rememberedMode() === "agent") {
    setMode(true);
  }
}

// A guest pressed Agent: a notice says that agent mode needs an account, with a
// button that opens the page's sign-in dialog.
function askToSignIn() {
  const w = window as any;
  const text = "Sign in to access agent mode.";
  if (typeof w.notify !== "function") {
    window.alert(text);
    return;
  }
  const signIn = typeof w.openSignIn === "function" ? { label: "Sign in", onClick: () => w.openSignIn() } : undefined;
  w.notify(text, { type: "info", timeout: 8000, action: signIn });
}

// Returns what undoes it. The mode follows the user signing in or out while
// the panel is open (the page puts is-signed-in on the body).
function wireMode(): () => void {
  const box = document.getElementById("chat-widget__mode");
  if (!box) return () => {};
  box.querySelectorAll("button").forEach((b) =>
    b.addEventListener("click", () => {
      if (agentRunner.running) return;
      const wantsAgent = b.getAttribute("data-mode") === "agent";
      if (wantsAgent && agentAvailability() === "signin") {
        askToSignIn(); // a guest: say why, and leave the mode and what is remembered as they are
        return;
      }
      const agent = wantsAgent && agentAvailability() === "ready";
      setMode(agent);
      rememberMode(agent ? "agent" : "chat");
    })
  );
  setMode(false);
  refreshMode();
  // (a panel opened while a task is under way gets its buttons from open())
  const watch = new MutationObserver(refreshMode);
  watch.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  return () => watch.disconnect();
}

// Brings the chip, the cards and the effort control in line with `choice`.
function renderChoice() {
  const m = chosenModel();
  const e = chosenEffort();
  const chip = chipEl();
  if (chip) {
    const label = document.getElementById("chat-widget__model-label");
    if (label) label.textContent = chipText();
    chip.setAttribute("aria-label", `Model and effort: ${chipText()}`);
  }
  document.querySelectorAll<HTMLElement>("#chat-widget__models .cw-model").forEach((btn) => {
    btn.setAttribute("aria-checked", btn.dataset.id === m.id ? "true" : "false");
  });
  const group = document.getElementById("chat-widget__efforts");
  const effortLabel = document.getElementById("cw-effort-label");
  if (group) group.classList.toggle("is-off", !m.reasoning);
  if (effortLabel) effortLabel.classList.toggle("is-off", !m.reasoning);
  document.querySelectorAll<HTMLButtonElement>("#chat-widget__efforts button").forEach((btn) => {
    btn.setAttribute("aria-pressed", m.reasoning && btn.dataset.id === e.id ? "true" : "false");
    btn.disabled = !m.reasoning;
  });
  const note = document.getElementById("chat-widget__effort-note");
  if (note) {
    note.textContent = m.reasoning
      ? e.note
      : `${m.short} doesn't think before answering, so effort doesn't apply.`;
  }
}

// the dialog or another tab may change it while the panel is open
MC.onChange(renderChoice);

// Arrow keys move through a group of buttons and choose as they go.
function arrowKeys(group: HTMLElement) {
  group.addEventListener("keydown", (ev: KeyboardEvent) => {
    const keys = ["ArrowRight", "ArrowDown", "ArrowLeft", "ArrowUp"];
    if (keys.indexOf(ev.key) < 0) return;
    const buttons = Array.prototype.slice
      .call(group.querySelectorAll("button"))
      .filter((b: HTMLButtonElement) => !b.disabled) as HTMLButtonElement[];
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (at < 0 || buttons.length < 2) return;
    ev.preventDefault();
    const step = ev.key === "ArrowRight" || ev.key === "ArrowDown" ? 1 : -1;
    const next = buttons[(at + step + buttons.length) % buttons.length];
    next.focus();
    next.click();
  });
}

// Builds the cards and the effort control and wires them. Returns what removes
// the listeners that outlive the panel's own elements.
function setupSettings(): () => void {
  const chip = chipEl();
  const models = document.getElementById("chat-widget__models");
  const efforts = document.getElementById("chat-widget__efforts");
  if (!chip || !models || !efforts) return () => {};

  let lastGroup = "";
  MODELS.forEach((m) => {
    if (m.group !== lastGroup) {
      // "OpenAI", "OpenRouter": a heading for each provider
      lastGroup = m.group;
      const heading = document.createElement("div");
      heading.className = "cw-set__group-label";
      heading.setAttribute("role", "presentation");
      heading.textContent = m.group;
      models.appendChild(heading);
    }
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "cw-model";
    btn.setAttribute("role", "radio");
    btn.dataset.id = m.id;
    const dot = document.createElement("span");
    dot.className = "cw-model__dot";
    dot.setAttribute("aria-hidden", "true");
    const text = document.createElement("span");
    text.className = "cw-model__text";
    const name = document.createElement("span");
    name.className = "cw-model__name";
    name.textContent = m.name;
    if (m.tag) {
      const tag = document.createElement("span");
      tag.className = "cw-model__tag";
      tag.textContent = m.tag;
      name.appendChild(tag);
    }
    const desc = document.createElement("span");
    desc.className = "cw-model__desc";
    desc.textContent = m.desc;
    text.append(name, desc);
    btn.append(dot, text);
    btn.addEventListener("click", () => MC.set(m.id));
    models.appendChild(btn);
  });
  EFFORTS.forEach((e) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.dataset.id = e.id;
    btn.textContent = e.label;
    btn.addEventListener("click", () => MC.set(undefined, e.id));
    efforts.appendChild(btn);
  });
  arrowKeys(models);
  arrowKeys(efforts);

  chip.addEventListener("click", () => setSettingsOpen(!settingsOpen()));
  // Esc closes the settings first, and leaves the panel open
  const form = document.getElementById("chat-widget__form")!;
  form.addEventListener("keydown", (ev: KeyboardEvent) => {
    if (ev.key === "Escape" && settingsOpen()) {
      ev.stopPropagation();
      setSettingsOpen(false, true);
    }
  });
  const onDocClick = (ev: MouseEvent) => {
    const el = settingsEl();
    if (!el || el.hidden) return;
    const target = ev.target as Node;
    if (!el.contains(target) && !chip.contains(target)) setSettingsOpen(false);
  };
  document.addEventListener("click", onDocClick);

  renderChoice();
  refreshChip();
  return () => document.removeEventListener("click", onDocClick);
}

// Listeners that live only while the panel is open.
let detachPanel = () => {};

function isOpen(): boolean {
  return containerElement.isConnected;
}

function open(e?: Event) {
  if (isOpen()) {
    // already open: just put the cursor in the box
    (document.getElementById("chat-widget__input") as HTMLTextAreaElement | null)?.focus();
    return;
  }
  if (config.closeOnOutsideClick) {
    document.body.appendChild(optionalBackdrop);
  }

  document.body.appendChild(containerElement);
  containerElement.innerHTML = widgetHTML;
  unread = null; // opening is how the result is seen
  containerElement.setAttribute("role", "dialog");
  containerElement.setAttribute("aria-label", config.widgetTitle);
  document.body.classList.add("genie-open");

  const chatbotHeaderTitleText = document.createElement("span");
  chatbotHeaderTitleText.id = "chat-widget__title_text";
  chatbotHeaderTitleText.textContent = config.widgetTitle;
  const chatbotHeaderTitle = document.getElementById(
    "chat-widget__title"
  )!;
  chatbotHeaderTitle.appendChild(chatbotHeaderTitleText);

  const chatbotBody = document.getElementById("chat-widget__body")!;
  chatbotBody.prepend(messagesHistory);
  if (config.greetingMessage && messagesHistory.children.length === 0) {
    createNewMessageEntry(config.greetingMessage, Date.now(), "system", true);
  }

  const context = document.getElementById("chat-widget__context");
  if (context) context.textContent = editorContextLabel();

  makeResizable(containerElement);
  if (config.closeOnOutsideClick) {
    // modal: keep keyboard focus inside the panel
    trap.activate();
  }

  document.getElementById("chat-widget__close")!.addEventListener("click", close);
  const onKeydown = (ev: KeyboardEvent) => {
    if (ev.key === "Escape") {
      ev.stopPropagation();
      close();
    }
  };
  containerElement.addEventListener("keydown", onKeydown);
  const detachSettings = setupSettings();
  const detachMode = wireMode();
  wireCoach();
  detachPanel = () => {
    containerElement.removeEventListener("keydown", onKeydown);
    detachSettings();
    detachMode();
  };

  const input = document.getElementById("chat-widget__input") as HTMLTextAreaElement;
  input.addEventListener("input", () => autoGrow(input));
  if (!config.closeOnOutsideClick) {
    input.focus({ preventScroll: true });
  }

  if (config.closeOnOutsideClick) {
    document
      .getElementById(WIDGET_BACKDROP_ID)!
      .addEventListener("click", close);
  }

  document
    .getElementById("chat-widget__form")!
    .addEventListener("submit", submit);
  // While a task runs the button stops it. This is on the click, not on the
  // form's submit: the box is empty then, and it is a required field, so the
  // browser would not submit the form at all.
  document.getElementById("chat-widget__submit")!.addEventListener("click", (e: Event) => {
    if (!agentRunner.running) return;
    e.preventDefault();
    agentRunner.stop();
  });

  if (config.submitOnKeydown) {
    document
      .getElementById("chat-widget__input")!
      .addEventListener("keydown", (e: KeyboardEvent)=> {
        if (e.which === 13 && !e.shiftKey) {
          e.preventDefault();
          // while a task runs the button is Stop: Enter must not stop it
          if (agentRunner.running) return;
          const submitBtn = document.getElementById("chat-widget__submit") as HTMLButtonElement;;
          submitBtn.click();
        }
      });
  }

  const peerchatSwitchElem = document.getElementById("peerchat-switch") as HTMLInputElement;
  if (peerchatSwitchElem) {
    peerchatSwitchElem.checked = peerchatmode;
    peerchatSwitchElem.addEventListener("change", peerchatSwitchlistener);
  }

  document.getElementById("chat-widget__usage-line")?.addEventListener("click", () => {
    usageDetails = !usageDetails;
    renderUsage();
  });
  renderUsage();
  fetchUsage();
  clearInterval(usageTimer);
  usageTimer = setInterval(renderUsage, 1000); // the recharge, and the wait when nothing is left
  // The panel is built again each time it opens, but a task or an answer may be
  // under way from before it was closed: its buttons must say so.
  showAgentState(lastAgentState);
  if (chatInFlight) document.getElementById("chat-widget__submit")?.setAttribute("disabled", "");
  renderActivity();
}

function close() {
  if (!isOpen()) return;
  clearInterval(usageTimer);
  // A task or an answer in flight is not stopped: the panel is hidden, and the
  // Ask Genie button shows how it goes (renderActivity). Stop is the panel's button.
  trap.deactivate();
  detachPanel();
  detachPanel = () => {};

  containerElement.innerHTML = "";

  containerElement.remove();
  optionalBackdrop.remove();
  document.body.classList.remove("genie-open");
  renderActivity();
}

function toggle(e?: Event) {
  if (isOpen()) {
    close();
  } else {
    open(e);
  }
}

// ---- what the site's own notes added to a reply ---------------------------------
// The server lists the notes and blog passages it added to the question in the
// X-OpenREPL-Context header: "none", or base64url of [{id, title, kind}]. The
// reply gets a line saying so, and each name opens the text it was based on.

const CONTEXT_HEADER = "X-OpenREPL-Context";
type ContextNote = { id: string; title: string; kind: string };

function onPracticePage(): boolean {
  return window.location.pathname.includes("practice");
}

// null: the request did not ask (no line); []: asked, nothing matched
function parseContextHeader(value: string | null): ContextNote[] | null {
  if (!value) return null;
  if (value === "none") return [];
  try {
    const b64 = value.replace(/-/g, "+").replace(/_/g, "/");
    const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    const list = JSON.parse(new TextDecoder("utf-8").decode(bytes));
    if (!Array.isArray(list)) return null;
    return list
      .filter((n: any) => n && typeof n.id === "string" && typeof n.title === "string")
      .slice(0, 6)
      .map((n: any) => ({ id: n.id, title: n.title, kind: String(n.kind || "note") }));
  } catch (e) {
    return null;
  }
}

function knowledgeUrl(id: string): string {
  return (config.url || "").replace(/chat\/completions$/, "knowledge/") + encodeURIComponent(id);
}

// only a path on this site may be linked to
function safeSiteLink(link: string): string {
  return typeof link === "string" && /^\/(?!\/)[^\s\\]*$/.test(link) ? link : "";
}

function appendContextLine(messageElement: HTMLElement, used: ContextNote[]) {
  const line = document.createElement("p");
  line.classList.add("chat-widget__message-meta", "chat-widget__context-line");
  if (used.length === 0) {
    line.textContent = "Answered without OpenREPL notes";
    messageElement.appendChild(line);
    return;
  }
  line.append("Used OpenREPL notes: ");
  const card = document.createElement("div");
  card.className = "chat-widget__notes";
  card.hidden = true;
  const rows: { head: HTMLButtonElement; body: HTMLElement; open: () => void; close: () => void }[] = [];

  const closeCard = () => {
    card.hidden = true;
  };
  card.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      ev.stopPropagation(); // closes the notes, not the whole panel
      closeCard();
      (line.querySelector("button") as HTMLElement | null)?.focus();
    }
  });

  used.forEach((note, i) => {
    const row = document.createElement("div");
    row.className = "chat-widget__note";
    const head = document.createElement("button");
    head.type = "button";
    head.className = "chat-widget__note-head";
    head.setAttribute("aria-expanded", "false");
    const title = document.createElement("span");
    title.textContent = note.title;
    head.appendChild(title);
    const body = document.createElement("div");
    body.className = "chat-widget__note-body";
    body.hidden = true;
    let loaded = false;

    const open = () => {
      rows.forEach((r) => r !== entry && r.close());
      body.hidden = false;
      head.setAttribute("aria-expanded", "true");
      if (!loaded) {
        loaded = true;
        body.textContent = "Loading…";
        fetch(knowledgeUrl(note.id))
          .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
          .then((d: any) => {
            body.textContent = "";
            const text = document.createElement("p");
            text.className = "chat-widget__note-text";
            text.textContent = String(d.text || ""); // plain text, never markup
            body.appendChild(text);
            const link = safeSiteLink(d.link);
            if (link) {
              const a = document.createElement("a");
              a.href = link;
              a.target = "_blank";
              a.rel = "noopener";
              a.textContent = note.kind === "post" ? "Read the post" : "Read more in the docs";
              body.appendChild(a);
            }
          })
          .catch(() => {
            loaded = false;
            body.textContent = "This note could not be loaded.";
          });
      }
    };
    const close = () => {
      body.hidden = true;
      head.setAttribute("aria-expanded", "false");
    };
    const entry = { head, body, open, close };
    rows.push(entry);
    head.addEventListener("click", () => (body.hidden ? open() : close()));
    row.append(head, body);
    card.appendChild(row);

    const name = document.createElement("button");
    name.type = "button";
    name.className = "chat-widget__context-link";
    name.textContent = note.title;
    name.addEventListener("click", () => {
      card.hidden = false;
      open();
    });
    if (i > 0) line.append(" · ");
    line.appendChild(name);
  });

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "chat-widget__notes-close";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.textContent = "×";
  closeBtn.addEventListener("click", closeCard);
  card.prepend(closeBtn);
  messageElement.append(line, card);
}

async function createNewMessageEntry(
  message: string,
  timestamp: number,
  from: "system" | "user",
  skipdbpush: boolean = false,
  meta: string = "",
  used: ContextNote[] | null = null
) {
  message = message.trim();
  //console.log("message: ", message)
  if (!skipdbpush && chatfirebasedbref) {
    // push to firebase db first
    chatfirebasedbref.push ({
         message: message,
         timestamp: timestamp,
         from: from,
         uid: UID,
      });
  }
  

  const messageElement = document.createElement("div");
  messageElement.classList.add("chat-widget__message");
  messageElement.classList.add(`chat-widget__message--${from}`);
  messageElement.id = `chat-widget__message--${from}--${timestamp}`;

  const messageText = document.createElement("div");
  messageText.classList.add("chat-widget__message-text");
  const markedtext = await marked(message, { renderer });
  messageText.innerHTML = markedtext;
  messageElement.appendChild(messageText);
  //console.log("marked: ", markedtext);

  if (meta) {
    // which model answered, for a reply from Genie
    const messageMeta = document.createElement("p");
    messageMeta.classList.add("chat-widget__message-meta");
    messageMeta.textContent = meta;
    messageElement.appendChild(messageMeta);
  }
  if (used !== null) {
    appendContextLine(messageElement, used);
  }

  const messageTimestamp = document.createElement("p");
  messageTimestamp.classList.add("chat-widget__message-timestamp");
  messageTimestamp.textContent =
    ("0" + new Date(timestamp).getHours()).slice(-2) + // Hours (padded with 0 if needed)
    ":" +
    ("0" + new Date(timestamp).getMinutes()).slice(-2); // Minutes (padded with 0 if needed)
  messageElement.appendChild(messageTimestamp);

  messagesHistory.prepend(messageElement);
}

const handleErrorResponse = async (errData: any) => {
    chatFailed = true;
    console.error("Chat Widget: Server error: ", errData);
    if (errData && errData.error && errData.error.type === "model_unavailable") {
      await showModelUnavailable(
        String(errData.error.message || "This model isn't available right now"),
        errData.error.code === "model_disabled"
      );
      return;
    }
    let error_reason : string = " "
    if (errData.error && errData.error.message && errData.error.type) {
      error_reason += "Reason: "+errData.error.type;
      error_reason += " : "+errData.error.message;
    } else if (typeof errData == "string") {
      error_reason += "Reason: "+errData;
    }
    await createNewMessageEntry("Unable to process your request Now."+error_reason, Date.now(), "system");
}

// A model that cannot be reached right now (the proxy answers type
// "model_unavailable", for Gemma through OpenRouter): a card that says so, with
// a way to retry and a way to carry on with GPT-6 Luna, which is not down.
// An admin may also have switched the model off (code "model_disabled"): then
// there is nothing to retry, only another model to choose.
async function showModelUnavailable(title: string, switchedOff: boolean = false) {
  const card = document.createElement("div");
  card.classList.add("chat-widget__message", "chat-widget__message--system", "chat-widget__unavailable");
  card.id = `chat-widget__message--system--${Date.now()}`;
  card.setAttribute("role", "alert");

  const heading = document.createElement("p");
  heading.className = "chat-widget__unavailable-title";
  heading.textContent = title;
  const text = document.createElement("p");
  text.className = "chat-widget__unavailable-text";
  text.textContent = switchedOff
    ? "It has been switched off. Choose another model."
    : "The service that runs it didn't answer. Try again in a moment, or switch to a model that is on all the time.";
  const actions = document.createElement("div");
  actions.className = "chat-widget__unavailable-actions";

  if (!switchedOff) {
    const again = document.createElement("button");
    again.type = "button";
    again.textContent = "Try again";
    again.addEventListener("click", () => {
      card.remove();
      runRequest();
    });
    actions.appendChild(again);
  }

  const fallback = MC.fallback(MC.get().model);
  if (fallback) {
    const switchBtn = document.createElement("button");
    switchBtn.type = "button";
    switchBtn.className = "is-primary";
    switchBtn.textContent = `Switch to ${fallback.name}`;
    switchBtn.addEventListener("click", () => {
      card.remove();
      MC.set(fallback.id);
      runRequest();
    });
    actions.appendChild(switchBtn);
  }
  card.append(heading, text, actions);
  messagesHistory.prepend(card);
}

const handleStandardResponse = async (res: Response, meta: string = "") => {
  if (res.ok) {
    const used = parseContextHeader(res.headers.get(CONTEXT_HEADER));
    const responseData : any = await res.json();
    if (responseData.choices && responseData.choices.length > 0) {
        const responseMessage : MessageType = responseData.choices[0].message;
        addMessageToHistory(responseMessage.role, responseMessage.content);
        await createNewMessageEntry(responseMessage.content, Date.now(), "system", false, meta, used);
    } else {
        handleErrorResponse(responseData);
    }
  } else {
    try {
      const responseData : any = await res.json();
      handleErrorResponse(responseData);
    } catch (error) {
        handleErrorResponse(res);
    }
  }
};

async function streamResponseToMessageEntry(
  message: string,
  timestamp: number,
  from: "system" | "user"
) {
  const existingMessageElement = messagesHistory.querySelector(
    `#chat-widget__message--${from}--${timestamp}`
  );
  if (existingMessageElement) {
    // If the message element already exists, update the text
    const messageText = existingMessageElement.querySelector(".chat-widget__message-text")!;
    messageText.innerHTML = await marked(message, { renderer });
    return;
  } else {
    // If the message element doesn't exist yet, create a new one
    await createNewMessageEntry(message, timestamp, from);
  }
}

const handleStreamedResponse = async (res: Response) => {
  if (!res.body) {
    console.error("Chat Widget: Streamed response has no body", res);
    await createNewMessageEntry("Unable to process your request now.", Date.now(), "system");
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let responseMessage = "";
  let ts = Date.now();

  // no `break` in a loop that awaits (see agent.ts: the build tool gets it wrong)
  let more = true;
  while (more) {
    const { value, done } = await reader.read();
    more = !(done || !value);
    if (more && value) {
      const chunk = decoder.decode(value, { stream: true });
      try {
        const json = JSON.parse(chunk);
        const deltaContent = json.choices[0]?.delta?.content || "";
        responseMessage += deltaContent;
        await streamResponseToMessageEntry(deltaContent, ts, "system");
      } catch (error) {
        console.error("Error parsing chunk: ", chunk, error);
      }
    }
  }
  const used = parseContextHeader(res.headers.get(CONTEXT_HEADER));
  const shown = messagesHistory.querySelector(`#chat-widget__message--system--${ts}`);
  if (used !== null && shown) {
    appendContextLine(shown as HTMLElement, used);
  }
};

async function submit(e: Event) {
  e.preventDefault();
  const target = e.target as HTMLFormElement;

  if (!config.url) {
    console.error("Chat Widget: No URL provided");
    alert("Could not send chat message: No URL provided");
    return;
  }

  // the button is Stop while a task runs
  if (agentRunner.running) {
    agentRunner.stop();
    return;
  }
  const msg = (target.elements as any).message.value;
  // nothing to send: an empty task would still cost a request
  if (!String(msg || "").trim()) return;
  // nothing left: the server would refuse it, so the line says when to come back
  if (noneLeft()) {
    flashUsage();
    return;
  }

  const submitElement = document.getElementById(
    "chat-widget__submit"
  )!;
  submitElement.setAttribute("disabled", "");

  let myrole: "system" | "user" = 'user';
  if (peerchatmode && isMaster()) {
    myrole = 'system';
  }
  messagesHistory.querySelectorAll(".chat-widget__notes").forEach((el) => ((el as HTMLElement).hidden = true));
  if (agentMode && !peerchatmode && agentAvailability() === "ready") {
    // a task: the agent shows the message itself, and takes the box until it is done
    target.reset();
    autoGrow((target.elements as any).message as HTMLTextAreaElement);
    await agentRunner.start(msg);
    return false;
  }
  addMessageToHistory(myrole, msg);

  await createNewMessageEntry(msg, Date.now(), myrole);
  target.reset();
  autoGrow((target.elements as any).message as HTMLTextAreaElement);
  if (peerchatmode) {
    submitElement.removeAttribute("disabled");
    // not much to do in peerchat mode
    return;
  }
  await runRequest();
  return false;
}

// Sends the conversation so far to the chosen model and shows the answer. Used
// by submit, and by "Try again" and "Switch to ..." on the unavailable card,
// which send the same conversation again with whatever model is chosen then.
async function runRequest() {
  // the button is looked up again at the end: the panel may have been closed and
  // opened meanwhile, and then it is another button
  document.getElementById("chat-widget__submit")?.setAttribute("disabled", "");
  chatInFlight = true;
  chatFailed = false;
  renderActivity();

  const requestHeaders = new Headers();
  requestHeaders.append("Content-Type", "application/json");
  if (config.api_key) {
    requestHeaders.append('Authorization', 'Bearer ' + config.api_key);
  }
  const data = {
    ...config.user,
    ...modelFields(),
    messages: [...conversationHistory, getcurrentIDECode()],
    stream: config.responseIsAStream,
    // ask the server for what the site knows about itself; the practice page
    // has its own interviewer prompt and does not
    ...(onPracticePage() ? {} : { context: "chat" }),
  };
  const usedCaption = captionText();
  const thinking = chosenModel().reasoning && chosenEffort().id !== "none";
  const thinkingLabel = thinkingBubble.querySelector(".chat-widget__thinking-label");
  if (thinkingLabel) thinkingLabel.textContent = thinking ? `${chosenModel().short} is thinking…` : "";
  messagesHistory.prepend(thinkingBubble);

  try {
    let response = await fetch(config.url, {
      method: "POST",
      headers: requestHeaders,
      body: JSON.stringify(data),
    });
    thinkingBubble.remove();
    const told = response.headers.get("X-OpenREPL-Usage");
    if (told) takeUsage(told);
    else fetchUsage(); // a refusal carries none

    if (config.responseIsAStream) {
      await handleStreamedResponse(response);
    } else {
      await handleStandardResponse(response, usedCaption);
    }
  } catch (e: any) {
    thinkingBubble.remove();
    chatFailed = true;
    console.error("Chat Widget:", e);
    await createNewMessageEntry("Unable to process your request Now.", Date.now(), "system");
  }

  chatInFlight = false;
  document.getElementById("chat-widget__submit")?.removeAttribute("disabled");
  if (!isOpen()) unread = chatFailed ? { kind: "failed", label: "Genie couldn't answer" } : { kind: "done", label: "Answer ready" };
  renderActivity();
}

// placeholder for a page that does not define its own (index.html does: the page
// script shows the change as a diff to accept, js/src/page/18-genie-review.js)
if (typeof (window as any).insertcodesnippet !== "function") {
  (window as any).insertcodesnippet = function(encodedcode: string) {
    const code = atob(encodedcode);
    console.log("code insert hit: ", code);
  };
}

// Asks Genie a question for the page (the right-click action Explain): opens the panel, switches to Chat and sends the text as the user's
// message. False if it cannot, with a notice that says why.
// Genie is answering a message (the send button is off for that long), or the
// coach is: a second request would cross with the first.
function genieBusy(): boolean {
  const b = document.getElementById("chat-widget__submit");
  return coachBusy || chatInFlight || (!!b && b.hasAttribute("disabled"));
}

async function ask(text: string): Promise<boolean> {
  const say = (message: string) => {
    const n = (window as any).notify;
    if (typeof n === "function") n(message, { type: "info" });
  };
  if (agentRunner.running) {
    say("Genie is busy with a task. Stop it or wait until it is done.");
    return false;
  }
  if (peerchatmode) {
    say("Turn off peer chat to ask Genie.");
    return false;
  }
  if (genieBusy()) {
    say("Genie is still answering. Ask again in a moment.");
    return false;
  }
  open();
  if (agentMode) setMode(false); // a question, not a task; the remembered mode stays as it was
  const input = document.getElementById("chat-widget__input") as HTMLTextAreaElement | null;
  const form = document.getElementById("chat-widget__form") as HTMLFormElement | null;
  if (!input || !form) return false;
  input.value = text;
  autoGrow(input);
  form.requestSubmit();
  return true;
}

// Genie is in the middle of something (a task, an answer, the coach): the page's
// right-click actions wait, because they use the same diff panel as a task does.
function busy(): boolean {
  return agentRunner.running || genieBusy();
}

const ChatWidget = { open, close, toggle, config, init, ask, busy };
(window as any).ChatWidget = ChatWidget;
declare global {
  interface Window {
    ChatWidget: typeof ChatWidget;
    insertcodesnippet: () => void;
    replacecodesnippet: () => void;
    firebase: typeof import('firebase');
    editor?: any;
  }
}

export default ChatWidget;
