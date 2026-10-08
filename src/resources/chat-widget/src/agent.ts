// Agent mode in the Genie panel: the loop that sends a task to the model step
// by step, carries out what it asks for in the editor, asks the user before it
// does, and shows each step. The protocol (what the model may ask for, and how
// its answer is checked) is in agent-protocol.ts; the limits (one unit a task,
// the steps, the hourly count, signed-in users only) are the server's
// (server/agent.go). See docs/agent-mode-design.md.

import {
  Action,
  CUT_OFF,
  MAX_OUTPUT,
  PAGE_ONLY_LANGUAGES,
  RunPhase,
  Scope,
  Status,
  StepResult,
  describe,
  endedAtTail,
  findLanguage,
  newOutput,
  parseStep,
  resultsMessage,
  riskOf,
  runPhase,
  scopeDetail,
  scopeOf,
  scopeQuestion,
} from "./agent-protocol";

type Msg = { role: string; content: string };
type AskResult =
  | { ok: true; content: string; token: string; step: [number, number] | null; cut: boolean }
  | { ok: false; error: string };

// What the panel shows of a task: its send button is a stop button while one
// runs, and waits while one winds down.
export type AgentState = "idle" | "running" | "stopping";

// What the panel gives the agent. The agent never reaches into the panel's
// own state.
export interface AgentHost {
  url(): string;
  headers(): Headers;
  modelFields(): Record<string, any>; // the chosen model, with room for a whole file
  ideContext(): Msg; // the editor and the terminal, as Genie sees them
  terminalText(): string;
  // The terminal of the active tab, as an object to compare: Run closes the
  // terminal and opens a new one, so a different object means the run has
  // started. null where the page cannot tell.
  terminal(): unknown;
  // Types into the active terminal as if the user did; false if it takes no input.
  terminalType(data: string): boolean;
  // The dots that show Genie is working, as in chat; false removes them.
  thinking(on: boolean): void;
  userSaid(text: string): Promise<void>;
  genieSaid(text: string): Promise<void>;
  messages(): HTMLElement; // where the cards go
  maxSteps(): number;
  uid(): string; // the tag of the user in the conversation
}

const REQUEST_TIMEOUT_MS = 120000; // a step that takes longer than this is given up (the proxy's own limit is near it)
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// how long the new terminal of a run may take to appear, where the page cannot
// tell the terminals apart (it is opened half a second after Run)
const RUN_START_MS = 1500;
// how long the starter code of a language may take to arrive
const LANGUAGE_WAIT_MS = 6000;

// the page elements an action works on, to show where Genie is
const TARGETS: Record<string, string> = {
  editor: ".editor-body",
  run: ".run-split",
  language: "#optionlist",
  output: "#terminal-div",
};

type Phase = "waiting" | "active" | "done" | "failed" | "stopped";
const MARKS: Record<Phase, string> = { waiting: "○", active: "◔", done: "✓", failed: "✗", stopped: "–" };

class StepLine {
  root = el("li", "cw-agent__step cw-agent__step--waiting");
  private mark = el("span", "cw-agent__mark", MARKS.waiting);
  private text = el("span", "cw-agent__what");
  constructor(label: string) {
    this.text.textContent = label;
    this.root.append(this.mark, this.text);
  }
  set(phase: Phase, label?: string) {
    this.root.className = "cw-agent__step cw-agent__step--" + phase;
    this.mark.textContent = MARKS[phase];
    if (label !== undefined) this.text.textContent = label;
  }
}

export class AgentRunner {
  running = false; // a task is in flight, until its last line has run; the panel's box waits for it
  private stopped = false; // Stop was pressed, or the panel closed: the task winds down
  private timedOut = false;
  private ctl: AbortController | null = null;
  private grants = new Set<Scope>(); // allowed for the rest of the session (until the page closes)
  private pendingAnswer: ((a: "once" | "session" | "deny") => void) | null = null;
  // The run this task started, if any: the terminal that was there when Run was
  // pressed (the run's own terminal is another one), and when.
  private ran = false;
  private runTerm: unknown = null;
  private runAt = 0;
  private card: HTMLElement | null = null;
  private list: HTMLElement | null = null;
  private header: HTMLElement | null = null;
  private stopBtn: HTMLButtonElement | null = null;

  constructor(private host: AgentHost, private onState: (state: AgentState) => void) {}

  // ---- the task ----------------------------------------------------------------

  async start(task: string): Promise<void> {
    if (this.running) return;
    this.running = true;
    this.stopped = false;
    this.timedOut = false;
    this.onState("running");
    this.ctl = new AbortController();
    this.ran = false;
    let finished = false;
    // Everything that can fail is inside the try: whatever happens, the finally
    // gives the box back. A task that dies before it starts must not leave the
    // send button disabled.
    try {
      await this.host.userSaid(task);
      this.drawCard(); // after the message it belongs to: the newest is shown last
      const messages: Msg[] = [{ role: "user", content: `[user-${this.host.uid()}] ${task}` }];
      let token = "";
      let badAnswers = 0;
      const max = this.host.maxSteps();
      for (let n = 1; n <= max && !finished && !this.stopped; n++) {
        const reading = this.addLine(n === 1 ? "Reading your editor and terminal" : "Thinking about the next step");
        reading.set("active");
        this.setHeader(n, max);
        const res = await this.ask(messages, token);
        if (!res.ok) {
          reading.set("failed", res.error);
          await this.host.genieSaid(res.error);
          break;
        }
        token = res.token || token;
        if (res.step) this.setHeader(res.step[0], res.step[1]);
        reading.set("done", n === 1 ? "Read your editor and terminal" : "Thought about the next step");

        const parsed = parseStep(res.content);
        if (!parsed.ok) {
          messages.push({ role: "assistant", content: res.content });
          if (++badAnswers >= 2) {
            reading.set("failed", res.cut ? "Genie's answer was too long" : "Genie's answer could not be read");
            await this.host.genieSaid(
              res.cut
                ? "What I wanted to write was too long for one answer, so I stopped. Ask for a smaller change, or for one part at a time."
                : "I could not put together a usable step, so I stopped. Try the task again, or ask it differently."
            );
            break;
          }
          reading.set("failed", res.cut ? "Genie's answer was too long; asking again" : "Genie's answer could not be read; asking again");
          const why = res.cut ? CUT_OFF : parsed.error;
          messages.push({ role: "user", content: JSON.stringify({ step_results: [], not_done: [why + ". Answer with one JSON object as described."] }) });
          continue;
        }
        const step = parsed.step;
        messages.push({ role: "assistant", content: res.content });
        if (step.say) await this.host.genieSaid(step.say);
        const results: StepResult[] = [];
        for (const a of step.actions) {
          if (this.stopped) break;
          results.push(await this.run(a));
        }
        if (this.stopped) break;
        if (step.done) finished = true;
        else messages.push({ role: "user", content: resultsMessage(results, step.dropped) });
        if (!finished && n === max) {
          await this.host.genieSaid("I used all " + max + " steps of this task. You can ask me to carry on.");
        }
      }
    } catch (e: any) {
      if (!this.stopped) {
        console.error("agent:", e);
        try {
          await this.host.genieSaid("Something went wrong, so I stopped.");
        } catch (e2) {
          console.error("agent:", e2);
        }
      }
    } finally {
      try {
        this.host.thinking(false);
        this.endCard(finished);
        this.clearHighlight();
      } catch (e) {
        console.error("agent:", e);
      }
      this.running = false;
      this.stopped = false;
      this.ctl = null;
      this.onState("idle");
    }
  }

  // Stop: the request in flight is cancelled, a question to the user is
  // answered "no", an open review is closed (what is pending counts as rejected).
  // The task is over when start() returns; running stays true until then.
  stop() {
    if (!this.running || this.stopped) return;
    this.stopped = true;
    this.onState("stopping");
    if (this.ctl) this.ctl.abort();
    if (this.pendingAnswer) this.pendingAnswer("deny");
    const review = (window as any).GenieReview;
    if (review && review.isOpen && review.isOpen()) review.close();
    this.clearHighlight();
  }

  // ---- one request ------------------------------------------------------------

  private async ask(messages: Msg[], token: string): Promise<AskResult> {
    const body = {
      ...this.host.modelFields(),
      context: "agent",
      agent_task: token,
      messages: [...messages, this.host.ideContext()],
    };
    // a model that never answers must not keep the task, and the box, for ever
    const timer = setTimeout(() => {
      this.timedOut = true;
      if (this.ctl) this.ctl.abort();
    }, REQUEST_TIMEOUT_MS);
    this.host.thinking(true);
    try {
      // raced against the abort: Stop and the timeout end the wait even if the
      // request itself does not give way
      return await Promise.race([this.exchange(body), this.aborted()]);
    } catch (e: any) {
      if (this.timedOut && !this.stopped) return { ok: false, error: "Genie did not answer in time, so I stopped. Try again." };
      throw e;
    } finally {
      clearTimeout(timer);
      this.host.thinking(false);
    }
  }

  private aborted(): Promise<never> {
    return new Promise<never>((_, reject) => {
      const fail = () => {
        const e = new Error("aborted");
        e.name = "AbortError";
        reject(e);
      };
      const signal = this.ctl ? this.ctl.signal : null;
      if (!signal || signal.aborted) fail();
      else signal.addEventListener("abort", fail, { once: true });
    });
  }

  private async exchange(body: Record<string, any>): Promise<AskResult> {
    const res = await fetch(this.host.url(), {
      method: "POST",
      headers: this.host.headers(),
      body: JSON.stringify(body),
      signal: this.ctl ? this.ctl.signal : undefined,
    });
    if (!res.ok) {
      let message = "Genie could not answer (" + res.status + ").";
      let code = "";
      try {
        const e = await res.json();
        if (e && e.error) {
          code = String(e.error.code || "");
          if (typeof e.error.message === "string") message = e.error.message;
        }
      } catch (e) {
        // keep the generic message
      }
      if (code === "agent_step_limit") message = "The task has used all its steps.";
      return { ok: false, error: message };
    }
    const m = (res.headers.get("X-OpenREPL-Agent-Step") || "").match(/^(\d+)\/(\d+)$/);
    const data: any = await res.json();
    const content = data && data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content;
    if (typeof content !== "string") return { ok: false, error: "Genie sent no answer." };
    return {
      ok: true,
      content,
      token: res.headers.get("X-OpenREPL-Agent-Task") || "",
      step: m ? [Number(m[1]), Number(m[2])] : null,
      cut: data.choices[0].finish_reason === "length", // the cap on the answer ended it: the JSON is not whole
    };
  }

  // ---- one action -------------------------------------------------------------

  private async run(a: Action): Promise<StepResult> {
    const line = this.addLine(describe(a));
    const scope = scopeOf(a);
    // Nothing to ask about: the language is the one in use already, so nothing
    // would change (perform says so to the model)
    const noChange = a.type === "set_language" && this.isCurrentLanguage(a.language || "");
    // a line that can do harm is asked about every time, whatever was allowed
    const risk = a.type === "terminal_type" ? riskOf(a.text || "") : "";
    if (scope && !noChange) {
      if (risk || !this.grants.has(scope)) line.set("active", "Waiting for your answer: " + describe(a));
      const allowed = await this.permission(scope, a, risk);
      if (this.stopped) {
        line.set("stopped", "Stopped");
        return { type: a.type, status: "skipped", detail: "the user stopped the task" };
      }
      if (!allowed) {
        line.set("failed", describe(a) + ": not allowed");
        return { type: a.type, status: "denied", detail: "the user did not allow " + scope + " actions" };
      }
    }
    line.set("active");
    const target =
      scope === "language" ? "language" : a.type === "read_output" || scope === "terminal" ? "output" : scope === "run" ? "run" : "editor";
    this.highlight(TARGETS[target]);
    let result: StepResult;
    try {
      result = await this.perform(a, line);
    } catch (e: any) {
      if (this.stopped) result = { type: a.type, status: "skipped", detail: "the user stopped the task" };
      else {
        console.error("agent action:", e);
        result = { type: a.type, status: "failed", detail: String((e && e.message) || e).slice(0, 200) };
      }
    }
    await sleep(a.type === "terminal_type" ? 700 : 350); // long enough to see where it was (and the line that was typed)
    this.clearHighlight();
    const phase: Phase = result.status === "failed" || result.status === "denied" || result.status === "rejected" ? "failed" : result.status === "skipped" ? "stopped" : "done";
    line.set(phase, labelFor(a, result));
    return result;
  }

  private async perform(a: Action, line: StepLine): Promise<StepResult> {
    const review = (window as any).GenieReview;
    switch (a.type) {
      case "editor_write":
      case "editor_insert": {
        if (!review) return { type: a.type, status: "failed", detail: "the editor is not available" };
        const r =
          a.type === "editor_write"
            ? await review.proposeAsync(a.text, { title: "Genie wants to replace the file:" })
            : await review.proposeInsertAsync(a.text, { title: "Genie wants to insert" });
        if (r.error) return { type: a.type, status: "failed", detail: r.error };
        if (r.total === 0) return { type: a.type, status: "done", detail: "the editor already had this text" };
        const status: Status = r.accepted === r.total ? "accepted" : r.accepted > 0 ? "partly_accepted" : "rejected";
        return { type: a.type, status, accepted: r.accepted, rejected: r.total - r.accepted };
      }
      case "set_language": {
        const picker = this.picker();
        if (!picker) return { type: a.type, status: "failed", detail: "there is no language picker" };
        const opts = Array.from(picker.options).map((o) => ({ value: o.value, text: o.text }));
        const value = findLanguage(opts, a.language || "");
        if (!value) return { type: a.type, status: "failed", detail: "no such language; the picker has: " + opts.map((o) => o.text).join(", ") };
        // already there: a change event would only start the terminal again and
        // put the starter code over the user's code
        if (picker.value === value) return { type: a.type, status: "done", detail: "it is the language already; nothing changed" };
        picker.value = value;
        picker.dispatchEvent(new Event("change", { bubbles: true }));
        this.ran = false; // the terminal is a new one, of the language: what ran before is gone
        // The page now opens the language's terminal and fetches its starter
        // code, which it puts in the editor a moment after it arrives. Writing
        // before that would be overwritten, so wait for it (the page says which
        // language's starter code is in, except on the practice page).
        const w = window as any;
        const told = "demoLoadedForLang" in w && !window.location.pathname.includes("practice");
        const deadline = Date.now() + LANGUAGE_WAIT_MS;
        await sleep(RUN_START_MS);
        while (told && !this.stopped && w.demoLoadedForLang !== value && Date.now() < deadline) await sleep(150);
        if (this.stopped) throw new Error("stopped");
        return { type: a.type, status: "done", detail: "the editor now holds the starter code of the language, and the terminal started again" };
      }
      case "run":
      case "debug": {
        const away = (window as any).awayWaitMs;
        if (typeof away === "function" && away() > 0) return { type: a.type, status: "failed", detail: "the execution node is away; try again in a moment" };
        if (this.pageOnly()) return { type: a.type, status: "failed", detail: this.pageOnly() };
        const fn = (window as any)[a.type === "run" ? "CompileandRun" : "RunandDebug"];
        if (typeof fn !== "function") return { type: a.type, status: "failed", detail: "it is not available here" };
        this.runTerm = this.host.terminal(); // before Run: Run closes it and opens the run's own
        this.runAt = Date.now();
        this.ran = true;
        fn();
        return { type: a.type, status: "done", detail: "started; read_output returns what it printed" };
      }
      case "terminal_type": {
        if (this.pageOnly()) return { type: a.type, status: "failed", detail: this.pageOnly() };
        const before = this.host.terminalText();
        // a program that is over takes no input; the line would go nowhere
        if (endedAtTail(before)) {
          return { type: a.type, status: "failed", detail: "the program in the terminal has ended, so a line typed now goes nowhere; use run to start it again, or set_language" };
        }
        const wait = a.waitSeconds || 3;
        if (!this.host.terminalType((a.text || "") + "\r")) {
          return { type: a.type, status: "failed", detail: "the terminal takes no input right now (it is closed or not connected yet)" };
        }
        this.host.thinking(true);
        let quiet: boolean;
        try {
          await sleep(250);
          quiet = await this.settle(Date.now() + wait * 1000, 1000, 700);
        } finally {
          this.host.thinking(false);
        }
        if (this.stopped) throw new Error("stopped");
        return {
          type: a.type,
          status: "done",
          output: newOutput(before, this.host.terminalText()),
          detail: quiet ? undefined : "the terminal was still printing after " + wait + " seconds; read_output waits for more, terminal_interrupt stops the program",
        };
      }
      case "terminal_interrupt": {
        const before = this.host.terminalText();
        if (!this.host.terminalType("\x03")) {
          return { type: a.type, status: "failed", detail: "the terminal takes no input right now (it is closed or not connected yet)" };
        }
        this.host.thinking(true);
        try {
          await sleep(250);
          await this.settle(Date.now() + 4000, 800, 500);
        } finally {
          this.host.thinking(false);
        }
        if (this.stopped) throw new Error("stopped");
        return { type: a.type, status: "done", output: newOutput(before, this.host.terminalText()) };
      }
      case "read_output": {
        if (this.pageOnly()) return { type: a.type, status: "failed", detail: this.pageOnly() };
        const wait = a.waitSeconds || 5;
        const deadline = Date.now() + wait * 1000;
        let phase = this.phase();
        let idle = false; // the program printed something and went quiet
        this.host.thinking(true);
        try {
          if (phase === "none") {
            // nothing was started by this task: let what is printing finish
            await this.settle(deadline, 1000, 400);
          } else {
            let last = "";
            let changedAt = Date.now();
            let sawOutput = false;
            while (!this.stopped && (phase === "starting" || phase === "running") && Date.now() < deadline) {
              await sleep(300);
              phase = this.phase();
              if (phase !== "running") continue;
              const text = this.host.terminalText();
              if (text !== last) {
                last = text;
                changedAt = Date.now();
                sawOutput = text.trim() !== "";
              } else if (sawOutput && Date.now() - changedAt > 2500) {
                idle = true; // most likely it asks for input
                break;
              }
            }
          }
        } finally {
          this.host.thinking(false);
        }
        if (this.stopped) throw new Error("stopped");
        // the run's terminal never came: what is on the screen is not its output
        if (phase === "starting") return { type: a.type, status: "failed", detail: "the program had not started after " + wait + " seconds; run it again" };
        const text = this.host.terminalText().trim();
        const out = text.length > MAX_OUTPUT ? "..." + text.slice(-MAX_OUTPUT) : text;
        return {
          type: a.type,
          status: "done",
          output: out,
          detail: idle
            ? "the program printed this and then went quiet: it is probably waiting for input (answer with terminal_type, or stop it with terminal_interrupt)"
            : phase === "running"
            ? "the program had not finished after " + wait + " seconds"
            : undefined,
        };
      }
      default:
        line.set("done");
        return { type: a.type, status: "done" };
    }
  }

  private isCurrentLanguage(name: string): boolean {
    const picker = this.picker();
    if (!picker) return false;
    const value = findLanguage(Array.from(picker.options).map((o) => ({ value: o.value, text: o.text })), name);
    return value !== null && picker.value === value;
  }

  private picker(): HTMLSelectElement | null {
    return document.getElementById("optionlist") as HTMLSelectElement | null;
  }

  // Why Genie cannot run the chosen language, or "" if it can.
  private pageOnly(): string {
    const picker = this.picker();
    if (!picker || PAGE_ONLY_LANGUAGES.indexOf(picker.value) < 0) return "";
    return "this language runs in a console inside the page, which Genie can neither run nor read; switch to another one (NodeJS for JavaScript)";
  }

  // Where the run this task started is (runPhase in agent-protocol.ts).
  private phase(): RunPhase {
    return runPhase({
      ran: this.ran,
      term: this.host.terminal(),
      runTerm: this.runTerm,
      sinceRunMs: Date.now() - this.runAt,
      startMs: RUN_START_MS,
      text: this.host.terminalText(),
    });
  }

  // Waits until the terminal has not changed for quietMs (and at least minMs
  // have passed), or its program is over, or the deadline. True if it was quiet.
  private async settle(deadline: number, quietMs: number, minMs: number): Promise<boolean> {
    const start = Date.now();
    let last = this.host.terminalText();
    let changedAt = start;
    while (!this.stopped) {
      await sleep(250);
      const now = Date.now();
      const text = this.host.terminalText();
      if (text !== last) {
        last = text;
        changedAt = now;
      }
      if (endedAtTail(text)) return true;
      if (now - changedAt >= quietMs && now - start >= minMs) return true;
      if (now >= deadline) return false;
    }
    return false;
  }

  // ---- permission ---------------------------------------------------------------

  // risk: why a terminal line is risky, "" if it is not. A risky line is asked
  // about every time and can only be allowed once.
  private permission(scope: Scope, a: Action, risk: string = ""): Promise<boolean> {
    if (!risk && this.grants.has(scope)) return Promise.resolve(true);
    return new Promise<boolean>((resolve) => {
      const box = el("div", "cw-agent__ask" + (risk ? " cw-agent__ask--risk" : ""));
      box.setAttribute("role", "group");
      const title = risk ? "Genie wants to type a risky line in your terminal" : scopeQuestion(scope);
      box.setAttribute("aria-label", title);
      box.appendChild(el("div", "cw-agent__ask-title", title));
      box.appendChild(el("p", "cw-agent__ask-text", scopeDetail(scope, a)));
      if (a.type === "terminal_type") {
        // exactly what will be typed, as plain text
        box.appendChild(el("pre", "cw-agent__cmd", a.text || ""));
      }
      if (risk) box.appendChild(el("p", "cw-agent__ask-warn", "Asked every time because it " + risk + "."));
      const buttons = el("div", "cw-agent__ask-buttons");
      const done = (answer: "once" | "session" | "deny") => {
        this.pendingAnswer = null;
        box.remove();
        if (answer === "session") this.grants.add(scope);
        resolve(answer !== "deny");
      };
      const mk = (label: string, cls: string, answer: "once" | "session" | "deny") => {
        const b = el("button", cls, label);
        b.type = "button";
        b.addEventListener("click", () => done(answer));
        return b;
      };
      buttons.append(mk("Allow once", "is-primary", "once"));
      if (!risk) buttons.append(mk("Allow for this session", "", "session"));
      buttons.append(mk("Deny", "", "deny"));
      box.appendChild(buttons);
      if (scope === "terminal" && !risk) {
        box.appendChild(el("p", "cw-agent__ask-hint", "Lines that can delete or change things are still asked about every time."));
      }
      this.pendingAnswer = done;
      if (this.list && this.list.parentElement) this.list.parentElement.appendChild(box);
      else this.host.messages().prepend(box);
      (buttons.firstChild as HTMLElement).focus();
      // the list is scrolled to the newest message; make sure the question is seen
      try {
        box.scrollIntoView({ block: "nearest" });
      } catch (e) {
        // scrolling is not essential
      }
    });
  }

  // ---- the task card ---------------------------------------------------------------

  private drawCard() {
    const card = el("div", "cw-agent");
    card.setAttribute("role", "region");
    card.setAttribute("aria-label", "Agent task");
    const head = el("div", "cw-agent__head");
    const title = el("span", "cw-agent__title", "Agent task");
    this.header = el("span", "cw-agent__count", "");
    this.stopBtn = el("button", "cw-agent__stop", "Stop");
    this.stopBtn.type = "button";
    this.stopBtn.addEventListener("click", () => this.stop());
    head.append(title, this.header, this.stopBtn);
    this.list = el("ol", "cw-agent__steps");
    this.list.setAttribute("aria-live", "polite");
    card.append(head, this.list);
    this.card = card;
    this.host.messages().prepend(card);
  }

  private setHeader(step: number, max: number) {
    if (this.header) this.header.textContent = "Step " + step + " of " + max;
  }

  private addLine(label: string): StepLine {
    const line = new StepLine(label);
    if (this.list) this.list.appendChild(line.root);
    return line;
  }

  private endCard(finished: boolean) {
    if (!this.card) return;
    if (this.stopBtn) this.stopBtn.remove();
    this.card.classList.add("cw-agent--ended");
    if (this.header) this.header.textContent = finished ? "Finished" : this.header.textContent + " · ended";
    this.list &&
      Array.from(this.list.children).forEach((li) => {
        if (li.classList.contains("cw-agent__step--active") || li.classList.contains("cw-agent__step--waiting")) {
          li.className = "cw-agent__step cw-agent__step--stopped";
          const mark = li.querySelector(".cw-agent__mark");
          if (mark) mark.textContent = MARKS.stopped;
        }
      });
    this.card = this.list = this.header = this.stopBtn = null;
  }

  // ---- showing where Genie is -----------------------------------------------------

  private highlight(selector: string) {
    this.clearHighlight();
    document.querySelectorAll(selector).forEach((e) => e.classList.add("genie-agent-target"));
  }

  private clearHighlight() {
    document.querySelectorAll(".genie-agent-target").forEach((e) => e.classList.remove("genie-agent-target"));
  }
}

function labelFor(a: Action, r: StepResult): string {
  switch (r.status) {
    case "accepted":
      return describe(a) + ": " + (r.accepted === 1 ? "1 change accepted" : r.accepted + " changes accepted");
    case "partly_accepted":
      return describe(a) + ": " + r.accepted + " accepted, " + r.rejected + " rejected";
    case "rejected":
      return describe(a) + ": you rejected it";
    case "denied":
      return describe(a) + ": not allowed";
    case "failed":
      return describe(a) + ": failed" + (r.detail ? " (" + r.detail + ")" : "");
    default:
      return describe(a);
  }
}
