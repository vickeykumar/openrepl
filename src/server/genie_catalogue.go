package server

import (
	"encoding/json"
	"sort"
	"strings"
)

// The skills that are built in, and the plugins the site ships (the catalogue). Both are plain skill files, the same format a person
// writes in .genie/skills: they are text, so they can be read, and an admin can switch a plugin on for the whole site.

// pluginManifest is plugin.json.
type pluginManifest struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
	// Connections are outside services a plugin wants (GitHub, Notion). Connections are not part of this version: a plugin that
	// asks for one is not installed from a link, and the catalogue has none.
	Connections []json.RawMessage `json:"connections"`
}

// cataloguePlugin is a plugin the site ships.
type cataloguePlugin struct {
	Name        string
	Title       string
	Version     string
	Description string
	Official    bool
	Skills      []string // the text of each skill file
	Notes       []catalogueNote
}

// catalogueNote is a note a plugin brings: plain Markdown that Genie reads when a task is about what it says (knowledge/<name>.md).
type catalogueNote struct {
	Name string
	Text string
}

func (c cataloguePlugin) skillInfos(source string) []skillInfo {
	var out []skillInfo
	for _, s := range c.Skills {
		if info, _, err := parseSkill(s); err == nil {
			info.Source, info.Plugin = source, c.Name
			// a plugin that is on for everyone has no file in the workspace: Genie reads it from the server under this path
			if source == "site" {
				info.Path = sitePluginDir + c.Name + "/skills/" + info.Name + ".md"
			}
			out = append(out, info)
		}
	}
	return out
}

// sitePluginDir is the path Genie reads the text of a plugin that is on for everyone under (agent_files read; it is not in any workspace).
const sitePluginDir = ".genie/site-plugins/"

// sitePluginText is the text at a path under sitePluginDir, if the plugin is on for everyone and the path is one of its skills or notes.
func sitePluginText(rel string) (string, bool) {
	if !strings.HasPrefix(rel, sitePluginDir) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(rel, sitePluginDir), "/")
	if len(parts) != 3 {
		return "", false
	}
	on := false
	for _, id := range GetSiteSettings().Genie.SitePlugins {
		on = on || id == parts[0]
	}
	c, ok := catalogueByID(parts[0])
	if !on || !ok || !strings.HasSuffix(parts[2], ".md") {
		return "", false
	}
	name := strings.TrimSuffix(parts[2], ".md")
	switch parts[1] {
	case "skills":
		for _, sk := range c.Skills {
			if info, _, err := parseSkill(sk); err == nil && info.Name == name {
				return sk, true
			}
		}
	case "knowledge":
		for _, n := range c.Notes {
			if n.Name == name {
				return n.Text, true
			}
		}
	}
	return "", false
}

func (c cataloguePlugin) manifest() pluginManifest {
	return pluginManifest{Name: c.Name, Title: c.Title, Version: c.Version, Description: c.Description, Author: "OpenREPL"}
}

func catalogueByID(id string) (cataloguePlugin, bool) {
	for _, c := range pluginCatalogue {
		if c.Name == id {
			return c, true
		}
	}
	return cataloguePlugin{}, false
}

// pluginChoice is a plugin of the catalogue as the admin form lists it.
type pluginChoice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func pluginChoices() []pluginChoice {
	out := []pluginChoice{}
	for _, c := range pluginCatalogue {
		out = append(out, pluginChoice{ID: c.Name, Title: c.Title})
	}
	return out
}

// ---- the built-in skills ---------------------------------------------------------------------------------------------------

var builtinSkills = []string{
	`---
name: explain
description: Explain the code or the error in plain words
mode: chat
args: what to explain, if not the editor
---
Explain what the code in the editor does, or the error in the terminal, in plain words for someone who is learning.
Go in order: what it is for, how it works step by step, and the one thing most likely to go wrong.
Keep it short. If the user named something else, explain that.`,
	`---
name: review
description: Look over the code for problems and improvements
mode: chat
args: a file or a worry
---
Review the code in the editor. Say what is wrong first (bugs, missing checks, things that can fail), then what could be simpler or clearer.
Give each point as one line with the line number, and show the fix for the important ones. Say what is good, in one line.
Do not rewrite the whole file.`,
	`---
name: test
description: Write tests for the code in the editor
mode: agent
args: the function to test
actions: [files_read, files_search, editor_write, editor_insert, editor_replace, files_new, files_save, run, read_output, finish]
---
Write tests for the code the user points at, in the language and test style the project already uses (look at its other tests first).
Cover the normal case, the edge cases and one failure. Save the tests in a file next to the code, run them, and say what passed and what failed.
If a test fails because of a bug in the code, say so; do not change the code unless the user asked.`,
	`---
name: fix-merge-conflicts
description: Resolve git conflicts in the workspace
mode: agent
args: the file or branch, if not all
actions: [bash, files_read, files_search, editor_replace, files_save, run, read_output, finish]
---
Find the files that have conflict markers (lines that start with seven < signs).
For each one:
1. Read both sides and the lines around them.
2. Keep the change that matches the intent of the surrounding code; merge both when they do not overlap.
3. Remove the markers and save the file.
When all are done run the tests if there are any, and say what you kept from each side. Do not commit.`,
	`---
name: pr
description: Write a pull request title and description
mode: chat
args: what the change is for
---
Write a pull request for the change the user describes or the code in the editor shows.
Give a title of at most 70 characters, then a description with: what changed, why, and how to check it. Plain words, short lines.
Do not invent changes that are not there.`,
}

func builtinSkillInfos(includeAgent bool) []skillInfo {
	var out []skillInfo
	for _, s := range builtinSkills {
		if info, _, err := parseSkill(s); err == nil {
			if info.Mode == "agent" && !includeAgent {
				continue
			}
			info.Source = "builtin"
			out = append(out, info)
		}
	}
	return out
}

func builtinSkillText(name string) (string, skillInfo, bool) {
	for _, s := range builtinSkills {
		if info, body, err := parseSkill(s); err == nil && info.Name == name {
			info.Source = "builtin"
			return body, info, true
		}
	}
	return "", skillInfo{}, false
}

// ---- the catalogue --------------------------------------------------------------------------------------------------------

var pluginCatalogue = []cataloguePlugin{
	{Name: "code-review-kit", Title: "Code review kit", Version: "1.0", Official: true,
		Description: "A security check, a performance look and test ideas for the code you have open.",
		Notes:       []catalogueNote{{Name: "review-checklist", Text: "# Review checklist\n\nWhat a good review of a change looks at, in this order:\n\n1. Does it do what it says, and is the edge case handled (empty, one, many, wrong type)?\n2. Can it fail, and does the failure say what went wrong?\n3. Is anything trusted that came from outside: input, files, addresses, commands?\n4. Is it slower than it needs to be for the biggest input it will see?\n5. Is there a test, and would it have caught the bug?\n"}},
		Skills: []string{
			`---
name: security-check
description: Look for security problems in the code
mode: chat
actions: [files_read, files_search]
---
Look at the code in the editor for security problems: input that is trusted without a check, secrets in the code, commands or queries built from user text, unsafe file paths, and missing limits.
List each one with the line number, why it matters, and the fix in one or two lines. If you find none, say what you looked for.`,
			`---
name: perf-check
description: Find the slow parts of the code
mode: chat
actions: [files_read, files_search]
---
Look at the code in the editor for what will be slow with a lot of data: loops inside loops, repeated work, reading the same thing again and again, and big copies.
For each, give the line, the cost in plain words (for example "grows with the square of the list") and a simpler way.`,
			`---
name: test-ideas
description: List the tests the code needs, without writing them
mode: chat
actions: [files_read, files_search]
---
List the tests this code needs, as short lines: the normal case, the edge cases (empty, one, many, wrong type), and each way it can fail.
Do not write the test code. Say which one to write first.`,
		}},
	{Name: "git-helper", Title: "Git helper", Version: "1.0", Official: true,
		Description: "A commit message, an explanation of a change, and help to undo a mistake.",
		Skills: []string{
			`---
name: commit-message
description: Write a commit message for the change
mode: chat
args: what the change is for
---
Write a commit message for the change the user describes or the code in the editor shows.
A first line of at most 60 characters in the present tense ("Fix the login check"), a blank line, then two or three short lines on why. No made-up details.`,
			`---
name: explain-diff
description: Explain a diff in plain words
mode: chat
---
Explain the diff the user pasted or the terminal shows: what changed in each file, why it probably changed, and anything that looks risky.
Go file by file, one or two lines each, then one line on the overall effect.`,
			`---
name: undo-help
description: Show how to undo a git mistake
mode: chat
args: what went wrong
---
The user made a git mistake. Ask what they did only if it is not clear, then give the exact commands to get back, in order, each with one line on what it does.
Say first which commands lose work and which do not, and start with the safe ones.`,
		}},
	{Name: "docs-writer", Title: "Docs writer", Version: "1.0", Official: true,
		Description: "A README, comments for functions, and a short summary of a project.",
		Skills: []string{
			`---
name: readme
description: Write a README for the project
mode: agent
args: who it is for
actions: [files_list, files_read, files_search, files_new, files_save, editor_write, finish]
---
Look at the files of the project, then write a README.md: what it is, how to run it, how to use it with one example, and where things are.
Use only what the files show; if something is not clear, leave it out or mark it as a question for the user. Do not change any other file.`,
			`---
name: comments
description: Add short comments to the functions in the editor
mode: agent
actions: [files_read, editor_replace, editor_insert, files_save, finish]
---
Add a short comment above each function in the editor file that has none: what it does and what it returns, in one or two lines, in the comment style of the language.
Do not change the code itself, and do not comment the obvious. Save the file.`,
		}},
}

func catalogueNames() []string {
	var n []string
	for _, c := range pluginCatalogue {
		n = append(n, c.Name)
	}
	sort.Strings(n)
	return n
}

// validSitePlugins checks the admin's list of catalogue plugins that are on for everyone: only ids of the catalogue, no repeats.
func validSitePlugins(ids []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := catalogueByID(id); !ok {
			return nil, false
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, true
}
