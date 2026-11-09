package utils

import (
	"sync"
	"time"
)

// ScanCache keeps the result of a scan of a whole database collection (the list of accounts, of
// feedback, of blog posts). A scan of a remote database such as Firestore costs a read for every
// record it returns, and the dashboard asks for the same lists every few seconds, so without
// this a page left open uses up the daily allowance. The result is kept for TTL; a change this
// server makes can drop it at once (Invalidate), though a rescan then waits at least MinInterval
// since the last one, so a stream of changes does not become a stream of scans; and an admin who
// presses Refresh can force one (fresh), at most once every MinInterval too.
type ScanCache[T any] struct {
	TTL         time.Duration
	MinInterval time.Duration
	// Now is the clock; time.Now when nil (tests move it)
	Now func() time.Time

	mu     sync.Mutex
	value  T
	has    bool
	stale  bool
	loaded time.Time
	scans  int
	source interface{}
	now_   bool // a change an admin made: the next Get reads again whatever MinInterval says
}

func (c *ScanCache[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the cached result, or runs load when there is none, it is older than TTL, it was
// dropped, or fresh asks for a new one. A reload never happens within MinInterval of the last
// one, except for the first. If load fails and there is an older result, that is returned along
// with the error, so a list does not go blank while the database is struggling.
func (c *ScanCache[T]) Get(fresh bool, load func() (T, error)) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	age := now.Sub(c.loaded)
	reload := !c.has || age >= c.TTL || c.now_ || ((c.stale || fresh) && age >= c.MinInterval)
	if !reload {
		return c.value, nil
	}
	v, err := load()
	c.scans++
	if err != nil {
		if c.has {
			return c.value, err
		}
		return v, err
	}
	c.value, c.has, c.stale, c.now_, c.loaded = v, true, false, false, now
	return v, nil
}

// GetFor is Get for a result read from source (a database handle): when the source is not the
// one the kept result came from (the database was replaced), the kept result is forgotten first.
func (c *ScanCache[T]) GetFor(source interface{}, fresh bool, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if c.has && c.source != source {
		var zero T
		c.value, c.has, c.stale, c.loaded = zero, false, false, time.Time{}
	}
	c.source = source
	c.mu.Unlock()
	return c.Get(fresh, load)
}

// Invalidate says that the data changed: the next Get reloads, as soon as MinInterval allows.
func (c *ScanCache[T]) Invalidate() {
	c.mu.Lock()
	c.stale = true
	c.mu.Unlock()
}

// InvalidateNow is Invalidate for a change an admin just made and will look at: the next Get reads
// again at once, without waiting for MinInterval. Admins change things rarely.
func (c *ScanCache[T]) InvalidateNow() {
	c.mu.Lock()
	c.stale, c.now_ = true, true
	c.mu.Unlock()
}

// Reset forgets the result altogether, whatever MinInterval says. For tests and for a database
// that was replaced.
func (c *ScanCache[T]) Reset() {
	c.mu.Lock()
	var zero T
	c.value, c.has, c.stale, c.now_, c.loaded = zero, false, false, false, time.Time{}
	c.mu.Unlock()
}

// Scans is how many times load has run, for tests.
func (c *ScanCache[T]) Scans() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scans
}
