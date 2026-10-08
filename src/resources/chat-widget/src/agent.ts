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
  MAX_TABS,
  PAGE_ONLY_LANGUAGES,
  RunPhase,
  Scope,
  Status,
  StepResult,
  describe,
  endedAtTail,
  filesAskReason,
  splitPath,
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
  | { ok: true; content: string; token: string; step: [number, number] | null; cut: boolean; context: string | null }
  | { ok: false; error: string };

// The Files panel, as the page offers it (js/src/page/07-file-browser.js,
// window.FileBrowser). Paths are relative to the home directory; "" is the home
// directory itself.
export type FilesResult = { ok: true; detail?: string; entries?: string[]; truncated?: boolean } | { ok: false; error: string };
export interface FilesHost {
  ready(): boolean;
  reveal(): void;
  info(path: string): { exists: boolean; type?: "file" | "folder"; protected?: boolean; hidden?: boolean };
  current(): string; // the file that is open in the editor, "" if none
  buffer(): "cut" | "copy" | null; // what a cut or a copy left in the panel
  list(path: string): FilesResult;
  open(path: string): Promise<FilesResult>;
  create(path: string, kind: "file" | "folder"): Promise<FilesResult>;
  save(): Promise<FilesResult>;
  rename(path: string, name: string): Promise<FilesResult>;
  cut(path: string): FilesResult;
  copy(path: string): FilesResult;
  paste(to: string): Promise<FilesResult>;
  move(path: string, to: string): Promise<FilesResult>;
  remove(path: string): Promise<FilesResult>;
}

// A task while it runs, and how a step of it leaves the loop.
type TaskState = { messages: Msg[]; token: string; badAnswers: number; max: number };
type StepOutcome = "next" | "finished" | "end";

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
  // The terminal tabs: how many are open, which one is shown (1 based), and
  // whether the one shown is connected and takes input.
  tabs(): { count: number; active: number };
  terminalReady(): boolean;
  reconnect(): boolean; // restarts the terminal of the shown tab; false if the page cannot
  addTab(): boolean; // presses the "+" of the tabs
  selectTab(n: number): boolean;
  files: FilesHost;
  closeTab(n: number): boolean; // presses the x of tab n
  userSaid(text: string): Promise<void>;
  // context: the X-OpenREPL-Context header of the step, which lists the site notes it was answered from
  genieSaid(text: string, context?: string | null): Promise<void>;
  // Something the panel tells the user about the task (an error, "I stopped"):
  // shown like Genie's words, but not kept in the conversation, which is what
  // the model reads later. An error is not something Genie said.
  note(text: string): Promise<void>;
  messages(): HTMLElement; // where the cards go
  maxSteps(): number;
  uid(): string; // the tag of the user in the conversation
  // The REPL of the language in use, as the picker names it ("gointerpreter").
  repl?(): string;
  // What the answer of a step says the user has left (the X-OpenREPL-Usage header).
  usage?(raw: string | null): void;
  // The three below let the panel show on its button how a task goes while the
  // panel is closed (a task goes on behind a closed panel). All are optional.
  // The task waits for the user (a question, a change to accept), or no longer does.
  attention?(on: boolean): void;
  // Step n of max has begun.
  progress?(n: number, max: number): void;
  // How the task ended; "stopped" is the user's own Stop.
  ended?(outcome: "done" | "failed" | "stopped"): void;
}

const PANEL_ID = "chat-widget__container"; // the Genie panel (index.ts)
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
// how long a restarted terminal or a new tab may take to connect
const NEW_TERMINAL_WAIT_MS = 12000;

// the page elements an action works on, to show where Genie is
const TARGETS: Record<string, string> = {
  editor: ".editor-body",
  run: ".run-split",
  language: "#optionlist",
  output: "#terminal-div",
  tabs: "#terminal-tabs",
  files: "#file-browser",
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
  private stopped = false; // Stop was pressed: the task winds down
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
      const task_: TaskState = {
        messages: [{ role: "user", content: `[user-${this.host.uid()}] ${task}` }],
        token: "",
        badAnswers: 0,
        max: this.host.maxSteps(),
      };
      // No `break` or `continue` in a loop that awaits, here or anywhere in the
      // widget: the build tool (microbundle rewrites async functions into promise
      // chains) has dropped a `continue` and emitted an undeclared helper for a
      // `break`. A step says how the loop goes on; test/async-loops.test.mjs
      // refuses the pattern, test/agent-compiled.test.mjs runs this loop as built.
      let go: StepOutcome = "next";
      for (let n = 1; n <= task_.max && go === "next" && !this.stopped; n++) {
        go = await this.step(n, task_);
      }
      finished = go === "finished";
    } catch (e: any) {
      if (!this.stopped) {
        console.error("agent:", e);
        try {
          await this.host.note("Something went wrong, so I stopped.");
        } catch (e2) {
          console.error("agent:", e2);
        }
      }
    } finally {
      // Stop is the user's own doing; anything else that did not finish (an
      // error, a timeout, a second unreadable answer) is a failure
      const outcome = finished ? "done" : this.stopped && !this.timedOut ? "stopped" : "failed";
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
      try {
        if (this.host.ended) this.host.ended(outcome);
      } catch (e) {
        console.error("agent:", e);
      }
      this.onState("idle");
    }
  }

  // One step of a task: ask the model, check its answer, carry out what it asks
  // for. "next": go on with another step; "finished": the task is done; "end":
  // it is over without being done (an error, a second unreadable answer, Stop).
  private async step(n: number, t: TaskState): Promise<StepOutcome> {
    const reading = this.addLine(n === 1 ? "Reading your editor and terminal" : "Thinking about the next step");
    reading.set("active");
    this.setHeader(n, t.max);
    const res = await this.ask(t.messages, t.token);
    if (!res.ok) {
      reading.set("failed", res.error);
      await this.host.note(res.error);
      return "end";
    }
    t.token = res.token || t.token;
    if (res.step) this.setHeader(res.step[0], res.step[1]);
    reading.set("done", n === 1 ? "Read your editor and terminal" : "Thought about the next step");

    const parsed = parseStep(res.content);
    if (!parsed.ok) {
      t.messages.push({ role: "assistant", content: res.content });
      t.badAnswers++;
      if (t.badAnswers >= 2) {
        reading.set("failed", res.cut ? "Genie's answer was too long" : "Genie's answer could not be read");
        await this.host.note(
          res.cut
            ? "What I wanted to write was too long for one answer, so I stopped. Ask for a smaller change, or for one part at a time."
            : "I could not put together a usable step, so I stopped. Try the task again, or ask it differently."
        );
        return "end";
      }
      reading.set("failed", res.cut ? "Genie's answer was too long; asking again" : "Genie's answer could not be read; asking again");
      const why = res.cut ? CUT_OFF : parsed.error;
      t.messages.push({ role: "user", content: JSON.stringify({ step_results: [], not_done: [why + ". Answer with one JSON object as described."] }) });
      return "next";
    }
    const step = parsed.step;
    t.messages.push({ role: "assistant", content: res.content });
    if (step.say) await this.host.genieSaid(step.say, res.context);
    const results: StepResult[] = [];
    for (let i = 0; i < step.actions.length && !this.stopped; i++) {
      results.push(await this.run(step.actions[i]));
    }
    if (this.stopped) return "end";
    if (step.done) return "finished";
    t.messages.push({ role: "user", content: resultsMessage(results, step.dropped) });
    if (n === t.max) await this.host.note("I used all " + t.max + " steps of this task. You can ask me to carry on.");
    return "next";
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
      // the REPL in use, by name: the server adds what it knows about it
      repl: this.host.repl ? this.host.repl() : "",
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
    // what the user has left after this step (null: the panel asks for it)
    if (this.host.usage) this.host.usage(res.headers.get("X-OpenREPL-Usage"));
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
      context: res.headers.get("X-OpenREPL-Context"),
    };
  }

  // ---- one action -------------------------------------------------------------

  private async run(a: Action): Promise<StepResult> {
    // a new tab in the language already chosen changes nothing in the editor
    if (a.type === "terminal_new_tab" && a.language && this.isCurrentLanguage(a.language)) a = { type: a.type };
    const line = this.addLine(describe(a));
    // nothing to ask about when it would be refused anyway
    if (a.type === "terminal_close_tab") {
      const why = this.closeProblem(a);
      if (why) {
        line.set("failed", describe(a) + ": not possible");
        return { type: a.type, status: "failed", detail: why };
      }
    }
    if (scopeOf(a) === "files") {
      const why = this.filesProblem(a);
      if (why) {
        line.set("failed", describe(a) + ": not possible");
        return { type: a.type, status: "failed", detail: why };
      }
    }
    const scope = scopeOf(a);
    // Nothing to ask about: the language is the one in use already, so nothing
    // would change (perform says so to the model)
    const noChange = a.type === "set_language" && this.isCurrentLanguage(a.language || "");
    // a line that can do harm is asked about every time, whatever was allowed
    const risk =
      a.type === "terminal_type"
        ? riskOf(a.text || "")
        : a.type === "terminal_close_tab"
        ? "closes the tab and stops what is running in it"
        : scopeOf(a) === "files"
        ? filesAskReason(a, this.host.files.buffer())
        : "";
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
      scope === "files"
        ? "files"
        : a.type === "terminal_new_tab" || a.type === "terminal_select_tab"
        ? "tabs"
        : scope === "language"
        ? "language"
        : a.type === "read_output" || scope === "terminal"
        ? "output"
        : scope === "run"
        ? "run"
        : "editor";
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
            ? await this.needsYou<any>(review.proposeAsync(a.text, { title: "Genie wants to replace the file:" }))
            : await this.needsYou<any>(review.proposeInsertAsync(a.text, { title: "Genie wants to insert" }));
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
        await this.waitForStarterCode(value, RUN_START_MS);
        return { type: a.type, status: "done", detail: this.afterLanguage() };
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
      case "terminal_reconnect": {
        const away = (window as any).awayWaitMs;
        if (typeof away === "function" && away() > 0) return { type: a.type, status: "failed", detail: "the execution node is away; try again in a moment" };
        if (this.pageOnly()) return { type: a.type, status: "failed", detail: this.pageOnly() };
        const before = this.host.terminal();
        if (!this.host.reconnect()) return { type: a.type, status: "failed", detail: "the terminal cannot be restarted here" };
        this.ran = false; // the program that was running is stopped
        const out = await this.awaitNewTerminal(before);
        if (!out.ok) return { type: a.type, status: "failed", detail: out.detail };
        return { type: a.type, status: "done", output: out.text, detail: "the terminal started again" };
      }
      case "terminal_new_tab": {
        const tabs = this.host.tabs();
        if (tabs.count >= MAX_TABS) {
          return { type: a.type, status: "failed", detail: "all " + MAX_TABS + " terminal tabs are open; closing one is up to the user" };
        }
        const before = this.host.terminal();
        let value = "";
        const pickerWas = this.picker() ? this.picker()!.value : "";
        if (a.language) {
          const picker = this.picker();
          const opts = picker ? Array.from(picker.options).map((o) => ({ value: o.value, text: o.text })) : [];
          const found = findLanguage(opts, a.language);
          if (!picker || !found) return { type: a.type, status: "failed", detail: "no such language; the picker has: " + opts.map((o) => o.text).join(", ") };
          if (PAGE_ONLY_LANGUAGES.indexOf(found) >= 0) return { type: a.type, status: "failed", detail: this.pageOnly(found) };
          value = found;
          // the new tab starts in the language the picker holds, so set it first
          // and the tab starts once; the change event follows for the editor
          picker.value = value;
        }
        if (!this.host.addTab()) {
          // the picker was set for the tab that did not come: put it back, or Run
          // would use a language the terminal does not have
          if (value && this.picker()) this.picker()!.value = pickerWas;
          return { type: a.type, status: "failed", detail: "the tab could not be opened" };
        }
        this.ran = false;
        if (value) {
          const picker = this.picker()!;
          // silent: the terminal that is shown is the new one and already runs it
          picker.dispatchEvent(new CustomEvent("change", { bubbles: true, detail: { silent: true } }));
          await this.waitForStarterCode(value, 0);
        }
        const out = await this.awaitNewTerminal(before);
        if (!out.ok) return { type: a.type, status: "failed", detail: out.detail };
        const now = this.host.tabs();
        return {
          type: a.type,
          status: "done",
          output: out.text,
          detail:
            "now on tab " + now.active + " of " + now.count + (value ? ", in " + this.pickerLanguage() + "; " + this.afterLanguage() : "") + "; Run uses the language in the picker (" + this.pickerLanguage() + ")",
        };
      }
      case "files_list":
      case "files_open":
      case "files_new":
      case "files_save":
      case "files_rename":
      case "files_cut":
      case "files_copy":
      case "files_paste":
      case "files_move":
      case "files_delete":
        return await this.fileAction(a);
      case "terminal_close_tab": {
        const why = this.closeProblem(a); // asked again: the tabs may have changed while the user decided
        if (why) return { type: a.type, status: "failed", detail: why };
        const n = a.tab || 0;
        if (!this.host.closeTab(n)) return { type: a.type, status: "failed", detail: "the tab could not be closed" };
        this.ran = false; // the program that ran in it is gone with it
        await sleep(600); // the page makes another tab the one shown a moment after
        const now = this.host.tabs();
        return {
          type: a.type,
          status: "done",
          detail: "tab closed; now on tab " + now.active + " of " + now.count + "; Run uses the language in the picker (" + this.pickerLanguage() + ")",
        };
      }
      case "terminal_select_tab": {
        const tabs = this.host.tabs();
        const n = a.tab || 0;
        if (n < 1 || n > tabs.count) return { type: a.type, status: "failed", detail: "there are " + tabs.count + " terminal tab" + (tabs.count === 1 ? "" : "s") + ", not " + n };
        if (n !== tabs.active && !this.host.selectTab(n)) return { type: a.type, status: "failed", detail: "the tab could not be selected" };
        await sleep(300);
        this.ran = false;
        const text = this.host.terminalText().trim();
        return {
          type: a.type,
          status: "done",
          output: text.length > MAX_OUTPUT ? "..." + text.slice(-MAX_OUTPUT) : text,
          detail: "now on tab " + n + " of " + tabs.count + "; Run uses the language in the picker (" + this.pickerLanguage() + ")",
        };
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
            while (!this.stopped && !idle && (phase === "starting" || phase === "running") && Date.now() < deadline) {
              await sleep(300);
              phase = this.phase();
              if (phase === "running") {
                const text = this.host.terminalText();
                if (text !== last) {
                  last = text;
                  changedAt = Date.now();
                  sawOutput = text.trim() !== "";
                } else if (sawOutput && Date.now() - changedAt > 2500) {
                  idle = true; // most likely it asks for input
                }
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

  // Why a file action cannot be done, or "": what is plainly not there, or not a
  // file or a folder as the action needs. The page checks all of it again when it
  // is done; this is only so that nothing is asked about that would be refused.
  private filesProblem(a: Action): string {
    const f = this.host.files;
    if (!f.ready()) return "the Files panel is not available here";
    // hidden (a name with a dot in front) and binary files are not Genie's to change
    const locked = (path: string | undefined): string => {
      const i = f.info(path || "");
      if (!i.exists) return (path || "") + " is not in the Files panel; files_list shows what is";
      if (i.hidden) return (path || "") + " is a hidden file: it is not Genie's to touch";
      if (i.protected) return (path || "") + " is a binary or protected file: it is not Genie's to change";
      return "";
    };
    const need = (path: string | undefined, type: "file" | "folder", what: string): string => {
      const i = f.info(path || "");
      if (!i.exists) return (path || "the home folder") + " is not in the Files panel; files_list shows what is";
      if (i.type !== type) return (path || "the home folder") + " is a " + i.type + ", " + what + " needs a " + type;
      return "";
    };
    switch (a.type) {
      case "files_list":
        return need(a.path, "folder", "listing");
      case "files_open":
        return need(a.path, "file", "opening in the editor") || locked(a.path);
      case "files_new": {
        const { parent, name } = splitPath(a.path || "");
        if (f.info(a.path || "").exists) return (a.path || "") + " exists already";
        if (name.charAt(0) === ".") return "a name that starts with a dot is a hidden file: it is not Genie's to make";
        return need(parent, "folder", "a new item in it");
      }
      case "files_save":
        return f.current() ? "" : "no file is open: open one with files_open first";
      case "files_rename": {
        const bad = locked(a.path);
        if (bad) return bad;
        const { parent } = splitPath(a.path || "");
        if (f.info(parent ? parent + "/" + a.name : a.name || "").exists) return (a.name || "") + " exists already in that folder";
        return "";
      }
      case "files_cut":
      case "files_copy":
        return locked(a.path);
      case "files_paste":
        return f.buffer() ? need(a.to, "folder", "pasting") : "nothing is cut or copied: use files_cut or files_copy first";
      case "files_move":
        return locked(a.path) || need(a.to, "folder", "moving into it");
      default: {
        // a binary file (a.out) may be deleted; a hidden one may not
        const i = f.info(a.path || "");
        if (!i.exists) return (a.path || "") + " is not in the Files panel; files_list shows what is";
        return i.hidden ? (a.path || "") + " is a hidden file: it is not Genie's to touch" : "";
      }
    }
  }

  // Why tab a.tab cannot be closed, or "": it has to be there, and it is not the
  // first, the main terminal of the session (terminal_reconnect restarts that one).
  // Any other tab may be closed, whoever opened it, when the user says yes.
  private closeProblem(a: Action): string {
    const tabs = this.host.tabs();
    const n = a.tab || 0;
    if (n < 1 || n > tabs.count) return "there are " + tabs.count + " terminal tab" + (tabs.count === 1 ? "" : "s") + ", not " + n;
    if (n === 1) return "tab 1 is the main terminal and is never closed by Genie; terminal_reconnect restarts it";
    return "";
  }

  // The file that is open in the editor (its path), "" if there is none or the
  // page has no Files panel.
  private openFile(): string {
    const f = this.host.files;
    return f.ready() ? f.current() : "";
  }

  // What a change of language did to the editor: the page puts the language's
  // starter code in it, unless a file is open, which it leaves alone.
  private afterLanguage(): string {
    const open = this.openFile();
    return open
      ? "the terminal started again in the language; the editor still holds the file that is open (" + open + ")"
      : "the editor now holds the starter code of the language, and the terminal started again";
  }

  private pickerLanguage(): string {
    const picker = this.picker();
    return picker && picker.selectedIndex >= 0 ? picker.options[picker.selectedIndex].text : "";
  }

  // After a language change the page opens the language's terminal and fetches
  // its starter code, which it puts in the editor a moment after it arrives.
  // Writing before that would be overwritten, so wait for it (the page says
  // which language's starter code is in, except on the practice page).
  private async waitForStarterCode(value: string, first: number): Promise<void> {
    const w = window as any;
    const told = "demoLoadedForLang" in w && !window.location.pathname.includes("practice");
    const deadline = Date.now() + LANGUAGE_WAIT_MS;
    if (first > 0) await sleep(first);
    this.host.thinking(true);
    try {
      while (told && !this.stopped && w.demoLoadedForLang !== value && Date.now() < deadline) await sleep(150);
    } finally {
      this.host.thinking(false);
    }
    if (this.stopped) throw new Error("stopped");
  }

  // Waits for the terminal that replaces `before` (a restart, a new tab) to be
  // connected, and for its first output to settle.
  private async awaitNewTerminal(before: unknown): Promise<{ ok: true; text: string } | { ok: false; detail: string }> {
    const deadline = Date.now() + NEW_TERMINAL_WAIT_MS;
    this.host.thinking(true);
    try {
      // no `break` in here: the build tool (microbundle) turns a break in an
      // async loop with a try/finally into a reference to a helper it never
      // declares ("_interrupt4 is not defined")
      let ready = false;
      while (!this.stopped && !ready && Date.now() < deadline) {
        const now = this.host.terminal();
        ready = now !== null && now !== before && this.host.terminalReady();
        if (!ready) await sleep(200);
      }
      if (this.stopped) throw new Error("stopped");
      if (this.host.terminal() === before || !this.host.terminalReady()) {
        return {
          ok: false,
          detail:
            "the terminal did not come up in " +
            NEW_TERMINAL_WAIT_MS / 1000 +
            " seconds. The server limits how many terminals one session may have open (about four), so close a tab yourself or try terminal_reconnect",
        };
      }
      await this.settle(Date.now() + 3000, 800, 400);
    } finally {
      this.host.thinking(false);
    }
    if (this.stopped) throw new Error("stopped");
    const text = this.host.terminalText().trim();
    return { ok: true, text: text.length > MAX_OUTPUT ? "..." + text.slice(-MAX_OUTPUT) : text };
  }

  private isCurrentLanguage(name: string): boolean {
    const picker = this.picker();
    if (!picker) return false;
    const value = findLanguage(Array.from(picker.options).map((o) => ({ value: o.value, text: o.text })), name);
    return value !== null && picker.value === value;
  }

  // The file actions, one by one. They stand on window.FileBrowser (the page's
  // Files panel) and say in words what happened, as the other actions do.
  private async fileAction(a: Action): Promise<StepResult> {
    const f = this.host.files;
    const done = (r: FilesResult, ok: StepResult): StepResult =>
      r.ok ? ok : { type: a.type, status: "failed", detail: r.error };
    const path = a.path || "";
    switch (a.type) {
      case "files_list": {
        f.reveal();
        const r = f.list(path);
        if (!r.ok) return done(r, { type: a.type, status: "done" });
        const text = (r.entries || []).join("\n");
        return {
          type: a.type,
          status: "done",
          output: text === "" ? "(empty)" : text.length > MAX_OUTPUT ? text.slice(0, MAX_OUTPUT) + "\n..." : text,
          detail: r.truncated ? "only the first entries are listed; list a folder to see more" : text.length > MAX_OUTPUT ? "the list is cut: list a folder to see the rest" : undefined,
        };
      }
      case "files_open": {
        const r = await f.open(path);
        if (!r.ok) return done(r, { type: a.type, status: "done" });
        await sleep(1800); // the page then sets the language of the file's kind, a second after
        return { type: a.type, status: "done", detail: (r.detail || path + " is open in the editor") + "; the language picker shows " + this.pickerLanguage() };
      }
      case "files_new": {
        const r = await f.create(path, a.kind === "folder" ? "folder" : "file");
        return done(r, { type: a.type, status: "done", detail: "created " + path + (a.kind === "folder" ? "" : "; it is empty and not open (files_open opens it)") });
      }
      case "files_save": {
        const r = await f.save();
        return done(r, { type: a.type, status: "done", detail: r.ok ? r.detail : undefined });
      }
      case "files_rename": {
        const r = await f.rename(path, a.name || "");
        return done(r, { type: a.type, status: "done", detail: "renamed to " + a.name });
      }
      case "files_cut": {
        const r = f.cut(path);
        return done(r, { type: a.type, status: "done", detail: "cut; files_paste puts it in a folder" });
      }
      case "files_copy": {
        const r = f.copy(path);
        return done(r, { type: a.type, status: "done", detail: "copied; files_paste puts a copy in a folder" });
      }
      case "files_paste": {
        const r = await f.paste(a.to || "");
        return done(r, { type: a.type, status: "done", detail: "pasted into " + (a.to || "the home folder") });
      }
      case "files_move": {
        const r = await f.move(path, a.to || "");
        return done(r, { type: a.type, status: "done", detail: "moved into " + (a.to || "the home folder") });
      }
      default: {
        const r = await f.remove(path);
        return done(r, { type: a.type, status: "done", detail: "deleted " + path + (r.ok && r.detail ? "; " + r.detail : "") });
      }
    }
  }

  private picker(): HTMLSelectElement | null {
    return document.getElementById("optionlist") as HTMLSelectElement | null;
  }

  // Why Genie cannot run the chosen language, or "" if it can.
  private pageOnly(value?: string): string {
    const picker = this.picker();
    if (!picker || PAGE_ONLY_LANGUAGES.indexOf(value || picker.value) < 0) return "";
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
    let state: "waiting" | "quiet" | "late" = "waiting";
    while (!this.stopped && state === "waiting") {
      await sleep(250);
      const now = Date.now();
      const text = this.host.terminalText();
      if (text !== last) {
        last = text;
        changedAt = now;
      }
      if (endedAtTail(text) || (now - changedAt >= quietMs && now - start >= minMs)) state = "quiet";
      else if (now >= deadline) state = "late";
    }
    return state === "quiet";
  }

  // ---- permission ---------------------------------------------------------------

  // risk: why a terminal line is risky, "" if it is not. A risky line is asked
  // about every time and can only be allowed once.
  private permission(scope: Scope, a: Action, risk: string = ""): Promise<boolean> {
    if (!risk && this.grants.has(scope)) return Promise.resolve(true);
    return new Promise<boolean>((resolve) => {
      const box = el("div", "cw-agent__ask" + (risk ? " cw-agent__ask--risk" : ""));
      box.setAttribute("role", "group");
      const title =
        a.type === "terminal_close_tab"
          ? "Genie wants to close a terminal tab"
          : risk && a.type === "terminal_type"
          ? "Genie wants to type a risky line in your terminal"
          : scopeQuestion(scope);
      box.setAttribute("aria-label", title);
      box.appendChild(el("div", "cw-agent__ask-title", title));
      box.appendChild(el("p", "cw-agent__ask-text", scopeDetail(scope, a, this.openFile())));
      if (a.type === "terminal_type") {
        // exactly what will be typed, as plain text
        box.appendChild(el("pre", "cw-agent__cmd", a.text || ""));
      } else if (scope === "files") {
        // exactly which paths, as plain text
        box.appendChild(el("pre", "cw-agent__cmd", describe(a)));
      }
      if (risk) box.appendChild(el("p", "cw-agent__ask-warn", "Asked every time because it " + risk + "."));
      const buttons = el("div", "cw-agent__ask-buttons");
      let answered = false;
      if (this.host.attention) this.host.attention(true);
      const done = (answer: "once" | "session" | "deny") => {
        if (answered) return;
        answered = true;
        if (this.host.attention) this.host.attention(false);
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
      // Keys go where the user is typing. A card that took the focus for its
      // first button would be answered "Allow once" by the next Enter or space
      // meant for the editor. So the focus moves only from inside the panel (or
      // from nowhere), and to the card itself: Tab then reaches its buttons.
      const active = document.activeElement as HTMLElement | null;
      const elsewhere = !!active && active !== document.body && !(typeof active.closest === "function" && active.closest("#" + PANEL_ID));
      if (!elsewhere) {
        box.tabIndex = -1;
        try {
          box.focus({ preventScroll: true });
        } catch (e) {
          // the focus is not essential
        }
      }
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
    if (this.host.progress) this.host.progress(step, max);
  }

  // Runs something the user has to answer (a diff, a question) and tells the
  // panel meanwhile, so that its button can say Genie is waiting.
  private async needsYou<T>(work: Promise<T>): Promise<T> {
    if (this.host.attention) this.host.attention(true);
    try {
      return await work;
    } finally {
      if (this.host.attention) this.host.attention(false);
    }
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
