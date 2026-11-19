package server

// A call to a model host that fails on the host's side is made again, up to chatRetries times, before the visitor is told: a 5xx answer (an
// internal error of the host, which is often gone a second later) or a connection that fails before any answer. A 4xx answer (a refusal, no
// credit, a limit) and a call that ran out of time are not tried again. Nothing of the first try has reached the visitor: the answer of the
// last try is what goes on, so the visitor is charged once, for the answer that came.

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const chatRetries = 3

// how long to wait before each new try (a var so that the tests are quick)
var chatRetryWait = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// retryable says whether this outcome of a try is the host's trouble and may pass: a 5xx, or a transport error that is not the call's own timeout.
func retryable(status int, err error, ctx context.Context) bool {
	if err != nil {
		return ctx.Err() == nil && !strings.Contains(err.Error(), "context deadline exceeded")
	}
	return status >= 500
}

// roundTripRetrying sends `first`, and the same request again (at most chatRetries times) when retryable says so. It returns the last response
// (its body unread when it is a 5xx that stays), the number of new tries made, and the error of the last try.
func roundTripRetrying(ctx context.Context, rt http.RoundTripper, first *http.Request, label string) (*http.Response, int, error) {
	req := first
	for attempt := 0; ; attempt++ {
		resp, err := rt.RoundTrip(req)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		if !retryable(status, err, ctx) || attempt >= chatRetries {
			return resp, attempt, err
		}
		// this try is given up: what the host said is read a little for the log, and the connection is freed
		var said []byte
		if resp != nil {
			said, _ = io.ReadAll(io.LimitReader(resp.Body, 300))
			resp.Body.Close()
		}
		log.Println("model host", label, "failed (status", status, "error", err, "):", strings.TrimSpace(string(said)), "- trying again", attempt+1, "of", chatRetries)
		wait := chatRetryWait[len(chatRetryWait)-1]
		if attempt < len(chatRetryWait) {
			wait = chatRetryWait[attempt]
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, attempt + 1, ctx.Err()
		}
		next := first.Clone(ctx)
		if first.GetBody != nil {
			body, berr := first.GetBody()
			if berr != nil {
				return nil, attempt + 1, berr
			}
			next.Body = body
		}
		req = next
	}
}
