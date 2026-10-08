package server

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// What Genie knows about OpenREPL itself: a small set of notes written for the
// purpose (resources/knowledge/*.md, compiled in), and the blog posts, read
// from the blog store. A request that asks for it gets the few passages that
// match the question added to its system message. The search is BM25 over
// stemmed words, in memory: no service, no extra cost. See LLD 07.

// A note is a Markdown file with a short header:
//
//	---
//	title: Sharing a live session
//	keywords: share, send, friend
//	link: /about.html
//	---
//	The text.
//
// Its id is the file name without ".md".

// ---- the passages ----------------------------------------------------------------

const (
	kindNote = "note"
	kindPost = "post"

	// a blog post is split into passages of about this many words
	postPassageWords = 140
	// no passage is sent on longer than this
	maxPassageChars = 1400
	// a post's passages that are indexed, at most
	maxPostPassages = 24
)

// A passage is one entry of the index.
type passage struct {
	ID       string
	Kind     string
	Title    string
	Keywords string
	Text     string
	Link     string // a path on this site, or ""

	tf  map[string]float64 // weighted term counts
	len float64
}

var passageID = regexp.MustCompile(`^[a-z0-9-]{1,80}$`)

// ---- words -----------------------------------------------------------------------

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a about above after again all also am an and any are as at be because been before being below between both
		but by can cannot could did do does doing don down during each few for from further had has have having he her here hers him his how i if in
		into is it its itself just me more most my myself no nor not now of off on once only or other our ours out over own same she should so some such
		than that the their theirs them then there these they this those through to too under until up very was we were what when where which while who
		whom why will with would you your yours yourself also get got make made need want like please tell show use used using way
		someone anyone everyone somebody anybody everybody happen happens happened`) {
		stopwords[w] = true
	}
}

// stem cuts a word down to a root, so that "sharing", "shared" and "shares"
// meet. It is crude on purpose; it only has to treat both sides the same.
func stem(w string) string {
	if len(w) <= 3 {
		return w
	}
	cut := func(suffix, repl string, minStem int) bool {
		if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= minStem {
			w = w[:len(w)-len(suffix)] + repl
			return true
		}
		return false
	}
	switch {
	case cut("sses", "ss", 2), cut("ies", "y", 2):
	case strings.HasSuffix(w, "ss"):
	case cut("s", "", 3):
	}
	hasVowel := func(s string) bool { return strings.ContainsAny(s, "aeiouy") }
	switch {
	case strings.HasSuffix(w, "ing") && len(w) > 5 && hasVowel(w[:len(w)-3]):
		w = w[:len(w)-3]
	case strings.HasSuffix(w, "ed") && len(w) > 4 && hasVowel(w[:len(w)-2]):
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "ly") && len(w) > 5:
		w = w[:len(w)-2]
	default:
		goto done
	}
	// "running" -> "runn" -> "run"; "shared" -> "shar" stays, as "share" -> "share" below
	if n := len(w); n > 3 && w[n-1] == w[n-2] && !strings.ContainsRune("lsz", rune(w[n-1])) {
		w = w[:n-1]
	}
done:
	if strings.HasSuffix(w, "e") && len(w) > 3 {
		w = w[:len(w)-1] // share -> shar, so it meets "sharing" -> "shar"
	}
	return w
}

// terms splits text into stemmed words without the filler ones.
func terms(text string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '#')
	}) {
		if f == "" || stopwords[f] {
			continue
		}
		out = append(out, stem(f))
	}
	return out
}

// ---- the index -------------------------------------------------------------------

// Index is a set of passages ready to be searched.
type Index struct {
	passages []*passage
	byID     map[string]*passage
	df       map[string]int
	avgLen   float64
}

// Weights of the parts of a passage when its words are counted.
const (
	weightTitle    = 5.0
	weightKeywords = 2.0
	weightText     = 1.0
)

func newIndex(list []*passage) *Index {
	ix := &Index{byID: make(map[string]*passage, len(list)), df: map[string]int{}}
	var total float64
	for _, p := range list {
		if _, dup := ix.byID[p.ID]; dup {
			continue
		}
		p.tf = map[string]float64{}
		add := func(text string, weight float64) {
			for _, t := range terms(text) {
				p.tf[t] += weight
				p.len += weight
			}
		}
		add(p.Title, weightTitle)
		add(p.Keywords, weightKeywords)
		add(p.Text, weightText)
		for t := range p.tf {
			ix.df[t]++
		}
		total += p.len
		ix.passages = append(ix.passages, p)
		ix.byID[p.ID] = p
	}
	if n := len(ix.passages); n > 0 {
		ix.avgLen = total / float64(n)
	}
	return ix
}

// Get returns the passage with an id, which must be one the index holds.
func (ix *Index) Get(id string) (*passage, bool) {
	if ix == nil || !passageID.MatchString(id) {
		return nil, false
	}
	p, ok := ix.byID[id]
	return p, ok
}

// A Hit is a passage that matched a question. Coverage is the share of the
// question, by the weight of its words, that the passage contains: a word that
// no passage has counts in full, because it says the question is about
// something else.
type Hit struct {
	P        *passage
	Score    float64
	Coverage float64
}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// a blog passage counts for a little less than a note that scores the
	// same: the notes are the facts, a post is one person's article
	postDiscount = 0.85
	// a hit must score at least this share of the best one
	relativeCut = 0.5
)

// Search returns up to limit passages whose score reaches min and whose
// coverage of the question reaches minCoverage, best first.
func (ix *Index) Search(query string, limit int, min, minCoverage float64) []Hit {
	if ix == nil || len(ix.passages) == 0 || limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	var q []string
	for _, t := range terms(query) {
		if !seen[t] {
			seen[t] = true
			q = append(q, t)
		}
	}
	if len(q) == 0 {
		return nil
	}
	n := float64(len(ix.passages))
	var all float64
	idfs := make([]float64, len(q))
	for i, t := range q {
		idfs[i] = logIDF(n, float64(ix.df[t]))
		all += idfs[i]
	}
	var hits []Hit
	for _, p := range ix.passages {
		var score, covered float64
		for i, t := range q {
			f := p.tf[t]
			if f == 0 {
				continue
			}
			covered += idfs[i]
			score += idfs[i] * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*p.len/ix.avgLen))
		}
		if p.Kind == kindPost {
			score *= postDiscount
		}
		coverage := covered / all
		if score >= min && score > 0 && coverage >= minCoverage {
			hits = append(hits, Hit{p, score, coverage})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) == 0 {
		return nil
	}
	cut := hits[0].Score * relativeCut
	out := hits[:0]
	for _, h := range hits {
		if h.Score < cut || len(out) == limit {
			break
		}
		out = append(out, h)
	}
	return out
}

func logIDF(n, df float64) float64 {
	// the usual BM25 form, kept above zero
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

// ---- reading the notes -----------------------------------------------------------

// parseNote reads one note. The id comes from the file name.
func parseNote(id string, data []byte) (*passage, error) {
	if !passageID.MatchString(id) {
		return nil, fmt.Errorf("the note %q has an id that is not letters, digits and dashes", id)
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, fmt.Errorf("the note %s has no header", id)
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil, fmt.Errorf("the note %s has an unfinished header", id)
	}
	p := &passage{ID: id, Kind: kindNote, Text: strings.TrimSpace(rest[end+5:])}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(strings.ToLower(k)) {
		case "title":
			p.Title = v
		case "keywords":
			p.Keywords = v
		case "link":
			p.Link = v
		}
	}
	if p.Title == "" {
		return nil, fmt.Errorf("the note %s has no title", id)
	}
	if p.Text == "" {
		return nil, fmt.Errorf("the note %s has no text", id)
	}
	if err := checkSiteLink(p.Link); err != nil {
		return nil, fmt.Errorf("the note %s: %v", id, err)
	}
	return p, nil
}

// checkSiteLink accepts "" or a path on this site. Anything that could lead
// somewhere else (a scheme, a host, "//") is refused.
func checkSiteLink(link string) error {
	if link == "" {
		return nil
	}
	if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") || strings.Contains(link, "\\") || strings.ContainsAny(link, " \t\r\n") {
		return fmt.Errorf("the link %q is not a path on this site", link)
	}
	if u, err := url.Parse(link); err != nil || u.Scheme != "" || u.Host != "" {
		return fmt.Errorf("the link %q is not a path on this site", link)
	}
	return nil
}

// loadNotes reads every note of a source.
func loadNotes(names []string, read func(name string) ([]byte, error)) ([]*passage, error) {
	var out []*passage
	for _, name := range names {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := read(name)
		if err != nil {
			return nil, err
		}
		p, err := parseNote(strings.TrimSuffix(name, ".md"), data)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ---- reading the blog posts ------------------------------------------------------

var markupPattern = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]*>`)

// plainText turns the HTML of a post into text.
func plainText(markup string) string {
	s := strings.NewReplacer("</p>", "\n\n", "<br>", "\n", "<br/>", "\n", "</li>", "\n", "</h1>", "\n\n", "</h2>", "\n\n", "</h3>", "\n\n").Replace(markup)
	s = markupPattern.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// postPassages splits a post into passages of about postPassageWords words,
// breaking at paragraphs.
func postPassages(name string, p BlogPost) []*passage {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = name
	}
	sum := sha1.Sum([]byte(name))
	prefix := "post-" + hex.EncodeToString(sum[:])[:10]
	link := "/blog?name=" + url.QueryEscape(name)

	body := plainText(p.Content)
	var chunks []string
	var cur []string
	words := 0
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, strings.Join(cur, "\n\n"))
			cur, words = nil, 0
		}
	}
	for _, para := range strings.Split(body, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		cur = append(cur, para)
		words += len(strings.Fields(para))
		if words >= postPassageWords {
			flush()
		}
	}
	flush()
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	if len(chunks) > maxPostPassages {
		chunks = chunks[:maxPostPassages]
	}
	out := make([]*passage, 0, len(chunks))
	for i, c := range chunks {
		text := c
		if i == 0 && strings.TrimSpace(p.Desc) != "" {
			text = strings.TrimSpace(p.Desc) + "\n\n" + c
		}
		out = append(out, &passage{
			ID: fmt.Sprintf("%s-%d", prefix, i+1), Kind: kindPost, Title: title, Text: strings.TrimSpace(text), Link: link,
		})
	}
	return out
}

// ---- the knowledge base ----------------------------------------------------------

// how long the blog part of the index is kept before the posts are read again
// when nobody saved or deleted one
const postIndexTTL = 5 * time.Minute

// knowledgeBase holds the notes, and the index of notes and posts.
type knowledgeBase struct {
	notes    []*passage
	posts    func() map[string]BlogPost // nil: no posts
	now      func() time.Time
	mu       sync.Mutex
	index    *Index
	builtAt  time.Time
	postsOld bool
}

func newKnowledgeBase(notes []*passage, posts func() map[string]BlogPost) *knowledgeBase {
	return &knowledgeBase{notes: notes, posts: posts, now: time.Now, postsOld: true}
}

// invalidate says that a post was saved or deleted, so the next search reads
// the posts again.
func (kb *knowledgeBase) invalidate() {
	kb.mu.Lock()
	kb.postsOld = true
	kb.mu.Unlock()
}

// Index returns the current index, building it first if the posts changed or
// were read too long ago.
func (kb *knowledgeBase) Index() *Index {
	kb.mu.Lock()
	defer kb.mu.Unlock()
	if kb.index != nil && !kb.postsOld && kb.now().Sub(kb.builtAt) < postIndexTTL {
		return kb.index
	}
	list := make([]*passage, 0, len(kb.notes)+16)
	for _, n := range kb.notes {
		c := *n
		list = append(list, &c)
	}
	if kb.posts != nil {
		posts := kb.posts()
		names := make([]string, 0, len(posts))
		for name := range posts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			list = append(list, postPassages(name, posts[name])...)
		}
	}
	kb.index = newIndex(list)
	kb.builtAt = kb.now()
	kb.postsOld = false
	return kb.index
}
