package homesnap

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"objstore"
)

// What a versioned bucket (B2) keeps of a snapshot (changed 2026-11-14). A day's file is overwritten by every snapshot of the day, and
// the bucket keeps each version it replaces: with a snapshot every 5 minutes that is dozens of copies of the same archive a day, all
// billed. After each snapshot the versions are thinned: of every file of the home (the days' archives and latest.json) the newest
// version is kept, and, of the older ones, the newest of each of the last VersionHours clock hours; the rest are removed for good. The days'
// files themselves are kept as before (Keep days). A bad snapshot therefore cannot replace the only good copy of the day: the
// hourly ones before it are still there to restore from (the profile page lists them).

const VersionHours = 6

// versionsToDrop picks, among the versions of one key, the ones to remove at time now. The newest version is kept; of the older ones,
// the newest of each clock hour (UTC) of the last VersionHours hours, the one that is going on included. Delete markers are never
// removed. (A clock hour, not "an hour ago": a version that is the last of its hour stays the last of it, so a kept copy is not
// dropped by the next snapshot.)
func versionsToDrop(vs []objstore.Version, now time.Time) []objstore.Version {
	var real []objstore.Version
	for _, v := range vs {
		if !v.DeleteMarker {
			real = append(real, v)
		}
	}
	if len(real) < 2 {
		return nil
	}
	sort.SliceStable(real, func(i, j int) bool { return real[i].LastModified.After(real[j].LastModified) })
	hourOf := func(t time.Time) int64 { return t.Unix() / 3600 }
	current := hourOf(now)
	kept := map[int64]bool{hourOf(real[0].LastModified): true} // the newest stands for its hour
	var drop []objstore.Version
	for _, v := range real[1:] {
		h := hourOf(v.LastModified)
		if h > current-VersionHours && h <= current && !kept[h] {
			kept[h] = true
			continue
		}
		drop = append(drop, v)
	}
	return drop
}

// pruneVersions thins the versions of the home's files in the bucket and says how many it removed. A store that keeps no versions, a
// listing that fails or a key that may not delete versions leaves everything as it is (the last is said once in the log).
func (s *Snapper) pruneVersions(ctx context.Context, c *objstore.Client, home string) int {
	all, err := c.ListVersions(ctx, s.prefix(home))
	if err != nil {
		if !errors.Is(err, objstore.ErrNoVersions) {
			log.Printf("homesnap: the versions of %s could not be listed: %v", home, err)
		}
		return 0
	}
	byKey := map[string][]objstore.Version{}
	for _, v := range all {
		byKey[v.Key] = append(byKey[v.Key], v)
	}
	now := s.now()
	removed := 0
	for key, vs := range byKey {
		for _, v := range versionsToDrop(vs, now) {
			if err := c.DeleteVersion(ctx, key, v.VersionID); err != nil {
				log.Printf("homesnap: an old version of %s could not be removed: %v (the key of the bucket needs the right to delete files)", key, err)
				return removed
			}
			removed++
		}
	}
	return removed
}
