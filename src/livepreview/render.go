package livepreview

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// RenderMarkdown turns Markdown into a page that cannot run anything. All text is escaped first, so a tag in the
// file is shown as text, never run; a link or a picture goes only to http, https, mailto, a path of the workspace or
// an anchor; and the page is served with a policy that allows no script (ServeFile). It covers what a README needs:
// headings, paragraphs, emphasis, code (inline and fenced), quotes, lists, tables, rules, links and pictures.
func RenderMarkdown(src, title string) string {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\x00", ""), "\r\n", "\n")
	var out strings.Builder
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			fence := trim[:3]
			i++
			var code []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				code = append(code, lines[i])
				i++
			}
			i++ // the closing fence
			out.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case headingLevel(trim) > 0:
			n := headingLevel(trim)
			text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trim[n:]), "#"))
			fmt.Fprintf(&out, "<h%d id=\"%s\">%s</h%d>\n", n, slug(text), inline(text), n)
			i++
		case isRule(trim):
			out.WriteString("<hr>\n")
			i++
		case strings.HasPrefix(trim, ">"):
			var quote []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				quote = append(quote, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
				i++
			}
			out.WriteString("<blockquote><p>" + inline(strings.Join(quote, " ")) + "</p></blockquote>\n")
		case isTableStart(lines, i):
			i = table(&out, lines, i)
		case listItem(trim) != "":
			ordered := orderedItem.MatchString(trim)
			tag := "ul"
			if ordered {
				tag = "ol"
			}
			out.WriteString("<" + tag + ">\n")
			for i < len(lines) && listItem(strings.TrimSpace(lines[i])) != "" && orderedItem.MatchString(strings.TrimSpace(lines[i])) == ordered {
				out.WriteString("<li>" + inline(listItem(strings.TrimSpace(lines[i]))) + "</li>\n")
				i++
			}
			out.WriteString("</" + tag + ">\n")
		default:
			var para []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !startsBlock(lines, i) {
				para = append(para, strings.TrimSpace(lines[i]))
				i++
			}
			out.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
		}
	}
	return page(title, out.String())
}

var (
	orderedItem = regexp.MustCompile(`^\d{1,9}[.)] `)
	bulletItem  = regexp.MustCompile(`^[-*+] `)
	ruleLine    = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	tableSep    = regexp.MustCompile(`^\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?$`)
)

func headingLevel(trim string) int {
	n := 0
	for n < len(trim) && n < 6 && trim[n] == '#' {
		n++
	}
	if n > 0 && len(trim) > n && trim[n] == ' ' {
		return n
	}
	return 0
}

func isRule(trim string) bool { return ruleLine.MatchString(strings.ReplaceAll(trim, " ", "")) }

func listItem(trim string) string {
	if bulletItem.MatchString(trim) {
		return strings.TrimSpace(trim[2:])
	}
	if loc := orderedItem.FindStringIndex(trim); loc != nil {
		return strings.TrimSpace(trim[loc[1]:])
	}
	return ""
}

func startsBlock(lines []string, i int) bool {
	trim := strings.TrimSpace(lines[i])
	return strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") || headingLevel(trim) > 0 || isRule(trim) ||
		strings.HasPrefix(trim, ">") || listItem(trim) != "" || isTableStart(lines, i)
}

func isTableStart(lines []string, i int) bool {
	return i+1 < len(lines) && strings.Contains(lines[i], "|") && tableSep.MatchString(strings.TrimSpace(lines[i+1])) && strings.Contains(lines[i+1], "-")
}

func cells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func table(out *strings.Builder, lines []string, i int) int {
	out.WriteString("<table>\n<thead><tr>")
	for _, c := range cells(lines[i]) {
		out.WriteString("<th>" + inline(c) + "</th>")
	}
	out.WriteString("</tr></thead>\n<tbody>\n")
	i += 2
	for i < len(lines) && strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "|") {
		out.WriteString("<tr>")
		for _, c := range cells(lines[i]) {
			out.WriteString("<td>" + inline(c) + "</td>")
		}
		out.WriteString("</tr>\n")
		i++
	}
	out.WriteString("</tbody></table>\n")
	return i
}

var (
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	imageRe  = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+&#34;[^)]*&#34;)?\)`)
	linkRe   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+&#34;[^)]*&#34;)?\)`)
	boldRe   = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	starRe   = regexp.MustCompile(`\*([^*\s][^*]*?)\*`)
	underRe  = regexp.MustCompile(`(^|[^\w])_([^_\s][^_]*?)_([^\w]|$)`)
	strikeRe = regexp.MustCompile(`~~(.+?)~~`)
	schemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*):`)
)

// inline: code spans are taken out first (their text is shown as it is), then everything is escaped, then the marks
// of emphasis, pictures and links are turned into tags.
func inline(s string) string {
	var codes []string
	s = codeSpan.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = html.EscapeString(s)
	s = imageRe.ReplaceAllStringFunc(s, func(m string) string {
		g := imageRe.FindStringSubmatch(m)
		src, ok := safeURL(g[2], false)
		if !ok {
			return g[1]
		}
		return `<img alt="` + g[1] + `" src="` + src + `">`
	})
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		href, ok := safeURL(g[2], true)
		if !ok {
			return g[1]
		}
		extra := ""
		if schemeRe.MatchString(href) {
			extra = ` target="_blank" rel="noopener noreferrer"`
		}
		return `<a href="` + href + `"` + extra + `>` + g[1] + `</a>`
	})
	s = boldRe.ReplaceAllString(s, "<strong>$1$2</strong>")
	s = starRe.ReplaceAllString(s, "<em>$1</em>")
	s = underRe.ReplaceAllString(s, "$1<em>$2</em>$3")
	s = strikeRe.ReplaceAllString(s, "<del>$1</del>")
	for i, c := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), c, 1)
	}
	return s
}

// safeURL takes an address as it stands in escaped text. A link may go to http, https, mailto, an anchor or a path of
// the workspace; a picture to http, https or a path (no mailto). A path that names a Markdown file stays rendered.
func safeURL(u string, link bool) (string, bool) {
	u = strings.TrimSpace(u)
	if m := schemeRe.FindStringSubmatch(u); m != nil {
		switch strings.ToLower(m[1]) {
		case "http", "https":
			return u, true
		case "mailto":
			return u, link
		}
		return "", false
	}
	if strings.HasPrefix(u, "//") || strings.ContainsAny(u, "\\") {
		return "", false
	}
	if link {
		rawPath, frag := u, ""
		if i := strings.IndexByte(u, '#'); i >= 0 {
			rawPath, frag = u[:i], u[i:]
		}
		low := strings.ToLower(rawPath)
		if strings.HasSuffix(low, ".md") || strings.HasSuffix(low, ".markdown") {
			return rawPath + "?render=md" + frag, true
		}
	}
	return u, true
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

const pageCSS = `html{color-scheme:light dark}
body{margin:0;padding:24px 28px;font:15px/1.65 system-ui,-apple-system,"Segoe UI",sans-serif;background:#fff;color:#1f1f24}
@media (prefers-color-scheme:dark){body{background:#14141b;color:#ece9e2}a{color:#8cc8ff}code,pre{background:#1f1f2a}th,td,hr,blockquote{border-color:#33333f}}
main{max-width:860px;margin:0 auto}
h1,h2{border-bottom:1px solid #d0d0d8;padding-bottom:.25em}
h1,h2,h3,h4,h5,h6{line-height:1.25;margin:1.4em 0 .6em}
a{color:#1a56db}img{max-width:100%}
code{font:13.5px ui-monospace,Menlo,Consolas,monospace;background:#f0f0f4;padding:.15em .35em;border-radius:5px}
pre{background:#f0f0f4;padding:12px 14px;border-radius:8px;overflow:auto}pre code{background:none;padding:0}
blockquote{margin:1em 0;padding:.1em 1em;border-left:4px solid #c8c8d4;color:#5a5a66}
table{border-collapse:collapse}th,td{border:1px solid #d0d0d8;padding:6px 12px}th{text-align:left}
hr{border:0;border-top:1px solid #d0d0d8}`

func page(title, body string) string {
	return "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" +
		html.EscapeString(title) + "</title><style>" + pageCSS + "</style></head><body><main>\n" + body + "</main></body></html>\n"
}

// ImagePage shows a picture of the workspace and nothing else: a page with no script, the picture drawn to fit and a
// click (a checkbox and its label) for its own size. A vector picture is drawn as a picture, so nothing in it runs.
func ImagePage(name string) string {
	src := url.PathEscape(name)
	n := html.EscapeString(name)
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + n + `</title><style>
html,body{margin:0;height:100%;background:#1b1b24;color:#a9a7b4;font:13px system-ui,sans-serif}
body{display:flex;overflow:auto}
#z{position:absolute;opacity:0;pointer-events:none}
label{margin:auto;display:block;cursor:zoom-in;padding:8px;box-sizing:border-box}
img{display:block;max-width:calc(100vw - 16px);max-height:calc(100vh - 16px);object-fit:contain;background:repeating-conic-gradient(#2a2a36 0 25%,#222230 0 50%) 0 0/16px 16px}
#z:checked+label{cursor:zoom-out}#z:checked+label img{max-width:none;max-height:none}
</style></head><body><input type="checkbox" id="z" aria-label="Show at its own size"><label for="z"><img src="` + src + `" alt="` + n + `"></label></body></html>`
}
