package appstore

import "sort"

// RetiredIDs are the entries that were cut from the default catalogue (2026-11-17, LLD 18 section 15). A server whose catalog database still
// holds a record of one (it was edited, measured or marked full once) keeps listing it: the default file cannot take a record out of the
// database. The admin sees those here and removes the ones he picks.
var RetiredIDs = map[string]bool{}

func init() {
	for _, id := range []string{"polars", "seaborn-base", "statsmodels", "pyarrow", "numba", "pytorch-cpu", "py27-six", "py27-requests", "py27-numpy",
		"clang-tools", "ccache", "vim", "litecli", "sqlite-utils", "gopls", "rust-analyzer", "libspatialite", "tcllib", "perl-text-csv",
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PurgeRetired deletes the database records of the retired entries the admin picked. Only ids that are retired and listed are touched, so
// it cannot take a live entry off the list; an installed app is not touched. It returns how many records went.
func (s *Service) PurgeRetired(ids []string) (int, error) {
	listed := map[string]bool{}
	for _, r := range s.Retired() {
		listed[r.ID] = true
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
