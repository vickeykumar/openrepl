//go:build linux
// +build linux

package wsync

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newHome creates an empty home in a temporary base directory.
func newHome(t *testing.T) (*Home, string) {
	t.Helper()
	base, err := ioutil.TempDir("", "wsync-base")
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenHome(base, "guest-test", true)
	if err != nil {
		os.RemoveAll(base)
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(); os.RemoveAll(base) })
	return h, filepath.Join(base, "guest-test")
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// put writes a file directly on disk, bypassing the package.
func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, root, rel string, tm time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), tm, tm); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := ioutil.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func mustScan(t *testing.T, h *Home, base *Record) map[string]Entry {
	t.Helper()
	res, err := h.Scan(ScanOptions{Base: base})
	if err != nil {
		t.Fatal(err)
	}
	return res.Entries
}

// fileEntry builds the entry WriteFile expects.
func fileEntry(rel, content string, mode uint32, mtime time.Time) Entry {
	return Entry{Path: rel, Type: File, Size: int64(len(content)), ModTime: mtime.UnixNano(), Mode: mode, Hash: sum(content)}
}

// noTemps fails if a temporary receive file is left anywhere under root.
func noTemps(t *testing.T, root string) {
	t.Helper()
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && isTemp(info.Name()) {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
