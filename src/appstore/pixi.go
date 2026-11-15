package appstore

import (
	"bufio"
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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pixi installs libraries, tools and languages from conda-forge with pixi (LLD 18). Every bundle is a small pixi project of its own, in
// the bundle folder: pixi.toml (the app, pinned, and the server's Python for a library), pixi.lock (the exact packages for every CPU
// type of Platforms) and .pixi/envs/default, the conda prefix, made of hard links to the shared cache in <Dir>/pkgs. The lock is kept
// in the catalog, so every node installs the same packages and a node of another CPU type gets the ones solved for it.
type Pixi struct {
	Bin       string   // the pixi program
	Dir       string   // the store (its pkgs folder is the cache)
	Python    string   // the Python of the server, as "3.10": every library is solved with it
	Platforms []string // the CPU types the lock covers: linux-64 and linux-aarch64 when empty
	Timeout   time.Duration
}

func (p Pixi) Name() string { return "pixi (conda-forge)" }

func (p Pixi) py() string {
	if p.Python == "" {
		return "3.10"
	}
	return p.Python
}

func (p Pixi) platforms() []string {
	if len(p.Platforms) == 0 {
		return []string{"linux-64", "linux-aarch64"}
	}
	return p.Platforms
}

// CondaPlatform is the conda name of this node's CPU type, "" for one conda-forge has no builds for.
func CondaPlatform() string {
	switch runtime.GOARCH {
	case "amd64":
		return "linux-64"
	case "arm64":
		return "linux-aarch64"
	}
	return ""
}

func (p Pixi) withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	t := p.Timeout
	if t == 0 {
		t = d
	}
	return context.WithTimeout(ctx, t)
}

// env is what every pixi command gets: its home and its cache in the store, no colours, no progress bars, and no other config (the
// user's, the system's) so that the same manifest gives the same result on every node.
func (p Pixi) env() []string { return append(os.Environ(), p.extraEnv()...) }

func (p Pixi) extraEnv() []string {
	return []string{
		"PIXI_HOME=" + filepath.Join(p.Dir, "pixi"),
		"PIXI_CACHE_DIR=" + filepath.Join(p.Dir, "pkgs"),
		"RATTLER_CACHE_DIR=" + filepath.Join(p.Dir, "pkgs"),
		"PIXI_NO_PROGRESS=true", "PIXI_COLOR=never", "NO_COLOR=1"}
}

func (p Pixi) cmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, p.Bin, args...)
	c.Env, c.Dir = p.env(), dir
	return c
}

var tomlName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Manifest is the pixi.toml of one app: the app pinned to version ("" is the newest) and, for a library (kind lib, or empty), the Python
// of the server, so that what the lock holds can be imported by it. A tool or a language brings the Python it needs, if any, and does not
// drag one in.
func (p Pixi) Manifest(name, version, kind string) (string, error) {
	if !ValidName(name) || !tomlName.MatchString(name) || (version != "" && !ValidVersion(version)) {
		return "", fmt.Errorf("%q %q is not a package", name, version)
	}
	var b strings.Builder
	b.WriteString("[workspace]\nname = \"openrepl-bundle\"\nchannels = [\"conda-forge\"]\nplatforms = [")
	for i, pl := range p.platforms() {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(pl))
	}
	b.WriteString("]\n\n[dependencies]\n")
	if kind == KindLib || kind == "" {
		fmt.Fprintf(&b, "python = %q\n", p.py()+".*")
	}
	spec := "*"
	if version != "" {
		spec = "==" + version
	}
	fmt.Fprintf(&b, "%q = %q\n", name, spec)
	return b.String(), nil
}

// tempProject makes a folder with the manifest in it, for a solve that installs nothing.
func (p Pixi) tempProject(manifest string) (string, error) {
	base := filepath.Join(p.Dir, "tmp")
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", err
	}
	d, err := os.MkdirTemp(base, "solve-")
	if err != nil {
		return "", err
	}
	return d, os.WriteFile(filepath.Join(d, manifestFile), []byte(manifest), 0644)
}

// Resolve solves the app for every CPU type of the lock (nothing is downloaded or installed) and reads, for this node's CPU type, the
// version, how many packages it brings and how big they are. A CPU type conda-forge cannot solve it for fails it here, naming the CPU.
func (p Pixi) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	manifest, err := p.Manifest(name, version, kind)
	if err != nil {
		return Resolved{}, err
	}
	ctx, cancel := p.withTimeout(ctx, 10*time.Minute)
	defer cancel()
	d, err := p.tempProject(manifest)
	if err != nil {
		return Resolved{}, err
	}
	defer os.RemoveAll(d)
	var out bytes.Buffer
	c := p.cmd(ctx, d, "lock", "--manifest-path", filepath.Join(d, manifestFile))
	c.Stdout, c.Stderr = &out, &out
	if err := c.Run(); err != nil {
		return Resolved{}, fmt.Errorf("conda-forge could not solve %s: %s", name, tail(solveReason(out.String()), 600))
	}
	raw, err := os.ReadFile(filepath.Join(d, lockFile))
	if err != nil {
		return Resolved{}, errors.New("pixi made no lock")
	}
	return resolvedFrom(raw, name, CondaPlatform())
}

// solveReason is the part of a failed solve that says why (pixi's own words, without its tree drawing).
func solveReason(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "│├╰─▶× "))
		if l != "" {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, " ")
}

// resolvedFrom reads a lock: the version of the app, the packages and their sizes for one CPU type.
func resolvedFrom(raw []byte, name, platform string) (Resolved, error) {
	pk, err := LockPackages(raw, platform)
	if err != nil {
		return Resolved{}, err
	}
	out := Resolved{Packages: len(pk)}
	var total float64
	for _, x := range pk {
		total += x.Size
		if x.Name == name && ValidVersion(x.Version) {
			out.Version = x.Version
		}
	}
	if out.Version == "" {
		return Resolved{}, fmt.Errorf("conda-forge has no %s for this server's Python", name)
	}
	out.DownloadMB = total / (1 << 20)
	out.DiskMB = out.DownloadMB * 2.5 // conda packages are compressed: about this much once they are unpacked
	return out, nil
}

// LockedPackage is one package of a lock.
type LockedPackage struct {
	Name, Version, Build, URL string
	Size                      float64
}

// LockPackages reads the packages a pixi.lock holds for one CPU type (platform "" is the first one in the lock), with their sizes.
func LockPackages(raw []byte, platform string) ([]LockedPackage, error) {
	var (
		urls     []string // the packages of the environment for the platform, in order
		inEnv    bool     // inside environments:
		inPkgs   bool     // inside the top level packages: list
		curPlat  string
		want     = map[string]bool{}
		sizes    = map[string]float64{}
		curURL   string
		sawPlats []string
	)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "environments:"):
			inEnv, inPkgs = true, false
			continue
		case strings.HasPrefix(line, "packages:"):
			inEnv, inPkgs = false, true
			continue
		case line != "" && line[0] != ' ' && line[0] != '-' && line[0] != '#':
			inEnv, inPkgs = false, false
			continue
		}
		if inEnv {
			t := strings.TrimSpace(line)
			// "      linux-64:" is a platform of the packages of the environment; "      - conda: URL" a package of it
			if strings.HasSuffix(t, ":") && !strings.HasPrefix(t, "- ") && strings.HasPrefix(line, "      ") && !strings.HasPrefix(line, "       ") {
				curPlat = strings.TrimSuffix(t, ":")
				if strings.HasPrefix(curPlat, "linux-") || strings.HasPrefix(curPlat, "osx-") || strings.HasPrefix(curPlat, "win-") || curPlat == "noarch" {
					sawPlats = append(sawPlats, curPlat)
					if platform == "" {
						platform = curPlat
					}
				}
				continue
			}
			if strings.HasPrefix(t, "- conda: ") && curPlat == platform {
				u := strings.TrimSpace(strings.TrimPrefix(t, "- conda: "))
				urls = append(urls, u)
				want[u] = true
			}
			continue
		}
		if inPkgs {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(line, "- conda: ") {
				curURL = strings.TrimSpace(strings.TrimPrefix(line, "- conda: "))
			} else if strings.HasPrefix(line, "- ") {
				curURL = ""
			} else if curURL != "" && strings.HasPrefix(t, "size: ") && strings.HasPrefix(line, "  size:") {
				if n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(t, "size: ")), 64); err == nil {
					sizes[curURL] = n
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("the lock has no packages for %s (it has %s)", platform, strings.Join(sawPlats, ", "))
	}
	out := make([]LockedPackage, 0, len(urls))
	for _, u := range urls {
		file := u[strings.LastIndexByte(u, '/')+1:]
		file = strings.TrimSuffix(strings.TrimSuffix(file, ".conda"), ".tar.bz2")
		parts := strings.Split(file, "-")
		if len(parts) < 3 {
			continue
		}
		n := len(parts)
		out = append(out, LockedPackage{Name: strings.Join(parts[:n-2], "-"), Version: parts[n-2], Build: parts[n-1], URL: u, Size: sizes[u]})
	}
	return out, nil
}

// Install makes the bundle in dest: its pixi.toml (unless the node already put the one of the catalog there), then the environment. With
// a pixi.lock in the folder (the node got it from the catalog) it installs exactly that and fails if the lock does not fit the
// manifest; without one it solves, and the lock it writes is what the caller keeps. The activation variables of the packages are saved.
func (p Pixi) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	ctx, cancel := p.withTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	mf := filepath.Join(dest, manifestFile)
	if _, err := os.Stat(mf); err != nil {
		manifest, merr := p.Manifest(name, version, kind)
		if merr != nil {
			return merr
		}
		if err := os.WriteFile(mf, []byte(manifest), 0644); err != nil {
			return err
		}
	}
	args := []string{"install", "--manifest-path", mf}
	if _, err := os.Stat(filepath.Join(dest, lockFile)); err == nil {
		args = append(args, "--locked")
	}
	c := p.cmd(ctx, dest, args...)
	c.Stdout, c.Stderr = log, log
	if err := c.Run(); err != nil {
		return err
	}
	if vars, err := p.activation(ctx, dest); err != nil {
		fmt.Fprintf(log, "the activation variables could not be read: %v\n", err)
	} else {
		WriteActivation(dest, vars)
	}
	return nil
}

// activation asks pixi for the variables activating the environment sets, and keeps the ones the packages added.
func (p Pixi) activation(ctx context.Context, dest string) (map[string]string, error) {
	base := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + filepath.Join(p.Dir, "pixi")}
	c := p.cmd(ctx, dest, "shell-hook", "--json", "--manifest-path", filepath.Join(dest, manifestFile))
	c.Env = append(append([]string{}, base...), p.extraEnv()...) // a plain environment: what activation adds to it is what we keep
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, tail(errb.String(), 200))
	}
	return activationFromHook(out.Bytes(), base)
}

// activationFromHook reads `pixi shell-hook --json`: its environment_variables, against the plain environment the command ran in.
func activationFromHook(raw []byte, base []string) (map[string]string, error) {
	var res struct {
		Env map[string]string `json:"environment_variables"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, errors.New("the answer of pixi could not be read")
	}
	var after []string
	for k, v := range res.Env {
		after = append(after, k+"="+v)
	}
	sort.Strings(after)
	return ActivationFrom(base, after), nil
}

var searchHead = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]*) \(\d+ versions?\)$`)

// Search asks conda-forge for the packages whose name starts with q, for this node's CPU type: one answer for each name with its newest
// version, the exact name first, then the shortest names, at most 30. (pixi reads the index of the channel from its cache, so the first
// search is slow and the next ones are quick.)
func (p Pixi) Search(ctx context.Context, q, kind string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if !searchPattern.MatchString(q) {
		return nil, nil
	}
	ctx, cancel := p.withTimeout(ctx, 4*time.Minute)
	defer cancel()
	d, err := p.tempProject("")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d)
	os.Remove(filepath.Join(d, manifestFile)) // no manifest: pixi searches the channel, not a workspace
	args := []string{"search", q + "*", "--channel", "conda-forge", "--limit", "1"}
	if pl := CondaPlatform(); pl != "" {
		args = append(args, "--platform", pl)
	}
	var out, errb bytes.Buffer
	c := p.cmd(ctx, d, args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("the search of conda-forge failed: %s", tail(solveReason(errb.String()), 300))
	}
	return foundFromPixi(out.String(), q, kind), nil
}

// foundFromPixi reads `pixi search`: "name (N versions)" and under it the versions, newest first, one per line.
func foundFromPixi(text, q, kind string) []Found {
	newest := map[string]string{}
	cur := ""
	for _, l := range strings.Split(text, "\n") {
		if m := searchHead.FindStringSubmatch(strings.TrimSpace(l)); m != nil && !strings.HasPrefix(l, " ") {
			cur = m[1]
			continue
		}
		if cur != "" && strings.HasPrefix(l, "  ") {
			if f := strings.Fields(l); len(f) > 0 && ValidVersion(f[0]) && ValidName(cur) {
				if _, seen := newest[cur]; !seen {
					newest[cur] = f[0]
				}
			}
		}
	}
	names := make([]string, 0, len(newest))
	for n := range newest {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := names[i], names[j]
		if (a == q) != (b == q) {
			return a == q
		}
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	})
	if len(names) > 30 {
		names = names[:30]
	}
	if kind == "" {
		kind = KindLib
	}
	out := make([]Found, 0, len(names))
	for _, n := range names {
		out = append(out, Found{Name: n, Version: newest[n], Kind: kind})
	}
	return out
}
