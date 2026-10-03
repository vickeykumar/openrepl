package utils

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strings"
)

// An env file is a file of NAME=value lines that gotty loads into its own
// environment when it starts, so that the settings of utils/config.go and the
// GOTTY_* flags can be kept in one file instead of being exported by hand. It
// is what --env-file names, ~/.env by default.
//
//	# comment
//	OPENREPL_ENV=dev
//	export OPENREPL_ADMIN_EMAILS="a@example.com,b@example.com"
//	OPENREPL_HOST='openrepl.example.com'   # a comment after a value
//
// Values can be unquoted, in single quotes (literal) or in double quotes
// (\n, \r, \t, \", \\ and \$ are understood). There is no $VAR expansion and a
// value cannot span lines.
//
// Three rules keep a file that other tools may share, like ~/.env, from doing
// harm. The file can only set OPENREPL_* and GOTTY_* variables; anything else in
// it is reported and ignored, so it cannot change PATH or LD_PRELOAD for the
// server. A variable that is already set in the environment keeps its value,
// as with any dotenv. And the file's values are never written to the log.
const (
	EnvFileDefault = "~/.env"
	EnvEnvFile     = "GOTTY_ENV_FILE"
)

// EnvFileResult says what loading an env file did. It holds names, never
// values.
type EnvFileResult struct {
	Path    string   // the file that was read, "" if there was none
	Loaded  []string // set from the file
	Kept    []string // in the file, but the environment already had them
	Ignored []string // in the file, but not OPENREPL_* or GOTTY_*
}

// LastEnvFile is the result of the last LoadEnvFile, for LogConfig.
var LastEnvFile EnvFileResult

// EnvPair is one line of an env file.
type EnvPair struct {
	Name, Value string
	Line        int
}

// EnvProblem is a line of an env file that could not be understood. The
// message never repeats the line.
type EnvProblem struct {
	Line int
	What string
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseEnvFile reads the lines of an env file. When a name appears twice, the
// last one wins and takes the place of the first.
func ParseEnvFile(data []byte) (pairs []EnvPair, problems []EnvProblem) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	index := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			problems = append(problems, EnvProblem{n, "not NAME=value"})
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if !envNamePattern.MatchString(name) {
			problems = append(problems, EnvProblem{n, "the name is not a valid variable name"})
			continue
		}
		value, what := parseEnvValue(strings.TrimLeft(line[eq+1:], " \t"))
		if what != "" {
			problems = append(problems, EnvProblem{n, what})
			continue
		}
		pair := EnvPair{Name: name, Value: value, Line: n}
		if at, seen := index[name]; seen {
			pairs[at] = pair
		} else {
			index[name] = len(pairs)
			pairs = append(pairs, pair)
		}
	}
	return pairs, problems
}

// parseEnvValue reads what follows the "=": a value, and what is wrong with it
// if anything is.
func parseEnvValue(s string) (value, problem string) {
	if s == "" {
		return "", ""
	}
	rest := func(after string) string {
		after = strings.TrimSpace(after)
		if after != "" && !strings.HasPrefix(after, "#") {
			return "there is text after the closing quote"
		}
		return ""
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "a quote is not closed on this line"
		}
		return s[1 : 1+end], rest(s[2+end:])
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			switch c := s[i]; {
			case c == '\\' && i+1 < len(s):
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case '"', '\\', '$':
					b.WriteByte(s[i])
				default:
					b.WriteByte('\\')
					b.WriteByte(s[i])
				}
			case c == '"':
				return b.String(), rest(s[i+1:])
			default:
				b.WriteByte(c)
			}
		}
		return "", "a quote is not closed on this line"
	}
	// unquoted: a comment starts at a # that follows a space
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimSpace(s), ""
}

// envFileMaySet reports whether an env file is allowed to set a variable.
func envFileMaySet(name string) bool {
	return strings.HasPrefix(name, "OPENREPL_") || strings.HasPrefix(name, "GOTTY_")
}

// expandHome turns a leading ~ into the home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home := homeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return home + path[1:]
}

// LoadEnvFile loads the env file at path into the environment. A file that
// does not exist is not an error unless the operator named it (explicit); one
// that cannot be read, or in which a line cannot be understood, is an error
// then too, and a warning in the log otherwise, because the default
// ~/.env may be a file other tools use.
func LoadEnvFile(path string, explicit bool) (EnvFileResult, error) {
	LastEnvFile = EnvFileResult{}
	full := expandHome(path)
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		err = fmt.Errorf("%s is a directory", full)
	}
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return LastEnvFile, nil
		}
		if !explicit {
			log.Printf("Warning: not loading env file %s: %v", full, err)
			return LastEnvFile, nil
		}
		return LastEnvFile, fmt.Errorf("cannot read env file %s: %v", full, err)
	}
	data, err := ioutil.ReadFile(full)
	if err != nil {
		if !explicit {
			log.Printf("Warning: not loading env file %s: %v", full, err)
			return LastEnvFile, nil
		}
		return LastEnvFile, fmt.Errorf("cannot read env file %s: %v", full, err)
	}
	pairs, problems := ParseEnvFile(data)
	if len(problems) > 0 {
		var msgs []string
		for _, p := range problems {
			msgs = append(msgs, fmt.Sprintf("line %d: %s", p.Line, p.What))
		}
		if explicit {
			return LastEnvFile, fmt.Errorf("env file %s: %s", full, strings.Join(msgs, "; "))
		}
		log.Printf("Warning: env file %s: %s (those lines are skipped)", full, strings.Join(msgs, "; "))
	}
	if info.Mode().Perm()&0077 != 0 {
		log.Printf("Warning: env file %s can be read by other users; run: chmod 600 %s", full, full)
	}
	res := EnvFileResult{Path: full}
	for _, pair := range pairs {
		switch {
		case !envFileMaySet(pair.Name):
			res.Ignored = append(res.Ignored, pair.Name)
		case os.Getenv(pair.Name) != "":
			res.Kept = append(res.Kept, pair.Name)
		default:
			if err := os.Setenv(pair.Name, pair.Value); err != nil {
				return res, fmt.Errorf("env file %s: line %d: %v", full, pair.Line, err)
			}
			res.Loaded = append(res.Loaded, pair.Name)
		}
	}
	LastEnvFile = res
	return res, nil
}
