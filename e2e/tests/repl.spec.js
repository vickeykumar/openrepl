// The REPLs start. A language page connects a terminal to the server, which starts the REPL
// program (node, ts-node, python3, irb, cling, gomacro, jshell) with its own PATH: this opens each one through
// the page, types a small sum and waits for the answer. It is what shows that an image has
// every REPL's program where the server looks for it (an image without `node` answered
// "executable file not found in $PATH" and every check on the pages still passed).
const { test, expect } = require("@playwright/test");

// what the terminal shows, as text
async function terminalText(page) {
  const rows = page.locator("#terminal-div .xterm-rows");
  if ((await rows.count()) === 0) return "";
  return (await rows.first().innerText()).replace(/ /g, " ");
}

// waits until the terminal shows `text` (a string or a regular expression)
async function waitForTerminal(page, text, message, unwrap = false, timeout = 60 * 1000) {
  await expect
    .poll(async () => ((t) => (unwrap ? t.replace(/\n/g, "") : t))(await terminalText(page)), { message, timeout, intervals: [500, 1000, 2000] })
    .toMatch(text);
}

async function type(page, line) {
  await page.locator("#terminal-div .xterm-helper-textarea").first().focus();
  await page.keyboard.type(line, { delay: 40 });
  await page.keyboard.press("Enter");
}

const REPLS = [
  { name: "Node.js", path: "/nodejs", prompt: />/, inputs: ["6 * 7"], answer: /\b42\b/ },
  // ts-node once stopped with "Cannot read property 'fileExists' of undefined": it loaded its own
  // nested typescript 7 (no JavaScript API) instead of the global 4.9.5
  { name: "TypeScript (ts-node)", path: "/typescript", prompt: />/, inputs: ["40 + 2"], answer: /\b42\b/ },
  { name: "Go (gomacro)", path: "/go", prompt: /gomacro>/, inputs: ["x := 6", "x * 7"], answer: /42\s+\/\/ int/ },
  { name: "Java (jshell)", path: "/java", prompt: /jshell>/, inputs: ["6 * 7"], answer: /==> 42/ },
  { name: "Python", path: "/python", prompt: />>>/, inputs: ["6 * 7"], answer: /\b42\b/ },
  { name: "Ruby (irb)", path: "/ruby", prompt: />/, inputs: ["6 * 7"], answer: /=> 42/ },
  // evcxr found no Rust toolchain once (the REPL's HOME is its workspace, rustup looks below HOME): "No such file or directory"
  { name: "Rust (evcxr)", path: "/rust", prompt: />>/, inputs: ["6 * 7"], answer: /\b42\b/ },
  { name: "Bash", path: "/bash", prompt: /@bash/, inputs: ["echo $((6*7))"], answer: /(^|\s)42(\s|$)/m },
  { name: "Perl (perli)", path: "/perl", prompt: /perli>/, inputs: ["6*7"], answer: /(^|\s)42(\s|$)/m },
  { name: "Tcl (tclsh)", path: "/tcl", prompt: /^%/m, inputs: ["expr 6*7"], answer: /(^|\s)42(\s|$)/m },
  { name: "SQLite", path: "/sqlite", prompt: /sqlite>/, inputs: ["select 6*7;"], answer: /(^|\s)42(\s|$)/m },
  // the first line loads a JSON document, the second is a filter on it
  { name: "jq", path: "/jq", prompt: /jq>>/, inputs: ['{"a":6}', ".a * 7"], answer: /(^|\s)42(\s|$)/m },
  // rappel assembles what is typed for the host: arm64 on an arm64 server, x86-64 otherwise. It shows the
  // registers after the line (a wide line that the terminal wraps, so the text is read unwrapped)
  { name: "Assembly (rappel)", path: "/assembly", prompt: /^>/m, anyOf: ["mov x0, 42", "mov rax, 42"], answer: /(x0|rax)=0*2a\b/, unwrap: true },
  // cling is in C mode here, which does not print the value of an expression: print it
  { name: "C and C++ (cling)", path: "/c", prompt: /\[cling\]/, inputs: ["#include <stdio.h>", 'printf("%d\\n", 6 * 7);'], answer: /(^|\s)42(\s|$)/m },
];

for (const repl of REPLS) {
  const define = repl.fixme ? test.fixme : test;
  define(`the ${repl.name} REPL starts and answers`, async ({ page }) => {
    test.setTimeout(150 * 1000);
    await page.goto(repl.path);
    await waitForTerminal(page, repl.prompt, `${repl.name}: no prompt in the terminal`);
    const shown = await terminalText(page);
    expect(shown, "the server could not start the program").not.toMatch(/executable file not found|failed to create backend|command not found|cannot extract standard library include paths/i);
    if (repl.anyOf) {
      // the line to type depends on the server (assembly of arm64 or x86-64): the first that is answered
      let answered = false;
      for (const line of repl.anyOf) {
        await type(page, line);
        try {
          await waitForTerminal(page, repl.answer, "", repl.unwrap, 8 * 1000);
          answered = true;
          break;
        } catch (e) {}
      }
      expect(answered, `${repl.name}: no answer to ${repl.anyOf.join(" or ")}`).toBe(true);
      return;
    }
    for (const line of repl.inputs) await type(page, line);
    await waitForTerminal(page, repl.answer, `${repl.name}: no answer to ${repl.inputs[repl.inputs.length - 1]}`, repl.unwrap);
  });
}
