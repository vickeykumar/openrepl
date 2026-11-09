package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nobonobo/unqlitego"

	"objstore"
)

var ctx = context.Background()

// ---- retention -----------------------------------------------------------------------------------

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func entries(times ...string) []Entry {
	var out []Entry
	for _, s := range times {
		out = append(out, Entry{Key: "k/" + s, At: at(s)})
	}
	return out
}

func keys(es []Entry) string {
	var ks []string
	for _, e := range es {
		ks = append(ks, strings.TrimPrefix(e.Key, "k/"))
	}
	return strings.Join(ks, ",")
}

func TestRetentionKeepsTheNewestOfEachHourAndEachDay(t *testing.T) {
	now := at("2026-11-01 15:00")
	keep, drop := Prune(entries(
		"2026-11-01 14:45", "2026-11-01 14:30", "2026-11-01 14:15", "2026-11-01 14:00", // one hour
		"2026-11-01 13:30", "2026-11-01 13:10", // another
		"2026-10-31 20:00", "2026-10-31 08:00", // yesterday, older than the last day's hours? 19h: hourly
		"2026-10-30 23:50", "2026-10-30 10:00", // two days ago: only the newest of the day
		"2026-10-20 12:00", // older than a week
	), now, "")
	// the newest three, the newest of the 13:00 hour, of yesterday (20:00 is also an hour of the last day) and of two days ago
	if want := "2026-11-01 14:45,2026-11-01 14:30,2026-11-01 14:15,2026-11-01 13:30,2026-10-31 20:00,2026-10-30 23:50"; keys(keep) != want {
		t.Errorf("kept:    %s\nwant:    %s", keys(keep), want)
	}
	if want := "2026-11-01 14:00,2026-11-01 13:10,2026-10-31 08:00,2026-10-30 10:00,2026-10-20 12:00"; keys(drop) != want {
		t.Errorf("dropped: %s\nwant:    %s", keys(drop), want)
	}
}

func TestRetentionNeverDropsTheNewestThreeOrTheOneLatestPointsAt(t *testing.T) {
	now := at("2026-11-01 15:00")
	old := entries("2026-09-01 10:00", "2026-09-01 11:00", "2026-09-01 12:00", "2026-08-01 12:00")
	keep, drop := Prune(old, now, "k/2026-08-01 12:00")
	// a month old, so nothing is kept for its hour or its day: the newest three and the protected one
	if len(keep) != 4 || len(drop) != 0 {
		t.Errorf("kept %s, dropped %s", keys(keep), keys(drop))
	}
	if keep, drop := Prune(entries("2026-11-01 14:45", "2026-11-01 14:30"), now, ""); len(keep) != 2 || len(drop) != 0 {
		t.Errorf("a short list: %s / %s", keys(keep), keys(drop))
	}
	if keep, drop := Prune(nil, now, ""); len(keep) != 0 || len(drop) != 0 {
		t.Error("an empty list")
	}
}

func TestRetentionKeepsAtMostAboutThirtyOne(t *testing.T) {
	now := at("2026-11-01 15:00")
	var es []Entry
	for m := 0; m < 8*24*60; m += 15 { // eight days of a backup every 15 minutes
		ts := now.Add(-time.Duration(m) * time.Minute)
		es = append(es, Entry{Key: fmt.Sprint("k/", ts.Unix()), At: ts})
	}
	keep, _ := Prune(es, now, "")
	if len(keep) < 24 || len(keep) > 33 {
		t.Errorf("kept %d of %d", len(keep), len(es))
	}
}

// ---- archives ------------------------------------------------------------------------------------

func makeDB(t *testing.T, path string, n int) {
	t.Helper()
	db, err := unqlitego.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := db.Store([]byte(fmt.Sprintf("key%d", i)), []byte(fmt.Sprintf("value %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func countDB(t *testing.T, path string) int {
	t.Helper()
	db, err := unqlitego.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cur, err := db.NewCursor()
	if err != nil {
		t.Fatal(err)
	}
	defer cur.Close()
	n := 0
	if cur.First() != nil {
		return 0
	}
	for cur.IsValid() {
		n++
		if cur.Next() != nil {
			break
		}
	}
	return n
}

func serverDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	makeDB(t, filepath.Join(dir, "user_sessions.db"), 5)
	makeDB(t, filepath.Join(dir, "blog.db"), 2)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"maintenance":{}}`), 0600)
	os.WriteFile(filepath.Join(dir, "admin-audit.jsonl"), []byte("{\"a\":1}\n"), 0600)
	os.WriteFile(filepath.Join(dir, "admin-stats.json"), []byte(`{"days":{}}`), 0600)
	os.WriteFile(filepath.Join(dir, "jobfile"), []byte("volatile"), 0600) // not part of a backup
	os.WriteFile(filepath.Join(dir, "blog.db_unqlite_journal"), []byte("j"), 0600)
	return dir
}

func TestSourcesListWhatIsThereAndNothingElse(t *testing.T) {
	dir := serverDir(t)
	if got := strings.Join(Sources(dir, ""), ","); got != "admin-audit.jsonl,admin-stats.json,blog.db,settings.json,user_sessions.db" {
		t.Errorf("files mode: %s", got)
	}
	// with a remote database the .db files are the local copy of it: backed up (a second copy of the remote
	// data), but never restored, and the settings are not in a file
	if got := strings.Join(Sources(dir, "firestore"), ","); got != "admin-audit.jsonl,admin-stats.json,blog.db,user_sessions.db" {
		t.Errorf("remote mode: %s", got)
	}
	if got := strings.Join(RestoreFiles(dir, "firestore"), ","); got != "admin-audit.jsonl,admin-stats.json" {
		t.Errorf("remote mode, what a restore puts back: %s", got)
	}
	if got := strings.Join(RestoreFiles(dir, ""), ","); got != "admin-audit.jsonl,admin-stats.json,blog.db,settings.json,user_sessions.db" {
		t.Errorf("files mode, what a restore puts back: %s", got)
	}
	if got := Sources(t.TempDir(), ""); len(got) != 0 {
		t.Errorf("an empty folder: %v", got)
	}
}

func TestAnArchiveHoldsTheFilesAndAReadBackIsChecked(t *testing.T) {
	dir := serverDir(t)
	data, m, err := Build(dir, "prod", Sources(dir, ""), at("2026-11-01 15:00"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "prod" || len(m.Files) != 5 || m.Fingerprint == "" {
		t.Fatalf("manifest: %+v", m)
	}
	m2, files, err := Open(data)
	if err != nil || len(files) != 5 || m2.Files[0].Name != m.Files[0].Name {
		t.Fatalf("open: %v %v", err, files)
	}
	if string(files["settings.json"]) != `{"maintenance":{}}` {
		t.Errorf("a file's content: %q", files["settings.json"])
	}
	// the copy of a database that is in the archive opens and has its records
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	os.WriteFile(copyPath, files["user_sessions.db"], 0600)
	if n := countDB(t, copyPath); n != 5 {
		t.Errorf("records in the archived database: %d", n)
	}
}

// an archive is made by hand, to be damaged in one way each
func handArchive(t *testing.T, manifest string, files map[string]string, extra func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	put := func(name, data string) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write([]byte(data))
	}
	if manifest != "" {
		put(manifestName, manifest)
	}
	for n, d := range files {
		put(n, d)
	}
	if extra != nil {
		extra(tw)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestAnArchiveThatIsNotWhatItSaysIsRefused(t *testing.T) {
	sum := SHA256([]byte("hello"))
	good := fmt.Sprintf(`{"version":1,"name":"x","files":[{"name":"a.json","size":5,"sha256":"%s"}]}`, sum)
	if _, _, err := Open(handArchive(t, good, map[string]string{"a.json": "hello"}, nil)); err != nil {
		t.Fatalf("a good one: %v", err)
	}
	for name, a := range map[string][]byte{
		"not gzip":          []byte("hello"),
		"no manifest":       handArchive(t, "", map[string]string{"a.json": "hello"}, nil),
		"a changed file":    handArchive(t, good, map[string]string{"a.json": "HELLO"}, nil),
		"a missing file":    handArchive(t, good, nil, nil),
		"a file not listed": handArchive(t, good, map[string]string{"a.json": "hello", "b.json": "x"}, nil),
		"a path":            handArchive(t, strings.Replace(good, "a.json", "../a.json", 1), map[string]string{"../a.json": "hello"}, nil),
		"a folder":          handArchive(t, strings.Replace(good, "a.json", "d/a.json", 1), map[string]string{"d/a.json": "hello"}, nil),
		"a bad manifest":    handArchive(t, `{"version":2}`, nil, nil),
		"a link": handArchive(t, good, map[string]string{"a.json": "hello"}, func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		}),
	} {
		if _, _, err := Open(a); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestADatabaseThatIsNotWholeIsNotArchived(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "blog.db")
	makeDB(t, db, 400)
	whole, _ := os.ReadFile(db)
	for name, content := range map[string][]byte{
		"cut in two":       whole[:len(whole)/2+17],
		"cut mid page":     whole[:len(whole)-100],
		"not a database":   []byte("this is not a database at all, it is some text of a hundred and some characters, enough to look like a file"),
		"zeros":            make([]byte, 8192),
		"a damaged header": append([]byte("XXXXXXXXXXXXXXXX"), whole[16:]...),
	} {
		os.WriteFile(db, content, 0600)
		if _, _, err := Build(dir, "x", []string{"blog.db"}, time.Now()); err == nil || !strings.Contains(err.Error(), "middle of a write") {
			t.Errorf("%s was archived (%v)", name, err)
		}
	}
	os.WriteFile(db, whole, 0600)
	if _, _, err := Build(dir, "x", []string{"blog.db"}, time.Now()); err != nil {
		t.Errorf("a whole one: %v", err)
	}

	// a file that is written while it is copied (the hook writes it between the copy and the check)
	afterCopyHook = func(path string) { os.WriteFile(path, append(whole, make([]byte, unqlitePageSize)...), 0600) }
	defer func() { afterCopyHook = nil }()
	if _, _, err := Build(dir, "x", []string{"blog.db"}, time.Now()); err == nil || !strings.Contains(err.Error(), "changed while it was copied") {
		t.Errorf("a file written during the copy: %v", err)
	}
}

// ---- the job and the restore ---------------------------------------------------------------------

func newBackuper(t *testing.T, dir string, f *objstore.Fake) *Backuper {
	t.Helper()
	cl, err := objstore.New(f.Config())
	if err != nil {
		t.Fatal(err)
	}
	now := at("2026-11-01 15:00")
	b := &Backuper{Dir: dir, Name: func() string { return "prod" }, Remote: func() string { return "" },
		Client: func() (*objstore.Client, error) { return cl, nil }}
	b.Now = func() time.Time { return now }
	return b
}

func TestABackupIsMadeOnlyWhenSomethingChanged(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	dir := serverDir(t)
	b := newBackuper(t, dir, f)

	res, err := b.RunOnce(ctx, false)
	if err != nil || !res.Uploaded || res.Files != 5 || res.Kept != 1 {
		t.Fatalf("first run: %+v %v", res, err)
	}
	stored := f.Stored()
	if len(stored) != 2 || !strings.HasSuffix(stored[1], "/latest.json") || !strings.HasSuffix(stored[0], ".tar.gz") || !strings.HasPrefix(stored[0], "pfx/backups/prod/") {
		t.Fatalf("stored: %v", stored)
	}
	st := b.Status()
	if st.LastOK.IsZero() || st.LastSize == 0 || st.Kept != 1 || st.LastErr != "" || len(st.Files) != 5 {
		t.Errorf("status: %+v", st)
	}

	if res, _ := b.RunOnce(ctx, false); res.Uploaded || res.Note != "nothing changed" {
		t.Errorf("a second run with nothing changed: %+v", res)
	}
	os.WriteFile(filepath.Join(dir, "admin-audit.jsonl"), []byte("{\"a\":1}\n{\"a\":2}\n"), 0600)
	b.Now = func() time.Time { return at("2026-11-01 15:15") }
	if res, _ := b.RunOnce(ctx, false); !res.Uploaded || res.Kept != 2 {
		t.Errorf("after a change: %+v", res)
	}
	if res, _ := b.RunOnce(ctx, true); !res.Uploaded {
		t.Errorf("forced: %+v", res)
	}
}

func TestAnUploadThatDiesLeavesTheLastBackupAsItWas(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	dir := serverDir(t)
	b := newBackuper(t, dir, f)
	if _, err := b.RunOnce(ctx, false); err != nil {
		t.Fatal(err)
	}
	before := f.Stored()
	latestBefore, _ := f.Object("pfx/backups/prod/latest.json")

	os.WriteFile(filepath.Join(dir, "admin-audit.jsonl"), []byte("changed\n"), 0600)
	b.Now = func() time.Time { return at("2026-11-01 15:15") }
	f.CutPuts(true)
	if _, err := b.RunOnce(ctx, false); err == nil {
		t.Fatal("a dead upload was taken for a backup")
	}
	f.CutPuts(false)
	after := f.Stored()
	latestAfter, _ := f.Object("pfx/backups/prod/latest.json")
	if strings.Join(before, ",") != strings.Join(after, ",") || string(latestBefore) != string(latestAfter) {
		t.Errorf("the bucket changed:\n%v\n%v", before, after)
	}
	if st := b.Status(); st.LastErr == "" || st.LastErrAt.IsZero() || st.LastOK.IsZero() {
		t.Errorf("status: %+v", st)
	}
	// and the next run, with the bucket answering, uploads it
	if res, err := b.RunOnce(ctx, false); err != nil || !res.Uploaded {
		t.Errorf("the next run: %+v %v", res, err)
	}
}

func TestPruningWaitsOneRunAndThenDeletes(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	dir := serverDir(t)
	b := newBackuper(t, dir, f)
	// fifteen archives of the same hour, from an earlier time
	for i := 0; i < 15; i++ {
		ts := at("2026-11-01 13:00").Add(time.Duration(i) * time.Minute)
		f.Set("pfx/"+b.archiveKey(ts), []byte("old"), ts)
	}
	res, err := b.RunOnce(ctx, false)
	if err != nil || res.Deleted != 0 || !strings.Contains(res.Note, "nothing deleted") {
		t.Fatalf("the first run of a process: %+v %v", res, err)
	}
	if len(f.Stored()) != 15+2 {
		t.Fatalf("stored after the first run: %d", len(f.Stored()))
	}
	os.WriteFile(filepath.Join(dir, "admin-audit.jsonl"), []byte("changed\n"), 0600)
	b.Now = func() time.Time { return at("2026-11-01 15:15") }
	res, err = b.RunOnce(ctx, false)
	// of the 15 old ones only the newest of the 13:00 hour stays; the newest 3 overall are the new ones and two old ones
	if err != nil || res.Deleted == 0 || res.Kept > 6 {
		t.Fatalf("the second run: %+v %v", res, err)
	}
	if got := len(f.Stored()); got != res.Kept+1 {
		t.Errorf("stored %d, kept %d (+ latest.json)", got, res.Kept)
	}
}

func restoreInto(t *testing.T, f *objstore.Fake, dir string, remote string) (*Restored, error) {
	b := newBackuper(t, dir, f)
	b.Remote = func() string { return remote }
	return b.RestoreIfEmpty(ctx)
}

func TestAnEmptyDiskGetsTheBackupBackAndAFullOneIsLeftAlone(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	src := serverDir(t)
	if _, err := newBackuper(t, src, f).RunOnce(ctx, false); err != nil {
		t.Fatal(err)
	}

	fresh := t.TempDir()
	r, err := restoreInto(t, f, fresh, "")
	if err != nil || r == nil || len(r.Files) != 5 || r.CreatedAt.IsZero() {
		t.Fatalf("restore: %+v %v", r, err)
	}
	if n := countDB(t, filepath.Join(fresh, "user_sessions.db")); n != 5 {
		t.Errorf("restored database: %d records", n)
	}
	if b, _ := os.ReadFile(filepath.Join(fresh, "admin-audit.jsonl")); string(b) != "{\"a\":1}\n" {
		t.Errorf("restored audit log: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(fresh, ".restore-*")); len(left) != 0 {
		t.Errorf("a temporary folder was left: %v", left)
	}
	if _, err := os.Stat(filepath.Join(fresh, "jobfile")); err == nil {
		t.Error("a file that is not part of a backup came back")
	}

	// a server that has its files is not touched
	if r, err := restoreInto(t, f, src, ""); r != nil || err != nil {
		t.Errorf("a folder with files: %+v %v", r, err)
	}
	// a server with one of the files (the audit log, written before the restore ran) is not "empty" either
	partial := t.TempDir()
	os.WriteFile(filepath.Join(partial, "admin-stats.json"), []byte("mine"), 0600)
	if r, _ := restoreInto(t, f, partial, ""); r != nil {
		t.Errorf("a folder with one file: %+v", r)
	}
}

func TestRestoreInFirestoreModeBringsBackOnlyTheAuditLogAndTheTotals(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	src := serverDir(t)
	if _, err := newBackuper(t, src, f).RunOnce(ctx, false); err != nil { // made with the files
		t.Fatal(err)
	}
	fresh := t.TempDir()
	r, err := restoreInto(t, f, fresh, "firestore")
	if err != nil || r == nil || strings.Join(r.Files, ",") != "admin-audit.jsonl,admin-stats.json" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(fresh, "user_sessions.db")); err == nil {
		t.Error("a database came back although Firestore holds them")
	}
}

// The copies of the remote data are in the backup (a second copy of what Firestore holds), and a copy on the
// disk does not stop the small files from being restored.
func TestABackupWithARemoteDatabaseHoldsTheLocalCopyAndRestoreIgnoresIt(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	src := serverDir(t)
	b := newBackuper(t, src, f)
	b.Remote = func() string { return "firestore" }
	res, err := b.RunOnce(ctx, false)
	if err != nil || !res.Uploaded {
		t.Fatalf("backup: %+v %v", res, err)
	}
	// a disk that was wiped but already has a copy of the database in it (this run's start made it)
	fresh := t.TempDir()
	makeDB(t, filepath.Join(fresh, "blog.db"), 1)
	r, err := restoreInto(t, f, fresh, "firestore")
	if err != nil || r == nil || strings.Join(r.Files, ",") != "admin-audit.jsonl,admin-stats.json" {
		t.Fatalf("restore: %+v %v", r, err)
	}
	if n := countDB(t, filepath.Join(fresh, "blog.db")); n != 1 {
		t.Errorf("the copy on the disk was replaced: %d records", n)
	}
	// a server with no remote database that restores this backup gets the copies back: they are the data
	// there (the settings were in the remote database, so they are not in it)
	files := t.TempDir()
	if r, err := restoreInto(t, f, files, ""); err != nil || r == nil || strings.Join(r.Files, ",") != "admin-audit.jsonl,admin-stats.json,blog.db,user_sessions.db" {
		t.Errorf("files mode: %+v %v", r, err)
	}
}

func TestADamagedLatestFallsBackToAnOlderBackup(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	src := serverDir(t)
	b := newBackuper(t, src, f)
	b.RunOnce(ctx, false)
	os.WriteFile(filepath.Join(src, "admin-audit.jsonl"), []byte("newer\n"), 0600)
	b.Now = func() time.Time { return at("2026-11-01 15:15") }
	b.RunOnce(ctx, false)

	// the newest archive is damaged: latest.json names it, the hash does not match
	var newest string
	for _, k := range f.Stored() {
		if strings.HasSuffix(k, "20261101T151500Z.tar.gz") {
			newest = k
		}
	}
	f.Set(newest, []byte("damaged"), at("2026-11-01 15:15"))
	fresh := t.TempDir()
	r, err := restoreInto(t, f, fresh, "")
	if err != nil || r == nil || !strings.HasSuffix(r.Key, "20261101T150000Z.tar.gz") {
		t.Fatalf("%+v %v", r, err)
	}
	if b, _ := os.ReadFile(filepath.Join(fresh, "admin-audit.jsonl")); string(b) != "{\"a\":1}\n" {
		t.Errorf("the older backup's audit log: %q", b)
	}

	// no latest.json at all: the newest archive that is good is found by listing
	f.Set("pfx/backups/prod/latest.json", nil, time.Now())
	ioutilRemove(f)
	fresh2 := t.TempDir()
	if r, err := restoreInto(t, f, fresh2, ""); err != nil || r == nil {
		t.Errorf("without latest.json: %+v %v", r, err)
	}
	// nothing usable: an error, and nothing is written
	for _, k := range f.Stored() {
		if strings.HasSuffix(k, ".tar.gz") {
			f.Set(k, []byte("junk"), at("2026-11-01 15:00"))
		}
	}
	fresh3 := t.TempDir()
	if r, err := restoreInto(t, f, fresh3, ""); err == nil || r != nil {
		t.Errorf("only junk: %+v %v", r, err)
	}
	if entries, _ := ioutil.ReadDir(fresh3); len(entries) != 0 {
		t.Errorf("something was written: %v", entries)
	}
}

func ioutilRemove(f *objstore.Fake) {
	cl, _ := objstore.New(f.Config())
	cl.Delete(ctx, "backups/prod/latest.json")
}

func TestARestoreFromABucketWithNoBackupIsNothing(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	if r, err := restoreInto(t, f, t.TempDir(), ""); r != nil || err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	// without an object store set up
	b := &Backuper{Dir: t.TempDir(), Name: func() string { return "x" }, Remote: func() string { return "" },
		Client: func() (*objstore.Client, error) { return nil, objstore.ErrNotConfigured }}
	if r, err := b.RestoreIfEmpty(ctx); r != nil || err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	if res, err := b.RunOnce(ctx, false); err != nil || res.Uploaded {
		t.Errorf("a run without a store: %+v %v", res, err)
	}
}
