package fileload

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NYTimes/gziphandler"
)

func get(t *testing.T, path, query string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/ws_filebrowser?"+query, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
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

func TestAnUnchangedFileIsAnsweredWithNothingAndAChangedOneInFull(t *testing.T) {
	p := file(t, "hello")
	first := get(t, p, "q=load")
	tag := first.Header().Get("ETag")
	if tag == "" || !strings.Contains(first.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("no tag or no revalidation: %q %q", tag, first.Header().Get("Cache-Control"))
	}
	again := get(t, p, "q=load", "If-None-Match", tag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("an unchanged file: %d, %d bytes", again.Code, again.Body.Len())
	}
	if get(t, p, "q=load", "If-None-Match", `W/"other"`).Code != 200 {
		t.Error("another tag must get the file")
	}
	if get(t, p, "q=load", "If-None-Match", `"x", `+tag).Code != http.StatusNotModified {
		t.Error("a tag in a list was not found")
	}
	// the file is written: its tag changes (a later time and another size)
	if err := os.WriteFile(p, []byte("hello, changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := get(t, p, "q=load", "If-None-Match", tag)
	if later.Code != 200 || decoded(t, later) != "hello, changed" {
		t.Errorf("a changed file: %d %q", later.Code, later.Body.String())
	}
}

func TestAPieceHasItsOwnNotModifiedAnswerWithTheSameHeaders(t *testing.T) {
	p := file(t, "0123456789")
	tag := get(t, p, "q=load&offset=2&length=3").Header().Get("ETag")
	rec := get(t, p, "q=load&offset=2&length=3", "If-None-Match", tag)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("got %d with %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("X-Chunk-Offset") != "2" || rec.Header().Get("X-Chunk-Length") != "3" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestATooLargeFileIsRefusedEvenWhenTheBrowserHoldsATag(t *testing.T) {
	p := file(t, strings.Repeat("x", 100))
	tag := get(t, p, "q=load").Header().Get("ETag")
	if rec := get(t, p, "q=load&max=50", "If-None-Match", tag); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want 413", rec.Code)
	}
}

func TestRawIsTheBytesThemselvesAndIsNeverCompressedHere(t *testing.T) {
	text := strings.Repeat("a line of a log file\n", 500)
	for _, content := range []string{"hello", text} {
		rec := get(t, file(t, content), "q=load&enc=raw", "Accept-Encoding", "gzip, br")
		if rec.Body.String() != content || rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Content-Type") != "application/octet-stream" {
			t.Errorf("%d bytes: got %d bytes, encoding %q, type %q", len(content), rec.Body.Len(), rec.Header().Get("Content-Encoding"), rec.Header().Get("Content-Type"))
		}
	}
}

// The site is served behind a gzip handler (server.go). The page reads the bytes of a load as the file, so what comes
// out of that handler, once the browser has undone the gzip, must be the file: not gzip bytes a second time (a file
// over a kilobyte was shown as binary, because a gzip stream has zero bytes in it).
func TestTheBytesThroughTheSitesGzipHandlerAreTheFile(t *testing.T) {
	text := strings.Repeat("2026-11-12T08:14:05Z INFO request id=12291 path=/api/run took=527ms\n", 4000)
	p := file(t, text)
	srv := httptest.NewServer(gziphandler.GzipHandler(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		req.ParseForm()
		Serve(rw, req, p)
	})))
	defer srv.Close()
	for _, query := range []string{"q=load&enc=raw", "q=load&enc=raw&offset=0&length=100000", "q=load&enc=raw&max=99999999"} {
		resp, err := http.Get(srv.URL + "/?" + query) // the client asks for gzip and undoes it, as a browser does
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := text
		if strings.Contains(query, "length=") {
			want = text[:100000]
		}
		if string(body) != want {
			head := body
			if len(head) > 8 {
				head = head[:8]
			}
			t.Errorf("%s: the body is not the file (%d bytes, starts % x)", query, len(body), head)
		}
	}
}

func TestRawKeepsEveryByteOfABinaryFileAndPiecesWork(t *testing.T) {
	data := []byte{0, 1, 2, 255, 254, '\r', '\n', 0}
	p := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(p, data, 0o644)
	if rec := get(t, p, "q=load&enc=raw"); !bytes.Equal(rec.Body.Bytes(), data) {
		t.Errorf("whole file: %v", rec.Body.Bytes())
	}
	rec := get(t, p, "q=load&enc=raw&offset=3&length=3")
	if !bytes.Equal(rec.Body.Bytes(), data[3:6]) || rec.Header().Get("X-Chunk-Length") != "3" {
		t.Errorf("piece: %v %v", rec.Body.Bytes(), rec.Header())
	}
}

func TestWithoutRawTheAnswerIsStillBase64(t *testing.T) {
	rec := get(t, file(t, strings.Repeat("z", 5000)), "q=load", "Accept-Encoding", "gzip")
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Content-Type") != "text/plain" || decoded(t, rec) != strings.Repeat("z", 5000) {
		t.Errorf("the original form changed: %v", rec.Header())
	}
}
