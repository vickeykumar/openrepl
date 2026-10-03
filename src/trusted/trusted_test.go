package trusted

import (
	"net/http"
	"testing"
)

func TestSetStripAndRead(t *testing.T) {
	h := http.Header{}
	h.Set("x-openrepl-uid", "spoofed")
	h.Set("X-OpenREPL-Priv", "admin")
	h.Set("X-Openrepl-Anything", "x")
	h.Set("Cookie", "a=b")

	Set(h, Identity{Guest: "g1", Privilege: "guest", Session: "g:g1"})
	got := FromHeader(h)
	want := Identity{Guest: "g1", Privilege: "guest", Session: "g:g1"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if h.Get("X-Openrepl-Anything") != "" {
		t.Fatal("unknown trusted-prefix header survived")
	}
	if h.Get("Cookie") != "a=b" {
		t.Fatal("unrelated header was removed")
	}

	Strip(h)
	if FromHeader(h) != (Identity{}) {
		t.Fatalf("Strip left %+v", FromHeader(h))
	}
}

func TestHomeDir(t *testing.T) {
	cases := []struct {
		id   Identity
		want string
		ok   bool
	}{
		{Identity{UID: "u1", HomeID: "vick1234"}, "/tmp/home/vick1234", true},
		{Identity{Guest: "abc123"}, "/tmp/home/guest-abc123", true},
		{Identity{UID: "u1"}, "", false},                                    // user without a home id
		{Identity{}, "", false},                                             // nobody
		{Identity{UID: "u1", HomeID: "../etc"}, "", false},                  // traversal
		{Identity{UID: "u1", HomeID: ".."}, "", false},                      // traversal
		{Identity{Guest: "a/b"}, "", false},                                 // separator
		{Identity{UID: "u1", HomeID: "x", Guest: "g"}, "/tmp/home/x", true}, // user wins
	}
	for _, c := range cases {
		got, err := HomeDir("/tmp/home/", c.id)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("HomeDir(%+v) = %q, %v; want %q, ok=%v", c.id, got, err, c.want, c.ok)
		}
	}
}
