//go:build linux
// +build linux

package wsync

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 123_456_789)

func TestWriteFileStoresContentModeAndTime(t *testing.T) {
	h, root := newHome(t)
	e := fileEntry("src/main.c", "int main(){}", 0755, t0)
	if err := h.WriteFile(e, strings.NewReader("int main(){}")); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "src/main.c") != "int main(){}" {
		t.Fatal("content differs")
	}
	fi, _ := os.Stat(filepath.Join(root, "src", "main.c"))
	if fi.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	if fi.ModTime().UnixNano() != t0.UnixNano() {
		t.Fatalf("mtime = %v, want %v (sender's time must be kept)", fi.ModTime().UnixNano(), t0.UnixNano())
	}
	noTemps(t, root)
}

func TestWriteFileNeverKeepsPrivilegeBits(t *testing.T) {
	h, root := newHome(t)
	e := fileEntry("tool", "x", 0, t0)
	e.Mode = 04755 // set-user-id, as a hostile peer might send
	if err := h.WriteFile(e, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(root, "tool"))
	if fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || fi.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v; privilege bits must be stripped", fi.Mode())
	}
}

func TestWriteFileReplacesAnExistingFileAtomically(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "a.txt", "old")
	if err := h.WriteFile(fileEntry("a.txt", "new content", 0644, t0), strings.NewReader("new content")); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "a.txt") != "new content" {
		t.Fatal("not replaced")
	}
	noTemps(t, root)
}

func TestWriteFileFailureLeavesNothingBehind(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "keep.txt", "original")

	cases := []struct {
		name string
		e    Entry
		body io.Reader
		want error
	}{
		{"wrong hash", fileEntry("keep.txt", "hello", 0644, t0), strings.NewReader("HELLO"), ErrHashMismatch},
		{"short content", fileEntry("keep.txt", "hello", 0644, t0), strings.NewReader("hel"), ErrShortRead},
		{"too large", Entry{Path: "keep.txt", Type: File, Size: DefaultMaxFileSize + 1}, strings.NewReader(""), ErrTooLarge},
		{"not a file", Entry{Path: "keep.txt", Type: Dir}, strings.NewReader(""), ErrNotRegular},
	}
	for _, c := range cases {
		if err := h.WriteFile(c.e, c.body); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if err := h.WriteFile(fileEntry("keep.txt", "abc", 0644, t0), failingReader{}); err == nil {
		t.Error("a read error was not reported")
	}
	if read(t, root, "keep.txt") != "original" {
		t.Fatal("a failed transfer replaced the existing file")
	}
	noTemps(t, root)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestWriteFileOnlyReadsTheDeclaredSize(t *testing.T) {
	h, root := newHome(t)
	// A peer that sends more than it declared cannot make us write more.
	e := fileEntry("a", "abc", 0644, t0)
	e.Hash = sum("abc")
	if err := h.WriteFile(e, strings.NewReader("abcEXTRA")); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "a") != "abc" {
		t.Fatalf("read past the declared size: %q", read(t, root, "a"))
	}
}

func TestWriteFileHonoursTheHomesSizeLimit(t *testing.T) {
	h, _ := newHome(t)
	h.MaxFileSize = 4
	if err := h.WriteFile(fileEntry("a", "12345", 0644, t0), strings.NewReader("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	if err := h.WriteFile(fileEntry("a", "1234", 0644, t0), strings.NewReader("1234")); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFileEmptyFile(t *testing.T) {
	h, root := newHome(t)
	if err := h.WriteFile(fileEntry("empty", "", 0644, t0), strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "empty")); err != nil || fi.Size() != 0 {
		t.Fatalf("empty file: %v, %v", fi, err)
	}
}

func TestWriteFileOverDirectories(t *testing.T) {
	h, root := newHome(t)
	os.Mkdir(filepath.Join(root, "empty"), 0755)
	if err := h.WriteFile(fileEntry("empty", "now a file", 0644, t0), strings.NewReader("now a file")); err != nil {
		t.Fatalf("an empty directory in the way must be replaced: %v", err)
	}
	if read(t, root, "empty") != "now a file" {
		t.Fatal("not replaced")
	}

	put(t, root, "full/inner.txt", "keep me")
	if err := h.WriteFile(fileEntry("full", "x", 0644, t0), strings.NewReader("x")); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("a directory with content must not be replaced by a file: %v", err)
	}
	if read(t, root, "full/inner.txt") != "keep me" {
		t.Fatal("the directory's content was lost")
	}
	noTemps(t, root)
}

func TestWriteFileLargeUsesBoundedMemory(t *testing.T) {
	h, root := newHome(t)
	const size = 40 << 20
	h.MaxFileSize = size
	// A reader that produces the data on the fly: the test itself never
	// holds the file in memory either.
	gen := func() io.Reader { return io.LimitReader(patternReader{}, size) }
	hash, err := hashReader(gen())
	if err != nil {
		t.Fatal(err)
	}
	e := Entry{Path: "big.bin", Type: File, Size: size, Mode: 0644, ModTime: t0.UnixNano(), Hash: hash}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := h.WriteFile(e, gen()); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("storing a %d MB file allocated %d MB; it must be streamed", size>>20, allocated>>20)
	}
	if fi, _ := os.Stat(filepath.Join(root, "big.bin")); fi.Size() != size {
		t.Fatalf("size = %d", fi.Size())
	}
	// And the scan hashes it to the same value, also without holding it.
	runtime.ReadMemStats(&before)
	scanned := mustScan(t, h, nil)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("scanning a %d MB file allocated %d MB", size>>20, allocated>>20)
	}
	if scanned["big.bin"].Hash != hash {
		t.Fatalf("scan hash %s != %s", scanned["big.bin"].Hash, hash)
	}
}

type patternReader struct{ n int }

func (p patternReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return len(b), nil
}

func TestPutDir(t *testing.T) {
	h, root := newHome(t)
	if err := h.PutDir(Entry{Path: "a/b/c", Type: Dir, Mode: 0750}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "a", "b", "c"))
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0750 {
		t.Fatalf("dir: %v %v", fi, err)
	}
	// Again, with another mode: it is updated, not an error.
	if err := h.PutDir(Entry{Path: "a/b/c", Type: Dir, Mode: 0700}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "a", "b", "c")); fi.Mode().Perm() != 0700 {
		t.Fatalf("mode not updated: %v", fi.Mode().Perm())
	}
	// A mode without owner access still leaves the owner able to write.
	if err := h.PutDir(Entry{Path: "ro", Type: Dir, Mode: 0444}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "ro")); fi.Mode().Perm()&0700 != 0700 {
		t.Fatalf("a directory lost owner access: %v", fi.Mode().Perm())
	}
	// A file or link in the way is replaced by the directory.
	put(t, root, "was-file", "x")
	if err := h.PutDir(Entry{Path: "was-file", Type: Dir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(root, "was-file")); !fi.IsDir() {
		t.Fatal("the file was not replaced by a directory")
	}
	os.Symlink("/etc", filepath.Join(root, "was-link"))
	if err := h.PutDir(Entry{Path: "was-link", Type: Dir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(root, "was-link")); !fi.IsDir() {
		t.Fatal("the link was not replaced by a directory")
	}
}

func TestPutSymlinkReplacesAtomically(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "target.txt", "t")
	if err := h.PutSymlink(Entry{Path: "ln", Type: Symlink, Link: "target.txt", ModTime: t0.UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(root, "ln")); got != "target.txt" {
		t.Fatalf("target = %q", got)
	}
	if err := h.PutSymlink(Entry{Path: "ln", Type: Symlink, Link: "other", ModTime: t0.UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(root, "ln")); got != "other" {
		t.Fatalf("target = %q after replacing", got)
	}
	for _, bad := range []Entry{
		{Path: "x", Type: Symlink, Link: ""},
		{Path: "x", Type: Symlink, Link: strings.Repeat("a", maxLinkTarget+1)},
		{Path: "x", Type: Symlink, Link: "a\x00b"},
		{Path: "x", Type: File, Link: "a"},
	} {
		if err := h.PutSymlink(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	noTemps(t, root)
}

func TestRemove(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "f", "x")
	put(t, root, "d/inner", "x")
	os.Symlink("/etc", filepath.Join(root, "l"))
	os.Mkdir(filepath.Join(root, "empty"), 0755)

	if err := h.Remove("f"); err != nil || exists(root, "f") {
		t.Fatalf("file: %v", err)
	}
	if err := h.Remove("l"); err != nil || exists(root, "l") {
		t.Fatalf("link: %v", err)
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Fatal("removing a link must not touch its target")
	}
	if err := h.Remove("empty"); err != nil || exists(root, "empty") {
		t.Fatalf("empty dir: %v", err)
	}
	if err := h.Remove("d"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("a directory with content must not be removed: %v", err)
	}
	if !exists(root, "d/inner") {
		t.Fatal("content of the directory was lost")
	}
	if err := h.Remove("never-existed"); err != nil {
		t.Fatalf("removing a missing path must succeed: %v", err)
	}
	if err := h.Remove("no-dir/never-existed"); err != nil {
		t.Fatalf("removing under a missing directory must succeed: %v", err)
	}
}

func TestRename(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "a.txt", "A")
	if err := h.Rename("a.txt", "deep/er/b.txt"); err != nil {
		t.Fatal(err)
	}
	if exists(root, "a.txt") || read(t, root, "deep/er/b.txt") != "A" {
		t.Fatal("rename did not move the file")
	}
	put(t, root, "c.txt", "C")
	if err := h.Rename("c.txt", "deep/er/b.txt"); err != nil {
		t.Fatalf("rename over a file: %v", err)
	}
	if read(t, root, "deep/er/b.txt") != "C" {
		t.Fatal("target not replaced")
	}
	if err := h.Rename("missing", "x"); !os.IsNotExist(err) {
		t.Fatalf("renaming a missing path: %v", err)
	}
}

func TestChmodAndSetTime(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "f", "x")
	if err := h.Chmod("f", 04755); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "f")); fi.Mode().Perm() != 0755 || fi.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	if err := h.SetTime("f", t0.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "f")); fi.ModTime().UnixNano() != t0.UnixNano() {
		t.Fatalf("mtime = %v", fi.ModTime().UnixNano())
	}
}

func TestLstat(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "f", "hello")
	os.Mkdir(filepath.Join(root, "d"), 0500) // no owner write
	os.Symlink("f", filepath.Join(root, "l"))
	syscall.Mkfifo(filepath.Join(root, "pipe"), 0600)

	if e, err := h.Lstat("f"); err != nil || e.Type != File || e.Size != 5 || e.Mode != 0644 {
		t.Fatalf("file: %+v %v", e, err)
	}
	if e, err := h.Lstat("d"); err != nil || e.Type != Dir || e.Mode != 0700 {
		t.Fatalf("dir: %+v %v (a directory always reports owner access)", e, err)
	}
	if e, err := h.Lstat("l"); err != nil || e.Type != Symlink {
		t.Fatalf("link: %+v %v", e, err)
	}
	if _, err := h.Lstat("pipe"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("fifo: %v", err)
	}
	if _, err := h.Lstat("nope"); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	if e, err := h.Lstat(""); err != nil || e.Type != Dir {
		t.Fatalf("home itself: %+v %v", e, err)
	}
}

func TestOpenReadRefusesAFifoWithoutBlocking(t *testing.T) {
	h, root := newHome(t)
	syscall.Mkfifo(filepath.Join(root, "pipe"), 0600)
	done := make(chan error, 1)
	go func() {
		_, err := h.OpenRead("pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenRead blocked on a FIFO")
	}
}

func TestOpenReadReturnsTheContent(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "d/f", "payload")
	f, err := h.OpenRead("d/f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b bytes.Buffer
	io.Copy(&b, f)
	if b.String() != "payload" {
		t.Fatalf("got %q", b.String())
	}
}

func TestListDir(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "a", "1")
	put(t, root, "d/b", "2")
	names, err := h.ListDir("")
	if err != nil || len(names) != 2 {
		t.Fatalf("%v %v", names, err)
	}
	names, err = h.ListDir("d")
	if err != nil || len(names) != 1 || names[0] != "b" {
		t.Fatalf("%v %v", names, err)
	}
	if _, err := h.ListDir("nope"); !os.IsNotExist(err) {
		t.Fatalf("missing dir: %v", err)
	}
}
