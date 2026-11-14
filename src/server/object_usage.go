package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"objstore"
)

// How much the object store holds, counted from its own listing (objstore.Client.Usage) for the live map of the dashboard. A count reads
// every page of the listing, so it is kept for half an hour and made in the background: the map shows "counting" until the first one
// is done, and the last count while a new one is made.

const (
	objectUsageTTL     = 30 * time.Minute
	objectUsageTimeout = 2 * time.Minute
)

var objectUsage struct {
	mu      sync.Mutex
	usage   objstore.Usage
	at      time.Time
	err     string
	has     bool
	running bool
	key     string
}

// objectUsageRead says what is known of the usage, and starts a count when there is none or it is old.
func objectUsageRead() (u objstore.Usage, at time.Time, errText string, has bool) {
	key := objectStorageSettingsKey()
	objectUsage.mu.Lock()
	defer objectUsage.mu.Unlock()
	if objectUsage.key != key { // another bucket or other keys: the old count is not of this store
		objectUsage.has, objectUsage.err, objectUsage.at = false, "", time.Time{}
		objectUsage.key = key
	}
	if !objectUsage.running && (!objectUsage.has || time.Since(objectUsage.at) >= objectUsageTTL) {
		objectUsage.running = true
		go countObjectUsage(key)
	}
	return objectUsage.usage, objectUsage.at, objectUsage.err, objectUsage.has
}

func countObjectUsage(key string) {
	var u objstore.Usage
	c, err := objstore.Default()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), objectUsageTimeout)
		u, err = c.Usage(ctx)
		cancel()
	}
	objectUsage.mu.Lock()
	defer objectUsage.mu.Unlock()
	objectUsage.running = false
	if objectUsage.key != key {
		return
	}
	objectUsage.at = time.Now()
	if err != nil {
		objectUsage.err = redactErr(err).Error()
		return // an older count, if there is one, stays
	}
	objectUsage.usage, objectUsage.has, objectUsage.err = u, true, ""
}

// humanSize writes a number of bytes as KB, MB or GB with one decimal.
func humanSize(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f GB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f KB", f/k)
	}
	return fmt.Sprintf("%d B", n)
}

func groupDigits(n int) string {
	s := fmt.Sprint(n)
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, out...), ",")
}

// objectUsageText is the line of the map for the usage: the size in the bucket, the number of objects, and what older versions add.
func objectUsageText(u objstore.Usage) string {
	files := "objects"
	if u.Objects == 1 {
		files = "object"
	}
	text := fmt.Sprintf("%s in %s %s", humanSize(u.Bytes), groupDigits(u.Objects), files)
	if u.VersionsKnown && u.OlderBytes > 0 {
		text += fmt.Sprintf(", and %s in %s older versions that the bucket keeps", humanSize(u.OlderBytes), groupDigits(u.OlderVersions))
	}
	if u.Truncated {
		text += " (at least: the bucket is too large to read to the end)"
	}
	return text
}

// objectUsageShort is the size alone, for the one line under the box.
func objectUsageShort(u objstore.Usage) string {
	total := u.Bytes + u.OlderBytes
	if u.Truncated {
		return "at least " + humanSize(total)
	}
	return humanSize(total)
}
