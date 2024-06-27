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

function generateFiveCharUUID(): string {
  // Generate a UUID and extract the first 5 characters
  const uuid: string = crypto.randomUUID();
  return uuid.substring(0, 5);
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
  
  return parsedcode+insertButton;
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

const NUM_MANDATORY_ENTRIES = 3;
const MAX_HISTORY_SIZE = 20; 
// older conversation history might not be usefull
// Initialize the conversationHistory array
let conversationHistory: MessageType[] = [];

// Function to add a message to the conversation history
function addMessageToHistory(role: string, content: string, uid: string=UID): void {
  if (role=="user") {
    // to handle multiple peer users
    content = `[${role}-${uid}] ` + content;
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
  addMessageToHistory("system", "welcome to openrepl.com!! I am Genie. your OpenRepl AI assistant.");
  addMessageToHistory("system", "documentation: "+documentation);
  addMessageToHistory("system", "keywords: "+ keywords);
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
    messages: conversationHistory,
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
    firebase: typeof import('firebase');
  }
}

export default ChatWidget;
