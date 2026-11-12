// A binary file as a hex dump, for the read-only viewer (24-large-file-viewer.js), without the page, so that node can
// test it (src/js/test). One line per 16 bytes: the offset in hex, the bytes in two groups of eight, and the printable
// characters beside them (a dot for the others), the way `od -A x -t x1z` and `xxd` show a file.

const HEX = [];
for (let i = 0; i < 256; i++) HEX.push((i < 16 ? "0" : "") + i.toString(16));

// bytes: a Uint8Array; base: the file offset of its first byte (shown on each line)
export function hexDump(bytes, base = 0) {
  const lines = [];
  for (let at = 0; at < bytes.length; at += 16) {
    let hex = "";
    let text = "";
    for (let k = 0; k < 16; k++) {
      if (k === 8) hex += " ";
      if (at + k < bytes.length) {
        const b = bytes[at + k];
        hex += HEX[b] + " ";
        text += b >= 32 && b < 127 ? String.fromCharCode(b) : ".";
      } else {
        hex += "   ";
      }
    }
    lines.push((base + at).toString(16).padStart(8, "0") + "  " + hex + " |" + text + "|");
  }
  return lines.join("\n");
}
