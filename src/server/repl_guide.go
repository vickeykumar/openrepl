package server

import (
	"strings"

	"utils"
)

// What Genie's agent is told about the REPL of the language in use, so that it
// types code at the prompt the way the REPL takes it instead of guessing (a
// model that does not know gointerpreter restarts the terminal or types :r
// where a line of Go was wanted).
//
// The text is built from the demo of the REPL (resources/meta/demos.xml, the
// same structure /demo?q=<repl> serves to the page): its prompt, its commands,
// an example session, and the Notes written for Genie where a REPL has habits
// the examples do not show. A REPL added to demos.xml gets a guide with it.

const (
	replGuideMaxLines  = 14   // statements of the example session
	replGuideMaxText   = 240  // of one statement or result
	replGuideMaxLength = 3600 // of the whole guide
)

// replGuide returns the guide for a REPL by its command name ("gointerpreter",
// "python"), "" for a name that has no demo.
func replGuide(command string) string {
	if utils.Commands2DemoMap == nil {
		InitCommands2DemoMap()
	}
	command = strings.TrimSpace(command)
	demo, ok := utils.Commands2DemoMap[command]
	if !ok || command == "" {
		return ""
	}

	prompt := ""
	for _, group := range demo.Codes {
		for _, c := range group.Code {
			if p := strings.TrimSpace(c.Prompt); p != "" && prompt == "" {
				prompt = p
			}
		}
	}

	var b strings.Builder
	b.WriteString("REPL guide. The terminal runs the REPL \"" + command + "\"")
	if prompt != "" {
		b.WriteString(", whose prompt is \"" + prompt + "\"")
	}
	b.WriteString(". To try code, type it at that prompt with terminal_type, one line per action, and read what comes back. Do not restart the terminal, and do not use a command of the REPL, to do what a line of code does.\n")

	if len(demo.Usage) > 0 {
		b.WriteString("\nCommands of this REPL:\n")
		for _, u := range demo.Usage {
			cmd := strings.TrimSpace(u.Command)
			if cmd == "" {
				continue
			}
			b.WriteString("  " + cmd + "  " + oneLine(u.Description, replGuideMaxText) + "\n")
		}
	}

	lines := 0
	var session strings.Builder
	for _, group := range demo.Codes {
		for _, c := range group.Code {
			statement := strings.TrimSpace(c.Statement)
			if statement == "" || lines >= replGuideMaxLines {
				continue
			}
			lines++
			session.WriteString("  " + strings.TrimSpace(c.Prompt) + " " + indentRest(clip(statement, replGuideMaxText)) + "\n")
			if result := strings.TrimSpace(c.Result); result != "" {
				session.WriteString("  " + indentRest(clip(result, replGuideMaxText)) + "\n")
			}
		}
	}
	if lines > 0 {
		b.WriteString("\nAn example session (a statement shown on several lines is typed as ONE line by you):\n")
		b.WriteString(session.String())
	}

	if notes := strings.TrimSpace(demo.Notes); notes != "" {
		b.WriteString("\nHow this REPL behaves:\n" + notes + "\n")
	}
	return clip(strings.TrimRight(b.String(), "\n"), replGuideMaxLength)
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + " ..."
}

func oneLine(s string, max int) string {
	return clip(strings.Join(strings.Fields(s), " "), max)
}

// indentRest keeps the later lines of a text under its first one.
func indentRest(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\n    ")
}
