package wsync

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryEqual(t *testing.T) {
	base := Entry{Path: "a", Type: File, Size: 3, Mode: 0644, Hash: "h", ModTime: 1}
	same := base
	same.Path, same.ModTime = "other", 999 // the path and the time do not count
	if !base.Equal(same) {
		t.Fatal("entries differing only in path and time should be equal")
	}
	for name, mod := range map[string]func(*Entry){
		"hash": func(e *Entry) { e.Hash = "x" },
		"size": func(e *Entry) { e.Size = 4 },
		"mode": func(e *Entry) { e.Mode = 0755 },
		"type": func(e *Entry) { e.Type = Dir },
	} {
		o := base
		mod(&o)
		if base.Equal(o) {
			t.Errorf("entries differing in %s were equal", name)
		}
	}
	l1 := Entry{Type: Symlink, Link: "a", Mode: 0777}
	l2 := Entry{Type: Symlink, Link: "b", Mode: 0777}
	if l1.Equal(l2) || !l1.Equal(l1) {
		t.Fatal("symlink equality must compare targets")
	}
	if !eq(nil, nil) || eq(&base, nil) || eq(nil, &base) {
		t.Fatal("absent entries equal only each other")
	}
}

func TestRecordMissingIsNotValid(t *testing.T) {
	s := RecordStore{Dir: t.TempDir()}
	r, err := s.Load("worker-1", "guest-a")
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid || len(r.Entries) != 0 {
		t.Fatalf("a missing record must be empty and not valid: %+v", r)
	}
}

func TestRecordSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := RecordStore{Dir: dir}
	r := NewRecord("guest-a", "worker-1")
	r.Set(Entry{Path: "a.c", Type: File, Size: 5, Mode: 0644, Hash: "h", ModTime: 7})
	r.Set(Entry{Path: "d", Type: Dir, Mode: 0755})
	r.Set(Entry{Path: "l", Type: Symlink, Link: "a.c", Mode: 0777})
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("worker-1", "guest-a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Valid || len(got.Entries) != 3 || got.Entries["a.c"] != r.Entries["a.c"] || got.Entries["l"].Link != "a.c" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	// Nothing but the record is left in the directory.
	files, _ := ioutil.ReadDir(filepath.Join(dir, "worker-1"))
	if len(files) != 1 || files[0].Name() != "guest-a.json" {
		t.Fatalf("directory holds %v", files)
	}
}

func TestRecordSaveReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	s := RecordStore{Dir: dir}
	r := NewRecord("h", "p")
	r.Set(Entry{Path: "a", Type: File, Hash: "1"})
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	r.Set(Entry{Path: "b", Type: File, Hash: "2"})
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load("p", "h")
	if len(got.Entries) != 2 {
		t.Fatalf("second save not visible: %+v", got.Entries)
	}
}

func TestRecordDamagedFileIsAnErrorNotAnEmptyRecord(t *testing.T) {
	dir := t.TempDir()
	s := RecordStore{Dir: dir}
	os.MkdirAll(filepath.Join(dir, "p"), 0700)
	ioutil.WriteFile(filepath.Join(dir, "p", "h.json"), []byte("{not json"), 0600)
	if _, err := s.Load("p", "h"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a damaged record must not load as empty (that would turn a bug into deletions): %v", err)
	}
}

func TestRecordNamesAreValidated(t *testing.T) {
	s := RecordStore{Dir: t.TempDir()}
	for _, bad := range []string{"", ".", "..", "a/b", "../x"} {
		if _, err := s.Load(bad, "h"); err == nil {
			t.Errorf("peer %q accepted", bad)
		}
		if _, err := s.Load("p", bad); err == nil {
			t.Errorf("home %q accepted", bad)
		}
	}
}

func TestRecordDeleteRemovesChildren(t *testing.T) {
	r := NewRecord("h", "p")
	for _, p := range []string{"d", "d/a", "d/sub", "d/sub/b", "dx", "other"} {
		r.Set(Entry{Path: p, Type: File})
	}
	r.Delete("d")
	if len(r.Entries) != 2 || r.Entries["dx"].Path == "" || r.Entries["other"].Path == "" {
		t.Fatalf("Delete(d) must remove d and what is under it, not dx: %v", r.Entries)
	}
}

func TestRecordRename(t *testing.T) {
	r := NewRecord("h", "p")
	r.Set(Entry{Path: "a", Type: File, Hash: "x"})
	r.Rename("a", "b")
	if _, ok := r.Get("a"); ok {
		t.Fatal("old path still recorded")
	}
	if e, ok := r.Get("b"); !ok || e.Hash != "x" || e.Path != "b" {
		t.Fatalf("new path = %+v, %v", e, ok)
	}
}

func TestRecordRemove(t *testing.T) {
	s := RecordStore{Dir: t.TempDir()}
	r := NewRecord("h", "p")
	r.Set(Entry{Path: "a", Type: File})
	s.Save(r)
	if err := s.Remove("p", "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("p", "h"); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
	got, _ := s.Load("p", "h")
	if got.Valid {
		t.Fatal("record survived Remove")
	}
}

func TestRecordDigest(t *testing.T) {
	a, b := NewRecord("h", "p"), NewRecord("h", "p")
	if a.Digest() != "" {
		t.Fatal("a record that is not valid has no digest")
	}
	a.Set(Entry{Path: "x", Type: File, Size: 1, Mode: 0644, Hash: "h1", ModTime: 1})
	a.Set(Entry{Path: "d", Type: Dir, Mode: 0755})
	// Same entries, set in another order and with other times and peers.
	b2 := NewRecord("h2", "p2")
	b2.Set(Entry{Path: "d", Type: Dir, Mode: 0755, ModTime: 99})
	b2.Set(Entry{Path: "x", Type: File, Size: 1, Mode: 0644, Hash: "h1", ModTime: 7})
	if a.Digest() == "" || a.Digest() != b2.Digest() {
		t.Fatalf("equal state must give equal digests: %q vs %q", a.Digest(), b2.Digest())
	}
	b2.Set(Entry{Path: "x", Type: File, Size: 1, Mode: 0644, Hash: "h2"})
	if a.Digest() == b2.Digest() {
		t.Fatal("different content must give different digests")
	}
	_ = b
	b2.Delete("x")
	if a.Digest() == b2.Digest() {
		t.Fatal("a missing entry must change the digest")
	}
}
