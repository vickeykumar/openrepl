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
