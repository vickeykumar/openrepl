import test from "node:test";
import assert from "node:assert/strict";
import { textToBase64 } from "../src/page/lib-encoding.mjs";

const back = (b64) => new TextDecoder("utf-8").decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));

test("plain text is the same as with btoa", () => {
  assert.equal(textToBase64("hello\n"), btoa("hello\n"));
  assert.equal(textToBase64(""), "");
});

test("characters outside Latin1 are saved, and read back as they were", () => {
  for (const s of ["loading…", "a — b", "→ ✓ ✗", "日本語", "emoji 😀 and 🇮🇳", "Привет", "mixed: é … ü"]) {
    assert.doesNotThrow(() => textToBase64(s), s);
    assert.equal(back(textToBase64(s)), s, s);
  }
});

test("a Latin1 letter is two bytes of UTF-8, as the load expects", () => {
  assert.equal(textToBase64("é"), "w6k="); // 0xC3 0xA9
  assert.notEqual(textToBase64("é"), btoa("é"));
});

test("a big text goes through in pieces and comes back whole", () => {
  const big = "…x".repeat(300000); // 900 KB of UTF-8
  assert.equal(back(textToBase64(big)), big);
});
