//go:build linux
// +build linux

package wsync

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This file holds a reference implementation of a reconcile between two
// homes in one process. It does what the conversation layer will do over a
// stream, but copies directly, so the comparison, the apply operations and
// the record updates can be tested together.

// node is one side of a pair: a home, its record and its role.
type node struct {
	name string
	h    *Home
	root string
	rec  *Record
	isGW bool
	// loose makes the helpers tolerate errors caused by the sync working in
	// the same directory at the same time.
	loose bool
}

func newNode(t *testing.T, name string, isGW bool) *node {
	h, root := newHome(t)
	return &node{name: name, h: h, root: root, rec: NewRecord("guest-test", "peer"), isGW: isGW}
}

func (n *node) scan(t *testing.T) map[string]Entry { return mustScan(t, n.h, n.rec) }

// pair is a gateway and a worker holding the same home.
type pair struct {
	t         *testing.T
	gw, wk    *node
	offset    time.Duration // worker clock minus gateway clock
	transfers int           // file contents copied, to check renames move no data
}

func newPair(t *testing.T) *pair {
	return &pair{t: t, gw: newNode(t, "gateway", true), wk: newNode(t, "worker", false)}
}

func mirrorKind(k ActionKind) ActionKind {
	switch k {
	case TakeRemote:
		return GiveLocal
	case GiveLocal:
		return TakeRemote
	case RenameLocal:
		return RenameRemote
	case RenameRemote:
		return RenameLocal
	}
	return k
}

// sync reconciles the pair once and returns the actions each side computed.
func (p *pair) sync() (gwActs, wkActs []Action) {
	t := p.t
	t.Helper()
	// The records are updated together, so they must agree. (The conversation
	// layer enforces this by comparing digests.)
	if p.gw.rec.Digest() != p.wk.rec.Digest() {
		t.Fatalf("the two records disagree before a reconcile:\n gateway %v\n worker  %v", sortedPaths(p.gw.rec.Entries), sortedPaths(p.wk.rec.Entries))
	}
	gwScan, wkScan := p.gw.scan(t), p.wk.scan(t)
	gwActs = Compare(Params{Base: p.gw.rec, Local: gwScan, Remote: wkScan, LocalIsGateway: true, Offset: p.offset})
	wkActs = Compare(Params{Base: p.wk.rec, Local: wkScan, Remote: gwScan, LocalIsGateway: false, Offset: p.offset})

	// Both sides must reach mirror-image decisions.
	g, w := map[string]Action{}, map[string]Action{}
	for _, a := range gwActs {
		g[a.Path] = a
	}
	for _, a := range wkActs {
		w[a.Path] = a
	}
	if len(g) != len(w) {
		t.Fatalf("the sides found different paths to act on:\n gateway %v\n worker  %v", gwActs, wkActs)
	}
	for path, a := range g {
		if b, ok := w[path]; !ok || mirrorKind(a.Kind) != b.Kind || a.From != b.From {
			t.Fatalf("%s: gateway says %v, worker says %v; they must mirror each other", path, a, b)
		}
	}

	gwOK := p.applyTakes(p.gw, p.wk, gwActs)
	wkOK := p.applyTakes(p.wk, p.gw, wkActs)
	// The giving side records a change once the receiving side has applied it.
	p.recordGives(p.gw, gwActs, wkOK)
	p.recordGives(p.wk, wkActs, gwOK)
	return gwActs, wkActs
}

// applyTakes performs the actions in which n takes from src.
func (p *pair) applyTakes(n, src *node, acts []Action) map[string]bool {
	t := p.t
	t.Helper()
	ok := map[string]bool{}
	for _, a := range acts {
		switch a.Kind {
		case Agree:
			if a.Local != nil {
				n.rec.Set(*a.Local)
			} else {
				n.rec.Delete(a.Path)
			}
			ok[a.Path] = true
		case TakeRemote:
			if a.Remote == nil {
				switch err := n.h.Remove(a.Path); err {
				case nil:
					n.rec.Delete(a.Path)
					ok[a.Path] = true
				case ErrNotEmpty:
					// Kept because something under it was changed elsewhere.
				default:
					t.Fatalf("%s: delete %s: %v", n.name, a.Path, err)
				}
				continue
			}
			p.copyEntry(n, src, *a.Remote)
			n.rec.Set(*a.Remote)
			ok[a.Path] = true
		case RenameLocal:
			if err := n.h.Rename(a.From, a.Path); err != nil {
				t.Fatalf("%s: rename %s -> %s: %v", n.name, a.From, a.Path, err)
			}
			n.h.SetTime(a.Path, a.Remote.ModTime)
			n.rec.Rename(a.From, a.Path)
			n.rec.Set(*a.Remote)
			ok[a.Path] = true
		}
	}
	return ok
}

// recordGives updates the record of the side that gave, for every change the
// other side applied.
func (p *pair) recordGives(n *node, acts []Action, peerOK map[string]bool) {
	for _, a := range acts {
		if !peerOK[a.Path] {
			continue
		}
		switch a.Kind {
		case GiveLocal:
			if a.Local != nil {
				n.rec.Set(*a.Local)
			} else {
				n.rec.Delete(a.Path)
			}
		case RenameRemote:
			n.rec.Rename(a.From, a.Path)
			n.rec.Set(*a.Local)
		}
	}
}

// copyEntry makes dst's copy of e equal to src's.
func (p *pair) copyEntry(dst, src *node, e Entry) {
	t := p.t
	t.Helper()
	switch e.Type {
	case Dir:
		if err := dst.h.PutDir(e); err != nil {
			t.Fatalf("%s: PutDir %s: %v", dst.name, e.Path, err)
		}
	case Symlink:
		if err := dst.h.PutSymlink(e); err != nil {
			t.Fatalf("%s: PutSymlink %s: %v", dst.name, e.Path, err)
		}
	case File:
		f, err := src.h.OpenRead(e.Path)
		if err != nil {
			t.Fatalf("%s: open %s: %v", src.name, e.Path, err)
		}
		defer f.Close()
		if err := dst.h.WriteFile(e, f); err != nil {
			t.Fatalf("%s: WriteFile %s: %v", dst.name, e.Path, err)
		}
		p.transfers++
	}
}

// assertConverged checks that both homes hold the same entries, that the
// records agree with them, and that another reconcile finds nothing to do.
func (p *pair) assertConverged() {
	t := p.t
	t.Helper()
	a, b := p.gw.scan(t), p.wk.scan(t)
	if !sameTree(a, b) {
		t.Fatalf("the homes differ after the reconcile:\n gateway %s\n worker  %s", describe(a), describe(b))
	}
	if p.gw.rec.Digest() != p.wk.rec.Digest() {
		t.Fatalf("the records differ after the reconcile")
	}
	before := p.transfers
	g, w := p.sync()
	if len(g) != 0 || len(w) != 0 {
		t.Fatalf("a second reconcile still has work to do:\n gateway %v\n worker  %v", g, w)
	}
	if p.transfers != before {
		t.Fatalf("a second reconcile transferred data")
	}
}

func sameTree(a, b map[string]Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for path, e := range a {
		o, ok := b[path]
		if !ok || !e.Equal(o) {
			return false
		}
	}
	return true
}

func describe(m map[string]Entry) string {
	var parts []string
	for _, p := range sortedPaths(m) {
		e := m[p]
		switch e.Type {
		case File:
			parts = append(parts, fmt.Sprintf("%s=%s", p, e.Hash[:6]))
		case Dir:
			parts = append(parts, p+"/")
		default:
			parts = append(parts, p+"->"+e.Link)
		}
	}
	return strings.Join(parts, " ")
}

// --- Helpers to change a home directly, like a program or the user would ---

var logical int64 = 1_700_000_000

// tick returns an increasing logical time. It is safe to call from several
// goroutines.
func tick() time.Time {
	return time.Unix(atomic.AddInt64(&logical, 10), 0)
}

func (n *node) write(t *testing.T, rel, content string) {
	t.Helper()
	if n.loose {
		// A program working while the sync runs: a directory may vanish
		// under it, which is not an error for the test.
		p := filepath.Join(n.root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0755)
		if os.WriteFile(p, []byte(content), 0644) == nil {
			tm := tick()
			os.Chtimes(p, tm, tm)
		}
		return
	}
	put(t, n.root, rel, content)
	setMtime(t, n.root, rel, tick())
}

func (n *node) remove(t *testing.T, rel string) {
	t.Helper()
	p := filepath.Join(n.root, filepath.FromSlash(rel))
	for attempt := 0; ; attempt++ {
		err := os.RemoveAll(p)
		if err == nil || (n.loose && attempt >= 5) {
			return
		}
		if !n.loose || attempt >= 5 {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // the sync was writing into it
	}
}

func (n *node) mv(t *testing.T, from, to string) {
	t.Helper()
	dst := filepath.Join(n.root, filepath.FromSlash(to))
	os.MkdirAll(filepath.Dir(dst), 0755)
	if err := os.Rename(filepath.Join(n.root, filepath.FromSlash(from)), dst); err != nil && !n.loose {
		t.Fatal(err)
	}
}

func (n *node) mkdir(t *testing.T, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(n.root, filepath.FromSlash(rel)), 0755); err != nil && !n.loose {
		t.Fatal(err)
	}
}

func (n *node) content(t *testing.T, rel string) string { return read(t, n.root, rel) }

// --- Scripted scenarios ---

func TestFirstContactIsAUnionAndDeletesNothing(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", "from gateway")
	p.gw.write(t, "dir/b.txt", "nested")
	p.wk.write(t, "w.txt", "from worker")
	p.wk.write(t, "a.txt", "worker's older a") // same name, different content
	// Make the gateway's a.txt the later one.
	p.gw.write(t, "a.txt", "from gateway")

	p.sync()
	p.assertConverged()
	if p.gw.content(t, "w.txt") != "from worker" || p.wk.content(t, "dir/b.txt") != "nested" {
		t.Fatal("each side must receive what only the other had")
	}
	if p.gw.content(t, "a.txt") != "from gateway" || p.wk.content(t, "a.txt") != "from gateway" {
		t.Fatal("the later modification must win a first-contact conflict")
	}
}

func TestChangesFlowBothWaysAndDeletesPropagate(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "keep.txt", "k")
	p.gw.write(t, "edit.txt", "v1")
	p.gw.write(t, "del-on-gw.txt", "x")
	p.gw.write(t, "del-on-wk.txt", "x")
	p.gw.write(t, "d/inner.txt", "i")
	p.sync()
	p.assertConverged()

	// While in step: changes on both sides.
	p.gw.write(t, "edit.txt", "v2 by gateway")
	p.gw.remove(t, "del-on-gw.txt")
	p.gw.write(t, "new-on-gw.txt", "n")
	p.wk.remove(t, "del-on-wk.txt")
	p.wk.write(t, "new-on-wk.txt", "m")
	p.wk.write(t, "d/more.txt", "w")
	p.sync()
	p.assertConverged()

	for _, n := range []*node{p.gw, p.wk} {
		if n.content(t, "edit.txt") != "v2 by gateway" || n.content(t, "new-on-gw.txt") != "n" ||
			n.content(t, "new-on-wk.txt") != "m" || n.content(t, "d/more.txt") != "w" || n.content(t, "keep.txt") != "k" {
			t.Fatalf("%s is missing a change: %s", n.name, describe(n.scan(t)))
		}
		if exists(n.root, "del-on-gw.txt") || exists(n.root, "del-on-wk.txt") {
			t.Fatalf("%s still has a file that was deleted on the other side", n.name)
		}
	}
}

func TestDeleteDoesNotComeBackAndMissingIsNotCopiedBack(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", "a")
	p.sync()
	p.assertConverged()
	p.wk.remove(t, "a.txt")
	for i := 0; i < 3; i++ {
		p.sync()
		p.assertConverged()
		if exists(p.gw.root, "a.txt") || exists(p.wk.root, "a.txt") {
			t.Fatalf("round %d: a deleted file came back", i)
		}
	}
}

func TestOfflineChangesOnBothSidesAreReconciled(t *testing.T) {
	// The worker was away; both sides kept changing files meanwhile.
	p := newPair(t)
	p.gw.write(t, "main.c", "int main(){return 0;}")
	p.gw.write(t, "old.txt", "old")
	p.gw.write(t, "shared.txt", "base")
	p.sync()
	p.assertConverged()

	// Worker at 10:05: main.c modified, output.txt created, old.txt deleted.
	p.wk.write(t, "main.c", "int main(){return 1;}")
	p.wk.write(t, "output.txt", "result")
	p.wk.remove(t, "old.txt")
	// Gateway while it was away: upload and save.
	p.gw.write(t, "upload.txt", "uploaded")
	p.gw.write(t, "shared.txt", "saved on gateway")
	p.sync()
	p.assertConverged()

	for _, n := range []*node{p.gw, p.wk} {
		if n.content(t, "main.c") != "int main(){return 1;}" || n.content(t, "output.txt") != "result" ||
			n.content(t, "upload.txt") != "uploaded" || n.content(t, "shared.txt") != "saved on gateway" || exists(n.root, "old.txt") {
			t.Fatalf("%s: %s", n.name, describe(n.scan(t)))
		}
	}
}

func TestBothModifiedLatestWinsOnBothSides(t *testing.T) {
	for _, workerLater := range []bool{true, false} {
		p := newPair(t)
		p.gw.write(t, "f", "base")
		p.sync()
		p.assertConverged()
		if workerLater {
			p.gw.write(t, "f", "gateway edit")
			p.wk.write(t, "f", "worker edit, later")
		} else {
			p.wk.write(t, "f", "worker edit")
			p.gw.write(t, "f", "gateway edit, later")
		}
		p.sync()
		p.assertConverged()
		want := "gateway edit, later"
		if workerLater {
			want = "worker edit, later"
		}
		if p.gw.content(t, "f") != want || p.wk.content(t, "f") != want {
			t.Fatalf("workerLater=%v: gateway %q, worker %q, want %q", workerLater, p.gw.content(t, "f"), p.wk.content(t, "f"), want)
		}
	}
}

func TestEqualTimesFavourTheGateway(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "f", "base")
	p.sync()
	p.gw.write(t, "f", "gateway")
	p.wk.write(t, "f", "worker!")
	same := tick()
	setMtime(t, p.gw.root, "f", same)
	setMtime(t, p.wk.root, "f", same)
	p.sync()
	p.assertConverged()
	if p.wk.content(t, "f") != "gateway" {
		t.Fatalf("on equal times the gateway's version must win, worker has %q", p.wk.content(t, "f"))
	}
}

func TestAWorkerClockThatIsAheadDoesNotWinByMistake(t *testing.T) {
	// The worker's clock runs 1 hour ahead. Its edit was made first in real
	// time, but carries a later timestamp.
	p := newPair(t)
	p.offset = time.Hour
	p.gw.write(t, "f", "base")
	p.sync()
	p.assertConverged()

	realTime := tick()
	p.wk.write(t, "f", "worker edit, first in real time")
	setMtime(t, p.wk.root, "f", realTime.Add(p.offset)) // stamped by the fast clock
	p.gw.write(t, "f", "gateway edit, later in real time")
	setMtime(t, p.gw.root, "f", realTime.Add(time.Second))

	p.sync()
	p.assertConverged()
	if p.wk.content(t, "f") != "gateway edit, later in real time" {
		t.Fatalf("the offset was not applied: worker has %q", p.wk.content(t, "f"))
	}
}

func TestDeleteVersusModifyKeepsTheModifiedFile(t *testing.T) {
	for _, deleteOnGateway := range []bool{true, false} {
		p := newPair(t)
		p.gw.write(t, "f", "base")
		p.sync()
		deleter, editor := p.gw, p.wk
		if !deleteOnGateway {
			deleter, editor = p.wk, p.gw
		}
		deleter.remove(t, "f")
		editor.write(t, "f", "edited")
		p.sync()
		p.assertConverged()
		if p.gw.content(t, "f") != "edited" || p.wk.content(t, "f") != "edited" {
			t.Fatalf("deleteOnGateway=%v: the edit was lost", deleteOnGateway)
		}
	}
}

func TestADeletedDirectoryKeepsAFileEditedElsewhere(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "dir/keep.txt", "1")
	p.gw.write(t, "dir/gone.txt", "1")
	p.gw.write(t, "dir/sub/deep.txt", "1")
	p.sync()
	p.assertConverged()

	p.wk.remove(t, "dir")
	p.gw.write(t, "dir/keep.txt", "edited on gateway")
	p.sync()
	p.assertConverged()
	for _, n := range []*node{p.gw, p.wk} {
		if n.content(t, "dir/keep.txt") != "edited on gateway" {
			t.Fatalf("%s lost the edit: %s", n.name, describe(n.scan(t)))
		}
		if exists(n.root, "dir/gone.txt") || exists(n.root, "dir/sub") {
			t.Fatalf("%s kept untouched files of a deleted directory: %s", n.name, describe(n.scan(t)))
		}
	}
}

func TestADirectoryIsNeverReplacedByAFile(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "x/inner.txt", "precious")
	p.wk.write(t, "x", "a file with the same name, later")
	p.sync()
	p.assertConverged()
	if p.gw.content(t, "x/inner.txt") != "precious" || p.wk.content(t, "x/inner.txt") != "precious" {
		t.Fatal("a directory's content was lost to a file")
	}
}

func TestRenamesMoveNoData(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", strings.Repeat("big content ", 1000))
	p.gw.write(t, "other.txt", "other")
	p.sync()
	p.assertConverged()

	before := p.transfers
	p.gw.mv(t, "a.txt", "sub/dir/renamed.txt")
	gwActs, wkActs := p.sync()
	p.assertConverged()
	if p.transfers != before+0 {
		// The new directories are created, but no file content moves.
		t.Fatalf("a rename transferred %d file(s)", p.transfers-before)
	}
	if p.wk.content(t, "sub/dir/renamed.txt") != strings.Repeat("big content ", 1000) || exists(p.wk.root, "a.txt") {
		t.Fatal("the worker did not move the file")
	}
	var sawRename bool
	for _, a := range append(gwActs, wkActs...) {
		if a.Kind == RenameRemote || a.Kind == RenameLocal {
			sawRename = true
		}
	}
	if !sawRename {
		t.Fatalf("no rename was detected: %v", gwActs)
	}

	// And a rename made on the worker.
	p.wk.mv(t, "other.txt", "moved-by-worker.txt")
	p.sync()
	p.assertConverged()
	if p.gw.content(t, "moved-by-worker.txt") != "other" || exists(p.gw.root, "other.txt") {
		t.Fatal("the gateway did not follow the worker's rename")
	}
}

func TestSymlinksAndDirectoriesAreSynchronized(t *testing.T) {
	p := newPair(t)
	p.gw.mkdir(t, "empty/nested")
	p.gw.write(t, "target.txt", "t")
	os.Symlink("target.txt", filepath.Join(p.gw.root, "ln"))
	os.Symlink("/etc/passwd", filepath.Join(p.gw.root, "abs"))
	p.sync()
	p.assertConverged()
	if got, _ := os.Readlink(filepath.Join(p.wk.root, "ln")); got != "target.txt" {
		t.Fatalf("link = %q", got)
	}
	if got, _ := os.Readlink(filepath.Join(p.wk.root, "abs")); got != "/etc/passwd" {
		t.Fatalf("absolute link = %q (it must be copied as text)", got)
	}
	if fi, err := os.Stat(filepath.Join(p.wk.root, "empty", "nested")); err != nil || !fi.IsDir() {
		t.Fatal("an empty directory was not synchronized")
	}
	// Remove them on the worker.
	os.Remove(filepath.Join(p.wk.root, "ln"))
	os.Remove(filepath.Join(p.wk.root, "empty", "nested"))
	p.sync()
	p.assertConverged()
	if exists(p.gw.root, "ln") || exists(p.gw.root, "empty/nested") {
		t.Fatal("deletions of a link and a directory did not propagate")
	}
}

func TestModeChangesPropagate(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "run.sh", "#!/bin/sh")
	p.sync()
	p.assertConverged()
	os.Chmod(filepath.Join(p.wk.root, "run.sh"), 0755)
	p.sync()
	p.assertConverged()
	if fi, _ := os.Stat(filepath.Join(p.gw.root, "run.sh")); fi.Mode().Perm() != 0755 {
		t.Fatalf("mode on the gateway = %v", fi.Mode().Perm())
	}
}

func TestLosingTheRecordNeverDeletesAnything(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", "a")
	p.gw.write(t, "b.txt", "b")
	p.sync()
	p.assertConverged()
	// Both sides lose their record. One file was deleted meanwhile.
	p.gw.rec, p.wk.rec = NewRecord("guest-test", "peer"), NewRecord("guest-test", "peer")
	p.gw.remove(t, "a.txt")
	p.sync()
	p.assertConverged()
	if !exists(p.gw.root, "a.txt") || !exists(p.wk.root, "a.txt") {
		t.Fatal("with no record the deleted file must come back, never be deleted elsewhere")
	}
	if !exists(p.gw.root, "b.txt") {
		t.Fatal("an untouched file vanished")
	}
}

func TestAFileBeingReceivedIsNeverScannedOrSent(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", "a")
	p.wk.write(t, TempPrefix+"partial", "half a fi")
	p.sync()
	p.assertConverged()
	if exists(p.gw.root, TempPrefix+"partial") {
		t.Fatal("a temporary file was synchronized")
	}
}

func TestFileDeletedWhileWeReadItIsHandled(t *testing.T) {
	p := newPair(t)
	p.gw.write(t, "a.txt", "a")
	p.sync()
	if _, err := p.gw.h.OpenRead("gone"); !os.IsNotExist(err) {
		t.Fatalf("opening a missing file: %v", err)
	}
	p.assertConverged()
}

// --- Randomized: many histories, same invariants ---

type opResult struct{}

func randomOps(t *testing.T, rng *rand.Rand, n *node, count int, track map[string]bool) {
	files := []string{"f0", "f1", "f2", "f3", "d0/f0", "d0/f1", "d1/f0", "d1/s/f0"}
	dirs := []string{"d0", "d1", "d1/s"}
	links := []string{"l0", "l1"}
	content := func() string { return strings.Repeat(string(rune('a'+rng.Intn(4))), 1+rng.Intn(6)) }
	for i := 0; i < count; i++ {
		switch rng.Intn(9) {
		case 0, 1, 2:
			p := files[rng.Intn(len(files))]
			if exists(n.root, p) && !isRegular(n.root, p) {
				continue
			}
			n.write(t, p, content())
			track[p] = true
		case 3:
			p := files[rng.Intn(len(files))]
			if isRegular(n.root, p) {
				n.remove(t, p)
				track[p] = true
			}
		case 4:
			from, to := files[rng.Intn(len(files))], files[rng.Intn(len(files))]
			if from != to && isRegular(n.root, from) && (!exists(n.root, to) || isRegular(n.root, to)) {
				n.mv(t, from, to)
				track[from], track[to] = true, true
			}
		case 5:
			n.mkdir(t, dirs[rng.Intn(len(dirs))])
		case 6:
			d := dirs[rng.Intn(len(dirs))]
			if exists(n.root, d) {
				filepath.Walk(filepath.Join(n.root, d), func(p string, info os.FileInfo, err error) error {
					if err == nil {
						rel, _ := filepath.Rel(n.root, p)
						track[filepath.ToSlash(rel)] = true
					}
					return nil
				})
				n.remove(t, d)
			}
		case 7:
			p := files[rng.Intn(len(files))]
			if isRegular(n.root, p) {
				mode := os.FileMode(0644)
				if rng.Intn(2) == 0 {
					mode = 0755
				}
				os.Chmod(filepath.Join(n.root, p), mode)
				track[p] = true
			}
		case 8:
			l := links[rng.Intn(len(links))]
			full := filepath.Join(n.root, l)
			if exists(n.root, l) {
				os.Remove(full)
			} else {
				os.Symlink(files[rng.Intn(len(files))], full)
			}
			track[l] = true
		}
	}
}

func isRegular(root, rel string) bool {
	fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && fi.Mode().IsRegular()
}

func TestRandomHistoriesConverge(t *testing.T) {
	seeds := 150
	if testing.Short() {
		seeds = 20
	}
	for seed := 1; seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		p := newPair(t)
		p.offset = time.Duration(rng.Intn(3)) * time.Second * 0 // clocks agree: the logical times order the edits
		// A common starting point.
		randomOps(t, rng, p.gw, 10, map[string]bool{})
		p.sync()
		p.assertConverged()

		for round := 0; round < 4; round++ {
			gwTouched, wkTouched := map[string]bool{}, map[string]bool{}
			gwBefore, wkBefore := p.gw.scan(t), p.wk.scan(t)
			randomOps(t, rng, p.gw, rng.Intn(8), gwTouched)
			randomOps(t, rng, p.wk, rng.Intn(8), wkTouched)
			gwAfter, wkAfter := p.gw.scan(t), p.wk.scan(t)

			p.sync()
			p.assertConverged()
			final := p.gw.scan(t)

			// No change is lost or invented: for every path, the final state is
			// a state one of the sides had after its edits, and a path changed
			// on one side only ends up as that side left it.
			for path := range union(gwAfter, wkAfter, final) {
				g, w, f := entryPtr(gwAfter, path), entryPtr(wkAfter, path), entryPtr(final, path)
				if !eq(f, g) && !eq(f, w) && !(f != nil && f.Type == Dir) && !(f == nil && (g != nil && g.Type == Dir || w != nil && w.Type == Dir)) {
					t.Fatalf("seed %d round %d: %s ends as %s, which neither side had (gateway %s, worker %s)", seed, round, path, show(f), show(g), show(w))
				}
				gwChanged := !eq(g, entryPtr(gwBefore, path))
				wkChanged := !eq(w, entryPtr(wkBefore, path))
				isDir := (g != nil && g.Type == Dir) || (w != nil && w.Type == Dir) || (f != nil && f.Type == Dir)
				if isDir {
					continue // directories follow their contents
				}
				if gwChanged && !wkChanged && !eq(f, g) {
					t.Fatalf("seed %d round %d: %s changed only on the gateway (%s) but ends as %s", seed, round, path, show(g), show(f))
				}
				if wkChanged && !gwChanged && !eq(f, w) {
					t.Fatalf("seed %d round %d: %s changed only on the worker (%s) but ends as %s", seed, round, path, show(w), show(f))
				}
				if gwChanged && wkChanged && g != nil && w != nil && !eq(g, w) {
					want := g
					if w.ModTime > g.ModTime {
						want = w
					}
					if !eq(f, want) {
						t.Fatalf("seed %d round %d: %s changed on both sides, the later one is %s but it ends as %s", seed, round, path, show(want), show(f))
					}
				}
				if gwChanged && wkChanged && (g == nil) != (w == nil) {
					present := g
					if present == nil {
						present = w
					}
					if !eq(f, present) {
						t.Fatalf("seed %d round %d: %s was deleted on one side and changed on the other; the change must survive (%s) but it ends as %s", seed, round, path, show(present), show(f))
					}
				}
			}
		}
	}
}

func show(e *Entry) string {
	if e == nil {
		return "absent"
	}
	switch e.Type {
	case File:
		return fmt.Sprintf("file %s mode %o", e.Hash[:6], e.Mode)
	case Dir:
		return "dir"
	}
	return "link->" + e.Link
}

func union(maps ...map[string]Entry) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			out[k] = struct{}{}
		}
	}
	return out
}

var _ = reflect.DeepEqual
var _ = sort.Strings
var _ = io.EOF
