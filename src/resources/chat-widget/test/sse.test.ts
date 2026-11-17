// node --test test/   (from src/resources/chat-widget)
import test from "node:test";
import assert from "node:assert/strict";
import { SSEParser, addEvent, answerOf, newStreamed, partialSay } from "../src/sse.ts";

const ev = (o: unknown) => "data: " + JSON.stringify(o) + "\n\n";

test("events are found whole however the text is cut", () => {
  const text = ev({ choices: [{ delta: { content: "Hel" } }] }) + ev({ choices: [{ delta: { content: "lo" } }] }) + "data: [DONE]\n\n";
  for (const size of [1, 3, 7, 50, text.length]) {
    const p = new SSEParser();
    const got: any[] = [];
    for (let i = 0; i < text.length; i += size) got.push(...p.feed(text.slice(i, i + size)));
    assert.equal(got.length, 2, "cut every " + size);
    assert.equal(got[1].choices[0].delta.content, "lo");
  }
});

test("a comment line, a CRLF and an event that is not JSON do not break the stream", () => {
  const p = new SSEParser();
  const got = p.feed(": keep-alive\r\n\r\ndata: not json\r\n\r\ndata: {\"a\":1}\r\n\r\n");
  assert.deepEqual(got, [{ a: 1 }]);
  assert.deepEqual(new SSEParser().feed("data: {\"x\":1}"), [], "an event that is not closed waits");
  const q = new SSEParser();
  q.feed("data: {\"x\":2}");
  assert.deepEqual(q.rest(), [{ x: 2 }]);
});

test("the content, the call, the usage and the reason it ended are put together", () => {
  const s = newStreamed();
  assert.ok(addEvent(s, { choices: [{ delta: { content: "Hel" } }] }));
  assert.ok(addEvent(s, { choices: [{ delta: { content: "lo" }, finish_reason: "stop" }] }));
  assert.ok(!addEvent(s, { choices: [], usage: { prompt_tokens: 5, completion_tokens: 2 } }));
  assert.equal(s.content, "Hello");
  assert.equal(s.finish, "stop");
  assert.deepEqual(s.usage, { prompt_tokens: 5, completion_tokens: 2 });
  assert.equal(answerOf(s), "Hello");
  const t = newStreamed();
  addEvent(t, { choices: [{ delta: { tool_calls: [{ index: 0, function: { name: "step", arguments: '{"say":' } }] } }] });
  addEvent(t, { choices: [{ delta: { tool_calls: [{ index: 0, function: { arguments: '"hi"}' } }] } }] });
  assert.equal(answerOf(t), '{"say":"hi"}');
  const e = newStreamed();
  addEvent(e, { error: { message: "the host is busy" } });
  assert.equal(e.error, "the host is busy");
  assert.ok(!addEvent(e, null));
});

test("the words of an agent step are read while its JSON is being written", () => {
  assert.equal(partialSay(""), "");
  assert.equal(partialSay('{"pla'), "");
  assert.equal(partialSay('{"say":"I will'), "I will");
  assert.equal(partialSay('{"say": "I will look at it.", "actions": ['), "I will look at it.");
  assert.equal(partialSay('{"say":"a \\"quoted\\" word\\nnext'), 'a "quoted" word\nnext');
  assert.equal(partialSay('{"say":"cut \\'), "cut ");
  assert.equal(partialSay('{"say":"caf\\u00e9 \\u00'), "café ");
  assert.equal(partialSay('```json\n{"plan":["x"],"say":"after the plan"'), "after the plan");
});
