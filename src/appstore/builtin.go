package appstore

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Built-in apps (LLD 18): what the server image already has. The server looks for each one when it starts (a program on the PATH and its
// version); the store lists them in My apps, tagged "Built in", for information only: no install, no uninstall, no switch, no request,
// and never counted in the disk limit. A built-in name cannot also be installed as an app.

// Builtin is one built-in app: a program, or a few that go together, and the REPLs that use it.
type Builtin struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Used    []string `json:"used"`           // the REPL ids that use it
	Docs    string   `json:"docs,omitempty"` // the official documentation
	Names   []string `json:"names"`          // the package names it stands for: a built-in name cannot be installed as an app
}

type builtinCmd struct {
	prog string
	args []string
	sh   string // a shell line instead (for a program that reads its version from its input)
}

type builtinSpec struct {
	Builtin
	cmds []builtinCmd
}

// builtinTable is the list. Each command prints the version; a program that is not found is left out of the answer.
var builtinTable = []builtinSpec{
	{Builtin{Name: "Python", Used: []string{"python", "ipython3"}, Docs: "https://docs.python.org/3.10/", Names: []string{"python", "python3", "ipython"}}, []builtinCmd{{prog: "python3", args: []string{"--version"}}}},
	{Builtin{Name: "Python 2.7", Used: []string{"python2.7"}, Docs: "https://docs.python.org/2.7/", Names: []string{"python2", "python2.7"}}, []builtinCmd{{prog: "python2.7", args: []string{"--version"}}}},
	{Builtin{Name: "Node.js", Used: []string{"node", "ts-node"}, Docs: "https://nodejs.org/docs/latest-v24.x/api/", Names: []string{"node", "nodejs"}}, []builtinCmd{{prog: "node", args: []string{"--version"}}}},
	{Builtin{Name: "TypeScript", Used: []string{"ts-node"}, Docs: "https://www.typescriptlang.org/docs/", Names: []string{"typescript", "tsc", "ts-node"}}, []builtinCmd{{prog: "tsc", args: []string{"--version"}}}},
	{Builtin{Name: "Go", Used: []string{"go", "yaegi"}, Docs: "https://go.dev/doc/", Names: []string{"go", "golang", "yaegi", "gomacro"}}, []builtinCmd{{prog: "go", args: []string{"version"}}}},
	{Builtin{Name: "OpenJDK", Used: []string{"java"}, Docs: "https://docs.oracle.com/en/java/javase/21/", Names: []string{"openjdk", "java", "jdk", "jshell"}}, []builtinCmd{{prog: "javac", args: []string{"-version"}}}},
	{Builtin{Name: "Rust (rustc, cargo)", Used: []string{"evcxr"}, Docs: "https://doc.rust-lang.org/stable/", Names: []string{"rust", "rustc", "cargo", "evcxr"}}, []builtinCmd{{prog: "rustc", args: []string{"--version"}}}},
	{Builtin{Name: "gcc, g++, cling", Used: []string{"c", "cpp"}, Docs: "https://gcc.gnu.org/onlinedocs/", Names: []string{"gcc", "gxx", "g++", "cling", "gcc-12"}}, []builtinCmd{{prog: "gcc", args: []string{"--version"}}, {prog: "cling", args: []string{"--version"}}}},
	{Builtin{Name: "Ruby", Used: []string{"irb"}, Docs: "https://docs.ruby-lang.org/en/3.0/", Names: []string{"ruby", "irb"}}, []builtinCmd{{prog: "ruby", args: []string{"--version"}}}},
	{Builtin{Name: "Perl", Used: []string{"perli"}, Docs: "https://perldoc.perl.org/", Names: []string{"perl"}}, []builtinCmd{{prog: "perl", args: []string{"-e", "print $^V"}}}},
	{Builtin{Name: "Tcl", Used: []string{"tclsh"}, Docs: "https://www.tcl.tk/man/tcl8.6/", Names: []string{"tcl", "tclsh"}}, []builtinCmd{{sh: "echo 'puts [info patchlevel]' | tclsh"}}},
	{Builtin{Name: "SQLite", Used: []string{"sqlite3"}, Docs: "https://www.sqlite.org/docs.html", Names: []string{"sqlite", "sqlite3"}}, []builtinCmd{{prog: "sqlite3", args: []string{"--version"}}}},
	{Builtin{Name: "jq", Used: []string{"jq-repl"}, Docs: "https://jqlang.github.io/jq/manual/v1.6/", Names: []string{"jq"}}, []builtinCmd{{prog: "jq", args: []string{"--version"}}}},
	{Builtin{Name: "nasm", Used: []string{"rappel"}, Docs: "https://www.nasm.us/docs.php", Names: []string{"nasm", "rappel"}}, []builtinCmd{{prog: "nasm", args: []string{"-v"}}}},
}

var builtinVersion = regexp.MustCompile(`[0-9]+(\.[0-9]+)+`)

// DetectBuiltins looks for each program of the table and reads its version. A program that is not there, or does not say a version,
// is left out. The grouped entries join the versions of the programs they hold ("11.4 / 1.2").
func DetectBuiltins(ctx context.Context) []Builtin {
	var out []Builtin
	for _, spec := range builtinTable {
		var versions []string
		for _, c := range spec.cmds {
			if v := runVersion(ctx, c); v != "" {
				versions = append(versions, v)
			}
		}
		if len(versions) == 0 {
			continue
		}
		b := spec.Builtin
		b.Version = strings.Join(versions, " / ")
		out = append(out, b)
	}
	return out
}

func runVersion(ctx context.Context, c builtinCmd) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if c.sh != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", c.sh)
	} else {
		if _, err := exec.LookPath(c.prog); err != nil {
			return ""
		}
		cmd = exec.CommandContext(ctx, c.prog, c.args...)
	}
	out, _ := cmd.CombinedOutput() // some print the version on stderr, some exit non-zero
	first := strings.SplitN(string(out), "\n", 2)[0]
	if !strings.ContainsAny(first, "0123456789") && len(out) > 0 {
		first = string(out)
	}
	return builtinVersion.FindString(first)
}

// SetBuiltins sets what this server has built in (made at its start).
func (s *Service) SetBuiltins(list []Builtin) {
	s.mu.Lock()
	s.builtins = list
	s.mu.Unlock()
}

// Builtins is the list, for the store page.
func (s *Service) Builtins() []Builtin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Builtin{}, s.builtins...)
}

// BuiltinNamed finds the built-in app a package name stands for.
func (s *Service) BuiltinNamed(name string) (Builtin, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.builtins {
		for _, n := range b.Names {
			if n == name {
				return b, true
			}
		}
	}
	return Builtin{}, false
}
