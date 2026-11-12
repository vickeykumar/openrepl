package fileload

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(t *testing.T, path, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/ws_filebrowser?"+query, nil)
	req.Form, _ = url.ParseQuery(query)
	rec := httptest.NewRecorder()
	Serve(rec, req, path)
	return rec
}

func decoded(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("the body is not base64: %v", err)
	}
	return string(b)
}

func file(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPlainLoadSendsTheWholeFileAndItsSize(t *testing.T) {
	rec := get(t, file(t, "hello\nworld"), "q=load")
	if rec.Code != 200 || decoded(t, rec) != "hello\nworld" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-File-Size") != "11" {
		t.Errorf("size header %q", rec.Header().Get("X-File-Size"))
	}
}

func TestAFileOverMaxIsNotReadOrSent(t *testing.T) {
	p := file(t, strings.Repeat("x", 100))
	rec := get(t, p, "q=load&max=50")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", rec.Code)
	}
	if rec.Header().Get("X-File-Size") != "100" {
		t.Errorf("the size must be told: %q", rec.Header().Get("X-File-Size"))
	}
	if strings.Contains(rec.Body.String(), "eHh4") {
		t.Error("the content was sent")
	}
	if rec := get(t, p, "q=load&max=100"); rec.Code != 200 {
		t.Errorf("a file exactly at max is sent, got %d", rec.Code)
	}
}

func TestAPieceIsReadFromAnOffset(t *testing.T) {
	p := file(t, "0123456789")
	rec := get(t, p, "q=load&offset=3&length=4")
	if got := decoded(t, rec); got != "3456" {
		t.Fatalf("piece %q", got)
	}
	if rec.Header().Get("X-Chunk-Offset") != "3" || rec.Header().Get("X-Chunk-Length") != "4" || rec.Header().Get("X-File-Size") != "10" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestAPiecePastTheEndIsShortOrEmptyAndNeverAnError(t *testing.T) {
	p := file(t, "0123456789")
	if got := decoded(t, get(t, p, "q=load&offset=8&length=100")); got != "89" {
		t.Errorf("short piece %q", got)
	}
	rec := get(t, p, "q=load&offset=500&length=10")
	if rec.Code != 200 || decoded(t, rec) != "" || rec.Header().Get("X-Chunk-Offset") != "10" {
		t.Errorf("past the end: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func TestAPieceIsCappedAndBadNumbersAreRefused(t *testing.T) {
	p := file(t, "abc")
	for _, q := range []string{"q=load&length=0", "q=load&length=-1", "q=load&length=x", "q=load&length=1&offset=-1", "q=load&length=1&offset=y"} {
		if rec := get(t, p, q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
	big := filepath.Join(t.TempDir(), "big")
	f, _ := os.Create(big)
	f.Truncate(MaxChunk + 1000)
	f.Close()
	rec := get(t, big, "q=load&length=999999999")
	if rec.Header().Get("X-Chunk-Length") != "4194304" {
		t.Errorf("the piece was not capped: %s", rec.Header().Get("X-Chunk-Length"))
	}
}

func TestAFolderIsNotAFile(t *testing.T) {
	if rec := get(t, t.TempDir(), "q=load"); rec.Code != http.StatusBadRequest {
		t.Errorf("got %d", rec.Code)
	}
}
