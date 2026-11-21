// node --test test/   (from src/resources/chat-widget; Node 22.18 or later runs TypeScript as it is)
import test from "node:test";
import assert from "node:assert/strict";
import {
  isSmallModel,
  starterChips,
  ACTIONS,
  MAX_ACTIONS,
  MAX_OUTPUT,
  MAX_SAY,
  PAGE_ONLY_LANGUAGES,
  cleanName,
  cleanPath,
  filesAskReason,
  splitPath,
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
  hardBlockOf,
  riskKindOf,
  shellWordOf,
  SHELL_REPLS,
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
    "editor_replace",
    "set_language",
    "run",
    "debug",
    "terminal_type",
    "terminal_interrupt",
    "terminal_reconnect",
    "terminal_new_tab",
    "terminal_select_tab",
    "terminal_close_tab",
    "bash",
    "files_list",
    "files_search",
    "files_read",
    "files_open",
    "files_new",
    "files_save",
    "files_rename",
    "files_cut",
    "files_copy",
    "files_paste",
    "files_move",
    "files_delete",
    "read_output",
    "preview_open",
    "preview_close",
    "preview_reload",
    "preview_state",
    "preview_read",
    "preview_console",
    "preview_network",
    "preview_inspect",
    "preview_find",
    "preview_wait",
    "preview_click",
    "preview_type",
    "preview_press",
    "preview_scroll",
    "preview_select",
    "preview_hover",
    "preview_drag",
    "preview_go",
    "preview_resize",
    "preview_eval",
    "preview_storage",
    "store_suggest",
    "mcp_call",
    "need_tools",
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
    // what overwrites, edits in place or writes a file
    "mv a.py b.py", "cp a.py b.py", "ln -sf a b", "sed -i s/a/b/ main.c", "perl -pi -e s/a/b/ f", "ls | tee out.txt", "echo hi > out.txt", "python a.py >> run.log", "cat a > ./b", ":> notes.md",
    // text that is run, and secrets on the screen
    "python3 -c 'print(1)'", "node -e 1", "env", "printenv PATH", "git checkout .", "git stash drop", "git rm a.py",
  ];
  for (const line of risky) assert.notEqual(riskOf(line), "", "should be risky: " + line);
  const fine = [
    "ls", "ls -la", "pwd", "cd src", "cat main.c", "echo hello", "python3 main.py", "gcc main.c -o main && ./main", "make", "./a.out", "go run main.go",
    "print(1 + 1)", "2 + 2", "import math", "math.sqrt(2)", "x = [1, 2, 3]", "len(x)", "def f(a): return a * 2", "SELECT * FROM users;", ".tables", "head -n 5 data.csv", "grep -n foo main.c",
    "wc -l main.c", "git status", "git log --oneline", "git diff", "printf '%d\\n' 5", "y", "42", "Alice", "which python3", "man ls", "tree",
    // a ">" that is not a redirect into a file
    "x > 0.5", "a > b", "if x >= 10: print(x)", "const f = x => x.length", "def f() -> int: return 1", "ls 2>/dev/null", "make 2>&1", "echo hi > /dev/null", "a >> 2", "List<String> xs",
    "git stash", "git checkout main", "environment = 1", "cpu = 4", "mvn test", "sedan = 1",
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

test("terminal tabs: reconnect, a new tab (in a language) and switching", () => {
  const s = ok('{"actions":[{"type":"terminal_reconnect"},{"type":"terminal_new_tab"},{"type":"terminal_new_tab","language":" Python "},{"type":"terminal_select_tab","tab":2}]}');
  assert.deepEqual(s.actions, [{ type: "terminal_reconnect" }, { type: "terminal_new_tab" }, { type: "terminal_new_tab", language: "Python" }]);
  assert.equal(s.dropped.length, 1, "the fourth is over the limit of three");
  const t = ok('{"actions":[{"type":"terminal_new_tab","language":" Python "},{"type":"terminal_select_tab","tab":2}]}');
  assert.deepEqual(t.actions, [{ type: "terminal_new_tab", language: "Python" }, { type: "terminal_select_tab", tab: 2 }]);
  // wrong fields are dropped and said
  for (const bad of ['{"type":"terminal_select_tab"}', '{"type":"terminal_select_tab","tab":0}', '{"type":"terminal_select_tab","tab":6}', '{"type":"terminal_select_tab","tab":"2"}', '{"type":"terminal_select_tab","tab":1.5}', '{"type":"terminal_new_tab","language":7}', '{"type":"terminal_new_tab","language":"' + "x".repeat(41) + '"}']) {
    const r = ok('{"actions":[' + bad + ']}');
    assert.equal(r.actions.length, 0, bad);
    assert.equal(r.dropped.length, 1, bad);
  }
  // an empty language is none
  assert.deepEqual(ok('{"actions":[{"type":"terminal_new_tab","language":"  "}]}').actions, [{ type: "terminal_new_tab" }]);
});

test("a new tab in a language is a language change, because the editor is replaced", () => {
  assert.equal(scopeOf({ type: "terminal_new_tab" }), "terminal");
  assert.equal(scopeOf({ type: "terminal_new_tab", language: "Go" }), "language");
  assert.equal(scopeOf({ type: "terminal_reconnect" }), "terminal");
  assert.equal(scopeOf({ type: "terminal_select_tab", tab: 2 }), "terminal");
  assert.match(scopeDetail("language", { type: "terminal_new_tab", language: "Go" }), /starter code in place of what it holds now/);
  assert.match(scopeDetail("terminal", { type: "terminal_reconnect" }), /stopped/);
  assert.match(scopeDetail("terminal", { type: "terminal_new_tab" }), /up to 5/);
  assert.equal(describe({ type: "terminal_new_tab", language: "Go" }), "Opening a new terminal tab in Go");
  assert.equal(describe({ type: "terminal_select_tab", tab: 3 }), "Switching to terminal tab 3");
  assert.equal(describe({ type: "terminal_reconnect" }), "Restarting the terminal");
});

test("closing a tab: which one, asked about, said in words", () => {
  assert.deepEqual(ok('{"actions":[{"type":"terminal_close_tab","tab":3}]}').actions, [{ type: "terminal_close_tab", tab: 3 }]);
  for (const bad of ['{"type":"terminal_close_tab"}', '{"type":"terminal_close_tab","tab":0}', '{"type":"terminal_close_tab","tab":6}', '{"type":"terminal_close_tab","tab":"2"}']) {
    const r = ok('{"actions":[' + bad + ']}');
    assert.equal(r.actions.length, 0, bad);
    assert.equal(r.dropped.length, 1, bad);
  }
  assert.equal(scopeOf({ type: "terminal_close_tab", tab: 2 }), "terminal");
  assert.equal(describe({ type: "terminal_close_tab", tab: 2 }), "Closing terminal tab 2");
  const detail = scopeDetail("terminal", { type: "terminal_close_tab", tab: 2 });
  assert.match(detail, /tab 2/);
  assert.match(detail, /main terminal, is never closed/);
  assert.match(detail, /stopped/);
});

test("a path is relative to the home directory and stays inside it", () => {
  for (const [raw, want] of [["src/main.py", "src/main.py"], [" src/main.py ", "src/main.py"], ["./src/main.py", "src/main.py"], ["src/", "src"], ["src//", "src"], ["a b/c d.txt", "a b/c d.txt"]] as const) {
    assert.equal(cleanPath(raw, false), want, raw);
  }
  // the home directory itself only where it makes sense
  for (const raw of ["", ".", "./", "  "]) {
    assert.equal(cleanPath(raw, true), "", JSON.stringify(raw));
    assert.equal(cleanPath(raw, false), null, JSON.stringify(raw));
  }
  // what leaves it, or hides something
  for (const raw of ["/etc/passwd", "/", "//x", "../x", "a/../b", "a/./b", "a//b", "~/x", ".git/config", "a/.git/x", ".genie/chats/a.jsonl", ".genie/tabs.json", "a\\b", "a\nb", "a\u0000b", "a\u001b[2Jb", "a\u009bb", "x".repeat(401), "a/" + "x".repeat(101), 5, null, undefined, {}, ["a"]]) {
    const v = cleanPath(raw as any, true);
    assert.ok(v === null || raw === "" , "should be refused: " + JSON.stringify(raw) + " -> " + v);
  }
  // a hidden name may be named (the person shows those, and a card asks before any action on one)
  for (const ok of [".env", "src/.secret", ".genie/skills/a.md"]) assert.equal(cleanPath(ok, false), ok);
  assert.equal(cleanName("notes.txt"), "notes.txt");
  assert.equal(cleanName("a/b"), null);
  assert.equal(cleanName(".."), null);
  assert.equal(cleanName(""), null);
  assert.deepEqual(splitPath("src/util/a.py"), { parent: "src/util", name: "a.py" });
  assert.deepEqual(splitPath("a.py"), { parent: "", name: "a.py" });
});

test("file actions: every field is checked", () => {
  const types = (json: string) => ok(json).actions;
  assert.deepEqual(types('{"actions":[{"type":"files_list"},{"type":"files_list","path":"src"},{"type":"files_save"}]}'), [{ type: "files_list", path: "" }, { type: "files_list", path: "src" }, { type: "files_save" }]);
  assert.deepEqual(types('{"actions":[{"type":"files_open","path":"./a.py"},{"type":"files_new","path":"src/b.py"},{"type":"files_new","path":"docs","kind":"folder"}]}'), [
    { type: "files_open", path: "a.py" },
    { type: "files_new", path: "src/b.py", kind: "file" },
    { type: "files_new", path: "docs", kind: "folder" },
  ]);
  assert.deepEqual(types('{"actions":[{"type":"files_rename","path":"a.py","name":"b.py"},{"type":"files_move","path":"b.py","to":"src"},{"type":"files_paste","to":"src"}]}'), [
    { type: "files_rename", path: "a.py", name: "b.py" },
    { type: "files_move", path: "b.py", to: "src" },
    { type: "files_paste", to: "src" },
  ]);
  assert.deepEqual(types('{"actions":[{"type":"files_cut","path":"a.py"},{"type":"files_copy","path":"b.py"},{"type":"files_delete","path":"c.py"}]}'), [
    { type: "files_cut", path: "a.py" },
    { type: "files_copy", path: "b.py" },
    { type: "files_delete", path: "c.py" },
  ]);
  // the moves into home, by leaving "to" out
  assert.deepEqual(types('{"actions":[{"type":"files_move","path":"src/a.py"}]}'), [{ type: "files_move", path: "src/a.py", to: "" }]);
  // each of these is dropped, once, with its reason
  for (const bad of [
    '{"type":"files_open"}', '{"type":"files_open","path":"/etc/passwd"}', '{"type":"files_open","path":"../x"}', '{"type":"files_delete","path":""}', '{"type":"files_delete","path":"."}',
    '{"type":"files_new","path":"a","kind":"socket"}', '{"type":"files_new"}', '{"type":"files_rename","path":"a"}', '{"type":"files_rename","path":"a","name":"b/c"}', '{"type":"files_rename","name":"b"}',
    '{"type":"files_move","path":"a","to":"/tmp"}', '{"type":"files_move","to":"x"}', '{"type":"files_paste","to":"../.."}', '{"type":"files_list","path":"a/../.."}', '{"type":"files_cut","path":5}',
  ]) {
    const r = ok('{"actions":[' + bad + ']}');
    assert.equal(r.actions.length, 0, bad);
    assert.equal(r.dropped.length, 1, bad);
  }
});

test("the file actions that change things for good are asked about every time", () => {
  const a = (type: string) => ({ type } as any);
  assert.match(filesAskReason(a("files_delete"), null), /for good/);
  assert.match(filesAskReason(a("files_rename"), null), /name/);
  assert.match(filesAskReason(a("files_move"), null), /moves/);
  assert.match(filesAskReason(a("files_paste"), "cut"), /moves/);
  for (const t of ["files_list", "files_open", "files_new", "files_save", "files_cut", "files_copy"]) assert.equal(filesAskReason(a(t), "cut"), "", t);
  assert.equal(filesAskReason(a("files_paste"), "copy"), "");
  assert.equal(filesAskReason(a("files_paste"), null), "");
  assert.equal(scopeOf(a("files_delete")), "files");
  assert.equal(scopeOf(a("files_list")), "files");
});

test("a change of language says what it does to the editor, which depends on whether a file is open", () => {
  const plain = scopeDetail("language", { type: "set_language", language: "Go" });
  assert.match(plain, /starter code in place of what it holds now/);
  const withFile = scopeDetail("language", { type: "set_language", language: "Go" }, "src/main.py");
  assert.match(withFile, /keeps src\/main\.py, the file that is open/);
  assert.doesNotMatch(withFile, /starter code/);
  assert.match(scopeDetail("language", { type: "terminal_new_tab", language: "Go" }, "a.py"), /keeps a\.py/);
});

test("what a file permission card says is what the action does", () => {
  assert.match(scopeDetail("files", { type: "files_open", path: "a.py" }), /save the file that is open/);
  assert.match(scopeDetail("files", { type: "files_open", path: "a.py" }), /not in a file is replaced/);
  assert.match(scopeDetail("files", { type: "files_delete", path: "src" }), /for good, and everything in it/);
  assert.match(scopeDetail("files", { type: "files_move", path: "a.py", to: "" }), /the home folder/);
  assert.match(scopeDetail("files", { type: "files_list" }), /sent to the model/);
  assert.equal(describe({ type: "files_rename", path: "a.py", name: "b.py" }), "Renaming a.py to b.py");
  assert.equal(describe({ type: "files_paste", to: "" }), "Pasting into the home folder");
  assert.match(scopeQuestion("files"), /files/);
});

test("preview actions: a port or a path, never both, and the permission is asked once for opening, reloading and closing", () => {
  const types = (json: string) => ok(json).actions;
  assert.deepEqual(types('{"actions":[{"type":"preview_open"},{"type":"preview_open","port":5000},{"type":"preview_open","path":"./site/index.html"}]}'), [
    { type: "preview_open" },
    { type: "preview_open", port: 5000 },
    { type: "preview_open", path: "site/index.html" },
  ]);
  assert.deepEqual(types('{"actions":[{"type":"preview_reload"},{"type":"preview_close"},{"type":"preview_state"}]}'), [{ type: "preview_reload" }, { type: "preview_close" }, { type: "preview_state" }]);
  for (const bad of ['{"type":"preview_open","port":80}', '{"type":"preview_open","port":"5000"}', '{"type":"preview_open","port":70000}', '{"type":"preview_open","path":"../x.html"}', '{"type":"preview_open","port":5000,"path":"a.html"}']) {
    const s = ok('{"actions":[' + bad + "]}");
    assert.equal(s.actions.length, 0, bad);
    assert.equal(s.dropped.length, 1, bad);
  }
  assert.equal(scopeOf({ type: "preview_open" }), "preview");
  assert.equal(scopeOf({ type: "preview_reload" }), "preview");
  assert.equal(scopeOf({ type: "preview_close" }), "preview");
  assert.equal(scopeOf({ type: "preview_state" }), null);
  assert.equal(scopeQuestion("preview"), "Genie wants to use the Preview");
  assert.match(scopeDetail("preview", { type: "preview_open", port: 5000 }), /port 5000/);
  assert.match(describe({ type: "preview_open", path: "a.html" }), /a\.html/);
});

test("bash: parsing, limits, scope, risk", () => {
  const r = ok('{"say":"x","actions":[{"type":"bash","command":"grep -rn TODO src | head -20","timeout_seconds":500}]}');
  assert.deepEqual(r.actions, [{ type: "bash", text: "grep -rn TODO src | head -20", timeoutSeconds: 120 }]);
  assert.equal(ok('{"actions":[{"type":"bash","command":"ls"}]}').actions[0].timeoutSeconds, 30);
  assert.equal(ok('{"actions":[{"type":"bash","command":"ls","timeout_seconds":0}]}').actions[0].timeoutSeconds, 1);
  assert.equal(ok('{"actions":[{"type":"bash","command":"a\\nb"}]}').actions.length, 1); // several lines are plain text
  for (const bad of ['{"type":"bash"}', '{"type":"bash","command":"  "}', '{"type":"bash","command":5}', '{"type":"bash","command":"a\\u0007b"}', '{"type":"bash","command":"' + "x".repeat(4001) + '"}']) {
    const s = ok('{"actions":[' + bad + "]}");
    assert.equal(s.actions.length, 0, bad.slice(0, 40));
    assert.equal(s.dropped.length, 1);
  }
  const a = { type: "bash" as const, text: "ls -la", timeoutSeconds: 30 };
  assert.equal(scopeOf(a), "shell");
  assert.equal(describe(a), "bash: ls -la");
  assert.match(scopeDetail("shell", a), /own shell.*30 seconds.*terminal is not touched/);
  assert.equal(riskKindOf(a), null);
  assert.equal(riskKindOf({ ...a, text: "rm -r build" }), "terminal");
  assert.match(hardBlockOf({ ...a, text: "sudo apt install x" }), /sudo/);
  assert.match(hardBlockOf({ ...a, text: "cp x /etc/y" }), /outside/);
});

test("a shell command typed into a language's REPL is recognised, the language's own lines are not", () => {
  for (const line of ["curl -I http://127.0.0.1:8000", "curl http://127.0.0.1:8000/style.css", "ls -la", "ls", "pwd", "cd src", "cat style.css", "git status", "mkdir -p a/b", "python3 server.py", "pip install requests", "go run main.go", "node app.js", "echo hello", "node app.js", "go build ./...", "gcc -o a a.c", "  grep -rn TODO .  "]) {
    assert.notEqual(shellWordOf(line), "", line);
  }
  for (const line of ["ls = [1, 2]", "cat.speak()", "git(5)", "print('curl x')", "x = curl", "go = 5", "node", "go", "go func() { f() }()", "go worker(ch)", "node = 3", "java = 1", "echo", "cd = 3", "make_it(2)", "for ls in a:", "curl(", "lst = ls -1", "def cat():", "cat = Cat('a')"]) {
    assert.equal(shellWordOf(line), "", line);
  }
  assert.ok(SHELL_REPLS.includes("bash") && !SHELL_REPLS.includes("python"));
});

test("store_suggest names a conda-forge package and asks the user: it installs and turns on the app", () => {
  const types = (j: string) => ok(j).actions;
  assert.deepEqual(types('{"actions":[{"type":"store_suggest","name":"Flask"}]}'), [{ type: "store_suggest", name: "flask" }]);
  for (const bad of ['{"type":"store_suggest"}', '{"type":"store_suggest","name":"../x"}', '{"type":"store_suggest","name":"a b"}', '{"type":"store_suggest","name":5}']) {
    const s = ok('{"actions":[' + bad + "]}");
    assert.equal(s.actions.length, 0, bad);
    assert.equal(s.dropped.length, 1, bad);
  }
  assert.equal(scopeOf({ type: "store_suggest", name: "flask" }), "store");
  assert.match(scopeQuestion("store"), /App Store/);
  assert.match(scopeDetail("store", { type: "store_suggest", name: "flask" }), /flask/);
  assert.match(describe({ type: "store_suggest", name: "flask" }), /flask/);
});

test("editor_replace needs the old text or the lines, and the new text", () => {
  const ok = parseStep(JSON.stringify({ actions: [{ type: "editor_replace", old: "a = 1", new: "a = 2" }, { type: "editor_replace", start_line: 3, end_line: 4, new: "" }, { type: "editor_replace", start_line: 7, new: "x" }], done: false }));
  assert.ok(ok.ok);
  if (ok.ok) {
    assert.deepEqual(ok.step.actions, [
      { type: "editor_replace", old: "a = 1", text: "a = 2" },
      { type: "editor_replace", startLine: 3, endLine: 4, text: "" },
      { type: "editor_replace", startLine: 7, endLine: 7, text: "x" },
    ]);
  }
  const bad = parseStep(JSON.stringify({ actions: [{ type: "editor_replace", old: "x" }, { type: "editor_replace", new: "y" }, { type: "editor_replace", start_line: 5, end_line: 2, new: "y" }, { type: "editor_replace", start_line: 0, new: "y" }, { type: "editor_replace", old: "x", new: 5 }] }));
  assert.ok(bad.ok);
  if (bad.ok) {
    assert.equal(bad.step.actions.length, 0);
    assert.equal(bad.step.dropped.length, 5);
  }
  assert.equal(scopeOf({ type: "editor_replace" }), "editor");
});

test("files_search and files_read are read-only: they need no permission, and their paths are checked", () => {
  const ok = parseStep(JSON.stringify({ actions: [{ type: "files_search", query: " login ", path: "src" }, { type: "files_read", path: "src/a.py", start_line: 10, end_line: 20 }, { type: "files_read", path: "b.py" }], done: false }));
  assert.ok(ok.ok);
  if (ok.ok) {
    assert.deepEqual(ok.step.actions, [
      { type: "files_search", query: "login", path: "src" },
      { type: "files_read", path: "src/a.py", startLine: 10, endLine: 20 },
      { type: "files_read", path: "b.py", startLine: 1, endLine: 400 },
    ]);
  }
  assert.equal(scopeOf({ type: "files_search" }), null);
  assert.equal(scopeOf({ type: "files_read" }), null);
  const bad = parseStep(JSON.stringify({ actions: [{ type: "files_search", query: "" }, { type: "files_search", query: "x", path: "../up" }, { type: "files_read", path: ".git/config" }, { type: "files_read", path: "/etc/passwd" }, { type: "files_read", path: "a", start_line: 5, end_line: 2 }, { type: "files_search", query: "a\nb" }] }));
  assert.ok(bad.ok);
  if (bad.ok) {
    assert.equal(bad.step.actions.length, 0);
    assert.equal(bad.step.dropped.length, 6);
  }
  // a read is never more than the most lines
  const long = parseStep(JSON.stringify({ actions: [{ type: "files_read", path: "a", start_line: 1, end_line: 9000 }] }));
  assert.ok(long.ok && long.step.actions[0].endLine === 400);
});


test("small models are told from their id", () => {
  for (const id of ["@cf/qwen/qwen3-30b-a3b-fp8", "@cf/google/gemma-4-26b-a4b-it", "gpt-4o-mini", "@cf/zai-org/glm-4.7-flash", "google/gemma-4-31b-it"]) {
    assert.equal(isSmallModel(id), true, id);
  }
  // a free or auto model is small unless its id says it is big; a size in the id decides
  for (const id of ["openrouter/free", "openrouter/auto", "some/free-router", "cohere/north-mini-code:free"]) assert.equal(isSmallModel(id), true, id);
  for (const id of ["nvidia/nemotron-3-super-120b-a12b:free", "meta/llama-3.3-70b-instruct:free"]) assert.equal(isSmallModel(id), false, id);
  for (const id of ["gpt-6-luna", "anthropic/claude-sonnet", "meta/llama-3.3-70b-instruct", ""]) {
    assert.equal(isSmallModel(id), false, id);
  }
});

test("each mode has its starter chips", () => {
  assert.ok(starterChips(true).includes("Run this"));
  assert.ok(starterChips(false).includes("Explain the error in my terminal"));
  assert.ok(starterChips(true, true).includes("Test my page") && starterChips(true, true).includes("Make it fit a phone"));
  assert.ok(starterChips(false, true).includes("What does my page show?"));
});

import { locateOld, nearestLines } from "../src/agent-protocol.ts";

test("the old text of a replace is found exactly, or by white space, or not at all", () => {
  const text = "<ul>\n    <li><a href=\"a\">A</a></li>\n    <li><a href=\"b\">B</a></li>\n</ul>\nlet cart = [];\nfunction add(x) {\n  cart.push(x);\n}\n";
  // exact
  let r = locateOld(text, "let cart = [];");
  assert.ok(r.ok && !r.tolerant && text.slice(r.start, r.end) === "let cart = [];");
  // other indentation and line breaks: found, and the range is the editor's own text
  r = locateOld(text, "function add(x) {\n      cart.push(x);\n  }");
  assert.ok(r.ok && r.tolerant);
  assert.equal(text.slice((r as any).start, (r as any).end), "function add(x) {\n  cart.push(x);\n}");
  // more than once: refused, not guessed
  r = locateOld(text, "<li><a");
  assert.ok(!r.ok && (r as any).many);
  // not there: the nearest lines are given
  r = locateOld(text, "function addToCart(item) {");
  assert.ok(!r.ok && !(r as any).many);
  assert.match((r as any).near, /6: function add\(x\) \{/);
  assert.equal(nearestLines("one\ntwo", "zzz"), "");
});

import { missingLines } from "../src/agent-protocol.ts";

test("the lines of an old text that are not in the editor are named", () => {
  const text = '<head>\n  <link href="a.css" rel="stylesheet">\n  <title>x</title>\n</head>\n';
  assert.equal(missingLines(text, '<link href="a.css" rel="stylesheet">\n<meta charset="utf-8">'), '<meta charset="utf-8">');
  assert.equal(missingLines(text, "<title>x</title>"), "");
  const r = locateOld(text, '<link href="a.css" rel="stylesheet">\n<meta charset="utf-8">');
  assert.ok(!r.ok && !(r as any).many && (r as any).missing === '<meta charset="utf-8">');
});

test("preview_drag takes a part, how far to drag and where to press, and checks every number", () => {
  const types = (j: string) => { const r = parseStep(j); return r.ok ? { actions: r.step.actions, dropped: r.step.dropped } : null; };
  const ok = types('{"actions":[{"type":"preview_drag","ref":"e4","dx":-80,"dy":40}]}');
  assert.deepEqual(ok!.actions, [{ type: "preview_drag", ref: "e4", dx: -80, dy: 40 }]);
  const full = types('{"actions":[{"type":"preview_drag","ref":"e4","x":120.4,"y":300,"dx":0,"dy":-200.6,"steps":20}]}');
  assert.deepEqual(full!.actions, [{ type: "preview_drag", ref: "e4", x: 120, y: 300, dx: 0, dy: -201, steps: 20 }]);
  for (const bad of [
    '{"type":"preview_drag","dx":5,"dy":5}', // no part
    '{"type":"preview_drag","ref":"e4"}', // no distance
    '{"type":"preview_drag","ref":"e4","dx":0,"dy":0}',
    '{"type":"preview_drag","ref":"e4","dx":99999,"dy":0}',
    '{"type":"preview_drag","ref":"e4","dx":"far","dy":0}',
    '{"type":"preview_drag","ref":"e4","x":10,"dx":5}', // x without y
    '{"type":"preview_drag","ref":"e4","x":-5,"y":5,"dx":5}',
    '{"type":"preview_drag","ref":"e4","dx":5,"steps":1}',
    '{"type":"preview_drag","ref":"e4","dx":5,"steps":200}',
  ]) {
    const r = types('{"actions":[' + bad + "]}");
    assert.equal(r!.actions.length, 0, bad);
    assert.ok(r!.dropped.some((d: string) => /preview_drag left out/.test(d)), bad);
  }
});

test("preview_click takes a point of the part for a canvas, both numbers or neither", () => {
  const one = (a: string) => { const r = parseStep('{"actions":[' + a + "]}"); return r.ok ? r.step : null; };
  assert.deepEqual(one('{"type":"preview_click","ref":"e2"}')!.actions, [{ type: "preview_click", ref: "e2" }]);
  assert.deepEqual(one('{"type":"preview_click","ref":"e2","x":50,"y":60}')!.actions, [{ type: "preview_click", ref: "e2", x: 50, y: 60 }]);
  assert.equal(one('{"type":"preview_click","ref":"e2","x":50}')!.actions.length, 0);
  assert.equal(one('{"type":"preview_click","ref":"e2","x":"a","y":1}')!.actions.length, 0);
});

test("preview_find takes a selector, a text or a point, and preview_read can ask for boxes", () => {
  const one = (a: string) => { const r = parseStep('{"actions":[' + a + "]}"); return r.ok ? r.step : null; };
  assert.deepEqual(one('{"type":"preview_find","selector":"#bird, .pig"}')!.actions, [{ type: "preview_find", selector: "#bird, .pig" }]);
  assert.deepEqual(one('{"type":"preview_find","query":"Score"}')!.actions, [{ type: "preview_find", query: "Score" }]);
  assert.deepEqual(one('{"type":"preview_find","left":300.4,"top":420}')!.actions, [{ type: "preview_find", left: 300, top: 420 }]);
  for (const bad of ['{"type":"preview_find"}', '{"type":"preview_find","selector":""}', '{"type":"preview_find","left":5}', '{"type":"preview_find","left":-1,"top":2}', '{"type":"preview_find","left":"a","top":2}']) {
    const r = one(bad)!;
    assert.equal(r.actions.length, 0, bad);
    assert.ok(r.dropped.some((d: string) => /preview_find left out/.test(d)), bad);
  }
  assert.deepEqual(one('{"type":"preview_read","boxes":true}')!.actions, [{ type: "preview_read", more: false, viewport: false, boxes: true }]);
  assert.equal((one('{"type":"preview_read"}')!.actions[0] as any).boxes, false);
});

test("space is drawing or nothing, for a click and for a drag", () => {
  const one = (a: string) => { const r = parseStep('{"actions":[' + a + "]}"); return r.ok ? r.step : null; };
  assert.deepEqual(one('{"type":"preview_click","ref":"e1","x":5,"y":6,"space":"drawing"}')!.actions, [{ type: "preview_click", ref: "e1", x: 5, y: 6, space: "drawing" }]);
  assert.deepEqual(one('{"type":"preview_drag","ref":"e1","dx":5,"space":"drawing"}')!.actions, [{ type: "preview_drag", ref: "e1", dx: 5, dy: 0, space: "drawing" }]);
  assert.deepEqual(one('{"type":"preview_click","ref":"e1","space":"screen"}')!.actions, [{ type: "preview_click", ref: "e1" }]);
  assert.equal(one('{"type":"preview_click","ref":"e1","space":"world"}')!.actions.length, 0);
  assert.equal(one('{"type":"preview_drag","ref":"e1","dx":5,"space":3}')!.actions.length, 0);
});
