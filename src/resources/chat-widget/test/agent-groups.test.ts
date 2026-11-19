import { test } from "node:test";
import assert from "node:assert/strict";
import { addGroups, groupsFor } from "../src/agent-groups.ts";
import { ACTIONS, ACTION_GROUP, GROUPS, parseStep } from "../src/agent-protocol.ts";

const none = { task: "", previewOpen: false, openFile: "", terminal: "", haveEditorText: false };

test("a plain question gets the core only", () => {
  assert.deepEqual(groupsFor([], { ...none, task: "what does OpenREPL do?" }), []);
  assert.deepEqual(groupsFor([], { ...none, task: "hello" }), []);
});

test("the words of the task, and the page, add the groups they say", () => {
  assert.deepEqual(groupsFor([], { ...none, task: "rename the folder src to lib" }), ["files"]);
  assert.ok(groupsFor([], { ...none, task: "install numpy and run it" }).includes("terminal"));
  assert.ok(groupsFor([], { ...none, task: "install numpy and run it" }).includes("store"));
  assert.ok(groupsFor([], { ...none, task: "make the page fit a phone" }).includes("preview"));
  assert.ok(groupsFor([], { ...none, previewOpen: true }).includes("preview"), "the pane is open");
  assert.ok(groupsFor([], { ...none, openFile: "shop/index.html" }).includes("preview"), "a web file is open");
  assert.ok(!groupsFor([], { ...none, openFile: "main.py", task: "explain this" }).includes("preview"));
  for (const task of ["play this new game by launching it", "try it out", "change the colors", "make a dashboard", "add a modal", "show me the demo", "make the app look better"]) {
    assert.ok(groupsFor([], { ...none, task }).includes("preview"), task);
  }
  for (const task of ["explain this loop", "what does a closure do", "rename the variable total"]) {
    assert.ok(!groupsFor([], { ...none, task }).includes("preview"), "not for: " + task);
  }
  assert.ok(groupsFor([], { ...none, openFile: "main.py" }).includes("editor"), "a file is open");
  assert.ok(groupsFor([], { ...none, haveEditorText: true }).includes("editor"));
});

test("a missing module in the terminal brings the terminal and the App Store", () => {
  const g = groupsFor([], { ...none, terminal: "$ python a.py\nModuleNotFoundError: No module named 'flask'\n$ " });
  assert.ok(g.includes("terminal") && g.includes("store"), g.join(","));
});

test("groups only grow, in the fixed order, without doubles or unknown names", () => {
  assert.deepEqual(groupsFor(["preview"], { ...none, task: "hello" }), ["preview"], "a group that came stays");
  assert.deepEqual(addGroups(["terminal"], ["store", "editor", "nonsense", "terminal"]), ["editor", "terminal", "store"]);
  assert.deepEqual(addGroups([], []), []);
});

test("need_tools is read, and the model's names are checked", () => {
  const ok = parseStep(JSON.stringify({ say: "", actions: [{ type: "need_tools", groups: ["terminal", "preview", "terminal", "nonsense"] }], done: false }));
  assert.equal(ok.ok, true);
  if (ok.ok) assert.deepEqual(ok.step.actions[0], { type: "need_tools", groups: ["terminal", "preview"] });
  const one = parseStep(JSON.stringify({ say: "", actions: [{ type: "need_tools", groups: "store" }], done: false }));
  if (one.ok) assert.deepEqual(one.step.actions[0].groups, ["store"], "one name as a string");
  const bad = parseStep(JSON.stringify({ say: "", actions: [{ type: "need_tools", groups: ["nonsense"] }], done: false }));
  assert.equal(bad.ok && bad.step.actions.length, 0);
  assert.ok(bad.ok && bad.step.dropped.some((d: string) => /need_tools left out/.test(d)));
});

test("the order of the groups is the protocol's", () => {
  assert.deepEqual(addGroups([], [...GROUPS]), [...GROUPS]);
  assert.deepEqual(addGroups([], [...GROUPS].reverse()), [...GROUPS]);
});

test("every action is in the core or in one group, and the groups are the server's", () => {
  const core = ["files_list", "files_search", "files_read", "need_tools", "finish"];
  for (const a of ACTIONS) assert.equal(core.includes(a) !== (a in ACTION_GROUP), true, a + " is in the core and in a group, or in neither");
  for (const a of Object.keys(ACTION_GROUP)) assert.ok((ACTIONS as readonly string[]).includes(a), a + " is not an action");
  for (const g of Object.values(ACTION_GROUP)) assert.ok((GROUPS as readonly string[]).includes(g), g);
});
