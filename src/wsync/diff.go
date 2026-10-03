package wsync

import (
	"sort"
	"time"
)

// ActionKind says what a reconcile must do about one path.
type ActionKind uint8

const (
	// Agree: both sides already match. Only the record changes.
	Agree ActionKind = iota + 1
	// TakeRemote: make the local side equal to the remote side. If the
	// remote side has no such path, delete it locally.
	TakeRemote
	// GiveLocal: make the remote side equal to the local side. If the local
	// side has no such path, ask the peer to delete it.
	GiveLocal
	// RenameLocal: the remote side moved a file; move it locally too.
	RenameLocal
	// RenameRemote: the local side moved a file; the peer moves it too.
	RenameRemote
)

func (k ActionKind) String() string {
	switch k {
	case Agree:
		return "agree"
	case TakeRemote:
		return "take-remote"
	case GiveLocal:
		return "give-local"
	case RenameLocal:
		return "rename-local"
	case RenameRemote:
		return "rename-remote"
	}
	return "unknown"
}

// Action is one step of a reconcile.
type Action struct {
	Kind ActionKind
	// Path is the path acted on. For a rename it is the new path.
	Path string
	// From is the old path of a rename.
	From string
	// Local and Remote are the two sides' entries at Path; nil means absent.
	// For a rename, the side that has to move the file has its entry at From
	// and the other side has its entry at Path.
	Local, Remote *Entry
	// Conflict is set when both sides had changed the path and the later
	// modification was chosen.
	Conflict bool
}

// IsDelete reports whether the action removes a path from the side that
// changes.
func (a Action) IsDelete() bool {
	return (a.Kind == TakeRemote && a.Remote == nil) || (a.Kind == GiveLocal && a.Local == nil)
}

// Params are the inputs of a three-way comparison.
type Params struct {
	// Base is the state both sides last agreed on.
	Base *Record
	// Local and Remote are the current states, as returned by Scan.
	Local, Remote map[string]Entry
	// LocalIsGateway says which side is the gateway. It decides ties.
	LocalIsGateway bool
	// Offset is the worker's clock minus the gateway's clock. File times
	// from the worker are corrected by it before they are compared.
	Offset time.Duration
}

// Compare decides, path by path, what must happen for the two sides to
// agree. Both sides run it with their own view and reach mirror-image
// results, because every rule depends only on the three states, on which side
// is the gateway, and on the clock offset.
//
// For each path, with b the agreed state and l and r the current ones:
//
//	l == r                    nothing to transfer; the record is brought up to date
//	only r changed since b    take remote (including a delete)
//	only l changed since b    give local (including a delete)
//	both changed              the later modification wins; a side that deleted
//	                          the path loses to a side that changed it;
//	                          a directory wins over anything that is not one
//
// A path that is in neither side and not in the record needs nothing. With
// no record at all, nothing counts as deleted, so the result is the union of
// both sides.
//
// The actions are ordered so they can be applied one after another: first
// everything that creates or updates, shortest path first (a directory
// before its contents), then deletes, longest path first (contents before
// the directory).
func Compare(p Params) []Action {
	paths := make(map[string]struct{}, len(p.Local)+len(p.Remote))
	for k := range p.Local {
		paths[k] = struct{}{}
	}
	for k := range p.Remote {
		paths[k] = struct{}{}
	}
	if p.Base != nil {
		for k := range p.Base.Entries {
			paths[k] = struct{}{}
		}
	}

	var actions []Action
	for path := range paths {
		l, r := entryPtr(p.Local, path), entryPtr(p.Remote, path)
		var b *Entry
		if p.Base != nil {
			if e, ok := p.Base.Entries[path]; ok {
				b = &e
			}
		}
		if a, ok := decide(p, path, l, r, b); ok {
			actions = append(actions, a)
		}
	}
	actions = pairRenames(actions)
	sortActions(actions)
	return actions
}

func decide(p Params, path string, l, r, b *Entry) (Action, bool) {
	act := Action{Path: path, Local: l, Remote: r}
	if eq(l, r) {
		// Same on both sides. The record only needs to catch up.
		if eq(l, b) {
			return act, false
		}
		act.Kind = Agree
		return act, true
	}
	changedL, changedR := !eq(l, b), !eq(r, b)
	switch {
	case !changedL && changedR:
		act.Kind = TakeRemote
	case changedL && !changedR:
		act.Kind = GiveLocal
	default:
		// Both changed, differently.
		act.Conflict = true
		switch {
		case l == nil:
			act.Kind = TakeRemote // deleted here, changed there: keep the change
		case r == nil:
			act.Kind = GiveLocal
		case l.Type == Dir && r.Type != Dir:
			act.Kind = GiveLocal // never replace a directory, and what is in it, by a file
		case r.Type == Dir && l.Type != Dir:
			act.Kind = TakeRemote
		case p.localIsNewer(*l, *r):
			act.Kind = GiveLocal
		default:
			act.Kind = TakeRemote
		}
	}
	return act, true
}

// localIsNewer compares modification times after correcting the worker's by
// its clock offset. On equal times the gateway's version wins.
func (p Params) localIsNewer(l, r Entry) bool {
	lt, rt := l.ModTime, r.ModTime
	off := p.Offset.Nanoseconds()
	if p.LocalIsGateway {
		rt -= off
	} else {
		lt -= off
	}
	if lt != rt {
		return lt > rt
	}
	return p.LocalIsGateway
}

func sortActions(actions []Action) {
	sort.SliceStable(actions, func(i, j int) bool {
		di, dj := actions[i].IsDelete(), actions[j].IsDelete()
		if di != dj {
			return !di // creates and updates first
		}
		if di {
			return actions[i].Path > actions[j].Path // contents before their directory
		}
		return actions[i].Path < actions[j].Path // a directory before its contents
	})
}

// renameKey identifies a file's content for pairing a delete with a create.
type renameKey struct {
	size int64
	hash string
	mode uint32
}

func keyOf(e *Entry) (renameKey, bool) {
	if e == nil || e.Type != File || e.Hash == "" {
		return renameKey{}, false
	}
	return renameKey{e.Size, e.Hash, e.Mode}, true
}

// pairRenames turns a delete and a create of the same file content, going
// the same way, into one rename. It is an optimisation: a pair that is not
// found is still correct as a delete and a create. Only regular files are
// paired, and only when the match is unique, so an ambiguous case is left as
// it is.
func pairRenames(actions []Action) []Action {
	type side struct{ deletes, creates map[renameKey][]int }
	sides := map[ActionKind]*side{
		GiveLocal:  {map[renameKey][]int{}, map[renameKey][]int{}},
		TakeRemote: {map[renameKey][]int{}, map[renameKey][]int{}},
	}
	for i, a := range actions {
		s := sides[a.Kind]
		if s == nil || a.Conflict {
			continue
		}
		// What the changing side gives up (delete) or gains (create).
		var gone, made *Entry
		if a.Kind == GiveLocal {
			gone, made = a.Remote, a.Local
		} else {
			gone, made = a.Local, a.Remote
		}
		switch {
		case made == nil:
			if k, ok := keyOf(gone); ok {
				s.deletes[k] = append(s.deletes[k], i)
			}
		case gone == nil:
			if k, ok := keyOf(made); ok {
				s.creates[k] = append(s.creates[k], i)
			}
		}
	}

	drop := map[int]bool{}
	var renames []Action
	for kind, s := range sides {
		for k, dels := range s.deletes {
			crs := s.creates[k]
			if len(dels) != 1 || len(crs) != 1 {
				continue
			}
			del, cr := actions[dels[0]], actions[crs[0]]
			re := Action{Path: cr.Path, From: del.Path, Local: cr.Local, Remote: cr.Remote}
			if kind == GiveLocal {
				re.Kind = RenameRemote
				re.Remote = del.Remote // what is at From on the peer
			} else {
				re.Kind = RenameLocal
				re.Local = del.Local
			}
			drop[dels[0]], drop[crs[0]] = true, true
			renames = append(renames, re)
		}
	}
	if len(drop) == 0 {
		return actions
	}
	out := make([]Action, 0, len(actions)-len(drop)+len(renames))
	for i, a := range actions {
		if !drop[i] {
			out = append(out, a)
		}
	}
	return append(out, renames...)
}
