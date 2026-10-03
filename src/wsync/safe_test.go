//go:build linux
// +build linux

package wsync

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidRel(t *testing.T) {
	good := []string{"a", "a/b", "dir/sub/file.txt", "a b", "naïve.txt", "-rf", "..x", "x..", "a.b/c"}
	bad := []string{"", "/", "/etc/passwd", "..", "../x", "a/../b", "a/..", "./a", "a/./b", "a//b", "a/", "a\x00b",
		".wsync-123", "dir/.wsync-abc", strings.Repeat("x", 256)}
	for _, p := range good {
		if err := ValidRel(p); err != nil {
			t.Errorf("ValidRel(%q) = %v, want ok", p, err)
		}
	}
	for _, p := range bad {
		if err := ValidRel(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidRel(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
}

func TestValidHomeName(t *testing.T) {
	for _, n := range []string{"guest-1", "vick1234", "a"} {
		if err := ValidHomeName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", "/abs", "a\x00", strings.Repeat("x", 256)} {
		if err := ValidHomeName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestOpenHomeRefusesALinkAsTheHome(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "guest-x")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenHome(base, "guest-x", false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("a home that is a symbolic link was opened: %v", err)
	}
	if _, err := OpenHome(base, "../etc", false); err == nil {
		t.Fatal("a home name with .. was accepted")
	}
}

// escape sets up a home with a directory link that points outside, and a
// file there that must stay untouched.
func escape(t *testing.T) (h *Home, root, outside string) {
	t.Helper()
	h, root = newHome(t)
	outside = t.TempDir()
	put(t, outside, "victim.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	return
}

func TestNothingFollowsADirectoryLinkOutOfTheHome(t *testing.T) {
	h, _, outside := escape(t)

	// Reading.
	if _, err := h.Lstat("out/victim.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Lstat through a link: %v", err)
	}
	if _, err := h.OpenRead("out/victim.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("OpenRead through a link: %v", err)
	}
	if _, err := h.ListDir("out"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("ListDir through a link: %v", err)
	}
	if _, err := h.Readlink("out/victim.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Readlink through a link: %v", err)
	}

	// Writing and deleting.
	e := fileEntry("out/new.txt", "pwned", 0644, time.Now())
	if err := h.WriteFile(e, strings.NewReader("pwned")); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("WriteFile through a link: %v", err)
	}
	if err := h.PutDir(Entry{Path: "out/dir", Type: Dir, Mode: 0755}); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("PutDir through a link: %v", err)
	}
	if err := h.PutSymlink(Entry{Path: "out/l", Type: Symlink, Link: "x"}); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("PutSymlink through a link: %v", err)
	}
	if err := h.Remove("out/victim.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Remove through a link: %v", err)
	}
	if err := h.Rename("out/victim.txt", "stolen.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Rename out of a link: %v", err)
	}
	if err := h.Rename("a.txt", "out/in.txt"); err == nil {
		t.Errorf("Rename into a link succeeded")
	}
	if err := h.Chmod("out/victim.txt", 0777); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Chmod through a link: %v", err)
	}
	if err := h.SetTime("out/victim.txt", 1); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("SetTime through a link: %v", err)
	}

	// Nothing outside changed.
	files, _ := ioutil.ReadDir(outside)
	if len(files) != 1 || files[0].Name() != "victim.txt" {
		t.Fatalf("the outside directory was changed: %v", files)
	}
	if read(t, outside, "victim.txt") != "secret" {
		t.Fatal("the outside file was changed")
	}
	if fi, _ := os.Stat(filepath.Join(outside, "victim.txt")); fi.Mode().Perm() != 0644 {
		t.Fatalf("the outside file's mode changed to %v", fi.Mode().Perm())
	}
}

func TestAFileLinkIsNeverFollowed(t *testing.T) {
	h, root := newHome(t)
	outside := t.TempDir()
	put(t, outside, "passwd", "root:x:0:0")
	os.Symlink(filepath.Join(outside, "passwd"), filepath.Join(root, "link"))

	// The link is a link: it cannot be read as a file.
	if _, err := h.OpenRead("link"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("OpenRead of a file link: %v", err)
	}
	e, err := h.Lstat("link")
	if err != nil || e.Type != Symlink {
		t.Fatalf("Lstat = %+v, %v; want a symlink entry", e, err)
	}
	target, err := h.Readlink("link")
	if err != nil || target != filepath.Join(outside, "passwd") {
		t.Fatalf("Readlink = %q, %v", target, err)
	}
	// Chmod must not reach the file the link points to.
	if err := h.Chmod("link", 0777); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Chmod of a file link: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(outside, "passwd")); fi.Mode().Perm() == 0777 {
		t.Fatal("Chmod followed the link")
	}
	// Writing a file over the link replaces the link, not its target.
	if err := h.WriteFile(fileEntry("link", "mine", 0600, time.Now()), strings.NewReader("mine")); err != nil {
		t.Fatal(err)
	}
	if read(t, outside, "passwd") != "root:x:0:0" {
		t.Fatal("WriteFile wrote through the link")
	}
	if read(t, root, "link") != "mine" {
		t.Fatal("the link was not replaced by the file")
	}
}

func TestAnAbsoluteLinkIsStoredAsTextAndNeverResolved(t *testing.T) {
	h, root := newHome(t)
	if err := h.PutSymlink(Entry{Path: "etc", Type: Symlink, Link: "/etc", Mode: 0777}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(root, "etc")); got != "/etc" {
		t.Fatalf("link target = %q", got)
	}
	// Anything under it is out of reach.
	if _, err := h.OpenRead("etc/passwd"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("a path through the stored link was followed: %v", err)
	}
}

func TestPathsWithDotDotAreRefusedByEveryOperation(t *testing.T) {
	h, _ := newHome(t)
	e := fileEntry("../x", "x", 0644, time.Now())
	checks := map[string]error{
		"Lstat":      func() error { _, err := h.Lstat("../x"); return err }(),
		"OpenRead":   func() error { _, err := h.OpenRead("../x"); return err }(),
		"WriteFile":  h.WriteFile(e, strings.NewReader("x")),
		"PutDir":     h.PutDir(Entry{Path: "a/../../x", Type: Dir}),
		"PutSymlink": h.PutSymlink(Entry{Path: "/abs", Type: Symlink, Link: "x"}),
		"Remove":     h.Remove("../x"),
		"Rename":     h.Rename("a", "../b"),
		"Chmod":      h.Chmod("/etc/passwd", 0777),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: %v, want ErrUnsafePath", name, err)
		}
	}
}

func TestAReplacedDirectoryIsNotWalked(t *testing.T) {
	// A program in the home swaps a directory for a link between two calls.
	h, root := newHome(t)
	outside := t.TempDir()
	put(t, outside, "f.txt", "outside")
	put(t, root, "d/f.txt", "inside")
	if got, err := h.OpenRead("d/f.txt"); err != nil {
		t.Fatal(err)
	} else {
		got.Close()
	}
	os.RemoveAll(filepath.Join(root, "d"))
	os.Symlink(outside, filepath.Join(root, "d"))
	if _, err := h.OpenRead("d/f.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("a swapped-in link was followed: %v", err)
	}
}
