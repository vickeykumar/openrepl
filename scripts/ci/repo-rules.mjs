// Runs the rules of lib-repo-rules.mjs on the files a pull request (or a push) changes.
//   BASE_SHA and HEAD_SHA say which commits to compare; GitHub's annotations
//   (::error, ::warning) show the problems on the pull request.
import { execFileSync } from "node:child_process";
import { forbiddenPaths, lineEndingProblems, docsReminder } from "./lib-repo-rules.mjs";

const base = process.env.BASE_SHA;
const head = process.env.HEAD_SHA || "HEAD";
if (!base || /^0+$/.test(base)) {
  console.log("No base commit to compare with (a new branch?): nothing to check.");
  process.exit(0);
}

const git = (...args) => execFileSync("git", args, { encoding: "utf8", maxBuffer: 256 * 1024 * 1024 });
const names = (filter) => git("diff", "--name-only", "-z", `--diff-filter=${filter}`, `${base}...${head}`).split("\0").filter(Boolean);

const changed = names("ACMR");
const touched = names("ACMRD");
console.log(`${changed.length} files added or changed.`);

let errors = 0;
for (const p of forbiddenPaths(changed)) {
  console.log(`::error file=${p.file}::Do not commit this file: ${p.why}.`);
  errors++;
}

const show = (rev, file) => {
  try {
    return git("show", `${rev}:${file}`);
  } catch (e) {
    return null; // the file is new
  }
};
const vendored = (f) => /^src\/(github\.com|golang\.org|gopkg\.in|google\.golang\.org)\//.test(f) || f.startsWith("vendor/") || f.startsWith("go_1.19/");
const endings = lineEndingProblems(
  changed.filter((f) => !vendored(f)),
  (f) => ({ base: show(base, f), head: show(head, f) })
);
for (const p of endings) {
  console.log(`::error file=${p.file}::${p.why}.`);
  errors++;
}

const reminder = docsReminder(touched);
if (reminder) console.log(`::warning title=Docs::${reminder}`);

if (errors) {
  console.log(`${errors} problem(s) found.`);
  process.exit(1);
}
console.log("The repository rules are kept.");
