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
	for _, ok := range []string{"", ".", "a.py", "src/auth.py", "src/", "a b/c d.txt"} {
		if _, good := plainRel(ok); !good {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", "~/x", ".env", "src/.git/config", "a\\b", "a\x00b", "a//b", strings.Repeat("x", 500)} {
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
		"a hidden file":    {`{"op":"read","path":".env"}`, http.StatusBadRequest, c},
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
