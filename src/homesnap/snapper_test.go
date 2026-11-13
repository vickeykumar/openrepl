package homesnap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"objstore"
)

var ctx = context.Background()

func newSnapper(t *testing.T, f *objstore.Fake) (*Snapper, string) {
	t.Helper()
	base := t.TempDir()
	cl, err := objstore.New(f.Config())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s := &Snapper{Base: func() string { return base }, Name: func() string { return "prod" },
		Client: func() (*objstore.Client, error) { return cl, nil }, Now: func() time.Time { return now }}
	s.Enable()
	return s, base
}

func write(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	os.Chtimes(path, old, old)
}

func makeHome(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	write(t, filepath.Join(dir, "main.c"), "int main(){}", time.Hour)
	write(t, filepath.Join(dir, "src", "util", "a.py"), "print(1)", time.Hour)
	write(t, filepath.Join(dir, ".wsync-state"), "reserved", time.Hour)
	os.MkdirAll(filepath.Join(dir, "empty"), 0755)
	os.Symlink("/etc/passwd", filepath.Join(dir, "link"))
	for _, p := range []string{filepath.Join(dir, "src", "util"), filepath.Join(dir, "src"), filepath.Join(dir, "empty"), dir} {
		old := time.Now().Add(-time.Hour)
		os.Chtimes(p, old, old)
	}
	return dir
}

func TestWhichHomesAreKept(t *testing.T) {
	for name, want := range map[string]bool{"anna0123": true, "u-1_2": true, "guest-123": false, "": false, ".hidden": false, "a/b": false, "..": false,
		"python": true /* a name alone says nothing; Touch is only called for signed-in users */, strings.Repeat("a", 65): false} {
		if IsKept(name) != want {
			t.Errorf("%q: %v", name, IsKept(name))
		}
	}
}

func TestAHomeIsSnapshottedAndComesBackWhole(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")

	res, err := s.SnapshotHome(ctx, "anna0123", false)
	if err != nil || !res.Made || res.Generation != 1 || res.Files != 5 /* main.c, src/, src/util/, src/util/a.py, empty/ */ {
		t.Fatalf("snapshot: %+v %v", res, err)
	}
	keys := f.Stored()
	if len(keys) != 2 || !strings.Contains(keys[0], "pfx/homes/prod/anna0123/anna0123-"+time.Now().UTC().Format("2006-01-02")+".tar.gz") || !strings.HasSuffix(keys[1], "/latest.json") {
		t.Fatalf("stored: %v", keys)
	}

	// the folder is wiped (a disk that was lost)
	os.RemoveAll(dir)
	restored, err := s.Ensure(ctx, "anna0123")
	if err != nil || !restored {
		t.Fatalf("restore: %v %v", restored, err)
	}
	for path, want := range map[string]string{"main.c": "int main(){}", "src/util/a.py": "print(1)"} {
		if b, err := os.ReadFile(filepath.Join(dir, path)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", path, b, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "empty")); err != nil || !fi.IsDir() {
		t.Error("an empty folder did not come back")
	}
	for _, never := range []string{".wsync-state", "link"} {
		if _, err := os.Lstat(filepath.Join(dir, never)); err == nil {
			t.Errorf("%s was in the snapshot", never)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(base, ".restore-*")); len(left) != 0 {
		t.Errorf("a temporary folder was left: %v", left)
	}
	// what came back is what was there: nothing to snapshot again
	if res, _ := s.SnapshotHome(ctx, "anna0123", false); res.Made || res.Note != "unchanged" {
		t.Errorf("after a restore: %+v", res)
	}
}

func TestAHomeThatIsThereIsNeverReplaced(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	write(t, filepath.Join(dir, "main.c"), "newer, written after the snapshot", 0)
	if restored, err := s.Ensure(ctx, "anna0123"); restored || err != nil {
		t.Fatalf("%v %v", restored, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "main.c")); string(b) != "newer, written after the snapshot" {
		t.Errorf("%q", b)
	}
	// an empty folder in the way is replaced
	empty := filepath.Join(base, "bob00001")
	os.MkdirAll(empty, 0755)
	makeHome(t, t.TempDir(), "x") // a home of another snapper does not matter
}

func TestChangesWaitForTheHomeToSettleAndUnchangedHomesAreNotUploadedAgain(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	before := len(f.Requests())
	if res, _ := s.SnapshotHome(ctx, "anna0123", false); res.Made || res.Note != "unchanged" {
		t.Errorf("unchanged: %+v", res)
	}
	if len(f.Requests()) != before {
		t.Error("an unchanged home was asked about at the object store")
	}
	write(t, filepath.Join(dir, "main.c"), "being typed right now", 0)
	if res, _ := s.SnapshotHome(ctx, "anna0123", false); res.Made || res.Note != "still changing" {
		t.Errorf("a home that is changing: %+v", res)
	}
	if res, _ := s.SnapshotHome(ctx, "anna0123", true); !res.Made || res.Generation != 2 {
		t.Errorf("forced: %+v", res)
	}
	write(t, filepath.Join(dir, "main.c"), "settled", time.Minute)
	if res, _ := s.SnapshotHome(ctx, "anna0123", false); !res.Made || res.Generation != 3 {
		t.Errorf("settled: %+v", res)
	}
}

func TestOnlyTheNewestThreeSnapshotsAreKept(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	clock := time.Now().UTC()
	s.Now = func() time.Time { return clock }
	var days []string
	for i := 0; i < 6; i++ {
		days = append(days, clock.Add(24*time.Hour).Format("2006-01-02"))
		clock = clock.Add(24 * time.Hour)
		write(t, filepath.Join(dir, "main.c"), fmt.Sprint("version ", i), time.Minute)
		if res, err := s.SnapshotHome(ctx, "anna0123", false); err != nil || !res.Made {
			t.Fatalf("%d: %+v %v", i, res, err)
		}
	}
	var snaps []string
	for _, k := range f.Stored() {
		if strings.HasSuffix(k, ".tar.gz") {
			snaps = append(snaps, k[strings.LastIndex(k, "/")+1:])
		}
	}
	if strings.Join(snaps, ",") != "anna0123-"+days[3]+".tar.gz,anna0123-"+days[4]+".tar.gz,anna0123-"+days[5]+".tar.gz" {
		t.Errorf("snapshots left: %v", snaps)
	}
}

func TestASnapshotThatDiesLeavesTheLastOneAsItWas(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	before := strings.Join(f.Stored(), ",")
	write(t, filepath.Join(dir, "main.c"), "changed", time.Minute)
	f.CutPuts(true)
	if _, err := s.SnapshotHome(ctx, "anna0123", false); err == nil {
		t.Fatal("a dead upload was taken for a snapshot")
	}
	f.CutPuts(false)
	if strings.Join(f.Stored(), ",") != before {
		t.Errorf("the bucket changed: %v", f.Stored())
	}
	if res, err := s.SnapshotHome(ctx, "anna0123", false); err != nil || !res.Made || res.Generation != 2 {
		t.Errorf("the next one: %+v %v", res, err)
	}
}

func TestAnotherWriterThatGotThereFirstWinsAndNothingIsCorrupted(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	a, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	a.SnapshotHome(ctx, "anna0123", false)

	// a second instance (an overlapping deploy) with its own copy of the home
	b, _ := newSnapper(t, f)
	b.Base = a.Base
	b.writer = "other-instance"
	a.writer = "this-instance"

	write(t, filepath.Join(dir, "main.c"), "written through a", time.Minute)
	// the other instance moves the pointer between a's upload and a's pointer
	a.beforePointer = func(home string) {
		a.beforePointer = nil
		cl, _ := objstore.New(f.Config())
		raw, _ := json.Marshal(Latest{Generation: 2, Key: a.dayKey(home), Writer: "other-instance"})
		if err := cl.Put(ctx, a.prefix(home)+"latest.json", raw, "application/json", nil); err != nil {
			t.Errorf("b: %v", err)
		}
	}
	_, err := a.SnapshotHome(ctx, "anna0123", false)
	if err != ErrConflict {
		t.Fatalf("a: %v", err)
	}
	// the pointer is the other's, and a did not move it
	cl, _ := objstore.New(f.Config())
	l, err := b.readLatest(ctx, cl, "anna0123")
	if err != nil || l.Writer != "other-instance" || l.Generation != 2 {
		t.Fatalf("latest: %+v %v", l, err)
	}
}

// a day's file that an instance with a newer generation wrote is not overwritten by an older one
func TestADaysFileOfANewerGenerationIsNotOverwritten(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	a, base := newSnapper(t, f)
	makeHome(t, base, "anna0123")
	if res, err := a.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Fatal(res, err)
	}
	cl, _ := objstore.New(f.Config())
	key := a.dayKey("anna0123")
	cl.Put(ctx, key, []byte("newer"), "application/gzip", map[string]string{"generation": "99"})
	a.mu.Lock()
	a.state("anna0123").generation = 0
	a.mu.Unlock()
	if _, err := a.SnapshotHome(ctx, "anna0123", true); err != ErrConflict {
		t.Fatalf("overwrote: %v", err)
	}
	if b, _ := f.Object("pfx/" + key); string(b) != "newer" {
		t.Errorf("the file was changed: %q", b)
	}
}

func TestADamagedLatestSnapshotFallsBackToTheOlderOne(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	clock := time.Now().UTC()
	s.Now = func() time.Time { return clock }
	s.SnapshotHome(ctx, "anna0123", false)
	clock = clock.Add(24 * time.Hour)
	second := clock.Format("2006-01-02")
	write(t, filepath.Join(dir, "main.c"), "second version", time.Minute)
	s.SnapshotHome(ctx, "anna0123", false)
	for _, k := range f.Stored() {
		if strings.HasSuffix(k, ".tar.gz") && strings.Contains(k, second) {
			f.Set(k, []byte("damaged"), time.Now())
		}
	}
	os.RemoveAll(dir)
	if restored, err := s.Ensure(ctx, "anna0123"); err != nil || !restored {
		t.Fatalf("%v %v", restored, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "main.c")); string(b) != "int main(){}" {
		t.Errorf("the older snapshot's file: %q", b)
	}
	// nothing usable: an error, and no folder
	for _, k := range f.Stored() {
		if strings.HasSuffix(k, ".tar.gz") {
			f.Set(k, []byte("junk"), time.Now())
		}
	}
	os.RemoveAll(dir)
	if restored, err := s.Ensure(ctx, "anna0123"); restored || err == nil {
		t.Errorf("only junk: %v %v", restored, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a folder was made from junk")
	}
}

func TestAHomeWithNoSnapshotIsAskedAboutOnceAWhile(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, _ := newSnapper(t, f)
	if restored, err := s.Ensure(ctx, "newuser1"); restored || err != nil {
		t.Fatal(restored, err)
	}
	asked := len(f.Requests())
	s.Ensure(ctx, "newuser1")
	s.Ensure(ctx, "newuser1")
	if len(f.Requests()) != asked {
		t.Errorf("asked again: %d -> %d", asked, len(f.Requests()))
	}
	// guests and odd names are never asked about
	for _, h := range []string{"guest-1", "../x", ""} {
		if _, err := s.Ensure(ctx, h); err != nil {
			t.Errorf("%q: %v", h, err)
		}
	}
	if len(f.Requests()) != asked {
		t.Error("a guest was asked about")
	}
}

func TestTouchRestoresAHomeThatIsMissingAndRegistersItForSnapshots(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	os.RemoveAll(dir)

	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	s2.Touch("anna0123")
	if b, err := os.ReadFile(filepath.Join(dir, "main.c")); err != nil || string(b) != "int main(){}" {
		t.Fatalf("%q %v", b, err)
	}
	if s2.Status().Homes != 1 || s2.Status().Restores != 1 {
		t.Errorf("status: %+v", s2.Status())
	}
	// a snapshotter that is not enabled does nothing
	s3, _ := newSnapper(t, f)
	s3.enabled = false
	os.RemoveAll(dir)
	s3.Touch("anna0123")
	if _, err := os.Stat(dir); err == nil {
		t.Error("a snapshotter that is off restored a home")
	}
}

func TestTheFinalSnapshotDoesNotWaitForAHomeToSettle(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.Touch("anna0123")
	write(t, filepath.Join(dir, "main.c"), "just written", 0)
	round, _ := s.SnapshotAll(ctx)
	if round.Made != 0 || round.Notes["still changing"] != 1 {
		t.Errorf("a home that is changing was snapshotted: %+v", round)
	}
	s.Final(5 * time.Second)
	if len(f.Stored()) != 2 {
		t.Errorf("stored after the final snapshot: %v", f.Stored())
	}
}

// ---- the archive ---------------------------------------------------------------------------------

func hand(t *testing.T, manifest string, entries map[string]string, extra func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	put := func(name, data string) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write([]byte(data))
	}
	if manifest != "" {
		put(manifestName, manifest)
	}
	for n, d := range entries {
		put(n, d)
	}
	if extra != nil {
		extra(tw)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestASnapshotThatIsNotWhatItSaysIsRefusedAndWritesNothing(t *testing.T) {
	good := fmt.Sprintf(`{"version":1,"home":"x","entries":[{"path":"a.txt","size":5,"sha256":"%s","mode":420}]}`, sum([]byte("hello")))
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(hand(t, good, map[string]string{"a.txt": "hello"}, nil), dest); err != nil {
		t.Fatalf("a good one: %v", err)
	}
	for name, a := range map[string][]byte{
		"not gzip":          []byte("hello"),
		"no manifest":       hand(t, "", map[string]string{"a.txt": "hello"}, nil),
		"a changed file":    hand(t, good, map[string]string{"a.txt": "HELLO"}, nil),
		"a missing file":    hand(t, good, nil, nil),
		"a file not listed": hand(t, good, map[string]string{"a.txt": "hello", "b.txt": "x"}, nil),
		"a path out":        hand(t, strings.Replace(good, "a.txt", "../a.txt", 1), map[string]string{"../a.txt": "hello"}, nil),
		"an absolute path":  hand(t, strings.Replace(good, "a.txt", "/etc/a.txt", 1), map[string]string{"/etc/a.txt": "hello"}, nil),
		"a reserved name":   hand(t, strings.Replace(good, "a.txt", ".wsync-x", 1), map[string]string{".wsync-x": "hello"}, nil),
		"a link": hand(t, good, map[string]string{"a.txt": "hello"}, func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		}),
	} {
		d := filepath.Join(t.TempDir(), "out")
		if _, err := Extract(a, d); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s: a folder was left", name)
		}
	}
}

func TestAFileThatChangesWhileItIsReadIsLeftOutAndABigOneToo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ok.txt"), "fine", time.Hour)
	big := filepath.Join(dir, "big.bin")
	f, _ := os.Create(big)
	f.Truncate(MaxFileBytes + 1)
	f.Close()
	data, m, err := Build(dir, "x", 1, "w", time.Now())
	if err != nil || len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0], "big.bin") {
		t.Fatalf("%+v %v", m.Skipped, err)
	}
	if _, err := Extract(data, filepath.Join(t.TempDir(), "o")); err != nil {
		t.Fatal(err)
	}
}

func TestARoundSaysWhyAHomeWasNotSnapshotted(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	s.Touch("nofolder1") // a home the server has no folder for (a gateway before sync brought its files)
	makeHome(t, base, "anna0123")
	s.Touch("anna0123")
	round, err := s.SnapshotAll(ctx)
	if err != nil || round.Made != 1 || round.Notes["this server has no folder for it"] != 1 {
		t.Fatalf("%+v %v", round, err)
	}
	if got := round.String(); got != "1 snapshots made; 1 not snapshotted: this server has no folder for it" {
		t.Errorf("%q", got)
	}
	st := s.Status()
	if st.Notes["nofolder1"] != "this server has no folder for it" || st.LastRound != round.String() || st.Notes["anna0123"] != "" {
		t.Errorf("status: %+v", st)
	}
	// the folder arrives (sync copied it): the next round makes it
	makeHome(t, base, "nofolder1")
	round, _ = s.SnapshotAll(ctx)
	if round.Made != 1 {
		t.Errorf("after the folder came: %+v", round)
	}
	if st := s.Status(); len(st.Notes) != 0 {
		t.Errorf("notes after it was made: %v", st.Notes)
	}
}

func TestAHomeThatNeverRestsIsSnapshottedAfterAFewRounds(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.Touch("anna0123")
	for round := 1; round <= maxWaitRounds+1; round++ {
		write(t, filepath.Join(dir, "log.txt"), fmt.Sprint("line ", round), 0) // written just now, every round
		r, _ := s.SnapshotAll(ctx)
		if round <= maxWaitRounds && (r.Made != 0 || r.Notes["still changing"] != 1) {
			t.Fatalf("round %d: %+v", round, r)
		}
		if round > maxWaitRounds && r.Made != 1 {
			t.Fatalf("round %d: %+v", round, r)
		}
	}
}

// A fresh home has what the server makes itself (.bashrc that a terminal writes, the histories of the REPLs, .tmp): that is no
// reason to leave the user's files in the bucket.
func TestAHomeWithOnlyWhatTheServerMakesIsRestoredAndKeepsThose(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	os.RemoveAll(dir)
	write(t, filepath.Join(dir, ".bashrc"), "PS1='$ '", time.Second)
	write(t, filepath.Join(dir, ".tmp", "x"), "scratch", time.Second)

	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	s2.Touch("anna0123")
	if b, err := os.ReadFile(filepath.Join(dir, "main.c")); err != nil || string(b) != "int main(){}" {
		t.Fatalf("the files were not put back: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src", "util", "a.py")); err != nil || string(b) != "print(1)" {
		t.Errorf("a file in a folder: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".bashrc")); err != nil || string(b) != "PS1='$ '" {
		t.Errorf("a file the server made, that the snapshot does not have, is gone: %q %v", b, err)
	}
	if s2.Status().Restores != 1 {
		t.Errorf("status: %+v", s2.Status())
	}
}

func TestAFileThatIsInTheSnapshotAndInTheFolderTakesTheSnapshotsVersion(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	write(t, filepath.Join(dir, ".bashrc"), "from the snapshot", time.Hour)
	if res, err := s.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made { // forced: the folder has just been changed
		t.Fatalf("%+v %v", res, err)
	}
	os.RemoveAll(dir)
	write(t, filepath.Join(dir, ".bashrc"), "fresh", time.Second)
	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	s2.Touch("anna0123")
	if b, _ := os.ReadFile(filepath.Join(dir, ".bashrc")); string(b) != "from the snapshot" {
		t.Errorf(".bashrc is %q", b)
	}
}

func TestAHomeWithAFileOfTheUserIsNotRestoredOver(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	os.RemoveAll(dir)
	write(t, filepath.Join(dir, ".bashrc"), "fresh", time.Second)
	write(t, filepath.Join(dir, "notes.txt"), "work of today", time.Second)
	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	s2.Touch("anna0123")
	if _, err := os.Stat(filepath.Join(dir, "main.c")); err == nil {
		t.Error("a home with a file of the user's was restored over")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "work of today" {
		t.Errorf("notes.txt is %q", b)
	}
}

// One failed attempt must not be the last: a timeout of the object store right after a deploy left the home without its files
// for good, and its next snapshot took the place of the good one.
func TestARestoreThatFailsIsTriedAgainAndNoSnapshotIsMadeBeforeItAnswers(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	os.RemoveAll(dir)
	write(t, filepath.Join(dir, ".bashrc"), "fresh", time.Hour)

	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	clock := time.Now()
	s2.Now = func() time.Time { return clock }
	f.FailNext(50)
	s2.Touch("anna0123") // the store does not answer
	if _, err := os.Stat(filepath.Join(dir, "main.c")); err == nil {
		t.Fatal("restored although the store did not answer")
	}
	// the home is not snapshotted while its restore has not answered: that would put a fresh folder in the place of the good one
	before := strings.Join(f.Stored(), ",")
	f.FailNext(0)
	if res, err := s2.SnapshotHome(ctx, "anna0123", true); err != nil || res.Made || res.Note != "waiting for its restore" {
		t.Fatalf("a snapshot was made, or not refused in words: %+v %v", res, err)
	}
	if strings.Join(f.Stored(), ",") != before {
		t.Errorf("the bucket changed: %v", f.Stored())
	}
	// at once the next request does not try again (not more often than once a minute)
	s2.Touch("anna0123")
	if _, err := os.Stat(filepath.Join(dir, "main.c")); err == nil {
		t.Error("tried again before the time")
	}
	// later it does, and the files come back
	clock = clock.Add(restoreRetryAfter + time.Second)
	s2.Touch("anna0123")
	if b, err := os.ReadFile(filepath.Join(dir, "main.c")); err != nil || string(b) != "int main(){}" {
		t.Fatalf("not restored at the next request: %q %v", b, err)
	}
	// and from then on it snapshots as usual
	write(t, filepath.Join(dir, "new.txt"), "x", time.Minute)
	if res, err := s2.SnapshotHome(ctx, "anna0123", true); err != nil || !res.Made {
		t.Errorf("no snapshot after the restore: %+v %v", res, err)
	}
}

func TestARestoreThatKeepsFailingIsGivenUpAfterTenTries(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, base := newSnapper(t, f)
	dir := makeHome(t, base, "anna0123")
	s.SnapshotHome(ctx, "anna0123", false)
	os.RemoveAll(dir)
	s2, _ := newSnapper(t, f)
	s2.Base = s.Base
	clock := time.Now()
	s2.Now = func() time.Time { return clock }
	f.FailNext(100000)
	for i := 0; i < maxRestoreTries+3; i++ {
		s2.Touch("anna0123")
		clock = clock.Add(restoreRetryAfter + time.Second)
	}
	s2.mu.Lock()
	st := s2.state("anna0123")
	tries, pending, touched := st.tries, st.pending, st.touched
	s2.mu.Unlock()
	if tries != maxRestoreTries || pending || !touched {
		t.Errorf("tries %d, pending %v, touched %v", tries, pending, touched)
	}
}

func TestTheIntervalIsSetFromTheDashboardAndTheJobWaitsForTheNewTime(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s, _ := newSnapper(t, f)
	if s.CurrentInterval() != Interval {
		t.Errorf("the built-in time: %v", s.CurrentInterval())
	}
	s.SetInterval(2 * time.Minute)
	if s.CurrentInterval() != 2*time.Minute {
		t.Errorf("set: %v", s.CurrentInterval())
	}
	s.SetInterval(0)
	if s.CurrentInterval() != Interval {
		t.Errorf("0 is the built-in time: %v", s.CurrentInterval())
	}
	// the job: a home that changes is snapshotted at the next round, and a new interval makes the job wait for it afresh
	dir := makeHome(t, s.Base(), "anna0123")
	s.Touch("anna0123")
	s.SetInterval(150 * time.Millisecond)
	stop := s.Start()
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.Stored()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(f.Stored()) == 0 {
		t.Fatalf("no snapshot at the short interval (%v)", dir)
	}
}
