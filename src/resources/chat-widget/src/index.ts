import { computePosition, flip, shift, autoUpdate } from "@floating-ui/dom";
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
  model: string;
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
  
  // Create and append the button
  const encodedcode = btoa(code);
  const insertButton = `<button class="share-btn" onclick="insertcodesnippet('${encodedcode}')">Insert</button>`
  const replaceButton = `<button class="share-btn" onclick="replacecodesnippet('${encodedcode}')">Replace</button>`
  
  return parsedcode+insertButton+replaceButton;
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
  model: "gpt-3.5-turbo",
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

const NUM_MANDATORY_ENTRIES = 4;
const MAX_HISTORY_SIZE = 20; 
// older conversation history might not be usefull
// Initialize the conversationHistory array
let conversationHistory: MessageType[] = [];

function getcurrentIDECode(): MessageType {
  let idecodemsg =  { 
        role: "system", 
        content: `Openrepl IDE/Editor real-time Code Content user is working on, 
        (refer this code whenever user ask to debug editor/ide 
        code without providing any code in message): `+ fetchEditorContent(),
      }
  return idecodemsg; 
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
  if (conversationHistory.length > MAX_HISTORY_SIZE) {
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
  `;

const trap = createFocusTrap(containerElement, {
  initialFocus: "#chat-widget__input",
  allowOutsideClick: true,
});

function makeResizable(containerElement: HTMLElement, target: HTMLElement) {
  // Create a resizer div
  const resizer = document.createElement("div");
  resizer.innerHTML = `
    <svg width="20" height="20" viewBox="0 0 20 20">
      <line x1="4" y1="16" x2="16" y2="4" stroke="gray" stroke-width="2" />
      <line x1="8" y1="16" x2="16" y2="8" stroke="gray" stroke-width="2" />
    </svg>
  `;
  resizer.style.position = "absolute";
  resizer.style.left = "5px";
  resizer.style.top = "5px";
  resizer.style.cursor = "nwse-resize";
  resizer.style.opacity = "0.7";
  resizer.style.transition = "opacity 0.2s";
  resizer.style.display = "flex";
  resizer.style.alignItems = "center";
  resizer.style.justifyContent = "center";
  resizer.style.width = "15px";
  resizer.style.height = "15px";

  // Style the container for a modern feel
  Object.assign(containerElement.style, {
    position: "absolute",
    borderRadius: "10px",
    boxShadow: "0 4px 10px rgba(0, 0, 0, 0.2)",
    overflow: "hidden",
    resize: "none", // Disable native resize
    transition: "width 0.2s ease, height 0.2s ease",
  });

  containerElement.appendChild(resizer);

  let isResizing = false;

  resizer.addEventListener("mousedown", (e) => {
    e.preventDefault();
    isResizing = true;

    const startX = e.clientX;
    const startY = e.clientY;
    const startWidth = containerElement.offsetWidth;
    const startHeight = containerElement.offsetHeight;

    function resize(e: MouseEvent) {
      if (!isResizing) return;
      const newWidth = Math.max(150, startWidth + (startX - e.clientX)); // Min width: 150px
      const newHeight = Math.max(100, startHeight + (startY - e.clientY)); // Min height: 100px
      containerElement.style.width = `${newWidth}px`;
      containerElement.style.height = `${newHeight}px`;

      // Recompute floating position to keep alignment
      updatePosition();
    }

    function stopResize() {
      isResizing = false;
      document.removeEventListener("mousemove", resize);
      document.removeEventListener("mouseup", stopResize);
    }

    document.addEventListener("mousemove", resize);
    document.addEventListener("mouseup", stopResize);
  });

  function updatePosition() {
    computePosition(target, containerElement, {
      placement: "top-start",
      middleware: [flip(), shift({ crossAxis: true, padding: 8 })],
      strategy: "fixed",
    }).then(({ x, y }) => {
      Object.assign(containerElement.style, {
        left: `${x}px`,
        top: `${y}px`,
      });
    });
  }

  return updatePosition; // Return the function so it can be used if needed
}

function open(e: Event) {
  if (config.closeOnOutsideClick) {
    document.body.appendChild(optionalBackdrop);
  }

  document.body.appendChild(containerElement);
  containerElement.innerHTML = widgetHTML;
  containerElement.style.display = "block";

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

  const target = (e?.target as HTMLElement) || document.body;
  cleanup = autoUpdate(target, containerElement, () => {
    computePosition(target, containerElement, {
      placement: "top-start",
      middleware: [flip(), shift({ crossAxis: true, padding: 8 })],
      strategy: "fixed",
    }).then(({ x, y }) => {
      Object.assign(containerElement.style, {
        left: `${x}px`,
        top: `${y}px`,
      });
    });
  });

  makeResizable(containerElement, target);
  trap.activate();

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
  trap.deactivate();

  containerElement.innerHTML = "";

  containerElement.remove();
  optionalBackdrop.remove();
  cleanup();
  cleanup = () => {};
}

async function createNewMessageEntry(
  message: string,
  timestamp: number,
  from: "system" | "user",
  skipdbpush: boolean = false
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

  const messageText = document.createElement("p");
  const markedtext = await marked(message, { renderer });
  messageText.innerHTML = markedtext;
  messageElement.appendChild(messageText);
  //console.log("marked: ", markedtext);

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
    let error_reason : string = " "
    if (errData.error && errData.error.message && errData.error.type) {
      error_reason += "Reason: "+errData.error.type;
      error_reason += " : "+errData.error.message;
    } else if (typeof errData == "string") {
      error_reason += "Reason: "+errData;
    }
    await createNewMessageEntry("Unable to process your request Now."+error_reason, Date.now(), "system");
}

const handleStandardResponse = async (res: Response) => {
  if (res.ok) {
    const responseData : any = await res.json();
    if (responseData.choices && responseData.choices.length > 0) {
        const responseMessage : MessageType = responseData.choices[0].message;
        addMessageToHistory(responseMessage.role, responseMessage.content);
        await createNewMessageEntry(responseMessage.content, Date.now(), "system");
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
    const messageText = existingMessageElement.querySelector("p")!;
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

  const requestHeaders = new Headers();
  requestHeaders.append("Content-Type", "application/json");
  if (config.api_key) {
    requestHeaders.append('Authorization', 'Bearer ' + config.api_key);
  }
  let myrole: "system" | "user" = 'user';
  if (peerchatmode && isMaster()) {
    myrole = 'system';
  }
  const msg = (target.elements as any).message.value;
  addMessageToHistory(myrole, msg);
  const data = {
    ...config.user,
    model: config.model,
    messages: [...conversationHistory, getcurrentIDECode()],
    temperature: config.temperature,
    max_tokens: config.max_tokens,
    stream: config.responseIsAStream,
  };

  await createNewMessageEntry(msg, Date.now(), myrole);
  target.reset();
  if (peerchatmode) {
    submitElement.removeAttribute("disabled");
    // not much to do in peerchat mode
    return;
  }
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
      await handleStandardResponse(response);
    }
  } catch (e: any) {
    thinkingBubble.remove();
    console.error("Chat Widget:", e);
    await createNewMessageEntry("Unable to process your request Now.", Date.now(), "system");
  }

  submitElement.removeAttribute("disabled");
  return false;
}

(window as any).insertcodesnippet = function(encodedcode: string) {
  const code = atob(encodedcode);
  console.log("code insert hit: ", code);
};

const ChatWidget = { open, close, config, init };
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
