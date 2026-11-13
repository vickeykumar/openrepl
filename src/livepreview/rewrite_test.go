package livepreview

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pfx = "/preview/tok/p/5000"

func TestAbsoluteLinksOfAPageGoThroughThePreview(t *testing.T) {
	page := `<!doctype html><html><head><link rel="stylesheet" href="/static/a.css"><script src='/static/app.js'></script></head>
<body><a href="/about">x</a> <a href="rel/page">r</a> <a href="//cdn.example.com/x.js">cdn</a> <a href="https://e.com/">ext</a>
<a href="#top">top</a> <form action="/login" method="post"></form> <img SRC="/img/a.png"> <a href=/unquoted>u</a></body></html>`
	got := string(RewriteHTML([]byte(page), pfx))
	for _, want := range []string{`href="` + pfx + `/static/a.css"`, `src='` + pfx + `/static/app.js'`, `href="` + pfx + `/about"`, `action="` + pfx + `/login"`, `SRC="` + pfx + `/img/a.png"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	for _, same := range []string{`href="rel/page"`, `href="//cdn.example.com/x.js"`, `href="https://e.com/"`, `href="#top"`} {
		if !strings.Contains(got, same) {
			t.Errorf("%s was changed", same)
		}
	}
	// rewriting twice does not stack the prefix on what already has it
	if again := string(RewriteHTML([]byte(got), pfx)); strings.Contains(again, pfx+pfx) {
		t.Error("the prefix was added twice")
	}
}

func TestTheScriptIsAddedAtTheTopOfTheHead(t *testing.T) {
	got := string(RewriteHTML([]byte(`<html><head><title>t</title></head><body></body></html>`), pfx))
	if !strings.Contains(got, `<head><script>(function(){var P="`+pfx+`"`) {
		t.Errorf("not at the top of the head: %s", got)
	}
	// no head: after a doctype, else at the start
	if got := string(RewriteHTML([]byte(`<!DOCTYPE html><p>hi</p>`), pfx)); !strings.HasPrefix(got, "<!DOCTYPE html><script>") {
		t.Errorf("after the doctype: %s", got)
	}
	if got := string(RewriteHTML([]byte(`<p>hi</p>`), pfx)); !strings.HasPrefix(got, "<script>") {
		t.Errorf("at the start: %s", got)
	}
}

func TestThePrefixIsSafeInsideTheScript(t *testing.T) {
	if got := jsString(`/p"</script><x>\`); strings.Contains(got, "</script>") || strings.Contains(got, `"<`) {
		t.Errorf("not escaped: %s", got)
	}
}

func TestOnlyHTMLAnswersAreRewrittenAndTheirLengthIsRight(t *testing.T) {
	port, dial := appWith(t, map[string]string{"/": "text/html", "/data": "application/json"})
	o := Options{Home: "/h", Port: port, Prefix: pfx, Dial: dial, Jar: NewJar(), Hook: HTMLHook(pfx)}
	page := run(o, "GET", "", "")
	if !strings.Contains(page.Body.String(), `<a href="`+pfx+`/x">`) || !strings.Contains(page.Body.String(), "<script>") {
		t.Errorf("the page was not rewritten: %q", page.Body.String())
	}
	if page.Header().Get("Content-Length") != fmt.Sprint(page.Body.Len()) {
		t.Errorf("length %q for %d bytes", page.Header().Get("Content-Length"), page.Body.Len())
	}
	data := run(o, "GET", "data", "")
	if data.Body.String() != `{"a":"/x"}` {
		t.Errorf("JSON was changed: %q", data.Body.String())
	}
}

func appWith(t *testing.T, types map[string]string) (int, Dialer) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", types["/"])
		fmt.Fprint(w, `<html><head></head><body><a href="/x">x</a></body></html>`)
	})
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", types["/data"])
		fmt.Fprint(w, `{"a":"/x"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return dialServer(srv)
}
