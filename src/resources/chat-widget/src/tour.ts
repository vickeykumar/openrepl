// The first-run tour of the Genie panel: three or four short cards that point at Chat and Agent, the model picker, the starter chips and
// the remember note, once, the first time the panel is opened. It can be run again by typing /tour in the box. The steps are plain
// data here; the card is drawn by startTour and goes when the user skips or finishes. Nothing is sent anywhere.

export const TOUR_KEY = "genie-tour-v1";

export interface TourStep {
  target: string; // id of the element it points at
  title: string;
  text: string;
}

export const TOUR: readonly TourStep[] = [
  { target: "chat-widget__mode", title: "Chat or Agent", text: "Chat answers questions about your code. Agent does the task for you: it edits files, runs your code and uses the Preview, and it asks before anything risky." },
  { target: "chat-widget__model-btn", title: "Pick a model", text: "A quicker model is fine for questions. A stronger one is better for bigger tasks. Effort is how long it thinks before it answers." },
  { target: "chat-widget__starters", title: "Not sure what to ask?", text: "Tap a starter. It fills the box, and you can change it before you send." },
  { target: "chat-widget__input", title: "Teach it your project", text: "Type remember: and a sentence, for example remember: tests run with pytest. Genie keeps it in GENIE.md and every task starts with it. Type /tour to see this again." },
];

export function tourSeen(): boolean {
  try {
    return localStorage.getItem(TOUR_KEY) === "1";
  } catch (e) {
    return false;
  }
}

export function markTourSeen() {
  try {
    localStorage.setItem(TOUR_KEY, "1");
  } catch (e) {}
}

// "/tour" in the box
export function isTourCommand(msg: string): boolean {
  return /^\s*\/tour\s*$/i.test(String(msg || ""));
}

// the steps whose target is on the panel now (a mode switch that is hidden, or chips that are not shown, are left out)
export function stepsFor(root: Document | HTMLElement): TourStep[] {
  const find = (id: string) => (root instanceof Document ? root.getElementById(id) : root.ownerDocument!.getElementById(id));
  return TOUR.filter((s) => {
    const el = find(s.target) as HTMLElement | null;
    return !!el && !el.hidden && !(el.closest && el.closest("[hidden]"));
  });
}

let current: HTMLElement | null = null;

export function endTour() {
  if (current) {
    current.remove();
    current = null;
  }
  document.querySelectorAll(".cw-tour-target").forEach((e) => e.classList.remove("cw-tour-target"));
}

// Draws the tour above the box. Returns false when there is nothing to show.
export function startTour(): boolean {
  endTour();
  const steps = stepsFor(document);
  const anchor = document.getElementById("chat-widget__starters");
  const form = anchor ? anchor.parentElement : null;
  if (!steps.length || !form || !anchor) return false;
  const card = document.createElement("div");
  card.id = "chat-widget__tour";
  card.className = "cw-tour";
  card.setAttribute("role", "region");
  card.setAttribute("aria-label", "Quick tour of Genie");
  const count = document.createElement("div");
  count.className = "cw-tour__count";
  const title = document.createElement("div");
  title.className = "cw-tour__title";
  const text = document.createElement("div");
  text.className = "cw-tour__text";
  text.setAttribute("aria-live", "polite");
  const buttons = document.createElement("div");
  buttons.className = "cw-tour__buttons";
  const skip = document.createElement("button");
  skip.type = "button";
  skip.className = "cw-tour__skip";
  skip.textContent = "Skip";
  const back = document.createElement("button");
  back.type = "button";
  back.className = "cw-tour__back";
  back.textContent = "Back";
  const next = document.createElement("button");
  next.type = "button";
  next.className = "cw-tour__next";
  buttons.append(skip, back, next);
  card.append(count, title, text, buttons);
  form.insertBefore(card, anchor);
  current = card;
  let i = 0;
  const show = () => {
    document.querySelectorAll(".cw-tour-target").forEach((e) => e.classList.remove("cw-tour-target"));
    const s = steps[i];
    const el = document.getElementById(s.target);
    if (el) el.classList.add("cw-tour-target");
    count.textContent = i + 1 + " of " + steps.length;
    title.textContent = s.title;
    text.textContent = s.text;
    back.hidden = i === 0;
    next.textContent = i === steps.length - 1 ? "Done" : "Next";
    next.focus();
  };
  const finish = () => {
    markTourSeen();
    endTour();
    (document.getElementById("chat-widget__input") as HTMLTextAreaElement | null)?.focus();
  };
  next.addEventListener("click", () => {
    if (i >= steps.length - 1) finish();
    else {
      i++;
      show();
    }
  });
  back.addEventListener("click", () => {
    if (i > 0) {
      i--;
      show();
    }
  });
  skip.addEventListener("click", finish);
  card.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      ev.stopPropagation(); // the panel stays; only the tour goes
      finish();
    }
  });
  show();
  return true;
}
