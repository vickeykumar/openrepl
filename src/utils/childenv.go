package utils

import (
	"os"
	"strings"
)

// The programs that users run (a bash, a Python, a C++ interpreter) are child
// processes of the server. Left alone they inherit its whole environment, and
// then anyone can read the server's secrets with `env`: the OpenAI key, the
// worker token, the basic-auth credential. ChildEnviron is what they get
// instead, and ExpandIn is how the environment box of the IDE is expanded.

// serverEnvPrefixes mark the variables that configure the server itself:
// OPENREPL_* (utils/config.go) and GOTTY_*, which is what every gotty flag
// can be given as, such as GOTTY_WORKER_TOKEN and GOTTY_CREDENTIAL.
var serverEnvPrefixes = []string{"OPENREPL_", "GOTTY_"}

// secretEnvWords catch the secrets that the server's own environment may hold
// without being called one of the above, such as a cloud key.
var secretEnvWords = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "API_KEY", "APIKEY", "ACCESS_KEY"}

// IsServerEnv reports whether an environment variable belongs to the server
// and must not be passed on to a program a user runs. The test is on the name,
// ignores case, and errs on the side of keeping a secret back.
func IsServerEnv(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range serverEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, word := range secretEnvWords {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return false
}

// ChildEnviron returns environ (as os.Environ gives it) without the variables
// that belong to the server.
func ChildEnviron(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if !IsServerEnv(name) {
			out = append(out, kv)
		}
	}
	return out
}

// ExpandIn replaces $VAR and ${VAR} in s with their values in env, a list of
// NAME=value entries in which a later entry wins. A name that is not in env
// becomes empty. Unlike os.ExpandEnv it never looks at the server's own
// environment, so what a user types in the environment box cannot be used to
// read it.
func ExpandIn(env []string, s string) string {
	return os.Expand(s, func(name string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], name+"=") {
				return env[i][len(name)+1:]
			}
		}
		return ""
	})
}
