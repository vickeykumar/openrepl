package ideusage

import (
	"errors"
	"sync"
	"time"
)

// ErrTooFast is returned when a terminal kept sending faster than the cap allows for longer than the grace time.
var ErrTooFast = errors.New("terminal output too fast")

// Rate is the speed cap of one terminal: at most `cap` bytes in any window. It keeps the bytes of the window in one-second
// buckets. Over the cap, Wait pauses the sender (the program waits, nothing more is sent); it gives up after the grace time.
type Rate struct {
	mu      sync.Mutex
	now     func() time.Time
	sleep   func(time.Duration)
	buckets map[int64]int64 // second -> bytes
}

// NewRate is a cap with no bytes sent yet.
func NewRate() *Rate {
	return &Rate{now: time.Now, sleep: time.Sleep, buckets: map[int64]int64{}}
}

func (r *Rate) used(window time.Duration) int64 {
	now := r.now().Unix()
	secs := int64(window / time.Second)
	if secs < 1 {
		secs = 1
	}
	var sum int64
	for s, n := range r.buckets {
		if s <= now-secs {
			delete(r.buckets, s)
			continue
		}
		sum += n
	}
	return sum
}

// Wait lets n bytes through when the window has room, pauses until it does, and returns ErrTooFast when the pause would pass grace.
// A frame bigger than the whole cap goes through alone, when the window is empty.
func (r *Rate) Wait(n int64, cap int64, window, grace time.Duration) error {
	if cap <= 0 {
		return nil
	}
	start := r.now()
	for {
		r.mu.Lock()
		u := r.used(window)
		if u+n <= cap || u == 0 {
			r.buckets[r.now().Unix()] += n
			r.mu.Unlock()
			return nil
		}
		r.mu.Unlock()
		if r.now().Sub(start) >= grace {
			return ErrTooFast
		}
		r.sleep(50 * time.Millisecond)
	}
}
