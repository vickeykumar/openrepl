package appstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The install strategy (LLD 18, section 12): a solve takes at most SolveLimit; when the gateway cannot solve an app for every CPU type it installs
// the best version for its own CPU type and every other node does the same for its own; a failure is permanent (retrying cannot help) or
// transient (it may pass: the network, the disk, a mirror that is down) and only a transient one is tried again, a few times, then given up.

// SolveLimit is the most that finding out what to install may take, for one app, whatever the manager: a solver that has not answered by then is
// not going to (a slow one only makes every install behind it wait).
var SolveLimit = 60 * time.Second // a variable for tests

// NoProgress is how long an install may go without any sign of life (output, new files in the store) before it is stopped, so that a hung one
// does not hold the install lock for the whole of its timeout.
var NoProgress = 10 * time.Minute

// watchTick is how often the watchdog looks (a variable for tests).
var watchTick = 30 * time.Second

var errNoProgress = errors.New("the install made no progress for 10 minutes and was stopped")

// singleCPUKey asks pixi to solve for this node's CPU type only (the best version that exists for it) instead of for every CPU type of the lock.
type singleCPUKey struct{}

// WithSingleCPU marks a context: the install is for this node's CPU type only (no lock for the others).
func WithSingleCPU(ctx context.Context) context.Context {
	return context.WithValue(ctx, singleCPUKey{}, true)
}

func singleCPU(ctx context.Context) bool { v, _ := ctx.Value(singleCPUKey{}).(bool); return v }

// permanentMarks are what the managers say when what was asked cannot be had at all (not for this CPU, not in this archive, not for this runtime).
var permanentMarks = []string{
	"no candidates were found", "cannot solve the request", "could not solve", "no recent version", "is not a package", "is not there",
	"is part of the image already", "needs a newer version", "no version of", "is not in the bundle", "not available on this cpu",
	"version '", "was not found", "has no ", "is not a usable version", "does not build",
}

// IsPermanent says whether a failure is one that trying again cannot cure. A timeout, the network and the disk are not: they are tried again.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errNoProgress) {
		return false
	}
	m := strings.ToLower(err.Error())
	for _, t := range []string{"timed out", "timeout", "deadline exceeded", "connection", "could not be reached", "temporary failure", "no space left", "i/o timeout", "tls", "eof", "reset by peer", "503", "502", "504", "no route"} {
		if strings.Contains(m, t) {
			return false
		}
	}
	for _, t := range permanentMarks {
		if strings.Contains(m, t) {
			return true
		}
	}
	return false
}

// workLog is where an install writes its output; it also remembers when it last wrote (a sign of life for the watchdog).
type workLog struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	last time.Time
}

func (w *workLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = time.Now()
	if w.buf.Len() > 256<<10 {
		w.buf.Next(w.buf.Len() - 128<<10) // keep the end
	}
	return w.buf.Write(p)
}

func (w *workLog) Reset() { w.mu.Lock(); w.buf.Reset(); w.last = time.Now(); w.mu.Unlock() }

func (w *workLog) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }

func (w *workLog) lastWrite() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// watch stops the work (cancel with errNoProgress) when, for NoProgress, there was no output and the folders did not grow. Call the returned
// function when the work is done.
func watch(cancel context.CancelCauseFunc, wl *workLog, dirs ...string) (stop func()) {
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(watchTick)
		defer tick.Stop()
		var size int64 = -1
		moved := time.Now()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				var sum int64
				for _, d := range dirs {
					sum += DirSize(d)
				}
				if sum != size || wl.lastWrite().After(moved) {
					size, moved = sum, time.Now()
					continue
				}
				if time.Since(moved) > NoProgress {
					cancel(errNoProgress)
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// watchDirs are the folders whose growth shows that an install is alive.
func watchDirs(storeDir string) []string {
	return []string{filepath.Join(storeDir, "apps"), filepath.Join(storeDir, "pkgs"), filepath.Join(storeDir, "tmp"), filepath.Join(storeDir, "npm-cache"), filepath.Join(storeDir, "apt")}
}

// condaKind says whether pixi makes an app of this kind: only those have a lock for every CPU type.
func condaKind(kind string) bool {
	switch cleanKind(kind) {
	case KindLib, KindTool, KindLang, KindPy27:
		return true
	}
	return false
}

// installedVersion is the version of the app that a pixi bundle really holds (from its lock, for this CPU type), "" when it cannot be told.
func installedVersion(dest, name string) string {
	l, ok := ReadLock(dest)
	if !ok || l.Manager != "" {
		return ""
	}
	pk, err := LockPackages([]byte(l.Lock), CondaPlatform())
	if err != nil {
		return ""
	}
	name = strings.TrimPrefix(name, "py27-")
	for _, p := range pk {
		if p.Name == name && ValidVersion(p.Version) {
			return p.Version
		}
	}
	return ""
}

func removeLockFiles(dir string) {
	for _, f := range []string{lockFile, manifestFile, aptLockFile, aptManifestFile} {
		os.Remove(filepath.Join(dir, f))
	}
}

// solveTimedOut says whether a solve ran out of its time (a smaller solve, for one CPU type, may still finish).
func solveTimedOut(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(m, "deadline exceeded") || strings.Contains(m, "timed out") || strings.Contains(m, "signal: killed")
}

// The version ladder (LLD 18, section 12): when the newest version of an app installs but does not work (it fails its check, or its download fails),
// the install tries the newest version OLDER than the one that failed, a few times, so that the best version that works is what the user gets, with
// no one having to read an error. A version is asked for as "<1.2.3" (the newest below it): every manager answers that from its own list.

// MaxLadder is how many older versions an install tries after the first (a variable for tests).
var MaxLadder = 5

// upperBound reads "<1.2.3": the version below which the newest is wanted.
func upperBound(want string) (string, bool) {
	if strings.HasPrefix(want, "<") && ValidVersion(want[1:]) {
		return want[1:], true
	}
	return "", false
}

// validWant says whether a wanted version is a version or an upper bound.
func validWant(want string) bool {
	if want == "" || ValidVersion(want) {
		return true
	}
	_, ok := upperBound(want)
	return ok
}

// olderThan keeps, of the versions (newest first), the ones below bound; one that cannot be read as a number is kept out.
func olderThan(vs []string, bound string) []string {
	b, ok := parseVersion(bound)
	if !ok {
		return vs
	}
	var out []string
	for _, v := range vs {
		if x, ok := parseVersion(v); ok && x.cmp(b) < 0 {
			out = append(out, v)
		}
	}
	return out
}

// ladderOK says whether an install of this manager and kind may be tried again with an older version (apt has one version in an archive; a plugin one).
func ladderOK(manager, kind string) bool {
	return manager != MgrApt && cleanKind(kind) != KindPlugin
}
