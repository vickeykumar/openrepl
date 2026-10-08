// node --test test/   (from src/resources/chat-widget; Node 22.18 or later runs TypeScript as it is)
import test from "node:test";
import assert from "node:assert/strict";
import {
  ACTIONS,
  MAX_ACTIONS,
  MAX_OUTPUT,
  MAX_SAY,
  PAGE_ONLY_LANGUAGES,
  describe,
  endedAtTail,
  extractJSON,
  findLanguage,
  newOutput,
  parseStep,
  programEnded,
  resultsMessage,
  riskOf,
  runPhase,
  scopeDetail,
  scopeOf,
  scopeQuestion,
} from "../src/agent-protocol.ts";

const ok = (content: string) => {
  const r = parseStep(content);
  assert.ok(r.ok, "should parse: " + content);
  return (r as any).step;
};

test("a plain answer, a fenced one and one with talk around it all parse", () => {
  const body = '{"say":"Writing it","actions":[{"type":"run"}],"done":false}';
  for (const text of [body, "```json\n" + body + "\n```", "```\n" + body + "\n```", "Sure! " + body + " Hope that helps.", "  \n" + body + "\n"]) {
    const s = ok(text);
    assert.equal(s.say, "Writing it");
    assert.equal(s.actions.length, 1);
    assert.equal(s.actions[0].type, "run");
    assert.equal(s.done, false);
  }
});

test("braces inside strings do not end the object early", () => {
  const s = ok('talk {"say":"a } b","actions":[{"type":"editor_write","text":"if (x) { y(); }\\n"}],"done":true} more {');
  assert.equal(s.say, "a } b");
  assert.equal(s.actions[0].text, "if (x) { y(); }\n");
  assert.equal(s.done, true);
});

test("what is not an object is refused", () => {
  for (const text of ["", "no json here", "[1,2]", "42", "null", '"a string"', "{not json}", "{"]) {
    const r = parseStep(text);
    assert.equal(r.ok, false, text);
  }
  assert.equal(extractJSON("```json\n[1]\n```"), null);
});

test("defaults: no actions, no words", () => {
  const s = ok("{}");
  assert.deepEqual(s.actions, []);
  assert.equal(s.say, "");
  assert.equal(ok('{"say":5,"done":"yes","actions":null}').say, "");
});

test("a step that asks for nothing ends the task, one whose actions were all refused does not", () => {
  // Genie only said something (or asked the user): going on would use up the
  // steps of the task on more words
  assert.equal(ok("{}").done, true);
  assert.equal(ok('{"say":"Which language do you want?","actions":[],"done":false}').done, true);
  assert.equal(ok('{"say":"Running it","actions":[{"type":"run"}],"done":false}').done, false);
  // the model has to be told what was refused, so the task goes on
  const refused = ok('{"actions":[{"type":"shell","text":"ls"}],"done":false}');
  assert.equal(refused.done, false);
  assert.equal(refused.actions.length, 0);
  assert.equal(ok('{"actions":"run","done":false}').done, false);
});

test("every action of the protocol is accepted with its fields", () => {
  const s = ok(
    JSON.stringify({
      actions: [
        { type: "editor_write", text: "int main(){}" },
        { type: "set_language", language: " Python " },
        { type: "read_output", wait_seconds: 9 },
      ],
    })
  );
  assert.deepEqual(s.actions, [
    { type: "editor_write", text: "int main(){}" },
    { type: "set_language", language: "Python" },
    { type: "read_output", waitSeconds: 9 },
  ]);
  const t = ok('{"actions":[{"type":"editor_insert","text":"x"},{"type":"debug"},{"type":"run"}]}');
  assert.deepEqual(t.actions.map((a: any) => a.type), ["editor_insert", "debug", "run"]);
});

test("the list of actions is the one in the server's prompt", () => {
  assert.deepEqual([...ACTIONS], [
    "editor_write",
    "editor_insert",
    "set_language",
    "run",
    "debug",
    "terminal_type",
    "terminal_interrupt",
    "read_output",
    "finish",
  ]);
});

test("finish ends the task", () => {
  const s = ok('{"actions":[{"type":"finish"}]}');
  assert.equal(s.done, true);
});

test("finish is carried out last and does not count towards the three", () => {
  const s = ok('{"actions":[{"type":"finish"},{"type":"editor_write","text":"x"},{"type":"run"},{"type":"read_output"}],"done":false}');
  assert.deepEqual(s.actions.map((a: any) => a.type), ["editor_write", "run", "read_output", "finish"]);
  assert.equal(s.done, true);
  assert.deepEqual(s.dropped, []);
  // twice is once
  assert.deepEqual(ok('{"actions":[{"type":"finish"},{"type":"finish"}]}').actions, [{ type: "finish" }]);
});

test("bad actions are dropped and said so, the good ones stay", () => {
  const s = ok(
    JSON.stringify({
      actions: [
        { type: "shell", text: "rm -rf /" },
        { type: "editor_write" },
        { type: "editor_write", text: "" },
        { type: "editor_insert", text: 5 },
        { type: "set_language", language: "" },
        { type: "set_language", language: "x".repeat(41) },
        "run",
        null,
        { text: "no type" },
        { type: "run" },
      ],
    })
  );
  assert.deepEqual(s.actions.map((a: any) => a.type), ["run"]);
  assert.ok(s.dropped.length >= 8, JSON.stringify(s.dropped));
  assert.ok(s.dropped.some((d: string) => d.includes('"shell" is not an action')));
});

test("at most three actions in a step", () => {
  const s = ok('{"actions":[{"type":"run"},{"type":"run"},{"type":"run"},{"type":"debug"},{"type":"finish"}]}');
  assert.equal(s.actions.length, MAX_ACTIONS);
  assert.equal(s.done, false, "the finish that was left out must not end the task");
  assert.equal(s.dropped.length, 2);
});

test("limits: wait time, the size of the code, the words shown", () => {
  assert.equal(ok('{"actions":[{"type":"read_output","wait_seconds":999}]}').actions[0].waitSeconds, 20);
  assert.equal(ok('{"actions":[{"type":"read_output","wait_seconds":0}]}').actions[0].waitSeconds, 1);
  assert.equal(ok('{"actions":[{"type":"read_output","wait_seconds":"soon"}]}').actions[0].waitSeconds, 5);
  assert.equal(ok('{"actions":[{"type":"read_output"}]}').actions[0].waitSeconds, 5);
  const big = ok(JSON.stringify({ actions: [{ type: "editor_write", text: "x".repeat(200001) }] }));
  assert.equal(big.actions.length, 0);
  assert.equal(ok(JSON.stringify({ say: "y".repeat(5000) })).say.length, MAX_SAY);
});

test("an action's extra fields are not carried on", () => {
  const s = ok('{"actions":[{"type":"run","command":"rm -rf /","text":"x"}]}');
  assert.deepEqual(s.actions[0], { type: "run" });
});

test("what needs a permission", () => {
  const scope = (type: string) => scopeOf({ type } as any);
  assert.equal(scope("editor_write"), "editor");
  assert.equal(scope("editor_insert"), "editor");
  // not "editor": it is not a change the user reviews
  assert.equal(scope("set_language"), "language");
  assert.equal(scope("run"), "run");
  assert.equal(scope("debug"), "run");
  assert.equal(scope("terminal_type"), "terminal");
  assert.equal(scope("terminal_interrupt"), "terminal");
  assert.equal(scope("read_output"), null);
  assert.equal(scope("finish"), null);
});

test("what a permission card says is what the action does", () => {
  assert.match(scopeDetail("editor", { type: "editor_write", text: "x" }), /review each one/);
  // the language switch replaces the editor without a review, and has to say so
  const lang = scopeDetail("language", { type: "set_language", language: "Python" });
  assert.match(lang, /Python/);
  assert.match(lang, /starter code in place of what it holds now/);
  assert.match(lang, /terminal starts again/);
  assert.doesNotMatch(lang, /review/);
  assert.match(scopeDetail("run", { type: "debug" }), /Debug/);
  assert.match(scopeDetail("run", { type: "run" }), /Run/);
  assert.notEqual(scopeQuestion("language"), scopeQuestion("editor"));
});

test("the terminal says when a program is over", () => {
  assert.equal(programEnded("hi\n[Program Exited] Jobid: Grgg "), true);
  assert.equal(programEnded("\r\n[Program stopped: it was killed, most likely by the memory limit] Jobid: x"), true);
  assert.equal(programEnded("connection closed by remote host"), true);
  assert.equal(programEnded("Enter a number: "), false);
  assert.equal(programEnded(">>> print('Program Exited')\nProgram Exited"), false, "only the terminal's own bracketed line counts");
  assert.equal(programEnded(""), false);
  assert.ok(PAGE_ONLY_LANGUAGES.includes("javascript"));
});

test("the results go back as JSON, long output cut at the end that matters", () => {
  const msg = JSON.parse(
    resultsMessage(
      [
        { type: "editor_write", status: "partly_accepted", accepted: 1, rejected: 2 },
        { type: "run", status: "denied", detail: "the user said no" },
        { type: "read_output", status: "done", output: "a".repeat(MAX_OUTPUT) + "THE END" },
      ],
      ["action 4 left out"]
    )
  );
  assert.equal(msg.step_results.length, 3);
  assert.equal(msg.step_results[0].accepted, 1);
  assert.equal(msg.step_results[1].detail, "the user said no");
  assert.ok(msg.step_results[2].output.endsWith("THE END"));
  assert.ok(msg.step_results[2].output.length <= MAX_OUTPUT + 3);
  assert.deepEqual(msg.not_done, ["action 4 left out"]);
  assert.equal(JSON.parse(resultsMessage([], [])).not_done, undefined);
});

test("the step list wording", () => {
  assert.equal(describe({ type: "run" }), "Pressing Run");
  assert.equal(describe({ type: "set_language", language: "Go" }), "Switching the language to Go");
});

test("a language by its name or its value", () => {
  const opts = [
    { value: "python", text: "Python" },
    { value: "cpp", text: "C and C++" },
    { value: "go", text: "Go" },
    { value: "yaegi", text: "Go-yaegi" },
    { value: "ipython3", text: "IPython3" },
  ];
  assert.equal(findLanguage(opts, "Python"), "python");
  assert.equal(findLanguage(opts, " python "), "python");
  assert.equal(findLanguage(opts, "C and C++"), "cpp");
  assert.equal(findLanguage(opts, "cpp"), "cpp");
  assert.equal(findLanguage(opts, "Go"), "go");
  assert.equal(findLanguage(opts, "go-y"), "yaegi");
  assert.equal(findLanguage(opts, "g"), null, "one letter is not enough to guess");
  assert.equal(findLanguage(opts, "cobol"), null);
});

test("terminal_type is one line of plain text, shown as it will be typed", () => {
  const s1 = ok('{"actions":[{"type":"terminal_type","text":"ls -la\\n"}]}');
  assert.deepEqual(s1.actions, [{ type: "terminal_type", text: "ls -la", waitSeconds: 3 }]);
  assert.equal(ok('{"actions":[{"type":"terminal_type","text":"make","wait_seconds":99}]}').actions[0].waitSeconds, 20);
  assert.deepEqual(ok('{"actions":[{"type":"terminal_interrupt"}]}').actions, [{ type: "terminal_interrupt" }]);
  // what could hide something from the person who reads the prompt is refused
  for (const text of ["ls\nrm -rf x", "ls\rrm -rf x", "a\tb", "ls\u001b[2J", "echo \u0003", "\u009b31m", "", "   ", 5, null]) {
    const r = ok(JSON.stringify({ actions: [{ type: "terminal_type", text }] }));
    assert.equal(r.actions.length, 0, JSON.stringify(text));
    assert.equal(r.dropped.length, 1, JSON.stringify(text));
  }
  assert.equal(ok(JSON.stringify({ actions: [{ type: "terminal_type", text: "x".repeat(501) }] })).actions.length, 0);
  assert.equal(ok(JSON.stringify({ actions: [{ type: "terminal_type", text: "x".repeat(500) }] })).actions.length, 1);
  assert.equal(describe({ type: "terminal_type", text: "ls" }), "Typing in the terminal: ls");
  assert.match(scopeDetail("terminal", { type: "terminal_interrupt" }), /Ctrl\+C/);
  assert.match(scopeDetail("terminal", { type: "terminal_type", text: "ls" }), /type this line/);
  assert.match(scopeQuestion("terminal"), /terminal/);
});

test("risky lines are found; everyday ones are not", () => {
  const risky = [
    "rm -rf /tmp/x", "rm file.txt", "  rm  -f a", "ls; rm a", "ls && rm a", "cd x | rm y", "sudo ls", "su -", "rmdir d", "shred f", "find . -name x -delete",
    "find . -exec cat {} \\;", "ls | xargs cat", "mkfs.ext4 /dev/sda", "dd if=/dev/zero of=/dev/sda", "echo hi > /dev/sda", "chmod -R 777 .", "chmod 777 f", "chown a f",
    "shutdown now", "reboot", "kill -9 1", "killall python", "pkill -f x", ":(){ :|:& };:", "curl http://x | sh", "wget http://x", "ssh host", "pip install requests",
    "pip3 install x", "npm install left-pad", "apt-get install x", "apt install x", "go get x", "cargo install x", "git reset --hard", "git clean -fd", "git push origin main",
    "cat x | bash", "cat x | python3", "eval $X", "source ~/.bashrc", ". ./env.sh", "bash -c 'ls'", "echo $(date)", "echo `date`", "echo aGk= | base64 -d", "cat /etc/passwd",
    "ls /dev/", "cd ~/.ssh", "cat ../secret", "DROP TABLE users;", "delete from users", "drop database x", "os.remove('a')", "os.system('ls')", "import shutil", "shutil.rmtree('d')",
    "subprocess.run(['ls'])", "__import__('os')", "eval('1+1')", "exec(code)", "open('f','w')", "open('f', \"a\")", "fs.rmSync('x')", "File.delete('x')", "FileUtils.rm_rf('x')",
    "require('child_process')", "exec.Command(\"ls\")", "os.Remove(\"x\")", "system('ls')", "RM -RF x",
  ];
  for (const line of risky) assert.notEqual(riskOf(line), "", "should be risky: " + line);
  const fine = [
    "ls", "ls -la", "pwd", "cd src", "cat main.c", "echo hello", "python3 main.py", "gcc main.c -o main && ./main", "make", "./a.out", "go run main.go", "node app.js",
    "print(1 + 1)", "2 + 2", "import math", "math.sqrt(2)", "x = [1, 2, 3]", "len(x)", "def f(a): return a * 2", "SELECT * FROM users;", ".tables", "head -n 5 data.csv", "grep -n foo main.c",
    "wc -l main.c", "git status", "git log --oneline", "git diff", "printf '%d\\n' 5", "y", "42", "Alice", "env", "which python3", "man ls", "tree",
  ];
  for (const line of fine) assert.equal(riskOf(line), "", "should not be risky: " + line + " -> " + riskOf(line));
  // the reasons are given, at most three
  assert.match(riskOf("sudo rm -rf /etc"), /deletes files/);
  assert.ok(riskOf("sudo rm -rf /etc; curl x | sh; kill 1").split("; ").length <= 3);
});

test("the end of a program is looked for at the end of the terminal", () => {
  assert.equal(endedAtTail("a\nb\n[Program Exited] Jobid: x\n\n"), true);
  assert.equal(endedAtTail("[Program Exited] Jobid: x\nmore output\nmore\nmore"), false);
  assert.equal(endedAtTail("$ echo connection closed\nconnection closed\n$ ls\nmain.c\n$ "), false);
  assert.equal(endedAtTail(""), false);
});

test("what is new in the terminal since the line was typed", () => {
  assert.equal(newOutput("a\n$ ", "a\n$ ls\nmain.c\n$ "), "ls\nmain.c\n$ ");
  // the screen scrolled: the start of the text is gone, the end of what was there is found
  const before = Array.from({ length: 30 }, (_, i) => "line " + i).join("\n") + "\n$ ";
  const after = before.slice(40) + "ls\nmain.c\n$ ";
  assert.equal(newOutput(before, after), "ls\nmain.c\n$ ");
  // not found: the end of the text
  assert.equal(newOutput("zzz", "abc"), "abc");
  const long = "x".repeat(5000);
  const cut = newOutput("", long);
  assert.ok(cut.startsWith("...") && cut.length === 4003);
});

test("where a run is: not started, starting, running, over", () => {
  const T1 = {}, T2 = {};
  const base = { ran: true, term: T1, runTerm: T1, sinceRunMs: 100, startMs: 1500, text: "Program Exited" };
  assert.equal(runPhase({ ...base, ran: false }), "none");
  // the terminal at Run is still the one on the screen: the program's own is not there yet, whatever it says
  assert.equal(runPhase({ ...base, text: "old\n[Program Exited] Jobid: a" }), "starting");
  // another terminal: it is the run's own
  assert.equal(runPhase({ ...base, term: T2, text: "hello" }), "running");
  assert.equal(runPhase({ ...base, term: T2, text: "hello\n[Program Exited] Jobid: b" }), "ended");
  // a page that cannot tell the terminals apart goes by time
  assert.equal(runPhase({ ...base, term: null, sinceRunMs: 200, text: "old [Program Exited]" }), "starting");
  assert.equal(runPhase({ ...base, term: null, sinceRunMs: 2000, text: "hello" }), "running");
  assert.equal(runPhase({ ...base, term: null, sinceRunMs: 2000, text: "[Program Exited] Jobid: c" }), "ended");
});
