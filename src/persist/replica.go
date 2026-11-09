package persist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"utils"
)

// A local copy of a remote database (Firestore or MongoDB).
//
// Firestore bills a read for every record a scan returns, and the dashboard and the pages read lists
// all day, so a server that read its databases straight from Firestore could use up the free daily
// allowance (and did, on 9 Nov 2026). With a replica the server fills a local UnQLite file from the
// remote database when it starts and then answers every read from that file; only a write goes to the
// remote database, and the remote database stays the one that is true:
//
//   - a write goes to the remote database first and to the local file after it, so the local copy is
//     never ahead; if the remote database refuses, nothing is written locally and the caller gets the error;
//   - the file is rebuilt at every start (it is only a copy), and a server that cannot fill it does not
//     start (giveUp) instead of coming up with an empty database;
//   - a collection that may be large (the shared snippets) is not filled at start but kept on demand: a
//     record is read from the remote database the first time it is asked for and stays in the file.
//
// One server only. Another server's writes are not seen until this one restarts (a second stage would
// follow the remote database's changes; see LLD 05).

// replicaDir holds the local copies, apart from the files of the databases themselves (backups collect
// *.db in the data directory, and a copy is rebuilt anyway).
var replicaDir = utils.GOTTY_PATH + "/replica"

const (
	replicaMissTTL  = 5 * time.Minute // how long a key the remote database did not have is not asked for again
	replicaMissKeep = 10000           // how many such keys are remembered
)

// ReplicaInfo says how one database is kept, for the Health page.
type ReplicaInfo struct {
	Name    string
	Mode    string // "full" (filled at start), "on demand" or "off" (every read is a read of the remote database)
	Records int    // records copied at start (full)
	Loaded  time.Time
	Took    time.Duration
	// Broken: the local copy could not be written, so reads go to the remote database again
	Broken bool
}

var (
	replicaMu   sync.Mutex
	replicaList []*replicaStore
	replicaOff  []ReplicaInfo // databases opened with the replica switched off
)

// ReplicaStatus lists the databases opened so far and how each is kept.
func ReplicaStatus() []ReplicaInfo {
	replicaMu.Lock()
	defer replicaMu.Unlock()
	out := append([]ReplicaInfo(nil), replicaOff...)
	for _, r := range replicaList {
		out = append(out, r.status())
	}
	return out
}

func forgetReplicas() {
	replicaMu.Lock()
	replicaList, replicaOff = nil, nil
	replicaMu.Unlock()
}

type replicaStore struct {
	name   string
	remote Store
	local  Store
	lazy   bool

	wmu sync.Mutex // one write at a time, whole: the remote and then the local copy, in the same order
	lmu sync.Mutex // the local file, one use at a time

	mu     sync.Mutex
	broken bool
	info   ReplicaInfo
	missed map[string]time.Time
}

// wrapReplica makes the local copy of remote, which lives in the database file path (its name names the
// copy). It returns remote itself when the replica is switched off, and when the copy cannot be filled
// and files in place of the database are allowed.
func wrapReplica(path string, remote Store) (Store, error) {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".db")
	if !utils.ReplicaEnabled() {
		replicaMu.Lock()
		replicaOff = append(replicaOff, ReplicaInfo{Name: name, Mode: "off"})
		replicaMu.Unlock()
		return remote, nil
	}
	local, err := openLocalCopy(filepath.Join(replicaDir, base))
	if err != nil {
		return replicaFailed(name, remote, fmt.Errorf("the local copy of %s cannot be opened: %v", name, err))
	}
	r := newReplica(name, remote, local, utils.ReplicaOnDemand(name))
	if !r.lazy {
		if err := r.fill(); err != nil {
			local.Close()
			return replicaFailed(name, remote, err)
		}
	}
	replicaMu.Lock()
	replicaList = append(replicaList, r)
	replicaMu.Unlock()
	logf("persist: %s", r.describe())
	return r, nil
}

// replicaFailed is what happens when the local copy cannot be made: the server stops (a database it
// cannot read is not one to start on), unless files are allowed in place of the databases, when it goes
// on reading the remote database as it did before there were replicas.
func replicaFailed(name string, remote Store, err error) (Store, error) {
	if !utils.DBFallbackToFiles() {
		giveUp("persist: the local copy of the database " + name + " could not be made (" + err.Error() + "). Not starting without it. Set " + utils.EnvReplica + "=off to read the remote database directly, or " + utils.EnvDBFallback + "=files to start anyway.")
	}
	logf("persist: %v; reading %s from the remote database directly", err, name)
	replicaMu.Lock()
	replicaOff = append(replicaOff, ReplicaInfo{Name: name, Mode: "off", Broken: true})
	replicaMu.Unlock()
	return remote, nil
}

// openLocalCopy opens a new, empty file: an earlier copy is only what the database held then.
func openLocalCopy(file string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return nil, err
	}
	for _, f := range []string{file, file + "_unqlite_journal"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return openUnqlite(file)
}

func newReplica(name string, remote, local Store, lazy bool) *replicaStore {
	return &replicaStore{name: name, remote: remote, local: local, lazy: lazy, missed: map[string]time.Time{},
		info: ReplicaInfo{Name: name, Mode: map[bool]string{false: "full", true: "on demand"}[lazy]}}
}

// fill copies every record of the remote database into the local copy.
func (r *replicaStore) fill() error {
	start := time.Now()
	n := 0
	var werr error
	err := r.remote.Each(func(k, v []byte) bool {
		if werr = r.local.Store(append([]byte(nil), k...), append([]byte(nil), v...)); werr != nil {
			return false
		}
		n++
		return true
	})
	if err == nil {
		err = werr
	}
	if err == nil {
		err = r.local.Commit()
	}
	if err != nil {
		return fmt.Errorf("reading %s from the remote database failed after %d records: %v", r.name, n, err)
	}
	r.mu.Lock()
	r.info.Records, r.info.Loaded, r.info.Took = n, time.Now(), time.Since(start)
	r.mu.Unlock()
	return nil
}

func (r *replicaStore) describe() string {
	s := r.status()
	if s.Mode == "on demand" {
		return fmt.Sprintf("%s: kept on demand (each record is read from the remote database the first time it is asked for)", s.Name)
	}
	return fmt.Sprintf("%s: %d records copied from the remote database in %s; reads are local from now on", s.Name, s.Records, s.Took.Round(time.Millisecond))
}

func (r *replicaStore) status() ReplicaInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.info
	i.Broken = r.broken
	return i
}

func (r *replicaStore) isBroken() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.broken
}

// markBroken: the local copy no longer matches the remote database (a write to it failed after the
// remote one worked), so it is not read any more.
func (r *replicaStore) markBroken(err error) {
	r.mu.Lock()
	first := !r.broken
	r.broken = true
	r.mu.Unlock()
	if first {
		logf("ERROR: persist: the local copy of %s could not be written (%v); reading the remote database directly from now on", r.name, err)
	}
}

// ---- Store ---------------------------------------------------------------------

func (r *replicaStore) Backend() string { return r.remote.Backend() }

func (r *replicaStore) Fetch(key []byte) ([]byte, error) {
	if r.isBroken() {
		return r.remote.Fetch(key)
	}
	r.lmu.Lock()
	v, err := r.local.Fetch(key)
	r.lmu.Unlock()
	if err == nil {
		return v, nil
	}
	if !r.lazy {
		return nil, ErrNotFound // all records are here
	}
	return r.fetchRemote(key)
}

// fetchRemote reads a record the on-demand copy does not have yet.
func (r *replicaStore) fetchRemote(key []byte) ([]byte, error) {
	k := string(key)
	r.mu.Lock()
	if t, ok := r.missed[k]; ok && time.Since(t) < replicaMissTTL {
		r.mu.Unlock()
		return nil, ErrNotFound
	}
	r.mu.Unlock()
	v, err := r.remote.Fetch(key)
	if err == ErrNotFound {
		r.mu.Lock()
		if len(r.missed) >= replicaMissKeep {
			r.missed = map[string]time.Time{}
		}
		r.missed[k] = time.Now()
		r.mu.Unlock()
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.lmu.Lock()
	serr := r.local.Store(append([]byte(nil), key...), append([]byte(nil), v...))
	if serr == nil {
		serr = r.local.Commit()
	}
	r.lmu.Unlock()
	if serr != nil {
		r.markBroken(serr)
	}
	return v, nil
}

func (r *replicaStore) Store(key, value []byte) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if err := r.remote.Store(key, value); err != nil {
		return err
	}
	if !r.isBroken() {
		r.lmu.Lock()
		err := r.local.Store(append([]byte(nil), key...), append([]byte(nil), value...))
		r.lmu.Unlock()
		if err != nil {
			r.markBroken(err)
		}
	}
	if r.lazy {
		r.mu.Lock()
		delete(r.missed, string(key))
		r.mu.Unlock()
	}
	return nil
}

func (r *replicaStore) Delete(key []byte) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	rerr := r.remote.Delete(key)
	if rerr != nil && rerr != ErrNotFound {
		return rerr
	}
	r.lmu.Lock()
	r.local.Delete(key) // a key the copy does not have is not an error here
	r.lmu.Unlock()
	return rerr
}

func (r *replicaStore) Commit() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if err := r.remote.Commit(); err != nil {
		return err
	}
	r.lmu.Lock()
	err := r.local.Commit()
	r.lmu.Unlock()
	if err != nil {
		r.markBroken(err)
	}
	return nil
}

func (r *replicaStore) Rollback() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	err := r.remote.Rollback()
	r.lmu.Lock()
	r.local.Rollback()
	r.lmu.Unlock()
	return err
}

// Each lists the records of the local copy (of the remote database, for an on-demand one, which does not
// hold them all). The records are taken first and fn is called after, so fn may use the database.
func (r *replicaStore) Each(fn func(key, value []byte) bool) error {
	if r.lazy || r.isBroken() {
		return r.remote.Each(fn)
	}
	type kv struct{ k, v []byte }
	var all []kv
	r.lmu.Lock()
	err := r.local.Each(func(k, v []byte) bool {
		all = append(all, kv{append([]byte(nil), k...), append([]byte(nil), v...)})
		return true
	})
	r.lmu.Unlock()
	if err != nil {
		return err
	}
	for _, e := range all {
		if !fn(e.k, e.v) {
			break
		}
	}
	return nil
}

func (r *replicaStore) Close() error {
	r.lmu.Lock()
	lerr := r.local.Close()
	r.lmu.Unlock()
	if err := r.remote.Close(); err != nil {
		return err
	}
	return lerr
}
