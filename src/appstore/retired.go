package appstore

import (
	"sort"
	"strings"
)

// RetiredIDs are the entries that were cut from the default catalogue (2026-11-17, LLD 18 section 15). A server whose catalog database still
// holds a record of one (it was edited, measured or marked full once) keeps listing it: the default file cannot take a record out of the
// database. The admin sees those here and removes the ones he picks.
var RetiredIDs = map[string]bool{}

func init() {
	for _, id := range []string{"polars", "seaborn-base", "statsmodels", "pyarrow", "numba", "pytorch-cpu", "clang-tools", "ccache", "vim", "litecli", "sqlite-utils", "gopls", "rust-analyzer", "libspatialite", "tcllib", "perl-text-csv",
		"perl-mojolicious", "perl-try-tiny", "perl-path-tiny", "npm:got", "npm:ramda", "npm:immutable", "npm:moment", "npm:date-fns",
		"npm:papaparse", "npm:jsdom", "npm:sinon", "npm:sharp", "npm:better-sqlite3", "web:water.css", "web:normalize.css", "web:animate.css",
		"web:lodash", "web:katex", "web:leaflet", "web:matter-js", "web:sortablejs", "web:swiper", "web:marked", "web:mathjs", "go:mux",
		"go:logrus", "go:x-text", "jar:commons-io", "jar:jackson-databind", "jar:joda-time", "jar:slf4j-simple"} {
		RetiredIDs[id] = true
	}
}

// RetiredEntry is a retired entry that this server still lists, and whether an installed copy exists (it stays installed either way).
type RetiredEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Installed bool   `json:"installed"`
	// Removed: an admin took it off the list (a marker in the database). InDefault: the default file still has it, so it can be restored.
	Removed   bool `json:"removed,omitempty"`
	InDefault bool `json:"inDefault,omitempty"`
}

// Retired lists the retired entries that the catalogue still shows, by id. An id that the default file carries again is not retired.
func (s *Service) Retired() []RetiredEntry {
	base, _ := DefaultCatalogue()
	inBase := map[string]bool{}
	for _, e := range base {
		inBase[e.ID] = true
	}
	var out []RetiredEntry
	for _, e := range s.Catalogue() {
		if !RetiredIDs[e.ID] || inBase[e.ID] {
			continue
		}
		r := RetiredEntry{ID: e.ID, Name: e.Name, Kind: e.Kind}
		if k := e.InstallKind(); k != "" {
			if a, ok := s.app(e.PackageName()); ok && a.State == StateInstalled && cleanKind(a.Kind) == k {
				r.Installed = true
			}
		}
		out = append(out, r)
	}
	// the entries an admin removed: kept as a marker, so the admin can see them and bring them back
	baseByID := map[string]Entry{}
	for _, e := range base {
		baseByID[e.ID] = e
	}
	var removed []string // collected first: the database is not read again inside its own scan
	s.each("catdel:", func(key string, _ []byte) { removed = append(removed, strings.TrimPrefix(key, "catdel:")) })
	for _, id := range removed {
		e, in := baseByID[id]
		r := RetiredEntry{ID: id, Name: id, Removed: true, InDefault: in}
		if in {
			r.Name, r.Kind = e.Name, e.Kind
			if k := e.InstallKind(); k != "" {
				if a, ok := s.app(e.PackageName()); ok && a.State == StateInstalled && cleanKind(a.Kind) == k {
					r.Installed = true
				}
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RestoreRemoved brings back entries that an admin removed and the default file still has. It returns how many came back.
func (s *Service) RestoreRemoved(ids []string) int {
	ok := map[string]bool{}
	for _, r := range s.Retired() {
		if r.Removed && r.InDefault {
			ok[r.ID] = true
		}
	}
	n := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if ok[id] {
			s.del("catdel:" + id)
			n++
		}
	}
	return n
}

// PurgeRetired deletes the database records of the retired entries the admin picked. Only ids that are retired and listed are touched, so
// it cannot take a live entry off the list; an installed app is not touched. It returns how many records went.
func (s *Service) PurgeRetired(ids []string) (int, error) {
	listed := map[string]bool{}
	for _, r := range s.Retired() {
		if !r.InDefault { // an entry the default file has can only be restored: its record is the list itself
			listed[r.ID] = true
		}
	}
	n := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if !listed[id] {
			continue
		}
		s.del("cat:" + id)
		s.del("catdel:" + id)
		n++
	}
	return n, nil
}
