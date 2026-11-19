package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(home, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestAPathOfTheAgentIsPlainAndInsideTheWorkspace(t *testing.T) {
	for _, ok := range []string{"", ".", "a.py", "src/auth.py", "src/", "a b/c d.txt", ".env", ".genie/skills/a.md"} {
		if _, good := plainRel(ok); !good {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", "~/x", ".genie/chats/a.jsonl", ".genie/tabs.json", "src/.git/config", "a\\b", "a\x00b", "a//b", strings.Repeat("x", 500)} {
		if _, good := plainRel(bad); good {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestSearchFindsTextWithTheFileAndTheLineAndSkipsWhatIsNotTheAgents(t *testing.T) {
	home := workspace(t, map[string]string{
		"auth.py":                   "def login(request):\n    return Login(request)\n",
		"src/routes.py":             "app.add_route('/login', login)\n",
		".env":                      "LOGIN_KEY=secret\n",
		".git/config":               "login = x\n",
		"node_modules/lib/index.js": "login()\n",
		"data.bin":                  "login\x00\x01\x02",
		"notes.txt":                 "nothing here\n",
		"big.txt":                   strings.Repeat("login\n", 300000),
		"deep/er/er/file.go":        "// LOGIN handler\n",
	})
	out := searchFiles(home, home, "login", time.Now)
	var got []string
	for _, m := range out.Matches {
		got = append(got, fmt.Sprintf("%s:%d", m.File, m.Line))
	}
	for _, want := range []string{"auth.py:1", "auth.py:2", "src/routes.py:1", "deep/er/er/file.go:1"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("%s was not found in %v", want, got)
		}
	}
	for _, g := range got {
		if strings.HasPrefix(g, ".env") || strings.HasPrefix(g, ".git") || strings.HasPrefix(g, "node_modules") || strings.HasPrefix(g, "data.bin") || strings.HasPrefix(g, "big.txt") {
			t.Errorf("%s was searched, and it is not for the agent", g)
		}
	}
	// a search in a folder stays in it, and a long line is cut
	sub := searchFiles(home, filepath.Join(home, "src"), "login", time.Now)
	if len(sub.Matches) != 1 || sub.Matches[0].File != "src/routes.py" {
		t.Errorf("in src: %+v", sub.Matches)
	}
	long := workspace(t, map[string]string{"x.txt": "login " + strings.Repeat("y", 500) + "\n"})
	if m := searchFiles(long, long, "login", time.Now).Matches[0]; len(m.Text) > filesMaxLineChars+3 {
		t.Errorf("a long line was not cut: %d", len(m.Text))
	}
}

func TestSearchStopsAtItsLimits(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("f%03d.txt", i)] = "hit\nhit\n"
	}
	home := workspace(t, files)
	out := searchFiles(home, home, "HIT", time.Now) // without regard to case
	if len(out.Matches) != filesMaxMatches || !out.Truncated {
		t.Errorf("matches: %d truncated: %v", len(out.Matches), out.Truncated)
	}
	// out of time
	start := time.Now()
	calls := 0
	late := func() time.Time {
		calls++
		if calls > 3 {
			return start.Add(time.Hour)
		}
		return start
	}
	if out := searchFiles(home, home, "hit", late); !out.Truncated || len(out.Matches) >= filesMaxMatches {
		t.Errorf("out of time: %+v", out)
	}
}

func TestASymbolicLinkOutOfTheWorkspaceIsNotFollowed(t *testing.T) {
	home := workspace(t, map[string]string{"in.txt": "secret in\n"})
	outside := workspace(t, map[string]string{"out.txt": "secret out\n"})
	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Skip("no symbolic links here")
	}
	os.Symlink(filepath.Join(outside, "out.txt"), filepath.Join(home, "file-link"))
	if _, ok := inside(home, filepath.Join(home, "link")); ok {
		t.Error("a link out of the workspace was followed")
	}
	if _, ok := inside(home, filepath.Join(home, "in.txt")); !ok {
		t.Error("a file of the workspace was refused")
	}
	for _, m := range searchFiles(home, home, "secret", time.Now).Matches {
		if strings.Contains(m.Text, "out") {
			t.Errorf("found text outside the workspace: %+v", m)
		}
	}
}

func TestReadReturnsNumberedLinesAndSaysWhereToGoOn(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	home := workspace(t, map[string]string{"a.txt": b.String()})
	r, err := readLines(filepath.Join(home, "a.txt"), 10, 12)
	if err != nil || r.StartLine != 10 || r.EndLine != 12 || r.TotalLines != 500 || r.Next != 13 || r.Truncated {
		t.Fatalf("a range: %+v %v", r, err)
	}
	if !strings.HasPrefix(r.Text, "   10  line 10\n") || strings.Count(r.Text, "\n") != 3 {
		t.Errorf("the lines: %q", r.Text)
	}
	// the end of the file: nothing more to go on to
	if last, _ := readLines(filepath.Join(home, "a.txt"), 499, 600); last.EndLine != 500 || last.Next != 0 {
		t.Errorf("the end: %+v", last)
	}
	// a range of a few hundred lines stops where the text no longer fits, and says where it stopped
	big := workspace(t, map[string]string{"b.txt": strings.Repeat(strings.Repeat("x", 100)+"\n", 400)})
	w, _ := readLines(filepath.Join(big, "b.txt"), 1, 400)
	if !w.Truncated || w.Next == 0 || len(w.Text) > filesReadChars+10 || w.EndLine != w.Next-1 {
		t.Errorf("too much text: truncated %v next %d end %d len %d", w.Truncated, w.Next, w.EndLine, len(w.Text))
	}
	// past the end
	if p, _ := readLines(filepath.Join(home, "a.txt"), 900, 910); p.Text != "" || p.TotalLines != 500 {
		t.Errorf("past the end: %+v", p)
	}
}

func filesCall(t *testing.T, s *Server, c *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/agent_files", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.handleAgentFiles(w, req)
	return w
}

func TestTheFilesEndpointIsForTheOwnerOfASignedInWorkspaceAndReadOnly(t *testing.T) {
	s, _ := adminTestServer(t)
	c := sessionCookie(t, "uid-ann", "ann@example.com")
	// the workspace of this user: write what the agent may look at
	probe := httptest.NewRequest("POST", "/agent_files", nil)
	probe.AddCookie(c)
	_, home := s.requestIdentity(httptest.NewRecorder(), probe)
	if home == "" {
		t.Skip("no workspace for the test user")
	}
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, "auth.py"), []byte("def login():\n    pass\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".env"), []byte("login=secret\n"), 0o644)
	t.Cleanup(func() { os.Remove(filepath.Join(home, "auth.py")); os.Remove(filepath.Join(home, ".env")) })

	w := filesCall(t, s, c, `{"op":"search","query":"login"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"file":"auth.py"`) || strings.Contains(w.Body.String(), ".env") {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	if w := filesCall(t, s, c, `{"op":"read","path":"auth.py","start_line":1,"end_line":1}`); w.Code != 200 || !strings.Contains(w.Body.String(), "def login") || strings.Contains(w.Body.String(), "pass") {
		t.Errorf("read: %d %s", w.Code, w.Body.String())
	}
	// what is refused
	for name, tc := range map[string]struct {
		body string
		code int
		c    *http.Cookie
	}{
		"a guest":          {`{"op":"search","query":"login"}`, http.StatusForbidden, nil},
		"inside .git":      {`{"op":"read","path":".git/config"}`, http.StatusBadRequest, c},
		"the chat store":   {`{"op":"read","path":".genie/chats/a.jsonl"}`, http.StatusBadRequest, c},
		"a hidden name":    {`{"op":"read","path":".env"}`, http.StatusOK, c},
		"a path out":       {`{"op":"read","path":"../x"}`, http.StatusBadRequest, c},
		"no such file":     {`{"op":"read","path":"nope.py"}`, http.StatusNotFound, c},
		"an empty query":   {`{"op":"search","query":"  "}`, http.StatusBadRequest, c},
		"another op":       {`{"op":"delete","path":"auth.py"}`, http.StatusBadRequest, c},
		"a search in file": {`{"op":"search","query":"x","path":"auth.py"}`, http.StatusBadRequest, c},
		"not json":         {`{"op":`, http.StatusBadRequest, c},
	} {
		if w := filesCall(t, s, tc.c, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// nothing was changed
	if b, _ := os.ReadFile(filepath.Join(home, "auth.py")); string(b) != "def login():\n    pass\n" {
		t.Errorf("the file was changed: %q", b)
	}
	// switched off with agent mode
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{AgentDisabled: true}})
	if w := filesCall(t, s, c, `{"op":"search","query":"login"}`); w.Code != http.StatusForbidden {
		t.Errorf("agent mode off: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/agent_files", nil)
	w2 := httptest.NewRecorder()
	s.handleAgentFiles(w2, req)
	if w2.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w2.Code)
	}
}

// ---- what a shell command changed, and putting a file back (agent_shell_changes.go) ----------------------------------

func TestTheWorkspaceBeforeAndAfterTellsWhatACommandMadeChangedAndDeleted(t *testing.T) {
	home := workspace(t, map[string]string{
		"keep.txt":              "same\n",
		"edit.py":               "x = 1\n",
		"gone.txt":              "bye\n",
		"touched.txt":           "same text\n",
		".env":                  "SECRET=1\n",
		"node_modules/lib/a.js": "a\n",
		"Trash/old.txt":         "trashed\n",
		"big.txt":               strings.Repeat("x", shellSnapFileBytes+10),
		"bin.dat":               "a\x00b",
		"sub/inner.txt":         "inner\n",
	})
	before, ok := snapshotWorkspace(home)
	if !ok || before["edit.py"].text == nil || *before["edit.py"].text != "x = 1\n" {
		t.Fatalf("the snapshot: %v %+v", ok, before["edit.py"])
	}
	for _, skipped := range []string{".env", "node_modules/lib/a.js", "Trash/old.txt"} {
		if _, in := before[skipped]; in {
			t.Errorf("%s was looked at", skipped)
		}
	}
	if before["big.txt"].text != nil || before["bin.dat"].text != nil {
		t.Error("a large file or a binary file was kept as text")
	}
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(filepath.Join(home, "edit.py"), []byte("x = 2\ny = 3\n"), 0o644)
	os.WriteFile(filepath.Join(home, "new.txt"), []byte("hello\n"), 0o644)
	os.WriteFile(filepath.Join(home, "touched.txt"), []byte("same text\n"), 0o644) // written again, the same
	os.WriteFile(filepath.Join(home, "big.txt"), []byte(strings.Repeat("y", shellSnapFileBytes+10)), 0o644)
	os.WriteFile(filepath.Join(home, ".env"), []byte("SECRET=2\n"), 0o644)
	os.Remove(filepath.Join(home, "gone.txt"))
	got, more := diffWorkspace(home, before)
	if more != 0 {
		t.Errorf("more: %d", more)
	}
	byPath := map[string]shellChange{}
	for _, c := range got {
		byPath[c.Path] = c
	}
	if len(got) != 4 {
		t.Fatalf("%d changes: %+v", len(got), got)
	}
	if c := byPath["edit.py"]; c.Kind != "changed" || !c.Undo || *c.Before != "x = 1\n" || *c.After != "x = 2\ny = 3\n" {
		t.Errorf("edit.py: %+v", c)
	}
	if c := byPath["new.txt"]; c.Kind != "new" || !c.Undo || c.After == nil || *c.After != "hello\n" || c.Before != nil {
		t.Errorf("new.txt: %+v", c)
	}
	if c := byPath["gone.txt"]; c.Kind != "deleted" || !c.Undo || *c.Before != "bye\n" {
		t.Errorf("gone.txt: %+v", c)
	}
	if c := byPath["big.txt"]; c.Kind != "changed" || c.Undo || c.Before != nil {
		t.Errorf("a large file changed has no copy to put back: %+v", c)
	}
	if _, in := byPath["touched.txt"]; in {
		t.Error("a file written again with the same text is a change")
	}
	if _, in := byPath[".env"]; in {
		t.Error("a hidden file is told")
	}
}

func TestChangesAreLimitedAndAHugeWorkspaceIsNotFollowed(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < shellChangesMax+7; i++ {
		files[fmt.Sprintf("f%03d.txt", i)] = "a"
	}
	home := workspace(t, files)
	before, _ := snapshotWorkspace(home)
	for i := 0; i < shellChangesMax+7; i++ {
		os.WriteFile(filepath.Join(home, fmt.Sprintf("n%03d.txt", i)), []byte("b"), 0o644)
	}
	got, more := diffWorkspace(home, before)
	if len(got) != shellChangesMax || more != 7 {
		t.Errorf("%d changes and %d more", len(got), more)
	}
	big := map[string]string{}
	for i := 0; i < shellSnapMaxFiles+3; i++ {
		big[fmt.Sprintf("d%d/f%d.txt", i%50, i)] = "x"
	}
	if _, ok := snapshotWorkspace(workspace(t, big)); ok {
		t.Error("a workspace of more than the allowed files was followed")
	}
}

func restoreCall(t *testing.T, s *Server, c *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/agent_restore", strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.handleAgentRestore(w, req)
	return w
}

func TestAFileIsPutBackOnlyWhenItIsStillAsTheCommandLeftIt(t *testing.T) {
	s, _ := adminTestServer(t)
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{}}); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, "uid-ann", "ann@example.com")
	probe := httptest.NewRequest("POST", "/agent_restore", nil)
	probe.AddCookie(c)
	_, home := s.requestIdentity(httptest.NewRecorder(), probe)
	if home == "" {
		t.Skip("no workspace for the test user")
	}
	os.MkdirAll(home, 0o755)
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, "shell")) })
	shellAllowedReset()
	os.MkdirAll(filepath.Join(home, "shell"), 0o755)
	os.WriteFile(filepath.Join(home, "shell", "a.py"), []byte("x = 2\n"), 0o644)
	// as the command left it: it is put back
	if w := restoreCall(t, s, c, `{"path":"shell/a.py","text":"x = 1\n","expect":"x = 2\n"}`); w.Code != 200 {
		t.Fatalf("put back: %d %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, "shell", "a.py")); string(b) != "x = 1\n" {
		t.Errorf("the file: %q", b)
	}
	// changed since: it is not
	os.WriteFile(filepath.Join(home, "shell", "a.py"), []byte("mine\n"), 0o644)
	if w := restoreCall(t, s, c, `{"path":"shell/a.py","text":"x = 1\n","expect":"x = 2\n"}`); w.Code != http.StatusConflict {
		t.Errorf("changed since: %d", w.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "shell", "a.py")); string(b) != "mine\n" {
		t.Errorf("the user's text was overwritten: %q", b)
	}
	// a file the command deleted comes back, with its folder; if it is there again it is left
	if w := restoreCall(t, s, c, `{"path":"shell/sub/b.txt","text":"back\n","must_not_exist":true}`); w.Code != 200 {
		t.Fatalf("a deleted file: %d %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, "shell", "sub", "b.txt")); string(b) != "back\n" {
		t.Errorf("the deleted file: %q", b)
	}
	if w := restoreCall(t, s, c, `{"path":"shell/sub/b.txt","text":"again\n","must_not_exist":true}`); w.Code != http.StatusConflict {
		t.Errorf("it is there again: %d", w.Code)
	}
	// a file that is gone when text was expected, a hidden name, a path out of the workspace, too much text, a visitor
	for body, want := range map[string]int{
		`{"path":"shell/none.txt","text":"x","expect":"y"}`:                                                     http.StatusConflict,
		`{"path":".env","text":"x","must_not_exist":true}`:                                                      http.StatusBadRequest,
		`{"path":"../x.txt","text":"x","must_not_exist":true}`:                                                  http.StatusBadRequest,
		`{"path":"/etc/passwd","text":"x","expect":"y"}`:                                                        http.StatusBadRequest,
		`{"path":"","text":"x"}`:                                                                                http.StatusBadRequest,
		`{"path":"shell/big.txt","text":"` + strings.Repeat("x", restoreMaxText+1) + `","must_not_exist":true}`: http.StatusBadRequest,
	} {
		if w := restoreCall(t, s, c, body); w.Code != want {
			t.Errorf("%.60s: %d, want %d", body, w.Code, want)
		}
	}
	if w := restoreCall(t, s, nil, `{"path":"shell/c.txt","text":"x","must_not_exist":true}`); w.Code != http.StatusForbidden {
		t.Errorf("a visitor: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/agent_restore", nil)
	w := httptest.NewRecorder()
	s.handleAgentRestore(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}

func shellAllowedReset() {
	shellRateMu.Lock()
	shellRate = map[string][]time.Time{}
	shellRateMu.Unlock()
}
