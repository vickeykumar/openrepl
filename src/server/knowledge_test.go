package server

import (
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// the notes that ship with the site
func realNotes(t *testing.T) []*passage {
	t.Helper()
	dir := filepath.Join("..", "resources", "knowledge")
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	notes, err := loadNotes(names, func(n string) ([]byte, error) { return ioutil.ReadFile(filepath.Join(dir, n)) })
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) < 10 {
		t.Fatalf("only %d notes", len(notes))
	}
	return notes
}

func testKB(t *testing.T, posts map[string]BlogPost) *knowledgeBase {
	t.Helper()
	var f func() map[string]BlogPost
	if posts != nil {
		f = func() map[string]BlogPost { return posts }
	}
	return newKnowledgeBase(realNotes(t), f)
}

func TestStemmingMeetsTheFormsOfAWord(t *testing.T) {
	groups := [][]string{
		{"share", "shares", "shared", "sharing"},
		{"file", "files"},
		{"fork", "forks", "forked", "forking"},
		{"run", "running", "runs"},
		{"question", "questions"},
		{"language", "languages"},
		{"code", "codes", "coding"},
	}
	for _, g := range groups {
		want := stem(g[0])
		for _, w := range g[1:] {
			if got := stem(w); got != want {
				t.Errorf("stem(%q) = %q, stem(%q) = %q: they should meet", w, got, g[0], want)
			}
		}
	}
	if stem("go") != "go" || stem("c++") != "c++" || stem("css") != "css" {
		t.Errorf("short words and symbols must stay: %q %q %q", stem("go"), stem("c++"), stem("css"))
	}
	if got := terms("How do I share my code with a friend?"); strings.Join(got, " ") != "shar cod friend" {
		t.Errorf("terms = %v", got)
	}
}

func TestNoteParsing(t *testing.T) {
	good := "---\ntitle: A note\nkeywords: a, b\nlink: /about.html\n---\nThe text.\n"
	p, err := parseNote("a-note", []byte(good))
	if err != nil || p.Title != "A note" || p.Keywords != "a, b" || p.Link != "/about.html" || p.Text != "The text." || p.Kind != kindNote {
		t.Fatalf("good note: %+v %v", p, err)
	}
	if _, err := parseNote("a-note", []byte(strings.ReplaceAll(good, "\n", "\r\n"))); err != nil {
		t.Errorf("a note with Windows line ends: %v", err)
	}
	if p, err := parseNote("a-note", []byte("---\ntitle: T\n---\nText\n")); err != nil || p.Link != "" {
		t.Errorf("a note without a link: %+v %v", p, err)
	}
	bad := map[string]string{
		"no header":     "title: A\n\nText",
		"unfinished":    "---\ntitle: A\nText",
		"no title":      "---\nkeywords: a\n---\nText",
		"no text":       "---\ntitle: A\n---\n   \n",
		"outside link":  "---\ntitle: A\nlink: https://evil.example/\n---\nText",
		"protocol link": "---\ntitle: A\nlink: //evil.example/x\n---\nText",
		"script link":   "---\ntitle: A\nlink: javascript:alert(1)\n---\nText",
		"relative link": "---\ntitle: A\nlink: about.html\n---\nText",
		"backslash":     "---\ntitle: A\nlink: /\\evil.example\n---\nText",
		"space":         "---\ntitle: A\nlink: /a b\n---\nText",
	}
	for name, text := range bad {
		if _, err := parseNote("a-note", []byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, id := range []string{"", "A-note", "a_note", "a.note", "../x", "a/b"} {
		if _, err := parseNote(id, []byte(good)); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
}

func TestEveryShippedNoteIsValidAndLinksToThisSite(t *testing.T) {
	for _, n := range realNotes(t) {
		if n.Title == "" || n.Text == "" || n.Keywords == "" {
			t.Errorf("%s: needs a title, keywords and text", n.ID)
		}
		if w := len(strings.Fields(n.Text)); w > 220 {
			t.Errorf("%s: %d words; a note is one short topic", n.ID, w)
		}
	}
}

// What a visitor might ask, and the note that should answer it.
func TestSearchFindsTheRightNoteForQuestionsAboutTheSite(t *testing.T) {
	ix := testKB(t, nil).Index()
	cases := []struct{ q, want string }{
		{"How can I send my code to a teammate?", "sharing-a-session"},
		{"how do I share my session with a friend", "sharing-a-session"},
		{"can someone type along in my terminal", "sharing-a-session"},
		{"how do I make a link to my code", "code-links"},
		{"how do I fork a REPL", "fork-a-repl"},
		{"run a server and a client side by side", "fork-a-repl"},
		{"which languages are supported", "languages"},
		{"is there a rust repl", "languages"},
		{"how do I debug c code with gdb", "run-and-debug"},
		{"why does debug start paused and say qemu", "run-and-debug"},
		{"can I debug go or assembly with gdb", "run-and-debug"},
		{"how do I upload a file", "files-and-workspace"},
		{"what happens to my files after an hour", "files-and-workspace"},
		{"do I need an account", "sign-in-and-accounts"},
		{"how long does a session last", "session-time-limit"},
		{"which models does genie use", "genie"},
		{"how do I use genie", "genie"},
		{"does openrepl have a file explorer sidebar", "files-and-workspace"},
		{"how do I open genie", "genie"},
		{"what does the insert button do in genie", "genie-insert-and-replace"},
		{"how do I undo what genie put in my editor", "genie-insert-and-replace"},
		{"what is agent mode", "genie-agent-mode"},
		{"why does my for loop not run in the go repl", "go-repl"},
		{"what does :r do in gointerpreter", "go-repl"},
		{"does genie keep working if i close the panel", "genie"},
		{"how many genie requests do i have left", "genie"},
		{"how do I make genie run my code for me", "genie-agent-mode"},
		{"how do I stop genie while it is working on a task", "genie-agent-mode"},
		{"can genie type commands in my terminal", "genie-terminal-and-safety"},
		{"is it safe to let genie run commands", "genie-terminal-and-safety"},
		{"can genie open a new terminal tab", "genie-agent-mode"},
		{"can genie restart my terminal", "genie-agent-mode"},
		{"how do I make genie add comments to a function", "genie-right-click-actions"},
		{"how do I explain a selected piece of code with genie", "genie-right-click-actions"},
		{"how do I make genie write tests for a function", "genie-right-click-actions"},
		{"can genie close the terminal tabs it opened", "genie-agent-mode"},
		{"can agent mode delete a file for me", "genie-agent-files"},
		{"can the agent open a file from the files panel in the editor", "genie-agent-files"},
		{"how do I let the agent rename and move my files", "genie-agent-files"},
		{"can I get a hint without seeing the answer", "practice-coach"},
		{"how do I check the complexity of my practice solution", "practice-coach"},
		{"how many hints do I get", "practice-coach"},
		{"how do I get interview practice questions", "practice"},
		{"how do I report a bug", "blog-and-contact"},
		{"how do I run openrepl with docker on my own server", "run-it-yourself"},
	}
	for _, c := range cases {
		hits := ix.Search(c.q, chatContextLimit, chatContextMin, chatContextCoverage)
		if len(hits) == 0 || hits[0].P.ID != c.want {
			var got []string
			for _, h := range hits {
				got = append(got, h.P.ID)
			}
			t.Errorf("%q -> %v, want %s first", c.q, got, c.want)
		}
	}
}

// Ordinary questions about code must not pull platform text into the answer.
func TestTheChatThresholdLeavesCodingQuestionsAlone(t *testing.T) {
	ix := testKB(t, nil).Index()
	for _, q := range []string{
		"Why does my while loop never end?",
		"What does this TypeError mean?",
		"how do I reverse a linked list in python",
		"explain what a segmentation fault is",
		"why is my function returning None",
		"how do I read a file line by line in go",
		"what is the difference between a list and a tuple",
		"fix the bug in my code",
		"write a function that checks for a palindrome",
	} {
		if hits := ix.Search(q, chatContextLimit, chatContextMin, chatContextCoverage); len(hits) != 0 {
			t.Errorf("%q matched %s (%.1f)", q, hits[0].P.ID, hits[0].Score)
		}
	}
}

func TestTheBlogThresholdAlwaysFindsTheBestNotes(t *testing.T) {
	ix := testKB(t, nil).Index()
	hits := ix.Search("how to use OpenREPL Genie", blogContextLimit, blogContextMin, blogContextCoverage)
	if len(hits) == 0 || hits[0].P.ID != "genie" {
		t.Fatalf("hits = %v", hits)
	}
	if len(hits) > blogContextLimit {
		t.Errorf("%d hits", len(hits))
	}
	if len(ix.Search("", 3, 0, 0)) != 0 || len(ix.Search("the of and", 3, 0, 0)) != 0 {
		t.Error("a question with only filler words matched")
	}
}

func TestSearchOnlyKeepsHitsCloseToTheBest(t *testing.T) {
	ix := newIndex([]*passage{
		{ID: "a", Kind: kindNote, Title: "Sharing", Keywords: "share", Text: "share share share link"},
		{ID: "b", Kind: kindNote, Title: "Other", Text: "a link to somewhere else, with many many other words that make it long and unrelated to anything else"},
	})
	hits := ix.Search("share link", 5, 0, 0)
	if len(hits) != 1 || hits[0].P.ID != "a" {
		t.Errorf("hits = %v", hits)
	}
}

func TestPostPassagesAreIndexedAndNotesWinWhenClose(t *testing.T) {
	long := strings.Repeat("<p>"+strings.Repeat("filler word ", 70)+"</p>", 3) + "<p>Zebrafish debugging with the secret flamingo method.</p>"
	posts := map[string]BlogPost{
		"flamingo & more": {Name: "flamingo & more", Title: "The flamingo method", Desc: "A debugging story", Content: long, Lastupdated: time.Now()},
	}
	kb := testKB(t, posts)
	ix := kb.Index()
	hits := ix.Search("tell me about the flamingo debugging method", blogContextLimit, blogContextMin, blogContextCoverage)
	if len(hits) == 0 || hits[0].P.Kind != kindPost || hits[0].P.Title != "The flamingo method" {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].P.Link != "/blog?name=flamingo+%26+more" {
		t.Errorf("link = %q", hits[0].P.Link)
	}
	if strings.Contains(hits[0].P.Text, "<p>") {
		t.Errorf("markup left in the passage: %q", hits[0].P.Text)
	}
	// a long post is several passages, each of about the same size
	count := 0
	for _, p := range ix.passages {
		if p.Kind == kindPost {
			count++
			if len(strings.Fields(p.Text)) > postPassageWords+80 {
				t.Errorf("passage of %d words", len(strings.Fields(p.Text)))
			}
		}
	}
	if count < 2 {
		t.Errorf("%d passages for a long post", count)
	}
	// the same words in a note and in a post: the note first
	posts["sharing again"] = BlogPost{Name: "sharing again", Title: "Sharing again", Desc: "x", Content: "<p>Press Share and send the link. Anyone who opens it sees your terminal and editor live and can type along in a chat.</p>"}
	kb.invalidate()
	hits = kb.Index().Search("share a live session link so a friend can type along", 3, blogContextMin, blogContextCoverage)
	if len(hits) < 2 || hits[0].P.Kind != kindNote {
		t.Fatalf("a note should come before a post: %+v", hits)
	}
}

func TestBlogChangesReachTheIndex(t *testing.T) {
	posts := map[string]BlogPost{}
	calls := 0
	kb := newKnowledgeBase(realNotes(t), func() map[string]BlogPost { calls++; return posts })
	clock := time.Now()
	kb.now = func() time.Time { return clock }

	ix := kb.Index()
	kb.Index()
	if calls != 1 {
		t.Fatalf("the posts were read %d times for two searches", calls)
	}
	posts["new"] = BlogPost{Name: "new", Title: "Quokka tutorial", Desc: "d", Content: "<p>All about quokkas.</p>"}
	if hits := kb.Index().Search("quokka", 3, 0, 0); len(hits) != 0 {
		t.Error("a post appeared before anything said it changed")
	}
	knowledgeInvalidateFor(kb)
	if hits := kb.Index().Search("quokka", 3, 0, 0); len(hits) == 0 {
		t.Error("a saved post was not found")
	}
	delete(posts, "new")
	kb.invalidate()
	if hits := kb.Index().Search("quokka", 3, 0, 0); len(hits) != 0 {
		t.Error("a deleted post was still found")
	}
	// posts that changed without a call (another instance) are read again later
	posts["other"] = BlogPost{Name: "other", Title: "Wombat guide", Desc: "d", Content: "<p>Wombats.</p>"}
	clock = clock.Add(postIndexTTL + time.Second)
	if hits := kb.Index().Search("wombat", 3, 0, 0); len(hits) == 0 {
		t.Error("the index was never refreshed")
	}
	if ix == kb.Index() {
		t.Error("the index object did not change")
	}
}

// helper: invalidate a specific base, as knowledgeInvalidate does for the shared one
func knowledgeInvalidateFor(kb *knowledgeBase) { kb.invalidate() }

func TestPlainTextOfAPost(t *testing.T) {
	got := plainText(`<h2>Title &amp; more</h2><p>One <b>bold</b> idea.</p><script>alert(1)</script><ul><li>a</li><li>b</li></ul>`)
	for _, want := range []string{"Title & more", "One bold idea.", "a\nb"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "alert") || strings.Contains(got, "<") {
		t.Errorf("markup left: %q", got)
	}
}

// ---- the request ---------------------------------------------------------------

func chatBody(t *testing.T, fields map[string]interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeContextHeader(t *testing.T, h string) []map[string]string {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(h)
	if err != nil {
		t.Fatalf("header %q: %v", h, err)
	}
	var list []map[string]string
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestARequestThatDoesNotAskIsLeftAlone(t *testing.T) {
	kb := testKB(t, nil)
	msgs := []map[string]string{{"role": "user", "content": "How can I share my session with a friend?"}}
	for _, ctx := range []interface{}{nil, "", "practice", "other", 5} {
		fields := map[string]interface{}{"messages": msgs}
		if ctx != nil {
			fields["context"] = ctx
		}
		in := chatBody(t, fields)
		out, res := addKnowledge(in, kb)
		if string(out) != string(in) || res.Asked || res.header() != "" {
			t.Errorf("context %v: asked=%v header=%q", ctx, res.Asked, res.header())
		}
	}
	if out, res := addKnowledge([]byte("not json"), kb); string(out) != "not json" || res.Asked {
		t.Error("a body that is not JSON was changed")
	}
}

func TestAChatRequestGetsTheMatchingNotesAsASystemMessage(t *testing.T) {
	kb := testKB(t, nil)
	msgs := []map[string]string{
		{"role": "system", "content": "you are Genie"},
		{"role": "user", "content": "How can I share my session with a friend so we can type together?"},
	}
	out, res := addKnowledge(chatBody(t, map[string]interface{}{"context": "chat", "messages": msgs, "model": "gpt-4o-mini"}), kb)
	if !res.Asked || len(res.Hits) == 0 || res.Hits[0].P.ID != "sharing-a-session" {
		t.Fatalf("hits = %+v", res.Hits)
	}
	var sent struct {
		Messages []map[string]string `json:"messages"`
		Model    string              `json:"model"`
	}
	if err := json.Unmarshal(out, &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) != 3 || sent.Messages[0]["role"] != "system" || !strings.Contains(sent.Messages[0]["content"], "[note] Sharing a live session") {
		t.Fatalf("messages = %+v", sent.Messages)
	}
	if sent.Messages[1]["content"] != "you are Genie" || sent.Messages[2]["content"] != msgs[1]["content"] || sent.Model != "gpt-4o-mini" {
		t.Errorf("the rest of the request changed: %+v", sent)
	}
	if !strings.Contains(sent.Messages[0]["content"], "do not invent") && !strings.Contains(sent.Messages[0]["content"], "Do not invent") {
		t.Error("the grounding rule is missing")
	}
	list := decodeContextHeader(t, res.header())
	if len(list) == 0 || list[0]["id"] != "sharing-a-session" || list[0]["title"] != "Sharing a live session" || list[0]["kind"] != "note" {
		t.Errorf("header = %v", list)
	}
}

func TestACodingQuestionFromTheChatGetsNothingAndSaysSo(t *testing.T) {
	kb := testKB(t, nil)
	in := chatBody(t, map[string]interface{}{"context": "chat", "messages": []map[string]string{{"role": "user", "content": "Why does my while loop never end?"}}})
	out, res := addKnowledge(in, kb)
	if string(out) != string(in) || !res.Asked || len(res.Hits) != 0 || res.header() != "none" {
		t.Errorf("asked=%v hits=%d header=%q", res.Asked, len(res.Hits), res.header())
	}
}

func TestTheBlogEditorUsesItsHintAndALowerThreshold(t *testing.T) {
	kb := testKB(t, nil)
	msgs := []map[string]string{{"role": "user", "content": "Write a short blog post section about this topic. Answer with the text only.\n\nusing the assistant"}}
	_, without := addKnowledge(chatBody(t, map[string]interface{}{"context": "blog", "messages": msgs}), kb)
	_, with := addKnowledge(chatBody(t, map[string]interface{}{"context": "blog", "messages": msgs, "context_hint": "How to use OpenREPL Genie"}), kb)
	if len(with.Hits) == 0 || with.Hits[0].P.ID != "genie" {
		t.Fatalf("with the hint: %+v", with.Hits)
	}
	if len(with.Hits) > blogContextLimit || len(without.Hits) > blogContextLimit {
		t.Error("too many passages")
	}
	// the hint replaces the question: the instructions around the text are not searched
	_, instr := addKnowledge(chatBody(t, map[string]interface{}{"context": "blog", "messages": msgs, "context_hint": "   "}), kb)
	if len(instr.Hits) != len(without.Hits) {
		t.Error("a blank hint changed the search")
	}
}

func TestContextFieldsNeverReachTheModel(t *testing.T) {
	isolateSettings(t)
	body := chatBody(t, map[string]interface{}{
		"context": "chat", "context_hint": "x", "model": "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": "how do I share a session"}},
	})
	out, _ := addKnowledge(body, testKB(t, nil))
	sanitized, _, err := sanitizeChatBody(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sanitized), "context") && strings.Contains(string(sanitized), `"context"`) || strings.Contains(string(sanitized), "context_hint") {
		t.Errorf("sent on: %s", sanitized)
	}
}

func TestThroughTheProxyTheModelGetsTheNotesAndTheAnswerListsThem(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	setKnowledge(testKB(t, nil))
	t.Cleanup(func() { setKnowledge(nil) })
	url, _, lastBody := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })

	w := proxyCall(t, `{"model":"gpt-4o-mini","context":"chat","messages":[{"role":"user","content":"how do I fork a REPL to run a server and a client"}]}`)
	if w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(*lastBody, "Forking a REPL") || strings.Contains(*lastBody, `"context"`) {
		t.Errorf("body sent on: %s", *lastBody)
	}
	list := decodeContextHeader(t, w.Header().Get(contextHeader))
	if len(list) == 0 || list[0]["id"] != "fork-a-repl" {
		t.Errorf("header = %v", w.Header().Get(contextHeader))
	}

	w = proxyCall(t, `{"model":"gpt-4o-mini","context":"chat","messages":[{"role":"user","content":"why does my loop never end"}]}`)
	if w.Header().Get(contextHeader) != "none" || strings.Contains(*lastBody, "Facts about OpenREPL") {
		t.Errorf("a coding question: header %q, body %s", w.Header().Get(contextHeader), *lastBody)
	}

	w = proxyCall(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"how do I fork a REPL to run a server and a client"}]}`)
	if w.Header().Get(contextHeader) != "" || strings.Contains(*lastBody, "Facts about OpenREPL") {
		t.Errorf("a request that did not ask: header %q", w.Header().Get(contextHeader))
	}
}

// ---- the endpoint --------------------------------------------------------------

func knowledgeGet(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handleKnowledgeAt("/")(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestKnowledgeEndpoint(t *testing.T) {
	posts := map[string]BlogPost{"hello": {Name: "hello", Title: "Hello post", Desc: "d", Content: "<p>Body text.</p>"}}
	setKnowledge(testKB(t, posts))
	t.Cleanup(func() { setKnowledge(nil) })

	w := knowledgeGet(t, "GET", "/knowledge/sharing-a-session")
	var got map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["title"] != "Sharing a live session" || got["link"] != "/about.html" ||
		!strings.Contains(got["text"], "Share") || got["kind"] != "note" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("note: %d %s", w.Code, w.Body.String())
	}
	var postID string
	for id := range posts {
		_ = id
	}
	for _, p := range theKnowledge().Index().passages {
		if p.Kind == kindPost {
			postID = p.ID
		}
	}
	w = knowledgeGet(t, "GET", "/knowledge/"+postID)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Hello post") || !strings.Contains(w.Body.String(), `/blog?name=hello`) {
		t.Fatalf("post passage %s: %d %s", postID, w.Code, w.Body.String())
	}
	if w := knowledgeGet(t, "HEAD", "/knowledge/sharing-a-session"); w.Code != 200 {
		t.Errorf("HEAD -> %d", w.Code)
	}
	if w := knowledgeGet(t, "POST", "/knowledge/sharing-a-session"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST -> %d", w.Code)
	}
}

func TestKnowledgeEndpointNeverReadsAnythingButTheIndex(t *testing.T) {
	setKnowledge(testKB(t, nil))
	t.Cleanup(func() { setKnowledge(nil) })
	for _, path := range []string{
		"/knowledge/", "/knowledge/nope", "/knowledge/..", "/knowledge/../etc/passwd", "/knowledge/%2e%2e/secrets",
		"/knowledge/sharing-a-session/..", "/knowledge/sharing-a-session/", "/knowledge/a/b", "/knowledge/sharing-a-session.md",
		"/knowledge/Sharing-A-Session", "/knowledge/sharing_a_session", "/knowledge/%2e%2e%2fsettings", "/knowledge/sharing-a-session%00",
		"/knowledge/" + strings.Repeat("a", 500), "/knowledge/post-0000000000-1",
	} {
		if w := knowledgeGet(t, "GET", path); w.Code != http.StatusNotFound {
			t.Errorf("%s -> %d %q", path, w.Code, w.Body.String())
		}
	}
}

func TestAnUnopenedBlogStoreGivesNoPostsInsteadOfPanicking(t *testing.T) {
	old := blog_db_handle
	blog_db_handle = nil
	t.Cleanup(func() { blog_db_handle = old })
	if got := blogPostsForKnowledge(); len(got) != 0 {
		t.Errorf("posts = %v", got)
	}
	kb := newKnowledgeBase(realNotes(t), blogPostsForKnowledge)
	if len(kb.Index().passages) == 0 {
		t.Error("the notes are missing without a blog store")
	}
}

// The chat panel puts a tag in front of what a user writes ("[user-k3J9x] "), so
// that people in a shared session can be told apart. The tag is not part of
// the question: its random id is a word no note has, and used to push the
// coverage of every question under the threshold.
func TestTheSpeakerTagIsNotPartOfTheQuestion(t *testing.T) {
	kb := testKB(t, nil)
	for _, q := range []string{
		"how do i use genie",
		"how to fork",
		"i mean how to fork the openrepl terminal",
		"how can i share my terminal",
		"give me the cling documentation",
		"how do I upload a file",
	} {
		msgs := []map[string]string{
			{"role": "system", "content": "you are Genie"},
			{"role": "user", "content": "[user-Xk3j9Qa2] " + q},
		}
		_, res := addKnowledge(chatBody(t, map[string]interface{}{"context": "chat", "messages": msgs}), kb)
		if len(res.Hits) == 0 {
			t.Errorf("%q: nothing matched with the tag in front", q)
		}
	}
	if got := lastUserText([]json.RawMessage{json.RawMessage(`{"role":"user","content":"[user-ab_c-1]   share it"}`)}); got != "share it" {
		t.Errorf("tag not removed: %q", got)
	}
	if got := lastUserText([]json.RawMessage{json.RawMessage(`{"role":"user","content":"a [user-x] b"}`)}); got != "a [user-x] b" {
		t.Errorf("a tag in the middle must stay: %q", got)
	}
}

func TestPaddingWordsDoNotHideAQuestionAboutTheSite(t *testing.T) {
	ix := testKB(t, nil).Index()
	for q, want := range map[string]string{
		"i mean how to fork the openrepl terminal": "fork-a-repl",
		"hey, how do I share my session?":          "sharing-a-session",
		"hey, what can genie do for me":            "genie",
	} {
		hits := ix.Search(q, chatContextLimit, chatContextMin, chatContextCoverage)
		if len(hits) == 0 || hits[0].P.ID != want {
			t.Errorf("%q -> %v, want %s", q, hits, want)
		}
	}
	for _, q := range []string{"thanks that worked", "ok", "how do I sort a list", "what is my mean value in python"} {
		if hits := ix.Search(q, chatContextLimit, chatContextMin, chatContextCoverage); len(hits) != 0 {
			t.Errorf("%q matched %s", q, hits[0].P.ID)
		}
	}
}

// The request the chat panel really sends: its fixed messages, the tagged
// question in the middle of the history, and the editor snapshot, a system
// message, last. Every question of the bug report goes through the proxy.
func TestTheRealWidgetRequestGetsItsNotesThroughTheProxy(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	setKnowledge(testKB(t, nil))
	t.Cleanup(func() { setKnowledge(nil) })
	url, _, lastBody := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })

	widget := func(question string, earlier ...map[string]string) string {
		msgs := []map[string]string{
			{"role": "system", "content": "welcome to openrepl.com!! you are Genie. An OpenRepl AI Assistant."},
			{"role": "system", "content": "Openrepl IDE real-time context of what the user is working on.\nLanguage: C and C++\n--- Editor code ---\nint main(){}\n--- Terminal output ---\ncling"},
		}
		msgs = append(msgs, earlier...)
		msgs = append(msgs,
			map[string]string{"role": "user", "content": "[user-a2592] " + question},
			map[string]string{"role": "system", "content": "Openrepl IDE real-time context of what the user is working on.\nLanguage: C and C++\n--- Editor code ---\nint main(){}\n--- Terminal output ---\ncling"})
		return string(chatBody(t, map[string]interface{}{
			"model": "gpt-6-luna", "reasoning_effort": "low", "max_completion_tokens": 2000,
			"messages": msgs, "stream": false, "context": "chat",
		}))
	}
	earlier := []map[string]string{
		{"role": "user", "content": "[user-a2592] hello"},
		{"role": "assistant", "content": "Hi, how can I help?"},
	}
	for question, want := range map[string]string{
		"how do i use genie": "genie",
		"how to fork":        "fork-a-repl",
		"i mean how to fork the openrepl terminal": "fork-a-repl",
		"share":                           "sharing-a-session",
		"how can i share my terminal":     "sharing-a-session",
		"give me the cling documentation": "languages",
	} {
		for _, history := range [][]map[string]string{nil, earlier} {
			w := proxyCall(t, widget(question, history...))
			if w.Code != 200 {
				t.Fatalf("%q: %d %s", question, w.Code, w.Body.String())
			}
			header := w.Header().Get(contextHeader)
			if header == "" || header == "none" {
				t.Errorf("%q (history %d): header %q, the notes were not found", question, len(history), header)
				continue
			}
			ids := decodeContextHeader(t, header)
			found := false
			for _, n := range ids {
				found = found || n["id"] == want
			}
			if !found {
				t.Errorf("%q: used %v, want %s among them", question, ids, want)
			}
			if !strings.Contains(*lastBody, "Facts about OpenREPL") || strings.Contains(*lastBody, `"context"`) {
				t.Errorf("%q: the model's request: %s", question, *lastBody)
			}
		}
	}
	// and a coding question still gets nothing
	w := proxyCall(t, widget("why does my while loop never end"))
	if w.Header().Get(contextHeader) != "none" || strings.Contains(*lastBody, "Facts about OpenREPL") {
		t.Errorf("a coding question: header %q", w.Header().Get(contextHeader))
	}
}
