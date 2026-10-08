// The built bundle (dist/index.umd.js) must not use a name that nothing declares.
//
// microbundle rewrites async functions into promise chains, and for some shapes
// (a `break` in an async loop inside a try/finally) it has emitted a helper it
// never declared: "_interrupt4 is not defined", at run time and only on the path
// that reached it. TypeScript and the other tests cannot see that, because they
// read the source. This reads the bundle.
//
// Run `npm run build` first; without a bundle the test is skipped.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "acorn";

const here = path.dirname(fileURLToPath(import.meta.url));
const bundle = path.join(here, "..", "dist", "index.umd.js");

// what a browser page, or the UMD wrapper, provides
const GLOBALS = new Set(
  (
    "window document navigator location console fetch setTimeout clearTimeout setInterval clearInterval requestAnimationFrame cancelAnimationFrame " +
    "localStorage sessionStorage crypto atob btoa alert confirm prompt getComputedStyle matchMedia history screen performance " +
    "Object Array String Number Boolean Symbol BigInt Math JSON Date RegExp Error TypeError RangeError SyntaxError ReferenceError EvalError URIError AggregateError " +
    "Promise Map Set WeakMap WeakSet Proxy Reflect Intl Function " +
    "parseInt parseFloat isNaN isFinite encodeURIComponent decodeURIComponent encodeURI decodeURI escape unescape " +
    "undefined NaN Infinity globalThis self arguments " +
    "Element HTMLElement HTMLInputElement HTMLTextAreaElement HTMLSelectElement HTMLButtonElement HTMLFormElement HTMLAnchorElement Node NodeList Document DocumentFragment " +
    "Event CustomEvent KeyboardEvent MouseEvent FocusEvent InputEvent MutationObserver ResizeObserver IntersectionObserver " +
    "AbortController AbortSignal Headers Request Response URL URLSearchParams FormData Blob File FileReader TextDecoder TextEncoder " +
    "Uint8Array Uint16Array Uint32Array Int8Array Int16Array Int32Array Float32Array Float64Array ArrayBuffer DataView " +
    "module exports define require firebase DOMParser XMLSerializer Image Audio queueMicrotask structuredClone WebSocket Worker CSS ShadowRoot"
  ).split(/\s+/)
);

// The names a pattern declares (a parameter, a var, a destructuring target).
function declared(pattern, into) {
  if (!pattern) return;
  switch (pattern.type) {
    case "Identifier":
      into.add(pattern.name);
      break;
    case "ObjectPattern":
      pattern.properties.forEach((p) => declared(p.type === "RestElement" ? p.argument : p.value, into));
      break;
    case "ArrayPattern":
      pattern.elements.forEach((e) => declared(e, into));
      break;
    case "RestElement":
      declared(pattern.argument, into);
      break;
    case "AssignmentPattern":
      declared(pattern.left, into);
      break;
  }
}

const isFunction = (n) => n.type === "FunctionDeclaration" || n.type === "FunctionExpression" || n.type === "ArrowFunctionExpression";

// Everything declared directly in a function body (or the program): vars and
// functions are hoisted to it from any depth that is not another function; for
// this check let, const and class are treated the same way, which can only make
// it miss an undeclared name, never report a declared one.
function hoisted(node, into) {
  const visit = (n) => {
    if (!n || typeof n.type !== "string") return;
    if (n.type === "VariableDeclaration") n.declarations.forEach((d) => declared(d.id, into));
    if (n.type === "FunctionDeclaration" || n.type === "ClassDeclaration") {
      if (n.id) into.add(n.id.name);
      if (n.type === "FunctionDeclaration") return; // its body is its own scope
    }
    if (n.type === "CatchClause") declared(n.param, into);
    if (isFunction(n)) return;
    for (const key of Object.keys(n)) {
      if (key === "type" || key === "start" || key === "end") continue;
      const v = n[key];
      if (Array.isArray(v)) v.forEach(visit);
      else if (v && typeof v.type === "string") visit(v);
    }
  };
  if (isFunction(node)) {
    if (node.body.type === "BlockStatement") node.body.body.forEach(visit);
  } else {
    node.body.forEach(visit);
  }
}

// undeclaredNames returns the names the code reads or writes that no enclosing
// scope declares, with the place of the first use of each.
export function undeclaredNames(code) {
  const ast = parse(code, { ecmaVersion: "latest", sourceType: "script" });
  const missing = new Map();
  const walk = (node, scopes) => {
    if (!node || typeof node.type !== "string") return;
    if (isFunction(node) || node.type === "Program") {
      const scope = new Set();
      if (isFunction(node)) {
        if (node.id) scope.add(node.id.name);
        node.params.forEach((p) => declared(p, scope));
      }
      hoisted(node, scope);
      scopes = scopes.concat([scope]);
    }
    if (node.type === "ClassExpression" && node.id) scopes = scopes.concat([new Set([node.id.name])]);
    if (node.type === "Identifier") {
      if (!GLOBALS.has(node.name) && !scopes.some((s) => s.has(node.name)) && !missing.has(node.name)) missing.set(node.name, node.start);
      return;
    }
    for (const key of Object.keys(node)) {
      if (key === "type" || key === "start" || key === "end") continue;
      // names that are not references: a.b, {b: 1}, a label, a method name
      if (node.type === "MemberExpression" && key === "property" && !node.computed) continue;
      if ((node.type === "Property" || node.type === "MethodDefinition" || node.type === "PropertyDefinition") && key === "key" && !node.computed) continue;
      if ((node.type === "LabeledStatement" || node.type === "BreakStatement" || node.type === "ContinueStatement") && key === "label") continue;
      const v = node[key];
      if (Array.isArray(v)) v.forEach((c) => walk(c, scopes));
      else if (v && typeof v.type === "string") walk(v, scopes);
    }
  };
  walk(ast, []);
  return missing;
}

test("the checker itself finds what is not declared, and only that", () => {
  const found = (code) => Array.from(undeclaredNames(code).keys()).sort();
  assert.deepEqual(found("var a = 1; function f(b) { return a + b + c; }"), ["c"]);
  assert.deepEqual(found("function f() { try { x(); } catch (e) { return e; } } function x() {}"), []);
  assert.deepEqual(found("var o = { key: 1 }; o.other; l: for (;;) { break l; }"), []);
  assert.deepEqual(found("(function (n) { if (_interrupt4 || n) return n; })(1)"), ["_interrupt4"]);
  assert.deepEqual(found("function f() { g(); function g() { return h; } var h = 2; }"), []);
  assert.deepEqual(found("var { a, b: [c, ...d] } = window; a + c + d + e;"), ["e"]);
  assert.deepEqual(found("const f = (x = y) => x; let y = 1; class K { m() { return K; } }"), []);
});

test("the built bundle uses no name that nothing declares", { skip: !fs.existsSync(bundle) && "no dist/index.umd.js: run `npm run build` first" }, () => {
  const code = fs.readFileSync(bundle, "utf8");
  const missing = undeclaredNames(code);
  const report = Array.from(missing.entries()).map(([name, at]) => name + " near: " + code.slice(Math.max(0, at - 60), at + 40).replace(/\s+/g, " "));
  assert.deepEqual(report, [], "names used in the bundle that nothing declares:\n" + report.join("\n"));
});
