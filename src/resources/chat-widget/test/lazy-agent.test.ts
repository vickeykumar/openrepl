import { test } from "node:test";
import assert from "node:assert/strict";
import { LazyAgent, agentUrl } from "../src/lazy-agent.ts";

// a stand-in for the real runner
class Fake {
  running = false;
  started: string[] = [];
  memory: any = { recap: "r", lastTaskLine: "l", past: ["p"] };
  stopped = 0;
  folded = 0;
  async start(t: string) {
    this.running = true;
    this.started.push(t);
    await Promise.resolve();
    this.running = false;
  }
  stop() { this.stopped++; }
  foldEnded() { this.folded++; }
  getMemory() { return this.memory; }
  setMemory(m: any) { this.memory = m; }
  canAutoApply() { return true; }
  enableAutoApply() {}
}

test("the agent file is the widget file's neighbour, with the same version mark", () => {
  assert.equal(agentUrl("https://x.dev/js/chat-widget.js"), "https://x.dev/js/chat-widget-agent.js");
  assert.equal(agentUrl("https://x.dev/js/chat-widget.js?v=abc"), "https://x.dev/js/chat-widget-agent.js?v=abc");
  assert.equal(agentUrl(""), "chat-widget-agent.js");
});

test("nothing is loaded until a task starts or the code is asked for; then it is loaded once", async () => {
  let loads = 0;
  const lazy = new LazyAgent((R: any) => new R() as any, async () => (loads++, Fake as any));
  assert.equal(loads, 0);
  assert.equal(lazy.running, false);
  await lazy.start("make a page");
  await lazy.start("and another");
  assert.equal(loads, 1, "the second task uses the runner that is there");
  const real = (lazy as any).real as Fake;
  assert.deepEqual(real.started, ["make a page", "and another"]);
});

test("running is true while the code is being fetched", async () => {
  let release: (c: any) => void = () => {};
  const lazy = new LazyAgent((R: any) => new R() as any, () => new Promise((r) => (release = r)));
  const p = lazy.start("task");
  assert.equal(lazy.running, true);
  release(Fake);
  await p;
  assert.equal(lazy.running, false);
});

test("what was set before the real runner was there is given to it, and read back before", async () => {
  const lazy = new LazyAgent((R: any) => new R() as any, async () => Fake as any);
  assert.deepEqual(lazy.getMemory(), { recap: "", lastTaskLine: "", past: [] });
  lazy.setMemory({ recap: "x", lastTaskLine: "y", past: ["z"] });
  assert.deepEqual(lazy.getMemory(), { recap: "x", lastTaskLine: "y", past: ["z"] });
  assert.equal(await lazy.preload(), true);
  assert.deepEqual(((lazy as any).real as Fake).memory, { recap: "x", lastTaskLine: "y", past: ["z"] });
  lazy.stop();
  lazy.foldEnded();
  assert.equal(((lazy as any).real as Fake).stopped, 1);
  assert.equal(((lazy as any).real as Fake).folded, 1);
  assert.equal(lazy.canAutoApply(), true);
});

test("before the code is there these do nothing, and a failed load says so and can be tried again", async () => {
  const lazy = new LazyAgent((R: any) => new R() as any, async () => { throw new Error("offline"); });
  lazy.stop();
  lazy.foldEnded();
  assert.equal(lazy.canAutoApply(), false);
  assert.equal(await lazy.preload(), false);
  await assert.rejects(() => lazy.start("t"), /offline/);
  assert.equal(lazy.failed, "offline");
  assert.equal(lazy.running, false);
});
