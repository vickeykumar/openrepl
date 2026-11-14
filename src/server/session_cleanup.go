package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"persist"
	"utils"
)

// The cleanup of shared sessions in the Firebase Realtime Database.
//
// The pages remove what a session leaves when it ends or runs out (page/31-share-session.js), but only if one of them is open
// to do it. This job, on the server, finds what is left over and removes it: it signs in to the database as the service account
// of the project (OPENREPL_FIREBASE_ADMIN, or the key of OPENREPL_FIRESTORE_CREDENTIALS), lists the session ids, and for each:
//
//   - a session with a share record (editor_values/<id>/share) whose end has passed, is removed;
//   - a session with no share record is removed too, once nothing says its owner is there (no live presence record,
//     editor_values/<id>/owner) and it has been seen so for a whole interval: an owner page of an older version, or one that has not
//     pressed Share, writes no share record, and its live session must not be taken from under it.
//
// "Removed" is the id under openrepl (the terminal), editor_values (the code), chat-list and file-browser. How often it runs is
// an admin setting (SiteSettings.SessionCleanupHours, daily by default).

// the places of the database that hold a session, by its id
var sessionPaths = []string{"openrepl", "editor_values", "chat-list", "file-browser"}

const (
	sessionCleanupFirstRun  = 5 * time.Minute // after the server starts
	sessionCleanupCheck     = time.Minute     // how often the loop looks at the clock
	sessionOwnerFresh       = 10 * time.Minute
	sessionCleanupMaxIDs    = 20000 // an id past this many in one run waits for the next
	sessionCleanupParallel  = 8
	sessionCleanupHTTPLimit = 20 * time.Second
)

// rtdb is a client of the Realtime Database REST API.
type rtdb struct {
	base  string     // https://<project>-default-rtdb.firebaseio.com, or the emulator's http://host:port
	query url.Values // what every request carries (the emulator's ns=)
	token func() (string, error)
	http  *http.Client
}

func (c *rtdb) url(path string, extra url.Values) string {
	q := url.Values{}
	for k, v := range c.query {
		q[k] = v
	}
	for k, v := range extra {
		q[k] = v
	}
	return c.base + "/" + strings.Trim(path, "/") + ".json?" + q.Encode()
}

func (c *rtdb) do(method, path string, extra url.Values, out interface{}) error {
	req, err := http.NewRequest(method, c.url(path, extra), nil)
	if err != nil {
		return err
	}
	tok, err := c.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("the database answered %d for %s %s", resp.StatusCode, method, path)
	}
	if out != nil && len(body) > 0 && string(body) != "null" {
		return json.Unmarshal(body, out)
	}
	return nil
}

// ids are the keys under a path, without reading what is below them.
func (c *rtdb) ids(path string) ([]string, error) {
	var m map[string]interface{}
	if err := c.do("GET", path, url.Values{"shallow": {"true"}}, &m); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (c *rtdb) remove(path string) error { return c.do("DELETE", path, nil, nil) }

// what the pages write: the end of a link and the presence of its owner
type shareRecord struct {
	EndsAt int64  `json:"endsAt"` // milliseconds
	State  string `json:"state"`
}
type ownerRecord struct {
	Online bool  `json:"online"`
	At     int64 `json:"at"`
}

// sessionVerdict says whether a session is to be removed now. firstSeen is when the job first found it with no share record
// and no live owner (zero when it has not), the caller keeps it.
func sessionVerdict(share *shareRecord, owner *ownerRecord, firstSeen time.Time, now time.Time, interval time.Duration) (remove bool, seen bool) {
	if share != nil && (share.EndsAt > 0 || share.State == "ended" || share.State == "expired") {
		if share.State == "ended" || share.State == "expired" || share.EndsAt <= now.UnixNano()/1e6 {
			return true, false
		}
		return false, false // a link that is still running
	}
	// no share record: not removed while the owner's page says it is there
	if owner != nil && owner.Online && owner.At > 0 && now.Sub(time.Unix(0, owner.At*1e6)) < sessionOwnerFresh {
		return false, false
	}
	if firstSeen.IsZero() {
		return false, true // seen now; removed when a whole interval has passed and it is still so
	}
	return now.Sub(firstSeen) >= interval, true
}

type cleanupResult struct {
	At       time.Time
	Scanned  int
	Removed  int
	Waiting  int // sessions with no share record that are waiting for their interval
	Errors   int
	Duration time.Duration
	Err      string
}

// runSessionCleanup goes through every session id once. seen is the job's memory of the ids it has found with no share record.
func runSessionCleanup(c *rtdb, now time.Time, seen map[string]time.Time, interval time.Duration) cleanupResult {
	started := time.Now()
	res := cleanupResult{At: now}
	set := map[string]bool{}
	for _, p := range sessionPaths {
		ids, err := c.ids(p)
		if err != nil {
			res.Errors++
			res.Err = err.Error()
			continue
		}
		for _, id := range ids {
			set[id] = true
		}
	}
	all := make([]string, 0, len(set))
	for id := range set {
		all = append(all, id)
	}
	sort.Strings(all)
	if len(all) > sessionCleanupMaxIDs {
		all = all[:sessionCleanupMaxIDs]
	}
	res.Scanned = len(all)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, sessionCleanupParallel)
	still := map[string]time.Time{}
	for _, id := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			var share *shareRecord
			var owner *ownerRecord
			var s shareRecord
			var o ownerRecord
			if err := c.do("GET", "editor_values/"+id+"/share", nil, &s); err != nil {
				mu.Lock()
				res.Errors++
				mu.Unlock()
				return
			} else if s != (shareRecord{}) {
				share = &s
			}
			if err := c.do("GET", "editor_values/"+id+"/owner", nil, &o); err == nil && o != (ownerRecord{}) {
				owner = &o
			}
			mu.Lock()
			first := seen[id]
			mu.Unlock()
			remove, wasSeen := sessionVerdict(share, owner, first, now, interval)
			mu.Lock()
			if remove {
				mu.Unlock()
				failed := false
				for _, p := range sessionPaths {
					if err := c.remove(p + "/" + id); err != nil {
						failed = true
					}
				}
				mu.Lock()
				if failed {
					res.Errors++
				} else {
					res.Removed++
				}
				mu.Unlock()
				return
			}
			if wasSeen {
				if first.IsZero() {
					first = now
				}
				still[id] = first
				res.Waiting++
			}
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	// what is not found any more, or has a share record or a live owner now, is forgotten
	for k := range seen {
		delete(seen, k)
	}
	for k, v := range still {
		seen[k] = v
	}
	res.Duration = time.Since(started)
	return res
}

// ---- the credentials and the loop -----------------------------------------------------------------------------------

const envFirebaseAdmin = "OPENREPL_FIREBASE_ADMIN"

// newSessionCleanupClient builds the database client from the environment, or says why it cannot.
func newSessionCleanupClient() (*rtdb, error) {
	raw := strings.TrimSpace(os.Getenv(envFirebaseAdmin))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(utils.EnvFirestoreCredentials))
	}
	if raw == "" {
		return nil, fmt.Errorf("no service account key: set %s (or %s)", envFirebaseAdmin, utils.EnvFirestoreCredentials)
	}
	tokens, err := persist.NewRTDBTokens(raw)
	if err != nil {
		return nil, err
	}
	cfg, _, _ := utils.FirebaseConfigFromEnv()
	base := strings.TrimSpace(cfg["databaseURL"])
	if base == "" {
		base = "https://" + tokens.ProjectID() + "-default-rtdb.firebaseio.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the database address %q is not usable", base)
	}
	c := &rtdb{base: u.Scheme + "://" + u.Host, query: u.Query(), token: tokens.Token, http: &http.Client{Timeout: sessionCleanupHTTPLimit}}
	return c, nil
}

var (
	sessionCleanupMu     sync.Mutex
	sessionCleanupLast   cleanupResult
	sessionCleanupReason string // why the job is not running, "" when it is
)

func recordCleanup(r cleanupResult) {
	sessionCleanupMu.Lock()
	sessionCleanupLast = r
	sessionCleanupMu.Unlock()
}

// sessionCleanupStatus is what the health panel says of the job.
func sessionCleanupStatus() (status, detail string) {
	sessionCleanupMu.Lock()
	defer sessionCleanupMu.Unlock()
	if sessionCleanupReason != "" {
		return healthWarn, "not running: " + sessionCleanupReason + ". Ended and expired sessions are then removed only by the pages that see them"
	}
	r := sessionCleanupLast
	if r.At.IsZero() {
		return healthOK, "waiting for its first run (5 minutes after the server started, then every " + fmt.Sprint(GetSiteSettings().sessionCleanupInterval()) + ")"
	}
	d := fmt.Sprintf("last run %s: %d sessions looked at, %d removed, %d waiting", r.At.Format("2006-01-02 15:04"), r.Scanned, r.Removed, r.Waiting)
	if r.Errors > 0 {
		return healthWarn, d + fmt.Sprintf(", %d errors (%s)", r.Errors, r.Err)
	}
	return healthOK, d
}

// StartSessionCleanup runs the cleanup every SessionCleanupHours (an admin setting), in the background, for the life of the
// server. A worker does not: the gateway or the single server does it for the project.
func (server *Server) startSessionCleanup() {
	if server.options.Mode == ModeWorker {
		return
	}
	go func() {
		client, err := newSessionCleanupClient()
		sessionCleanupMu.Lock()
		if err != nil {
			sessionCleanupReason = err.Error()
		}
		sessionCleanupMu.Unlock()
		if err != nil {
			log.Printf("session cleanup is off: %v", err)
			return
		}
		seen := map[string]time.Time{}
		last := time.Now().Add(sessionCleanupFirstRun - GetSiteSettings().sessionCleanupInterval())
		for {
			time.Sleep(sessionCleanupCheck)
			interval := GetSiteSettings().sessionCleanupInterval()
			if time.Since(last) < interval {
				continue
			}
			last = time.Now()
			res := runSessionCleanup(client, time.Now(), seen, interval)
			recordCleanup(res)
			log.Printf("session cleanup: %d sessions looked at, %d removed, %d waiting, %d errors in %s %s", res.Scanned, res.Removed, res.Waiting, res.Errors, res.Duration.Round(time.Millisecond), res.Err)
		}
	}()
}
