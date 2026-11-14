package appstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Vars is what a terminal gets so that it can use the bundles that are on for it. PathAdd goes after the system's PATH (the
// system's python3 stays the one that runs), the others go before what is there. There is no LD_LIBRARY_PATH: a conda program
// finds its libraries by the RPATH it was built with, and a path of conda libraries in front of the system's would break the
// system's own programs.
type Vars struct {
	PathAdd    []string // bin folders
	PythonPath []string // site-packages folders
	NodePath   []string // lib/node_modules folders
}

// VarsFor works out the folders of the bundles. Only folders that exist are listed. The names must be valid bundle names:
// the caller has read them from a manifest (ReadManifest checks them).
func VarsFor(dir string, ids []string) Vars {
	var v Vars
	for _, id := range ids {
		if !ValidBundle(id) {
			continue
		}
		root := filepath.Join(dir, "apps", id)
		if isDir(filepath.Join(root, "bin")) {
			v.PathAdd = append(v.PathAdd, filepath.Join(root, "bin"))
		}
		sp, _ := filepath.Glob(filepath.Join(root, "lib", "python3*", "site-packages"))
		sort.Strings(sp)
		for _, p := range sp {
			if isDir(p) {
				v.PythonPath = append(v.PythonPath, p)
			}
		}
		if isDir(filepath.Join(root, "lib", "node_modules")) {
			v.NodePath = append(v.NodePath, filepath.Join(root, "lib", "node_modules"))
		}
	}
	return v
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Apply puts the folders in front of (or, for PATH, behind) what an environment already has. env is a list of NAME=value.
func (v Vars) Apply(env []string) []string {
	get := func(k string) string {
		for _, e := range env {
			if strings.HasPrefix(e, k+"=") {
				return e[len(k)+1:]
			}
		}
		return ""
	}
	set := func(k, val string) {
		for i, e := range env {
			if strings.HasPrefix(e, k+"=") {
				env[i] = k + "=" + val
				return
			}
		}
		env = append(env, k+"="+val)
	}
	join := func(first []string, then string) string {
		out := strings.Join(first, ":")
		if then != "" {
			if out != "" {
				out += ":"
			}
			out += then
		}
		return out
	}
	if len(v.PathAdd) > 0 {
		p := get("PATH")
		if p == "" {
			p = "/usr/local/bin:/usr/bin:/bin"
		}
		set("PATH", p+":"+strings.Join(v.PathAdd, ":"))
	}
	if len(v.PythonPath) > 0 {
		set("PYTHONPATH", join(v.PythonPath, get("PYTHONPATH")))
	}
	if len(v.NodePath) > 0 {
		set("NODE_PATH", join(v.NodePath, get("NODE_PATH")))
	}
	return env
}

// EnvScript is the env.sh of a user: what a shell runs ( . env.sh ) to use the bundles that are on, without a new terminal.
func EnvScript(dir string, ids []string) string {
	v := VarsFor(dir, ids)
	var b strings.Builder
	b.WriteString("# made by OpenREPL's App Store: the bundles that are on for you. Do not edit.\n")
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	if len(v.PathAdd) > 0 {
		fmt.Fprintf(&b, "export PATH=\"$PATH\":%s\n", q(strings.Join(v.PathAdd, ":")))
	}
	if len(v.PythonPath) > 0 {
		fmt.Fprintf(&b, "export PYTHONPATH=%s${PYTHONPATH:+:$PYTHONPATH}\n", q(strings.Join(v.PythonPath, ":")))
	}
	if len(v.NodePath) > 0 {
		fmt.Fprintf(&b, "export NODE_PATH=%s${NODE_PATH:+:$NODE_PATH}\n", q(strings.Join(v.NodePath, ":")))
	}
	return b.String()
}
