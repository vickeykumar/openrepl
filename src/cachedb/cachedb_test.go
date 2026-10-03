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
