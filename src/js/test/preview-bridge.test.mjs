// The bridge scripts of the preview (src/livepreview/rewrite.go, bridge_cmd.go) are Go strings that the browser runs: a syntax error
// in one would break every previewed page and show only in a browser. This reads them out of the Go source and parses them.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "livepreview");

function scripts(file) {
  const src = fs.readFileSync(path.join(dir, file), "utf8");
  const out = [];
  const re = /<script>([\s\S]*?)<\/script>/g;
  let m;
  while ((m = re.exec(src))) out.push(m[1].replace(/` \+ "`" \+ `/g, "`"));
  return out;
}

test("the scripts of the bridge are valid JavaScript", () => {
  const all = scripts("bridge_cmd.go").concat(scripts("rewrite.go"));
  assert.ok(all.length >= 3, "the scripts were found: " + all.length);
  for (const code of all) {
    // the client script of layer two has a Go string in it ("P=" + jsString(prefix)): it is not a whole script on its own
    if (code.includes('var P=')) continue;
    assert.doesNotThrow(() => new Function(code), code.slice(0, 80));
  }
});

test("the command script has every command the agent uses", () => {
  const code = scripts("bridge_cmd.go")[0];
  for (const name of ["read", "target", "click", "type", "press", "scroll", "select", "hover", "wait", "inspect", "eval", "storage"]) {
    assert.ok(code.includes("C." + name + "="), "command " + name);
  }
  assert.ok(code.includes("e.source!==parent"), "a command is taken only from the page that holds the frame");
});
