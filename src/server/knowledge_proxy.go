package server

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
)

// The chat proxy side of the knowledge base: a request that asks for it gets
// the passages that match its question added to the system message, and the
// answer says which ones were used. The page asks with two body fields that
// never reach the model:
//
//	"context": "chat" | "blog"     the Genie panel, or the blog editor
//	"context_hint": "<text>"       more words to search with (the post's title)
//
// The Genie panel only wants the notes when one matches strongly, so ordinary
// coding questions are left as they are. The blog editor always wants the best
// ones. The practice page does not ask.

const (
	contextChat = "chat"
	contextBlog = "blog"

	// the lowest score a passage needs, and how many are sent on at most
	chatContextMin      = 2.0
	chatContextCoverage = 0.65
	chatContextLimit    = 3
	blogContextMin      = 1.5
	blogContextCoverage = 0.0
	blogContextLimit    = 4

	// everything added to a request is cut to this
	maxContextChars = 5000

	// the response header that lists what was used: "none", or base64url of
	// [{"id":..., "title":..., "kind":...}]
	contextHeader = "X-OpenREPL-Context"
)

// ---- the shared knowledge base --------------------------------------------------

var (
	knowledgeMu    sync.Mutex
	knowledgeBase_ *knowledgeBase
)

// theKnowledge returns the knowledge base of the server, reading the compiled-in
// notes the first time. A note that does not pass its checks is logged and
// skipped, so one bad file cannot take the server down.
func theKnowledge() *knowledgeBase {
	knowledgeMu.Lock()
	defer knowledgeMu.Unlock()
	if knowledgeBase_ == nil {
		var notes []*passage
		names, err := AssetDir("knowledge")
		if err != nil {
			log.Println("knowledge: no notes compiled in:", err)
		}
		for _, name := range names {
			list, err := loadNotes([]string{name}, func(n string) ([]byte, error) { return Asset("knowledge/" + n) })
			if err != nil {
				log.Println("knowledge: skipping", name+":", err)
				continue
			}
			notes = append(notes, list...)
		}
		knowledgeBase_ = newKnowledgeBase(notes, blogPostsForKnowledge)
		log.Printf("knowledge: %d notes", len(notes))
	}
	return knowledgeBase_
}

// blogPostsForKnowledge reads the posts, or nothing when the blog store is not
// open (a node that does not serve the blog).
func blogPostsForKnowledge() map[string]BlogPost {
	if blog_db_handle == nil {
		return nil
	}
	return FetchBlogDataMap()
}

// setKnowledge replaces the knowledge base, for tests.
func setKnowledge(kb *knowledgeBase) {
	knowledgeMu.Lock()
	knowledgeBase_ = kb
	knowledgeMu.Unlock()
}

// knowledgeInvalidate is called when a post is saved or deleted.
func knowledgeInvalidate() {
	knowledgeMu.Lock()
	kb := knowledgeBase_
	knowledgeMu.Unlock()
	if kb != nil {
		kb.invalidate()
	}
}

// ---- adding it to a request -----------------------------------------------------

// contextResult says what a request asked for and what it got.
type contextResult struct {
	Asked bool
	Hits  []Hit
}

// header is the value of the response header, or "" when nothing was asked.
func (c contextResult) header() string {
	if !c.Asked {
		return ""
	}
	if len(c.Hits) == 0 {
		return "none"
	}
	type used struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Kind  string `json:"kind"`
	}
	list := make([]used, 0, len(c.Hits))
	for _, h := range c.Hits {
		list = append(list, used{h.P.ID, h.P.Title, h.P.Kind})
	}
	data, _ := json.Marshal(list)
	return base64.RawURLEncoding.EncodeToString(data)
}

// lastUserText is the text of the last message the user wrote.
func lastUserText(messages []json.RawMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(messages[i], &m) != nil || m.Role != "user" {
			continue
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			return text
		}
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			var b strings.Builder
			for _, p := range parts {
				b.WriteString(p.Text)
				b.WriteByte(' ')
			}
			return b.String()
		}
		return ""
	}
	return ""
}

// addKnowledge looks the question of a request up and returns the body to send
// on: the same one, with a system message in front when something matched. A
// body that does not ask, or that cannot be read, is returned as it is.
func addKnowledge(body []byte, kb *knowledgeBase) ([]byte, contextResult) {
	var in map[string]json.RawMessage
	if json.Unmarshal(body, &in) != nil {
		return body, contextResult{}
	}
	var mode string
	if raw, ok := in["context"]; !ok || json.Unmarshal(raw, &mode) != nil || (mode != contextChat && mode != contextBlog) {
		return body, contextResult{}
	}
	var messages []json.RawMessage
	if json.Unmarshal(in["messages"], &messages) != nil || len(messages) == 0 {
		return body, contextResult{}
	}
	res := contextResult{Asked: true}
	query := lastUserText(messages)
	// A hint is what the page wants to search with: the blog editor sends the
	// title of the post and the text it works on, because its prompt is mostly
	// instructions ("write a short blog post section...") that would match the
	// wrong notes.
	if raw, ok := in["context_hint"]; ok {
		var hint string
		if json.Unmarshal(raw, &hint) == nil && strings.TrimSpace(hint) != "" {
			query = cutRunes(hint, 400)
		}
	}
	min, coverage, limit := chatContextMin, chatContextCoverage, chatContextLimit
	if mode == contextBlog {
		min, coverage, limit = blogContextMin, blogContextCoverage, blogContextLimit
	}
	if kb == nil {
		return body, res
	}
	res.Hits = kb.Index().Search(query, limit, min, coverage)
	if len(res.Hits) == 0 {
		return body, res
	}
	system, _ := json.Marshal(map[string]string{"role": "system", "content": contextPrompt(res.Hits)})
	in["messages"], _ = json.Marshal(append([]json.RawMessage{system}, messages...))
	out, err := json.Marshal(in)
	if err != nil {
		return body, contextResult{Asked: true}
	}
	return out, res
}

// contextPrompt is the system message that carries the passages.
func contextPrompt(hits []Hit) string {
	var b strings.Builder
	b.WriteString("Facts about OpenREPL, the website this conversation runs on, taken from its own notes. " +
		"Use them to answer questions about OpenREPL. Do not invent features, limits, prices or steps they do not mention, " +
		"and if they do not cover what is asked, say that you are not sure. " +
		"A passage marked [blog post] comes from an article on the site's blog: use it only if it answers the question, and say that it comes from a post.\n")
	for _, h := range hits {
		kind := "note"
		if h.P.Kind == kindPost {
			kind = "blog post"
		}
		text := cutRunes(h.P.Text, maxPassageChars)
		if b.Len()+len(text) > maxContextChars {
			break
		}
		b.WriteString("\n[" + kind + "] " + h.P.Title + "\n" + text + "\n")
	}
	return b.String()
}

// cutRunes shortens s to at most n bytes without breaking a character.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// ---- the endpoint ----------------------------------------------------------------

// handleKnowledgeAt serves GET <prefix>knowledge/<id>: the text of a note or of
// a blog passage the index holds, so the Genie panel can show what an answer
// was based on. An id that is not in the index is a 404, whatever it looks like.
func handleKnowledgeAt(prefix string) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			rw.Header().Set("Allow", "GET, HEAD")
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(req.URL.Path, prefix+"knowledge/")
		p, ok := theKnowledge().Index().Get(id)
		if !ok {
			http.NotFound(rw, req)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(rw).Encode(map[string]string{
			"id": p.ID, "kind": p.Kind, "title": p.Title, "text": cutRunes(p.Text, maxPassageChars), "link": p.Link,
		})
	}
}
