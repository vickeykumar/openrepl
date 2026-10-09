// The starter questions of the practice page (resources/js/practice-store.js):
// they are put in the visitor's list once, they are not sent to the account
// until opened, they do not count in the 100 generated questions a visitor
// keeps, and a deleted one stays deleted. The store is run as a browser would
// run it, with a pretend page, storage and network.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const resources = path.join(here, "..", "..", "resources", "js");
const source = fs.readFileSync(path.join(resources, "practice-store.js"), "utf8");
const catalog = JSON.parse(fs.readFileSync(path.join(resources, "dsa.json"), "utf8"));

// a page with its own storage; `network` is what /js/dsa.json and /practice/progress answer
function page({ items = catalog, storage = {}, account = null } = {}) {
  const puts = [];
  const store = Object.assign({}, storage);
  const window = {
    addEventListener() {},
    fetch: null,
  };
  window.fetch = (url, opts = {}) => {
    if (String(url).includes("dsa.json")) return Promise.resolve({ ok: items !== null, status: items === null ? 404 : 200, json: async () => items });
    if (opts.method === "PUT") {
      const body = JSON.parse(opts.body);
      puts.push(body);
      return Promise.resolve({ ok: true, status: 200, json: async () => ({ signedIn: true, questions: body.questions, state: body.state }) });
    }
    return Promise.resolve({ ok: true, status: 200, json: async () => (account ? Object.assign({ signedIn: true }, account) : { signedIn: false }) });
  };
  const ctx = {
    window,
    fetch: window.fetch,
    document: { readyState: "complete", getElementById: () => null, addEventListener() {} },
    localStorage: {
      getItem: (k) => (k in store ? store[k] : null),
      setItem: (k, v) => void (store[k] = String(v)),
    },
    console: { warn() {}, error() {}, log() {} },
    setTimeout,
    clearTimeout,
    JSON,
  };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return { PS: window.PracticeStore, store, puts, window };
}

const question = (n, extra = {}) => ({ id: "q" + n, name: "Q" + n, nameHyphenated: "q" + n, topic: "Arrays", difficulty: "Easy", added: 1800000000000 + n, ...extra });

test("the first visit has every starter question, once", async () => {
  const p = page();
  assert.equal(await p.PS.ready(), true);
  const all = p.PS.all();
  const slugs = new Set(catalog.map((c) => c.title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "")));
  assert.equal(all.length, slugs.size);
  assert.ok(all.length > 290, "only " + all.length);
  const two = p.PS.find("two-sum");
  assert.equal(two.id, "s-two-sum");
  assert.equal(two.name, "Two Sum");
  assert.equal(two.topic, "Arrays");
  assert.equal(two.difficulty, "Easy");
  assert.equal(two.description, null);
  assert.equal(two.starter, true);
  // the pages that read the list directly find it too
  const raw = JSON.parse(p.store.questions);
  assert.ok(raw.find((q) => q.nameHyphenated === "best-time-to-buy-and-sell-stock-ii"));
});

test("names read like names, and a second call adds nothing", async () => {
  const p = page();
  await p.PS.ready();
  assert.equal(p.PS.find("best-time-to-buy-and-sell-stock-ii").name, "Best Time to Buy and Sell Stock II");
  assert.equal(p.PS.find("lru-cache").name, "LRU Cache");
  const before = p.store.questions;
  assert.equal(await p.PS.seed(), true); // the same promise: it ran once
  assert.equal(p.store.questions, before);
  // a second page load finds them all there
  const again = page({ storage: p.store });
  assert.equal(await again.PS.ready(), false);
  assert.equal(again.PS.all().length, p.PS.all().length);
});

test("the starters come after what the visitor generated, in the order of the file", async () => {
  const p = page({ storage: { questions: JSON.stringify([question(1)]) } });
  await p.PS.ready();
  const byNewest = p.PS.all().sort((a, b) => b.added - a.added);
  assert.equal(byNewest[0].id, "q1");
  assert.equal(byNewest[1].nameHyphenated, "two-sum");
  assert.equal(byNewest[2].nameHyphenated, "best-time-to-buy-and-sell-stock");
});

test("a question the visitor already has is not added again", async () => {
  const mine = question(1, { name: "Two Sum", nameHyphenated: "two-sum", id: "old-random-id", description: "text" });
  const p = page({ storage: { questions: JSON.stringify([mine]) } });
  await p.PS.ready();
  assert.equal(p.PS.all().filter((q) => q.nameHyphenated === "two-sum").length, 1);
  assert.equal(p.PS.find("two-sum").id, "old-random-id");
});

test("a starter that was deleted is not put back", async () => {
  const first = page();
  await first.PS.ready();
  first.PS.remove("s-two-sum");
  const again = page({ storage: first.store });
  await again.PS.ready();
  assert.equal(again.PS.find("two-sum"), null);
  assert.equal(again.PS.all().length, first.PS.all().length);
});

test("a starter nobody opened is not sent to the account, and one that was opened is", async () => {
  const p = page({ account: { questions: {}, state: {} } });
  await p.PS.ready();
  p.PS.setDone("s-two-sum", true);
  await p.PS.sync();
  let sent = p.puts[p.puts.length - 1];
  assert.deepEqual(Object.keys(sent.questions), [], "the starters were sent");
  assert.equal(sent.state["s-two-sum"].done, true, "its done mark is sent");
  // opened: its description was written
  const two = p.PS.find("two-sum");
  two.description = "Given an array...";
  p.PS.update(two);
  await p.PS.sync();
  sent = p.puts[p.puts.length - 1];
  assert.deepEqual(Object.keys(sent.questions), ["s-two-sum"]);
});

test("starters do not count in the 100 generated questions, and a full list keeps them", async () => {
  const mine = [];
  for (let i = 0; i < 100; i++) mine.push(question(i));
  const p = page({ storage: { questions: JSON.stringify(mine) } });
  await p.PS.ready();
  const starters = p.PS.all().filter((q) => q.starter).length;
  const added = p.PS.add(question(500));
  assert.equal(added.error, null);
  const all = p.PS.all();
  assert.equal(all.filter((q) => !q.starter).length, 100);
  assert.equal(all.filter((q) => q.starter).length, starters, "a starter was dropped to make room");
  assert.equal(p.PS.find("q0"), null, "the oldest generated question stays");
  assert.ok(p.PS.find("q500"));
});

test("an unreachable or broken list leaves the practice page as it was", async () => {
  for (const items of [null, { not: "a list" }, []]) {
    const p = page({ items, storage: { questions: JSON.stringify([question(1)]) } });
    assert.equal(await p.PS.ready(), false);
    assert.equal(p.PS.all().length, 1);
  }
});

test("a visitor's own questions and the account's copy of a starter are kept when signed in", async () => {
  const opened = { id: "s-two-sum", name: "Two Sum", nameHyphenated: "two-sum", topic: "Arrays", difficulty: "Easy", description: "from another device", code_templates: {}, added: 1735689600000, updated: 1800000000000, starter: true };
  const p = page({ account: { questions: { "s-two-sum": opened }, state: {} } });
  await p.PS.ready();
  await p.PS.init();
  assert.equal(p.PS.find("two-sum").description, "from another device");
  assert.equal(p.PS.all().filter((q) => q.nameHyphenated === "two-sum").length, 1);
});
