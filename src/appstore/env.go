package appstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// the libraries of C, C++, Tcl and Perl (a bundle with no Python of its own: a header or a library, not a program that brings Python)
	Include   []string // include folders: CPLUS_INCLUDE_PATH and C_INCLUDE_PATH
	Lib       []string // lib folders: LIBRARY_PATH (link time) and LD_RUN_PATH
	PkgConfig []string // lib/pkgconfig and share/pkgconfig: PKG_CONFIG_PATH
	Perl      []string // PERL5LIB
	Tcl       []string // TCLLIBPATH
	// Extra are the variables the packages set when they are activated (JAVA_HOME, PROJ_DATA, ...), read from each bundle's
	// activate.json; the first bundle on the list wins a name two of them set.
	Extra map[string]string
}

// activateFile is where a node saves the variables a bundle's packages set at activation (made at install, see ActivationFrom).
const activateFile = "activate.json"

// extraDeny are variables a package may not set for a user: the ones this file builds itself, and the ones that would change what the
// system's own programs load or where a user's shell starts.
var extraDeny = map[string]bool{"PATH": true, "PYTHONPATH": true, "NODE_PATH": true, "LD_LIBRARY_PATH": true, "LD_PRELOAD": true, "LD_AUDIT": true,
	"HOME": true, "USER": true, "SHELL": true, "PWD": true, "OLDPWD": true, "TMPDIR": true, "XDG_DATA_DIRS": true, "XDG_CONFIG_DIRS": true, "PYTHONHOME": true,
	"IFS": true, "ENV": true, "BASH_ENV": true, "PS1": true, "PROMPT_COMMAND": true}

var extraName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ValidExtra says whether a variable of an activation may be passed on: an ordinary upper case name that is not in extraDeny, not one of
// the manager's own (CONDA_*, MAMBA_*, PIXI_*), and a value with no line break or NUL, at most 4096 bytes.
func ValidExtra(name, value string) bool {
	if !extraName.MatchString(name) || extraDeny[name] || len(value) > 4096 || strings.ContainsAny(value, "\x00\n\r") {
		return false
	}
	for _, p := range []string{"CONDA_", "MAMBA_", "PIXI_", "LD_", "BASH_"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// ActivationFrom turns the environment a package activation produced into what is kept: the variables that are new or changed against
// the plain environment, only the ones ValidExtra allows. before and after are lists of NAME=value.
func ActivationFrom(before, after []string) map[string]string {
	base := map[string]string{}
	for _, e := range before {
		if i := strings.IndexByte(e, '='); i > 0 {
			base[e[:i]] = e[i+1:]
		}
	}
	out := map[string]string{}
	for _, e := range after {
		i := strings.IndexByte(e, '=')
		if i <= 0 {
			continue
		}
		k, val := e[:i], e[i+1:]
		if old, ok := base[k]; ok && old == val {
			continue
		}
		if ValidExtra(k, val) && val != "" {
			out[k] = val
		}
	}
	return out
}

// WriteActivation saves a bundle's activation variables in the bundle (nothing is written when there are none).
func WriteActivation(bundle string, vars map[string]string) error {
	p := filepath.Join(bundle, activateFile)
	if len(vars) == 0 {
		os.Remove(p)
		return nil
	}
	raw, err := json.Marshal(map[string]interface{}{"env": vars})
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0644)
}

// readActivation reads them; a file that is not there, or is not valid, gives none (it is a convenience, never a reason to stop a terminal).
func readActivation(bundle string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(bundle, activateFile))
	if err != nil || len(raw) > 64<<10 {
		return nil
	}
	var f struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	out := map[string]string{}
	for k, val := range f.Env {
		if ValidExtra(k, val) {
			out[k] = val
		}
	}
	return out
}

// VarsFor works out the folders of the bundles. Only folders that exist are listed. The names must be valid bundle names:
// the caller has read them from a manifest (ReadManifest checks them).
func VarsFor(dir string, ids []string) Vars {
	var v Vars
	for _, id := range ids {
		if !ValidBundle(id) {
			continue
		}
		bundle := filepath.Join(dir, "apps", id)
		root := PrefixOf(bundle)
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
		for k, val := range readActivation(bundle) {
			if v.Extra == nil {
				v.Extra = map[string]string{}
			}
			if _, taken := v.Extra[k]; !taken {
				v.Extra[k] = val
			}
		}
		if isDir(filepath.Join(root, "lib", "node_modules")) {
			v.NodePath = append(v.NodePath, filepath.Join(root, "lib", "node_modules"))
		}
		if len(sp) == 0 && root != bundle { // a conda prefix that has no Python of its own
			if isDir(filepath.Join(root, "include")) {
				v.Include = append(v.Include, filepath.Join(root, "include"))
				if isDir(filepath.Join(root, "lib")) {
					v.Lib = append(v.Lib, filepath.Join(root, "lib"))
				}
			}
			for _, pc := range []string{"lib/pkgconfig", "share/pkgconfig"} {
				if isDir(filepath.Join(root, pc)) {
					v.PkgConfig = append(v.PkgConfig, filepath.Join(root, pc))
				}
			}
			if tcl, _ := filepath.Glob(filepath.Join(root, "lib", "tcllib*")); len(tcl) > 0 {
				v.Tcl = append(v.Tcl, filepath.Join(root, "lib"))
			}
			for _, pat := range []string{"lib/perl5/site_perl/*", "lib/perl5/[0-9]*"} {
				pl, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
				sort.Strings(pl)
				for _, p := range pl {
					if isDir(p) {
						v.Perl = append(v.Perl, p)
					}
				}
			}
		}
		// a Node bundle made by npm: <bundle>/node_modules, with its .bin
		if isDir(filepath.Join(bundle, "node_modules")) {
			v.NodePath = append(v.NodePath, filepath.Join(bundle, "node_modules"))
			if isDir(filepath.Join(bundle, "node_modules", ".bin")) {
				v.PathAdd = append(v.PathAdd, filepath.Join(bundle, "node_modules", ".bin"))
			}
		}
	}
	return v
}

// PrefixOf is the conda prefix of a bundle: <bundle>/.pixi/envs/default for a bundle that pixi made (the bundle folder is its project:
// pixi.toml, pixi.lock and the environment), the bundle folder itself for the ones that were made before (micromamba), and for Node and
// plugin bundles, which have no prefix.
func PrefixOf(bundle string) string {
	if p := filepath.Join(bundle, ".pixi", "envs", "default"); isDir(p) {
		return p
	}
	return bundle
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
	for _, x := range []struct {
		names []string
		dirs  []string
	}{{[]string{"CPLUS_INCLUDE_PATH", "C_INCLUDE_PATH"}, v.Include}, {[]string{"LIBRARY_PATH", "LD_RUN_PATH"}, v.Lib}, {[]string{"PKG_CONFIG_PATH"}, v.PkgConfig},
		{[]string{"PERL5LIB"}, v.Perl}, {[]string{"TCLLIBPATH"}, v.Tcl}} {
		for _, n := range x.names {
			if len(x.dirs) > 0 {
				set(n, join(x.dirs, get(n)))
			}
		}
	}
	for k, val := range v.Extra {
		set(k, val)
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
	for _, x := range []struct {
		names []string
		dirs  []string
	}{{[]string{"CPLUS_INCLUDE_PATH", "C_INCLUDE_PATH"}, v.Include}, {[]string{"LIBRARY_PATH", "LD_RUN_PATH"}, v.Lib}, {[]string{"PKG_CONFIG_PATH"}, v.PkgConfig},
		{[]string{"PERL5LIB"}, v.Perl}, {[]string{"TCLLIBPATH"}, v.Tcl}} {
		for _, n := range x.names {
			if len(x.dirs) > 0 {
				fmt.Fprintf(&b, "export %s=%s${%s:+:$%s}\n", n, q(strings.Join(x.dirs, ":")), n, n)
			}
		}
	}
	keys := make([]string, 0, len(v.Extra))
	for k := range v.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, q(v.Extra[k]))
	}
	return b.String()
}
