package server

import (
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"containers"
	"encoder"
	"webtty"
)

// A fork is a terminal opened from another one (the jid of that terminal comes in its arguments): it
// joins the sandbox of the first terminal. On a host with sandboxes the first terminal is the root of
// its sandbox, so when it ends, the system stops everything inside, forks included. The fork then
// used to close with a generic message, or with the memory-limit text, and nothing said the first
// terminal was the reason (LLD 02, "Forks").

// errParentEnded means a fork ended because the terminal it was made from ended.
var errParentEnded = errors.New("parent ended")

// parentEndedWait is how long a fork that was stopped (not one that finished by itself) waits for the
// end of its first terminal to show: the system stops the fork at the same moment the first terminal
// ends, and this server learns that a few moments later.
var parentEndedWait = 3 * time.Second

// processAlive says whether a process exists (a variable so that tests can change it).
var processAlive = containers.ProcessAlive

// startRefusedByParent says whether a terminal could not start because it is a fork of a terminal that is
// not there (any more): a page opened by the Fork button after the first terminal ended, or after a
// restart of the server, whose jids are not the same after it.
func startRefusedByParent(err error, params url.Values) bool {
	return err != nil && forkParent(params) != -1 && strings.Contains(err.Error(), "invalid parent id")
}

// forkParent is the pid of the terminal a fork was made from, or -1 for a first terminal.
func forkParent(params url.Values) int {
	if jid := params.Get("jid"); jid != "" {
		return encoder.DecodeToPID(jid)
	}
	return -1
}

// parentEnded says whether the terminal with pid ppid is gone. A fork that finished by itself (a clean
// exit) is asked at once. One that was stopped waits up to parentEndedWait, since the first terminal's
// end may not have shown yet.
func parentEnded(ppid int, stopped bool) bool {
	if !processAlive(ppid) {
		return true
	}
	if !stopped {
		return false
	}
	deadline := time.Now().Add(parentEndedWait)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if !processAlive(ppid) {
			return true
		}
	}
	return false
}

// forkEnded works out why a fork ended and writes one line about it to the log, so that the next time
// a fork closes the log says which case it was: the exit status of its program, whether the first
// terminal was still there, and which end closed the connection. It says whether the first terminal's
// end is the reason.
func forkEnded(ppid int, runErr error, exitStatus string) bool {
	stopped := exitStatus != ""
	ended := false
	if runErr == webtty.ErrSlaveClosed {
		ended = parentEnded(ppid, stopped)
	}
	log.Printf("fork of terminal %d ended: closed by %v, program exit %q, first terminal gone: %v",
		ppid, runErr, exitStatus, !processAlive(ppid))
	return ended
}
