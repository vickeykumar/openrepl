package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// every test of this package waits a millisecond, not seconds, between tries
func init() {
	chatRetryWait = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
}

func quickRetries(t *testing.T) {
	t.Helper()
	old := chatRetryWait
	chatRetryWait = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { chatRetryWait = old })
}

func newTry(t *testing.T, url, body string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), "POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAHostThatFailsTwiceAndThenAnswersIsRetriedAndTheAnswerGoesOn(t *testing.T) {
	quickRetries(t)
	var calls int32
	var bodies []string
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if atomic.AddInt32(&calls, 1) <= 2 {
			http.Error(w, `{"error":{"message":"The server had an error while processing your request. Sorry about that!"}}`, 500)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer h.Close()
	resp, n, err := roundTripRetrying(context.Background(), http.DefaultTransport, newTry(t, h.URL, `{"model":"m"}`), "m")
	if err != nil || resp.StatusCode != 200 || n != 2 || calls != 3 {
		t.Fatalf("status %v retries %d calls %d err %v", resp, n, calls, err)
	}
	for i, b := range bodies {
		if b != `{"model":"m"}` {
			t.Errorf("try %d sent %q: every try sends the whole body", i, b)
		}
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != `{"ok":true}` {
		t.Errorf("the answer of the last try: %q", got)
	}
}

func TestAHostThatKeepsFailingIsTriedFourTimesAndItsLastAnswerIsKept(t *testing.T) {
	quickRetries(t)
	var calls int32
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "still broken", 503)
	}))
	defer h.Close()
	resp, n, err := roundTripRetrying(context.Background(), http.DefaultTransport, newTry(t, h.URL, "x"), "m")
	if err != nil || resp.StatusCode != 503 || n != chatRetries || calls != chatRetries+1 {
		t.Fatalf("status %v retries %d calls %d err %v", resp, n, calls, err)
	}
	if b, _ := io.ReadAll(resp.Body); !strings.Contains(string(b), "still broken") {
		t.Errorf("the last answer is not read away: %q", b)
	}
}

func TestARefusalAndAnAnswerAreNotRetried(t *testing.T) {
	quickRetries(t)
	for _, code := range []int{200, 400, 401, 402, 404, 429} {
		var calls int32
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(code)
		}))
		resp, n, err := roundTripRetrying(context.Background(), http.DefaultTransport, newTry(t, h.URL, "x"), "m")
		h.Close()
		if err != nil || resp.StatusCode != code || n != 0 || calls != 1 {
			t.Errorf("%d: retries %d calls %d err %v", code, n, calls, err)
		}
	}
}

type failingTransport struct{ calls int32 }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if atomic.AddInt32(&f.calls, 1) < 3 {
		return nil, errors.New("connection reset by peer")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func TestAConnectionThatFailsBeforeAnyAnswerIsRetried(t *testing.T) {
	quickRetries(t)
	ft := &failingTransport{}
	resp, n, err := roundTripRetrying(context.Background(), ft, newTry(t, "http://example.invalid", "x"), "m")
	if err != nil || resp.StatusCode != 200 || n != 2 {
		t.Fatalf("status %v retries %d err %v", resp, n, err)
	}
}

func TestACallThatRanOutOfTimeOrWasCancelledIsNotRetried(t *testing.T) {
	quickRetries(t)
	ft := &failingTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, n, err := roundTripRetrying(ctx, ft, newTry(t, "http://example.invalid", "x"), "m")
	if err == nil || n != 0 || ft.calls != 1 {
		t.Errorf("a cancelled call is not tried again: retries %d calls %d err %v", n, ft.calls, err)
	}
	if retryable(0, errors.New("Post x: context deadline exceeded"), context.Background()) {
		t.Error("a timeout of the call is not the host's trouble")
	}
}

func TestTheHealthCardCountsTheRetries(t *testing.T) {
	old := genieHealth
	genieHealth = freshHealth(t)
	defer func() { genieHealth = old }()
	m := chatModel{ID: "r/m", Name: "R"}
	genieHealth.note(m, "ok", time.Second, time.Second, time.Now())
	healthRetried(m, 2)
	healthRetried(m, 0)
	rep := genieHealth.report(24, 20, 8000, time.Now())
	if len(rep.Rows) != 1 || rep.Rows[0].Retries != 2 {
		t.Errorf("retries: %+v", rep.Rows)
	}
}
