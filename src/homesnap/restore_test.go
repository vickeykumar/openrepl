package homesnap

import (
	"archive/zip"
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"objstore"
)

// three snapshots of one home: generation 1 holds a.txt and b/b.txt, 2 adds c.txt, 3 changes a.txt
func threeSnapshots(t *testing.T) (*Snapper, *objstore.Fake, string, func()) {
	t.Helper()
	f := objstore.NewFake()
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	write(t, filepath.Join(dir, "a.txt"), "one", time.Hour)
	write(t, filepath.Join(dir, "b", "b.txt"), "bee", time.Hour)
	clock := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	snap := func() {
		t.Helper()
		if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
			t.Fatalf("snapshot: %+v %v", res, err)
		}
		clock = clock.Add(2 * time.Hour)
	}
	snap()
	write(t, filepath.Join(dir, "c.txt"), "see", time.Hour)
	snap()
	write(t, filepath.Join(dir, "a.txt"), "uno", time.Hour)
	snap()
	return s, f, dir, func() { clock = clock.Add(2 * time.Minute) }
}

func TestTheListIsReadFromTheBucketNewestFirst(t *testing.T) {
	s, f, _, _ := threeSnapshots(t)
	defer f.Close()
	infos, err := s.List(ctx, "anna0123")
	if err != nil || len(infos) != 3 {
		t.Fatalf("%+v %v", infos, err)
	}
	if infos[0].Generation != 3 || !infos[0].Newest || infos[1].Generation != 2 || infos[2].Generation != 1 || infos[2].Newest {
		t.Errorf("order: %+v", infos)
	}
	if infos[2].At.Format("2006-01-02 15:04") != "2026-11-12 10:00" || infos[0].Size <= 0 {
		t.Errorf("time and size: %+v", infos[2])
	}
	// another user's home, a guest, and a folder that is not a home are not listed
	for _, h := range []string{"guest-abc", "bob00000", "../x"} {
		if infos, err := s.List(ctx, h); err == nil && len(infos) > 0 {
			t.Errorf("%s: %+v", h, infos)
		}
	}
}

func TestASnapshotMuchSmallerThanTheOneBeforeItIsMarkedFew(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	// not compressible, as a real workspace is
	b := make([]byte, 60000)
	rand.New(rand.NewSource(1)).Read(b)
	write(t, filepath.Join(dir, "big.bin"), string(b), time.Hour)
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Fatal(res, err)
	}
	os.RemoveAll(dir)
	write(t, filepath.Join(dir, ".bashrc"), "x", time.Hour)
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Fatal(res, err)
	}
	infos, _ := s.List(ctx, "anna0123")
	if len(infos) != 2 || !infos[0].Few || infos[1].Few {
		t.Errorf("few: %+v", infos)
	}
}

func TestASnapshotCanComeBackInANewFolderAndNothingThereIsTouched(t *testing.T) {
	s, f, dir, later := threeSnapshots(t)
	defer f.Close()
	write(t, filepath.Join(dir, "today.txt"), "work of today", time.Minute)
	res, err := s.Restore(ctx, "anna0123", 1, "folder")
	if err != nil || res.Folder != "restored-2026-11-12-1000" || res.Files != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, res.Folder, "a.txt")); string(b) != "one" {
		t.Errorf("a.txt of generation 1: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, res.Folder, "b", "b.txt")); string(b) != "bee" {
		t.Errorf("b/b.txt: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "uno" {
		t.Errorf("the file that is there was touched: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "today.txt")); string(b) != "work of today" {
		t.Errorf("today.txt: %q", b)
	}
	// a second time within a minute is refused, later it gets a new name
	if _, err := s.Restore(ctx, "anna0123", 1, "folder"); err != ErrTooSoon {
		t.Errorf("too soon: %v", err)
	}
	later()
	later()
	if res, err := s.Restore(ctx, "anna0123", 1, "folder"); err != nil || res.Folder != "restored-2026-11-12-1000-2" {
		t.Errorf("second folder: %+v %v", res, err)
	}
}

func TestReplaceMakesTheWorkspaceTheSnapshotAfterACopyOfWhatWasThere(t *testing.T) {
	s, f, dir, _ := threeSnapshots(t)
	defer f.Close()
	write(t, filepath.Join(dir, "today.txt"), "work of today", time.Minute)
	// the oldest of the three: the copy that is made first pushes it out of the bucket, and it must still come back
	res, err := s.Restore(ctx, "anna0123", 1, "replace")
	if err != nil || res.Mode != "replace" || res.Files != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	for name, want := range map[string]string{"a.txt": "one", "b/b.txt": "bee"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != want {
			t.Errorf("%s is %q, want %q", name, b, want)
		}
	}
	for _, gone := range []string{"c.txt", "today.txt"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s was not removed", gone)
		}
	}
	// the files are as new as now, so that the sync with a worker keeps them
	if fi, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil || s.now().Sub(fi.ModTime()) > time.Minute || fi.ModTime().Sub(s.now()) > time.Minute {
		t.Errorf("modification time: %v %v", fi, err)
	}
	// nothing is left of the work: no folder of the old home or of the restore in the workspaces folder
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".replaced-") || strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
	// the copy of what was there is in the list, as "before", and holds today's file
	infos, _ := s.List(ctx, "anna0123")
	if len(infos) == 0 || !infos[0].Before {
		t.Fatalf("the list: %+v", infos)
	}
	data, _, err := s.Archive(ctx, "anna0123", infos[0].Generation)
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	if err := ToZip(data, &zipped); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(zipped.Bytes()), int64(zipped.Len()))
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "today.txt") || !strings.Contains(strings.Join(names, ","), "c.txt") {
		t.Errorf("the safety copy: %v", names)
	}
}

func TestReplaceChangesNothingWhenTheCopyOfWhatIsThereCannotBeMade(t *testing.T) {
	s, f, dir, _ := threeSnapshots(t)
	defer f.Close()
	write(t, filepath.Join(dir, "today.txt"), "work of today", time.Minute)
	f.CutPuts(true) // the upload of the copy dies
	if _, err := s.Restore(ctx, "anna0123", 2, "replace"); err == nil {
		t.Fatal("replaced although the copy could not be made")
	}
	f.CutPuts(false)
	if b, _ := os.ReadFile(filepath.Join(dir, "today.txt")); string(b) != "work of today" {
		t.Errorf("the workspace was changed: %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".replaced-") || strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func TestASnapshotThatIsNotThereOrIsAnotherUsersIsRefused(t *testing.T) {
	s, f, _, _ := threeSnapshots(t)
	defer f.Close()
	if _, err := s.Restore(ctx, "anna0123", 99, "folder"); err != ErrNoSuchSnapshot {
		t.Errorf("a generation that is not there: %v", err)
	}
	if _, err := s.Restore(ctx, "anna0123", 1, "everything"); err == nil {
		t.Error("an unknown way was taken")
	}
	if _, err := s.Restore(ctx, "guest-abc", 1, "folder"); err == nil {
		t.Error("a guest's home was restored")
	}
	if _, err := s.Restore(ctx, "bob00000", 1, "folder"); err == nil {
		t.Error("a home with no snapshots was restored")
	}
}

func TestAZipHasTheFilesAndFoldersOfTheSnapshot(t *testing.T) {
	s, f, _, _ := threeSnapshots(t)
	defer f.Close()
	data, _, err := s.Archive(ctx, "anna0123", 2)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ToZip(data, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		b := new(bytes.Buffer)
		b.ReadFrom(rc)
		rc.Close()
		got[zf.Name] = b.String()
	}
	if got["a.txt"] != "one" || got["b/b.txt"] != "bee" || got["c.txt"] != "see" {
		t.Errorf("%v", got)
	}
	if _, ok := got["MANIFEST.json"]; ok {
		t.Error("the manifest is not a file of the user")
	}
}
