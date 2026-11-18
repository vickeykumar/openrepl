package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cookie"
)

// Practice in the workspace (docs/lld/06-frontend.md, "Practice in the workspace"). A signed-in user's practice list and the code they
// wrote for each question live in their own workspace, in the hidden folder .practice, so they follow the workspace (snapshots, other
// browsers) and no server database holds them:
//
//	.practice/state.json                  the done and deleted marks ({id: {done, del, t}})
//	.practice/questions/<id>.json         a question as the browser sent it (its description and starter code for each language)
//	.practice/code/<id>/<language>.txt    the code the user wrote for that question in that language (its modified time is when)
//
//	POST /practice_ws {"op":"doc"}                                   the list: questions, marks
//	POST /practice_ws {"op":"merge","doc":{questions,state}}         merge the browser's list into it, answer with the result
//	POST /practice_ws {"op":"code_get","id":"1731"}                  the code of every language of one question
//	POST /practice_ws {"op":"code_put","id":"1731","language":"Python","code":"…","t":1731900000000}
//
// The merge is the one of /practice/progress (mergePractice): the newer mark and the newer copy of a question win. The first time a user
// has no .practice folder, what the server's practice database holds for them is merged in (once; that record is left as it is).
// Only the owner of a workspace reaches it (a shared session's viewer is refused). It is execution-bound in the gateway like
// genie_chats, because the workspace is on the node that runs it. The admin can switch it off (PracticeWorkspaceOff): the page then uses
// /practice/progress as before.

const (
	practiceDirName       = ".practice"
	practiceMaxCode       = 64 << 10 // bytes of one solution
	practiceMaxCodeTotal  = 16 << 20 // bytes of all the solutions of a workspace
	practiceMaxLanguages  = 40       // languages kept for one question
	practiceWSPerMinute   = 240      // calls a minute for one person
	practiceWSMaxBody     = 2 << 20  // bytes in one call
	practiceFutureSlackMs = 24 * 3600 * 1000
)

var practiceLanguagePattern = regexp.MustCompile(`^[A-Za-z0-9+#_-]{1,24}$`)

type practiceWSRequest struct {
	Op       string       `json:"op"`
	Doc      *practiceDoc `json:"doc,omitempty"`
	ID       string       `json:"id,omitempty"`
	Language string       `json:"language,omitempty"`
	Code     string       `json:"code,omitempty"`
	T        int64        `json:"t,omitempty"`
}

var (
	practiceWSMu     sync.Mutex
	practiceWSLocks  = map[string]*sync.Mutex{}
	practiceWSRateMu sync.Mutex
	practiceWSRate   = map[string][]time.Time{}
)

// practiceWSLock is the lock of one workspace: two calls for the same one do not write at the same time.
func practiceWSLock(home string) *sync.Mutex {
	practiceWSMu.Lock()
	defer practiceWSMu.Unlock()
	l, ok := practiceWSLocks[home]
	if !ok {
		l = &sync.Mutex{}
		practiceWSLocks[home] = l
	}
	return l
}

func practiceWSAllowed(who string, now time.Time) bool {
	practiceWSRateMu.Lock()
	defer practiceWSRateMu.Unlock()
	recent := practiceWSRate[who][:0]
	for _, t := range practiceWSRate[who] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= practiceWSPerMinute {
		practiceWSRate[who] = recent
		return false
	}
	practiceWSRate[who] = append(recent, now)
	return true
}

// practiceFolder is the .practice folder of a workspace; made when asked, and never reached through a link out of the workspace
// (it and its sub folders must be folders of the workspace itself, checked before anything is made).
func practiceFolder(realHome string, create bool) (string, error) {
	dir := filepath.Join(realHome, practiceDirName)
	for _, d := range []string{dir, filepath.Join(dir, "questions"), filepath.Join(dir, "code")} {
		if st, err := os.Lstat(d); err == nil && !st.IsDir() {
			return "", os.ErrInvalid
		}
	}
	if create {
		for _, d := range []string{filepath.Join(dir, "questions"), filepath.Join(dir, "code")} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// loadPracticeFiles reads the list from the files; a file that cannot be read, or that is not a question, is left out.
func loadPracticeFiles(dir string) practiceDoc {
	d := newPracticeDoc()
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		var st struct {
			State   map[string]practiceState `json:"state"`
			Updated int64                    `json:"updated"`
		}
		if json.Unmarshal(b, &st) == nil {
			for id, s := range st.State {
				if practiceIDPattern.MatchString(id) {
					d.State[id] = s
				}
			}
			d.Updated = st.Updated
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "questions"))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || !e.Type().IsRegular() {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !practiceIDPattern.MatchString(id) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "questions", name))
		if err != nil || len(b) > practiceMaxQuestion || len(b) < 2 || b[0] != '{' || !json.Valid(b) {
			continue
		}
		d.Questions[id] = json.RawMessage(b)
	}
	return d
}

// storePracticeFiles writes the list: the marks whole, each question that changed, and removes the files of the questions that are gone
// (with the code written for them). A file whose text is the same is not written again, so the snapshots of the workspace do not see a change.
func storePracticeFiles(dir string, d practiceDoc) error {
	state, err := json.Marshal(map[string]interface{}{"v": 1, "state": d.State, "updated": d.Updated})
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil || string(old) != string(state) {
		if err := writeFileAtomic(filepath.Join(dir, "state.json"), state); err != nil {
			return err
		}
	}
	for id, q := range d.Questions {
		p := filepath.Join(dir, "questions", id+".json")
		if old, err := os.ReadFile(p); err == nil && string(old) == string(q) {
			continue
		}
		if err := writeFileAtomic(p, q); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "questions"))
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if _, live := d.Questions[id]; !live && strings.HasSuffix(e.Name(), ".json") {
			os.Remove(filepath.Join(dir, "questions", e.Name()))
		}
	}
	codes, _ := os.ReadDir(filepath.Join(dir, "code"))
	for _, e := range codes {
		if _, live := d.Questions[e.Name()]; !live && e.IsDir() {
			os.RemoveAll(filepath.Join(dir, "code", e.Name()))
		}
	}
	return nil
}

// practiceCodeSize is the bytes of all the solutions of a workspace.
func practiceCodeSize(dir string) int64 {
	var n int64
	filepath.Walk(filepath.Join(dir, "code"), func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

type practiceCode struct {
	Code string `json:"code"`
	T    int64  `json:"t"`
}

func (server *Server) handlePracticeWS(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if GetSiteSettings().PracticeWorkspaceOff {
		jsonError(rw, http.StatusForbidden, "off", "Practice is not saved in the workspace on this site.")
		return
	}
	req.ParseForm()
	if homeOverridden(req) {
		jsonError(rw, http.StatusForbidden, "not_owner", "Only the owner of a workspace has practice in it.")
		return
	}
	uid, home := server.requestIdentity(rw, req)
	if !cookie.Is_UserLoggedIn(req) || uid == "" {
		jsonError(rw, http.StatusUnauthorized, "signed_out", "Sign in to save your practice in your workspace.")
		return
	}
	var in practiceWSRequest
	if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, practiceWSMaxBody)).Decode(&in); err != nil {
		jsonError(rw, http.StatusBadRequest, "bad_request", "send {\"op\": ...}")
		return
	}
	if !practiceWSAllowed(uid, time.Now()) {
		jsonError(rw, http.StatusTooManyRequests, "rate", "Too many saves in a minute: wait a little.")
		return
	}
	os.MkdirAll(home, 0o755)
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		jsonError(rw, http.StatusNotFound, "no_workspace", "The workspace is not there yet.")
		return
	}
	lock := practiceWSLock(realHome)
	lock.Lock()
	defer lock.Unlock()
	reply := func(v interface{}) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(v)
	}
	existed := false
	if st, err := os.Lstat(filepath.Join(realHome, practiceDirName)); err == nil && st.IsDir() {
		existed = true
	}
	writes := in.Op == "merge" || in.Op == "code_put"
	dir, err := practiceFolder(realHome, writes)
	if err != nil {
		jsonError(rw, http.StatusInternalServerError, "folder", "The practice folder could not be used.")
		return
	}
	// the list of a user who has no folder yet starts as what the server's practice database holds for them (once)
	base := func() practiceDoc {
		if existed {
			return loadPracticeFiles(dir)
		}
		practiceDBMu.Lock()
		defer practiceDBMu.Unlock()
		if practice_db_handle == nil {
			return newPracticeDoc()
		}
		return loadPractice(uid)
	}
	switch in.Op {
	case "doc":
		d := base()
		reply(practiceReply{d, true})
	case "merge":
		if in.Doc == nil {
			jsonError(rw, http.StatusBadRequest, "bad_request", "send the list in \"doc\"")
			return
		}
		clean, ok := validPractice(*in.Doc)
		if !ok {
			jsonError(rw, http.StatusRequestEntityTooLarge, "too_many", "The practice list has too many questions.")
			return
		}
		merged := mergePractice(base(), clean)
		if err := storePracticeFiles(dir, merged); err != nil {
			log.Println("ERROR: storing practice in the workspace:", err.Error())
			jsonError(rw, http.StatusServiceUnavailable, "write", "Couldn't save your practice. Try again later.")
			return
		}
		reply(practiceReply{merged, true})
	case "code_get":
		if !practiceIDPattern.MatchString(in.ID) {
			jsonError(rw, http.StatusBadRequest, "bad_id", "That is not a question id.")
			return
		}
		out := map[string]practiceCode{}
		entries, _ := os.ReadDir(filepath.Join(dir, "code", in.ID))
		for _, e := range entries {
			lang := strings.TrimSuffix(e.Name(), ".txt")
			if !strings.HasSuffix(e.Name(), ".txt") || !e.Type().IsRegular() || !practiceLanguagePattern.MatchString(lang) {
				continue
			}
			p := filepath.Join(dir, "code", in.ID, e.Name())
			b, err := os.ReadFile(p)
			info, err2 := os.Stat(p)
			if err != nil || err2 != nil || len(b) > practiceMaxCode || !utf8.Valid(b) {
				continue
			}
			out[lang] = practiceCode{Code: string(b), T: info.ModTime().UnixNano() / int64(time.Millisecond)}
		}
		reply(map[string]interface{}{"id": in.ID, "code": out})
	case "code_put":
		if !practiceIDPattern.MatchString(in.ID) || !practiceLanguagePattern.MatchString(in.Language) {
			jsonError(rw, http.StatusBadRequest, "bad_request", "A question id and a language.")
			return
		}
		if len(in.Code) > practiceMaxCode || !utf8.ValidString(in.Code) || strings.ContainsRune(in.Code, 0) {
			jsonError(rw, http.StatusRequestEntityTooLarge, "too_large", "The code is too large to save (64 KB).")
			return
		}
		q := filepath.Join(dir, "questions", in.ID+".json")
		if st, err := os.Lstat(q); err != nil || !st.Mode().IsRegular() {
			// the question is saved first: code is kept for a question of the list only
			jsonError(rw, http.StatusConflict, "no_question", "That question is not in your saved list yet.")
			return
		}
		cdir := filepath.Join(dir, "code", in.ID)
		if st, err := os.Lstat(cdir); err == nil && !st.IsDir() {
			jsonError(rw, http.StatusInternalServerError, "folder", "The practice folder could not be used.")
			return
		}
		p := filepath.Join(cdir, in.Language+".txt")
		_, statErr := os.Lstat(p)
		isNew := os.IsNotExist(statErr)
		if isNew {
			if n, _ := os.ReadDir(cdir); len(n) >= practiceMaxLanguages || practiceCodeSize(dir)+int64(len(in.Code)) > practiceMaxCodeTotal {
				jsonError(rw, http.StatusRequestEntityTooLarge, "quota", "Your saved solutions are over their limit.")
				return
			}
		}
		if err := os.MkdirAll(cdir, 0o700); err != nil {
			jsonError(rw, http.StatusInternalServerError, "folder", "The practice folder could not be used.")
			return
		}
		if old, err := os.ReadFile(p); err != nil || string(old) != in.Code {
			if err := writeFileAtomic(p, []byte(in.Code)); err != nil {
				jsonError(rw, http.StatusServiceUnavailable, "write", "Couldn't save the code. Try again later.")
				return
			}
		}
		now := time.Now()
		mt := now
		if in.T > 0 {
			mt = time.Unix(0, in.T*int64(time.Millisecond))
			if mt.After(now.Add(practiceFutureSlackMs * time.Millisecond)) {
				mt = now
			}
		}
		os.Chtimes(p, mt, mt)
		reply(map[string]interface{}{"ok": true, "t": mt.UnixNano() / int64(time.Millisecond)})
	default:
		jsonError(rw, http.StatusBadRequest, "bad_op", "doc, merge, code_get or code_put.")
	}
}
