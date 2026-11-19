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
)

// withGenieLog gives the log a folder of its own for the test.
func withGenieLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	genieLog.mu.Lock()
	old := genieLog.dir
	genieLog.dir, genieLog.pruned, genieLog.dropped, genieLog.users = dir, time.Time{}, 0, map[string][]time.Time{}
	genieLog.mu.Unlock()
	t.Cleanup(func() {
		genieLog.mu.Lock()
		genieLog.dir = old
		genieLog.mu.Unlock()
	})
	return dir
}

func tellEvent(t *testing.T, c *http.Cookie, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/chat/genie-event", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleGenieEvent(w, req)
	return w.Code
}

func dayLines(t *testing.T, dir string) []string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	var out []string
	for _, n := range names {
		b, _ := os.ReadFile(n)
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
	}
	return out
}

func TestAFailureReasonKeepsNoPathsNamesQuotesOrKeys(t *testing.T) {
	for in, want := range map[string]string{
		"the editor has only 35 lines":                      "the editor has only 35 lines",
		"cannot open /home/ann/secret-project/main.py":      "cannot open <path>",
		"the file shop/index.html changed":                  "the file <path> changed",
		"the old text \"password = hunter2\" was not found": `the old text "…" was not found`,
		"mail ann@example.com about it":                     "mail <email> about it",
		"token=abcdef0123456789abcdef0123456789abcdef used": "token=[redacted] used",
		"a line\nwith   odd \t spacing":                     "a line with odd spacing",
		"key AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA":          "key <secret>",
	} {
		if got := scrubReason(in); got != want {
			t.Errorf("scrubReason(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("word ", 100)
	if got := scrubReason(long); len([]rune(got)) > genieReasonMax+1 {
		t.Errorf("a long reason is %d characters", len([]rune(got)))
	}
}

func TestAnEventHoldsOnlyTheFieldsOfTheLog(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	e, ok := eventFrom(map[string]interface{}{
		"event": "step_failed", "sid": "abc123XYZ", "model": "my-secret-model-name", "action": "make_coffee", "ext": "HTML",
		"reason": "x", "step": 9.0, "failed": 3.0, "outcome": "bogus", "plan": "gold",
		"task": "build my bank's login page", "message": "hello", "email": "ann@example.com", "uid": "uid-ann", "filename": "secret.py",
	}, now)
	if !ok {
		t.Fatal("a good event was refused")
	}
	if e.Model != "other" || e.Action != "other" || e.Ext != "html" || e.Outcome != "" || e.Plan != "free" || e.Mode != "agent" || e.Step != 9 || e.Failed != 3 {
		t.Errorf("event: %+v", e)
	}
	b, _ := jsonLine(e)
	for _, leak := range []string{"bank", "hello", "ann@", "uid-ann", "secret.py", "gold"} {
		if strings.Contains(b, leak) {
			t.Errorf("%q reached the log: %s", leak, b)
		}
	}
	for _, bad := range []map[string]interface{}{
		{"event": "format_disk", "sid": "abc123XYZ"}, // not an event
		{"event": "stuck", "sid": "x"},               // not a session id
		{"event": "stuck"},
	} {
		if _, ok := eventFrom(bad, now); ok {
			t.Errorf("%v was taken", bad)
		}
	}
	if e, _ := eventFrom(map[string]interface{}{"event": "step_failed", "sid": "abc123XYZ", "model": "gpt-4o-mini", "action": "editor_replace"}, now); e.Model != "gpt-4o-mini" || e.Action != "editor_replace" {
		t.Errorf("a known model and action are kept: %+v", e)
	}
}

func jsonLine(e genieEvent) (string, error) {
	b, err := json.Marshal(e)
	return string(b), err
}

func TestTheEventRouteTakesOnlySignedInUsersAndOnlyWhileOn(t *testing.T) {
	g := GenieSettings{}
	c := meterSetup(t, g)
	dir := withGenieLog(t)
	body := `{"event":"step_failed","sid":"abc123XYZ","model":"gpt-4o-mini","action":"editor_replace","reason":"the editor has only 35 lines","step":4,"failed":1}`
	if code := tellEvent(t, nil, body); code != http.StatusUnauthorized {
		t.Errorf("a visitor: %d", code)
	}
	if n := len(dayLines(t, dir)); n != 0 {
		t.Fatalf("a visitor's event was kept (%d)", n)
	}
	if code := tellEvent(t, c, body); code != http.StatusNoContent {
		t.Fatalf("a signed-in user: %d", code)
	}
	lines := dayLines(t, dir)
	if len(lines) != 1 || !strings.Contains(lines[0], `"action":"editor_replace"`) || !strings.Contains(lines[0], `"plan":"free"`) || strings.Contains(lines[0], "uid-ann") {
		t.Fatalf("the kept line: %v", lines)
	}
	if code := tellEvent(t, c, `{"event":"nope","sid":"abc123XYZ"}`); code != http.StatusBadRequest {
		t.Errorf("a bad event: %d", code)
	}
	if code := tellEvent(t, c, `{"event":`); code != http.StatusBadRequest {
		t.Errorf("bad json: %d", code)
	}
	// the end of a task comes with its outcome, not here
	tellEvent(t, c, `{"event":"task_end","sid":"abc123XYZ","outcome":"done"}`)
	if n := len(dayLines(t, dir)); n != 1 {
		t.Errorf("a task_end through the event route was kept (%d lines)", n)
	}
	// switched off: nothing is kept
	if err := SaveSiteSettings(SiteSettings{Genie: g, GenieEventsOff: true}); err != nil {
		t.Fatal(err)
	}
	tellEvent(t, c, body)
	if n := len(dayLines(t, dir)); n != 1 {
		t.Errorf("an event was kept while the log is off (%d lines)", n)
	}
	if err := SaveSiteSettings(SiteSettings{Genie: g}); err != nil {
		t.Fatal(err)
	}
	// a user may report only so many in an hour
	for i := 0; i < genieUserEventsMax+20; i++ {
		tellEvent(t, c, body)
	}
	if n := len(dayLines(t, dir)); n > genieUserEventsMax {
		t.Errorf("%d events kept, the limit is %d", n, genieUserEventsMax)
	}
}

func TestTheEndOfATaskIsKeptOnceWithItsOutcomeAndNoWords(t *testing.T) {
	c := meterSetup(t, GenieSettings{AgentMaxSteps: 5})
	dir := withGenieLog(t)
	w := agentCall(t, agentStep1, c, "")
	token := w.Header().Get(agentTaskHeader)
	done := `{"task":"` + token + `","outcome":"done","changed":true,"checked":true,"sid":"abc123XYZ","model":"gpt-4o-mini","steps":7,"secs":42,"failed":2}`
	tellOutcome(t, c, token, done)
	tellOutcome(t, c, token, done) // again: not kept again
	lines := dayLines(t, dir)
	if len(lines) != 1 {
		t.Fatalf("lines: %v", lines)
	}
	for _, want := range []string{`"event":"task_end"`, `"outcome":"done"`, `"steps":7`, `"secs":42`, `"failed":2`, `"model":"gpt-4o-mini"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("%s is not in %s", want, lines[0])
		}
	}
	if strings.Contains(lines[0], token) || strings.Contains(lines[0], "uid-ann") || strings.Contains(lines[0], "write hello") {
		t.Errorf("the line holds more than it should: %s", lines[0])
	}
}

func TestTheLogIsKeptForItsDaysAndNoLonger(t *testing.T) {
	dir := withGenieLog(t)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, back := range []int{0, 1, 29, 30, 45} {
		day := now.AddDate(0, 0, -back)
		os.WriteFile(genieDayFile(dir, day), []byte(`{"t":"x","event":"stuck","model":"other","sid":"abc123XYZ"}`+"\n"), 0o644)
	}
	genieLog.add(genieEvent{Event: "stuck", Model: "other", SID: "abc123XYZ", T: now.Format(time.RFC3339), Plan: "free"}, now)
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if len(names) != 4 { // days 0, 1, 29 and 30: the 45 days old file went (30 days back is the last day kept)
		t.Errorf("%d files left: %v", len(names), names)
	}
	if got := genieLog.files(2, now); len(got) != 2 {
		t.Errorf("the last two days: %v", got)
	}
}

func TestTheAdminReadsTheGenieLogAndDownloadsIt(t *testing.T) {
	_, h := adminTestServer(t)
	dir := withGenieLog(t)
	now := time.Now()
	add := func(e genieEvent) {
		e.T, e.Plan, e.SID = now.UTC().Format(time.RFC3339), "free", "abc123XYZ"
		genieLog.add(e, now)
	}
	for i := 0; i < 4; i++ {
		add(genieEvent{Event: "task_end", Model: "gpt-4o-mini", Outcome: "done"})
	}
	add(genieEvent{Event: "task_end", Model: "gpt-4o-mini", Outcome: "failed"})
	add(genieEvent{Event: "task_end", Model: "other", Outcome: "stopped"})
	for i := 0; i < 3; i++ {
		add(genieEvent{Event: "step_failed", Model: "other", Action: "editor_replace", Reason: "the editor has only 35 lines"})
	}
	add(genieEvent{Event: "step_failed", Model: "gpt-4o-mini", Action: "run", Reason: "not possible"})
	add(genieEvent{Event: "stuck", Model: "other", Action: "editor_replace"})
	if n := len(dayLines(t, dir)); n != 11 {
		t.Fatalf("%d lines", n)
	}

	var r genieLogReply
	decode(t, get(h, "/admin/genie-log?days=7"), &r)
	if r.Tasks != 6 || r.Done != 4 || r.DonePct != 66.7 || r.Failures != 4 || r.Stuck != 1 || r.WorstAction != "editor_replace" || len(r.Models) != 2 {
		t.Fatalf("summary: %+v", r)
	}
	if r.Models[0].Model != "gpt-4o-mini" || r.Models[0].Tasks != 5 || r.Models[0].DonePct != 80 || r.Models[0].TopAction != "run" {
		t.Errorf("the first model: %+v", r.Models[0])
	}
	if r.Models[1].Model != "other" || r.Models[1].TopReason != "the editor has only 35 lines" || r.Models[1].Stuck != 1 {
		t.Errorf("the second model: %+v", r.Models[1])
	}
	if len(r.Events) != 11 || r.Events[0].Event != "stuck" { // newest first
		t.Errorf("events: %d, first %+v", len(r.Events), r.Events[0])
	}
	decode(t, get(h, "/admin/genie-log?event=step_failed&model=other"), &r)
	if r.Matching != 3 || len(r.Events) != 3 {
		t.Errorf("filtered: matching %d, shown %d", r.Matching, len(r.Events))
	}
	decode(t, get(h, "/admin/genie-log?q=35+lines&limit=2"), &r)
	if r.Matching != 3 || len(r.Events) != 2 {
		t.Errorf("search: matching %d, shown %d", r.Matching, len(r.Events))
	}

	dl := get(h, "/admin/genie-log?download=1")
	if dl.Code != 200 || !strings.Contains(dl.Header().Get("Content-Disposition"), "attachment") || strings.Count(dl.Body.String(), "\n") != 11 {
		t.Errorf("download: %d %v lines %d", dl.Code, dl.Header(), strings.Count(dl.Body.String(), "\n"))
	}
	var audit struct {
		Entries []struct{ Action, Detail string }
	}
	decode(t, get(h, "/admin/audit"), &audit)
	found := false
	for _, e := range audit.Entries {
		if e.Action == "genie log" {
			found = true
		}
	}
	if !found {
		t.Errorf("the download is not in the audit log: %+v", audit.Entries)
	}
	// only admins
	req := httptest.NewRequest("GET", "/admin/genie-log", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a visitor reached the Genie log: %d", w.Code)
	}
}

func TestTheMoreFieldsOfAnEventAreChecked(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	e, ok := eventFrom(map[string]interface{}{
		"event": "stuck", "sid": "abc123XYZ", "tid": "tab12cd34", "code": "stale_ref", "rule": "same-step", "choice": "stronger",
		"trail": "preview_read, preview_click ,rm -rf /,editor_write,<script>", "tokens": 4321.0, "lang": "Python",
	}, now)
	if !ok {
		t.Fatal("refused")
	}
	if e.TID != "tab12cd34" || e.Code != "stale_ref" || e.Rule != "same-step" || e.Choice != "stronger" || e.Trail != "preview_read,preview_click,editor_write" || e.Tokens != 4321 || e.Lang != "Python" {
		t.Errorf("event: %+v", e)
	}
	bad, _ := eventFrom(map[string]interface{}{
		"event": "step_failed", "sid": "abc123XYZ", "tid": "no way", "code": "my own words", "rule": "made-up", "choice": "ask me", "trail": "evil", "tokens": -5.0, "lang": "<b>x</b>",
	}, now)
	if bad.TID != "" || bad.Code != "other" || bad.Rule != "" || bad.Choice != "" || bad.Trail != "" || bad.Tokens != 0 || bad.Lang != "" {
		t.Errorf("a field was not checked: %+v", bad)
	}
	if e, ok := eventFrom(map[string]interface{}{"event": "stuck_answer", "sid": "abc123XYZ", "choice": "go"}, now); !ok || e.Choice != "go" {
		t.Errorf("stuck_answer: %v %+v", ok, e)
	}
}

func TestTheAdminSeesTheMostCommonFailureCodes(t *testing.T) {
	_, h := adminTestServer(t)
	withGenieLog(t)
	now := time.Now()
	add := func(e genieEvent) {
		e.T, e.Plan, e.SID = now.UTC().Format(time.RFC3339), "free", "abc123XYZ"
		genieLog.add(e, now)
	}
	for i := 0; i < 3; i++ {
		add(genieEvent{Event: "step_failed", Model: "other", Action: "preview_click", Code: "stale_ref"})
	}
	add(genieEvent{Event: "step_failed", Model: "other", Action: "files_save", Code: "no_file_open"})
	add(genieEvent{Event: "step_failed", Model: "other", Action: "preview_drag", Code: "stale_ref"})
	var r genieLogReply
	decode(t, get(h, "/admin/genie-log?days=7"), &r)
	if len(r.Codes) != 2 || r.Codes[0].Code != "stale_ref" || r.Codes[0].N != 4 || r.Codes[0].TopAction != "preview_click" || r.Codes[1].Code != "no_file_open" {
		t.Errorf("codes: %+v", r.Codes)
	}
	decode(t, get(h, "/admin/genie-log?q=stale_ref"), &r)
	if r.Matching != 4 {
		t.Errorf("search by code: %d", r.Matching)
	}
}
