package server

import (
	"bytes"
	"errors"
	"log"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"encoder"
	"webtty"
)

// A fork that ends because the terminal it was made from ended is told so (forks.go).

func TestForkParentIsReadFromTheJid(t *testing.T) {
	if got := forkParent(url.Values{}); got != -1 {
		t.Errorf("a first terminal has no parent: %d", got)
	}
	if got := forkParent(url.Values{"jid": {encoder.EncodePID(4321)}}); got != 4321 {
		t.Errorf("parent = %d, want 4321", got)
	}
	if got := forkParent(url.Values{"jid": {"not a jid"}}); got != -1 {
		t.Errorf("a jid that does not decode: %d", got)
	}
}

func withProcesses(t *testing.T, alive func(int) bool) {
	t.Helper()
	oldAlive, oldWait := processAlive, parentEndedWait
	processAlive, parentEndedWait = alive, 300*time.Millisecond
	t.Cleanup(func() { processAlive, parentEndedWait = oldAlive, oldWait })
}

func TestAForkThatFinishedByItselfAsksAboutItsParentAtOnce(t *testing.T) {
	withProcesses(t, func(int) bool { return true })
	start := time.Now()
	if parentEnded(100, false) {
		t.Error("the first terminal is still there")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("a clean exit waited %v for nothing", time.Since(start))
	}
	withProcesses(t, func(int) bool { return false })
	if !parentEnded(100, false) {
		t.Error("the first terminal is gone")
	}
}

func TestAForkThatWasStoppedWaitsForItsParentsEndToShow(t *testing.T) {
	var calls int32
	// the first terminal's end shows only after a few looks, as it does when the fork is stopped by the same event
	withProcesses(t, func(int) bool { return atomic.AddInt32(&calls, 1) < 4 })
	if !parentEnded(100, true) {
		t.Error("the end of the first terminal was not waited for")
	}
	// a parent that stays alive is not blamed, and the wait ends
	withProcesses(t, func(int) bool { return true })
	start := time.Now()
	if parentEnded(100, true) {
		t.Error("a first terminal that is still there was blamed")
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v, want about %v", d, parentEndedWait)
	}
}

func TestEveryForkThatEndsLeavesOneLineInTheLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	withProcesses(t, func(int) bool { return false })

	if !forkEnded(777, webtty.ErrSlaveClosed, "signal: killed") {
		t.Error("a fork stopped while its first terminal is gone must say so")
	}
	line := buf.String()
	for _, want := range []string{"fork of terminal 777 ended", "slave closed", `"signal: killed"`, "first terminal gone: true"} {
		if !strings.Contains(line, want) {
			t.Errorf("the log line %q lacks %q", line, want)
		}
	}
	// a fork that was closed from the page is logged but not blamed on anyone
	buf.Reset()
	if forkEnded(777, webtty.ErrMasterClosed, "") {
		t.Error("a fork closed by its own page was blamed on its first terminal")
	}
	if !strings.Contains(buf.String(), "fork of terminal 777 ended") {
		t.Errorf("not logged: %q", buf.String())
	}
	// another error is not the first terminal's doing either
	if forkEnded(777, errors.New("failed to create backend"), "") {
		t.Error("an error was blamed on the first terminal")
	}
}

func TestAForkRefusedBecauseItsFirstTerminalIsGoneIsTold(t *testing.T) {
	refused := errors.New(`failed to start command ` + "`gomacro`" + ` due to invalid parent id: 374`)
	fork := url.Values{"jid": {encoder.EncodePID(374)}}
	if !startRefusedByParent(refused, fork) {
		t.Error("a fork whose first terminal is gone must be told so")
	}
	if startRefusedByParent(refused, url.Values{}) {
		t.Error("a first terminal has no parent to blame")
	}
	if startRefusedByParent(errors.New("executable file not found"), fork) {
		t.Error("another reason for not starting is not the parent's doing")
	}
	if startRefusedByParent(nil, fork) {
		t.Error("no error, nothing refused")
	}
}
