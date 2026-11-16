package appstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoMod mirrors Go modules: `go` (the image's) downloads the module and everything it needs from the Go module proxy, which checks every module
// against the Go checksum database, into <bundle>/gomod/cache/download, the layout of a module proxy. A terminal uses it as GOPROXY=file://...
// (see Vars), with no internet. The newest version that builds with the image's Go is the one that is kept.
type GoMod struct {
	Dir    string // the store
	Lookup MirrorLookup
	Proxy  string // the module proxy ("" is proxy.golang.org)
	Run    runner
}

// goVer is a version as Go writes it (v1.2.3); the store keeps it without the v (a bundle's version starts with a digit).
func goVer(v string) string {
	if v != "" && !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func (g GoMod) Name() string { return "go module mirror (proxy.golang.org)" }

func (g GoMod) proxy() string {
	if g.Proxy != "" {
		return g.Proxy
	}
	return "https://proxy.golang.org"
}

func (g GoMod) env(cache string) []string {
	return []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GO111MODULE=on", "GOPROXY=" + g.proxy(), "GOMODCACHE=" + cache, "GOCACHE=" + filepath.Join(cache, "..", "build"),
		"GOSUMDB=sum.golang.org", "GOPATH=" + filepath.Join(cache, "..", "path"), "CGO_ENABLED=0"}
}

func (g GoMod) goBin() (string, error) {
	if p := toolPath("go", "/usr/local/go/bin/go"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("go is not installed on this server")
}

// try makes a module project in tmp that requires mod@version and downloads everything of its build list into cache; it fails when the module
// needs a newer Go than this one, or a checksum does not match.
func (g GoMod) try(ctx context.Context, tmp, cache, mod, version string) error {
	goBin, err := g.goBin()
	if err != nil {
		return err
	}
	run := orRun(g.Run)
	env := g.env(cache)
	if err := os.MkdirAll(tmp, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module openrepl.local/mirror\n\ngo 1.18\n"), 0644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := run(ctx, tmp, env, goBin, "get", mod+"@"+version); err != nil {
		return fmt.Errorf("go get %s@%s: %s", mod, version, tail(strings.TrimSpace(string(out)), 300))
	}
	if out, err := run(ctx, tmp, env, goBin, "mod", "download", "all"); err != nil {
		return fmt.Errorf("go mod download: %s", tail(strings.TrimSpace(string(out)), 300))
	}
	return nil
}

// candidates are the versions of the module, the newest first.
func (g GoMod) candidates(ctx context.Context, tmp, mod string) ([]string, error) {
	goBin, err := g.goBin()
	if err != nil {
		return nil, err
	}
	os.MkdirAll(tmp, 0755)
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module openrepl.local/mirror\n\ngo 1.18\n"), 0644)
	out, err := orRun(g.Run)(ctx, tmp, g.env(filepath.Join(tmp, "mod")), goBin, "list", "-m", "-versions", mod)
	if err != nil {
		return nil, fmt.Errorf("the module proxy does not know %s: %s", mod, tail(strings.TrimSpace(string(out)), 300))
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return nil, fmt.Errorf("the module proxy lists no version of %s", mod)
	}
	vs := stable(f[1:])
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
	}
	if len(vs) == 0 {
		return nil, fmt.Errorf("%s has no release version", mod)
	}
	return vs, nil
}

// pick finds the newest version that downloads and builds with this Go (or the pinned one), leaving what it fetched in cache.
func (g GoMod) pick(ctx context.Context, tmp, cache, mod, pin string) (string, error) {
	vs := []string{pin}
	if pin == "" {
		var err error
		if vs, err = g.candidates(ctx, tmp, mod); err != nil {
			return "", err
		}
	}
	var last error
	for i, v := range vs {
		if i >= maxCandidates {
			break
		}
		os.RemoveAll(cache)
		if err := g.try(ctx, tmp, cache, mod, v); err != nil {
			last = err
			continue
		}
		return v, nil
	}
	return "", fmt.Errorf("no version of %s works with this server's Go: %v", mod, last)
}

func (g GoMod) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	e, err := lookupEntry(g.Lookup, KindGoMod, name)
	if err != nil {
		return Resolved{}, err
	}
	tmp, err := os.MkdirTemp(g.Dir, "gomod-resolve-")
	if err != nil {
		return Resolved{}, err
	}
	defer os.RemoveAll(tmp)
	cache := filepath.Join(tmp, "mod")
	if version == "" {
		version = e.Version
	}
	v, err := g.pick(ctx, tmp, cache, e.Source(), goVer(version))
	if err != nil {
		return Resolved{}, err
	}
	mb := dirMB(filepath.Join(cache, "cache", "download"))
	return Resolved{Version: strings.TrimPrefix(v, "v"), Packages: countModules(filepath.Join(cache, "cache", "download")), DownloadMB: mb, DiskMB: mb}, nil
}

// countModules counts the modules (the .zip files) of a proxy layout.
func countModules(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".zip") {
			n++
		}
		return nil
	})
	return n
}

func (g GoMod) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	e, err := lookupEntry(g.Lookup, KindGoMod, name)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(g.Dir, "gomod-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	cache := filepath.Join(tmp, "mod")
	if version == "" {
		version = e.Version
	}
	v, err := g.pick(ctx, tmp, cache, e.Source(), goVer(version))
	if err != nil {
		return err
	}
	// only the proxy layout is kept: the extracted copies of the modules would double the size
	if err := os.MkdirAll(filepath.Join(dest, "gomod"), 0755); err != nil {
		return err
	}
	if err := moveDir(filepath.Join(cache, "cache"), filepath.Join(dest, "gomod", "cache")); err != nil {
		return err
	}
	fmt.Fprintf(log, "%s %s: %d modules, %.1f MB\n", e.Source(), v, countModules(filepath.Join(dest, "gomod", "cache", "download")), dirMB(dest))
	return os.WriteFile(filepath.Join(dest, "mirror.json"), []byte(fmt.Sprintf("{\"source\":%q,\"version\":%q}\n", e.Source(), strings.TrimPrefix(v, "v"))), 0644)
}

func (g GoMod) Search(ctx context.Context, q, kind string) ([]Found, error) { return nil, nil }

// ProxyOf is the GOPROXY entry of a Go module bundle.
func ProxyOf(bundle string) string {
	return "file://" + filepath.Join(bundle, "gomod", "cache", "download")
}

// checkGoMod proves that a program that imports the module builds with only the mirror: the same way a terminal uses it.
func (g GoMod) check(ctx context.Context, dest string, e Entry, version string) error {
	goBin, err := g.goBin()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(g.Dir, "gomod-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=mod", "GO111MODULE=on", "GOPROXY=" + ProxyOf(dest) + ",off", "GOSUMDB=off", "GONOSUMDB=*",
		"GOMODCACHE=" + filepath.Join(tmp, "mod"), "GOCACHE=" + filepath.Join(tmp, "build"), "GOPATH=" + filepath.Join(tmp, "path"), "CGO_ENABLED=0"}
	imp := e.CheckArg
	if imp == "" {
		imp = e.Source()
	}
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module openrepl.local/check\n\ngo 1.18\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n\nimport _ "+fmt.Sprintf("%q", imp)+"\n\nfunc main() {}\n"), 0644)
	run := orRun(g.Run)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if out, err := run(ctx, tmp, env, goBin, "get", e.Source()+"@"+goVer(version)); err != nil {
		return fmt.Errorf("go get from the mirror: %s", tail(strings.TrimSpace(string(out)), 300))
	}
	if out, err := run(ctx, tmp, env, goBin, "build", "-o", filepath.Join(tmp, "t"), "."); err != nil {
		return fmt.Errorf("a program that imports %s does not build: %s", imp, tail(strings.TrimSpace(string(out)), 400))
	}
	return nil
}
