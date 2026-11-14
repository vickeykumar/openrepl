package homesnap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"objstore"
)

func TestVersionsToDropKeepsTheNewestAndTheLastOfEachClockHourForSixHours(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 50, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 14, h, m, 0, 0, time.UTC) }
	mk := func(id string, t time.Time) objstore.Version {
		return objstore.Version{Key: "k", VersionID: id, LastModified: t}
	}
	vs := []objstore.Version{
		mk("v-now", at(12, 45)),     // the newest
		mk("v-12b", at(12, 30)),     // the same hour as the newest: dropped
		mk("v-11-last", at(11, 50)), // the last of hour 11: kept
		mk("v-11-first", at(11, 20)),
		mk("v-9", at(9, 40)), // kept
		mk("v-7", at(7, 30)), // hour 7 is the oldest of the six hours (7 to 12): kept
		mk("v-6", at(6, 30)), // older than that: dropped
		mk("v-yesterday", at(23, 0).AddDate(0, 0, -1)),
		{Key: "k", VersionID: "marker", LastModified: at(11, 0), DeleteMarker: true}, // never dropped
	}
	drop := map[string]bool{}
	for _, v := range versionsToDrop(vs, now) {
		drop[v.VersionID] = true
	}
	want := map[string]bool{"v-12b": true, "v-11-first": true, "v-6": true, "v-yesterday": true}
	for id := range want {
		if !drop[id] {
			t.Errorf("%s was not dropped", id)
		}
	}
	for id := range drop {
		if !want[id] {
			t.Errorf("%s was dropped", id)
		}
	}
	if len(versionsToDrop(vs[:1], now)) != 0 {
		t.Error("the only version was dropped")
	}
}

func TestSnapshotsThinTheVersionsOfTheBucket(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	f.SetVersioned(true)
	s, base := newSnapper(t, f)
	dir := filepath.Join(base, "anna0123")
	clock := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	f.Now = func() time.Time { return clock }
	c, err := objstore.New(f.Config())
	if err != nil {
		t.Fatal(err)
	}
	// a snapshot every 5 minutes for four hours, the home changing each time: 49 snapshots of one day's file
	removedTotal := 0
	for i := 0; i <= 48; i++ {
		write(t, filepath.Join(dir, "a.txt"), strings.Repeat("x", i+1), time.Hour)
		res, err := s.SnapshotHome(ctx, "anna0123", true)
		if err != nil || !res.Made {
			t.Fatalf("snapshot %d: %+v %v", i, res, err)
		}
		removedTotal += res.VersionsRemoved
		clock = clock.Add(5 * time.Minute)
	}
	if removedTotal < 2*(49-5) {
		t.Errorf("the snapshots removed %d old versions of the two files, want %d", removedTotal, 2*(49-5))
	}
	all, err := c.ListVersions(ctx, s.prefix("anna0123"))
	if err != nil {
		t.Fatal(err)
	}
	perKey := map[string]int{}
	for _, v := range all {
		perKey[v.Key]++
	}
	for key, n := range perKey {
		// 49 snapshots between 09:00 and 13:00: the newest, and the last of each of the hours 09, 10, 11 and 12
		if n != 5 {
			t.Errorf("%s keeps %d versions", key, n)
		}
	}
	// the day's file is still the current one, and a restore from it works
	os.RemoveAll(dir)
	restored, err := s.Ensure(ctx, "anna0123")
	if err != nil || !restored {
		t.Fatalf("restore after the thinning: %v %v", restored, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || len(b) != 49 {
		t.Errorf("a.txt after the restore: %d bytes %v", len(b), err)
	}
}

func TestAStoreWithoutVersionsIsLeftAlone(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, _ := newSnapper(t, f)
	c, _ := objstore.New(f.Config())
	c.Put(ctx, s.prefix("h")+"h-2026-11-14.tar.gz", []byte("x"), "", nil)
	if n := s.pruneVersions(ctx, c, "h"); n != 0 {
		t.Errorf("removed %d", n)
	}
}
