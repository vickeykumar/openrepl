package cachedb

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestEachVisitsEveryRecordAndCanStop(t *testing.T) {
	db, err := NewDatabase(filepath.Join(t.TempDir(), "each.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	visited := 0
	if err := db.Each(func(k, v []byte) bool { visited++; return true }); err != nil || visited != 0 {
		t.Fatalf("an empty database: visited %d, err %v", visited, err)
	}

	want := map[string]string{"a": "1", "b": "2", "c": "3"}
	for k, v := range want {
		if err := db.Store([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	db.Commit()

	got := map[string]string{}
	if err := db.Each(func(k, v []byte) bool { got[string(k)] = string(v); return true }); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]string) []string {
		out := []string{}
		for k, v := range m {
			out = append(out, k+"="+v)
		}
		sort.Strings(out)
		return out
	}
	if len(got) != 3 || keys(got)[0] != "a=1" || keys(got)[2] != "c=3" {
		t.Errorf("visited %v, want %v", keys(got), keys(want))
	}

	n := 0
	db.Each(func(k, v []byte) bool { n++; return false })
	if n != 1 {
		t.Errorf("Each went on after fn returned false: %d calls", n)
	}
}

// The cache keeps no entry bigger than a 1024th of its size, and says so with an error that Store ignores. The old, smaller
// value of the key must not stay in the cache then: a read would return it for ever, and a profile that grew (a user with many
// sessions) would never show its newest session.
func TestAValueTooBigForTheCacheDoesNotLeaveTheOldOneBehind(t *testing.T) {
	db, err := NewDatabaseSize(filepath.Join(t.TempDir(), "big.db"), 512*1024) // an entry of at most 512 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	small := []byte("small value")
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	if err := db.Store([]byte("k"), small); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Fetch([]byte("k")); err != nil || string(got) != string(small) {
		t.Fatalf("the small value: %q %v", got, err)
	}
	if err := db.Store([]byte("k"), big); err != nil {
		t.Fatal(err)
	}
	got, err := db.Fetch([]byte("k"))
	if err != nil || len(got) != len(big) {
		t.Fatalf("after storing a value the cache cannot hold, a read returned %d bytes (%v), not the %d stored", len(got), err, len(big))
	}
}
