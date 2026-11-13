package livepreview

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func workspace(t *testing.T) (home string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	must(t, os.MkdirAll(filepath.Join(home, "site", "css"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, "site", "index.html"), []byte("<h1>hi</h1>"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, "site", "css", "a.css"), []byte("body{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, "notes.md"), []byte("# notes"), 0o644))
	must(t, os.MkdirAll(filepath.Join(home, "empty"), 0o755))
	// something outside the workspace, and a sibling that starts with the same name
	must(t, os.WriteFile(filepath.Join(base, "secret.txt"), []byte("SECRET"), 0o644))
	must(t, os.MkdirAll(filepath.Join(base, "home2"), 0o755))
	must(t, os.WriteFile(filepath.Join(base, "home2", "other.txt"), []byte("OTHER"), 0o644))
	return home
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func get(home, urlPath, rel string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ServeFile(rec, httptest.NewRequest("GET", urlPath, nil), home, rel)
	return rec
}

func TestAFileOfTheWorkspaceIsSentWithItsTypeAndTheSandboxHeaders(t *testing.T) {
	home := workspace(t)
	rec := get(home, "/preview/t/f/site/css/a.css", "site/css/a.css")
	if rec.Code != 200 || rec.Body.String() != "body{}" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("%d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox ") || strings.Contains(h.Get("Content-Security-Policy"), "allow-same-origin") {
		t.Errorf("the sandbox: %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" || h.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("headers: %v", h)
	}
	if md := get(home, "/p/notes.md", "notes.md"); !strings.HasPrefix(md.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("markdown is shown as text: %q", md.Header().Get("Content-Type"))
	}
}

func TestAFolderShowsItsIndexAndASlashIsAddedFirst(t *testing.T) {
	home := workspace(t)
	if rec := get(home, "/preview/t/f/site", "site"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/preview/t/f/site/" {
		t.Errorf("no slash: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(home, "/preview/t/f/site/", "site/"); rec.Code != 200 || rec.Body.String() != BridgeScript+"<h1>hi</h1>" {
		t.Errorf("index: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(home, "/preview/t/f/empty/", "empty/"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "No index.html") {
		t.Errorf("a folder without an index: %d %q", rec.Code, rec.Body.String())
	}
}

func TestNothingOutsideTheWorkspaceIsSent(t *testing.T) {
	home := workspace(t)
	for _, rel := range []string{"../secret.txt", "site/../../secret.txt", "../home2/other.txt", "/../secret.txt", "..%2fsecret.txt", "site/%2e%2e/%2e%2e/secret.txt", "a\x00b"} {
		rec := get(home, "/x", rel)
		if rec.Code == 200 {
			t.Errorf("%q was sent: %q", rel, rec.Body.String())
		}
	}
	// the rooted form is read as inside the workspace, not as an absolute path of the machine
	if rec := get(home, "/x", "/etc/passwd"); rec.Code == 200 {
		t.Error("/etc/passwd was sent")
	}
}

func TestALinkThatLeadsOutOfTheWorkspaceIsNotFollowed(t *testing.T) {
	home := workspace(t)
	outside := filepath.Join(filepath.Dir(home), "secret.txt")
	must(t, os.Symlink(outside, filepath.Join(home, "leak.txt")))
	must(t, os.Symlink(filepath.Dir(home), filepath.Join(home, "up")))
	if rec := get(home, "/x", "leak.txt"); rec.Code == 200 {
		t.Error("a link to a file outside was followed")
	}
	if rec := get(home, "/x", "up/secret.txt"); rec.Code == 200 {
		t.Error("a link to the folder above was followed")
	}
	// a link inside the workspace is fine
	must(t, os.Symlink(filepath.Join(home, "notes.md"), filepath.Join(home, "inside.md")))
	if rec := get(home, "/x", "inside.md"); rec.Code != 200 {
		t.Errorf("a link inside the workspace: %d", rec.Code)
	}
}

func TestOnlyReadingIsAllowed(t *testing.T) {
	home := workspace(t)
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := httptest.NewRecorder()
		ServeFile(rec, httptest.NewRequest(m, "/x", strings.NewReader("data")), home, "notes.md")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	ServeFile(rec, httptest.NewRequest("OPTIONS", "/x", nil), home, "notes.md")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("OPTIONS: %d %v", rec.Code, rec.Header())
	}
}

func TestAMissingFileAndAMissingWorkspaceAre404(t *testing.T) {
	home := workspace(t)
	if rec := get(home, "/x", "nope.html"); rec.Code != 404 {
		t.Errorf("missing: %d", rec.Code)
	}
	if rec := get(filepath.Join(home, "gone"), "/x", "notes.md"); rec.Code != 404 {
		t.Errorf("no workspace: %d", rec.Code)
	}
}

func TestAPageOfTheWorkspaceGetsTheScriptOfThePaneAndOtherFilesDoNot(t *testing.T) {
	home := workspace(t)
	page := get(home, "/preview/t/f/site/index.html", "site/index.html")
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, `post({t:"nav"`) || !strings.HasSuffix(body, "<h1>hi</h1>") {
		t.Errorf("a page: %d %q", page.Code, body)
	}
	if page.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Errorf("the length is of the new body: %q for %d", page.Header().Get("Content-Length"), len(body))
	}
	if css := get(home, "/preview/t/f/site/style.css", "site/style.css"); css.Code == 200 && strings.Contains(css.Body.String(), "openrepl") {
		t.Errorf("a stylesheet has no script: %q", css.Body.String())
	}
	out := string(InjectBridge([]byte("<!doctype html><title>x</title>")))
	if !strings.HasPrefix(out, "<!doctype html><script>") {
		t.Errorf("after the doctype when there is no head: %q", out[:40])
	}
}
