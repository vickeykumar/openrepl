package livepreview

import (
	"bytes"
	"html"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// A text file shown in the frame of the pane (a program that answers "Hello" as text/plain, a .txt or .json of the
// workspace) is drawn by the browser's own plain-text page, which takes the colours of the visitor's system: under a dark
// editor that is white text, on the frame's white ground, so the frame looks empty. Asked for as a frame, such an
// answer is wrapped in a small light page of its own. Anything else that asks (fetch, a script, a download, the page
// opened in its own tab) gets the text as it is.

// plainDest says whether the request is for the document of a frame.
func plainDest(r *http.Request) bool {
	return r != nil && r.Header.Get("Sec-Fetch-Dest") == "iframe" && r.Header.Get("Range") == ""
}

func isPlainType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return ct == "text/plain" || ct == "application/json"
}

// PlainPage is text as a light page.
func PlainPage(text []byte) []byte {
	var b bytes.Buffer
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="color-scheme" content="light"><style>` +
		`html{color-scheme:light;background:#fff}body{margin:12px;background:#fff;color:#111}` +
		`pre{margin:0;white-space:pre-wrap;word-wrap:break-word;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}` +
		`</style></head><body><pre>`)
	b.WriteString(html.EscapeString(string(text)))
	b.WriteString("</pre></body></html>")
	return b.Bytes()
}

// wrapPlain turns a plain-text answer for a frame into a page. It reports whether it did.
func wrapPlain(res *http.Response) (bool, error) {
	if res.StatusCode == http.StatusPartialContent || res.StatusCode == http.StatusSwitchingProtocols || !isPlainType(res.Header.Get("Content-Type")) || !plainDest(res.Request) {
		return false, nil
	}
	if res.Header.Get("Content-Encoding") != "" || res.Header.Get("Content-Disposition") != "" {
		return false, nil
	}
	if n, err := strconv.Atoi(res.Header.Get("Content-Length")); err == nil && n > maxRewrite {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxRewrite+1))
	if err != nil {
		return false, err
	}
	if len(body) > maxRewrite {
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), res.Body), res.Body}
		return false, nil
	}
	res.Body.Close()
	out := PlainPage(body)
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Set("Content-Length", strconv.Itoa(len(out)))
	res.Header.Set("Content-Type", "text/html; charset=utf-8")
	res.Header.Del("Etag")
	return true, nil
}

// plainFile says whether a file of the workspace, asked for as a frame, is shown as a page of its own.
func plainFile(r *http.Request, name string) bool {
	return r.Method == http.MethodGet && plainDest(r) && isPlainType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))))
}
