// The agent's code is a second file, fetched the first time it is needed (Agent mode is switched on, a task starts, a task is resumed), so
// that a visitor who only chats does not download it (docs/lld/19-genie-agent.md section 20). Until it is there this stands in for the
// runner with the few things the panel asks of it; once it is there everything goes to the real one.

import type { AgentHost, AgentMemory, AgentRunner, AgentState } from "./agent";

export type AgentCtor = new (host: AgentHost, onState: (state: AgentState) => void) => AgentRunner;

// where the widget's own script came from: the agent file is beside it, with the same version mark
let widgetSrc = "";
try {
  const cur = document.currentScript as HTMLScriptElement | null;
  widgetSrc = (cur && cur.src) || "";
  if (!widgetSrc) {
    const s = document.querySelector('script[src*="chat-widget"]') as HTMLScriptElement | null;
    widgetSrc = (s && s.src) || "";
  }
} catch (e) {
  widgetSrc = "";
}

export function agentUrl(src: string = widgetSrc): string {
  if (!src) return "chat-widget-agent.js";
  return src.replace(/chat-widget(\.min)?\.js/, "chat-widget-agent.js");
}

let loading: Promise<AgentCtor> | null = null;

// Fetches the agent's code once. A failure is not kept: the next try asks again.
export function loadAgentCode(url: string = agentUrl()): Promise<AgentCtor> {
  const w = window as any;
  if (w.GenieAgentCode && w.GenieAgentCode.AgentRunner) return Promise.resolve(w.GenieAgentCode.AgentRunner as AgentCtor);
  if (loading) return loading;
  loading = new Promise<AgentCtor>((resolve, reject) => {
    const s = document.createElement("script");
    s.src = url;
    s.async = true;
    s.onload = () => {
      if (w.GenieAgentCode && w.GenieAgentCode.AgentRunner) resolve(w.GenieAgentCode.AgentRunner as AgentCtor);
      else reject(new Error("the agent file did not hold the agent"));
    };
    s.onerror = () => reject(new Error("the agent file could not be loaded"));
    document.head.appendChild(s);
  });
  loading.catch(() => {
    loading = null;
  });
  return loading;
}

const EMPTY_MEMORY = (): AgentMemory => ({ recap: "", lastTaskLine: "", past: [] });

export class LazyAgent {
  private real: AgentRunner | null = null;
  private starting = false;
  private memory: AgentMemory | null = null; // what was set before the real runner was there
  failed = ""; // why the last load failed
  private make: (R: AgentCtor) => AgentRunner;
  private load: () => Promise<AgentCtor>;

  // (no parameter properties: node runs this file as it is, in the tests)
  constructor(make: (R: AgentCtor) => AgentRunner, load: () => Promise<AgentCtor> = loadAgentCode) {
    this.make = make;
    this.load = load;
  }

  // a task is in flight (or its code is being fetched)
  get running(): boolean {
    return this.starting || (!!this.real && this.real.running);
  }

  // Fetches the code now (Agent mode was switched on: by the time a task starts it is there). Never throws.
  preload(): Promise<boolean> {
    return this.ensure().then(
      () => true,
      () => false
    );
  }

  private async ensure(): Promise<AgentRunner> {
    if (this.real) return this.real;
    const R = await this.load();
    if (!this.real) {
      this.real = this.make(R);
      if (this.memory) this.real.setMemory(this.memory);
      this.memory = null;
    }
    return this.real;
  }

  async start(task: string, attached: string = "", limit: string[] | null = null): Promise<void> {
    if (this.running) return;
    this.starting = true;
    let r: AgentRunner;
    try {
      r = await this.ensure();
      this.failed = "";
    } catch (e) {
      this.failed = String((e as Error).message || e);
      throw e;
    } finally {
      this.starting = false;
    }
    await r.start(task, attached, limit);
  }

  stop() {
    if (this.real) this.real.stop();
  }
  foldEnded() {
    if (this.real) this.real.foldEnded();
  }
  getMemory(): AgentMemory {
    return this.real ? this.real.getMemory() : this.memory || EMPTY_MEMORY();
  }
  setMemory(m: AgentMemory | null) {
    if (this.real) this.real.setMemory(m);
    else this.memory = m;
  }
  canAutoApply(): boolean {
    return this.real ? this.real.canAutoApply() : false;
  }
  enableAutoApply() {
    if (this.real) this.real.enableAutoApply();
  }
}
