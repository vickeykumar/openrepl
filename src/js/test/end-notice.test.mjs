// The end of a terminal reaches the page twice: as an "end notice" message (type 7) and as the WebSocket close
// frame. A proxy in front of the server (Render's edge) can hold the close frame back for ~20 s or lose it, so
// the notice alone must be enough, and the two together must act once. The page code is a class that needs a
// browser, so this checks the shape of the code that guarantees both.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const src = fs.readFileSync(path.join(here, "..", "src", "webtty.ts"), "utf8").replace(/\r\n/g, "\n");
const go = fs.readFileSync(path.join(here, "..", "..", "webtty", "message_types.go"), "utf8");

test("the message type matches the server's", () => {
  assert.match(go, /End = '7'/);
  assert.match(src, /export const msgEnd = '7'/);
});

test("an end notice runs the same handler as the close event", () => {
  assert.match(src, /case msgEnd:[\s\S]*?onEnded\(\{ code: end\.code, reason: end\.reason \}\)/);
  assert.match(src, /connection\.onClose\(onEnded\)/);
});

test("the handler acts once, whichever comes first", () => {
  assert.match(src, /const onEnded = \(closeEvent\) => \{\n\s+if \(ended\) \{\n\s+return;\n\s+\}\n\s+ended = true;/);
});

test("the flag is per connection, so a reconnect handles its own end", () => {
  assert.match(src, /const setup = \(\) => \{[\s\S]*?let ended = false;/);
});
