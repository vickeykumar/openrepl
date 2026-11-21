import { test } from "node:test";
import assert from "node:assert/strict";
import type { SkillInfo } from "../src/skills.ts";
import { commandSpans, limitFor, matchSkills, saveSkillRequest, skillBlock, skillGroup, skillsIn, skillsNoteText, slashToken } from "../src/skills.ts";

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
  assert.ok(r && /files_new/.test(r) && /files_open/.test(r) && /editor_write/.test(r) && /files_save/.test(r), "it names the actions that make the file");
  assert.ok(r && /new file/.test(r), "the words that bring the files actions");
  assert.equal(saveSkillRequest(""), null);
  assert.equal(saveSkillRequest("../x"), null);
  assert.equal(saveSkillRequest("Bad Name"), null);
});

test("a skill's effort lowers the effort of the request and never raises it", async () => {
  const { effortFor } = await import("../src/skills.ts");
  const e = (effort?: string) => sk("x", effort ? { effort } : {});
  assert.equal(effortFor([e("low")], "high"), "low");
  assert.equal(effortFor([e("medium")], "high"), "medium");
  assert.equal(effortFor([e("low")], "low"), null, "the same: nothing to change");
  assert.equal(effortFor([e("high")], "low"), null, "a skill never raises the effort");
  assert.equal(effortFor([e()], "high"), null, "a skill that names none says nothing");
  assert.equal(effortFor([e("low"), e("medium")], "high"), "medium", "the highest of the skills named");
  assert.equal(effortFor([e("low")], "none"), null, "off stays off");
  assert.equal(effortFor([e("bogus")], "high"), null);
});

test("the name of a skill is taken out of the message by its chip, and only that name", async () => {
  const { withoutSkill } = await import("../src/skills.ts");
  assert.equal(withoutSkill("let's /explain this code", "explain"), "let's this code");
  assert.equal(withoutSkill("/explain this", "explain"), "this");
  assert.equal(withoutSkill("see /explain-more and /explain.", "explain"), "see /explain-more and .", "the dot after the name stays");
  assert.equal(withoutSkill("a/explain", "explain"), "a/explain", "inside a word it is not the skill");
});

test("plugin notes are listed with their paths for an agent task, and a task with only notes still gets them", () => {
  const notes = [{ name: "review-checklist", title: "Review checklist", plugin: "code-review-kit", path: ".genie/plugins/code-review-kit/knowledge/review-checklist.md" }];
  const note = skillsNoteText([sk("explain")], notes);
  assert.match(note, /Notes from plugins/);
  assert.match(note, /- Review checklist \(\.genie\/plugins\/code-review-kit\/knowledge\/review-checklist\.md\)/);
  assert.match(note, /does not change your rules|do not change your rules/);
  assert.ok(!note.includes("Skills in this workspace"), "no skills that are files: no skills list");
  assert.match(skillsNoteText(SKILLS, notes), /Skills in this workspace[\s\S]*Notes from plugins/);
  assert.equal(skillsNoteText([sk("explain")], []), "");
  // a skill of a plugin on for everyone has a path under the server's own folder, so Genie reads it like any other
  const site = sk("commit-message", { source: "site", plugin: "git-helper", path: ".genie/site-plugins/git-helper/skills/commit-message.md" });
  assert.match(skillsNoteText([site]), /\/commit-message: does commit-message \(\.genie\/site-plugins\/git-helper\/skills\/commit-message\.md\)/);
});

test("the commands of a message: a skill anywhere, a command of the panel only as the first word, nothing else", () => {
  const cmds = ["context", "tour", "add-plugin", "remember"];
  const names = (t: string) => commandSpans(t, cmds, SKILLS).map((s) => t.slice(s.start, s.end));
  assert.deepEqual(names("/context"), ["/context"]);
  assert.deepEqual(names("  /tour now"), ["/tour"], "spaces before the first word do not matter");
  assert.deepEqual(names("why /context"), [], "a command of the panel is for the first word");
  assert.deepEqual(names("let's /explain this and then /test"), ["/explain", "/test"]);
  assert.deepEqual(names("/remember use /explain"), ["/remember", "/explain"]);
  assert.deepEqual(names("/nothing /usr/bin/explain a/test"), []);
  assert.deepEqual(names("so /explain."), ["/explain"], "a full stop after the name stays out of it");
  assert.deepEqual(names("/exp"), [], "half a name is not a command yet");
  const t = "a /explain b";
  assert.deepEqual(commandSpans(t, cmds, SKILLS), [{ start: 2, end: 10, name: "explain" }]);
});

test("a command that only opens something is a command wherever it is typed, the others at the start only", () => {
  const cmds = ["context", "save-skill"];
  const t = "please /context and /save-skill";
  assert.deepEqual(commandSpans(t, cmds, SKILLS).map((s) => t.slice(s.start, s.end)), []);
  assert.deepEqual(commandSpans(t, cmds, SKILLS, ["context"]).map((s) => t.slice(s.start, s.end)), ["/context"]);
});
