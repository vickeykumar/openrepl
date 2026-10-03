package wsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Record is the state both sides last agreed on, for one home and one peer.
// Every decision is made by comparing the current files with it: it tells a
// file that was deleted from one that was never there, and an echo of a
// change just applied from a real change.
//
// A record is updated only after the peer has acknowledged a change, so it
// is never ahead of what both sides really have.
type Record struct {
	Home string `json:"home"`
	Peer string `json:"peer"`
	// Valid is false when no record was found. A reconcile with no record is
	// a union of both sides and deletes nothing.
	Valid   bool             `json:"-"`
	Entries map[string]Entry `json:"entries"`
}

// NewRecord returns an empty record that is not Valid.
func NewRecord(home, peer string) *Record {
	return &Record{Home: home, Peer: peer, Entries: make(map[string]Entry)}
}

// Get returns the agreed entry for path.
func (r *Record) Get(path string) (Entry, bool) {
	e, ok := r.Entries[path]
	return e, ok
}

// Set records e as agreed.
func (r *Record) Set(e Entry) {
	r.Entries[e.Path] = e
	r.Valid = true
}

// Delete forgets path and, if it was a directory, everything under it.
func (r *Record) Delete(path string) {
	delete(r.Entries, path)
	for p := range r.Entries {
		if hasPrefixPath(p, path) {
			delete(r.Entries, p)
		}
	}
	r.Valid = true
}

// Rename moves an agreed entry to a new path.
func (r *Record) Rename(from, to string) {
	if e, ok := r.Entries[from]; ok {
		delete(r.Entries, from)
		e.Path = to
		r.Entries[to] = e
	}
	r.Valid = true
}

// Digest summarizes the agreed state: a hash over every entry, in path order.
// The two sides of a conversation compare digests before they trust their
// records. Records only agree if both were updated together; if they differ
// (one side lost its record, or a crash left them apart) each side falls back
// to an empty record, which makes the reconcile a union that deletes nothing.
func (r *Record) Digest() string {
	if r == nil || !r.Valid {
		return ""
	}
	h := sha256.New()
	paths := make([]string, 0, len(r.Entries))
	for p := range r.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		e := r.Entries[p]
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%o\x00%s\x00%s\n", p, e.Type, e.Size, e.Mode, e.Hash, e.Link)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RecordStore keeps records as files: <dir>/<peer>/<home>.json.
type RecordStore struct {
	Dir string
}

func (s RecordStore) path(peer, home string) (string, error) {
	if err := ValidHomeName(peer); err != nil {
		return "", err
	}
	if err := ValidHomeName(home); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, peer, home+".json"), nil
}

// Load reads the record for a home and peer. A missing file gives an empty
// record that is not Valid; a damaged file is an error and is not replaced
// silently, because an empty record would turn a bug into deletions.
func (s RecordStore) Load(peer, home string) (*Record, error) {
	p, err := s.path(peer, home)
	if err != nil {
		return nil, err
	}
	data, err := ioutil.ReadFile(p)
	if os.IsNotExist(err) {
		return NewRecord(home, peer), nil
	}
	if err != nil {
		return nil, err
	}
	r := NewRecord(home, peer)
	if err := json.Unmarshal(data, r); err != nil {
		return nil, errors.New("wsync: damaged record " + p + ": " + err.Error())
	}
	if r.Entries == nil {
		r.Entries = make(map[string]Entry)
	}
	r.Home, r.Peer, r.Valid = home, peer, true
	return r, nil
}

// Save writes the record atomically: a temporary file in the same directory,
// flushed to disk, then renamed over the old one.
func (s RecordStore) Save(r *Record) error {
	p, err := s.path(r.Peer, r.Home)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := ioutil.TempFile(dir, "."+strings.TrimSuffix(filepath.Base(p), ".json")+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return err
	}
	// Make the rename itself durable.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	r.Valid = true
	return nil
}

// Remove deletes the record of a home.
func (s RecordStore) Remove(peer, home string) error {
	p, err := s.path(peer, home)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
