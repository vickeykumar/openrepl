package appstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The mirrors (LLD 18): Go modules, Maven jars and Rust crates are fetched once by the server, with the checksums of their registry checked, into a
// bundle of the store, and a terminal (which has no internet) uses them offline. Each installer picks, when no version is pinned, the newest
// version that the image's own toolchain can build: the toolchain is asked (go, java, cargo), never a number written here.

// MirrorLookup finds the catalogue entry of an app of a mirror kind (the installers need the real package name: a module path, group:artifact).
type MirrorLookup func(kind, app string) (Entry, bool)

// runner runs a program and returns what it printed; the tests replace it.
type runner func(ctx context.Context, dir string, env []string, prog string, args ...string) ([]byte, error)

func realRun(ctx context.Context, dir string, env []string, prog string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, prog, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return out.Bytes(), err
}

func orRun(r runner) runner {
	if r == nil {
		return realRun
	}
	return r
}

// lookupEntry finds the entry or says what is wrong.
func lookupEntry(l MirrorLookup, kind, app string) (Entry, error) {
	if l == nil {
		return Entry{}, errors.New("the catalogue is not known to this installer")
	}
	e, ok := l(kind, app)
	if !ok {
		return Entry{}, fmt.Errorf("%s is not in the catalogue as an app of kind %s", app, kind)
	}
	return e, nil
}

// maxCandidates is how many of the newest versions an installer tries before it gives up.
const maxCandidates = 12

var stableVersion = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,3}$`)

// stable keeps the versions that are releases (no -rc, -beta, -SNAPSHOT, +incompatible, ...), the newest first as they came.
func stable(versions []string) []string {
	var out []string
	for _, v := range versions {
		if stableVersion.MatchString(v) {
			out = append(out, v)
		}
	}
	return out
}

// dirMB is the size of a folder in MB.
func dirMB(dir string) float64 { return float64(DirSize(dir)) / (1 << 20) }

// download fetches a URL into memory (at most max bytes), with a time limit.
func download(ctx context.Context, url string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached", hostOf(url))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is bigger than %d MB", url, max>>20)
	}
	return b, nil
}

func hostOf(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	return u
}

// toolPath finds a program on the PATH or in the places the image keeps it.
func toolPath(name string, extra ...string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, g := range extra {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			return m[len(m)-1]
		}
	}
	return ""
}

// atoiPrefix reads the digits at the start of s.
func atoiPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// moveDir moves a folder; where a rename is not possible (a file system that cannot rename a folder, such as an overlay), it copies and removes.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, target)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
