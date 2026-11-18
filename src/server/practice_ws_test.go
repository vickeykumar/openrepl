package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"persist"
)

type practiceEnv struct {
	s    *Server
	c    *http.Cookie
	home string
}

func practiceWSSetup(t *testing.T, site SiteSettings) *practiceEnv {
	t.Helper()
	s, _ := adminTestServer(t)
	if err := SaveSiteSettings(site); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, "uid-ann", "ann@example.com")
	probe := httptest.NewRequest("POST", "/practice_ws", nil)
	probe.AddCookie(c)
	_, home := s.requestIdentity(httptest.NewRecorder(), probe)
	if home == "" {
		t.Skip("no workspace for the test user")
	}
	os.MkdirAll(home, 0o755)
	os.RemoveAll(filepath.Join(home, practiceDirName))
	practiceWSRateMu.Lock()
	practiceWSRate = map[string][]time.Time{}
	practiceWSRateMu.Unlock()
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, practiceDirName)) })
	return &practiceEnv{s: s, c: c, home: home}
}

func (e *practiceEnv) call(t *testing.T, body string, c *http.Cookie) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/practice_ws", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.s.handlePracticeWS(w, req)
	var out map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *practiceEnv) ok(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	code, out := e.call(t, body, e.c)
	if code != 200 {
		t.Fatalf("%s: %d %v", body, code, out)
	}
	return out
}

const q1 = `{"id":"q1","name":"Two Sum","nameHyphenated":"two-sum","topic":"Arrays","difficulty":"Easy","added":1000,"updated":1000}`

func TestThePracticeListIsKeptInTheWorkspaceAndComesBack(t *testing.T) {
	e := practiceWSSetup(t, SiteSettings{})
	out := e.ok(t, `{"op":"merge","doc":{"questions":{"q1":`+q1+`},"state":{"q1":{"done":true,"t":5000}}}}`)
	if out["signedIn"] != true || len(out["questions"].(map[string]interface{})) != 1 {
		t.Fatalf("the merge answers with the list: %v", out)
	}
	for _, f := range []string{"state.json", "questions/q1.json"} {
		if _, err := os.Stat(filepath.Join(e.home, practiceDirName, f)); err != nil {
			t.Errorf("%s was not written: %v", f, err)
		}
	}
	got := e.ok(t, `{"op":"doc"}`)
	st := got["state"].(map[string]interface{})["q1"].(map[string]interface{})
	if st["done"] != true || got["questions"].(map[string]interface{})["q1"] == nil {
		t.Fatalf("what comes back: %v", got)
	}
	// the newer copy of a question wins, an older one does not
	e.ok(t, `{"op":"merge","doc":{"questions":{"q1":{"id":"q1","name":"Two Sum II","added":1000,"updated":9000}}}}`)
	e.ok(t, `{"op":"merge","doc":{"questions":{"q1":{"id":"q1","name":"Old","added":1000,"updated":500}}}}`)
	b, _ := os.ReadFile(filepath.Join(e.home, practiceDirName, "questions", "q1.json"))
	if !strings.Contains(string(b), "Two Sum II") {
		t.Errorf("the newer copy stays: %s", b)
	}
}

func TestSolutionsAreKeptForEachQuestionAndLanguage(t *testing.T) {
	e := practiceWSSetup(t, SiteSettings{})
	// code is kept for a question that is in the list
	if code, _ := e.call(t, `{"op":"code_put","id":"q1","language":"Python","code":"x"}`, e.c); code != http.StatusConflict {
		t.Errorf("code for a question that is not saved: %d", code)
	}
	e.ok(t, `{"op":"merge","doc":{"questions":{"q1":`+q1+`}}}`)
	e.ok(t, `{"op":"code_put","id":"q1","language":"Python","code":"def two_sum():\n    return [0, 1]\n","t":1731900000000}`)
	e.ok(t, `{"op":"code_put","id":"q1","language":"C++","code":"int main() {}","t":1731900001000}`)
	got := e.ok(t, `{"op":"code_get","id":"q1"}`)["code"].(map[string]interface{})
	py := got["Python"].(map[string]interface{})
	if !strings.Contains(py["code"].(string), "two_sum") || int64(py["t"].(float64)) != 1731900000000 || got["C++"] == nil {
		t.Fatalf("each language comes back with its time: %v", got)
	}
	b, err := os.ReadFile(filepath.Join(e.home, practiceDirName, "code", "q1", "Python.txt"))
	if err != nil || !strings.Contains(string(b), "return [0, 1]") {
		t.Errorf("a plain file the user can read: %v %q", err, b)
	}
	// a question that is deleted takes its code with it
	e.ok(t, `{"op":"merge","doc":{"state":{"q1":{"del":true,"t":9999999999999}}}}`)
	if _, err := os.Stat(filepath.Join(e.home, practiceDirName, "code", "q1")); !os.IsNotExist(err) {
		t.Errorf("the code of a deleted question is removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.home, practiceDirName, "questions", "q1.json")); !os.IsNotExist(err) {
		t.Errorf("and the question: %v", err)
	}
}

func TestThePracticeFolderIsNotLeftByAnId(t *testing.T) {
	e := practiceWSSetup(t, SiteSettings{})
	e.ok(t, `{"op":"merge","doc":{"questions":{"q1":`+q1+`}}}`)
	for _, body := range []string{
		`{"op":"code_put","id":"../x","language":"Python","code":"x"}`,
		`{"op":"code_put","id":"q1","language":"../../etc","code":"x"}`,
		`{"op":"code_put","id":"q1","language":"Py/thon","code":"x"}`,
		`{"op":"code_get","id":"../.."}`,
		`{"op":"code_put","id":"q1","language":"Python","code":"` + strings.Repeat("a", practiceMaxCode+1) + `"}`,
	} {
		if code, _ := e.call(t, body, e.c); code < 400 {
			t.Errorf("%.60s: %d", body, code)
		}
	}
	// a question id that is not a plain name is not written as a file
	e.ok(t, `{"op":"merge","doc":{"questions":{"../escape":`+q1+`}}}`)
	if _, err := os.Stat(filepath.Join(e.home, "escape.json")); err == nil {
		t.Error("an id with a slash was written")
	}
}

func TestOnlyTheSignedInOwnerReachesThePracticeFolder(t *testing.T) {
	e := practiceWSSetup(t, SiteSettings{})
	if code, _ := e.call(t, `{"op":"doc"}`, nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
	off := practiceWSSetup(t, SiteSettings{PracticeWorkspaceOff: true})
	if code, out := off.call(t, `{"op":"doc"}`, off.c); code != http.StatusForbidden || out["error"].(map[string]interface{})["code"] != "off" {
		t.Errorf("switched off: %d %v", code, out)
	}
}

func TestThePracticeRecordOfTheServerIsTakenInOnce(t *testing.T) {
	e := practiceWSSetup(t, SiteSettings{})
	practiceDBMu.Lock()
	had := practice_db_handle
	h, err := persist.Open(filepath.Join(t.TempDir(), "practice.db"))
	if err != nil {
		practiceDBMu.Unlock()
		t.Skip("no practice database in this test: " + err.Error())
	}
	practice_db_handle = h
	practiceDBMu.Unlock()
	t.Cleanup(func() {
		practiceDBMu.Lock()
		practice_db_handle.Close()
		practice_db_handle = had
		practiceDBMu.Unlock()
	})
	practiceDBMu.Lock()
	d := newPracticeDoc()
	d.Questions["old1"] = json.RawMessage(`{"id":"old1","name":"From the server","added":10,"updated":10}`)
	err = storePractice("uid-ann", d)
	practiceDBMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := e.ok(t, `{"op":"doc"}`)
	if got["questions"].(map[string]interface{})["old1"] == nil {
		t.Fatalf("the list the server held is offered: %v", got)
	}
	e.ok(t, `{"op":"merge","doc":{"questions":{"q1":`+q1+`}}}`)
	// the folder exists now: the server's record is not read again
	practiceDBMu.Lock()
	d.Questions["old2"] = json.RawMessage(`{"id":"old2","name":"Later on the server","added":20,"updated":20}`)
	storePractice("uid-ann", d)
	practiceDBMu.Unlock()
	got = e.ok(t, `{"op":"doc"}`)
	qs := got["questions"].(map[string]interface{})
	if qs["old1"] == nil || qs["q1"] == nil || qs["old2"] != nil {
		t.Fatalf("taken in once: %v", qs)
	}
}
