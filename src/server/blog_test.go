package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"persist"
	"strings"
	"testing"
	"time"
)

func withBlogDB(t *testing.T) {
	t.Helper()
	db, err := persist.Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := blog_db_handle
	blog_db_handle = db
	t.Cleanup(func() { blog_db_handle = old; db.Close() })
}

func putPost(t *testing.T, name, title, desc, content string, at time.Time) {
	t.Helper()
	data, _ := json.Marshal(BlogPost{Name: name, Title: title, Desc: desc, Content: content, Lastupdated: at})
	if err := blog_db_handle.Store([]byte(name), data); err != nil {
		t.Fatal(err)
	}
}

func TestReadingTimeCountsWordsNotTags(t *testing.T) {
	words := func(n int) string { return "<p>" + strings.Repeat("word ", n) + "</p>" }
	for in, want := range map[string]int{
		"":          1,
		words(10):   1,
		words(200):  1,
		words(201):  2,
		words(1000): 5,
		strings.Repeat(`<img src="x.png" alt="`+strings.Repeat("alt ", 300)+`">`, 1) + words(5): 1, // attributes are not words
	} {
		if got := blogMinutes(in); got != want {
			t.Errorf("%.30q... = %d minutes, want %d", in, got, want)
		}
	}
}

func TestPostsAreListedNewestFirst(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC) }
	got := sortedBlogs(map[string]BlogPost{
		"old":     {Title: "Old", Lastupdated: d(1)},
		"new":     {Title: "New", Lastupdated: d(9)},
		"b-same":  {Title: "Same B", Lastupdated: d(5)},
		"a-same":  {Title: "Same A", Lastupdated: d(5)},
		"no-date": {Title: ""},
	})
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "new,a-same,b-same,old,no-date" {
		t.Fatalf("order: %v", names)
	}
	if got[4].Title != "no-date" {
		t.Errorf("a post without a title is listed by its name: %q", got[4].Title)
	}
}

func blogAs(t *testing.T, h http.Handler, c *http.Cookie, form url.Values, header bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/blog", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	if header {
		r.Header.Set(adminHeader, adminHeaderValue)
	}
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleBlog(w, r)
	return w
}

func blogGet(query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handleBlog(w, httptest.NewRequest("GET", "/blog"+query, nil))
	return w
}

func TestOnlyAnAdminFromTheEditorChangesPosts(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	boss := sessionCookie(t, "uid-boss", "boss@example.com")
	ann := sessionCookie(t, "uid-ann", "ann@example.com")
	form := url.Values{"name": {"first"}, "title": {"First"}, "desc": {"A first post."}, "content": {"<p>Hello</p>"}}

	if w := blogAs(t, nil, nil, form, true); w.Code != http.StatusUnauthorized {
		t.Errorf("nobody: %d", w.Code)
	}
	if w := blogAs(t, nil, ann, form, true); w.Code != http.StatusUnauthorized {
		t.Errorf("a visitor: %d", w.Code)
	}
	if w := blogAs(t, nil, boss, form, false); w.Code != http.StatusForbidden {
		t.Errorf("an admin without the editor's header: %d", w.Code)
	}
	w := blogAs(t, nil, boss, form, true)
	var out struct {
		OK      bool
		Message string
		Name    string
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || !out.OK || out.Name != "first" || !strings.Contains(out.Message, "Saved") {
		t.Fatalf("a save: %d %s", w.Code, w.Body.String())
	}
	if len(FetchBlogDataMap()) != 1 {
		t.Fatal("not stored")
	}
}

func TestAPostIsChecked(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	boss := sessionCookie(t, "uid-boss", "boss@example.com")
	ok := func() url.Values {
		return url.Values{"name": {"p"}, "title": {"T"}, "desc": {"D"}, "content": {"<p>x</p>"}}
	}
	for name, mod := range map[string]func(url.Values){
		"no name":        func(v url.Values) { v.Set("name", "  ") },
		"no title":       func(v url.Values) { v.Set("title", "") },
		"no description": func(v url.Values) { v.Set("desc", "") },
		"no content":     func(v url.Values) { v.Set("content", " ") },
		"name too long":  func(v url.Values) { v.Set("name", strings.Repeat("n", blogNameMax+1)) },
		"title too long": func(v url.Values) { v.Set("title", strings.Repeat("t", blogTitleMax+1)) },
		"desc too long":  func(v url.Values) { v.Set("desc", strings.Repeat("d", blogDescMax+1)) },
	} {
		v := ok()
		mod(v)
		if w := blogAs(t, nil, boss, v, true); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	big := ok()
	big.Set("content", strings.Repeat("x", blogMaxBody+10))
	if w := blogAs(t, nil, boss, big, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a post over 2 MB: %d", w.Code)
	}
	if len(FetchBlogDataMap()) != 0 {
		t.Fatal("a refused post was stored")
	}
	// a line break in the name does not get through
	v := ok()
	v.Set("name", "multi\nline")
	if w := blogAs(t, nil, boss, v, true); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if _, found := FetchBlogDataMap()["multiline"]; !found {
		t.Errorf("stored under %v", FetchBlogDataMap())
	}
}

func TestADeleteNeedsTheSameAndSaysWhenThereIsNothing(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	boss := sessionCookie(t, "uid-boss", "boss@example.com")
	putPost(t, "gone", "Gone", "d", "<p>x</p>", time.Now())
	del := func(name string, header bool, c *http.Cookie) int {
		return blogAs(t, nil, c, url.Values{"q": {"delete"}, "name": {name}}, header).Code
	}
	if del("gone", true, nil) != 401 || del("gone", false, boss) != 403 {
		t.Fatal("a delete was allowed without an admin or the header")
	}
	if del("nothing", true, boss) != 404 {
		t.Error("deleting a post that is not there")
	}
	if del("gone", true, boss) != 200 || len(FetchBlogDataMap()) != 0 {
		t.Error("the delete did not work")
	}
}

func TestTheListAndAPostAreShownTheWayTheDesignSays(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	putPost(t, "older", "The Older One", "About old things.", "<p>"+strings.Repeat("word ", 450)+"</p>", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	putPost(t, "newer & better", "The <b>Newer</b> One", "About new things.", "<p>Hi</p><script>no()</script>", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))

	list := blogGet("").Body.String()
	iNew, iOld := strings.Index(list, "Newer"), strings.Index(list, "The Older One")
	if iNew < 0 || iOld < 0 || iNew > iOld {
		t.Fatalf("the newer post is not first:\n%s", list)
	}
	for _, want := range []string{"October 5, 2026", "1 min read", "3 min read", "Read more", `/blog?name=newer%20%26%20better`} {
		if !strings.Contains(list, want) {
			t.Errorf("the list lacks %q", want)
		}
	}
	if strings.Contains(list, "<b>Newer</b>") {
		t.Error("a title with markup is not escaped")
	}

	post := blogGet("?name=older").Body.String()
	for _, want := range []string{"The Older One", "September 1, 2026", "3 min read", "All posts", `class="blog-body"`} {
		if !strings.Contains(post, want) {
			t.Errorf("the post lacks %q", want)
		}
	}
	if w := blogGet("?name=missing"); w.Code != http.StatusNotFound {
		t.Errorf("a post that is not there: %d", w.Code)
	}
	if w := blogGet("?name=%3Cscript%3Ex"); strings.Contains(w.Body.String(), "<script>x") {
		t.Error("the name of a missing post is not escaped")
	}
}

func TestTheEditorsListHasTitlesAndDates(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	putPost(t, "a", "Alpha", "d", "<p>x</p>", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	putPost(t, "b", "Beta", "d", "<p>x</p>", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	var idx []blogSummary
	if err := json.Unmarshal(blogGet("?q=index").Body.Bytes(), &idx); err != nil || len(idx) != 2 || idx[0].Name != "b" || idx[0].Title != "Beta" || idx[0].Minutes != 1 {
		t.Fatalf("index: %v %+v", err, idx)
	}
	var names []string
	json.Unmarshal(blogGet("?q=list").Body.Bytes(), &names)
	if strings.Join(names, ",") != "b,a" {
		t.Fatalf("list: %v", names)
	}
	var one BlogPost
	json.Unmarshal(blogGet("?q=json&name=a").Body.Bytes(), &one)
	if one.Title != "Alpha" || one.Content != "<p>x</p>" {
		t.Fatalf("json: %+v", one)
	}
}

func TestOtherMethodsOnTheBlogAreRefused(t *testing.T) {
	adminTestServer(t)
	withBlogDB(t)
	w := httptest.NewRecorder()
	handleBlog(w, httptest.NewRequest("PUT", "/blog", nil))
	if w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Header().Get("Allow"), "POST") {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Allow"))
	}
}
