// The loading card of the practice editor (resources/js/question-loader.js):
// what it says for each way a question can fail, the time limit, Cancel, Try
// again, and a newer load taking over from an older one. It is run with a
// pretend card (the `ui` option), so that what the visitor is shown can be read.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const source = fs.readFileSync(path.join(here, "..", "..", "resources", "js", "question-loader.js"), "utf8");

function loaderPage() {
  const window = {};
  const ctx = { window, document: { querySelector: () => null, getElementById: () => null }, console: { error() {}, log() {}, warn() {} }, setTimeout, clearTimeout, AbortController, Promise };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return window.QuestionLoader;
}

// a card that writes down what it was asked to show
function card() {
  const log = [];
  const ui = {
    log,
    last: () => log[log.length - 1],
    waiting: (label) => log.push({ s: "waiting", label }),
    loading: (o) => log.push({ s: "loading", ...o }),
    slow: (o) => log.push({ s: "slow", ...o }),
    failed: (d, h) => log.push({ s: "failed", d, h }),
    hide: () => log.push({ s: "hidden" }),
  };
  return ui;
}
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const failure = (kind, extra = {}) => Object.assign(new Error(kind), { kind }, extra);

test("a question that loads: the card says it is working, then goes, and the value is handed on", async () => {
  const QL = loaderPage();
  const ui = card();
  let got = null;
  QL.load({ ui, title: "Two Sum", language: "Python", work: async () => "TEMPLATE", onReady: (v) => (got = v) });
  assert.equal(ui.last().s, "loading");
  assert.match(ui.last().title, /Getting your question ready/);
  assert.match(ui.last().text, /Two Sum/);
  assert.match(ui.last().text, /starter for Python/);
  assert.match(ui.last().text, /5 to 20 seconds/);
  await wait(10);
  assert.equal(got, "TEMPLATE");
  assert.equal(ui.last().s, "hidden");
});

test("each way it can fail says what went wrong, in words", () => {
  const QL = loaderPage();
  const d = (kind, extra) => QL.describeFailure(failure(kind, extra), 60000);
  assert.match(d("timeout").text, /within 60 seconds/);
  assert.match(d("offline").title, /Couldn't reach OpenREPL/);
  assert.match(d("offline").text, /internet connection/);
  assert.match(d("limit", { status: 429 }).title, /reached your Genie limit/);
  assert.equal(d("limit", { status: 429 }).detail, "HTTP 429");
  assert.match(d("auth", { status: 403 }).text, /Reload the page/);
  assert.equal(d("off", { status: 503, serverMessage: "Genie is switched off for now. Please try again later." }).text, "Genie is switched off for now. Please try again later.");
  assert.match(d("format").title, /answer couldn't be used/);
  const server = d("server", { status: 502, serverMessage: "Bad gateway" });
  assert.match(server.text, /HTTP 502/);
  assert.equal(server.detail, "Bad gateway");
  // an Error that says nothing about why is a problem of the service, not a blank card
  assert.match(QL.describeFailure(new Error("who knows")).title, /AI service had a problem/);
  assert.match(QL.describeFailure(undefined).title, /AI service had a problem/);
});

test("a failure shows its reason, and Try again runs the work again", async () => {
  const QL = loaderPage();
  const ui = card();
  let calls = 0, got = null;
  QL.load({ ui, title: "Two Sum", work: async () => { if (++calls === 1) throw failure("limit", { status: 429 }); return "OK"; }, onReady: (v) => (got = v) });
  await wait(10);
  assert.equal(ui.last().s, "failed");
  assert.match(ui.last().d.title, /Genie limit/);
  ui.last().h.retry();
  assert.equal(ui.last().s, "loading", "the card goes back to working while it tries again");
  await wait(10);
  assert.equal(calls, 2);
  assert.equal(got, "OK");
  assert.equal(ui.last().s, "hidden");
});

test("Close on a failure gives up: the editor is told, and gets the question without the starter", async () => {
  const QL = loaderPage();
  const ui = card();
  let why = null;
  QL.load({ ui, work: async () => { throw failure("server", { status: 500 }); }, onGiveUp: (w) => (why = w) });
  await wait(10);
  ui.last().h.close();
  assert.equal(why, "failed");
  assert.equal(ui.last().s, "hidden");
});

test("it gives up after the time limit, stops the request, and says it took too long", async () => {
  const QL = loaderPage();
  const ui = card();
  let aborted = false;
  QL.load({
    ui, timeoutMs: 40, slowMs: 1000,
    work: (signal) => new Promise((_, reject) => signal.addEventListener("abort", () => { aborted = true; reject(Object.assign(new Error("aborted"), { name: "AbortError" })); })),
  });
  await wait(90);
  assert.equal(aborted, true, "the request was not dropped");
  assert.equal(ui.last().s, "failed");
  assert.match(ui.last().d.title, /taking too long/);
});

test("after a while it says it is still working", async () => {
  const QL = loaderPage();
  const ui = card();
  QL.load({ ui, timeoutMs: 1000, slowMs: 30, work: () => new Promise(() => {}) });
  assert.equal(ui.last().s, "loading");
  await wait(70);
  assert.equal(ui.last().s, "slow");
  assert.match(ui.last().title, /Getting your question ready/);
});

test("Cancel stops the request and says nothing more than that it was cancelled", async () => {
  const QL = loaderPage();
  const ui = card();
  let aborted = false, why = null;
  QL.load({
    ui, onGiveUp: (w) => (why = w),
    work: (signal) => new Promise((_, reject) => signal.addEventListener("abort", () => { aborted = true; reject(Object.assign(new Error("aborted"), { name: "AbortError" })); })),
  });
  ui.last().onCancel();
  await wait(10);
  assert.equal(aborted, true);
  assert.equal(why, "cancelled");
  assert.equal(ui.last().s, "hidden", "a cancel is not an error: no failure card");
  assert.ok(!ui.log.some((e) => e.s === "failed"));
});

test("a newer load takes over: the older one is stopped and shows nothing", async () => {
  const QL = loaderPage();
  const ui = card();
  let firstAborted = false, firstReady = false, secondGot = null;
  QL.load({
    ui, title: "First",
    work: (signal) => new Promise((_, reject) => signal.addEventListener("abort", () => { firstAborted = true; reject(Object.assign(new Error("aborted"), { name: "AbortError" })); })),
    onReady: () => (firstReady = true),
  });
  QL.load({ ui, title: "Second", work: async () => "B", onReady: (v) => (secondGot = v) });
  await wait(20);
  assert.equal(firstAborted, true);
  assert.equal(firstReady, false);
  assert.equal(secondGot, "B");
  assert.ok(!ui.log.some((e) => e.s === "failed"), "the older load must not show an error");
  assert.equal(ui.last().s, "hidden");
});

test("a work function that throws at once is a failure on the card, not an uncaught error", async () => {
  const QL = loaderPage();
  const ui = card();
  QL.load({ ui, work: () => { throw failure("offline"); } });
  await wait(10);
  assert.equal(ui.last().s, "failed");
  assert.match(ui.last().d.title, /Couldn't reach OpenREPL/);
});

// ---- reading the model's answer ----------------------------------------------------

test("the template is found under the language's key, however the model wrote it", () => {
  const QL = loaderPage();
  const t = { template: "int main() {}", multiline_comment_start: "/*", multiline_comment_end: "*/" };
  const read = (answer, lang = "C") => QL.pickTemplate(answer, lang);
  assert.equal(read({ description: "d", C: t }).template, "int main() {}");
  assert.equal(read({ description: "d", c: t }).template, "int main() {}", "a lower case key");
  assert.equal(read({ description: "d", "C ": t }).template, "int main() {}", "a key with a space");
  assert.equal(read({ description: "d", templates: { c: t } }).template, "int main() {}", "one level down");
  assert.equal(read({ description: "d", code_templates: { C: t } }).template, "int main() {}");
  assert.equal(read({ description: "d", template: "int main() {}" }).template, "int main() {}", "the template as the whole answer");
  assert.equal(read({ description: "d", c_language: t }).template, "int main() {}", "the only template, under another name");
  assert.equal(read({ description: "d", "C++": t }, "C++").template, "int main() {}");
  // the answer for another language is not the one that was asked for
  assert.equal(read({ description: "d", "C++": t }, "C"), null);
  assert.equal(read({ description: "d", Python: t }, "C"), null);
});

test("a template with no comment marks gets the language's", () => {
  const QL = loaderPage();
  assert.deepEqual({ ...QL.pickTemplate({ Python: { template: "def f(): pass" } }, "Python") }, { template: "def f(): pass", multiline_comment_start: '"""', multiline_comment_end: '"""' });
  assert.equal(QL.pickTemplate({ C: { template: "x" } }, "C").multiline_comment_start, "/*");
  assert.equal(QL.pickTemplate({ Ruby: { template: "x", multiline_comment_start: "", multiline_comment_end: "=end" } }, "Ruby").multiline_comment_start, "=begin");
});

test("an answer with no template at all is not one", () => {
  const QL = loaderPage();
  for (const answer of [null, undefined, "text", 5, [], {}, { description: "only a description" }, { C: "not an object" }, { C: { template: 5 } }, { a: { template: "x" }, b: { template: "y" } }]) {
    assert.equal(QL.pickTemplate(answer, "C"), null, JSON.stringify(answer));
  }
});
