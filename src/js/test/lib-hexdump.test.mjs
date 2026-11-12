// The hex dump of a binary file (src/page/lib-hexdump.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { hexDump } from "../src/page/lib-hexdump.mjs";

const bytes = (...a) => Uint8Array.from(a);

test("a short line: the offset, the bytes, and the text beside them", () => {
  const out = hexDump(Uint8Array.from(Buffer.from("ELF\0\x01binary")));
  assert.equal(out, "00000000  45 4c 46 00 01 62 69 6e  61 72 79                 |ELF..binary|");
});

test("sixteen bytes make a line, with a gap after the eighth, and the next line carries on the offset", () => {
  const data = Uint8Array.from({ length: 20 }, (_, i) => i);
  const lines = hexDump(data).split("\n");
  assert.equal(lines.length, 2);
  assert.equal(lines[0], "00000000  00 01 02 03 04 05 06 07  08 09 0a 0b 0c 0d 0e 0f  |................|");
  assert.ok(lines[1].startsWith("00000010  10 11 12 13 "));
});

test("the offset starts where the piece starts in the file", () => {
  assert.ok(hexDump(bytes(65, 66), 0x1000).startsWith("00001000  41 42 "));
  assert.ok(hexDump(bytes(65), 0x123456789).startsWith("123456789  41 "), "past 8 digits it just gets longer");
});

test("printable characters are shown and everything else is a dot", () => {
  assert.ok(hexDump(bytes(0x1f, 0x20, 0x41, 0x7e, 0x7f, 0x80, 0xff)).endsWith("|. A~...|"));
});

test("nothing gives nothing", () => {
  assert.equal(hexDump(new Uint8Array(0)), "");
});

test("a big piece is dumped in one go", () => {
  const data = new Uint8Array(256 * 1024).fill(0xab);
  const out = hexDump(data);
  assert.equal(out.split("\n").length, 16384);
});
