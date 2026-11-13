package livepreview

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownBlocks(t *testing.T) {
	src := "# Title\n\nSome *emphasis*, **bold**, ~~gone~~ and `code <b>`.\n\n- one\n- two\n\n1. first\n2. second\n\n> quoted\n\n```\nx < y\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n"
	got := RenderMarkdown(src, "notes.md")
	for _, want := range []string{
		`<h1 id="title">Title</h1>`, "<em>emphasis</em>", "<strong>bold</strong>", "<del>gone</del>", "<code>code &lt;b&gt;</code>",
		"<ul>\n<li>one</li>\n<li>two</li>\n</ul>", "<ol>\n<li>first</li>\n<li>second</li>\n</ol>", "<blockquote><p>quoted</p></blockquote>",
		"<pre><code>x &lt; y</code></pre>", "<th>a</th>", "<td>2</td>", "<hr>", "<title>notes.md</title>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the page lacks %q:\n%s", want, got)
		}
	}
}

func TestMarkdownCannotRunAnything(t *testing.T) {
	src := "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[click](javascript:alert(1)) [data](data:text/html,<b>x) [vb](vbscript:x)\n\n![p](javascript:alert(1)) ![q](data:image/png;base64,AAAA)\n\n[ok](https://example.com/a?b=1&c=2) [m](mailto:a@b.c) [rel](other.md#part) [img](pic.png) [prot](//evil.example/x)\n"
	got := RenderMarkdown(src, "x")
	for _, bad := range []string{"<script", "<img src=x", "href=\"javascript", "href=\"data:", "vbscript:", "src=\"javascript", "src=\"data:", "href=\"//evil"} {
		if strings.Contains(got, bad) {
			t.Errorf("the page contains %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{
		"&lt;script&gt;", `href="https://example.com/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer"`, `href="mailto:a@b.c"`,
		`href="other.md?render=md#part"`, `href="pic.png"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the page lacks %q:\n%s", want, got)
		}
	}
}

func TestMarkdownTitleIsEscaped(t *testing.T) {
	if got := RenderMarkdown("x", `</title><script>1</script>`); strings.Contains(got, "<script>1") {
		t.Errorf("the title was not escaped: %s", got)
	}
}

func TestServeRenderedMarkdownAndImagePage(t *testing.T) {
	home := workspace(t)
	must(t, os.WriteFile(filepath.Join(home, "read me.md"), []byte("# Hello\n\n![](logo.svg)\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o644))
	rec := get(home, "/f/read%20me.md?render=md", "read me.md")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<h1 id=\"hello\">Hello</h1>") {
		t.Fatalf("markdown: %d %s", rec.Code, rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || strings.Contains(csp, "allow-scripts") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("a rendered page must allow no script: %q", csp)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", rec.Header().Get("Content-Type"))
	}
	// without the parameter a Markdown file is still the text it is
	plain := get(home, "/f/notes.md", "notes.md")
	if !strings.HasPrefix(plain.Header().Get("Content-Type"), "text/plain") || plain.Body.String() != "# notes" {
		t.Errorf("plain: %q %q", plain.Header().Get("Content-Type"), plain.Body.String())
	}
	img := get(home, "/f/logo.svg?render=image", "logo.svg")
	if img.Code != 200 || !strings.Contains(img.Body.String(), `<img src="logo.svg"`) || strings.Contains(img.Body.String(), "<script") {
		t.Errorf("image page: %d %s", img.Code, img.Body.String())
	}
	if !strings.Contains(img.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("image page policy: %q", img.Header().Get("Content-Security-Policy"))
	}
}

func TestServePDFIsAPDFAndNothingElse(t *testing.T) {
	home := workspace(t)
	must(t, os.WriteFile(filepath.Join(home, "doc.pdf"), []byte("%PDF-1.4\n<script>alert(1)</script>"), 0o644))
	rec := get(home, "/f/doc.pdf", "doc.pdf")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("pdf: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("a PDF carries no sandbox policy (its viewer does not run under one): %q", rec.Header().Get("Content-Security-Policy"))
	}
}

func TestRenderedMarkdownTooLarge(t *testing.T) {
	home := workspace(t)
	must(t, os.WriteFile(filepath.Join(home, "big.md"), []byte(strings.Repeat("a", MaxMarkdown+1)), 0o644))
	if rec := get(home, "/f/big.md?render=md", "big.md"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a file over the limit is refused: %d", rec.Code)
	}
}

var _ = httptest.NewRecorder
