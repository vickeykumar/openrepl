package appstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AptBundle installs a program of the Ubuntu archive into a bundle of the store, with no root and no change to the system (LLD 18, "minimal
// installs"). apt is used to work out the packages and to download them (with its own state and cache folders under <store>/apt, and the
// signatures of the archive checked); dpkg-deb unpacks them into <bundle>; no maintainer script is ever run. Only the packages that the image
// does not have are fetched: what the image has is used from the system when the program runs.
//
// The two things a maintainer script would have done are done from its text: the links of `update-alternatives --install` are made inside the
// bundle, and the entry's `env` (for example VIMRUNTIME) is saved for the environment of the terminal.
type AptBundle struct {
	Dir    string // the store
	Lookup MirrorLookup
	Run    runner
}

func (a AptBundle) Name() string { return "apt bundle (Ubuntu archive, unpacked)" }

const aptLockFile, aptManifestFile = "apt.lock", "apt.json"

// aptState are the options that give apt a state of its own: the system's package database is only read.
func (a AptBundle) opts() []string {
	return []string{"-o", "Dir::State=" + filepath.Join(a.Dir, "apt", "state"), "-o", "Dir::State::status=/var/lib/dpkg/status",
		"-o", "Dir::Cache=" + filepath.Join(a.Dir, "apt", "cache"), "-o", "Debug::NoLocking=1", "-o", "Acquire::Languages=none",
		"-o", "APT::Install-Recommends=false", "-o", "APT::Install-Suggests=false"}
}

func (a AptBundle) apt(ctx context.Context, dir, prog string, args ...string) ([]byte, error) {
	env := []string{"DEBIAN_FRONTEND=noninteractive", "LC_ALL=C"}
	return orRun(a.Run)(ctx, dir, env, prog, append(a.opts(), args...)...)
}

// lists makes sure the package lists are there and not older than a day (the first time they are fetched: about 60 MB for each CPU type).
func (a AptBundle) lists(ctx context.Context) error {
	stamp := filepath.Join(a.Dir, "apt", "updated")
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(a.Dir, "apt", "state", "lists", "partial"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(a.Dir, "apt", "cache", "archives", "partial"), 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := a.apt(ctx, "", "apt-get", "update"); err != nil {
		return fmt.Errorf("the package lists could not be fetched: %s", tail(strings.TrimSpace(string(out)), 300))
	}
	return os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)
}

var aptInst = regexp.MustCompile(`^Inst (\S+)(?: \[([^\]]+)\])? \(([^ )]+)`)

// parsePlan reads the lines of `apt-get -s install`: the packages that would be installed, and which of them replace a package that the image
// has (an upgrade).
func parsePlan(out string) (pkgs []AptPkg, upgrades []string) {
	for _, l := range strings.Split(out, "\n") {
		if m := aptInst.FindStringSubmatch(l); m != nil {
			pkgs = append(pkgs, AptPkg{Name: m[1], Version: m[3]})
			if m[2] != "" {
				upgrades = append(upgrades, m[1])
			}
		}
	}
	return pkgs, upgrades
}

// parseShow reads one stanza of `apt-cache show`.
func parseShow(out string) (section, sha string, sizeKB int) {
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "Section: "):
			section = strings.TrimSpace(l[9:])
		case strings.HasPrefix(l, "SHA256: "):
			sha = strings.TrimSpace(l[8:])
		case strings.HasPrefix(l, "Installed-Size: "):
			sizeKB, _ = strconv.Atoi(strings.TrimSpace(l[16:]))
		}
	}
	return
}

// plan works out the packages of an install of pkg (at version when it is given): exactly the new ones, with their section, checksum and size.
func (a AptBundle) plan(ctx context.Context, pkg, version string) ([]AptPkg, error) {
	if !ValidApt(pkg) {
		return nil, fmt.Errorf("%q is not a package name", pkg)
	}
	if err := a.lists(ctx); err != nil {
		return nil, err
	}
	spec := pkg
	if version != "" {
		spec = pkg + "=" + version
	}
	out, err := a.apt(ctx, "", "apt-get", "install", "-s", "--no-install-recommends", spec)
	if err != nil {
		return nil, fmt.Errorf("apt cannot install %s: %s", spec, tail(strings.TrimSpace(string(out)), 300))
	}
	pkgs, up := parsePlan(string(out))
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("%s is part of the image already", pkg)
	}
	if len(up) > 0 {
		return nil, fmt.Errorf("%s needs a newer version of %s than the image has, and the store does not replace what the image has", pkg, strings.Join(up, ", "))
	}
	for i := range pkgs {
		show, err := a.apt(ctx, "", "apt-cache", "show", "--no-all-versions", pkgs[i].Name+"="+pkgs[i].Version)
		if err != nil {
			return nil, fmt.Errorf("apt-cache show %s: %s", pkgs[i].Name, tail(strings.TrimSpace(string(show)), 200))
		}
		pkgs[i].Section, pkgs[i].SHA256, pkgs[i].SizeKB = parseShow(string(show))
		pkgs[i].Arch = aptArch()
	}
	return pkgs, nil
}

// bundleVersion turns a Debian version into one that a bundle can carry (no epoch, at most 32 characters, letters, digits and . _ + ! -).
func bundleVersion(v string) string {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		v = v[i+1:]
	}
	v = strings.NewReplacer("~", "-").Replace(v)
	if len(v) > 31 {
		if i := strings.IndexByte(v, '-'); i > 0 {
			v = v[:i]
		}
	}
	if len(v) > 31 {
		v = v[:31]
	}
	return v
}

func (a AptBundle) option(app string) (Entry, Option, error) {
	e, err := lookupEntry(a.Lookup, KindTool, app)
	if err != nil {
		e, err = lookupEntry(a.Lookup, KindLib, app)
	}
	if err != nil {
		return Entry{}, Option{}, err
	}
	o, ok := e.OptionOf(MgrApt)
	if !ok {
		o = Option{Manager: MgrApt}
	}
	if o.Package == "" {
		o.Package = e.Source()
	}
	return e, o, nil
}

func (a AptBundle) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	e, o, err := a.option(name)
	if err != nil {
		return Resolved{}, err
	}
	pkgs, err := a.plan(ctx, o.Package, "")
	if err != nil {
		return Resolved{}, err
	}
	var kb int
	top := pkgs[len(pkgs)-1].Version
	for _, p := range pkgs {
		kb += p.SizeKB
		if p.Name == o.Package {
			top = p.Version
		}
	}
	if o.MinVersion != "" && VersionLess(top, o.MinVersion) {
		return Resolved{}, fmt.Errorf("the version in the Ubuntu archive of this server (%s) is older than %s, which %s needs", top, o.MinVersion, e.Name)
	}
	return Resolved{Version: bundleVersion(top), Packages: len(pkgs), DiskMB: float64(kb) / 1024, DownloadMB: float64(kb) / 1024 / 3, GUI: GUIOfApt(pkgs)}, nil
}

// VersionLess says whether Debian-style version a is older than b, by the upstream numbers (dots and digits are compared as numbers).
func VersionLess(a, b string) bool {
	num := func(v string) []int {
		if i := strings.IndexByte(v, ':'); i >= 0 {
			v = v[i+1:]
		}
		if i := strings.IndexAny(v, "-+~"); i >= 0 {
			v = v[:i]
		}
		var out []int
		for _, f := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(strings.TrimLeft(f, "v"))
			out = append(out, n)
		}
		return out
	}
	x, y := num(a), num(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			return p < q
		}
	}
	return false
}

var altInstall = regexp.MustCompile(`update-alternatives\s+--install\s+(/\S+)\s+\S+\s+(/\S+)`)

var (
	shAssign = regexp.MustCompile(`(?m)^\s*(\w+)=([^\s$"']+)\s*$`)
	shFor    = regexp.MustCompile(`(?s)for\s+(\w+)\s+in\s+([^;\n]+?)\s*;?\s*do\b(.*?)\bdone\b`)
)

// expandShell does the two things a postinst needs before its update-alternatives lines can be read: a simple `for i in a b ; do ... done` is
// written out once for each word, and the variables that are set to a plain word (variant=basic) are put in. Nothing is run.
func expandShell(text string) string {
	text = shFor.ReplaceAllStringFunc(text, func(m string) string {
		g := shFor.FindStringSubmatch(m)
		var b strings.Builder
		for _, w := range strings.Fields(g[2]) {
			b.WriteString(strings.NewReplacer("${"+g[1]+"}", w, "$"+g[1], w).Replace(g[3]))
			b.WriteString("\n")
		}
		return b.String()
	})
	vars := map[string]string{}
	for _, m := range shAssign.FindAllStringSubmatch(text, -1) {
		vars[m[1]] = m[2]
	}
	for k, v := range vars {
		text = strings.NewReplacer("${"+k+"}", v, "$"+k, v).Replace(text)
	}
	return text
}

// alternatives reads, from the text of a postinst, the links that update-alternatives would make: [link, target].
func alternatives(postinst string) [][2]string {
	var out [][2]string
	for _, m := range altInstall.FindAllStringSubmatch(expandShell(postinst), -1) {
		if strings.Contains(m[1]+m[2], "$") {
			continue // a name the text does not settle: no link rather than a wrong one
		}
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

func (a AptBundle) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	_, o, err := a.option(name)
	if err != nil {
		return err
	}
	// the lock of the catalog (a node that installs what the gateway chose) or a fresh plan
	var pkgs []AptPkg
	fromLock := false
	if raw, err := os.ReadFile(filepath.Join(dest, aptLockFile)); err == nil {
		fromLock = true
		if json.Unmarshal(raw, &pkgs) != nil || len(pkgs) == 0 {
			return fmt.Errorf("the lock of %s cannot be read", name)
		}
		if err := a.lists(ctx); err != nil {
			return err
		}
	} else if pkgs, err = a.plan(ctx, o.Package, ""); err != nil {
		return err
	}
	src := "a plan made now"
	if fromLock {
		src = "the lock of the gateway"
	}
	var kb int
	for _, p := range pkgs {
		kb += p.SizeKB
	}
	logStep(log, "look up", "info", "apt: %d packages from %s, %.1f MB installed", len(pkgs), src, float64(kb)/1024)
	return a.installPlan(ctx, o, pkgs, dest, log, fromLock)
}

// installPlan downloads and unpacks the packages of a plan (or of a lock) into the bundle.
func (a AptBundle) installPlan(ctx context.Context, o Option, pkgs []AptPkg, dest string, log io.Writer, fromLock bool) error {
	tmp, err := os.MkdirTemp(a.Dir, "apt-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	var links [][2]string
	for _, p := range pkgs {
		dt := time.Now()
		out, err := a.apt(ctx, tmp, "apt-get", "download", p.Name+"="+p.Version)
		logCommand(log, "apt-get", []string{"download", p.Name + "=" + p.Version}, err, time.Since(dt))
		if err != nil {
			if fromLock && strings.Contains(string(out), "was not found") {
				// this CPU type's archive does not have the version the gateway locked (the archives of the CPU types are not always in step): this node makes its
				// own plan and installs the best version it has
				fmt.Fprintf(log, "%s %s is not in this archive: planning for this node\n", p.Name, p.Version)
				os.RemoveAll(dest)
				os.MkdirAll(dest, 0755)
				plan, perr := a.plan(ctx, o.Package, "")
				if perr != nil {
					return perr
				}
				return a.installPlan(ctx, o, plan, dest, log, false)
			}
			return fmt.Errorf("%s %s could not be downloaded (the archive may have a newer version: make a new lock): %s", p.Name, p.Version, tail(strings.TrimSpace(string(out)), 200))
		}
		deb, _ := filepath.Glob(filepath.Join(tmp, p.Name+"_*.deb"))
		if len(deb) != 1 {
			return fmt.Errorf("the file of %s was not downloaded", p.Name)
		}
		if p.SHA256 != "" && (p.Arch == "" || p.Arch == aptArch()) {
			if sum, err := fileSHA256(deb[0]); err != nil || sum != p.SHA256 {
				return fmt.Errorf("the checksum of %s does not match the archive's", p.Name)
			}
		}
		if out, err := orRun(a.Run)(ctx, tmp, nil, "dpkg-deb", "-x", deb[0], dest); err != nil {
			return fmt.Errorf("%s could not be unpacked: %s", p.Name, tail(strings.TrimSpace(string(out)), 200))
		}
		ctl := filepath.Join(tmp, "ctl-"+p.Name)
		if _, err := orRun(a.Run)(ctx, tmp, nil, "dpkg-deb", "-e", deb[0], ctl); err == nil {
			if post, err := os.ReadFile(filepath.Join(ctl, "postinst")); err == nil && len(post) < 1<<20 {
				links = append(links, alternatives(string(post))...) // read as text; the script is never run
			}
		}
		os.Remove(deb[0])
	}
	for _, l := range links {
		makeLink(dest, l[0], l[1])
	}
	// the names the entry adds (Ubuntu's batcat is bat for the user)
	for name, target := range o.Links {
		makeLink(dest, "/"+name, "/"+target)
	}
	// what the bundle needs to run: the entry's variables, with the place of the bundle in them
	if len(o.Env) > 0 {
		env := map[string]string{}
		for k, v := range o.Env {
			if ValidExtra(k, strings.ReplaceAll(v, "{bundle}", dest)) {
				env[k] = strings.ReplaceAll(v, "{bundle}", dest)
			}
		}
		raw, _ := json.Marshal(struct {
			Env map[string]string `json:"env"`
		}{env})
		os.WriteFile(filepath.Join(dest, activateFile), raw, 0644)
	}
	lockJSON, _ := json.Marshal(pkgs)
	manifest, _ := json.Marshal(map[string]string{"package": o.Package})
	var top string
	for _, p := range pkgs {
		if p.Name == o.Package {
			top = p.Version
		}
	}
	os.WriteFile(filepath.Join(dest, aptLockFile), lockJSON, 0644)
	os.WriteFile(filepath.Join(dest, aptManifestFile), manifest, 0644)
	os.WriteFile(filepath.Join(dest, "bundle.json"), []byte(fmt.Sprintf("{\"manager\":\"apt\",\"package\":%q,\"version\":%q}\n", o.Package, top)), 0644)
	var kb int
	for _, p := range pkgs {
		kb += p.SizeKB
	}
	fmt.Fprintf(log, "%s %s: %d packages from the Ubuntu archive, %d MB\n", o.Package, top, len(pkgs), kb/1024)
	return nil
}

func (a AptBundle) Search(ctx context.Context, q, kind string) ([]Found, error) {
	return searchApt(ctx, a.opts(), q, nil)
}

// makeLink makes the link of an update-alternatives (an absolute path of the system) inside the bundle, pointing at its target in the bundle.
func makeLink(bundle, link, target string) {
	lp := filepath.Join(bundle, filepath.FromSlash(link))
	tp := filepath.Join(bundle, filepath.FromSlash(target))
	if _, err := os.Lstat(lp); err == nil {
		return
	}
	if _, err := os.Stat(tp); err != nil {
		return
	}
	rel, err := filepath.Rel(filepath.Dir(lp), tp)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(lp), 0755)
	os.Symlink(rel, lp)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AptDirs are the folders of an unpacked bundle that a terminal needs on its paths: bin folders, library folders, headers and pkg-config files.
func AptDirs(bundle string) (bins, libs, includes, pkgcfg []string) {
	for _, d := range []string{"usr/bin", "usr/sbin", "usr/games", "bin", "sbin"} {
		if isDir(filepath.Join(bundle, d)) {
			bins = append(bins, filepath.Join(bundle, d))
		}
	}
	// a package of Ubuntu 22.04 may still ship /lib and /bin (the image has them as links into /usr): they are libraries and programs too
	trip, _ := filepath.Glob(filepath.Join(bundle, "usr", "lib", "*-linux-gnu"))
	trip2, _ := filepath.Glob(filepath.Join(bundle, "lib", "*-linux-gnu"))
	sort.Strings(trip)
	sort.Strings(trip2)
	trip = append(trip, trip2...)
	for _, t := range trip {
		libs = append(libs, t)
		if isDir(filepath.Join(t, "pkgconfig")) {
			pkgcfg = append(pkgcfg, filepath.Join(t, "pkgconfig"))
		}
	}
	for _, d := range []string{"usr/lib", "lib"} {
		if isDir(filepath.Join(bundle, d)) {
			libs = append(libs, filepath.Join(bundle, d))
		}
	}
	if isDir(filepath.Join(bundle, "usr", "include")) {
		includes = append(includes, filepath.Join(bundle, "usr", "include"))
	}
	if isDir(filepath.Join(bundle, "usr", "share", "pkgconfig")) {
		pkgcfg = append(pkgcfg, filepath.Join(bundle, "usr", "share", "pkgconfig"))
	}
	return
}

// IsAptBundle says whether a bundle folder was made by AptBundle.
func IsAptBundle(bundle string) bool {
	_, err := os.Stat(filepath.Join(bundle, aptLockFile))
	return err == nil
}

// aptArch is the Debian name of this machine's CPU type.
func aptArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return runtime.GOARCH
}
