package server

import (
	"persist"
	"strings"
	"testing"
)

// The lists are kept for a while in production (scancaches.go); the tests change data and look at
// once, so a change shows on the next read.
func init() {
	feedbackScan.MinInterval = 0
	snippetScan.MinInterval = 0
	blogScan.MinInterval = 0
	tokenScanMinInterval = 0
	tokenTotals.scan.MinInterval = 0
}

// The dashboard asks for the same lists every few seconds. Each is a scan of a whole collection, so it is
// made once and kept; Refresh (?fresh=1) asks for a new one.
func TestTheAdminListsAreScannedOnceNotOnEveryRefresh(t *testing.T) {
	_, h := adminTestServer(t)
	withFeedbackDB(t, map[string]feedback{"1790000000000000001": {Name: "Ann", Message: "hi"}})
	before := feedbackScan.Scans()
	for i := 0; i < 20; i++ {
		if w := get(h, "/admin/feedback"); w.Code != 200 {
			t.Fatalf("feedback: %d", w.Code)
		}
	}
	if got := feedbackScan.Scans() - before; got != 1 {
		t.Errorf("20 refreshes made %d scans, want 1", got)
	}
	if w := get(h, "/admin/feedback?fresh=1"); w.Code != 200 {
		t.Fatalf("fresh: %d", w.Code)
	}
	if got := feedbackScan.Scans() - before; got != 2 {
		t.Errorf("Refresh made %d scans in all, want 2", got)
	}
	// a message that arrives is in the next list (this server's own change drops the kept one)
	if err := StoreFeedbackData(&feedback{Name: "New", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	var r struct {
		Feedback []map[string]interface{} `json:"feedback"`
	}
	decode(t, get(h, "/admin/feedback"), &r)
	found := false
	for _, m := range r.Feedback {
		if m["name"] == "New" {
			found = true
		}
	}
	if !found {
		t.Errorf("a new message is not in the list: %v", r.Feedback)
	}
}

func TestTheHealthPageWarnsBeforeFirestoreRunsOutOfItsFreeAllowance(t *testing.T) {
	for _, c := range []struct {
		reads, writes, deletes int64
		want                   string
	}{
		{100, 10, 0, healthOK},
		{34999, 0, 0, healthOK},
		{35000, 0, 0, healthWarn},
		{0, 14000, 0, healthWarn},
		{44999, 0, 0, healthWarn},
		{45000, 0, 0, healthFail},
		{0, 0, 19000, healthFail},
		{60000, 30000, 0, healthFail},
	} {
		got, detail := firestoreAllowanceCheck(c.reads, c.writes, c.deletes)
		if got != c.want || detail == "" {
			t.Errorf("%d reads, %d writes, %d deletes: %s (%q), want %s", c.reads, c.writes, c.deletes, got, detail, c.want)
		}
	}
}

func TestTheHealthPageSaysHowTheDatabasesAreRead(t *testing.T) {
	status, detail := replicaCheck(nil)
	if status != healthOK || detail == "" {
		t.Errorf("nothing open: %s %q", status, detail)
	}
	full := []persist.ReplicaInfo{
		{Name: "user_sessions", Mode: "full", Records: 130},
		{Name: "blog", Mode: "full", Records: 8},
		{Name: "snippets", Mode: "on demand"},
	}
	status, detail = replicaCheck(full)
	if status != healthOK || !strings.Contains(detail, "user_sessions 130") || !strings.Contains(detail, "138 records") || !strings.Contains(detail, "kept on demand: snippets") {
		t.Errorf("a replica: %s %q", status, detail)
	}
	status, detail = replicaCheck(append(full, persist.ReplicaInfo{Name: "feedback", Mode: "off"}))
	if status != healthWarn || !strings.Contains(detail, "straight from the remote database") || !strings.Contains(detail, "feedback") {
		t.Errorf("one read directly: %s %q", status, detail)
	}
	status, detail = replicaCheck([]persist.ReplicaInfo{{Name: "blog", Mode: "full", Records: 3, Broken: true}})
	if status != healthWarn || !strings.Contains(detail, "could not be written") {
		t.Errorf("a broken copy: %s %q", status, detail)
	}
}
