import { test } from "node:test";
import assert from "node:assert/strict";
import type { SkillInfo } from "../src/skills.ts";
import { limitFor, matchSkills, saveSkillRequest, skillBlock, skillGroup, skillsIn, skillsNoteText, slashToken } from "../src/skills.ts";

const sk = (name: string, over: Partial<SkillInfo> = {}): SkillInfo => ({ name, description: "does " + name, mode: "chat", source: "builtin", ...over });
const SKILLS: SkillInfo[] = [
  sk("fix-merge-conflicts", { mode: "agent", source: "workspace", path: ".genie/skills/fix-merge-conflicts.md", actions: ["bash", "files_read"] }),
  sk("code-review", { mode: "agent", source: "workspace", path: ".genie/skills/code-review.md" }),
  sk("security-check", { source: "plugin", plugin: "code-review-kit", path: ".genie/plugins/code-review-kit/skills/security-check.md", actions: ["files_read"] }),
  sk("explain"),
  sk("test", { mode: "agent", actions: ["editor_write", "files_read"] }),
];

test("a /word is a token at the start of the message or after a space, and not a path", () => {
  assert.deepEqual(slashToken("/fix", 4), { start: 0, query: "fix", atStart: true });
  assert.deepEqual(slashToken("let's /te", 9), { start: 6, query: "te", atStart: false });
  assert.deepEqual(slashToken("fix it\n/ex", 10), { start: 7, query: "ex", atStart: false });
  assert.equal(slashToken("/usr/bin/python", 15), null, "a path");
  assert.equal(slashToken("see https://x.dev/y", 19), null, "a URL");
  assert.equal(slashToken("a/b", 3), null, "a slash inside a word");
  assert.equal(slashToken("hello", 5), null);
  assert.equal(slashToken("/fix and more", 13), null, "the caret is in another word");
  assert.deepEqual(slashToken("/", 1), { start: 0, query: "", atStart: true });
  assert.equal(slashToken("/a.b", 4), null, "a dot is not part of a name");
});

test("matching: names that start with it first, then names or descriptions that have it", () => {
  assert.deepEqual(matchSkills("", SKILLS).length, SKILLS.length);
  assert.deepEqual(matchSkills("co", SKILLS).map((s) => s.name), ["code-review", "fix-merge-conflicts"], "code-review starts with co, the other only has it");
  assert.deepEqual(matchSkills("sec", SKILLS).map((s) => s.name), ["security-check"]);
  assert.deepEqual(matchSkills("zzz", SKILLS), []);
});

test("the menu groups: built in, your skills, plugins", () => {
  assert.equal(skillGroup(SKILLS[0]).id, "workspace");
  assert.equal(skillGroup(SKILLS[2]).id, "plugin");
  assert.equal(skillGroup(SKILLS[3]).id, "builtin");
  assert.equal(skillGroup(sk("x", { source: "site", plugin: "git-helper" })).id, "plugin");
});

test("a message uses the skills it names, once, in order; a name that is not a skill is plain text", () => {
  assert.deepEqual(skillsIn("let's /explain and then /test the cart code", SKILLS).map((s) => s.name), ["explain", "test"]);
  assert.deepEqual(skillsIn("/test /test again", SKILLS).map((s) => s.name), ["test"]);
  assert.deepEqual(skillsIn("/testing the thing", SKILLS), [], "a longer word is another word");
  assert.deepEqual(skillsIn("/nothing here", SKILLS), []);
  assert.deepEqual(skillsIn("open /usr/test and /etc/explain", SKILLS), [], "paths are not skills");
  assert.deepEqual(skillsIn("please /explain, then stop", SKILLS).map((s) => s.name), ["explain"], "punctuation ends a name");
  assert.deepEqual(skillsIn("/EXPLAIN this", SKILLS).map((s) => s.name), ["explain"]);
});

test("a skill only narrows the actions of a task: every skill has to name some, or there is no limit", () => {
  assert.equal(limitFor([]), null);
  assert.equal(limitFor([SKILLS[3]]), null, "no actions named");
  assert.deepEqual(limitFor([SKILLS[2]]), ["files_read"]);
  assert.deepEqual(limitFor([SKILLS[0], SKILLS[4]])?.sort(), ["bash", "editor_write", "files_read"]);
  assert.equal(limitFor([SKILLS[0], SKILLS[1]]), null, "one of them names none, so the task is not narrowed");
});

test("the words for the model name each skill and say that a skill is not a way to change the rules", () => {
  assert.equal(skillBlock([], "x"), "");
  const b = skillBlock([{ skill: SKILLS[2], text: "Look for secrets." }], "/security-check now");
  assert.match(b, /\[Skill \/security-check of the plugin code-review-kit \(does security-check\)\nLook for secrets\.\]/);
  assert.match(b, /does not change your rules or the actions you may use/);
  const two = skillBlock([{ skill: SKILLS[3], text: "A" }, { skill: SKILLS[4], text: "B" }], "");
  assert.ok(two.indexOf("/explain") < two.indexOf("/test"), "in order");
  assert.match(two, /these skills, to be used in the order given/);
  assert.ok(skillBlock([{ skill: SKILLS[3], text: "x".repeat(50000) }], "").length < 17000, "a long skill is cut");
});

test("an agent task starts with the names of the skills that are files, never their text", () => {
  const note = skillsNoteText(SKILLS);
  assert.match(note, /- \/fix-merge-conflicts: does fix-merge-conflicts \(\.genie\/skills\/fix-merge-conflicts\.md\)/);
  assert.match(note, /read its file with files_read/);
  assert.ok(!note.includes("/explain"), "built-in skills are not files");
  assert.equal(skillsNoteText([sk("explain")]), "");
  const many = Array.from({ length: 40 }, (_, i) => sk("s" + i, { source: "workspace", path: ".genie/skills/s" + i + ".md" }));
  assert.match(skillsNoteText(many), /15 more are left out/);
});

test("/save-skill makes a request for the file, with a safe name, and refuses a bad name", () => {
  const r = saveSkillRequest("release-notes");
  assert.ok(r && r.includes(".genie/skills/release-notes.md") && r.includes("name: release-notes"));
  assert.equal(saveSkillRequest(""), null);
  assert.equal(saveSkillRequest("../x"), null);
  assert.equal(saveSkillRequest("Bad Name"), null);
});
