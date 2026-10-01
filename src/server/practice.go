package server

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cookie"
	"github.com/nobonobo/unqlitego"
	"user"
	"utils"
)

// Practice progress (T17). A signed-in visitor's practice questions, and which
// ones are done, are kept on the server so the list follows them to other
// browsers. The browser keeps its own copy (js/practice-store.js) and sends it
// here; the server merges it into the stored copy and returns the result.
//
//	GET  /practice/progress  the stored document, or {"signedIn": false}
//	PUT  /practice/progress  merge the body into it (POST is accepted too)
//
// The user comes from the session cookie and is checked against the session
// DB. /login still trusts the client (launch blocker B2): until that is fixed,
// a caller who knows someone's uid could read or change their practice list.

const PRACTICE_DB = utils.GOTTY_PATH + "/practice.db"

const (
	practiceMaxBody      = 2 << 20  // bytes in one request
	practiceMaxQuestion  = 64 << 10 // bytes in one question
	practiceMaxQuestions = 200      // questions kept per user
	practiceMaxStates    = 1000     // done/deleted marks kept per user
	practiceRateLimit    = 240      // writes one user can make ...
	practiceRateSpan     = 10 * time.Minute
)

var (
	practice_db_handle *unqlitego.Database
	practiceDBMu       sync.Mutex
	practiceIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

// practiceState is one question's mark: done, or deleted (a tombstone, so a
// question removed in one browser doesn't come back from another). T is the
// time of the change in ms; the newer change wins.
type practiceState struct {
	Done bool  `json:"done,omitempty"`
	Del  bool  `json:"del,omitempty"`
	T    int64 `json:"t"`
}

// practiceDoc holds questions by id. A question is kept as the browser sent
// it; only its "added" and "updated" times are read, to pick the newer copy.
type practiceDoc struct {
	Questions map[string]json.RawMessage `json:"questions"`
	State     map[string]practiceState   `json:"state"`
	Updated   int64                      `json:"updated"`
}

func newPracticeDoc() practiceDoc {
	return practiceDoc{Questions: map[string]json.RawMessage{}, State: map[string]practiceState{}}
}

func InitPracticeDBHandle() {
	var err error
	practice_db_handle, err = unqlitego.NewDatabase(PRACTICE_DB)
	if err != nil {
		log.Println("ERROR: Error while creating practice DB handle : ", err.Error())
		practice_db_handle = nil // progress stays in the browser, the rest of the site works
		return
	}
	log.Println("Successfully initialized practice handle: ", practice_db_handle)
}

func ClosePracticeDBHandle() {
	practiceDBMu.Lock()
	defer practiceDBMu.Unlock()
	if practice_db_handle == nil {
		return
	}
	if err := practice_db_handle.Close(); err != nil {
		log.Println("ERROR: Error while closing practice DB handle : ", err.Error())
	}
	practice_db_handle = nil
}

// ---- merge ----------------------------------------------------------------------

func questionVersion(raw json.RawMessage) int64 {
	var v struct {
		Added   int64 `json:"added"`
		Updated int64 `json:"updated"`
	}
	json.Unmarshal(raw, &v)
	if v.Updated > v.Added {
		return v.Updated
	}
	return v.Added
}

// mergePractice combines two documents: for each id the newer mark and the
// newer copy of the question win, and deleted questions are dropped.
func mergePractice(a, b practiceDoc) practiceDoc {
	out := newPracticeDoc()
	for id, s := range a.State {
		out.State[id] = s
	}
	for id, s := range b.State {
		if cur, ok := out.State[id]; !ok || s.T > cur.T || (s.T == cur.T && s.Del && !cur.Del) {
			out.State[id] = s
		}
	}
	for _, d := range []practiceDoc{a, b} {
		for id, q := range d.Questions {
			if cur, ok := out.Questions[id]; !ok || questionVersion(q) > questionVersion(cur) {
				out.Questions[id] = q
			}
		}
	}
	for id := range out.Questions {
		if out.State[id].Del {
			delete(out.Questions, id)
		}
	}
	trimPractice(&out)
	if a.Updated > b.Updated {
		out.Updated = a.Updated
	} else {
		out.Updated = b.Updated
	}
	return out
}

// trimPractice keeps the newest questions and marks within the limits.
func trimPractice(d *practiceDoc) {
	now := time.Now().UnixNano() / int64(time.Millisecond)
	if len(d.Questions) > practiceMaxQuestions {
		ids := make([]string, 0, len(d.Questions))
		for id := range d.Questions {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return questionVersion(d.Questions[ids[i]]) > questionVersion(d.Questions[ids[j]])
		})
		for _, id := range ids[practiceMaxQuestions:] {
			delete(d.Questions, id)
			d.State[id] = practiceState{Del: true, T: now}
		}
	}
	if len(d.State) > practiceMaxStates {
		// forget the oldest tombstones first, then marks of questions that are
		// gone; marks of questions still in the list always stay
		ids := make([]string, 0, len(d.State))
		for id := range d.State {
			if _, live := d.Questions[id]; !live {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			si, sj := d.State[ids[i]], d.State[ids[j]]
			if si.Del != sj.Del {
				return si.Del
			}
			return si.T < sj.T
		})
		for _, id := range ids {
			if len(d.State) <= practiceMaxStates {
				break
			}
			delete(d.State, id)
		}
	}
}

// validPractice drops anything malformed from a document sent by a browser.
func validPractice(in practiceDoc) (practiceDoc, bool) {
	out := newPracticeDoc()
	if len(in.Questions) > practiceMaxQuestions || len(in.State) > practiceMaxStates {
		return out, false
	}
	limit := time.Now().Add(24*time.Hour).UnixNano() / int64(time.Millisecond)
	for id, q := range in.Questions {
		if !practiceIDPattern.MatchString(id) || len(q) > practiceMaxQuestion || len(q) < 2 || q[0] != '{' {
			continue
		}
		out.Questions[id] = q
	}
	for id, s := range in.State {
		if !practiceIDPattern.MatchString(id) || s.T < 0 {
			continue
		}
		if s.T > limit {
			s.T = limit
		}
		out.State[id] = s
	}
	out.Updated = time.Now().UnixNano() / int64(time.Millisecond)
	return out, true
}

// ---- storage --------------------------------------------------------------------

func practiceKey(uid string) []byte { return []byte("u:" + uid) }

// loadPractice needs practiceDBMu held.
func loadPractice(uid string) practiceDoc {
	d := newPracticeDoc()
	if practice_db_handle == nil {
		return d
	}
	data, err := practice_db_handle.Fetch(practiceKey(uid))
	if err != nil || len(data) == 0 {
		return d
	}
	if json.Unmarshal(data, &d) != nil {
		return newPracticeDoc()
	}
	if d.Questions == nil {
		d.Questions = map[string]json.RawMessage{}
	}
	if d.State == nil {
		d.State = map[string]practiceState{}
	}
	return d
}

// storePractice needs practiceDBMu held.
func storePractice(uid string, d practiceDoc) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := practice_db_handle.Store(practiceKey(uid), data); err != nil {
		return err
	}
	return practice_db_handle.Commit()
}

// ---- rate limit per user ------------------------------------------------------------

var (
	practiceRate   = map[string]*rateWindow{}
	practiceRateMu sync.Mutex
)

func allowPracticeWrite(uid string) bool {
	practiceRateMu.Lock()
	defer practiceRateMu.Unlock()
	now := time.Now()
	if len(practiceRate) > 10000 {
		for k, w := range practiceRate {
			if now.Sub(w.start) > practiceRateSpan {
				delete(practiceRate, k)
			}
		}
	}
	w, ok := practiceRate[uid]
	if !ok || now.Sub(w.start) > practiceRateSpan {
		practiceRate[uid] = &rateWindow{start: now, count: 1}
		return true
	}
	if w.count >= practiceRateLimit {
		return false
	}
	w.count++
	return true
}

// ---- handler ------------------------------------------------------------------------

// practiceUser returns the signed-in user's uid from a live session.
func practiceUser(r *http.Request) (string, bool) {
	uid := cookie.Get_Uid(r)
	if uid == "" || !cookie.Is_UserLoggedIn(r) {
		return "", false
	}
	if user.IsSessionExpired(uid, cookie.Get_SessionID(r)) {
		return "", false
	}
	return uid, true
}

// practiceReply is a document plus whether the visitor is signed in.
type practiceReply struct {
	practiceDoc
	SignedIn bool `json:"signedIn"`
}

func handlePracticeProgress(w http.ResponseWriter, r *http.Request) {
	uid, ok := practiceUser(r)
	if !ok {
		if r.Method == http.MethodGet {
			// not an error: the page keeps the list in the browser
			writeSnippetJSON(w, http.StatusOK, map[string]bool{"signedIn": false})
			return
		}
		writeSnippetJSON(w, http.StatusUnauthorized, map[string]string{"error": "Sign in to save your progress to your account."})
		return
	}
	switch r.Method {
	case http.MethodGet:
		practiceDBMu.Lock()
		if practice_db_handle == nil {
			practiceDBMu.Unlock()
			writeSnippetJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Saving progress isn't available right now."})
			return
		}
		d := loadPractice(uid)
		practiceDBMu.Unlock()
		writeSnippetJSON(w, http.StatusOK, practiceReply{d, true})
	case http.MethodPut, http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, practiceMaxBody)
		var in practiceDoc
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			if strings.Contains(err.Error(), "too large") {
				writeSnippetJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "The practice list is too large to save."})
			} else {
				writeSnippetJSON(w, http.StatusBadRequest, map[string]string{"error": "The practice list isn't valid."})
			}
			return
		}
		in, ok := validPractice(in)
		if !ok {
			writeSnippetJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "The practice list has too many questions."})
			return
		}
		if !allowPracticeWrite(uid) {
			writeSnippetJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many saves. Try again in a few minutes."})
			return
		}
		practiceDBMu.Lock()
		if practice_db_handle == nil {
			practiceDBMu.Unlock()
			writeSnippetJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Saving progress isn't available right now."})
			return
		}
		merged := mergePractice(loadPractice(uid), in)
		err := storePractice(uid, merged)
		practiceDBMu.Unlock()
		if err != nil {
			log.Println("ERROR: storing practice progress: ", err.Error())
			writeSnippetJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Couldn't save your progress. Try again later."})
			return
		}
		writeSnippetJSON(w, http.StatusOK, practiceReply{merged, true})
	default:
		w.Header().Set("Allow", "GET, PUT, POST")
		writeSnippetJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Use GET or PUT."})
	}
}
