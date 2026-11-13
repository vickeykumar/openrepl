package livepreview

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func TestATokenNamesItsWorkspaceUntilItEnds(t *testing.T) {
	tok, err := NewToken("secret", "/home/openrepl/guest-a", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if home, ok := Home("secret", tok, t0.Add(59*time.Minute)); !ok || home != "/home/openrepl/guest-a" {
		t.Errorf("a live token: %q %v", home, ok)
	}
	if _, ok := Home("secret", tok, t0.Add(time.Hour)); ok {
		t.Error("a token that has ended still works")
	}
}

func TestATokenCannotBeMadeOrChangedWithoutTheSecret(t *testing.T) {
	tok, _ := NewToken("secret", "/home/openrepl/guest-a", t0, time.Hour)
	if _, ok := Home("other-secret", tok, t0); ok {
		t.Error("a token was accepted under another secret")
	}
	// another workspace in the same token: the signature no longer fits
	other, _ := NewToken("secret", "/home/openrepl/guest-b", t0, time.Hour)
	forged := strings.SplitN(other, ".", 2)[0] + "." + strings.SplitN(tok, ".", 2)[1]
	if _, ok := Home("secret", forged, t0); ok {
		t.Error("a token with another workspace and the first one's signature was accepted")
	}
	for _, bad := range []string{"", ".", "abc", "abc.def", tok + "x", strings.SplitN(tok, ".", 2)[0]} {
		if _, ok := Home("secret", bad, t0); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestNoTokenWithoutASecretOrAWorkspace(t *testing.T) {
	if _, err := NewToken("", "/home/x", t0, time.Hour); err == nil {
		t.Error("a token was made with no secret")
	}
	if _, err := NewToken("s", "", t0, time.Hour); err == nil {
		t.Error("a token was made with no workspace")
	}
	if _, ok := Home("", "a.b", t0); ok {
		t.Error("a token was read with no secret")
	}
}

func TestATokenCanNameTheViewerOfASharedSession(t *testing.T) {
	now := time.Now()
	tok, err := NewViewerToken("s", "/home/openrepl/ws", "abc123", now, TokenLife)
	if err != nil {
		t.Fatal(err)
	}
	home, viewer, ok := Viewer("s", tok, now)
	if !ok || home != "/home/openrepl/ws" || viewer != "abc123" {
		t.Errorf("Viewer = %q %q %v", home, viewer, ok)
	}
	if h, ok := Home("s", tok, now); !ok || h != "/home/openrepl/ws" {
		t.Errorf("Home of a viewer's token = %q %v", h, ok)
	}
	plain, _ := NewToken("s", "/home/openrepl/ws", now, TokenLife)
	if _, v, _ := Viewer("s", plain, now); v != "" {
		t.Errorf("a token without a viewer has none: %q", v)
	}
}
