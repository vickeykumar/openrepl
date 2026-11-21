// The permissions of the agent (agent-protocol.ts, "agent permissions"): what is allowed for the session, the riskier level with its time,
// and the floor that nothing lifts. node --test test/
import test from "node:test";
import assert from "node:assert/strict";
import { Permissions, hardBlockOf, riskKindOf, RISK_PHRASE, UNDO_LIMIT, EVERYDAY_SCOPES } from "../src/agent-protocol.ts";

const type = (text: string) => ({ type: "terminal_type" as const, text });
const file = (t: string, path: string, extra: any = {}) => ({ type: t as any, path, ...extra });

test("by default every action is asked about", () => {
  const p = new Permissions();
  for (const a of [type("ls"), { type: "run" as const }, { type: "editor_write" as const, text: "x" }, file("files_open", "a.py")]) assert.equal(p.decide(a), "ask");
  assert.equal(p.auto(), false);
  assert.equal(p.autoApply(), false);
});

test("allowing one kind for the session lets that kind go, and only it", () => {
  const p = new Permissions();
  p.grant("run");
  assert.equal(p.decide({ type: "run" }), "auto");
  assert.equal(p.decide({ type: "debug" }), "auto");
  assert.equal(p.decide(type("ls")), "ask");
  assert.equal(p.autoApply(), false); // the editor's changes are still reviewed
  p.revoke("run");
  assert.equal(p.decide({ type: "run" }), "ask");
});

test("allow everything lets every everyday kind go, applies the editor's changes at once, and stop auto mode takes it all back", () => {
  const p = new Permissions();
  assert.equal(p.grantEverything(), true);
  for (const s of EVERYDAY_SCOPES) assert.equal(p.has(s), true, s);
  assert.equal(p.decide(type("python main.py")), "auto");
  assert.equal(p.decide(file("files_new", "a.py")), "auto");
  assert.equal(p.decide({ type: "editor_write", text: "x" }), "auto");
  assert.equal(p.autoApply(), true);
  assert.equal(p.auto(), true);
  p.stopAuto();
  assert.equal(p.decide({ type: "run" }), "ask");
  assert.equal(p.autoApply(), false);
  assert.equal(p.auto(), false);
});

test("a switch of the badge menu turns one kind off again, and the editor's off also ends the auto-apply", () => {
  const p = new Permissions();
  p.grantEverything();
  p.revoke("terminal");
  assert.equal(p.decide(type("ls")), "ask");
  assert.equal(p.decide({ type: "run" }), "auto");
  p.revoke("editor");
  assert.equal(p.autoApply(), false);
  assert.equal(p.has("editor"), false);
});

test("risky things are asked about, whatever the everyday level allows", () => {
  const p = new Permissions();
  p.grantEverything();
  for (const a of [type("rm old.txt"), type("git reset --hard"), file("files_delete", "a.py"), file("files_rename", "a.py", { name: "b.py" }), file("files_move", "a.py", { to: "src" }), file("files_paste", "", { to: "src" }), { type: "terminal_close_tab" as const, tab: 2 }]) {
    assert.equal(p.decide(a), "ask", JSON.stringify(a));
  }
  assert.equal(riskKindOf(type("ls")), null);
  assert.equal(riskKindOf(type("rm a")), "terminal");
  assert.equal(riskKindOf(file("files_delete", "a")), "delete");
  assert.equal(riskKindOf(file("files_move", "a", { to: "b" })), "move");
});

test("the riskier level allows only the kinds chosen, only for its time, and says it is the riskier level", () => {
  let now = 1_000_000;
  const p = new Permissions(() => ({}), () => now);
  assert.equal(p.allowRisky(["delete", "move"], 30), true);
  assert.equal(p.riskyOn(), true);
  assert.equal(p.decide(file("files_delete", "a.py")), "risky");
  assert.equal(p.decide(file("files_rename", "a.py", { name: "b.py" })), "risky");
  assert.equal(p.decide(type("rm a.txt")), "ask"); // not a chosen kind
  assert.equal(p.decide({ type: "terminal_close_tab", tab: 2 }), "ask");
  assert.equal(p.riskyMinutesLeft(), 30);
  now += 10 * 60000;
  assert.equal(p.riskyMinutesLeft(), 20);
  now += 20 * 60000 + 1;
  assert.equal(p.riskyOn(), false);
  assert.equal(p.decide(file("files_delete", "a.py")), "ask");
  assert.deepEqual(p.riskyKinds(), []);
});

test("until the page closes has no end, and stopping the riskier level is immediate", () => {
  const p = new Permissions();
  p.allowRisky(["terminal"], 0);
  assert.equal(p.riskyUntilClose(), true);
  assert.equal(p.riskyMinutesLeft(), 0);
  assert.equal(p.decide(type("rm notes.txt")), "risky");
  p.stopRisky();
  assert.equal(p.decide(type("rm notes.txt")), "ask");
  assert.equal(p.allowRisky([], 30), false); // no kind chosen
  assert.equal(RISK_PHRASE, "allow risky");
});

test("what is never lifted: sudo, wiping the home folder or the system, outside the workspace, hidden files, the parent folder", () => {
  const blocked = [
    "sudo rm a.txt", "rm -rf /", "rm -rf ~", "rm -rf $HOME", "rm -rf .", "rm -fr *", "rm -rf /home/openrepl/..",
    "dd if=/dev/zero of=/dev/sda", "mkfs.ext4 /dev/sda1", ":(){ :|:& };:", "chmod -R 777 /", "rm /etc/passwd", "mv a.txt /etc/",
    "rm ../other.txt", "rm .bashrc", "mv .ssh x", "cp a.txt ../../x", "echo hi > /dev/sda",
  ];
  for (const line of blocked) assert.notEqual(hardBlockOf(type(line)), "", line);
  const fine = ["rm notes.txt", "mv a.txt src/", "rm -r build", "git reset --hard", "rm /tmp/x.txt", "rm /home/openrepl/guest-1/a.txt", "ls -la"];
  for (const line of fine) assert.equal(hardBlockOf(type(line)), "", line);
  const p = new Permissions();
  p.allowRisky(["terminal", "delete", "move", "tab"], 30);
  assert.equal(p.decide(type("sudo rm a.txt")), "ask");
  assert.equal(p.decide(type("rm .bashrc")), "ask");
  assert.equal(p.decide(type("rm notes.txt")), "risky");
  assert.equal(p.decide(file("files_delete", ".hidden/x")), "ask");
  assert.equal(p.decide(file("files_move", "a/.git/config", { to: "b" })), "ask");
  assert.equal(p.decide(file("files_delete", "src/a.py")), "risky");
  assert.equal(p.decide(file("files_delete", "Trash/old.py")), "ask"); // emptying the Trash is not undone by the Trash
  assert.equal(p.decide(file("files_delete", "Trash")), "ask");
});

test("a paste of a copy is an everyday action, a paste after a cut is a risky one", () => {
  const p = new Permissions();
  p.grantEverything();
  const paste = file("files_paste", "", { to: "src" });
  assert.equal(p.decide(paste, true), "auto"); // the caller found no risk (a copy)
  assert.equal(p.decide(paste), "ask"); // after a cut
});

test("the admin can switch the auto level and the riskier level off", () => {
  const p = new Permissions(() => ({ autoOff: true, riskyOff: true }));
  assert.equal(p.grantEverything(), false);
  assert.equal(p.setAutoApply(true), false);
  assert.equal(p.allowRisky(["delete"], 30), false);
  assert.equal(p.decide({ type: "run" }), "ask");
  // one kind for the session stays possible: it is the card that exists today
  p.grant("run");
  assert.equal(p.decide({ type: "run" }), "auto");
  assert.equal(p.autoApply(), false);
  const q = new Permissions(() => ({ riskyOff: true }));
  assert.equal(q.grantEverything(), true);
  assert.equal(q.allowRisky(["delete"], 30), false);
});

test("the editor's changes that went in without a review can be undone, newest first, the last ten", () => {
  const p = new Permissions();
  const undone: number[] = [];
  for (let i = 1; i <= UNDO_LIMIT + 3; i++) p.pushUndo({ label: "change " + i, undo: () => (undone.push(i), true), done: false });
  assert.equal(p.undoable().length, UNDO_LIMIT);
  assert.equal(p.undoLast()!.label, "change " + (UNDO_LIMIT + 3));
  assert.equal(p.undoLast()!.label, "change " + (UNDO_LIMIT + 2));
  assert.deepEqual(undone, [UNDO_LIMIT + 3, UNDO_LIMIT + 2]);
  const q = new Permissions();
  q.pushUndo({ label: "stuck", undo: () => false, done: false });
  q.undoLast();
  assert.equal(q.undoable().length, 1); // it could not be undone, so it is still listed
});

test("bash is an everyday scope: safe commands follow the grant, risky ones ask", () => {
  const p = new Permissions(() => ({}));
  const ls = { type: "bash" as const, text: "ls -la", timeoutSeconds: 30 };
  assert.equal(p.decide(ls), "ask");
  p.grant("shell");
  assert.equal(p.decide(ls), "auto");
  assert.equal(p.decide({ ...ls, text: "rm -r build" }), "ask");
  assert.equal(p.decide({ ...ls, text: "sudo ls" }), "ask");
});

// ---- connections (MCP): the kinds of call, and text from outside ----
import { parseStep as parseStepForMcp, scopeOf as scopeOfMcp, scopeQuestion as scopeQuestionMcp, scopeDetail as scopeDetailMcp, riskyAfterOutside as riskyAfterOutsideMcp, Permissions as PermissionsMcp, EVERYDAY_SCOPES as EVERYDAY_MCP } from "../src/agent-protocol.ts";

const mcp = (level: "read" | "write" | "irreversible" | undefined, tool = "create_issue") => ({ type: "mcp_call" as const, connection: "github", tool, args: { title: "x" }, level, title: "GitHub" });

test("a call to a connection is a read, a write or a thing that cannot be undone, by the tool's own level, and a tool of no known level is a write", () => {
  assert.equal(scopeOfMcp(mcp("read", "get_issue")), "mcpread");
  assert.equal(scopeOfMcp(mcp("write")), "mcpwrite");
  assert.equal(scopeOfMcp(mcp("irreversible", "merge_pull_request")), "mcpdanger");
  assert.equal(scopeOfMcp(mcp(undefined)), "mcpwrite");
  assert.match(scopeQuestionMcp("mcpread"), /read/);
  assert.match(scopeQuestionMcp("mcpdanger"), /cannot be undone/);
  assert.match(scopeDetailMcp("mcpwrite", mcp("write")), /write to GitHub as you/);
  assert.match(scopeDetailMcp("mcpwrite", mcp("write")), /create_issue/);
});

test("the model's mcp_call is read with its limits, and what the page decides (level, name) is not taken from it", () => {
  const step = (a: unknown) => {
    const r = parseStepForMcp(JSON.stringify({ say: "x", actions: [a], done: false }));
    assert.ok(r.ok);
    return (r as any).step as { actions: any[]; dropped: string[] };
  };
  const ok = step({ type: "mcp_call", connection: "GitHub", tool: "create_issue", args: { title: "t" }, level: "read", title: "Evil" });
  assert.equal(ok.actions.length, 1);
  assert.equal(ok.actions[0].connection, "github");
  assert.equal(ok.actions[0].level, undefined, "the level is the page's, from the connection's own list");
  assert.equal(ok.actions[0].title, undefined);
  for (const bad of [{ connection: "", tool: "t" }, { connection: "github", tool: "../x" }, { connection: "github", tool: "t", args: [1] }, { connection: "github", tool: "t", args: "x" }, { connection: "github", tool: "t", args: { a: "x".repeat(17000) } }]) {
    const r = step({ type: "mcp_call", ...bad });
    assert.equal(r.actions.length, 0, JSON.stringify(bad).slice(0, 80));
    assert.ok(r.dropped.some((d) => d.includes("mcp_call left out")));
  }
});

test("a read can be allowed for the session, a write too, and what cannot be undone is asked every time", () => {
  const p = new PermissionsMcp();
  assert.equal(p.decide(mcp("read", "get_issue")), "ask");
  p.grant("mcpread");
  assert.equal(p.decide(mcp("read", "get_issue")), "auto");
  assert.equal(p.decide(mcp("write")), "ask");
  p.grant("mcpwrite");
  assert.equal(p.decide(mcp("write")), "auto");
  p.grant("mcpdanger");
  assert.equal(p.decide(mcp("irreversible", "merge_pull_request")), "ask", "no grant lifts it");
  const all = new PermissionsMcp();
  all.grantEverything();
  assert.equal(all.decide(mcp("read", "get_issue")), "auto");
  assert.equal(all.decide(mcp("write")), "auto", "Allow everything covers writes");
  assert.equal(all.decide(mcp("irreversible", "merge_pull_request")), "ask");
  assert.ok(EVERYDAY_MCP.includes("mcpread") && EVERYDAY_MCP.includes("mcpwrite") && !EVERYDAY_MCP.includes("mcpdanger" as any));
});

test("taint guard: after text from outside a risky action asks again, whatever was allowed, and a new task is clean", () => {
  const p = new PermissionsMcp();
  p.grantEverything();
  const bash = { type: "bash" as const, text: "ls" };
  const del = { type: "files_delete" as const, path: "a.txt" };
  const evalA = { type: "preview_eval" as const, text: "1+1" };
  assert.equal(p.decide(bash), "auto");
  assert.equal(p.decide(mcp("write")), "auto");
  p.markOutside();
  assert.equal(p.isOutside(), true);
  assert.equal(p.decide(bash), "ask", "a shell command");
  assert.equal(p.decide(del), "ask", "a delete");
  assert.equal(p.decide(mcp("write")), "ask", "a write to a connection");
  assert.equal(p.decide(evalA), "ask", "code in the page");
  // reading and the everyday things are not asked again
  assert.equal(p.decide(mcp("read", "get_issue")), "auto");
  assert.equal(p.decide({ type: "files_open" as const, path: "a.txt" }), "auto");
  assert.ok(riskyAfterOutsideMcp(bash) && riskyAfterOutsideMcp(evalA) && !riskyAfterOutsideMcp(mcp("read", "get_issue")));
  p.clearOutside();
  assert.equal(p.decide(bash), "auto");
});
