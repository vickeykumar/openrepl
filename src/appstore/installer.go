package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Installer makes bundles of every kind: conda-forge packages (libraries, tools, languages) with pixi, Node libraries with npm,
// and the editor plugins that come with the server. A test, or a dev server without them, uses the fake. The server runs it outside
// any terminal's sandbox, so a terminal needs no network for an install.
type Installer interface {
	// Resolve says which version of a package an install would get now (the newest that works with the server's Python, for a
	// library), what it brings with it and how big it is. version "" means the newest; a version an admin picked is checked the same way.
	Resolve(ctx context.Context, name, version, kind string) (Resolved, error)
	// Install makes the finished contents of a bundle in dest (which does not exist yet); what it says goes to log. It does not
	// write the marker: the caller does, after its own checks.
	Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error
	// Search finds packages by a few letters of their name, for the store's search box. kind "" is every kind the installer has.
	Search(ctx context.Context, q, kind string) ([]Found, error)
	// Name says which installer this is, for the admin page.
	Name() string
}

// Resolved is what a solve found: the version of the package, and what an install of it brings with it.
type Resolved struct {
	Version    string
	Packages   int     // how many packages (the app and its dependencies) are linked
	DownloadMB float64 // what has to be fetched, when the solver says
	DiskMB     float64 // about what it takes on the disk once installed
}

// Found is one answer of a search.
type Found struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Summary string `json:"summary,omitempty"`
	Kind    string `json:"kind"`
}

var searchPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,40}$`)

// ---- npm -----------------------------------------------------------------------------------------------------------------

// Npm installs Node libraries from the npm registry into a bundle of their own: <bundle>/node_modules, with its .bin. Scripts of
// the packages are not run (--ignore-scripts), the way a Python library is only unpacked.
type Npm struct {
	Bin     string // the npm program
	Node    string // the node program whose version a library must run on ("": found on the PATH, or in the image's nvm)
	Dir     string // the store (the npm cache is <dir>/npm-cache)
	Timeout time.Duration
}

func (n Npm) Name() string { return "npm (registry.npmjs.org)" }

func (n Npm) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, n.Bin, args...)
	c.Env = append(os.Environ(), "npm_config_cache="+filepath.Join(n.Dir, "npm-cache"), "npm_config_update_notifier=false", "npm_config_fund=false", "npm_config_audit=false")
	c.Dir = os.TempDir()
	return c
}

func (n Npm) with(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	t := n.Timeout
	if t == 0 {
		t = d
	}
	return context.WithTimeout(ctx, t)
}

// nodeVersion is the version of the Node the server (and the users' terminals) run, "" when there is none.
func (n Npm) nodeVersion(ctx context.Context) string {
	node := n.Node
	if node == "" {
		node, _ = exec.LookPath("node")
	}
	if node == "" {
		if m, _ := filepath.Glob("/root/.nvm/versions/node/*/bin/node"); len(m) > 0 {
			node = m[len(m)-1]
		}
	}
	if node == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (n Npm) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	if !ValidName(name) || (version != "" && !ValidVersion(version)) {
		return Resolved{}, fmt.Errorf("%q %q is not a package", name, version)
	}
	ctx, cancel := n.with(ctx, 5*time.Minute)
	defer cancel()
	view := func(spec string, fields ...string) ([]byte, error) {
		var out, errb bytes.Buffer
		c := n.cmd(ctx, append(append([]string{"view", spec}, fields...), "--json")...)
		c.Stdout, c.Stderr = &out, &errb
		if err := c.Run(); err != nil {
			return nil, fmt.Errorf("npm could not find %s: %s", spec, tail(errb.String()+out.String(), 400))
		}
		return out.Bytes(), nil
	}
	if version != "" {
		raw, err := view(name+"@"+version, "version", "dist.unpackedSize")
		if err != nil {
			return Resolved{}, err
		}
		return npmViewFrom(raw)
	}
	// the newest version that the server's Node can run: the latest of a library often needs a newer Node than the image has
	raw, err := view(name, "versions")
	if err != nil {
		return Resolved{}, err
	}
	cands := npmCandidates(raw)
	if len(cands) == 0 {
		return Resolved{}, fmt.Errorf("npm lists no version of %s", name)
	}
	node := n.nodeVersion(ctx)
	for i, v := range cands {
		if i >= 40 {
			break
		}
		raw, err := view(name+"@"+v, "engines.node", "dist.unpackedSize")
		if err != nil {
			return Resolved{}, err
		}
		rng, mb := npmEngines(raw)
		if node == "" || NodeSatisfies(rng, node) {
			return Resolved{Version: v, Packages: 1, DownloadMB: mb / 3, DiskMB: mb}, nil
		}
	}
	return Resolved{}, fmt.Errorf("no recent version of %s works with this server's Node %s", name, node)
}

// npmCandidates reads `npm view <name> versions --json` (a list, or one string): the versions without a prerelease, the newest first.
func npmCandidates(raw []byte) []string {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var one string
		if json.Unmarshal(raw, &one) != nil {
			return nil
		}
		list = []string{one}
	}
	var out []string
	for _, v := range list {
		if ValidVersion(v) && !strings.ContainsAny(v, "-+") {
			if _, ok := parseVersion(v); ok {
				out = append(out, v)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := parseVersion(out[i])
		b, _ := parseVersion(out[j])
		return a.cmp(b) > 0
	})
	return out
}

// npmEngines reads `npm view <name>@<version> engines.node dist.unpackedSize --json`: the range of Node versions (a few packages have
// an array or an object there: taken as any) and the unpacked size in MB. An empty answer (a version that says neither) is any Node.
func npmEngines(raw []byte) (string, float64) {
	var v struct {
		Engines  interface{} `json:"engines.node"`
		Unpacked float64     `json:"dist.unpackedSize"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", 0
	}
	rng, _ := v.Engines.(string)
	return rng, v.Unpacked / (1 << 20)
}

// npmViewFrom reads `npm view <spec> version dist.unpackedSize --json` for one version (or, when several match, a list: the last is
// the newest).
func npmViewFrom(raw []byte) (Resolved, error) {
	type view struct {
		Version  string  `json:"version"`
		Unpacked float64 `json:"dist.unpackedSize"`
	}
	var one view
	if err := json.Unmarshal(raw, &one); err != nil {
		var many []view
		if err2 := json.Unmarshal(raw, &many); err2 != nil || len(many) == 0 {
			return Resolved{}, errors.New("the answer of npm could not be read")
		}
		one = many[len(many)-1]
	}
	if !ValidVersion(one.Version) {
		return Resolved{}, errors.New("npm gave no usable version")
	}
	mb := one.Unpacked / (1 << 20)
	return Resolved{Version: one.Version, Packages: 1, DownloadMB: mb / 3, DiskMB: mb}, nil
}

func (n Npm) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	if !ValidName(name) || !ValidVersion(version) {
		return fmt.Errorf("%q %q is not a package", name, version)
	}
	ctx, cancel := n.with(ctx, 20*time.Minute)
	defer cancel()
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	c := n.cmd(ctx, "install", "--prefix", dest, "--ignore-scripts", "--omit=dev", "--no-audit", "--no-fund", "--no-package-lock", "--loglevel=error", name+"@"+version)
	c.Stdout, c.Stderr = log, log
	return c.Run()
}

func (n Npm) Search(ctx context.Context, q, kind string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if !searchPattern.MatchString(q) {
		return nil, nil
	}
	ctx, cancel := n.with(ctx, 60*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	c := n.cmd(ctx, "search", "--json", "--searchlimit=30", q)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("the search of npm failed: %s", tail(errb.String(), 300))
	}
	var rows []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Desc    string `json:"description"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		return nil, errors.New("the answer of the search could not be read")
	}
	var res []Found
	for _, r := range rows {
		if ValidName(r.Name) && ValidVersion(r.Version) {
			s := strings.TrimSpace(r.Desc)
			if len(s) > 200 {
				s = s[:200]
			}
			res = append(res, Found{Name: r.Name, Version: r.Version, Summary: s, Kind: KindNode})
		}
	}
	return res, nil
}

// ---- one installer for every kind ------------------------------------------------------------------------------------------

// Multi sends each kind to the installer that makes it: libraries, tools and languages to conda-forge, Node libraries to npm,
// editor plugins to the ones that come with the server.
type Multi struct {
	Conda, Node, Plugin Installer
	GoMod, Jar, Crate   Installer // the mirrors (mirror.go)
}

func (m Multi) pick(kind string) Installer {
	switch kind {
	case KindNode:
		if m.Node != nil {
			return m.Node
		}
		return Missing{Reason: "npm is not installed on this server (set OPENREPL_NPM to its path)"}
	case KindPlugin:
		if m.Plugin != nil {
			return m.Plugin
		}
		return Missing{Reason: "no editor plugins come with this server"}
	case KindGoMod:
		if m.GoMod != nil {
			return m.GoMod
		}
		return Missing{Reason: "Go is not installed on this server"}
	case KindJar:
		if m.Jar != nil {
			return m.Jar
		}
		return Missing{Reason: "Java is not installed on this server"}
	case KindCrate:
		if m.Crate != nil {
			return m.Crate
		}
		return Missing{Reason: "Rust (cargo) is not installed on this server"}
	}
	if m.Conda != nil {
		return m.Conda
	}
	return Missing{Reason: "pixi is not installed on this server (set OPENREPL_PIXI to its path)"}
}

func (m Multi) Name() string {
	parts := []string{m.pick(KindLib).Name()}
	if m.Node != nil {
		parts = append(parts, m.Node.Name())
	}
	return strings.Join(parts, " + ")
}

func (m Multi) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	return m.pick(kind).Resolve(ctx, name, version, kind)
}

func (m Multi) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	return m.pick(kind).Install(ctx, name, version, kind, dest, log)
}

// Search asks every installer that has packages of the kind (all of them when kind is empty); one that fails does not stop the others.
func (m Multi) Search(ctx context.Context, q, kind string) ([]Found, error) {
	var out []Found
	var firstErr error
	try := func(in Installer, k string) {
		r, err := in.Search(ctx, q, k)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		out = append(out, r...)
	}
	switch kind {
	case KindNode:
		try(m.pick(KindNode), KindNode)
	case KindPlugin:
		try(m.pick(KindPlugin), KindPlugin)
	case KindLib, KindTool, KindLang:
		try(m.pick(KindLib), kind)
	default:
		try(m.pick(KindLib), KindLib)
		if m.Node != nil {
			try(m.Node, KindNode)
		}
	}
	if len(out) > 0 {
		firstErr = nil
	}
	return out, firstErr
}

// ---- the fake ------------------------------------------------------------------------------------------------------------

// Fake makes small bundles of every kind, for tests and for a dev server that has no pixi: a Python module of the package's
// name, a Node module, a program. Failing names fail, as a conda install that cannot be solved does.
type Fake struct {
	Version string          // what every package resolves to, "1.0.0" when empty
	Fail    map[string]bool // names that cannot be installed
	Delay   time.Duration
}

func (f Fake) Name() string { return "fake (tests and development)" }

func (f Fake) ver() string {
	if f.Version == "" {
		return "1.0.0"
	}
	return f.Version
}

func (f Fake) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	if !ValidName(name) || (version != "" && !ValidVersion(version)) {
		return Resolved{}, fmt.Errorf("%q is not a package name", name)
	}
	if f.Fail[name] {
		return Resolved{}, fmt.Errorf("conda-forge could not solve %s: python 3.10.* is not compatible with %s (fake)", name, name)
	}
	v := f.ver()
	if version != "" {
		v = version
	}
	if kind == KindPlugin {
		p, ok := PluginByName(name)
		if !ok {
			return Resolved{}, fmt.Errorf("there is no plugin called %s", name)
		}
		return Resolved{Version: p.Version, Packages: 1}, nil
	}
	return Resolved{Version: v, Packages: 3, DownloadMB: 1.5, DiskMB: 4}, nil
}

func (f Fake) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.Fail[name] {
		fmt.Fprintf(log, "Solving environment: failed\nFound conflicts! %s is not compatible with python 3.10.*\n", name)
		return errors.New("the solver failed")
	}
	switch kind {
	case KindPlugin:
		return PluginInstaller{}.Install(ctx, name, version, kind, dest, log)
	case KindNode:
		mod := filepath.Join(dest, "node_modules", name)
		if err := os.MkdirAll(mod, 0755); err != nil {
			return err
		}
		os.WriteFile(filepath.Join(mod, "package.json"), []byte(fmt.Sprintf(`{"name":%q,"version":%q,"main":"index.js"}`, name, version)), 0644)
		if err := os.WriteFile(filepath.Join(mod, "index.js"), []byte(fmt.Sprintf("module.exports = { name: %q, version: %q };\n", name, version)), 0644); err != nil {
			return err
		}
		bin := filepath.Join(dest, "node_modules", ".bin")
		os.MkdirAll(bin, 0755)
		return os.WriteFile(filepath.Join(bin, name+"-hello"), []byte("#!/bin/sh\necho \""+name+" "+version+"\"\n"), 0755)
	case KindTool, KindLang:
		bin := filepath.Join(dest, "bin")
		if err := os.MkdirAll(bin, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho \""+name+" "+version+"\"\n"), 0755)
	}
	mod := strings.NewReplacer("-", "_", ".", "_").Replace(name)
	site := filepath.Join(dest, "lib", "python3.10", "site-packages", mod)
	if err := os.MkdirAll(site, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(site, "__init__.py"), []byte(fmt.Sprintf("__version__ = %q\nNAME = %q\n", version, name)), 0644); err != nil {
		return err
	}
	bin := filepath.Join(dest, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	script := "#!/bin/sh\necho \"" + name + " " + version + "\"\n"
	return os.WriteFile(filepath.Join(bin, name+"-hello"), []byte(script), 0755)
}

// fakeCatalog is what the fake knows when it is searched.
var fakeCatalog = []Found{
	{Name: "flask", Version: "3.0.2", Summary: "A simple framework for building complex web applications.", Kind: KindLib},
	{Name: "flask-cors", Version: "4.0.0", Summary: "Cross Origin Resource Sharing for Flask.", Kind: KindLib},
	{Name: "numpy", Version: "2.1.0", Summary: "Arrays and maths for Python.", Kind: KindLib},
	{Name: "express", Version: "4.19.2", Summary: "Fast, unopinionated web framework for Node.", Kind: KindNode},
	{Name: "lodash", Version: "4.17.21", Summary: "A utility library for Node.", Kind: KindNode},
}

func (f Fake) Search(ctx context.Context, q, kind string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if kind == KindPlugin {
		return PluginInstaller{}.Search(ctx, q, kind)
	}
	var out []Found
	for _, c := range fakeCatalog {
		if strings.HasPrefix(c.Name, q) && (kind == "" || kind == c.Kind || ((kind == KindTool || kind == KindLang) && c.Kind == KindLib)) {
			out = append(out, c)
		}
	}
	return out, nil
}

// tail is the last n bytes of s, from a line start.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return s
}

// CleanLog makes a log fit to show and to send to an admin: the last lines, with the store's folders shortened and no secrets.
func CleanLog(s, dir string) string {
	s = strings.ReplaceAll(s, dir, "/store")
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		s = strings.ReplaceAll(s, h, "~")
	}
	return tail(s, 1500)
}

// ---- no installer --------------------------------------------------------------------------------------------------------

// Missing is the installer of a server that has no micromamba: every install says why it cannot be done.
type Missing struct{ Reason string }

func (m Missing) Name() string { return "none: " + m.Reason }
func (m Missing) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	return Resolved{}, errors.New(m.Reason)
}
func (m Missing) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	return errors.New(m.Reason)
}
func (m Missing) Search(ctx context.Context, q, kind string) ([]Found, error) {
	return nil, errors.New(m.Reason)
}

// PythonOf asks a python3 program for its "major.minor" (so that every bundle is solved with the Python of the server).
func PythonOf(bin string) string {
	out, err := exec.Command(bin, "-c", "import sys;print('%d.%d' % sys.version_info[:2])").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || !regexpMinor.MatchString(v) {
		return ""
	}
	return v
}

var regexpMinor = regexp.MustCompile(`^3\.[0-9]{1,2}$`)
