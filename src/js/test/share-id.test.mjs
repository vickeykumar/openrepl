// Which part of the address is a share id (src/share-id.ts), and what the page
// script does with it (src/page/01-session.js). The rule is TypeScript, so it is
// compiled here with the compiler the bundle is built with.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const ts = require("typescript");
const js = (file) =>
  ts.transpileModule(fs.readFileSync(path.join(here, "..", "src", file), "utf8"), { compilerOptions: { module: ts.ModuleKind.ES2020, target: ts.ScriptTarget.ES2017 } }).outputText;
const { shareIdFrom, SHARE_ID_PATTERN } = await import("data:text/javascript;base64," + Buffer.from(js("share-id.ts")).toString("base64"));

// keys of the Firebase SDK's own generator (firebase-database.js), run with the clock of 2014, 2017, 2025 and now
const REAL = ["-JOJiRV-a8RYmm_zDNlx", "-KeGSp--p2fz1h5yyPnM", "-OJVdrF-2UPpWUafqBoU", "-P3WCLWG1hm_ZMkP2Al3"];

test("a share link is the Firebase push key after the #", () => {
  for (const id of REAL) {
    assert.equal(shareIdFrom("#" + id), id);
    assert.equal(shareIdFrom(id), id, "location.hash always has the #, but the rule does not need it");
  }
});

test("the anchors of the page are not share links", () => {
  for (const anchor of ["#languages", "#repl", "#workspace", "#request", "#faq", "#", "", "#-", "#-Nx3kQ7"]) {
    assert.equal(shareIdFrom(anchor), "", anchor);
  }
});

test("anything that is not the shape of a key is left out", () => {
  for (const bad of ["#-OJVdrF-2UPpWUafqBoU?x=1", "#-OJVdrF-2UPpWUafqBoU/", "# -OJVdrF-2UPpWUafqBoU", "#--" + "a".repeat(40), "#OJVdrF-2UPpWUafqBoU", "#-ab cd"]) {
    assert.equal(shareIdFrom(bad), "", bad);
  }
  assert.equal(shareIdFrom(null), "");
  assert.equal(shareIdFrom(undefined), "");
  assert.ok(SHARE_ID_PATTERN.test("-" + "x".repeat(12)) && !SHARE_ID_PATTERN.test("-" + "x".repeat(11)) && !SHARE_ID_PATTERN.test("-" + "x".repeat(31)));
});

// ---- the page script, run with a pretend page ----------------------------------

function page(hash) {
  const listeners = {};
  const location = { hash, reloads: 0, reload() { this.reloads++; } };
  const window = { location, innerWidth: 1200, innerHeight: 800, addEventListener: (t, f) => (listeners[t] = f) };
  const source = fs
    .readFileSync(path.join(here, "..", "src", "page", "01-session.js"), "utf8")
    .replace(/^import \{ shareIdFrom \} from "..\/share-id";$/m, "")
    .replace(/^export \{\};.*$/m, "");
  const ctx = {
    window,
    shareIdFrom,
    firebase: { database: () => ({ ref: () => ({ push: () => ({ key: "-NEWKEYFROMPUSH12345" }) }) }) },
    document: {},
    $: () => ({ on() {} }),
    console,
  };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return { window, location, change: (h) => { location.hash = h; listeners.hashchange && listeners.hashchange(); } };
}

test("a page with no hash, or with an anchor, is the master and makes its own session", () => {
  for (const hash of ["", "#languages", "#workspace", "#request", "#repl"]) {
    const p = page(hash);
    assert.equal(p.window.isMaster(), true, "hash " + JSON.stringify(hash));
    assert.equal(p.window.getExampleRef(), "-NEWKEYFROMPUSH12345", "it must start a session of its own, not read the anchor");
  }
});

test("a page opened with a share link is a viewer of that session", () => {
  const p = page("#" + REAL[2]);
  assert.equal(p.window.isMaster(), false);
  assert.equal(p.window.getExampleRef(), REAL[2]);
});

test("clicking an anchor does not change what the page is", () => {
  const master = page("");
  master.change("#languages");
  assert.equal(master.window.isMaster(), true);
  assert.equal(master.location.reloads, 0);

  const viewer = page("#" + REAL[0]);
  viewer.change("#languages");
  assert.equal(viewer.window.isMaster(), false, "the viewer's session must not turn into a master's");
  assert.equal(viewer.window.getExampleRef(), REAL[0]);
  assert.equal(viewer.location.reloads, 0);
});

test("another share link pasted into the address bar loads that session", () => {
  const viewer = page("#" + REAL[0]);
  viewer.change("#" + REAL[1]);
  assert.equal(viewer.location.reloads, 1);
  const master = page("");
  master.change("#" + REAL[3]);
  assert.equal(master.location.reloads, 1);
  // the same link again is not news
  const same = page("#" + REAL[0]);
  same.change("#" + REAL[0]);
  assert.equal(same.location.reloads, 0);
});
