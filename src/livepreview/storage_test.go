package livepreview

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheStoreKeepsEachAreaPerScopeAndASessionPerViewer(t *testing.T) {
	st := NewStore()
	if err := st.Set("local", "/h/a", "v1", "f:/", []byte(`{"count":"3"}`)); err != nil {
		t.Fatal(err)
	}
	st.Set("session", "/h/a", "v1", "f:/", []byte(`{"tab":"one"}`))
	st.Set("cookie", "/h/a", "v1", "f:/", []byte(`{"sid":{"v":"x","e":0}}`))
	snap := st.Snapshot("/h/a", "v1", "f:/")
	for _, want := range []string{`"local":{"count":"3"}`, `"session":{"tab":"one"}`, `"cookie":{"sid":{"v":"x","e":0}}`} {
		if !strings.Contains(snap, want) {
			t.Errorf("snapshot %s lacks %s", snap, want)
		}
	}
	// another viewer shares the local store and has a session of his own; another scope or workspace has nothing
	other := st.Snapshot("/h/a", "v2", "f:/")
	if !strings.Contains(other, `"count":"3"`) || strings.Contains(other, "one") {
		t.Errorf("a second viewer: %s", other)
	}
	for _, empty := range []string{st.Snapshot("/h/a", "v1", "f:/other/"), st.Snapshot("/h/b", "v1", "f:/"), st.Snapshot("/h/a", "v1", "p:5000")} {
		if strings.Contains(empty, "count") {
			t.Errorf("a store of another scope leaked: %s", empty)
		}
	}
}

func TestTheStoreRefusesWhatIsNotAnObjectOrIsTooBig(t *testing.T) {
	st := NewStore()
	for _, bad := range []string{`[1]`, `"x"`, `{`, ``, `null`} {
		if st.Set("local", "/h", "v", "f:/", []byte(bad)) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if st.Set("elsewhere", "/h", "v", "f:/", []byte(`{}`)) == nil {
		t.Error("an unknown area should be refused")
	}
	big := `{"k":"` + strings.Repeat("a", MaxArea) + `"}`
	if st.Set("local", "/h", "v", "f:/", []byte(big)) == nil {
		t.Error("more than 5 MB should be refused")
	}
}

func TestAPageGetsItsStorageInItsOwnScriptAndItCannotCloseTheScript(t *testing.T) {
	st := NewStore()
	st.Set("local", "/h", "v", "f:/site/", []byte(`{"x":"</script><b>"}`))
	si := &StorageInfo{Store: st, Home: "/h", Viewer: "v", URL: "/preview/tok/s"}
	out := string(injectAtTop([]byte("<html><head></head><body>hi</body></html>"), si.script(storageScope("site/index.html"))))
	if !strings.Contains(out, `"x":"\u003c/script>\u003cb>"`) {
		t.Errorf("the data has to be escaped inside the script: %s", out[:200])
	}
	if strings.Count(out, "</script>") != 1 {
		t.Errorf("the data closed the script: %d", strings.Count(out, "</script>"))
	}
	if !strings.Contains(out, `U="/preview/tok/s"`) || !strings.Contains(out, `SC="f:/site/"`) {
		t.Error("the script has to know where and what to write back")
	}
	if (*StorageInfo)(nil).script("f:/") != "" {
		t.Error("with no store there is no script")
	}
	if storageScope("a.html") != "f:/" || storageScope("site/x/a.html") != "f:/site/x/" || ProgramScope(5000) != "p:5000" {
		t.Errorf("scopes: %q %q %q", storageScope("a.html"), storageScope("site/x/a.html"), ProgramScope(5000))
	}
}

func TestAPageWritesItsStorageBackWithAPost(t *testing.T) {
	st := NewStore()
	post := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		HandleStorage(rec, httptest.NewRequest(method, "/preview/t/s", strings.NewReader(body)), st, "/h", "v")
		return rec
	}
	if rec := post("POST", `{"scope":"f:/","area":"local","data":{"a":"1"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(st.Snapshot("/h", "v", "f:/"), `"a":"1"`) {
		t.Error("the write did not arrive")
	}
	for _, c := range []struct{ method, body string }{
		{"GET", ""}, {"POST", `nonsense`}, {"POST", `{"scope":"","area":"local","data":{}}`},
		{"POST", `{"scope":"f:/","area":"local","data":[1]}`}, {"POST", `{"scope":"f:/","area":"x","data":{}}`},
	} {
		if rec := post(c.method, c.body); rec.Code < 400 {
			t.Errorf("%s %q should be refused: %d", c.method, c.body, rec.Code)
		}
	}
	if rec := post("OPTIONS", ""); rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("a preflight: %d %v", rec.Code, rec.Header())
	}
}
