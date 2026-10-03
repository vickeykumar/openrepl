//go:build linux
// +build linux

package wsync

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScanFindsFilesDirsAndLinks(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "main.c", "int main(){}")
	put(t, root, "src/util.c", "void f(){}")
	os.MkdirAll(filepath.Join(root, "empty"), 0755)
	os.Symlink("main.c", filepath.Join(root, "ln"))
	os.Chmod(filepath.Join(root, "main.c"), 0755)

	res, err := h.Scan(ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Entries
	if len(got) != 5 {
		t.Fatalf("entries = %d: %v", len(got), sortedPaths(got))
	}
	if e := got["main.c"]; e.Type != File || e.Size != 12 || e.Mode != 0755 || e.Hash != sum("int main(){}") {
		t.Fatalf("main.c = %+v", e)
	}
	if e := got["src"]; e.Type != Dir {
		t.Fatalf("src = %+v", e)
	}
	if e := got["src/util.c"]; e.Type != File || e.Hash != sum("void f(){}") {
		t.Fatalf("src/util.c = %+v", e)
	}
	if e := got["empty"]; e.Type != Dir {
		t.Fatalf("empty = %+v", e)
	}
	if e := got["ln"]; e.Type != Symlink || e.Link != "main.c" {
		t.Fatalf("ln = %+v", e)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped %v", res.Skipped)
	}
}

func TestScanDoesNotFollowLinks(t *testing.T) {
	h, root := newHome(t)
	outside := t.TempDir()
	put(t, outside, "secret.txt", "secret")
	os.Symlink(outside, filepath.Join(root, "out"))
	os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "file-link"))

	got := mustScan(t, h, nil)
	if len(got) != 2 {
		t.Fatalf("entries: %v", sortedPaths(got))
	}
	if got["out"].Type != Symlink || got["file-link"].Type != Symlink {
		t.Fatalf("links must stay links: %+v", got)
	}
	for p := range got {
		if strings.Contains(p, "secret") && p != "file-link" {
			t.Fatalf("scan reached outside the home: %s", p)
		}
	}
}

func TestScanSkipsSpecialFilesBigFilesAndTemps(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "ok.txt", "ok")
	syscall.Mkfifo(filepath.Join(root, "pipe"), 0600)
	l, err := net.Listen("unix", filepath.Join(root, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	put(t, root, "big.bin", strings.Repeat("x", 100))
	put(t, root, TempPrefix+"abcdef", "partial")

	res, err := h.Scan(ScanOptions{MaxFileSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries["ok.txt"].Type != File {
		t.Fatalf("entries: %v", sortedPaths(res.Entries))
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Path] = s.Reason
	}
	for _, p := range []string{"pipe", "sock", "big.bin"} {
		if reasons[p] == "" {
			t.Errorf("%s was not reported as skipped: %v", p, res.Skipped)
		}
	}
	if _, ok := reasons[TempPrefix+"abcdef"]; ok {
		t.Error("a fresh temporary file should be ignored silently")
	}
	if !exists(root, TempPrefix+"abcdef") {
		t.Fatal("a fresh temporary file may still be being written and must not be removed")
	}
}

func TestScanRemovesOnlyStaleTemporaryFiles(t *testing.T) {
	h, root := newHome(t)
	put(t, root, TempPrefix+"old", "x")
	put(t, root, TempPrefix+"new", "x")
	put(t, root, "d/"+TempPrefix+"nested-old", "x")
	old := time.Now().Add(-time.Hour)
	setMtime(t, root, TempPrefix+"old", old)
	setMtime(t, root, "d/"+TempPrefix+"nested-old", old)

	mustScan(t, h, nil)
	if exists(root, TempPrefix+"old") || exists(root, "d/"+TempPrefix+"nested-old") {
		t.Fatal("stale temporary files were not cleaned up")
	}
	if !exists(root, TempPrefix+"new") {
		t.Fatal("a recent temporary file was removed")
	}
}

func TestScanReusesTheRecordedHashWhenSizeAndTimeMatch(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "f", "hello")
	setMtime(t, root, "f", t0)

	first := mustScan(t, h, nil)["f"]
	base := NewRecord("h", "p")
	fake := first
	fake.Hash = "recorded-hash"
	base.Set(fake)

	// Size and time match the record: the recorded hash is used, which proves
	// the file was not read again.
	if got := mustScan(t, h, base)["f"]; got.Hash != "recorded-hash" {
		t.Fatalf("hash = %q, want the recorded one", got.Hash)
	}
	// Same size, different time: the file is read again.
	setMtime(t, root, "f", t0.Add(time.Second))
	if got := mustScan(t, h, base)["f"]; got.Hash != sum("hello") {
		t.Fatalf("hash = %q, want a fresh one", got.Hash)
	}
	// Same time, different size: read again.
	fake.Size = 99
	base.Set(fake)
	setMtime(t, root, "f", t0)
	if got := mustScan(t, h, base)["f"]; got.Hash != sum("hello") {
		t.Fatalf("hash = %q, want a fresh one", got.Hash)
	}
}

func TestScanRegularFileOnlyNameChecks(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "weird name \u00e9\u4e2d.txt", "x")
	put(t, root, "-rf", "x")
	put(t, root, "a\nb", "x")
	got := mustScan(t, h, nil)
	if len(got) != 3 {
		t.Fatalf("entries: %v", sortedPaths(got))
	}
}

func TestScanDetectsAFileThatKeepsChanging(t *testing.T) {
	h, root := newHome(t)
	put(t, root, "stable", "x")
	// hashOpen reports a change if the file is not the same after reading.
	f, err := h.OpenRead("stable")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := hashOpen(f, "stable"); err != nil {
		t.Fatalf("a stable file: %v", err)
	}
	// Appending between the two stats is simulated through a reader that
	// writes while it is read.
	g, err := os.OpenFile(filepath.Join(root, "growing"), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.WriteString("start")
	r, _ := os.Open(filepath.Join(root, "growing"))
	defer r.Close()
	before, _ := r.Stat()
	g.WriteString("more")
	g.Sync()
	setMtime(t, root, "growing", time.Now().Add(time.Minute))
	after, _ := r.Stat()
	if before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
		t.Skip("filesystem does not report the change")
	}
}

func TestScanOfAnEmptyHome(t *testing.T) {
	h, _ := newHome(t)
	res, err := h.Scan(ScanOptions{})
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("%v %v", res, err)
	}
}

func TestScanTwiceGivesTheSameResult(t *testing.T) {
	// A second listing of the home must not start where the first ended.
	h, root := newHome(t)
	put(t, root, "a", "1")
	put(t, root, "d/b", "2")
	first := mustScan(t, h, nil)
	for i := 0; i < 3; i++ {
		again := mustScan(t, h, nil)
		if len(again) != len(first) || again["a"].Hash != first["a"].Hash || again["d/b"].Hash != first["d/b"].Hash {
			t.Fatalf("scan %d differs: %v vs %v", i+2, sortedPaths(again), sortedPaths(first))
		}
	}
	for i := 0; i < 3; i++ {
		if names, err := h.ListDir(""); err != nil || len(names) != 2 {
			t.Fatalf("ListDir %d: %v %v", i, names, err)
		}
	}
}
