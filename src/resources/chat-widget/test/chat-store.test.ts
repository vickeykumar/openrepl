// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { ChatStore } from "../src/chat-store.ts";

type Reply = { status: number; json?: any } | "throw";

function fake(replies: Reply[] = []) {
  const calls: any[] = [];
  const status: string[] = [];
  const call = async (body: any) => {
    calls.push(body);
    const r = replies.length ? replies.shift()! : { status: 200, json: { ok: true } };
    if (r === "throw") throw new Error("offline");
    return { status: r.status, json: r.json || {} };
  };
  const store = new ChatStore(call, (s) => status.push(s), 0);
  return { store, calls, status };
}

test("the state says what the server said, and a refusal is a status", async () => {
  const f = fake([{ status: 200, json: { enabled: true, off: false, open: ["a"], chats: [], bytes: 0, limits: { chats: 50, tabs: 8, bytes: 204800 } } }]);
  const s = await f.store.state();
  assert.deepEqual(s!.open, ["a"]);
  assert.equal(f.store.status, "ok");
  const v = fake([{ status: 403, json: { error: { code: "not_owner" } } }]);
  assert.equal(await v.store.state(), null);
  assert.equal(v.store.status, "viewer");
  const off = fake([{ status: 403, json: { error: { code: "off" } } }]);
  await off.store.state();
  assert.equal(off.store.status, "off");
  const g = fake([{ status: 403, json: { error: { code: "guests_off" } } }]);
  await g.store.state();
  assert.equal(g.store.status, "guests_off");
  const down = fake(["throw"]);
  assert.equal(await down.store.state(), null);
  assert.equal(down.store.status, "failing");
});

test("lines are written in order, and lines for one chat that follow each other go in one call", async () => {
  const f = fake();
  f.store.append("aaaa", [{ t: "meta", id: "aaaa" }]);
  f.store.append("aaaa", [{ t: "user", text: "hi" }]);
  f.store.append("bbbb", [{ t: "meta", id: "bbbb" }]);
  await f.store.whenIdle();
  assert.ok(f.calls.length >= 2 && f.calls.length <= 3);
  assert.equal(f.calls[f.calls.length - 1].id, "bbbb", "the other chat is written after");
  const all = f.calls.filter((c) => c.id === "aaaa").flatMap((c) => c.lines.map((l: any) => l.t));
  assert.deepEqual(all, ["meta", "user"]);
});

test("when the workspace is away the lines wait and are written when it is back", async () => {
  const f = fake(["throw", { status: 507, json: { error: { code: "write" } } }]);
  f.store.append("aaaa", [{ t: "meta", id: "aaaa" }, { t: "user", text: "one" }]);
  await f.store.whenIdle();
  assert.equal(f.store.status, "failing");
  assert.equal(f.store.pending, 1, "the lines are kept");
  f.store.retry();
  await f.store.whenIdle();
  assert.equal(f.store.status, "failing", "still full");
  f.store.retry();
  await f.store.whenIdle();
  assert.equal(f.store.status, "ok");
  assert.equal(f.store.pending, 0);
  assert.equal(f.calls.length, 3);
  assert.deepEqual(f.calls[2].lines.map((l: any) => l.t), ["meta", "user"]);
  assert.deepEqual(f.status, ["failing", "ok"]);
});

test("a line the server will not take is left out and the next goes on", async () => {
  const f = fake([{ status: 400, json: { error: { code: "bad_line" } } }]);
  f.store.append("aaaa", [{ t: "meta", id: "aaaa" }]);
  f.store.append("bbbb", [{ t: "meta", id: "bbbb" }]);
  await f.store.whenIdle();
  assert.equal(f.store.pending, 0);
  assert.equal(f.store.status, "ok");
  assert.ok(f.calls.length >= 2);
});

test("a refusal drops the queue and nothing more is written", async () => {
  const f = fake([{ status: 403, json: { error: { code: "not_owner" } } }]);
  f.store.append("aaaa", [{ t: "meta", id: "aaaa" }]);
  await f.store.whenIdle();
  assert.equal(f.store.status, "viewer");
  f.store.append("aaaa", [{ t: "user", text: "later" }]);
  f.store.setTabs(["aaaa"]);
  await f.store.whenIdle();
  assert.equal(f.calls.length, 1, "something was written after the refusal");
  assert.equal(f.store.pending, 0);
});

test("only the last word on the open tabs counts, and a deleted chat loses what waited for it", async () => {
  const f = fake(["throw"]);
  f.store.setTabs(["a"]);
  await f.store.whenIdle();
  f.store.setTabs(["a", "b"]);
  f.store.setTabs(["b"]);
  f.store.append("zzzz", [{ t: "meta", id: "zzzz" }]);
  f.store.remove("zzzz");
  f.store.retry();
  await f.store.whenIdle();
  const tabs = f.calls.filter((c) => c.op === "tabs");
  assert.deepEqual(tabs[tabs.length - 1].open, ["b"]);
  assert.ok(!f.calls.some((c) => c.op === "append" && c.id === "zzzz"));
  assert.ok(f.calls.some((c) => c.op === "delete" && c.id === "zzzz"));
});

test("reading, exporting, switching saving off and deleting everything", async () => {
  const f = fake([
    { status: 200, json: { lines: [{ t: "user", text: "x" }] } },
    { status: 404, json: {} },
    { status: 200, json: { markdown: "# t" } },
    { status: 200, json: { ok: true, off: true } },
    { status: 200, json: { ok: true } },
  ]);
  assert.deepEqual(await f.store.read("aaaa"), [{ t: "user", text: "x" }]);
  assert.equal(await f.store.read("nope"), null);
  assert.equal(await f.store.exportMarkdown("aaaa"), "# t");
  assert.equal(await f.store.setOff(true), true);
  assert.equal(f.store.status, "saving_off");
  f.store.append("aaaa", [{ t: "user", text: "ignored" }]);
  await f.store.whenIdle();
  assert.equal(f.calls.length, 4, "a line was written while saving is off");
  assert.equal(await f.store.removeAll(), true);
});
