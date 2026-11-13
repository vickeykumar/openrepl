package server

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestOutputKeeperKeepsHeadAndTail(t *testing.T) {
	k := &outputKeeper{}
	k.Write([]byte("start\r\n"))
	chunk := []byte(strings.Repeat("x", 1000) + "\n")
	for i := 0; i < 200; i++ {
		k.Write(chunk)
	}
	k.Write([]byte("the end\n"))
	out, cut := k.text()
	if !cut || !strings.HasPrefix(out, "start\n") || !strings.HasSuffix(out, "the end\n") || !strings.Contains(out, "bytes of output cut") {
		t.Errorf("head/tail: cut=%v len=%d", cut, len(out))
	}
	if len(out) > shellHead+shellTail+100 {
		t.Errorf("too long: %d", len(out))
	}
	small := &outputKeeper{}
	small.Write([]byte("a\r\nb\xff"))
	s, cut := small.text()
	if cut || !utf8.ValidString(s) || strings.Contains(s, "\r") {
		t.Errorf("small output: %q cut=%v", s, cut)
	}
}

func TestShellTimeoutIsBounded(t *testing.T) {
	for in, want := range map[int]time.Duration{0: shellDefaultTimeout, -5: shellDefaultTimeout, 7: 7 * time.Second, 120: shellMaxTimeout, 9999: shellMaxTimeout} {
		if got := shellTimeout(in); got != want {
			t.Errorf("shellTimeout(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestShellRateLimit(t *testing.T) {
	now := time.Now()
	for i := 0; i < shellPerMinute; i++ {
		if !shellAllowed("rate-user", now) {
			t.Fatalf("refused command %d", i)
		}
	}
	if shellAllowed("rate-user", now) {
		t.Error("the command over the limit was allowed")
	}
	if !shellAllowed("other-user", now) {
		t.Error("another user was refused")
	}
	if !shellAllowed("rate-user", now.Add(61*time.Second)) {
		t.Error("still refused after a minute")
	}
}

func TestShellOneAtATimePerWorkspace(t *testing.T) {
	release, ok := lockShell("/tmp/ws-a")
	if !ok {
		t.Fatal("first lock refused")
	}
	if rel2, ok := lockShell("/tmp/ws-b"); !ok {
		t.Error("another workspace was blocked")
	} else {
		rel2()
	}
	done := make(chan bool)
	go func() {
		r, ok := lockShell("/tmp/ws-a")
		if ok {
			r()
		}
		done <- ok
	}()
	time.Sleep(200 * time.Millisecond)
	release()
	if !<-done {
		t.Error("the waiting command did not get the lock after release")
	}
}
