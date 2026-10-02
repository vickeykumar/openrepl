package wsync

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// f builds a file entry; the hash stands for the content.
func f(path, hash string, mtime int64) Entry {
	return Entry{Path: path, Type: File, Size: int64(len(hash)), Mode: 0644, Hash: hash, ModTime: mtime}
}

func d(path string) Entry { return Entry{Path: path, Type: Dir, Mode: 0755} }

func m(entries ...Entry) map[string]Entry {
	out := map[string]Entry{}
	for _, e := range entries {
		out[e.Path] = e
	}
	return out
}

func base(entries ...Entry) *Record {
	r := NewRecord("h", "p")
	for _, e := range entries {
		r.Set(e)
	}
	r.Valid = true
	return r
}

type kinds map[string]ActionKind

func kindsOf(actions []Action) kinds {
	out := kinds{}
	for _, a := range actions {
		out[a.Path] = a.Kind
	}
	return out
}

func TestCompareThreeWayTable(t *testing.T) {
	type c struct {
		name   string
		base   *Record
		l, r   map[string]Entry
		offset time.Duration
		isGW   bool
		want   kinds
	}
	cases := []c{
		{name: "unchanged everywhere", base: base(f("a", "1", 1)), l: m(f("a", "1", 1)), r: m(f("a", "1", 1)), want: kinds{}},
		{name: "only the time differs, same content", base: base(f("a", "1", 1)), l: m(f("a", "1", 1)), r: m(f("a", "1", 99)), want: kinds{}},

		// One side changed.
		{name: "remote modified", base: base(f("a", "1", 1)), l: m(f("a", "1", 1)), r: m(f("a", "2", 5)), want: kinds{"a": TakeRemote}},
		{name: "local modified", base: base(f("a", "1", 1)), l: m(f("a", "2", 5)), r: m(f("a", "1", 1)), want: kinds{"a": GiveLocal}},
		{name: "remote created", base: base(), l: m(), r: m(f("a", "1", 1)), want: kinds{"a": TakeRemote}},
		{name: "local created", base: base(), l: m(f("a", "1", 1)), r: m(), want: kinds{"a": GiveLocal}},

		// Deletes are decided from the record.
		{name: "remote deleted, local unchanged", base: base(f("a", "1", 1)), l: m(f("a", "1", 1)), r: m(), want: kinds{"a": TakeRemote}},
		{name: "local deleted, remote unchanged", base: base(f("a", "1", 1)), l: m(), r: m(f("a", "1", 1)), want: kinds{"a": GiveLocal}},
		{name: "deleted on both", base: base(f("a", "1", 1)), l: m(), r: m(), want: kinds{"a": Agree}},

		// Both changed.
		{name: "same change on both sides", base: base(f("a", "1", 1)), l: m(f("a", "2", 5)), r: m(f("a", "2", 9)), want: kinds{"a": Agree}},
		{name: "both created the same file", base: base(), l: m(f("a", "1", 1)), r: m(f("a", "1", 2)), want: kinds{"a": Agree}},
		{name: "remote deleted, local modified: keep the change", base: base(f("a", "1", 1)), l: m(f("a", "2", 5)), r: m(), want: kinds{"a": GiveLocal}},
		{name: "local deleted, remote modified: keep the change", base: base(f("a", "1", 1)), l: m(), r: m(f("a", "2", 5)), want: kinds{"a": TakeRemote}},

		// Latest modification wins.
		{name: "both modified, local later", base: base(f("a", "1", 1)), l: m(f("a", "2", 20)), r: m(f("a", "3", 10)), want: kinds{"a": GiveLocal}},
		{name: "both modified, remote later", base: base(f("a", "1", 1)), l: m(f("a", "2", 10)), r: m(f("a", "3", 20)), want: kinds{"a": TakeRemote}},
		{name: "both modified, equal times, gateway wins (local is gateway)", isGW: true, base: base(f("a", "1", 1)), l: m(f("a", "2", 10)), r: m(f("a", "3", 10)), want: kinds{"a": GiveLocal}},
		{name: "both modified, equal times, gateway wins (local is worker)", isGW: false, base: base(f("a", "1", 1)), l: m(f("a", "2", 10)), r: m(f("a", "3", 10)), want: kinds{"a": TakeRemote}},

		// First contact: no record, nothing is deleted.
		{name: "no record, only remote has it", base: NewRecord("h", "p"), l: m(), r: m(f("a", "1", 1)), want: kinds{"a": TakeRemote}},
		{name: "no record, only local has it", base: NewRecord("h", "p"), l: m(f("a", "1", 1)), r: m(), want: kinds{"a": GiveLocal}},
		{name: "no record, both have it, same", base: NewRecord("h", "p"), l: m(f("a", "1", 1)), r: m(f("a", "1", 1)), want: kinds{"a": Agree}},
		{name: "no record, both have it, different, remote later", base: NewRecord("h", "p"), l: m(f("a", "1", 1)), r: m(f("a", "2", 9)), want: kinds{"a": TakeRemote}},
		{name: "a nil record behaves like no record", base: nil, l: m(f("a", "1", 1)), r: m(), want: kinds{"a": GiveLocal}},

		// A path nobody has and the record forgot needs nothing; one the
		// record still lists is forgotten.
		{name: "gone everywhere and not recorded", base: base(), l: m(), r: m(), want: kinds{}},

		// Types.
		{name: "directory created remotely", base: base(), l: m(), r: m(d("d")), want: kinds{"d": TakeRemote}},
		{name: "directory beats a file whatever the time (local dir)", base: base(), l: m(d("x")), r: m(f("x", "1", 99)), want: kinds{"x": GiveLocal}},
		{name: "directory beats a file whatever the time (remote dir)", base: base(), l: m(f("x", "1", 99)), r: m(d("x")), want: kinds{"x": TakeRemote}},
	}
	for _, tc := range cases {
		got := kindsOf(Compare(Params{Base: tc.base, Local: tc.l, Remote: tc.r, LocalIsGateway: tc.isGW, Offset: tc.offset}))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got  %v\n want %v", tc.name, got, tc.want)
		}
	}
}

func TestCompareMarksConflicts(t *testing.T) {
	acts := Compare(Params{
		Base:  base(f("a", "1", 1), f("b", "1", 1), f("c", "1", 1)),
		Local: m(f("a", "2", 5), f("b", "1", 1), f("c", "2", 5)), Remote: m(f("a", "3", 9), f("b", "2", 5)),
	})
	conflict := map[string]bool{}
	for _, a := range acts {
		conflict[a.Path] = a.Conflict
	}
	if !conflict["a"] || conflict["b"] || !conflict["c"] {
		t.Fatalf("conflicts = %v (a: both changed; b: one side; c: deleted vs changed)", conflict)
	}
}

func TestCompareAppliesTheClockOffset(t *testing.T) {
	// The worker's clock is 10 s ahead: its time 100 s is the gateway's 90 s.
	off := 10 * time.Second
	sec := int64(time.Second)
	gw := f("a", "gateway", 95*sec)
	wk := f("a", "worker", 100*sec)
	b := base(f("a", "base", 1))

	// Gateway as local: its 95 s is later than the worker's corrected 90 s.
	got := Compare(Params{Base: b, Local: m(gw), Remote: m(wk), LocalIsGateway: true, Offset: off})
	if got[0].Kind != GiveLocal {
		t.Fatalf("gateway change is later after correction, got %v", got[0].Kind)
	}
	// The same decision from the worker's point of view.
	got = Compare(Params{Base: b, Local: m(wk), Remote: m(gw), LocalIsGateway: false, Offset: off})
	if got[0].Kind != TakeRemote {
		t.Fatalf("worker side must reach the mirror decision, got %v", got[0].Kind)
	}
	// Without the correction the worker would have won.
	got = Compare(Params{Base: b, Local: m(gw), Remote: m(wk), LocalIsGateway: true})
	if got[0].Kind != TakeRemote {
		t.Fatalf("without the offset the worker's later raw time wins, got %v", got[0].Kind)
	}
}

func TestCompareBothSidesReachMirrorResults(t *testing.T) {
	mirror := map[ActionKind]ActionKind{
		Agree: Agree, TakeRemote: GiveLocal, GiveLocal: TakeRemote, RenameLocal: RenameRemote, RenameRemote: RenameLocal,
	}
	gw := m(f("a", "g", 10), f("b", "1", 1), f("c", "1", 1), d("dir"), f("same", "s", 1), f("tie", "g", 5))
	wk := m(f("a", "w", 20), f("c", "2", 9), f("e", "new", 3), f("same", "s", 4), f("tie", "w", 5))
	b := base(f("a", "0", 1), f("b", "1", 1), f("c", "1", 1), f("same", "old", 1))
	off := 3 * time.Second

	fromGW := Compare(Params{Base: b, Local: gw, Remote: wk, LocalIsGateway: true, Offset: off})
	fromWK := Compare(Params{Base: b, Local: wk, Remote: gw, LocalIsGateway: false, Offset: off})
	a, c := kindsOf(fromGW), kindsOf(fromWK)
	if len(a) != len(c) {
		t.Fatalf("different paths: %v vs %v", a, c)
	}
	for path, k := range a {
		if mirror[k] != c[path] {
			t.Errorf("%s: gateway says %v, worker says %v; they must be mirror images", path, k, c[path])
		}
	}
}

func TestCompareOrdersActionsForSafeApplication(t *testing.T) {
	b := base(d("old"), f("old/a", "1", 1), d("old/sub"), f("old/sub/b", "1", 1))
	// Remote removed everything under old; remote created new/x.
	l := m(d("old"), f("old/a", "1", 1), d("old/sub"), f("old/sub/b", "1", 1))
	r := m(d("new"), d("new/deeper"), f("new/deeper/x", "9", 1), f("new/y", "8", 1))
	acts := Compare(Params{Base: b, Local: l, Remote: r})

	pos := map[string]int{}
	for i, a := range acts {
		pos[a.Path] = i
	}
	// Creations first, a directory before its contents.
	if !(pos["new"] < pos["new/deeper"] && pos["new/deeper"] < pos["new/deeper/x"] && pos["new"] < pos["new/y"]) {
		t.Errorf("a directory must come before what it contains: %v", acts)
	}
	// Deletions after every creation, contents before their directory.
	for _, del := range []string{"old", "old/a", "old/sub", "old/sub/b"} {
		if pos[del] < pos["new/deeper/x"] {
			t.Errorf("delete of %s comes before the creations", del)
		}
	}
	if !(pos["old/sub/b"] < pos["old/sub"] && pos["old/sub"] < pos["old"] && pos["old/a"] < pos["old"]) {
		t.Errorf("a directory must be deleted after its contents: %v", acts)
	}
}

func TestCompareDeletedDirectoryKeepsAChangedFileInIt(t *testing.T) {
	b := base(d("dir"), f("dir/keep", "1", 1), f("dir/gone", "1", 1))
	// Local deleted the whole directory; remote edited dir/keep.
	l := m()
	r := m(d("dir"), f("dir/keep", "EDITED", 9), f("dir/gone", "1", 1))
	got := kindsOf(Compare(Params{Base: b, Local: l, Remote: r}))
	if got["dir/keep"] != TakeRemote {
		t.Fatalf("an edit must win over the deletion of its directory: %v", got)
	}
	if got["dir/gone"] != GiveLocal {
		t.Fatalf("the untouched file is deleted on the other side: %v", got)
	}
	if got["dir"] != GiveLocal {
		t.Fatalf("the directory delete is still requested; it fails there while it has content: %v", got)
	}
}

func TestComparePairsRenames(t *testing.T) {
	b := base(f("a.txt", "H", 1))
	// Local renamed a.txt to b.txt.
	acts := Compare(Params{Base: b, Local: m(f("b.txt", "H", 1)), Remote: m(f("a.txt", "H", 1))})
	if len(acts) != 1 || acts[0].Kind != RenameRemote || acts[0].From != "a.txt" || acts[0].Path != "b.txt" {
		t.Fatalf("local rename: %+v", acts)
	}
	if acts[0].Local == nil || acts[0].Local.Path != "b.txt" || acts[0].Remote == nil || acts[0].Remote.Path != "a.txt" {
		t.Fatalf("a rename must carry the entry at the new path on the mover and the old path on the peer: %+v", acts[0])
	}
	// Remote renamed it.
	acts = Compare(Params{Base: b, Local: m(f("a.txt", "H", 1)), Remote: m(f("b.txt", "H", 1))})
	if len(acts) != 1 || acts[0].Kind != RenameLocal || acts[0].From != "a.txt" || acts[0].Path != "b.txt" {
		t.Fatalf("remote rename: %+v", acts)
	}
	// Into a new directory: the directory is still created first.
	acts = Compare(Params{Base: b, Local: m(d("sub"), f("sub/b.txt", "H", 1)), Remote: m(f("a.txt", "H", 1))})
	got := kindsOf(acts)
	if got["sub"] != GiveLocal || got["sub/b.txt"] != RenameRemote {
		t.Fatalf("move into a new directory: %v", got)
	}
	if acts[0].Path != "sub" {
		t.Fatalf("the directory must be created before the rename: %+v", acts)
	}
}

func TestComparePairsRenamesOnlyWhenUnambiguous(t *testing.T) {
	b := base(f("a", "H", 1), f("b", "H", 1))
	// Two identical files deleted, one created: ambiguous, left as delete + create.
	got := kindsOf(Compare(Params{Base: b, Local: m(f("c", "H", 1)), Remote: m(f("a", "H", 1), f("b", "H", 1))}))
	for p, k := range got {
		if k == RenameRemote || k == RenameLocal {
			t.Fatalf("ambiguous rename was paired: %v (%s)", got, p)
		}
	}
	// Different content is not a rename.
	b = base(f("a", "H", 1))
	got = kindsOf(Compare(Params{Base: b, Local: m(f("b", "OTHER", 1)), Remote: m(f("a", "H", 1))}))
	if got["a"] != GiveLocal || got["b"] != GiveLocal {
		t.Fatalf("changed content must be delete + create: %v", got)
	}
	// Conflicts are never paired.
	b = base(f("a", "H", 1))
	acts := Compare(Params{Base: b, Local: m(f("b", "H", 1)), Remote: m(f("a", "CHANGED", 5))})
	for _, a := range acts {
		if a.Kind == RenameLocal || a.Kind == RenameRemote {
			t.Fatalf("a conflicting path was paired as a rename: %+v", acts)
		}
	}
}

func TestCompareDoesNotRenameDirectories(t *testing.T) {
	b := base(d("a"))
	got := kindsOf(Compare(Params{Base: b, Local: m(d("b")), Remote: m(d("a"))}))
	if got["a"] != GiveLocal || got["b"] != GiveLocal {
		t.Fatalf("directories are delete + create: %v", got)
	}
}

func TestCompareIsDeterministic(t *testing.T) {
	b := base(f("a", "1", 1), f("b", "1", 1), f("c", "1", 1), f("d", "1", 1))
	l := m(f("a", "2", 5), f("c", "1", 1), f("e", "n", 1))
	r := m(f("a", "3", 6), f("b", "1", 1), f("d", "9", 1))
	first := Compare(Params{Base: b, Local: l, Remote: r})
	for i := 0; i < 50; i++ {
		again := Compare(Params{Base: b, Local: l, Remote: r})
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs:\n%v\n%v", i, first, again)
		}
	}
	paths := []string{}
	for _, a := range first {
		paths = append(paths, a.Path)
	}
	if len(paths) == 0 || !sort.StringsAreSorted(paths[:1]) {
		t.Fatal("no actions")
	}
}
