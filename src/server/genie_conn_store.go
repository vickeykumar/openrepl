package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"user"
)

// Where the connection records are kept (the persistence table of 2026-11-21).
//
// They used to be files in /gottyTraces/genie-connections, a folder that is not in a backup and is lost when a host keeps no disk between deploys
// (every person's sign-in went with it). They are now records of the user database (user_sessions.db, the one of the profiles), which lives
// in MongoDB or Firestore when the site has one and is backed up with the other databases when it is a file:
//
//	genie-conn:<uid>     one record for each person: what they enabled and the sealed credential of each connection
//	genie-conn-req:all   the requests for a connection (one list, at most 500)
//
// Both keys hold a colon, which the list of users and the other readers of this database skip (user/admin.go), like worker-pin:<uid>.
// The old files are read once, imported when the database has nothing for that person, and removed. What stays in the folder is the call log
// (calls-*.jsonl), which is only a record of recent calls and is kept 14 days.

const (
	connKeyPrefix   = "genie-conn:"
	connReqKey      = "genie-conn-req:all"
	connLogKeepDays = 14
)

// connKV is the part of the user database that this file uses. Tests give it an in-memory one.
type connKV interface {
	Fetch(key []byte) ([]byte, error)
	Store(key, value []byte) error
	Delete(key []byte) error
	Commit() error
	Each(fn func(key, value []byte) bool) error
}

// connDB is the database the records are in. Tests replace it.
var connDB = func() connKV { return user.GetUserDBHandle() }

// connUserDoc is the record of one person. A connection that is enabled but not connected has no Connection.
type connUserDoc struct {
	V            int                       `json:"v"`
	UID          string                    `json:"uid"`
	Updated      string                    `json:"updated"`
	Integrations map[string]*connUserInteg `json:"integrations"`
}

type connUserInteg struct {
	Enabled    bool          `json:"enabled"`
	EnabledAt  string        `json:"enabledAt,omitempty"`
	Connection *connUserConn `json:"connection,omitempty"`
}

type connUserConn struct {
	Kind        string `json:"kind,omitempty"` // "" is a pasted token, "oauth" a sign-in
	URL         string `json:"url"`            // the address the credential was given for
	Sealed      string `json:"sealed"`
	Expires     string `json:"expires,omitempty"`
	Account     string `json:"account,omitempty"`
	ConnectedAt string `json:"connectedAt,omitempty"`
	RenewedAt   string `json:"renewedAt,omitempty"`
	Reconnect   bool   `json:"reconnect,omitempty"`
}

func connKey(uid string) []byte { return []byte(connKeyPrefix + uid) }

func loadConnDoc(uid string) *connUserDoc {
	b, err := connDB().Fetch(connKey(uid))
	if err != nil || len(b) == 0 {
		return nil
	}
	var d connUserDoc
	if json.Unmarshal(b, &d) != nil || d.V != 1 {
		return nil
	}
	if d.Integrations == nil {
		d.Integrations = map[string]*connUserInteg{}
	}
	return &d
}

func saveConnDoc(d *connUserDoc) error {
	db := connDB()
	if len(d.Integrations) == 0 {
		if err := db.Delete(connKey(d.UID)); !isNotFound(err) {
			return err
		}
		return db.Commit()
	}
	d.V, d.Updated = 1, time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(d)
	if err := db.Store(connKey(d.UID), b); err != nil {
		return err
	}
	return db.Commit()
}

// isNotFound: the databases answer a missing key in their own words (unqlite says it in its error text).
func isNotFound(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "no such"))
}

// the old file of a person: where they were before the database
func legacyConnFile(uid string) string {
	return connFileOf(uid)
}

func (c *connUserConn) toRecord(uid, id string) connRecord {
	return connRecord{User: uid, ID: id, URL: c.URL, Sealed: c.Sealed, At: c.ConnectedAt, Kind: c.Kind, Expires: c.Expires, Reconnect: c.Reconnect}
}

// readConnRecords gives the connected services of a person (a record that is only enabled is not one of them). A person the database does
// not know yet, whose old file is there, is imported from it.
func readConnRecords(uid string) []connRecord {
	d := loadConnDoc(uid)
	if d == nil {
		if recs := readLegacyConnFile(uid); len(recs) > 0 {
			if err := writeConnRecords(uid, recs); err == nil {
				os.Remove(legacyConnFile(uid))
			}
			d = loadConnDoc(uid)
		}
	}
	if d == nil {
		return nil
	}
	ids := make([]string, 0, len(d.Integrations))
	for id, e := range d.Integrations {
		if e.Connection != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]connRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, d.Integrations[id].Connection.toRecord(uid, id))
	}
	return out
}

func readLegacyConnFile(uid string) []connRecord {
	b, err := os.ReadFile(legacyConnFile(uid))
	if err != nil {
		return nil
	}
	var recs []connRecord
	json.Unmarshal(b, &recs)
	return recs
}

// writeConnRecords sets the connections of a person to recs: a service that is in the list is enabled and connected, one that is not loses its
// connection (and the record of it goes when it was only the connection that kept it).
func writeConnRecords(uid string, recs []connRecord) error {
	d := loadConnDoc(uid)
	if d == nil {
		d = &connUserDoc{V: 1, UID: uid, Integrations: map[string]*connUserInteg{}}
	}
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.ID] = true
		e := d.Integrations[r.ID]
		if e == nil {
			e = &connUserInteg{Enabled: true, EnabledAt: r.At}
			d.Integrations[r.ID] = e
		}
		conn := &connUserConn{Kind: r.Kind, URL: r.URL, Sealed: r.Sealed, Expires: r.Expires, ConnectedAt: r.At, Reconnect: r.Reconnect}
		if e.Connection != nil {
			conn.Account, conn.RenewedAt = e.Connection.Account, e.Connection.RenewedAt // what this file's callers do not set stays
		}
		e.Connection = conn
	}
	for id, e := range d.Integrations {
		if !seen[id] {
			e.Connection = nil
			if !e.Enabled {
				delete(d.Integrations, id)
			}
		}
	}
	return saveConnDoc(d)
}

// ---- the requests ----------------------------------------------------------------------------------------------------------------

func readConnRequests() []connRequest {
	b, err := connDB().Fetch([]byte(connReqKey))
	if err != nil || len(b) == 0 {
		// the old file, once
		if old, ferr := os.ReadFile(legacyReqFile()); ferr == nil {
			var r []connRequest
			if json.Unmarshal(old, &r) == nil && len(r) > 0 {
				if writeConnRequests(r) == nil {
					os.Remove(legacyReqFile())
				}
				return r
			}
		}
		return nil
	}
	var r []connRequest
	json.Unmarshal(b, &r)
	return r
}

func writeConnRequests(r []connRequest) error {
	db := connDB()
	b, _ := json.Marshal(r)
	if err := db.Store([]byte(connReqKey), b); err != nil {
		return err
	}
	return db.Commit()
}

func legacyReqFile() string { return filepath.Join(connDir, "requests.json") }

// ---- who is connected, for the admin --------------------------------------------------------------------------------------------

type connWho struct {
	User string `json:"user"`
	Conn string `json:"conn"`
	At   string `json:"at"`
}

// connectedPeople lists who is connected to what: never the credentials.
func connectedPeople() []connWho {
	people := []connWho{}
	connDB().Each(func(key, value []byte) bool {
		if !strings.HasPrefix(string(key), connKeyPrefix) {
			return true
		}
		var d connUserDoc
		if json.Unmarshal(value, &d) != nil {
			return true
		}
		for id, e := range d.Integrations {
			if e.Connection != nil {
				people = append(people, connWho{d.UID, id, e.Connection.ConnectedAt})
			}
		}
		return true
	})
	sort.Slice(people, func(i, j int) bool { return people[i].At > people[j].At })
	return people
}

// ---- the call log: daily files, kept 14 days ---------------------------------------------------------------------------------------

var lastLogPrune string

// pruneCallLogs removes the daily files older than connLogKeepDays, once a day. The caller holds connLogMu.
func pruneCallLogs(now time.Time) {
	day := now.UTC().Format("2006-01-02")
	if lastLogPrune == day {
		return
	}
	lastLogPrune = day
	entries, err := os.ReadDir(connDir)
	if err != nil {
		return
	}
	cut := now.UTC().AddDate(0, 0, -connLogKeepDays).Format("2006-01-02")
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "calls-") && strings.HasSuffix(n, ".jsonl") && n[len("calls-"):len(n)-len(".jsonl")] < cut {
			os.Remove(filepath.Join(connDir, n))
		}
	}
}
