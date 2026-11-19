// The Genie panel pinned beside the IDE (index.ts keeps the panel itself): the dock, its width and the pin. Moved out of index.ts
// as it was; the panel and whether it is open come in through configureDock.

interface DockDeps {
  container: () => HTMLElement;
  isOpen: () => boolean;
}
let deps: DockDeps = { container: () => document.body, isOpen: () => false };
export function configureDock(d: DockDeps) {
  deps = d;
}
export function isPinned(): boolean {
  return pinnedPref;
}

// ---- pinned beside the IDE -------------------------------------------------------
// The pin button docks the panel in the workspace row (#genie-dock, index.html),
// to the right of the editor and the terminal, like a side bar. While it is
// pinned the page stacks the editor over the terminal to make room: this file
// only says so, with the "genie-pin" event (js/src/page/02-layout.js listens).
// Closing a pinned panel hides it and keeps the pin. Where there is no room (a
// phone, a window under 1100px) the panel floats as before and the pin button is
// hidden; the choice itself is kept for a wider window.

const PIN_KEY = "genie-pinned";
const DOCK_W_KEY = "genie-dock-width";
const DOCK_MIN_VIEWPORT = 1100;
const DOCK_DEFAULT_W = 380;
const DOCK_MIN_W = 300;

let pinnedPref = (() => {
  try {
    return localStorage.getItem(PIN_KEY) === "1";
  } catch (e) {
    return false; // storage can be blocked: not remembered
  }
})();
let pinTold = false; // what the page was last told
let resizePending = false;
let dockWidthSet = 0; // the width the dock was last given

export function dockEl(): HTMLElement | null {
  return document.getElementById("genie-dock");
}

// A phone: the panel is the third pane of the page (Editor | Terminal | Genie), always in the dock; the page shows the dock
// only while its tab is the one shown (js/src/page/13-mobile.js, ui-refresh.css). Nothing of this is on a desktop.
export function onPhone(): boolean {
  return document.body.classList.contains("is-mobile");
}

export function canDock(): boolean {
  return !!dockEl() && (onPhone() || window.innerWidth >= DOCK_MIN_VIEWPORT);
}

function wantDock(): boolean {
  return canDock() && (onPhone() || pinnedPref);
}

// the editor, the terminal and the terminal tabs fit themselves to a resize
export function requestResize() {
  if (resizePending) return;
  resizePending = true;
  requestAnimationFrame(() => {
    resizePending = false;
    window.dispatchEvent(new Event("resize"));
  });
}

export function announcePin() {
  // the page stacks the IDE only while the pinned panel is shown: a pin that is kept but closed leaves the layout as the user chose it
  const now = wantDock() && deps.isOpen() && !onPhone(); // (the page stacks the IDE beside a pinned panel: not on a phone)
  if (now === pinTold) return;
  pinTold = now;
  window.dispatchEvent(new CustomEvent("genie-pin", { detail: { pinned: now } }));
  requestResize();
}

// Puts the panel where it belongs: in the dock when pinned, else on the page.
export function placePanel() {
  const dock = dockEl();
  const was = document.body.classList.contains("genie-docked");
  if (dock && wantDock()) {
    dock.hidden = false;
    dock.appendChild(deps.container());
    deps.container().classList.add("is-docked");
    document.body.classList.add("genie-docked");
  } else {
    deps.container().classList.remove("is-docked");
    document.body.classList.remove("genie-docked");
    if (dock) dock.hidden = true;
    document.body.appendChild(deps.container());
  }
  // the IDE has more or less room only when the dock came or went
  if (was !== document.body.classList.contains("genie-docked")) requestResize();
  announcePin();
}

export function refreshPin() {
  const b = document.getElementById("chat-widget__pin");
  if (!b) return;
  b.hidden = !canDock() || onPhone(); // a phone has no floating panel to pin
  b.setAttribute("aria-pressed", String(pinnedPref));
  const words = pinnedPref ? "Unpin Genie (back to floating)" : "Pin Genie beside the IDE";
  b.setAttribute("aria-label", words);
  b.setAttribute("title", words);
}

export function setPinned(on: boolean) {
  pinnedPref = on;
  try {
    localStorage.setItem(PIN_KEY, on ? "1" : "0");
  } catch (e) {
    // not remembered
  }
  if (deps.isOpen()) placePanel();
  refreshPin();
  announcePin();
}

// the window changed size: dock or float the panel if what fits has changed
function onViewportChange() {
  if (deps.isOpen() && deps.container().classList.contains("is-docked") !== wantDock()) placePanel();
  if (deps.isOpen()) refreshPin();
  announcePin();
  // keep a shown dock within what fits (a hidden one measures 0 and keeps its width)
  const dock = dockEl();
  if (dock && !dock.hidden && wantDock()) setDockWidth(dock.getBoundingClientRect().width, false);
}

function setDockWidth(w: number, save: boolean = true): number {
  const dock = dockEl();
  const row = dock && dock.parentElement;
  const total = row ? row.getBoundingClientRect().width : window.innerWidth;
  // (a row that has no width yet, as at start-up, limits nothing)
  const max = total > 0 ? Math.max(DOCK_MIN_W, Math.min(680, Math.floor(total * 0.6))) : 680;
  const width = Math.round(Math.min(max, Math.max(DOCK_MIN_W, w)));
  // Only a width that changed is news to the page. This runs on every resize of
  // the window (onViewportChange), and telling the page to resize from here each
  // time would be a resize that causes the next one, for ever: the editor and the
  // terminal would refit on every frame, and the right-click menu of the editor,
  // which closes on a resize, could not stay open.
  const changed = width !== dockWidthSet;
  dockWidthSet = width;
  document.documentElement.style.setProperty("--genie-dock-w", width + "px");
  const grip = document.getElementById("genie-dock-grip");
  if (grip) {
    grip.setAttribute("aria-valuemin", String(DOCK_MIN_W));
    grip.setAttribute("aria-valuemax", String(max));
    grip.setAttribute("aria-valuenow", String(width));
  }
  if (save) {
    try {
      localStorage.setItem(DOCK_W_KEY, String(width));
    } catch (e) {
      // not remembered
    }
  }
  if (changed) requestResize();
  return width;
}

// The grip on the dock's left edge: drag it, or use the arrow keys; a double
// click goes back to the default width.
export function wireDock() {
  const dock = dockEl();
  const grip = document.getElementById("genie-dock-grip");
  if (!dock || !grip) return;
  let stored = DOCK_DEFAULT_W;
  try {
    stored = parseInt(localStorage.getItem(DOCK_W_KEY) || "", 10) || DOCK_DEFAULT_W;
  } catch (e) {
    // the default
  }
  setDockWidth(stored, false);
  const widthNow = () => dock.getBoundingClientRect().width;
  grip.addEventListener("pointerdown", (ev: PointerEvent) => {
    ev.preventDefault();
    try {
      grip.setPointerCapture(ev.pointerId);
    } catch (e) {
      // the drag still works while the pointer stays on the grip
    }
    const startX = ev.clientX;
    const startW = widthNow();
    grip.classList.add("is-dragging");
    document.body.classList.add("genie-dock-dragging");
    const move = (e: PointerEvent) => setDockWidth(startW + (startX - e.clientX), false);
    const up = () => {
      grip.removeEventListener("pointermove", move);
      grip.removeEventListener("pointerup", up);
      grip.removeEventListener("pointercancel", up);
      grip.classList.remove("is-dragging");
      document.body.classList.remove("genie-dock-dragging");
      setDockWidth(widthNow());
    };
    grip.addEventListener("pointermove", move);
    grip.addEventListener("pointerup", up);
    grip.addEventListener("pointercancel", up);
  });
  grip.addEventListener("keydown", (ev: KeyboardEvent) => {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
    ev.preventDefault();
    setDockWidth(widthNow() + (ev.key === "ArrowLeft" ? 16 : -16));
  });
  grip.addEventListener("dblclick", () => setDockWidth(DOCK_DEFAULT_W));
  window.addEventListener("resize", onViewportChange);
}

