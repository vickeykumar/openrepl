package utils

import (
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"
	"time"
)

// A guest's folder is removed an hour after their last visit by a job (jobscheduler.go). A job
// lives in memory, and the file that keeps the waiting ones is saved every minute, so a crash or a
// forced stop can still lose a few: the folders those jobs were for would stay for ever. This sweep
// finds them: a guest folder that nothing is waiting to remove and that nothing was written in for a
// good while longer than the deadline.

// GuestFolderPrefix starts the name of every guest's folder (user.GetHomeDir, trusted.HomeDir).
const GuestFolderPrefix = "guest-"

// GuestSweepInterval is how often StartGuestSweeper looks, and GuestSweepAge how long a folder with
// no job has to stand untouched (the deadline and half an hour more) before it is removed.
const (
	GuestSweepInterval = 15 * time.Minute
	GuestSweepAge      = DEADLINE_MINUTES*time.Minute + 30*time.Minute
	// the first look comes a little after the start, when whatever guards the folders is in place
	guestSweepFirst = 2 * time.Minute
)

// SweepGuestHomes removes the guest folders of root that have no job waiting (pending is asked with the
// job's name), whose newest file is older than maxAge, and returns how many it removed. remove is what
// deletes one (RemoveDir, which asks RemoveDirGuard first).
func SweepGuestHomes(root string, pending func(job string) bool, maxAge time.Duration, now time.Time, remove func(dir string)) int {
	entries, err := ioutil.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), GuestFolderPrefix) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if pending(REMOVE_JOB_KEY+dir) || now.Sub(newest(dir)) < maxAge {
			continue
		}
		remove(dir)
		removed++
	}
	return removed
}

// StartGuestSweeper looks for forgotten guest folders a little after the start and then every
// GuestSweepInterval. It returns what stops it.
func StartGuestSweeper() (stop func()) {
	done := make(chan struct{})
	report := Jobs.Register("guest-sweep", "Forgotten guest folders", "Removes a guest folder that no removal waits for and in which nothing was written for 90 minutes (a crash can lose the waiting removals).", GuestSweepInterval)
	sweep := func() {
		report.Run(func() (string, error) {
			n := SweepGuestHomes(filepath.Clean(HOME_DIR), GottyJobs.Pending, GuestSweepAge, time.Now(), RemoveDir)
			if n > 0 {
				log.Printf("INFO: %d forgotten guest folders were removed from %s", n, HOME_DIR)
			}
			return fmt.Sprintf("%d forgotten folders removed", n), nil
		})
	}
	go func() {
		wait := time.NewTimer(guestSweepFirst)
		defer wait.Stop()
		select {
		case <-wait.C:
		case <-done:
			return
		}
		sweep()
		ticker := time.NewTicker(GuestSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sweep()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
