package utils

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// /tmp is shared by every REPL (the filesystem sandbox leaves it open, LLD 14)
// and nothing cleans it in a container, so a program can fill the disk of the
// server with it. When OPENREPL_TMP_MAX_AGE is set (the Docker image sets it)
// the server deletes what has not been touched for that long.

// TmpCleanInterval is how often StartTmpCleaner looks.
const TmpCleanInterval = 10 * time.Minute

// names in /tmp that are never deleted: the Go build cache that every REPL shares
var tmpKept = map[string]bool{"go_cache": true}

// StartTmpCleaner cleans /tmp every TmpCleanInterval, if OPENREPL_TMP_MAX_AGE is set.
func StartTmpCleaner() {
	maxAge := TmpMaxAge()
	if maxAge <= 0 {
		Jobs.RegisterOff("tmp-clean", "/tmp cleaner", "Deletes what has not been touched in /tmp for a while, so that a program cannot fill the disk.", EnvTmpMaxAge+" is not set")
		return
	}
	report := Jobs.Register("tmp-clean", "/tmp cleaner", "Deletes what has not been touched in /tmp for a while, so that a program cannot fill the disk.", TmpCleanInterval)
	log.Printf("INFO: /tmp is cleaned every %v of what is older than %v (%s)", TmpCleanInterval, maxAge, EnvTmpMaxAge)
	go func() {
		for range time.Tick(TmpCleanInterval) {
			report.Run(func() (string, error) {
				n := CleanDir(os.TempDir(), maxAge, time.Now(), tmpKeep())
				if n > 0 {
					log.Printf("INFO: removed %d old entries from %s", n, os.TempDir())
				}
				return fmt.Sprintf("%d old entries removed", n), nil
			})
		}
	}()
}

// tmpKeep keeps the names above and the folder of the workspaces if it is in /tmp.
func tmpKeep() func(path string) bool {
	home := filepath.Clean(HOME_DIR)
	return func(path string) bool {
		path = filepath.Clean(path)
		return tmpKept[filepath.Base(path)] || path == home || strings.HasPrefix(home, path+"/")
	}
}

// newest is the latest modification time in a file or in everything below a folder.
// A folder's own time changes only when an entry is added or removed, so a folder
// that something is still working in is told by what is inside it.
func newest(path string) time.Time {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	latest := fi.ModTime()
	if !fi.IsDir() {
		return latest
	}
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return latest
	}
	for _, e := range entries {
		if t := newest(filepath.Join(path, e.Name())); t.After(latest) {
			latest = t
		}
	}
	return latest
}

// CleanDir deletes the entries of dir whose newest file is older than maxAge,
// except those keep names, and returns how many it deleted. dir itself stays.
func CleanDir(dir string, maxAge time.Duration, now time.Time, keep func(path string) bool) int {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if keep != nil && keep(path) {
			continue
		}
		if now.Sub(newest(path)) < maxAge {
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			removed++
		}
	}
	return removed
}
