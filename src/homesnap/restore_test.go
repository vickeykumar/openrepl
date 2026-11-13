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

// a home with a snapshot on each of three UTC days, and a second one on the last day (so that its file has an earlier version)
// day 1 (2026-11-10): a.txt "one", b/b.txt; day 2 (11-11): c.txt added; day 3 (11-12): a.txt "uno", then d.txt added later in the day
type days struct {
	s     *Snapper
	f     *objstore.Fake
	dir   string
	clock *time.Time
}

func threeDays(t *testing.T, versioned bool) days {
	t.Helper()
	f := objstore.NewFake()
	f.SetVersioned(versioned)
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	clock := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	f.Now = func() time.Time { return clock }
	write(t, filepath.Join(dir, "a.txt"), "one", time.Hour)
	write(t, filepath.Join(dir, "b", "b.txt"), "bee", time.Hour)
	snap := func() {
		t.Helper()
		if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
			t.Fatalf("snapshot: %+v %v", res, err)
		}
	}
	snap()
	clock = clock.Add(24 * time.Hour)
	write(t, filepath.Join(dir, "c.txt"), "see", time.Hour)
	snap()
	clock = clock.Add(24 * time.Hour)
	write(t, filepath.Join(dir, "a.txt"), "uno", time.Hour)
	snap()
	clock = clock.Add(2 * time.Hour)
	write(t, filepath.Join(dir, "d.txt"), "dee", time.Hour)
	snap()
	clock = clock.Add(2 * time.Minute)
	return days{s: s, f: f, dir: dir, clock: &clock}
}

func (d days) later(dur time.Duration) { *d.clock = d.clock.Add(dur) }

func TestThereIsOneFileForEachUTCDayAndASnapshotOverwritesItsDaysFile(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	var got []string
	for _, k := range d.f.Stored() {
		got = append(got, k[strings.LastIndex(k, "/")+1:])
	}
	want := []string{"anna0123-2026-11-10.tar.gz", "anna0123-2026-11-11.tar.gz", "anna0123-2026-11-12.tar.gz", "latest.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%v, want %v", got, want)
	}
	// the day's file holds the end of the day: the second snapshot of day 3
	data, info, err := d.s.Archive(ctx, "anna0123", "d:2026-11-12")
	if err != nil || info.Day != "2026-11-12" {
		t.Fatal(info, err)
	}
	var zipped bytes.Buffer
	if err := ToZip(data, &zipped); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(zipped.Bytes()), int64(zipped.Len()))
	names := ""
	for _, f := range zr.File {
		names += f.Name + ","
	}
	if !strings.Contains(names, "d.txt") || !strings.Contains(names, "c.txt") {
		t.Errorf("the end of the day: %s", names)
	}
}

func TestTheListHasTheDaysNewestFirstAndTheEarlierVersionsOfADay(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	infos, err := d.s.List(ctx, "anna0123")
	if err != nil || len(infos) != 3 {
		t.Fatalf("%+v %v", infos, err)
	}
	if infos[0].ID != "d:2026-11-12" || !infos[0].Newest || infos[1].ID != "d:2026-11-11" || infos[2].ID != "d:2026-11-10" || infos[2].Newest {
		t.Errorf("order: %+v", infos)
	}
	if infos[0].Day != "2026-11-12" || infos[0].Size <= 0 || infos[0].At.Format("2006-01-02 15:04") != "2026-11-12 12:00" {
		t.Errorf("the last write of the day: %+v", infos[0])
	}
	// day 3 was written twice: one earlier version, from before d.txt was added; days written once have none
	if len(infos[0].Earlier) != 1 || infos[0].Earlier[0].Kind != "version" || infos[0].Earlier[0].At.Format("15:04") != "10:00" {
		t.Errorf("earlier versions of the last day: %+v", infos[0].Earlier)
	}
	if len(infos[1].Earlier) != 0 || len(infos[2].Earlier) != 0 {
		t.Errorf("a day written once has no earlier version: %+v %+v", infos[1].Earlier, infos[2].Earlier)
	}
	// another user's home, a guest, and a folder that is not a home are not listed
	for _, h := range []string{"guest-abc", "bob00000", "../x"} {
		if infos, err := d.s.List(ctx, h); err == nil && len(infos) > 0 {
			t.Errorf("%s: %+v", h, infos)
		}
	}
}

func TestAnEarlierVersionOfADayComesBackAsItWas(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	infos, _ := d.s.List(ctx, "anna0123")
	earlier := infos[0].Earlier[0]
	res, err := d.s.Restore(ctx, "anna0123", earlier.ID, "folder")
	if err != nil || res.Files != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(d.dir, res.Folder, "d.txt")); err == nil {
		t.Error("d.txt was added after that version")
	}
	if b, _ := os.ReadFile(filepath.Join(d.dir, res.Folder, "c.txt")); string(b) != "see" {
		t.Errorf("c.txt: %q", b)
	}
	// an ID that is not in the list is refused, whatever it looks like
	for _, id := range []string{"d:2026-11-12@v99999", "d:2030-01-01", "../../x", "g:5", "d:2026-11-12@"} {
		if _, err := d.s.Restore(ctx, "anna0123", id, "folder"); err == nil {
			t.Errorf("%q was taken", id)
		}
	}
}

func TestADayThatFellOutOfTheKeptDaysIsHiddenAndStillListedWhileTheStoreKeepsIt(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	// a fourth day: the first day's file is removed from the bucket (hidden, on a versioned one)
	d.later(24 * time.Hour)
	write(t, filepath.Join(d.dir, "e.txt"), "eee", time.Hour)
	if res, err := d.s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made || res.Deleted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	infos, _ := d.s.List(ctx, "anna0123")
	if len(infos) != 4 || infos[0].Day != "2026-11-13" || infos[3].Kind != "hidden" || infos[3].Day != "2026-11-10" {
		t.Fatalf("%+v", infos)
	}
	if infos[3].Newest || infos[0].Kind != "day" {
		t.Errorf("flags: %+v", infos)
	}
	// it can still be got back
	res, err := d.s.Restore(ctx, "anna0123", infos[3].ID, "folder")
	if err != nil || res.Files != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(d.dir, res.Folder, "a.txt")); string(b) != "one" {
		t.Errorf("a.txt of the removed day: %q", b)
	}
}

func TestWithoutVersionsTheListHasTheKeptDaysOnly(t *testing.T) {
	d := threeDays(t, false)
	defer d.f.Close()
	d.later(24 * time.Hour)
	write(t, filepath.Join(d.dir, "e.txt"), "eee", time.Hour)
	d.s.SnapshotHome(ctx, "anna0123", true)
	infos, err := d.s.List(ctx, "anna0123")
	if err != nil || len(infos) != 3 {
		t.Fatalf("%+v %v", infos, err)
	}
	for _, in := range infos {
		if len(in.Earlier) != 0 || in.Kind != "day" {
			t.Errorf("no history to show: %+v", in)
		}
	}
	if infos[0].Day != "2026-11-13" || infos[2].Day != "2026-11-11" {
		t.Errorf("the kept days: %+v", infos)
	}
}

func TestASnapshotMuchSmallerThanTheOneBeforeItIsMarkedFew(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	clock := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	f.Now = func() time.Time { return clock }
	// not compressible, as a real workspace is
	b := make([]byte, 60000)
	rand.New(rand.NewSource(1)).Read(b)
	write(t, filepath.Join(dir, "big.bin"), string(b), time.Hour)
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Fatal(res, err)
	}
	clock = clock.Add(24 * time.Hour)
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

func TestADayCanComeBackInANewFolderAndNothingThereIsTouched(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	write(t, filepath.Join(d.dir, "today.txt"), "work of today", time.Minute)
	res, err := d.s.Restore(ctx, "anna0123", "d:2026-11-10", "folder")
	if err != nil || res.Folder != "restored-2026-11-10-1000" || res.Files != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(d.dir, res.Folder, "a.txt")); string(b) != "one" {
		t.Errorf("a.txt of day 1: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(d.dir, "a.txt")); string(b) != "uno" {
		t.Errorf("the file that is there was touched: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(d.dir, "today.txt")); string(b) != "work of today" {
		t.Errorf("today.txt: %q", b)
	}
	// a second time within a minute is refused, later it gets a new name
	if _, err := d.s.Restore(ctx, "anna0123", "d:2026-11-10", "folder"); err != ErrTooSoon {
		t.Errorf("too soon: %v", err)
	}
	d.later(2 * time.Minute)
	if res, err := d.s.Restore(ctx, "anna0123", "d:2026-11-10", "folder"); err != nil || res.Folder != "restored-2026-11-10-1000-2" {
		t.Errorf("second folder: %+v %v", res, err)
	}
}

func TestReplaceMakesTheWorkspaceExactlyTheSnapshot(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	write(t, filepath.Join(d.dir, "today.txt"), "work of today", time.Minute)
	res, err := d.s.Restore(ctx, "anna0123", "d:2026-11-10", "replace")
	if err != nil || res.Mode != "replace" || res.Files != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	for name, want := range map[string]string{"a.txt": "one", "b/b.txt": "bee"} {
		if b, _ := os.ReadFile(filepath.Join(d.dir, name)); string(b) != want {
			t.Errorf("%s is %q, want %q", name, b, want)
		}
	}
	for _, gone := range []string{"c.txt", "d.txt", "today.txt"} {
		if _, err := os.Stat(filepath.Join(d.dir, gone)); err == nil {
			t.Errorf("%s was not removed", gone)
		}
	}
	if fi, err := os.Stat(filepath.Join(d.dir, "a.txt")); err != nil || d.s.now().Sub(fi.ModTime()) > time.Minute || fi.ModTime().Sub(d.s.now()) > time.Minute {
		t.Errorf("modification time: %v %v", fi, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(d.dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".replaced-") || strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
	// nothing is saved of what was there: the list is still the three days
	infos, _ := d.s.List(ctx, "anna0123")
	for _, in := range infos {
		if strings.Contains(in.ID, "b:") {
			t.Errorf("a copy was made: %+v", in)
		}
	}
}

func TestASnapshotThatIsNotThereOrIsAnotherUsersIsRefused(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	if _, err := d.s.Restore(ctx, "anna0123", "d:2030-01-01", "folder"); err != ErrNoSuchSnapshot {
		t.Errorf("a day that is not there: %v", err)
	}
	if _, err := d.s.Restore(ctx, "anna0123", "d:2026-11-10", "everything"); err == nil {
		t.Error("an unknown way was taken")
	}
	if _, err := d.s.Restore(ctx, "guest-abc", "d:2026-11-10", "folder"); err == nil {
		t.Error("a guest's home was restored")
	}
	if _, err := d.s.Restore(ctx, "bob00000", "d:2026-11-10", "folder"); err == nil {
		t.Error("a home with no snapshots was restored")
	}
}

func TestOldSnapshotsOfTheEarlierSchemeAreReadAndRemovedWhenOldAndADayExists(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	clock := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	f.Now = func() time.Time { return clock }
	write(t, filepath.Join(dir, "a.txt"), "one", time.Hour)
	// a snapshot of the earlier scheme: put by hand, the name of that scheme
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Fatal(res, err)
	}
	day := f.Stored()[0]
	data, _ := f.Object(day)
	f.Set("pfx/homes/prod/anna0123/0000000007-20261108T100000Z-abcdef.tar.gz", data, clock.Add(-4*24*time.Hour)) // 4 days old
	f.Set("pfx/homes/prod/anna0123/0000000008-20261111T100000Z-abcdef.tar.gz", data, clock.Add(-24*time.Hour))   // a day old
	infos, _ := s.List(ctx, "anna0123")
	kinds := ""
	for _, in := range infos {
		kinds += in.Kind + ","
	}
	if !strings.Contains(kinds, "legacy") || len(infos) != 3 {
		t.Fatalf("the earlier scheme is listed: %+v", infos)
	}
	// the next snapshot removes the one that is old, and keeps the one of a day ago
	clock = clock.Add(time.Hour)
	write(t, filepath.Join(dir, "b.txt"), "bee", time.Minute)
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || res.Deleted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, k := range f.Stored() {
		if strings.Contains(k, "0000000007-") {
			t.Errorf("an old snapshot is still there: %s", k)
		}
	}
	if !strings.Contains(strings.Join(f.Stored(), ","), "0000000008-") {
		t.Error("a snapshot of a day ago was removed")
	}
}

func TestAZipHasTheFilesAndFoldersOfTheSnapshot(t *testing.T) {
	d := threeDays(t, true)
	defer d.f.Close()
	data, _, err := d.s.Archive(ctx, "anna0123", "d:2026-11-10")
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
	if got["a.txt"] != "one" || got["b/b.txt"] != "bee" {
		t.Errorf("%v", got)
	}
	if _, ok := got["MANIFEST.json"]; ok {
		t.Error("the manifest is not a file of the user")
	}
}

// the generation in the name of an object of the earlier scheme has leading zeros: it is read as a decimal number, and each
// object has an id of its own (it was read as octal once, and every one from 8 up was "g:0")
func TestTheObjectsOfTheEarlierSchemeHaveIdsOfTheirOwn(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	makeHome(t, base, "anna0123")
	cl, _ := objstore.New(f.Config())
	data := []byte("x")
	for _, n := range []string{"0000000089-20261112T201626Z-aa", "0000000090-20261112T211123Z-aa", "0000000100-20261113T081200Z-aa"} {
		cl.Put(ctx, s.prefix("anna0123")+n+".tar.gz", data, "application/gzip", nil)
	}
	infos, err := s.List(ctx, "anna0123")
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, in := range infos {
		got += in.ID + ","
	}
	if got != "g:100,g:90,g:89," {
		t.Errorf("ids: %s", got)
	}
}
