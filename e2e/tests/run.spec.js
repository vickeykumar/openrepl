// The Run button of every language page: the page loads its sample program into the editor, Run sends it to
// the server with the language and the options, the server runs the Compiler script of the language (demos.xml)
// in the workspace, and the output reaches the terminal. A language whose sample does not run, or whose
// program the server cannot start, fails here and not only in a user's browser. The REPL of each language is
// tested in repl.spec.js; the same Run scripts run under the filesystem sandbox in scripts/ci/landlock-compat.py.
const { test, expect } = require("@playwright/test");

// path of the page, and what the sample program prints (read unwrapped: the terminal wraps long lines)
const RUNS = [
  { name: "C and C++ (cling)", path: "/c", prints: /fib\(10\) = 55/ },
  { name: "Go (gomacro)", path: "/go", prints: /"the" appears 3 times/ },
  { name: "Java", path: "/java", prints: /10! = 3628800/ },
  { name: "Node.js", path: "/nodejs", prints: /Fibonacci numbers: 0, 1, 1, 2, 3, 5, 8, 13, 21, 34/ },
  { name: "TypeScript (ts-node)", path: "/typescript", prints: /Go is 17 years old/ },
  { name: "Python", path: "/python", prints: /Longest word: quick/ },
  { name: "Ruby (irb)", path: "/ruby", prints: /Longest word: quick/ },
  { name: "Rust (evcxr)", path: "/rust", prints: /Longest word: jumped/ },
  { name: "Bash", path: "/bash", prints: /Today is/ },
  { name: "Perl (perli)", path: "/perl", prints: /Fibonacci numbers: 0, 1, 1, 2, 3, 5, 8, 13, 21, 34/ },
  { name: "Tcl (tclsh)", path: "/tcl", prints: /Fibonacci numbers: 0, 1, 1, 2, 3, 5, 8, 13, 21, 34/ },
  { name: "SQLite", path: "/sqlite", prints: /14\|SQLite\|SQLite Doc/ },
  { name: "jq", path: "/jq", prints: /Input Json Loaded Successfully/ },
  // x86-64 source: on an arm64 server the Run script links it with the x86-64 linker and runs it under qemu-user
  { name: "Assembly (rappel)", path: "/assembly", prints: /Hello, World!/ },
];

async function terminalText(page) {
  const rows = page.locator("#terminal-div .xterm-rows");
  if ((await rows.count()) === 0) return "";
  return (await rows.first().innerText()).replace(/ /g, " ").replace(/\n/g, "");
}

for (const run of RUNS) {
  test(`Run works for ${run.name}`, async ({ page }) => {
    test.setTimeout(180 * 1000);
    await page.goto(run.path);
    // the sample program is in the editor, and the terminal has started its REPL
    await page.waitForFunction(() => {
      try {
        return window.editor.env.editor.getValue().length > 20;
      } catch (e) {
        return false;
      }
    });
    await expect(page.locator("#terminal-div .xterm-rows").first()).toContainText(/\S/, { timeout: 60 * 1000 });
    await page.waitForTimeout(2000); // the REPL's prompt is out before Run starts
    await page.locator("#play-button").click();
    await expect
      .poll(() => terminalText(page), { message: `${run.name}: the sample did not print what it should`, timeout: 120 * 1000, intervals: [1000, 2000] })
      .toMatch(run.prints);
    expect(await terminalText(page), "the server could not run the program").not.toMatch(/executable file not found|failed to create backend|command not found|No such file or directory/i);
  });
}
