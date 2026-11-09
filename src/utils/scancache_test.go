package utils

import (
	"errors"
	"testing"
	"time"
)

func TestScanCacheKeepsAResultForTheTTLAndScansOnce(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	c := &ScanCache[int]{TTL: 5 * time.Minute, MinInterval: 30 * time.Second, Now: func() time.Time { return now }}
	n := 0
	load := func() (int, error) { n++; return n, nil }
	for i := 0; i < 100; i++ {
		if v, _ := c.Get(false, load); v != 1 {
			t.Fatalf("call %d returned %d", i, v)
		}
	}
	if c.Scans() != 1 {
		t.Errorf("scans = %d, want 1", c.Scans())
	}
	now = now.Add(5 * time.Minute)
	if v, _ := c.Get(false, load); v != 2 {
		t.Errorf("after the TTL: %d", v)
	}
}

func TestScanCacheInvalidateAndFreshWaitForTheMinimumInterval(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	c := &ScanCache[int]{TTL: 5 * time.Minute, MinInterval: 30 * time.Second, Now: func() time.Time { return now }}
	n := 0
	load := func() (int, error) { n++; return n, nil }
	c.Get(false, load)
	c.Invalidate()
	now = now.Add(10 * time.Second)
	if v, _ := c.Get(false, load); v != 1 {
		t.Errorf("a change 10 s after a scan must not scan again yet: %d", v)
	}
	if v, _ := c.Get(true, load); v != 1 {
		t.Errorf("Refresh 10 s after a scan must not scan again yet: %d", v)
	}
	now = now.Add(25 * time.Second)
	if v, _ := c.Get(false, load); v != 2 {
		t.Errorf("a change is shown once the interval has passed: %d", v)
	}
	// fresh without a change, after the interval
	now = now.Add(31 * time.Second)
	if v, _ := c.Get(true, load); v != 3 {
		t.Errorf("Refresh after the interval scans: %d", v)
	}
	// Reset forgets at once
	c.Reset()
	if v, _ := c.Get(false, load); v != 4 {
		t.Errorf("after Reset: %d", v)
	}
}

func TestScanCacheKeepsTheOldListWhenTheDatabaseFails(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	c := &ScanCache[[]string]{TTL: time.Minute, MinInterval: time.Second, Now: func() time.Time { return now }}
	c.Get(false, func() ([]string, error) { return []string{"a"}, nil })
	now = now.Add(2 * time.Minute)
	v, err := c.Get(false, func() ([]string, error) { return nil, errors.New("429") })
	if err == nil || len(v) != 1 || v[0] != "a" {
		t.Errorf("got %v, %v: want the old list and the error", v, err)
	}
}

func TestScanCacheInvalidateNowDoesNotWaitForTheMinimumInterval(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	c := &ScanCache[int]{TTL: 5 * time.Minute, MinInterval: time.Minute, Now: func() time.Time { return now }}
	n := 0
	load := func() (int, error) { n++; return n, nil }
	c.Get(false, load)
	now = now.Add(2 * time.Second)
	c.InvalidateNow() // an admin blocked an account and is looking at the list
	if v, _ := c.Get(false, load); v != 2 {
		t.Errorf("a change an admin made must show at once: %d", v)
	}
	if v, _ := c.Get(false, load); v != 2 {
		t.Errorf("and only once: %d", v)
	}
}
