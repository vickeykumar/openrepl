package persist

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The local copy of a remote database (replica.go): filled at start, read locally, written through.

// fakeRemote is a database that counts what is asked of it and can be made to fail.
type fakeRemote struct {
	mu                              sync.Mutex
	data                            map[string]string
	fetches, eachs, stores, deletes int
	failStore, failEach, failFetch  error
}

func newFakeRemote(kv map[string]string) *fakeRemote {
	r := &fakeRemote{data: map[string]string{}}
	for k, v := range kv {
		r.data[k] = v
	}
	return r
}

func (f *fakeRemote) Store(k, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stores++
	if f.failStore != nil {
		return f.failStore
	}
	f.data[string(k)] = string(v)
	return nil
}
func (f *fakeRemote) Fetch(k []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	if f.failFetch != nil {
		return nil, f.failFetch
	}
	v, ok := f.data[string(k)]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(v), nil
}
func (f *fakeRemote) Delete(k []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if _, ok := f.data[string(k)]; !ok {
		return ErrNotFound
	}
	delete(f.data, string(k))
	return nil
}
func (f *fakeRemote) Commit() error   { return nil }
func (f *fakeRemote) Rollback() error { return nil }
func (f *fakeRemote) Each(fn func(key, value []byte) bool) error {
	f.mu.Lock()
	f.eachs++
	if f.failEach != nil {
		f.mu.Unlock()
		return f.failEach
	}
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		keys = append(keys, k)
	}
	snapshot := map[string]string{}
	for k, v := range f.data {
		snapshot[k] = v
	}
	f.mu.Unlock()
	sort.Strings(keys)
	for _, k := range keys {
		if !fn([]byte(k), []byte(snapshot[k])) {
			break
		}
	}
	return nil
}
func (f *fakeRemote) Close() error    { return nil }
func (f *fakeRemote) Backend() string { return "fake" }

// localCopy opens a real local file for a replica, in a folder of the test.
func localCopy(t *testing.T) Store {
	t.Helper()
	s, err := openLocalCopy(filepath.Join(t.TempDir(), "copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func filled(t *testing.T, kv map[string]string, lazy bool) (*replicaStore, *fakeRemote) {
	t.Helper()
	remote := newFakeRemote(kv)
	r := newReplica("things", remote, localCopy(t), lazy)
	if !lazy {
		if err := r.fill(); err != nil {
			t.Fatal(err)
		}
	}
	return r, remote
}

func fetched(t *testing.T, s Store, key string) string {
	t.Helper()
	v, err := s.Fetch([]byte(key))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(v)
}

func TestTheCopyIsFilledAtStartAndEveryReadIsLocal(t *testing.T) {
	r, remote := filled(t, map[string]string{"a": "1", "b": "2", "c": "3"}, false)
	if got := r.status(); got.Records != 3 || got.Mode != "full" {
		t.Fatalf("status: %+v", got)
	}
	if remote.eachs != 1 {
		t.Fatalf("the fill read the remote database %d times, want 1 scan", remote.eachs)
	}
	for i := 0; i < 50; i++ {
		if fetched(t, r, "b") != "2" || fetched(t, r, "nope") != "<"+ErrNotFound.Error()+">" {
			t.Fatal("a read of the copy gave the wrong answer")
		}
		var got []string
		r.Each(func(k, v []byte) bool { got = append(got, string(k)+"="+string(v)); return true })
		sort.Strings(got)
		if strings.Join(got, ",") != "a=1,b=2,c=3" {
			t.Fatalf("each: %v", got)
		}
	}
	if remote.fetches != 0 || remote.eachs != 1 {
		t.Errorf("reads reached the remote database: %d fetches, %d scans", remote.fetches, remote.eachs)
	}
	if r.Backend() != "fake" {
		t.Errorf("the backend is the remote one: %q", r.Backend())
	}
}

func TestAWriteGoesToTheRemoteDatabaseFirstAndThenToTheCopy(t *testing.T) {
	r, remote := filled(t, map[string]string{"a": "1"}, false)
	if err := r.Store([]byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	if remote.data["b"] != "2" || fetched(t, r, "b") != "2" {
		t.Errorf("remote %q, copy %q", remote.data["b"], fetched(t, r, "b"))
	}
	if err := r.Store([]byte("a"), []byte("changed")); err != nil {
		t.Fatal(err)
	}
	if remote.data["a"] != "changed" || fetched(t, r, "a") != "changed" {
		t.Error("a changed record is not changed in both")
	}
	if remote.fetches != 0 {
		t.Errorf("writing made %d remote reads", remote.fetches)
	}
}

func TestWhenTheRemoteDatabaseRefusesNothingIsWrittenToTheCopy(t *testing.T) {
	r, remote := filled(t, map[string]string{"a": "1"}, false)
	remote.failStore = errors.New("429")
	if err := r.Store([]byte("a"), []byte("new")); err == nil || err.Error() != "429" {
		t.Fatalf("the error was lost: %v", err)
	}
	if fetched(t, r, "a") != "1" {
		t.Errorf("the copy is ahead of the remote database: %q", fetched(t, r, "a"))
	}
	if err := r.Store([]byte("b"), []byte("2")); err == nil {
		t.Fatal("a new record was accepted")
	}
	if fetched(t, r, "b") != "<"+ErrNotFound.Error()+">" {
		t.Error("a record the remote database refused is in the copy")
	}
	if r.status().Broken {
		t.Error("a refused write is not a broken copy")
	}
}

func TestADeleteRemovesTheRecordFromBoth(t *testing.T) {
	r, remote := filled(t, map[string]string{"a": "1", "b": "2"}, false)
	if err := r.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, in := remote.data["a"]; in || fetched(t, r, "a") != "<"+ErrNotFound.Error()+">" {
		t.Error("the record is still somewhere")
	}
	// a record that is not there: the remote database's answer, and the copy is as it was
	if err := r.Delete([]byte("zzz")); err != ErrNotFound {
		t.Errorf("deleting a missing record: %v", err)
	}
	if fetched(t, r, "b") != "2" {
		t.Error("another record was lost")
	}
}

func TestEachMayWriteWhileItRuns(t *testing.T) {
	r, _ := filled(t, map[string]string{"a": "1", "b": "2"}, false)
	done := make(chan struct{})
	go func() {
		r.Each(func(k, v []byte) bool {
			r.Store(append([]byte("seen-"), k...), v) // a write inside the walk must not wait for the walk
			return true
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Each deadlocked with a write made in its callback")
	}
	if fetched(t, r, "seen-a") != "1" {
		t.Error("the write made inside the walk is missing")
	}
}

func TestAnOnDemandCopyReadsTheRemoteDatabaseOnceForEachRecord(t *testing.T) {
	r, remote := filled(t, map[string]string{"s1": "code1", "s2": "code2"}, true)
	if remote.eachs != 0 {
		t.Fatalf("an on-demand copy was filled at start (%d scans)", remote.eachs)
	}
	for i := 0; i < 20; i++ {
		if fetched(t, r, "s1") != "code1" {
			t.Fatal("wrong record")
		}
	}
	if remote.fetches != 1 {
		t.Errorf("20 reads of one record made %d remote reads, want 1", remote.fetches)
	}
	// a record that does not exist is not asked for again for a while
	for i := 0; i < 20; i++ {
		if fetched(t, r, "missing") != "<"+ErrNotFound.Error()+">" {
			t.Fatal("a missing record was found")
		}
	}
	if remote.fetches != 2 {
		t.Errorf("20 reads of a missing record made %d remote reads in all, want 2", remote.fetches)
	}
	// a record written later is found at once, even if it was missed before
	if err := r.Store([]byte("missing"), []byte("now here")); err != nil {
		t.Fatal(err)
	}
	if fetched(t, r, "missing") != "now here" {
		t.Error("a record that was written after it was missed is still missing")
	}
	// a list is the remote database's: the copy does not hold them all
	var n int
	r.Each(func(k, v []byte) bool { n++; return true })
	if n != 3 || remote.eachs != 1 {
		t.Errorf("each: %d records, %d scans", n, remote.eachs)
	}
	if r.status().Mode != "on demand" {
		t.Errorf("mode %q", r.status().Mode)
	}
}

func TestACopyThatCannotBeWrittenIsNotReadAnyMore(t *testing.T) {
	r, remote := filled(t, map[string]string{"a": "1"}, false)
	r.local.Close() // the file is gone from under it
	if err := r.Store([]byte("b"), []byte("2")); err != nil {
		t.Fatalf("the write reached the remote database and is not an error: %v", err)
	}
	if !r.status().Broken {
		t.Fatal("a failed write to the copy was not noticed")
	}
	// reads now come from the remote database, which has the new record
	if fetched(t, r, "b") != "2" || remote.fetches == 0 {
		t.Errorf("reads did not go to the remote database: %q, %d fetches", fetched(t, r, "b"), remote.fetches)
	}
	n := 0
	r.Each(func(k, v []byte) bool { n++; return true })
	if n != 2 {
		t.Errorf("each: %d records, want 2", n)
	}
}

func TestAFillThatFailsStopsTheServerUnlessFilesAreAllowed(t *testing.T) {
	old, oldDir := giveUp, replicaDir
	replicaDir = t.TempDir()
	var said string
	giveUp = func(msg string) { said = msg }
	t.Cleanup(func() { giveUp, replicaDir = old, oldDir })
	t.Setenv("OPENREPL_REPLICA", "")
	t.Setenv("OPENREPL_DB_FALLBACK", "")
	forgetReplicas()
	t.Cleanup(forgetReplicas)

	remote := newFakeRemote(map[string]string{"a": "1"})
	remote.failEach = errors.New("firestore: 429")
	s, err := wrapReplica("/opt/gotty/blog.db", remote)
	if err != nil {
		t.Fatal(err)
	}
	if said == "" || !strings.Contains(said, "blog") || !strings.Contains(said, "429") || !strings.Contains(said, "OPENREPL_REPLICA=off") {
		t.Errorf("the server was not stopped with a reason: %q", said)
	}
	if s != Store(remote) {
		t.Error("after giving up, the remote database itself is what is returned (the caller never runs on in the real server)")
	}

	// with files allowed it reads the remote database directly, as before there were replicas
	said = ""
	t.Setenv("OPENREPL_DB_FALLBACK", "files")
	s, _ = wrapReplica("/opt/gotty/blog.db", remote)
	if said != "" || s != Store(remote) {
		t.Errorf("fallback allowed: %q", said)
	}
	infos := ReplicaStatus()
	if len(infos) != 2 || infos[0].Mode != "off" {
		t.Errorf("status: %+v", infos)
	}
}

func TestTheReplicaCanBeSwitchedOff(t *testing.T) {
	t.Setenv("OPENREPL_REPLICA", "off")
	forgetReplicas()
	t.Cleanup(forgetReplicas)
	remote := newFakeRemote(nil)
	s, err := wrapReplica("/opt/gotty/feedback.db", remote)
	if err != nil || s != Store(remote) {
		t.Fatalf("%v %v", s, err)
	}
	if remote.eachs != 0 {
		t.Error("a switched-off replica read the remote database")
	}
	if got := ReplicaStatus(); len(got) != 1 || got[0].Name != "feedback" || got[0].Mode != "off" {
		t.Errorf("status: %+v", got)
	}
}

func TestACopyIsRebuiltAtEveryStart(t *testing.T) {
	dir := t.TempDir()
	oldDir := replicaDir
	replicaDir = dir
	t.Cleanup(func() { replicaDir = oldDir })
	t.Setenv("OPENREPL_REPLICA", "")
	forgetReplicas()
	t.Cleanup(forgetReplicas)

	// an earlier run left a copy with a record the database no longer has
	old, err := openLocalCopy(filepath.Join(dir, "blog.db"))
	if err != nil {
		t.Fatal(err)
	}
	old.Store([]byte("stale"), []byte("x"))
	old.Commit()
	old.Close()

	remote := newFakeRemote(map[string]string{"fresh": "y"})
	s, err := wrapReplica("/opt/gotty/blog.db", remote)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if fetched(t, s, "stale") != "<"+ErrNotFound.Error()+">" || fetched(t, s, "fresh") != "y" {
		t.Errorf("stale %q fresh %q", fetched(t, s, "stale"), fetched(t, s, "fresh"))
	}
	if got := ReplicaStatus(); len(got) != 1 || got[0].Records != 1 || got[0].Mode != "full" {
		t.Errorf("status: %+v", got)
	}
}

func TestTheReplicaHoldsUpUnderManyReadersAndWriters(t *testing.T) {
	r, remote := filled(t, map[string]string{"k0": "v0"}, false)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				k := []byte(fmt.Sprintf("k%d-%d", w, i))
				r.Store(k, []byte("v"))
				r.Commit()
				r.Fetch(k)
				if i%10 == 0 {
					r.Delete(k)
				}
			}
		}(w)
	}
	for rd := 0; rd < 4; rd++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				r.Each(func(k, v []byte) bool { return true })
				r.Fetch([]byte("k0"))
			}
		}()
	}
	wg.Wait()
	// what is in the copy is what is in the remote database
	var local []string
	r.Each(func(k, v []byte) bool { local = append(local, string(k)); return true })
	var rem []string
	for k := range remote.data {
		rem = append(rem, k)
	}
	sort.Strings(local)
	sort.Strings(rem)
	if strings.Join(local, ",") != strings.Join(rem, ",") {
		t.Errorf("the copy and the remote database differ:\n%v\n%v", local, rem)
	}
}
