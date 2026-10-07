import { createFocusTrap } from "focus-trap";
import { marked } from "marked";

import { widgetHTML } from "./widgetHtmlString";
import { keywords, documentation } from "./openreplkeywords";
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
const NUM_MANDATORY_ENTRIES = 4;
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

function fetchTerminalOutput(): string {
  try {
    // the xterm adapter keeps its buffer; older builds only have the rows in the DOM
    const tab = document.querySelector("#terminal-tabs .tab.active") as any;
    const term = tab && tab.gottyterm && tab.gottyterm.term;
    if (term && typeof term.recentText === "function") {
      return String(term.recentText(terminalLines()));
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
  // only four permanent prompts
  if (window.location.pathname.includes("practice")) {
    addMessageToHistory("system", welcomeprompt+" Interviewer.");
    addMessageToHistory("system", interviewPrompt);
  } else {
    addMessageToHistory("system", welcomeprompt+" Assistant.");
    addMessageToHistory("system", "documentation: "+documentation);
  }
  addMessageToHistory("system", "keywords: "+ keywords);
  addMessageToHistory("system", "Openrepl IDE/EditorCodeContent: "+ fetchEditorContent());
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
  const chip = chipEl();
  if (!chip) return;
  chip.hidden = peerchatmode;
  if (peerchatmode) setSettingsOpen(false);
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
  detachPanel = () => {
    containerElement.removeEventListener("keydown", onKeydown);
    detachSettings();
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

  if (config.submitOnKeydown) {
    document
      .getElementById("chat-widget__input")!
      .addEventListener("keydown", (e: KeyboardEvent)=> {
        if (e.which === 13 && !e.shiftKey) {
          e.preventDefault();
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
}

function close() {
  if (!isOpen()) return;
  trap.deactivate();
  detachPanel();
  detachPanel = () => {};

  containerElement.innerHTML = "";

  containerElement.remove();
  optionalBackdrop.remove();
  document.body.classList.remove("genie-open");
}

function toggle(e?: Event) {
  if (isOpen()) {
    close();
  } else {
    open(e);
  }
}

async function createNewMessageEntry(
  message: string,
  timestamp: number,
  from: "system" | "user",
  skipdbpush: boolean = false,
  meta: string = ""
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
    const responseData : any = await res.json();
    if (responseData.choices && responseData.choices.length > 0) {
        const responseMessage : MessageType = responseData.choices[0].message;
        addMessageToHistory(responseMessage.role, responseMessage.content);
        await createNewMessageEntry(responseMessage.content, Date.now(), "system", false, meta);
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

  while (true) {
    const { value, done } = await reader.read();
    if (done || !value) {
      break;
    }

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
};

async function submit(e: Event) {
  e.preventDefault();
  const target = e.target as HTMLFormElement;

  if (!config.url) {
    console.error("Chat Widget: No URL provided");
    alert("Could not send chat message: No URL provided");
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
  const msg = (target.elements as any).message.value;
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
  const submitElement = document.getElementById("chat-widget__submit")!;
  submitElement.setAttribute("disabled", "");

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

    if (config.responseIsAStream) {
      await handleStreamedResponse(response);
    } else {
      await handleStandardResponse(response, usedCaption);
    }
  } catch (e: any) {
    thinkingBubble.remove();
    console.error("Chat Widget:", e);
    await createNewMessageEntry("Unable to process your request Now.", Date.now(), "system");
  }

  submitElement.removeAttribute("disabled");
}

(window as any).insertcodesnippet = function(encodedcode: string) {
  const code = atob(encodedcode);
  console.log("code insert hit: ", code);
};

const ChatWidget = { open, close, toggle, config, init };
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
