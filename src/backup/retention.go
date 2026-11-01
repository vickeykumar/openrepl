package backup

import (
	"sort"
	"time"
)

// Entry is one archive in the bucket.
type Entry struct {
	Key string
	At  time.Time
}

const (
	// KeepHourly is how many hours back the newest archive of each hour is kept, and KeepDaily how many days
	// back the newest archive of each day is; KeepNewest archives are always kept, whatever their age.
	KeepHourly = 24
	KeepDaily  = 7
	KeepNewest = 3
)

// Prune says which archives to delete: it keeps the newest KeepNewest, the one named protect (what latest.json
// points at), the newest of each of the last KeepHourly hours and the newest of each of the last KeepDaily days
// (days and hours by UTC time). Nothing is dropped from a list of KeepNewest or fewer.
func Prune(entries []Entry, now time.Time, protect string) (keep, drop []Entry) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.After(sorted[j].At) }) // newest first
	kept := map[string]bool{}
	if protect != "" {
		kept[protect] = true
	}
	hours, days := map[string]bool{}, map[string]bool{}
	for i, e := range sorted {
		age := now.Sub(e.At)
		if i < KeepNewest {
			kept[e.Key] = true
		}
		if age <= KeepHourly*time.Hour {
			h := e.At.UTC().Format("2006010215")
			if !hours[h] {
				hours[h] = true
				kept[e.Key] = true
			}
		}
		if age <= KeepDaily*24*time.Hour {
			d := e.At.UTC().Format("20060102")
			if !days[d] {
				days[d] = true
				kept[e.Key] = true
			}
		}
	}
	for _, e := range sorted {
		if kept[e.Key] {
			keep = append(keep, e)
		} else {
			drop = append(drop, e)
		}
	}
	return keep, drop
}
