package persist

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"utils"
)

// behaviour every backend must have; run for unqlite always, and for MongoDB
// when OPENREPL_TEST_MONGODB_URI names one.
func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Fetch([]byte("missing")); err == nil {
		t.Fatal("a missing key was found")
	}
	for k, v := range map[string]string{"a": "1", "b": "2", "12345": "num"} {
		if err := s.Store([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	s.Commit()
	if v, err := s.Fetch([]byte("b")); err != nil || string(v) != "2" {
		t.Fatalf("fetch: %q %v", v, err)
	}
	s.Store([]byte("b"), []byte("22")) // a second store replaces
	s.Commit()
	if v, _ := s.Fetch([]byte("b")); string(v) != "22" {
		t.Fatalf("replace: %q", v)
	}
	var seen []string
	if err := s.Each(func(k, v []byte) bool { seen = append(seen, string(k)+"="+string(v)); return true }); err != nil {
		t.Fatal(err)
	}
	sort.Strings(seen)
	if fmt.Sprint(seen) != "[12345=num a=1 b=22]" {
		t.Fatalf("each: %v", seen)
	}
	n := 0
	s.Each(func(k, v []byte) bool { n++; return false })
	if n != 1 {
		t.Fatalf("each did not stop: %d", n)
	}
	if err := s.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	s.Commit()
	if _, err := s.Fetch([]byte("a")); err == nil {
		t.Fatal("a deleted key was found")
	}
	// a key that is not text
	bin := []byte{0xff, 0xfe, 0x00, 0x01}
	if err := s.Store(bin, []byte("bin")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Fetch(bin); err != nil || string(v) != "bin" {
		t.Fatalf("binary key: %q %v", v, err)
	}
}

func TestUnqliteStore(t *testing.T) {
	Configure(false)
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Backend() != "unqlite" {
		t.Fatalf("backend %q", s.Backend())
	}
	exercise(t, s)
}

func TestAWorkerKeepsUsingFilesEvenWithTheURISet(t *testing.T) {
	t.Setenv("OPENREPL_MONGODB_URI", "mongodb://127.0.0.1:1/")
	Configure(false)
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Backend() != "unqlite" || UsingMongo() {
		t.Fatalf("a worker used %s", s.Backend())
	}
}

func TestNotConfiguredMeansFiles(t *testing.T) {
	t.Setenv("OPENREPL_MONGODB_URI", "")
	Configure(true)
	if UsingMongo() {
		t.Fatal("MongoDB without a URI")
	}
}

func TestMongoStoreAndCopyOnce(t *testing.T) {
	uri := os.Getenv("OPENREPL_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("OPENREPL_TEST_MONGODB_URI is not set")
	}
	db := fmt.Sprintf("openrepl_test_%d", time.Now().UnixNano())
	t.Setenv("OPENREPL_MONGODB_URI", uri)
	t.Setenv("OPENREPL_MONGODB_DB", db)
	ResetRemote() // a fresh connection for this database
	Configure(true)
	t.Cleanup(func() {
		mu.Lock()
		if client != nil {
			c, cancel := ctx()
			client.Database(utils.MongoDBName()).Drop(c)
			cancel()
		}
		mu.Unlock()
		Configure(false)
		ResetRemote()
	})

	// the records of an existing unqlite file are copied the first time, once
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	Configure(false)
	local, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	local.Store([]byte("SESSION_KEY"), []byte("old-secret"))
	local.Store([]byte("user1"), []byte(`{"name":"a"}`))
	local.Commit()
	local.Close()
	Configure(true)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "mongodb" {
		t.Fatalf("backend %q", s.Backend())
	}
	if v, err := s.Fetch([]byte("SESSION_KEY")); err != nil || string(v) != "old-secret" {
		t.Fatalf("the copy: %q %v", v, err)
	}
	s.Store([]byte("user1"), []byte(`{"name":"changed"}`))
	s.Delete([]byte("SESSION_KEY"))

	// opening again does not copy again: MongoDB wins
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Fetch([]byte("SESSION_KEY")); err == nil {
		t.Fatal("the file was copied a second time")
	}
	if v, _ := s2.Fetch([]byte("user1")); string(v) != `{"name":"changed"}` {
		t.Fatalf("user1 = %q", v)
	}
	if err := s2.Delete([]byte("nothing")); err != ErrNotFound {
		t.Fatalf("deleting a missing key: %v", err)
	}

	exercise(t, mustOpen(t, filepath.Join(dir, "other.db")))
}

func mustOpen(t *testing.T, path string) Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMongoUnreachableFallsBackToFilesAfterThreeTries(t *testing.T) {
	t.Setenv("OPENREPL_MONGODB_URI", "mongodb://boss:hunter2@127.0.0.1:1/")
	t.Setenv("OPENREPL_FIRESTORE_CREDENTIALS", "")
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	fastInit(t)
	ResetRemote()
	Configure(true)
	defer func() { Configure(false); ResetRemote() }()
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backend, notes := Chosen()
	if s.Backend() != "unqlite" || backend != "unqlite" || Remote() != "" {
		t.Fatalf("backend %q chosen %q", s.Backend(), backend)
	}
	if !contains(notes, "MongoDB did not answer after 3 tries") || contains(notes, "hunter2") || contains(notes, "Firestore is not configured") == false {
		t.Fatalf("notes: %q", notes)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fastInit makes the start-up test quick: short tries, almost no wait.
func fastInit(t *testing.T) {
	t.Helper()
	oldT, oldP, oldD := initTries, probeTimeout, retryDelay
	initTries, probeTimeout, retryDelay = 3, 300*time.Millisecond, time.Millisecond
	t.Cleanup(func() { initTries, probeTimeout, retryDelay = oldT, oldP, oldD })
}
