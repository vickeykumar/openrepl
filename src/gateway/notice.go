package gateway

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A session whose worker is away is not moved at once, because the worker may
// only be restarting: it is placed again after Config.RelocateAfter. Until
// then its terminal cannot start. This file tells the user so, and how long
// to wait, instead of answering with a bare error: the page shows a countdown
// and keeps Reconnect and Run disabled until it ends.

// awaySince is when the backend was last seen leaving. After a gateway
// restart every worker gets the whole grace period.
func (rt *Router) awaySince(id string) time.Time {
	rt.mu.RLock()
	since, ok := rt.offlineSince[id]
	rt.mu.RUnlock()
	if !ok || since.Before(rt.started) {
		since = rt.started
	}
	return since
}

// retryIn is how long until sessions on a backend that is away are placed
// again, or 0 if that is not going to happen: no workspace sync, the
// gateway's own backend, or the grace period is already over.
func (rt *Router) retryIn(id string) time.Duration {
	if rt.homeOf == nil || id == "" || id == LocalID {
		return 0
	}
	left := rt.relocateAfter - time.Since(rt.awaySince(id))
	if left < 0 {
		return 0
	}
	return left
}

// retryError is a refusal that says when to try again.
type retryError struct {
	error
	after time.Duration
}

func (e *retryError) Unwrap() error { return e.error }

// withRetry adds the time until the backend's sessions are placed again to
// err, when there is one.
func (rt *Router) withRetry(err error, backendID string) error {
	if after := rt.retryIn(backendID); after > 0 {
		return &retryError{error: err, after: after}
	}
	return err
}

// refuse answers a request that cannot be served now with 503. retryIn, when
// positive, is how long until the session is placed again: it goes into
// Retry-After and, for a terminal's WebSocket, into what the page is told
// (Config.TerminalNotice), which is what happens when a user presses
// Reconnect or Run.
func (rt *Router) refuse(w http.ResponseWriter, r *http.Request, msg string, retryIn time.Duration) {
	if retryIn > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryIn.Seconds()))))
		if rt.notice != nil && isWebSocketRequest(r) && rt.isTerminalPath(r) {
			if rt.notice(w, r, retryIn) {
				return
			}
		}
	}
	http.Error(w, msg, http.StatusServiceUnavailable)
}

// refuseTerminal refuses a terminal for a reason the user is told: a WebSocket
// is closed with it (Config.RefuseTerminal), anything else gets a 503.
func (rt *Router) refuseTerminal(w http.ResponseWriter, r *http.Request, reason string) {
	if rt.refuseTerm != nil && isWebSocketRequest(r) && rt.refuseTerm(w, r, reason) {
		return
	}
	http.Error(w, reason, http.StatusServiceUnavailable)
}

// refuseError is refuse for an error from placing a session.
func (rt *Router) refuseError(w http.ResponseWriter, r *http.Request, err error, code int) {
	var re *retryError
	if code == http.StatusServiceUnavailable && errors.As(err, &re) {
		rt.refuse(w, r, err.Error(), re.after)
		return
	}
	http.Error(w, err.Error(), code)
}

func (rt *Router) isTerminalPath(r *http.Request) bool {
	if rt.terminal == nil {
		return false
	}
	_, _, ok := rt.terminal(rt.rel(r.URL.Path))
	return ok
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
