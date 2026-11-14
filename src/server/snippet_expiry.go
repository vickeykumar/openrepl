package server

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// The end of a code link: the links made before they had one are given one, and the links that have ended are removed every week.
//
// The server refuses an ended link at once (snippets.go), so the week only decides when its record is taken out of the store.
// A link with no end (made before this) is not removed; at the first start after this ships it is given an end a month from that day,
// or the most the admin allows when that is shorter, once, and written back. No link is left without an end.

const (
	snippetCleanupFirst = 10 * time.Minute // after the server starts
	snippetCleanupEvery = 7 * 24 * time.Hour
	snippetStampWait    = 2 * time.Minute
)

type snippetCleanupResult struct {
	At      time.Time
	Looked  int
	Removed int
	Stamped int
	Err     string
}

var (
	snippetCleanupMu   sync.Mutex
	snippetCleanupLast snippetCleanupResult
)

// snippetEnds is the end of a link as a time to show, "" while it has none.
func snippetEnds(s snippet) string {
	if s.Expires == 0 {
		return ""
	}
	return time.Unix(s.Expires, 0).UTC().Format(time.RFC3339)
}

// stampOldSnippets gives every link that has no end one, and says how many it did.
func stampOldSnippets(now time.Time, maxDays int) (int, error) {
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return 0, errSnippetsOff
	}
	d := defaultSnippetMaxDays
	if maxDays < d {
		d = maxDays
	}
	ends := endOfDay(now.AddDate(0, 0, d))
	type kv struct {
		key  []byte
		data []byte
	}
	var todo []kv
	err := snippet_db_handle.Each(func(key, value []byte) bool {
		if !snippetIDPattern.MatchString(string(key)) {
			return true
		}
		var s snippet
		if json.Unmarshal(value, &s) != nil || s.Expires != 0 {
			return true
		}
		s.Expires = ends
		if data, err := json.Marshal(s); err == nil {
			todo = append(todo, kv{append([]byte{}, key...), data})
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	for _, t := range todo {
		if err := snippet_db_handle.Store(t.key, t.data); err != nil {
			return 0, err
		}
	}
	if len(todo) > 0 {
		if err := snippet_db_handle.Commit(); err != nil {
			return 0, err
		}
		snippetScan.InvalidateNow()
	}
	return len(todo), nil
}

// removeExpiredSnippets takes the links whose end has passed out of the store.
func removeExpiredSnippets(now time.Time) (looked, removed int, err error) {
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return 0, 0, errSnippetsOff
	}
	var gone [][]byte
	err = snippet_db_handle.Each(func(key, value []byte) bool {
		if !snippetIDPattern.MatchString(string(key)) {
			return true
		}
		looked++
		var s snippet
		if json.Unmarshal(value, &s) == nil && snippetExpired(s, now) {
			gone = append(gone, append([]byte{}, key...))
		}
		return true
	})
	if err != nil {
		return looked, 0, err
	}
	for _, k := range gone {
		if err := snippet_db_handle.Delete(k); err != nil {
			return looked, removed, err
		}
		removed++
	}
	if removed > 0 {
		if err := snippet_db_handle.Commit(); err != nil {
			return looked, removed, err
		}
		snippetScan.InvalidateNow()
	}
	return looked, removed, nil
}

// runSnippetCleanup is one round: the links without an end get one, then the ended ones go.
func runSnippetCleanup(now time.Time, maxDays int) snippetCleanupResult {
	res := snippetCleanupResult{At: now}
	var err error
	if res.Stamped, err = stampOldSnippets(now, maxDays); err != nil {
		res.Err = err.Error()
	}
	if looked, removed, err := removeExpiredSnippets(now); err != nil {
		res.Err = err.Error()
	} else {
		res.Looked, res.Removed = looked, removed
	}
	snippetCleanupMu.Lock()
	snippetCleanupLast = res
	snippetCleanupMu.Unlock()
	return res
}

// snippetCleanupStatus is what the health panel says of the job.
func snippetCleanupStatus() (status, detail string) {
	snippetCleanupMu.Lock()
	r := snippetCleanupLast
	snippetCleanupMu.Unlock()
	if r.At.IsZero() {
		return healthOK, "waiting for its first run (10 minutes after the server started, then every week)"
	}
	d := fmt.Sprintf("last run %s: %d code links looked at, %d removed, %d given an end", r.At.Format("2006-01-02 15:04"), r.Looked, r.Removed, r.Stamped)
	if r.Err != "" {
		return healthWarn, d + " (" + r.Err + ")"
	}
	return healthOK, d
}

// startSnippetCleanup runs in the background for the life of the server: the links with no end get one soon after it starts, and
// the ended ones are removed 10 minutes after the start and then every week. A worker keeps no code links.
func (server *Server) startSnippetCleanup() {
	if server.options.Mode == ModeWorker {
		return
	}
	go func() {
		time.Sleep(snippetStampWait)
		if n, err := stampOldSnippets(time.Now(), GetSiteSettings().snippetMaxDays()); err != nil {
			log.Printf("code links: giving the old ones an end: %v", err)
		} else if n > 0 {
			log.Printf("code links: %d links made before they had an end were given one", n)
		}
		time.Sleep(snippetCleanupFirst - snippetStampWait)
		for {
			res := runSnippetCleanup(time.Now(), GetSiteSettings().snippetMaxDays())
			log.Printf("code link cleanup: %d looked at, %d removed, %d given an end %s", res.Looked, res.Removed, res.Stamped, res.Err)
			time.Sleep(snippetCleanupEvery)
		}
	}()
}
