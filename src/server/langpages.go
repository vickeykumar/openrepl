package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
)

// Language landing pages (T12): /python, /cpp and so on. Each one serves the
// home page with its own title, description and heading, and the workspace
// starts in that language. /sitemap.xml lists them for search engines.

// LangPage describes one language page.
type LangPage struct {
	Slug   string // URL path without the slash, e.g. "python"
	Repl   string // value of the language in #optionlist, e.g. "python"
	Name   string // display name, e.g. "Python"
	Detail string // one sentence on what this REPL offers
}

var langPages = []LangPage{
	{"c", "c", "C", "Type C statements in the Cling interpreter, or write a whole program and press Run."},
	{"cpp", "cpp", "C++", "Try C++ one statement at a time in the Cling interpreter, or write a whole program and press Run."},
	{"go", "go", "Go", "Try Go expressions in the Go interpreter, or write a whole program and press Run."},
	{"python", "python", "Python", "Python 3 is ready to type in, and IPython and Python 2.7 are in the language picker."},
	{"rust", "evcxr", "Rust", "Evaluate Rust expressions in the evcxr REPL, or write a whole program and press Run."},
	{"java", "java", "Java", "Try Java snippets in JShell, or write a whole class and press Run."},
	{"javascript", "javascript", "JavaScript", "Run JavaScript in a browser console, right on the page."},
	{"nodejs", "node", "Node.js", "Use the Node.js REPL with the full standard library, or run a whole script."},
	{"typescript", "ts-node", "TypeScript", "Type TypeScript into a ts-node REPL, or run a whole file."},
	{"ruby", "irb", "Ruby", "Try Ruby in irb, or write a whole script and press Run."},
	{"perl", "perli", "Perl", "Try Perl one line at a time, or write a whole script and press Run."},
	{"bash", "bash", "Bash", "Get a real Bash shell in its own sandbox, so you can try commands and scripts safely."},
	{"tcl", "tclsh", "Tcl", "Try Tcl in tclsh, or write a whole script and press Run."},
	{"sqlite", "sqlite3", "SQLite", "Write SQL in the sqlite3 shell, with an example table ready to query."},
	{"jq", "jq-repl", "jq", "Filter JSON with jq interactively, starting from a sample document."},
	{"assembly", "rappel", "x86 Assembly", "Step through x86 assembly in the rappel REPL and watch the registers change."},
}

var langPageBySlug = func() map[string]LangPage {
	m := make(map[string]LangPage, len(langPages))
	for _, p := range langPages {
		m[p.Slug] = p
	}
	return m
}()

// langPageFor returns the language page for a request path such as "/python".
func langPageFor(path string) (LangPage, bool) {
	p, ok := langPageBySlug[strings.Trim(path, "/")]
	return p, ok
}

// siteURL is the scheme and host the request came in on, e.g. https://openrepl.com.
func siteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// indexPage is what index.html prints in its title, meta tags and hero.
type indexPage struct {
	URL          string
	Title        string
	Description  string
	Eyebrow      string
	Heading      string // first part of the hero heading
	HeadingColor string // the part shown in the accent colour
	Blurb        string
	Client       map[string]interface{} // window.OPENREPL_PAGE for scribbler.js
}

const homeTitle = "OpenREPL: Online Code Editor and REPL for C++, Go, Python and more"
const homeDescription = "Write, run and debug code in your browser. A free, open-source REPL and editor for C, C++, Go, Python, Rust and many more, with an AI assistant. No install needed."

func langPageData(p LangPage) indexPage {
	return indexPage{
		Title:        fmt.Sprintf("Online %s REPL and editor | OpenREPL", p.Name),
		Description:  fmt.Sprintf("Run %s online in your browser. %s Free, no install, no sign-up.", p.Name, p.Detail),
		Eyebrow:      fmt.Sprintf("Free online %s REPL · no install", p.Name),
		Heading:      fmt.Sprintf("Run %s", p.Name),
		HeadingColor: "in your browser.",
		Blurb:        p.Detail + " Type a line and see the result instantly, then share a live link.",
	}
}

// clientLangPages is sent to the page so switching language on a language
// page can update the address, title and heading without a reload.
func clientLangPages() []map[string]string {
	out := make([]map[string]string, 0, len(langPages))
	for _, p := range langPages {
		d := langPageData(p)
		out = append(out, map[string]string{
			"slug": p.Slug, "repl": p.Repl, "name": p.Name, "title": d.Title,
			"eyebrow": d.Eyebrow, "heading": d.Heading, "headingColor": d.HeadingColor, "blurb": d.Blurb,
		})
	}
	return out
}

// indexPageFor builds the template data for "/", "/practice" or a language page.
func indexPageFor(r *http.Request) indexPage {
	base := siteURL(r)
	if p, ok := langPageFor(r.URL.Path); ok {
		d := langPageData(p)
		d.URL = base + "/" + p.Slug
		d.Client = map[string]interface{}{"slug": p.Slug, "repl": p.Repl, "pages": clientLangPages()}
		return d
	}
	return indexPage{
		URL:          base + r.URL.Path,
		Title:        homeTitle,
		Description:  homeDescription,
		Eyebrow:      "Free and open source · 19 languages",
		Heading:      "Write it. Run it.",
		HeadingColor: "Share it.",
		Blurb:        "An online REPL and editor for C, C++, Go, Python, Rust and 14 more. Type a code snippet and see the result instantly.",
		Client:       map[string]interface{}{"slug": "", "repl": "", "pages": clientLangPages()},
	}
}

// handleSitemap serves /sitemap.xml with the home page, the language pages
// and the other public pages.
func handleSitemap(w http.ResponseWriter, r *http.Request) {
	base := siteURL(r)
	paths := []string{"/", "/practice/dsa-questions", "/doc.html", "/about.html", "/privacy.html", "/terms.html"}
	for _, p := range langPages {
		paths = append(paths, "/"+p.Slug)
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc></url>\n", base, p)
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write(b.Bytes())
}
