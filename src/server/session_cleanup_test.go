package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionVerdict(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(d).UnixNano() / 1e6 }
	day := 24 * time.Hour
	cases := []struct {
		name   string
		share  *shareRecord
		owner  *ownerRecord
		first  time.Time
		remove bool
		seen   bool
	}{
		{"a link whose end has passed", &shareRecord{EndsAt: ms(-time.Minute), State: "live"}, nil, time.Time{}, true, false},
		{"a link still running", &shareRecord{EndsAt: ms(time.Hour), State: "live"}, nil, time.Time{}, false, false},
		{"a link that was ended", &shareRecord{EndsAt: ms(time.Hour), State: "ended"}, nil, time.Time{}, true, false},
		{"no share record, the owner is here", nil, &ownerRecord{Online: true, At: ms(-time.Minute)}, time.Time{}, false, false},
		{"no share record, the owner's heartbeat is old", nil, &ownerRecord{Online: true, At: ms(-time.Hour)}, time.Time{}, false, true},
		{"no share record, nobody, seen the first time", nil, nil, time.Time{}, false, true},
		{"no share record, nobody, seen less than an interval ago", nil, nil, now.Add(-day / 2), false, true},
		{"no share record, nobody, seen a whole interval ago", nil, nil, now.Add(-day - time.Minute), true, true},
		{"no share record, the owner left", nil, &ownerRecord{Online: false, At: ms(-2 * day)}, now.Add(-2 * day), true, true},
	}
	for _, c := range cases {
		remove, seen := sessionVerdict(c.share, c.owner, c.first, now, day)
		if remove != c.remove || seen != c.seen {
			t.Errorf("%s: remove=%v seen=%v, want %v %v", c.name, remove, seen, c.remove, c.seen)
		}
	}
}

// a Realtime Database that keeps its tree in memory and answers what the cleanup asks: shallow lists, reads and deletes
type fakeRTDB struct {
	mu   sync.Mutex
	tree map[string]interface{}
}

func (f *fakeRTDB) node(path string) (interface{}, bool) {
	var cur interface{} = f.tree
	for _, p := range strings.Split(strings.Trim(path, "/"), "/") {
		if p == "" {
			continue
		}
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func (f *fakeRTDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")
	switch r.Method {
	case "GET":
		v, ok := f.node(path)
		if !ok {
			w.Write([]byte("null"))
			return
		}
		if r.URL.Query().Get("shallow") == "true" {
			out := map[string]bool{}
			if m, ok := v.(map[string]interface{}); ok {
				for k := range m {
					out[k] = true
				}
			}
			json.NewEncoder(w).Encode(out)
			return
		}
		json.NewEncoder(w).Encode(v)
	case "DELETE":
		parts := strings.Split(path, "/")
		parent, ok := f.node(strings.Join(parts[:len(parts)-1], "/"))
		if m, isMap := parent.(map[string]interface{}); ok && isMap {
			delete(m, parts[len(parts)-1])
		}
		w.Write([]byte("null"))
	}
}

func TestRunSessionCleanup(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(d).UnixNano() / 1e6 }
	session := func(share, owner map[string]interface{}) map[string]interface{} {
		v := map[string]interface{}{"content": "code"}
		if share != nil {
			v["share"] = share
		}
		if owner != nil {
			v["owner"] = owner
		}
		return v
	}
	fake := &fakeRTDB{tree: map[string]interface{}{
		"editor_values": map[string]interface{}{
			"over":    session(map[string]interface{}{"endsAt": ms(-time.Hour), "state": "live"}, nil),
			"running": session(map[string]interface{}{"endsAt": ms(3 * 24 * time.Hour), "state": "live"}, nil),
			"here":    session(nil, map[string]interface{}{"online": true, "at": ms(-time.Minute)}),
			"nobody":  session(nil, nil),
		},
		"openrepl":     map[string]interface{}{"over": map[string]interface{}{"t": 1}, "running": map[string]interface{}{"t": 1}, "orphan": map[string]interface{}{"t": 1}},
		"chat-list":    map[string]interface{}{"over": map[string]interface{}{"m": 1}, "running": map[string]interface{}{"m": 1}},
		"file-browser": map[string]interface{}{"over": map[string]interface{}{"x": 1}, "nobody": map[string]interface{}{"x": 1}},
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := &rtdb{base: u.Scheme + "://" + u.Host, query: url.Values{"ns": {"demo"}}, token: func() (string, error) { return "test-token", nil }, http: http.DefaultClient}
	seen := map[string]time.Time{}
	day := 24 * time.Hour

	// the first run: the over one goes everywhere; the share-less ones without a live owner are noted, not removed
	res := runSessionCleanup(c, now, seen, day)
	if res.Removed != 1 || res.Errors != 0 {
		t.Fatalf("first run: %+v", res)
	}
	for _, p := range sessionPaths {
		if _, ok := fake.node(p + "/over"); ok {
			t.Errorf("%s/over is still there", p)
		}
	}
	for _, keep := range []string{"editor_values/running", "openrepl/running", "chat-list/running", "editor_values/here", "editor_values/nobody", "openrepl/orphan", "file-browser/nobody"} {
		if _, ok := fake.node(keep); !ok {
			t.Errorf("%s was removed by the first run", keep)
		}
	}
	if len(seen) != 2 || seen["nobody"].IsZero() || seen["orphan"].IsZero() {
		t.Errorf("seen after the first run: %v", seen)
	}

	// a whole interval later: the ones nobody has, and the orphan records, go; a running link and an owner that is there stay
	res = runSessionCleanup(c, now.Add(day+time.Minute), seen, day)
	if res.Removed != 2 {
		t.Fatalf("second run: %+v", res)
	}
	for _, gone := range []string{"editor_values/nobody", "file-browser/nobody", "openrepl/orphan"} {
		if _, ok := fake.node(gone); ok {
			t.Errorf("%s is still there after the second run", gone)
		}
	}
	for _, keep := range []string{"editor_values/running", "openrepl/running", "chat-list/running", "editor_values/here"} {
		if _, ok := fake.node(keep); !ok {
			t.Errorf("%s was removed", keep)
		}
	}
}

func TestSessionCleanupSetting(t *testing.T) {
	var s SiteSettings
	if s.sessionCleanupInterval() != 24*time.Hour {
		t.Errorf("default %v", s.sessionCleanupInterval())
	}
	s.SessionCleanupHours = 6
	if s.sessionCleanupInterval() != 6*time.Hour {
		t.Errorf("set %v", s.sessionCleanupInterval())
	}
	for _, bad := range []int{-1, 721} {
		if _, err := (SiteSettings{SessionCleanupHours: bad}).normalize(); err == nil {
			t.Errorf("%d hours was accepted", bad)
		}
	}
	if _, err := (SiteSettings{SessionCleanupHours: 720}).normalize(); err != nil {
		t.Errorf("720 hours was refused: %v", err)
	}
}
