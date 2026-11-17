// The panel's side of the saved chats: asks server/genie_chats.go (POST genie_chats) to read and add to the chats of the
// workspace. No DOM, and the call is given to it, so that node runs it in test/chat-store.test.ts.
//
// What is written goes through a queue, in order. When the workspace cannot be reached (its node is away, the disk is
// full) the lines wait in the queue and are tried again, and the status says "failing"; nothing is lost while the page
// is open. When the server says the chats are not for this person (a viewer of a shared session, a site that switched
// them off) the queue is dropped and the status says why.

import type { ChatLine, ChatSummary } from "./chat-tabs";

export type StoreStatus = "ok" | "failing" | "off" | "viewer" | "guests_off" | "saving_off";

export interface StoreState {
  enabled: boolean;
  off: boolean; // this person switched saving off
  open: string[];
  chats: ChatSummary[];
  bytes: number;
  limits: { chats: number; tabs: number; bytes: number };
}

export type Call = (body: Record<string, unknown>) => Promise<{ status: number; json: any }>;

type Job = { kind: "append"; id: string; lines: ChatLine[] } | { kind: "tabs"; open: string[] } | { kind: "delete"; id: string };

export class ChatStore {
  status: StoreStatus = "ok";
  private queue: Job[] = [];
  private pumping = false;
  private current: Job | null = null; // the job that is being sent: it is not changed or taken out from under the call
  private timer: any = null;
  private idle: (() => void)[] = [];

  private call: Call;
  private onStatus: (s: StoreStatus) => void;
  private retryMs: number;

  // (no parameter properties: node runs this file as it is, in the tests)
  constructor(call: Call, onStatus: (s: StoreStatus) => void = () => {}, retryMs: number = 8000) {
    this.call = call;
    this.onStatus = onStatus;
    this.retryMs = retryMs;
  }

  private set(s: StoreStatus) {
    if (this.status !== s) {
      this.status = s;
      this.onStatus(s);
    }
  }

  // Why a server answer means the chats are not for this page: the status to take, or null.
  private refusal(status: number, json: any): StoreStatus | null {
    const code = json && json.error && json.error.code;
    if (status === 403 && code === "not_owner") return "viewer";
    if (status === 403 && code === "off") return "off";
    if (status === 403 && code === "guests_off") return "guests_off";
    if (status === 409 && code === "saving_off") return "saving_off";
    return null;
  }

  // ---- reading -------------------------------------------------------------------------

  // The chats, the open tabs and the limits; null when the chats are not available to this page (the status says why).
  async state(): Promise<StoreState | null> {
    try {
      const r = await this.call({ op: "state" });
      const refused = this.refusal(r.status, r.json);
      if (refused) {
        this.set(refused);
        return null;
      }
      if (r.status !== 200 || !r.json || r.json.enabled !== true) {
        this.set("failing");
        return null;
      }
      this.set("ok");
      return r.json as StoreState;
    } catch (e) {
      this.set("failing");
      return null;
    }
  }

  async read(id: string): Promise<ChatLine[] | null> {
    try {
      const r = await this.call({ op: "read", id });
      if (r.status !== 200 || !r.json || !Array.isArray(r.json.lines)) return null;
      return r.json.lines as ChatLine[];
    } catch (e) {
      return null;
    }
  }

  async exportMarkdown(id: string | null): Promise<string | null> {
    try {
      const r = await this.call(id ? { op: "export", id } : { op: "exportAll" });
      return r.status === 200 && r.json && typeof r.json.markdown === "string" ? r.json.markdown : null;
    } catch (e) {
      return null;
    }
  }

  // ---- writing (queued) -----------------------------------------------------------------

  append(id: string, lines: ChatLine[]) {
    if (lines.length === 0 || !this.writable()) return;
    // lines for the same chat that are next to each other go in one call (at most 20: the server's limit)
    const last = this.queue[this.queue.length - 1];
    if (last && last !== this.current && last.kind === "append" && last.id === id && last.lines.length + lines.length <= 20) last.lines = last.lines.concat(lines);
    else this.queue.push({ kind: "append", id, lines: lines.slice() });
    this.pump();
  }

  // Which chats are open. Only the last word counts, so a queued one is replaced.
  setTabs(open: string[]) {
    if (!this.writable()) return;
    this.queue = this.queue.filter((j) => j.kind !== "tabs" || j === this.current);
    this.queue.push({ kind: "tabs", open: open.slice() });
    this.pump();
  }

  // Removes a chat for good; what was waiting to be written to it is dropped.
  remove(id: string) {
    this.queue = this.queue.filter((j) => !(j.kind === "append" && j.id === id) || j === this.current);
    this.queue.push({ kind: "delete", id });
    this.pump();
  }

  async removeAll(): Promise<boolean> {
    this.queue = [];
    try {
      const r = await this.call({ op: "deleteAll" });
      return r.status === 200;
    } catch (e) {
      return false;
    }
  }

  async setOff(off: boolean): Promise<boolean> {
    try {
      const r = await this.call({ op: "off", off });
      if (r.status === 200) {
        this.set(off ? "saving_off" : "ok");
        return true;
      }
      return false;
    } catch (e) {
      return false;
    }
  }

  // Whether writing is worth trying: not for a viewer, a site that has it off, or a person who switched it off.
  private writable(): boolean {
    return this.status === "ok" || this.status === "failing";
  }

  // A new start: another workspace (the person signed in), so what waited for the old one is dropped.
  reset() {
    this.queue = [];
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.set("ok");
  }

  get pending(): number {
    return this.queue.length;
  }

  // Resolves when the queue is not being worked on: everything was written or dropped, or what is left waits for the
  // workspace to come back (pending says how many).
  whenIdle(): Promise<void> {
    if (!this.pumping) return Promise.resolve();
    return new Promise<void>((resolve) => this.idle.push(resolve));
  }

  // Tries the queue again now (the timer calls it while the workspace is away).
  retry() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.pump();
  }

  private finish() {
    this.pumping = false;
    const waiting = this.idle;
    this.idle = [];
    waiting.forEach((r) => r());
  }

  // No `break` or `continue` in a loop that awaits (see agent.ts: the build tool gets it wrong): the loop ends by its flag.
  private async pump() {
    if (this.pumping) return;
    this.pumping = true;
    let go = true;
    while (go && this.queue.length > 0) {
      go = await this.step(this.queue[0]);
    }
    this.finish();
  }

  // One job: true to go on with the next, false to stop (the job stays, or the queue was dropped).
  private async step(job: Job): Promise<boolean> {
    this.current = job;
    let status = 0;
    let json: any = null;
    try {
      const body = job.kind === "append" ? { op: "append", id: job.id, lines: job.lines } : job.kind === "tabs" ? { op: "tabs", open: job.open } : { op: "delete", id: job.id };
      const r = await this.call(body);
      status = r.status;
      json = r.json;
    } catch (e) {
      status = 0;
    }
    this.current = null;
    // the job leaves the queue by what it is, not by its place: others may have been added or taken out meanwhile
    const done = () => {
      const at = this.queue.indexOf(job);
      if (at >= 0) this.queue.splice(at, 1);
    };
    if (status === 200) {
      done();
      this.set("ok");
      return true;
    }
    const refused = this.refusal(status, json);
    if (refused) {
      this.queue = [];
      this.set(refused);
      return false;
    }
    if (status >= 400 && status < 500 && status !== 429 && status !== 408) {
      done(); // the server will never take this one: leave it out and go on
      return true;
    }
    // the workspace cannot be reached, or is full: keep the job and try again later
    this.set("failing");
    if (!this.timer && this.retryMs > 0) {
      this.timer = setTimeout(() => {
        this.timer = null;
        this.pump();
      }, this.retryMs);
    }
    return false;
  }
}
